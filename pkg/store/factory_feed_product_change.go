package store

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
)

// PostProductChange is a read projection of the explicit review handoff.
// Accepted describes product scope only, not generation, verification or
// release; the build request it opens shows those.
//
// PostProductStateUnavailable names an explicit unknown projection: the
// comment sits on the same-product lane but the read behind product_change
// failed.
const PostProductStateUnavailable = "unavailable"

// UnavailablePostProductChange projects an explicit unknown for a
// same-product comment whose read failed.
func UnavailablePostProductChange(intent, acceptedRevision string) *PostProductChange {
	return &PostProductChange{Intent: intent, AcceptedRevision: acceptedRevision, State: PostProductStateUnavailable}
}

type PostProductChange struct {
	Intent           string `json:"intent"`
	AcceptedRevision string `json:"accepted_revision"`
	DraftRevision    string `json:"draft_revision,omitempty"`
	State            string `json:"state"`
	CanReview        bool   `json:"can_review"`
}

type postProductBinding struct {
	Revision int64  `json:"revision"`
	Draft    string `json:"draft"`
}

// acceptedPostProduct checks an immutable canonical source. It does not
// guess lineage from a headline, issue similarity or repository membership.
func (s *Store) acceptedPostProduct(ctx context.Context, repository int64, intent, revision string) (*factory.PostProductSource, error) {
	if intent == "" || revision == "" {
		return nil, nil
	}
	var matches bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_intent_revisions WHERE id=$1 AND intent=$2 AND data->>'repository'=$3 AND data->>'state'='accepted')`, revision, intent, strconv.FormatInt(repository, 10)).Scan(&matches)
	if err != nil || !matches {
		return nil, err
	}
	return &factory.PostProductSource{Intent: intent, AcceptedRevision: revision}, nil
}

func (s *Store) assignmentPostProduct(ctx context.Context, a factory.Assignment) (*factory.PostProductSource, error) {
	if a.Subject.Delivery != nil {
		return s.acceptedPostProduct(ctx, a.Repository, a.Subject.Delivery.IntentID, a.Subject.Delivery.AcceptedRevision)
	}
	if a.Subject.Intent != nil {
		return s.acceptedPostProduct(ctx, a.Repository, a.Subject.Intent.IntentID, a.Subject.Intent.RevisionID)
	}
	return nil, nil
}

func (s *Store) postPostProduct(ctx context.Context, detail FeedPostDetail) (*factory.PostProductSource, error) {
	if detail.Payload != nil && detail.Payload.Communication != nil {
		if source := detail.Payload.Communication.IntentSource; source != nil && source.State == factory.IntentAccepted {
			return s.acceptedPostProduct(ctx, detail.Record.Repository, source.Intent, source.Revision)
		}
	}
	if detail.Source != nil && detail.Source.Product != nil {
		p := detail.Source.Product
		return s.acceptedPostProduct(ctx, detail.Record.Repository, p.Intent, p.AcceptedRevision)
	}
	if detail.Assignment != "" {
		a, err := s.Assignment(ctx, detail.Assignment)
		if err != nil {
			return nil, err
		}
		if a.Repository == detail.Record.Repository {
			return s.assignmentPostProduct(ctx, a)
		}
	}
	return nil, nil
}

func (s *Store) PostProductChange(ctx context.Context, detail FeedPostDetail, pipe factory.PostIntake, actor int64) (*PostProductChange, error) {
	if detail.Source == nil || detail.Source.Product == nil || pipe.Judgment != factory.PostJudgmentApplied || pipe.Route != factory.PostRouteNewIssue {
		return nil, nil
	}
	p := detail.Source.Product
	view := &PostProductChange{Intent: p.Intent, AcceptedRevision: p.AcceptedRevision, State: "superseded"}
	var raw, headRaw []byte
	var binding postProductBinding
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(c.data->'product_change','{}'::jsonb),r.data FROM factory_feed_posts c JOIN factory_intent_heads h ON h.id=$2 JOIN factory_intent_revisions r ON r.id=h.revision WHERE c.id=$1`, detail.Record.ID, p.Intent).Scan(&raw, &headRaw)
	if err != nil {
		return nil, err
	}
	var head factory.IntentRevision
	if err = json.Unmarshal(raw, &binding); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(headRaw, &head); err != nil {
		return nil, err
	}
	if head.Repository != detail.Record.Repository {
		return nil, ErrNotFound
	}
	if binding.Draft != "" {
		view.DraftRevision = binding.Draft
	}
	if detail.Record.Deleted || detail.Record.Revoked || binding.Draft != "" && binding.Revision != detail.Record.Revision {
		return view, nil
	}
	if binding.Draft == "" {
		if head.ID == p.AcceptedRevision && head.State == factory.IntentAccepted && pipe.Issue == 0 && pipe.Draft == factory.PostDraftPending && pipe.Effect == factory.PostEffectPending {
			view.State = "review_required"
			view.CanReview = head.Owner == actor
		}
		return view, nil
	}
	var descended bool
	err = s.db.QueryRowContext(ctx, `WITH RECURSIVE ancestry AS (SELECT id,data FROM factory_intent_revisions WHERE id=$1 AND intent=$3 UNION ALL SELECT r.id,r.data FROM factory_intent_revisions r JOIN ancestry a ON r.id=a.data->>'predecessor' WHERE r.intent=$3) SELECT EXISTS(SELECT 1 FROM ancestry WHERE id=$2)`, head.ID, binding.Draft, p.Intent).Scan(&descended)
	if err != nil {
		return nil, err
	}
	if descended && head.State == factory.IntentAccepted && effectivePostProductIdea(head, detail) {
		view.State = "accepted"
		// Only the head owner may open the private build request behind
		// the accepted direction; this bit authorizes navigation only.
		view.CanReview = head.Owner == actor
	} else if descended && head.State == factory.IntentDraft {
		view.State = "draft"
		view.CanReview = head.Owner == actor
	}
	if pipe.Issue != 0 {
		// The same-product lane never creates a native issue: a pipeline
		// that left it no longer speaks for the accepted direction.
		view.State = "superseded"
		view.CanReview = false
	}
	return view, nil
}

func revisionAdoptsPostProductIdea(revision factory.IntentRevision, idea string) bool {
	decided := false
	for _, item := range revision.Ideas {
		if item.ID != idea || item.Decision != "accept" && item.Decision != "edit" {
			continue
		}
		decided = true
		break
	}
	if !decided {
		return false
	}
	if len(revision.EffectiveOutcomes) == 0 {
		return true
	}
	for _, outcome := range revision.EffectiveOutcomes {
		if outcome == idea {
			return true
		}
	}
	return false
}

func effectivePostProductIdea(head factory.IntentRevision, detail FeedPostDetail) bool {
	return revisionAdoptsPostProductIdea(head, factory.PostDecisionID("product-idea", detail.Record.ID, detail.Record.Revision))
}
