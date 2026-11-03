package factory

import (
	"errors"
	"strings"
)

// AuthoredProducer identifies deterministic hand-authored proposal content.
// No model call authors this wording; Jev only judges supplied alternatives.
const AuthoredProducer = "soda-authored-v1"

// PlanningProducer identifies prose authored by the admitted native planning
// agent. JEV compares and checks this prose; it does not write it.
const PlanningProducer = "soda-planning-agent-v1"

// WalkthroughBranch names one meaningful outcome of a step and its result.
type WalkthroughBranch struct {
	Outcome string `json:"outcome"`
	Result  string `json:"result"`
}

// WalkthroughStep names who acts, what they do and the visible result.
type WalkthroughStep struct {
	Actor    string              `json:"actor"`
	Action   string              `json:"action"`
	Result   string              `json:"result"`
	Branches []WalkthroughBranch `json:"branches,omitempty"`
}

// ProductAlternative is one authored way to deliver the requested workflow.
type ProductAlternative struct {
	Key          string            `json:"key"`
	Name         string            `json:"name"`
	Audience     string            `json:"audience"`
	Summary      string            `json:"summary"`
	Steps        []WalkthroughStep `json:"steps"`
	Visibility   string            `json:"visibility"`
	Consequences string            `json:"consequences"`
	Evidence     string            `json:"evidence"`
	Outcomes     []string          `json:"outcomes"`
	IdeaIDs      []string          `json:"idea_ids,omitempty"`
}

// AlternativeSet is the producer's bounded answer for one objective. When
// Supported is false there are no alternatives; Unknowns names the gap.
type AlternativeSet struct {
	Producer     string               `json:"producer"`
	Supported    bool                 `json:"supported"`
	Pattern      string               `json:"pattern,omitempty"`
	Alternatives []ProductAlternative `json:"alternatives,omitempty"`
	Unknowns     []string             `json:"unknowns"`
}

var alternativeFamilies = []struct {
	pattern string
	signals []string
	keys    [2]string
}{
	{"decision", []string{"approv", "review", "request", "submit", "decision", "decide", "leave", "time off", "time-off", "vacation"}, [2]string{"single_person", "shared_review"}},
	{"intake", []string{"intake", "onboard", "signup", "sign-up", "sign up", "register", "enroll", "join"}, [2]string{"self_service", "managed_intake"}},
}

var authoredAlternatives = map[string]ProductAlternative{
	"single_person": {
		Key:      "single_person",
		Name:     "Small single-person workflow",
		Audience: "One person completing their own request without a separate decider.",
		Summary:  "The person records what they need and sees the recorded result directly.",
		Steps: []WalkthroughStep{
			{Actor: "The person", Action: "describes what they need", Result: "The request is recorded"},
			{Actor: "The person", Action: "confirms the recorded request", Result: "The request counts as submitted"},
		},
		Visibility:   "Only the person sees their own request unless the description says otherwise.",
		Consequences: "No waiting on another person, and no second check before the request counts.",
		Evidence:     "Authored pattern; no external research was performed.",
	},
	"shared_review": {
		Key:      "shared_review",
		Name:     "Shared review workflow",
		Audience: "A person making a request and another person deciding on it.",
		Summary:  "The person submits a request; a reviewer decides, and the person sees the decision.",
		Steps: []WalkthroughStep{
			{Actor: "The person", Action: "describes what they need and submits the request", Result: "The reviewer can see the request"},
			{Actor: "The reviewer", Action: "makes a decision", Result: "The person sees the decision", Branches: []WalkthroughBranch{
				{Outcome: "Approved", Result: "The person sees confirmation"},
				{Outcome: "Declined", Result: "The person sees the reason and can revise the request"},
			}},
		},
		Visibility:   "The person and the reviewer see the request; who else can see it is not established.",
		Consequences: "A decision takes reviewer time, and a declined request needs revision.",
		Evidence:     "Authored pattern; no external research was performed.",
	},
	"self_service": {
		Key:      "self_service",
		Name:     "Self-service path",
		Audience: "A person completing intake on their own.",
		Summary:  "The person provides the needed details and receives the intake result directly.",
		Steps: []WalkthroughStep{
			{Actor: "The person", Action: "provides the requested details", Result: "The details are recorded"},
			{Actor: "The person", Action: "confirms the recorded details", Result: "The person sees the intake result"},
		},
		Visibility:   "Only the person sees their own details unless the description says otherwise.",
		Consequences: "No waiting on staff, and no human check of the provided details.",
		Evidence:     "Authored pattern; no external research was performed.",
	},
	"managed_intake": {
		Key:      "managed_intake",
		Name:     "Managed intake path",
		Audience: "A person providing details and a staff member completing the intake.",
		Summary:  "The person provides details; a staff member finishes the intake and shares the result.",
		Steps: []WalkthroughStep{
			{Actor: "The person", Action: "provides the requested details", Result: "A staff member can see the details"},
			{Actor: "The staff member", Action: "completes the intake", Result: "The person sees the intake result", Branches: []WalkthroughBranch{
				{Outcome: "Completed", Result: "The person sees confirmation"},
				{Outcome: "Needs more detail", Result: "The person sees what to provide"},
			}},
		},
		Visibility:   "The person and the staff member see the details; who else can see them is not established.",
		Consequences: "Staff time is needed, and incomplete details ask the person for more.",
		Evidence:     "Authored pattern; no external research was performed.",
	},
}

var authoredUnknowns = map[string][]string{
	"decision": {
		"Who can see a request beyond the people named in the walkthrough is not established.",
		"What happens while no decision has been made yet is not established.",
	},
	"intake": {
		"Who can see the provided details beyond the people named in the walkthrough is not established.",
		"What the intake result permits afterwards is not established.",
	},
}

// DescribeAlternatives deterministically maps a representative supported
// objective onto authored alternatives and their walkthroughs. It performs
// no model calls and no research. Unsupported objectives report Supported
// as false with the gap named in Unknowns.
func DescribeAlternatives(objective string) AlternativeSet {
	trimmed := strings.TrimSpace(objective)
	// Objective bound: 16000 UTF-8 BYTES, the persisted-old-valid
	// standard shared with the review-label rule. A multibyte objective
	// that fits in 16000 runes but exceeds 16000 bytes stays
	// unsupported, so recorded baselines keep their support verdict.
	if trimmed == "" || len(trimmed) > 16000 || strings.ContainsRune(trimmed, 0) {
		return AlternativeSet{Producer: AuthoredProducer, Unknowns: []string{"No usable objective was supplied, so no alternative is offered."}}
	}
	lowered := strings.ToLower(trimmed)
	best := -1
	for i, family := range alternativeFamilies {
		hits := 0
		for _, signal := range family.signals {
			if strings.Contains(lowered, signal) {
				hits++
			}
		}
		if hits > 0 && (best < 0 || hits > signalHits(lowered, best)) {
			best = i
		}
	}
	if best < 0 {
		return AlternativeSet{Producer: AuthoredProducer, Unknowns: []string{"This objective is outside the authored representative coverage, so only the conservative baseline applies."}}
	}
	family := alternativeFamilies[best]
	set := AlternativeSet{Producer: AuthoredProducer, Supported: true, Pattern: family.pattern, Unknowns: append([]string(nil), authoredUnknowns[family.pattern]...)}
	for _, key := range family.keys {
		authored := authoredAlternatives[key]
		alternative := authored
		alternative.Steps = append([]WalkthroughStep(nil), authored.Steps...)
		for i := range alternative.Steps {
			alternative.Steps[i].Branches = append([]WalkthroughBranch(nil), authored.Steps[i].Branches...)
		}
		alternative.Outcomes = []string{proposalObjective(trimmed)}
		set.Alternatives = append(set.Alternatives, alternative)
	}
	return set
}

func signalHits(lowered string, family int) int {
	hits := 0
	for _, signal := range alternativeFamilies[family].signals {
		if strings.Contains(lowered, signal) {
			hits++
		}
	}
	return hits
}

// Validate checks producer identity, bounds and supported-shape coherence.
func (s AlternativeSet) Validate() error {
	if s.Producer != AuthoredProducer || len(s.Unknowns) == 0 || len(s.Unknowns) > 8 {
		return errors.New("invalid alternative set identity or unknowns")
	}
	for _, unknown := range s.Unknowns {
		if !intentText(unknown, 4000) {
			return errors.New("invalid alternative unknown")
		}
	}
	if !s.Supported {
		if s.Pattern != "" || len(s.Alternatives) != 0 {
			return errors.New("unsupported set must not carry a pattern or alternatives")
		}
		return nil
	}
	matched := false
	for _, family := range alternativeFamilies {
		if family.pattern == s.Pattern {
			matched = true
		}
	}
	if !matched || len(s.Alternatives) != 2 {
		return errors.New("invalid supported alternative set")
	}
	seen := map[string]bool{}
	for _, alternative := range s.Alternatives {
		if authored, exists := authoredAlternatives[alternative.Key]; !exists || authored.Name != alternative.Name {
			return errors.New("unknown authored product alternative")
		}
		if err := alternative.Validate(); err != nil {
			return err
		}
		if seen[alternative.Key] {
			return errors.New("duplicate alternative")
		}
		seen[alternative.Key] = true
	}
	return nil
}

// validateIdentity checks bounded, portable keys and human-readable content.
func (a ProductAlternative) validateIdentity() error {
	if !intentText(a.Key, 64) || !intentText(a.Name, 4000) || len(a.IdeaIDs) > 50 {
		return errors.New("invalid product alternative identity")
	}
	for _, char := range a.Key {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_') {
			return errors.New("invalid product alternative key")
		}
	}
	if !intentText(a.Audience, 4000) || !intentText(a.Summary, 4000) || !intentText(a.Visibility, 4000) || !intentText(a.Consequences, 4000) || !intentText(a.Evidence, 4000) {
		return errors.New("invalid product alternative prose")
	}
	if len(a.Steps) == 0 || len(a.Steps) > 8 || len(a.Outcomes) == 0 || len(a.Outcomes) > 20 {
		return errors.New("invalid product alternative bounds")
	}
	return nil
}

func validateWalkthroughStep(step WalkthroughStep) error {
	if !intentText(step.Actor, 4000) || !intentText(step.Action, 4000) || !intentText(step.Result, 4000) || len(step.Branches) > 4 {
		return errors.New("invalid walkthrough step")
	}
	for _, branch := range step.Branches {
		if !intentText(branch.Outcome, 4000) || !intentText(branch.Result, 4000) {
			return errors.New("invalid walkthrough branch")
		}
	}
	return nil
}

func (a ProductAlternative) Validate() error {
	if err := a.validateIdentity(); err != nil {
		return err
	}
	for _, step := range a.Steps {
		if err := validateWalkthroughStep(step); err != nil {
			return err
		}
	}
	for _, outcome := range a.Outcomes {
		if !intentText(outcome, 4000) {
			return errors.New("invalid alternative outcome")
		}
	}
	return nil
}
