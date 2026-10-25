package templates

import (
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"math/big"
	"strconv"
	"strings"
)

// RubricDefinition belongs to the same immutable definition as its verifiers.
// Decimal numbers remain exact through validation and score calculation.
type RubricDefinition struct {
	Dimensions []RubricDimension `json:"dimensions"`
	PassRule   RubricPassRule    `json:"pass_rule"`
}
type RubricPassRule struct {
	HardGates   string      `json:"hard_gates"`
	WeightedMin json.Number `json:"weighted_min,omitempty"`
}
type RubricDimension struct {
	Key          string      `json:"dimension_key"`
	Kind         string      `json:"kind"`
	Weight       json.Number `json:"weight,omitempty"`
	VerifierKeys []string    `json:"verifier_keys,omitempty"`
}
type RubricDimensionScore struct {
	Key      string       `json:"dimension_key"`
	Score    *json.Number `json:"score,omitempty"`
	PassFail *bool        `json:"pass_fail,omitempty"`
}
type RubricCalculation struct {
	Total       *json.Number `json:"total"`
	Passed      bool         `json:"passed"`
	WeightedMin json.Number  `json:"weighted_min"`
}

// Huma otherwise infers json.Number from its string underlying Go type.
// Preserve exact decimal decoding while publishing the actual JSON number wire type.
func (RubricPassRule) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["weighted_min"] = &huma.Schema{Type: huma.TypeNumber}
	return s
}
func (RubricDimension) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["weight"] = &huma.Schema{Type: huma.TypeNumber}
	return s
}
func (RubricDimensionScore) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["score"] = &huma.Schema{Type: huma.TypeNumber, Nullable: true}
	return s
}
func (RubricCalculation) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["total"] = &huma.Schema{Type: huma.TypeNumber, Nullable: true}
	s.Properties["weighted_min"] = &huma.Schema{Type: huma.TypeNumber}
	return s
}

func rubricNumber(n json.Number) (*big.Rat, error) {
	// Reject non-JSON spellings (fractions, NaN) even for in-process callers.
	if !json.Valid([]byte(n)) || len(n) > 64 {
		return nil, ErrInvalid
	}
	// Bound exponent expansion before constructing an arbitrary-precision value.
	if pos := strings.IndexAny(string(n), "eE"); pos >= 0 {
		exponent, err := strconv.Atoi(string(n)[pos+1:])
		if err != nil || exponent < -64 || exponent > 64 {
			return nil, ErrInvalid
		}
	}
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return nil, ErrInvalid
	}
	return r, nil
}
func rubricDefinition(d TemplateDefinition) (*RubricDefinition, error) {
	value, exists := d.Metadata["rubric"]
	if !exists {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 64000 {
		return nil, ErrInvalid
	}
	var r RubricDefinition
	if json.Unmarshal(raw, &r) != nil {
		return nil, ErrInvalid
	}
	defs, err := verifierDefinitions(d)
	if err != nil {
		return nil, err
	}
	if err = validateRubric(r, defs); err != nil {
		return nil, err
	}
	if r.PassRule.WeightedMin == "" {
		r.PassRule.WeightedMin = "3.5"
	}
	return &r, nil
}
func validateRubric(r RubricDefinition, verifiers []VerifierDefinition) error {
	if len(r.Dimensions) == 0 || len(r.Dimensions) > 100 || r.PassRule.HardGates != "all_pass" {
		return ErrInvalid
	}
	minimum := r.PassRule.WeightedMin
	if minimum == "" {
		minimum = "3.5"
	}
	m, err := rubricNumber(minimum)
	if err != nil || m.Cmp(big.NewRat(3, 1)) < 0 || m.Cmp(big.NewRat(9, 2)) > 0 {
		return ErrInvalid
	}
	deterministic := map[string]bool{}
	for _, v := range verifiers {
		deterministic[v.Key] = v.Kind == "DETERMINISTIC"
	}
	seen := map[string]bool{}
	sum := new(big.Rat)
	for _, dim := range r.Dimensions {
		if strings.TrimSpace(dim.Key) == "" || len(dim.Key) > 64 || seen[dim.Key] {
			return ErrInvalid
		}
		seen[dim.Key] = true
		for _, key := range dim.VerifierKeys {
			if _, exists := deterministic[key]; !exists {
				return ErrInvalid
			}
		}
		switch dim.Kind {
		case "WEIGHTED":
			w, e := rubricNumber(dim.Weight)
			if e != nil || w.Sign() <= 0 || w.Cmp(big.NewRat(1, 1)) > 0 {
				return ErrInvalid
			}
			sum.Add(sum, w)
		case "HARD_GATE":
			if dim.Weight != "" {
				return ErrInvalid
			}
			supported := false
			for _, key := range dim.VerifierKeys {
				supported = supported || deterministic[key]
			}
			if !supported {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	delta := new(big.Rat).Sub(sum, big.NewRat(1, 1))
	if delta.Abs(delta).Cmp(big.NewRat(1, 1000)) > 0 {
		return ErrInvalid
	}
	return nil
}

// CalculateRubric computes a verdict only. Callers must establish current stage
// authority and validate actual evidence before persisting or using this result.
// briefMinimum may raise the frozen template threshold, never lower it.
func CalculateRubric(r RubricDefinition, scores []RubricDimensionScore, briefMinimum json.Number) (RubricCalculation, error) {
	out := RubricCalculation{}
	minimum := r.PassRule.WeightedMin
	if minimum == "" {
		minimum = "3.5"
	}
	m, err := rubricNumber(minimum)
	if err != nil || m.Cmp(big.NewRat(3, 1)) < 0 || m.Cmp(big.NewRat(9, 2)) > 0 {
		return out, ErrInvalid
	}
	if briefMinimum != "" {
		b, e := rubricNumber(briefMinimum)
		if e != nil || b.Cmp(m) < 0 || b.Cmp(big.NewRat(5, 1)) > 0 {
			return out, ErrInvalid
		}
		minimum, m = briefMinimum, b
	}
	if len(scores) != len(r.Dimensions) || len(scores) == 0 {
		return out, ErrInvalid
	}
	byKey := map[string]RubricDimensionScore{}
	for _, score := range scores {
		if _, exists := byKey[score.Key]; exists {
			return out, ErrInvalid
		}
		byKey[score.Key] = score
	}
	total, weight := new(big.Rat), new(big.Rat)
	hardPass := true
	seen := map[string]bool{}
	for _, dim := range r.Dimensions {
		s, exists := byKey[dim.Key]
		if !exists || seen[dim.Key] || strings.TrimSpace(dim.Key) == "" || len(dim.Key) > 64 {
			return out, ErrInvalid
		}
		seen[dim.Key] = true
		switch dim.Kind {
		case "HARD_GATE":
			if s.PassFail == nil || s.Score != nil || dim.Weight != "" {
				return out, ErrInvalid
			}
			hardPass = hardPass && *s.PassFail
		case "WEIGHTED":
			if s.Score == nil || s.PassFail != nil {
				return out, ErrInvalid
			}
			n, e := rubricNumber(*s.Score)
			w, we := rubricNumber(dim.Weight)
			if e != nil || we != nil || n.Cmp(big.NewRat(1, 1)) < 0 || n.Cmp(big.NewRat(5, 1)) > 0 || w.Sign() <= 0 || w.Cmp(big.NewRat(1, 1)) > 0 {
				return out, ErrInvalid
			}
			total.Add(total, new(big.Rat).Mul(n, w))
			weight.Add(weight, w)
		default:
			return out, ErrInvalid
		}
	}
	if r.PassRule.HardGates != "all_pass" || new(big.Rat).Abs(new(big.Rat).Sub(weight, big.NewRat(1, 1))).Cmp(big.NewRat(1, 1000)) > 0 {
		return out, ErrInvalid
	}
	out.WeightedMin = minimum
	if !hardPass {
		return out, nil
	}
	// Inputs are finite JSON decimals; their sum/product has a denominator made
	// only of powers of two and five. Emit every fractional place without rounding.
	denominator := new(big.Int).Set(total.Denom())
	places := 0
	for _, divisor := range []int64{2, 5} {
		count := 0
		for new(big.Int).Mod(denominator, big.NewInt(divisor)).Sign() == 0 {
			denominator.Div(denominator, big.NewInt(divisor))
			count++
		}
		if count > places {
			places = count
		}
	}
	n := json.Number(total.FloatString(places))
	out.Total, out.Passed = &n, total.Cmp(m) >= 0
	return out, nil
}
