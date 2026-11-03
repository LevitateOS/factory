package factory

import "strconv"

// Contributions extend discussion access for an already admitted role, never
// execution or mutation authority. One source revision can invite one role.
const (
	ContributionProbability = 0.70
	ContributionConfidence  = 0.40
	ContributionMaxRoles    = 4
)

// These independent formulations are fixed before any answer is read. The
// starting gate was evaluated on single-source useful/redundant/ambiguous cases;
// it is a conservative policy, not a claim of universal model calibration.
var ContributionQuestions = []string{
	"Should the eligible running role add a materially useful comment to this source now? Choose contribute only when its expertise and current scope can supply a specific missing correction, risk, or evidence. Existing replies may already answer the point. Acknowledgments, generic encouragement, unrelated advice, ambiguous targets, and repeating another role's answer require no_contribution. The invitation authorizes one discussion reply, not a requirement change or new execution.",
	"Decide whether one reply from the supplied eligible role would improve this particular discussion. Select no_contribution for courtesy, repetition, uncertain relevance, already resolved questions or matters outside its admitted scope. Select contribute when the post and existing replies expose an unanswered issue this role can specifically help resolve. Do not infer new authority from the post; only a bounded comment is being considered.",
	"Evaluate the incremental value of inviting this admitted role into the given thread. Could it offer concrete missing evidence, a relevant correction, or an actionable risk within its present work? If so choose contribute; otherwise choose no_contribution. Account for prior replies, relevance and ambiguity. Do not reward activity for its own sake or invite the role merely because it exists. This is one comment, never automatic adoption of its advice.",
}

type ContributionRole struct {
	Assignment string     `json:"assignment"`
	Capability string     `json:"capability"`
	Profile    MCPProfile `json:"profile"`
	Subject    string     `json:"subject"`
	Issue      int64      `json:"issue"`
	Scope      string     `json:"scope"`
}

type ContributionReply struct {
	Actor string `json:"actor"`
	Body  string `json:"body"`
}

type ContributionReview struct {
	Source               string              `json:"source"`
	SourceKind           string              `json:"source_kind,omitempty"`
	Revision             int64               `json:"revision"`
	Repository           int64               `json:"repository"`
	Body                 string              `json:"body"`
	SourceActor          string              `json:"source_actor"`
	Context              string              `json:"context,omitempty"`
	Replies              []ContributionReply `json:"replies"`
	Roles                []ContributionRole  `json:"roles"`
	State                string              `json:"state"`
	Assignment           string              `json:"assignment,omitempty"`
	Profile              MCPProfile          `json:"profile,omitempty"`
	Calls                []ContributionCall  `json:"calls,omitempty"`
	Reply                string              `json:"reply,omitempty"`
	Probability          float64             `json:"minimum_probability"`
	Confidence           float64             `json:"minimum_confidence"`
	PolicyVersion        string              `json:"policy_version"`
	ProbabilityThreshold float64             `json:"probability_threshold"`
	ConfidenceThreshold  float64             `json:"confidence_threshold"`
	RepliesVersion       string              `json:"replies_version"`
}

type ContributionCall struct {
	Assignment string         `json:"assignment"`
	Question   string         `json:"question"`
	Answer     PriorityAnswer `json:"answer"`
}

func ContributionReviewID(source string, revision int64) string {
	return MCPInputDigest([]byte("role-contribution-v1\n" + source + "\n" + strconv.FormatInt(revision, 10)))[:32]
}

// ContributionRecordReviewID names the assessment behind one assignment-record
// source revision. Records keep their own digest prefix so a record and a
// post sharing one identity never share an invitation; post identities are
// preserved byte-for-byte by ContributionReviewID above.
func ContributionRecordReviewID(source string, revision int64) string {
	return MCPInputDigest([]byte("role-contribution-record-v1\n" + source + "\n" + strconv.FormatInt(revision, 10)))[:32]
}

// ContributionReviewInvitationID selects the durable assessment identity for
// one review without changing the historical post shape.
func ContributionReviewInvitationID(review ContributionReview) string {
	if review.SourceKind == PostSourceRecord {
		return ContributionRecordReviewID(review.Source, review.Revision)
	}
	return ContributionReviewID(review.Source, review.Revision)
}

// SelectContribution requires agreement across all independent formulations.
// It keeps a single role with the strongest conservative probability, retaining
// original candidate order as the deterministic tie-breaker.
// The fixed question loop supplies calls validated by the JEV client.
func SelectContribution(review *ContributionReview) {
	review.State = "not_invited"
	review.Assignment, review.Profile, review.Probability, review.Confidence = "", "", 0, 0
	if review.ProbabilityThreshold <= 0 || review.ConfidenceThreshold <= 0 {
		return
	}
	for _, role := range review.Roles {
		probability, confidence, count := 1.0, 1.0, 0
		for _, call := range review.Calls {
			if call.Assignment != role.Assignment {
				continue
			}
			count++
			if call.Answer.Selected != "contribute" {
				probability = 0
			}
			probability = min(probability, call.Answer.Probabilities["contribute"])
			confidence = min(confidence, call.Answer.Confidence)
		}
		if count != len(ContributionQuestions) || probability == 0 || probability < review.ProbabilityThreshold || confidence < review.ConfidenceThreshold {
			continue
		}
		if review.Assignment == "" || probability > review.Probability {
			review.State, review.Assignment, review.Profile = "invited", role.Assignment, role.Profile
			review.Probability, review.Confidence = probability, confidence
		}
	}
}
