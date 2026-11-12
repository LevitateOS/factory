package runtime

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

var errNativeRead = errors.New("native collaboration observation unavailable")
var errNativeReadScope = errors.New("native collaboration resource outside scope")

// These are decoded, credential-free native facts. Rust owns the defining
// provider selectors and provider decoding. Go owns canonical IDs/projection.
type nativeRead struct {
	GrantID              string                      `json:"grant_id,omitempty"`
	Paths                []string                    `json:"paths,omitempty"`
	Creator              string                      `json:"creator,omitempty"`
	Cursor               string                      `json:"cursor,omitempty"`
	Comments             []string                    `json:"comments,omitempty"`
	TargetRef            string                      `json:"target_ref,omitempty"`
	ReviewerKind         string                      `json:"reviewer_kind,omitempty"`
	WritePull            *bool                       `json:"pull,omitempty"`
	CommentID            string                      `json:"comment_id,omitempty"`
	CommentNodeID        string                      `json:"comment_node_id,omitempty"`
	Anchor               *factory.ReviewThreadAnchor `json:"anchor,omitempty"`
	Name                 string                      `json:"name,omitempty"`
	Commit               string                      `json:"commit,omitempty"`
	HeadRepository       string                      `json:"head_repository,omitempty"`
	ComparisonRepository string                      `json:"comparison_repository,omitempty"`
	BaseBranch           string                      `json:"base_branch,omitempty"`
	HeadBranch           string                      `json:"head_branch,omitempty"`
	Kind                 string                      `json:"kind"`
	Repository           string                      `json:"repository"`
	Number               uint32                      `json:"number,omitempty"`
	Page                 uint32                      `json:"page,omitempty"`
	Limit                uint32                      `json:"limit,omitempty"`
	State                *string                     `json:"state,omitempty"`
	Base                 *string                     `json:"base,omitempty"`
	ExpectedBase         string                      `json:"expected_base,omitempty"`
	Head                 *string                     `json:"head,omitempty"`
	Before               string                      `json:"before,omitempty"`
	ReviewID             string                      `json:"review_id,omitempty"`
	ReviewNodeID         string                      `json:"review_node_id,omitempty"`
	ThreadID             *string                     `json:"thread_id,omitempty"`
	RunID                string                      `json:"run_id,omitempty"`
	Attempt              string                      `json:"attempt,omitempty"`
	RunPage              uint32                      `json:"run_page,omitempty"`
}

func (q nativeRead) envelopeLimits() (int, int) {
	request, response := 8192, 2<<20
	if q.Kind == "inline_preparation" {
		request = 64 << 10
	}
	if q.Kind == "source_bundle" {
		response = project.MaxSourceWire
	}
	return request, response
}

type nativeReadEffect struct {
	Version uint8      `json:"version"`
	Kind    string     `json:"kind"`
	Query   nativeRead `json:"query"`
}
type nativeBranch struct {
	Repository *collaboration.ObservedRepository `json:"repository"`
	Name       string                            `json:"name"`
	Commit     *collaboration.CommitID           `json:"commit"`
}
type nativePull struct {
	collaboration.ObservedIssue
	Draft          *bool         `json:"draft"`
	Base           *nativeBranch `json:"base"`
	Head           *nativeBranch `json:"head"`
	Merged         *bool         `json:"merged"`
	MergedAt       *time.Time    `json:"merged_at"`
	Mergeable      *bool         `json:"mergeable"`
	MergeableState *string       `json:"mergeable_state"`
	Rebaseable     *bool         `json:"rebaseable"`
}
type nativeComment struct {
	ExternalID      string                       `json:"external_id"`
	Author          *collaboration.ObservedActor `json:"author"`
	Body            string                       `json:"body"`
	CreatedAt       *time.Time                   `json:"created_at"`
	UpdatedAt       *time.Time                   `json:"updated_at"`
	MinimizedReason *string                      `json:"minimized_reason"`
}
type nativeReview struct {
	ExternalID    string                       `json:"external_id"`
	Author        *collaboration.ObservedActor `json:"author"`
	Body          string                       `json:"body"`
	State         string                       `json:"state"`
	ProviderState string                       `json:"provider_state"`
	Commit        *collaboration.CommitID      `json:"commit"`
	SubmittedAt   *time.Time                   `json:"submitted_at"`
	Official      *bool                        `json:"official,omitempty"`
	Stale         *bool                        `json:"stale,omitempty"`
}
type nativeReviewComment struct {
	ExternalID        string                       `json:"external_id"`
	Author            *collaboration.ObservedActor `json:"author"`
	CreatedAt         *time.Time                   `json:"created_at"`
	UpdatedAt         *time.Time                   `json:"updated_at"`
	ReviewID          *string                      `json:"review_id"`
	ReplyTo           *string                      `json:"reply_to"`
	Body              *string                      `json:"body"`
	Path              *string                      `json:"path"`
	DiffHunk          *string                      `json:"diff_hunk"`
	Side              *string                      `json:"side"`
	StartSide         *string                      `json:"start_side"`
	SubjectType       *string                      `json:"subject_type"`
	AuthorAssociation *string                      `json:"author_association"`
	HTMLURL           *string                      `json:"html_url"`
	Commit            *collaboration.CommitID      `json:"commit"`
	OriginalCommit    *collaboration.CommitID      `json:"original_commit"`
	Line              *int64                       `json:"line"`
	StartLine         *int64                       `json:"start_line"`
	OriginalLine      *int64                       `json:"original_line"`
	OriginalStartLine *int64                       `json:"original_start_line"`
	Position          *int64                       `json:"position"`
	OriginalPosition  *int64                       `json:"original_position"`
}

func nativeIndex(number int64) bool { return number > 0 && number <= math.MaxUint32 }
func (b *nativeProductAccount) readScope(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
	if actor != b.binding.Actor.ID || b.repository == nil || b.repository.ID != repository {
		return errNativeReadScope
	}
	ref, err := b.store.CollaborationRepositoryOnHost(ctx, b.binding.Host.ID, repository)
	if errors.Is(err, sql.ErrNoRows) {
		return errNativeReadScope
	}
	if err != nil {
		return err
	}
	if ref.ExternalID != b.identity.SelectedRepository {
		return errNativeReadScope
	}
	return ctx.Err()
}
func (b *nativeProductAccount) read(ctx context.Context, query nativeRead, body any) error {
	identity, err := b.port.read(ctx, query, body)
	if err != nil {
		return err
	}
	if !sameNativeAccount(b.identity, identity) {
		return errNativeRead
	}
	return ctx.Err()
}

// decodeNativeRead accepts the versioned native contract and the frozen prior
// envelope. Neither encoding is command authority or a retained send receipt.
func decodeNativeRead(raw json.RawMessage, kind string, body any) (collaboration.ObservedActor, error) {
	var account collaboration.ObservedActor
	var fields map[string]json.RawMessage
	if nativeDecode(raw, &fields) != nil {
		return account, errNativeRead
	}
	var payload json.RawMessage
	if _, versioned := fields["version"]; versioned {
		var envelope struct {
			Version uint8                       `json:"version"`
			Kind    string                      `json:"kind"`
			Account collaboration.ObservedActor `json:"account"`
			Payload json.RawMessage             `json:"payload"`
		}
		if nativeDecode(raw, &envelope) != nil || envelope.Version != 1 || envelope.Kind != kind {
			return account, errNativeRead
		}
		account, payload = envelope.Account, envelope.Payload
	} else {
		// Known prior kinds only: new operations use version1's single payload.
		field := map[string]string{"source_bundle": "publication", "merge_completion": "publication", "publication_refs": "publication", "pull_required_checks": "required_checks", "automation_review": "automation_review", "delivery_opinions": "delivery_opinions", "automation_checks": "automation_checks", "automation_inputs": "inputs", "reviewer_lookup": "reviewer_lookup", "write_capabilities": "capabilities", "inline_preservation": "preservation", "inline_preparation": "preparation", "branch_creation": "preparation", "merge_preparation": "preparation", "pull_review_publication": "publication", "pull_pending_review_snapshot": "preparation", "pull_review_preparation": "preparation", "pull_branches": "branches", "issues": "issues", "issue": "issues", "pulls": "pulls", "pull": "pulls",
			"issue_comments": "discussion", "pull_comments": "discussion", "pull_review_comment": "review_comments", "pull_review_comments": "review_comments", "pull_reviews": "reviews", "pull_review": "reviews", "pull_threads": "threads", "pull_review_requests": "review_requests", "pull_files": "files", "pull_commits": "commits", "pull_check_runs": "checks", "pull_commit_statuses": "statuses", "workflow_runs": "workflow_runs", "workflow_run_jobs": "jobs", "pull_workflow_jobs": "jobs", "pull_protection": "protection", "target_tip": "publication", "setup_source": "publication", "issue_creator_permission": "capabilities", "operation_grant": "capabilities", "external_pull_candidates": "external_pull_candidates", "merge_readiness": "merge_readiness"}[kind]
		var observed string
		if field == "" || len(fields) != 3 || nativeDecode(fields["kind"], &observed) != nil || observed != kind || nativeDecode(fields["account"], &account) != nil {
			return account, errNativeRead
		}
		payload = fields[field]
	}
	if !nativeActor(account) || len(payload) == 0 || string(payload) == "null" || nativeDecode(payload, body) != nil {
		return collaboration.ObservedActor{}, errNativeRead
	}
	return account, nil
}

func (p *NativeCollaboration) read(ctx context.Context, query nativeRead, body any) (NativeIdentity, error) {
	var raw json.RawMessage
	var effect any = nativeReadEffect{Kind: "read", Version: 1, Query: query}
	if query.Kind == "inline_preparation" && query.Anchor != nil {
		effect = nativeInlinePreparationEffect{Kind: "read", Version: 1, Query: nativeInlinePreparationQuery{Kind: query.Kind, Repository: query.Repository, Number: query.Number, Anchor: *query.Anchor}}
	}
	scope, err := p.call(ctx, effect, &raw)
	if err != nil {
		return NativeIdentity{}, err
	}
	account, err := decodeNativeRead(raw, query.Kind, body)
	if err != nil || account.ExternalID != scope.Connection.Account || scope.Repository != query.Repository {
		return NativeIdentity{}, errNativeRead
	}
	return NativeIdentity{Connection: scope.Connection, Account: account, SelectedRepository: scope.Repository}, nil
}
func (b *nativeProductAccount) mapRead(ctx context.Context, remote, name string, actors []collaboration.ObservedActor, refs []collaboration.ObservedRepository) (collaboration.ConnectionObservationMapping, error) {
	if b.repository == nil || remote != b.repository.ExternalID || !nativeObservedText(name, 512, false) {
		return collaboration.ConnectionObservationMapping{}, errNativeRead
	}
	for _, actor := range actors {
		if !nativeActor(actor) {
			return collaboration.ConnectionObservationMapping{}, errNativeRead
		}
	}
	referenceNames := map[string]string{remote: name}
	for _, ref := range refs {
		if !nativeRemoteID(ref.ExternalID) || !nativeObservedText(ref.Name, 512, false) {
			return collaboration.ConnectionObservationMapping{}, errNativeRead
		}
		if previous, ok := referenceNames[ref.ExternalID]; ok && previous != ref.Name {
			return collaboration.ConnectionObservationMapping{}, errNativeRead
		}
		referenceNames[ref.ExternalID] = ref.Name
	}
	return b.store.ApplyCollaborationObservation(ctx, collaboration.ConnectionObservationBatch{Connection: nativeConnectionObservation(b.port, b.identity), Repository: b.repository.ID,
		Repositories: []collaboration.ObservedRepository{{ExternalID: remote, Name: name}}, Actors: actors, References: refs})
}
func (b *nativeProductAccount) observation(facts any) collaboration.Observation {
	var repository collaboration.RepositoryID
	if b.repository != nil {
		repository = b.repository.ID
	}
	return collaboration.Observation{State: collaboration.ReadAvailable, Revision: nativeObservationRevision(struct {
		Connection collaboration.ConnectionRef
		Repository collaboration.RepositoryID
		Facts      any
	}{b.binding.Connection, repository, facts}), ObservedAt: time.Now().UTC()}
}
func nativeActorRef(mapping collaboration.ConnectionObservationMapping, actor *collaboration.ObservedActor) *collaboration.ActorRef {
	if actor == nil {
		return nil
	}
	ref := mapping.Actors[actor.ExternalID]
	return &ref
}
func appendNativeActor(actors []collaboration.ObservedActor, actor *collaboration.ObservedActor) []collaboration.ObservedActor {
	if actor != nil {
		return append(actors, *actor)
	}
	return actors
}
func nativeBranchRef(branch *nativeBranch, mapping collaboration.ConnectionObservationMapping) collaboration.GitRef {
	var result collaboration.GitRef
	if branch == nil {
		return result
	}
	result.Name, result.Commit = branch.Name, branch.Commit
	if branch.Repository != nil {
		ref := mapping.References[branch.Repository.ExternalID]
		result.Repository = &ref
	}
	return result
}
func nativePullRef(raw collaboration.ObservedIssue, repository collaboration.RepositoryID) collaboration.PullRef {
	return collaboration.PullRef{Kind: collaboration.SubjectPull, Repository: repository, ExternalID: raw.ExternalID, Number: raw.Number}
}
func nativeIssueRef(raw collaboration.ObservedIssue, repository collaboration.RepositoryID) collaboration.IssueRef {
	return collaboration.IssueRef{Kind: collaboration.SubjectIssue, Repository: repository, ExternalID: raw.ExternalID, Number: raw.Number}
}
func nativePullFacts(raw nativePull) ([]collaboration.ObservedActor, []collaboration.ObservedRepository, error) {
	if !nativeRemoteID(raw.ExternalID) || raw.Number < 1 || !(raw.State == "open" || raw.State == "closed" || raw.State == "merged") || raw.State == "merged" && raw.Merged != nil && !*raw.Merged {
		return nil, nil, errNativeRead
	}
	actors := appendNativeActor(nil, raw.Author)
	var refs []collaboration.ObservedRepository
	for _, branch := range []*nativeBranch{raw.Base, raw.Head} {
		if branch != nil {
			if branch.Commit != nil && !collaboration.ValidCommitID(*branch.Commit) {
				return nil, nil, errNativeRead
			}
			if branch.Repository != nil {
				refs = append(refs, *branch.Repository)
			}
		}
	}
	return actors, refs, nil
}

// Opaque pagination carries its original resource, filter, pin and page size.
// It is never an authority receipt and is re-admitted on every request.
type nativeCursor struct {
	Kind       string `json:"kind"`
	Repository string `json:"repository"`
	Number     int64  `json:"number,omitempty"`
	Root       string `json:"root,omitempty"`
	State      string `json:"state,omitempty"`
	Base       string `json:"base,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Head       string `json:"head,omitempty"`
	Page       int    `json:"page"`
	Limit      int    `json:"limit"`
}

func cursorEncode(c nativeCursor, next int) string {
	if next == 0 {
		return ""
	}
	c.Page = next
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func cursorDecode(raw string, expected nativeCursor) (nativeCursor, error) {
	expected.Page = 1
	if raw == "" {
		return expected, nil
	}
	if len(raw) > 4096 {
		return nativeCursor{}, errNativeRead
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nativeCursor{}, errNativeRead
	}
	var c nativeCursor
	if nativeDecode(decoded, &c) != nil || (c.Page < 2 && !(c.Page == 1 && c.Kind == "pull_commit_statuses")) || c.Page > 1_000_000 || c.Limit < 1 || c.Limit > 50 {
		return c, errNativeRead
	}
	want := expected
	want.Page = c.Page
	// Thread catalogue previews use ten comments; continuation keeps that size
	// even when the detail reader asks for a larger upper bound.
	if c.Kind == "pull_review_threads_v1" && c.Root != "" && c.Limit <= want.Limit {
		want.Limit = c.Limit
	}
	if c.Kind == "pull_commits" && len(c.Root) > 0 && len(c.Root) <= 1024 {
		want.Root = c.Root
	}
	if c != want {
		return c, errNativeRead
	}
	return c, nil
}
func validNativePage(next, page, limit, count int, filtered bool) bool {
	return count <= limit && (next == 0 || next > page && next <= 1_000_000 && (filtered || count > 0))
}
func nativePage[T any](observation collaboration.Observation, items []T, next string) collaboration.Page[T] {
	if items == nil {
		items = []T{}
	}
	return collaboration.Page[T]{Observation: observation, Items: items, Next: next, Complete: next == ""}
}

func decimalRepository(id collaboration.RepositoryID) string { return strconv.FormatInt(int64(id), 10) }

func (b *nativeProductAccount) admitRead(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, missing error) error {
	err := b.readScope(ctx, actor, repo)
	if errors.Is(err, errNativeReadScope) {
		return missing
	}
	return err
}
