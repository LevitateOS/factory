package control

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

// systemOneDistributionTolerance matches the priority answer contract:
// provider distributions sum to approximately 1.
const systemOneDistributionTolerance = 0.001

func systemOneConfidence(answer map[string]any) (float64, error) {
	value, ok := answer["confidence"].(float64)
	if !ok || math.IsNaN(value) || value < 0 || value > 1 {
		return 0, errors.New("System One answer is missing finite 0-1 confidence")
	}
	return value, nil
}

// systemOneDistribution requires the named probabilities to cover
// exactly the expected keys with finite 0-1 values summing to
// approximately 1.
func systemOneDistribution(answer map[string]any, expected []string) (map[string]float64, error) {
	raw, ok := answer["probabilities"].(map[string]any)
	if !ok {
		return nil, errors.New("System One answer is missing its probabilities")
	}
	if len(raw) != len(expected) {
		return nil, errors.New("System One probabilities do not cover the requested options")
	}
	dist := make(map[string]float64, len(expected))
	sum := 0.0
	for _, key := range expected {
		value, ok := raw[key].(float64)
		if !ok || math.IsNaN(value) || value < 0 || value > 1 {
			return nil, errors.New("System One probabilities do not cover the requested options")
		}
		dist[key] = value
		sum += value
	}
	if math.Abs(sum-1) > systemOneDistributionTolerance {
		return nil, errors.New("System One probabilities do not sum to 1")
	}
	return dist, nil
}

// ValidateSystemOneAnswer checks the shared primitive contract independently of
// the provider transport and its funding or usage envelope.
func ValidateSystemOneAnswer(kind string, answer map[string]any, criteria any) error {
	switch kind {
	case "noul":
		// The noul primitive carries no separate confidence field;
		// 0.5 expresses uncertainty.
		value, ok := answer["noul"].(float64)
		if !ok || math.IsNaN(value) || value < 0 || value > 1 {
			return errors.New("System One noul answer is not a 0-1 probability")
		}
	case "choice":
		value, ok := answer["choice"].(string)
		var options map[string]json.RawMessage
		raw, err := json.Marshal(criteria)
		if err != nil || json.Unmarshal(raw, &options) != nil {
			return errors.New("System One choice criteria are not named options")
		}
		if _, present := options[value]; !ok || !present {
			return errors.New("System One choice is not a requested option")
		}
		names := make([]string, 0, len(options))
		for name := range options {
			names = append(names, name)
		}
		dist, err := systemOneDistribution(answer, names)
		if err != nil {
			return err
		}
		for _, p := range dist {
			if p > dist[value]+systemOneDistributionTolerance {
				return errors.New("System One choice is not the highest-probability option")
			}
		}
		if _, err := systemOneConfidence(answer); err != nil {
			return err
		}
	case "score":
		value, ok := answer["score"].(float64)
		var levels []json.RawMessage
		raw, err := json.Marshal(criteria)
		if err != nil || json.Unmarshal(raw, &levels) != nil || len(levels) < 2 {
			return errors.New("System One score criteria are not ordered levels")
		}
		if !ok || math.IsNaN(value) || value < 0 || value > float64(len(levels)-1) {
			return errors.New("System One score is outside its level range")
		}
		legend, ok := answer["legend"].(map[string]any)
		if !ok {
			return errors.New("System One score answer is missing its legend")
		}
		keys := make([]string, 0, len(levels))
		for i := range levels {
			key := strconv.Itoa(i)
			if _, present := legend[key]; !present {
				return errors.New("System One legend does not cover every level")
			}
			keys = append(keys, key)
		}
		if len(legend) != len(keys) {
			return errors.New("System One legend does not cover every level")
		}
		dist, err := systemOneDistribution(answer, keys)
		if err != nil {
			return err
		}
		weighted := 0.0
		for i := range levels {
			weighted += float64(i) * dist[strconv.Itoa(i)]
		}
		if math.Abs(weighted-value) > systemOneDistributionTolerance {
			return errors.New("System One score is not its probability-weighted average")
		}
		if _, err := systemOneConfidence(answer); err != nil {
			return err
		}
	default:
		return errors.New("System One returned an unknown question type")
	}
	return nil
}
