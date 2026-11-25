package factory

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Typed questions (slice 5.1). An ask may declare the shape its answer takes;
// the feed renders that shape natively from this data, never from agent
// markup. Free text stays the default: an ask without a form, or any ordinary
// reply, is answered in words. A typed answer is an ordinary reply to the ask
// that also carries the answer as data; the server checks it against the
// ask's form here and records the plain-text rendering as the reply body, so
// readers of text keep working and the agent receives both.
const (
	QuestionPickOne     = "pick_one"
	QuestionYesNo       = "yes_no"
	QuestionScore       = "score"
	QuestionPickSeveral = "pick_several"
	QuestionValue       = "value"
)

// ErrQuestionAnswerInvalid marks an answer that does not fit its question.
var ErrQuestionAnswerInvalid = errors.New("answer does not fit the question")

// FeedQuestionForm is the declared answer shape of one ask. Only the fields
// of its kind may be set.
type FeedQuestionForm struct {
	Kind string `json:"kind"`
	// pick_one (2–6) and pick_several (2–8).
	Options []FeedQuestionOption `json:"options,omitempty"`
	// pick_several: 0 means at least one / up to every option.
	MinPicks int `json:"min_picks,omitempty"`
	MaxPicks int `json:"max_picks,omitempty"`
	// yes_no: what each side leads to.
	YesMeans string `json:"yes_means,omitempty"`
	NoMeans  string `json:"no_means,omitempty"`
	// score: an integer range inside 0..10.
	Low       int    `json:"low,omitempty"`
	High      int    `json:"high,omitempty"`
	LowLabel  string `json:"low_label,omitempty"`
	HighLabel string `json:"high_label,omitempty"`
	// value: a labelled short text, or a number when Number is set.
	Label  string `json:"label,omitempty"`
	Unit   string `json:"unit,omitempty"`
	Number bool   `json:"number,omitempty"`
}

type FeedQuestionOption struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// FeedQuestionAnswer is one typed answer; only the field of its kind is set.
type FeedQuestionAnswer struct {
	Kind    string   `json:"kind"`
	Choice  string   `json:"choice,omitempty"`
	Choices []string `json:"choices,omitempty"`
	Yes     *bool    `json:"yes,omitempty"`
	Score   *int     `json:"score,omitempty"`
	Value   string   `json:"value,omitempty"`
}

// FeedQuestionAnswered is served on an ask from its first live typed answer
// reply; workers cannot submit it.
type FeedQuestionAnswered struct {
	Post   string             `json:"post"`
	Text   string             `json:"text"`
	Answer FeedQuestionAnswer `json:"answer"`
}

var questionOptionID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// plainText admits one short label: plain words only, one line, no markup.
func plainText(value, field string, max int, required bool) error {
	if strings.TrimSpace(value) == "" {
		if required {
			return fmt.Errorf("question %s is required", field)
		}
		if value != "" {
			return fmt.Errorf("question %s is blank", field)
		}
		return nil
	}
	if utf8.RuneCountInString(value) > max || !utf8.ValidString(value) {
		return fmt.Errorf("question %s exceeds %d characters", field, max)
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '<' || r == '>' || r == '`' || r == ' ' || r == ' ' {
			return fmt.Errorf("question %s must be plain text on one line", field)
		}
	}
	return nil
}

// Validate admits one declared answer shape.
func (f FeedQuestionForm) Validate() error {
	options := len(f.Options) > 0
	picks := f.MinPicks != 0 || f.MaxPicks != 0
	yesNo := f.YesMeans != "" || f.NoMeans != ""
	score := f.Low != 0 || f.High != 0 || f.LowLabel != "" || f.HighLabel != ""
	value := f.Label != "" || f.Unit != "" || f.Number
	only := func(allowed ...bool) error {
		set := 0
		for _, field := range []bool{options, picks, yesNo, score, value} {
			if field {
				set++
			}
		}
		for _, field := range allowed {
			if field {
				set--
			}
		}
		if set != 0 {
			return fmt.Errorf("question form %s carries fields of another kind", f.Kind)
		}
		return nil
	}
	switch f.Kind {
	case QuestionPickOne, QuestionPickSeveral:
		if err := only(options, picks && f.Kind == QuestionPickSeveral); err != nil {
			return err
		}
		max := 6
		if f.Kind == QuestionPickSeveral {
			max = 8
		}
		if len(f.Options) < 2 || len(f.Options) > max {
			return fmt.Errorf("question form %s needs 2–%d options", f.Kind, max)
		}
		ids, labels := map[string]bool{}, map[string]bool{}
		for _, option := range f.Options {
			if !questionOptionID.MatchString(option.ID) || ids[option.ID] {
				return errors.New("question option ids must be unique lowercase identifiers of at most 32 characters")
			}
			ids[option.ID] = true
			if err := plainText(option.Label, "option label", 80, true); err != nil {
				return err
			}
			if labels[strings.TrimSpace(option.Label)] {
				return errors.New("question option labels must be unique")
			}
			labels[strings.TrimSpace(option.Label)] = true
			if err := plainText(option.Detail, "option detail", 240, false); err != nil {
				return err
			}
		}
		least, most := f.picks()
		if f.MinPicks < 0 || f.MaxPicks < 0 || least > most || most > len(f.Options) {
			return errors.New("question form min_picks and max_picks must fit the options")
		}
	case QuestionYesNo:
		if err := only(yesNo); err != nil {
			return err
		}
		if err := plainText(f.YesMeans, "yes_means", 160, false); err != nil {
			return err
		}
		return plainText(f.NoMeans, "no_means", 160, false)
	case QuestionScore:
		if err := only(score); err != nil {
			return err
		}
		if f.Low < 0 || f.High > 10 || f.Low >= f.High {
			return errors.New("question score needs an integer range inside 0..10")
		}
		if err := plainText(f.LowLabel, "low_label", 40, false); err != nil {
			return err
		}
		return plainText(f.HighLabel, "high_label", 40, false)
	case QuestionValue:
		if err := only(value); err != nil {
			return err
		}
		if err := plainText(f.Label, "label", 80, true); err != nil {
			return err
		}
		return plainText(f.Unit, "unit", 16, false)
	default:
		return errors.New("unknown question form kind")
	}
	return nil
}

// picks is the effective pick_several bound: 0 means one / every option.
func (f FeedQuestionForm) picks() (int, int) {
	least, most := f.MinPicks, f.MaxPicks
	if least == 0 {
		least = 1
	}
	if most == 0 {
		most = len(f.Options)
	}
	return least, most
}

// Admit checks one answer against this form and returns its canonical form
// (pick_several in option order) and the plain-text rendering recorded as the
// reply body. It is the one definition of a fitting answer.
func (f FeedQuestionForm) Admit(a FeedQuestionAnswer) (FeedQuestionAnswer, string, error) {
	refuse := func(reason string) (FeedQuestionAnswer, string, error) {
		return FeedQuestionAnswer{}, "", fmt.Errorf("%w: %s", ErrQuestionAnswerInvalid, reason)
	}
	if a.Kind != f.Kind {
		return refuse("answer kind differs from the question")
	}
	others := func(set ...bool) bool {
		n := 0
		for _, field := range []bool{a.Choice != "", a.Choices != nil, a.Yes != nil, a.Score != nil, a.Value != ""} {
			if field {
				n++
			}
		}
		for _, field := range set {
			if field {
				n--
			}
		}
		return n != 0
	}
	label := func(id string) (string, bool) {
		for _, option := range f.Options {
			if option.ID == id {
				return strings.TrimSpace(option.Label), true
			}
		}
		return "", false
	}
	out := FeedQuestionAnswer{Kind: f.Kind}
	switch f.Kind {
	case QuestionPickOne:
		text, ok := label(a.Choice)
		if others(a.Choice != "") || !ok {
			return refuse("pick one of the listed options")
		}
		out.Choice = a.Choice
		return out, text, nil
	case QuestionYesNo:
		if others(a.Yes != nil) || a.Yes == nil {
			return refuse("answer yes or no")
		}
		out.Yes = a.Yes
		if *a.Yes {
			return out, "Yes", nil
		}
		return out, "No", nil
	case QuestionScore:
		if others(a.Score != nil) || a.Score == nil || *a.Score < f.Low || *a.Score > f.High {
			return refuse("score inside the question's range")
		}
		out.Score = a.Score
		text := fmt.Sprintf("%d on a scale of %d to %d", *a.Score, f.Low, f.High)
		if f.LowLabel != "" || f.HighLabel != "" {
			ends := []string{}
			if f.LowLabel != "" {
				ends = append(ends, fmt.Sprintf("%d = %s", f.Low, strings.TrimSpace(f.LowLabel)))
			}
			if f.HighLabel != "" {
				ends = append(ends, fmt.Sprintf("%d = %s", f.High, strings.TrimSpace(f.HighLabel)))
			}
			text += " (" + strings.Join(ends, ", ") + ")"
		}
		return out, text, nil
	case QuestionPickSeveral:
		if others(a.Choices != nil) {
			return refuse("pick from the listed options")
		}
		chosen := map[string]bool{}
		for _, id := range a.Choices {
			if _, ok := label(id); !ok || chosen[id] {
				return refuse("pick each listed option at most once")
			}
			chosen[id] = true
		}
		least, most := f.picks()
		if len(chosen) < least || len(chosen) > most {
			return refuse(fmt.Sprintf("pick %d to %d options", least, most))
		}
		labels := []string{}
		for _, option := range f.Options {
			if chosen[option.ID] {
				out.Choices = append(out.Choices, option.ID)
				labels = append(labels, strings.TrimSpace(option.Label))
			}
		}
		return out, strings.Join(labels, "; "), nil
	case QuestionValue:
		value := strings.TrimSpace(a.Value)
		if others(a.Value != "") || plainText(value, "value", 200, true) != nil {
			return refuse("give a short value on one line")
		}
		if f.Number {
			n, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsInf(n, 0) || math.IsNaN(n) || len(value) > 32 {
				return refuse("give a number")
			}
		}
		out.Value = value
		text := strings.TrimSpace(f.Label) + ": " + value
		if f.Unit != "" {
			text += " " + strings.TrimSpace(f.Unit)
		}
		return out, text, nil
	}
	return refuse("unknown question form kind")
}
