package templates

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func rubricFixture() (RubricDefinition, []RubricDimensionScore) {
	r := RubricDefinition{PassRule: RubricPassRule{HardGates: "all_pass"}}
	scores := []RubricDimensionScore{}
	for i, weight := range []string{"0.25", "0.15", "0.15", "0.15", "0.30"} {
		key := string(rune('a' + i))
		r.Dimensions = append(r.Dimensions, RubricDimension{Key: key, Kind: "WEIGHTED", Weight: json.Number(weight)})
		n := json.Number([]string{"4", "3", "4", "3", "5"}[i])
		scores = append(scores, RubricDimensionScore{Key: key, Score: &n})
	}
	r.Dimensions = append(r.Dimensions, RubricDimension{Key: "tests", Kind: "HARD_GATE", VerifierKeys: []string{"unit-tests"}})
	pass := true
	scores = append(scores, RubricDimensionScore{Key: "tests", PassFail: &pass})
	return r, scores
}
func TestRubricExactCalculationHardGateAndBriefThreshold(t *testing.T) {
	r, scores := rubricFixture()
	result, err := CalculateRubric(r, scores, "")
	require.NoError(t, err)
	require.Equal(t, json.Number("4"), *result.Total)
	require.True(t, result.Passed)
	require.Equal(t, json.Number("3.5"), result.WeightedMin)
	result, err = CalculateRubric(r, scores, "4.000001")
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.NotNil(t, result.Total)
	_, err = CalculateRubric(r, scores, "3.499999")
	require.ErrorIs(t, err, ErrInvalid)
	*scores[5].PassFail = false
	result, err = CalculateRubric(r, scores, "")
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Nil(t, result.Total)
}

func TestRubricPreservesDecimalPrecisionAndJSONExponent(t *testing.T) {
	r := RubricDefinition{PassRule: RubricPassRule{HardGates: "all_pass"}, Dimensions: []RubricDimension{{Key: "quality", Kind: "WEIGHTED", Weight: "1e0"}}}
	score := json.Number("3.500000000000000001")
	result, err := CalculateRubric(r, []RubricDimensionScore{{Key: "quality", Score: &score}}, "3.500000000000000002")
	require.NoError(t, err)
	require.Equal(t, score, *result.Total)
	require.False(t, result.Passed)
}
func TestRubricRejectsMalformedAndIncompleteScores(t *testing.T) {
	for _, fault := range []string{"missing", "duplicate", "unknown", "below", "above", "fraction", "exponent", "both", "missing_gate", "unknown_kind", "weight", "duplicate_definition"} {
		t.Run(fault, func(t *testing.T) {
			r, s := rubricFixture()
			switch fault {
			case "missing":
				s = s[:len(s)-1]
			case "duplicate":
				s[1] = s[0]
			case "unknown":
				s[0].Key = "other"
			case "below":
				*s[0].Score = "0.999999"
			case "above":
				*s[0].Score = "5.000001"
			case "fraction":
				*s[0].Score = "4/1"
			case "exponent":
				*s[0].Score = "1e999999999"
			case "both":
				s[0].PassFail = s[5].PassFail
			case "missing_gate":
				s[5].PassFail = nil
			case "unknown_kind":
				r.Dimensions[0].Kind = "OTHER"
			case "weight":
				r.Dimensions[0].Weight = "0.1"
			case "duplicate_definition":
				r.Dimensions[1] = r.Dimensions[0]
			}
			_, e := CalculateRubric(r, s, "")
			require.ErrorIs(t, e, ErrInvalid)
		})
	}
}
func TestRubricPublicationRequiresDeterministicHardGateAndValidWeights(t *testing.T) {
	r, _ := rubricFixture()
	d := testDefinition()
	d.Metadata["rubric"] = r
	d.Metadata["verifiers"] = []VerifierDefinition{verifierDefinition()}
	require.NoError(t, d.Validate(nil))
	decoded, err := rubricDefinition(d)
	require.NoError(t, err)
	require.Equal(t, json.Number("3.5"), decoded.PassRule.WeightedMin)
	for _, fault := range []string{"model_only", "unknown_verifier", "weight", "duplicate", "threshold", "rule", "empty"} {
		t.Run(fault, func(t *testing.T) {
			r, _ := rubricFixture()
			v := verifierDefinition()
			switch fault {
			case "model_only":
				v.Kind = "MODEL_SCORED"
				v.Runner = "AGENT_JUDGE"
			case "unknown_verifier":
				r.Dimensions[5].VerifierKeys = []string{"missing"}
			case "weight":
				r.Dimensions[0].Weight = "0.248999"
			case "duplicate":
				r.Dimensions[1].Key = r.Dimensions[0].Key
			case "threshold":
				r.PassRule.WeightedMin = "4.500001"
			case "rule":
				r.PassRule.HardGates = "any_pass"
			case "empty":
				r.Dimensions = nil
			}
			d.Metadata["rubric"] = r
			d.Metadata["verifiers"] = []VerifierDefinition{v}
			require.ErrorIs(t, d.Validate(nil), ErrInvalid)
		})
	}
}
