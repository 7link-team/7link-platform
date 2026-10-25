package delivery

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequirementHTTPRejectsMalformedResourceAndConcurrencyTokens(t *testing.T) {
	m := &Module{}
	for _, bad := range []string{"", "0", "-1", "not-an-id", "9223372036854775808"} {
		t.Run("id/"+bad, func(t *testing.T) {
			ctx := t.Context()
			_, err := m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: bad})
			assertWorkStatus(t, err, 422)
			_, err = m.getRequirementHTTP(ctx, &RequirementGetInput{EntityID: bad, RequirementID: "1"})
			assertWorkStatus(t, err, 422)
			_, err = m.getRequirementHTTP(ctx, &RequirementGetInput{EntityID: "99", RequirementID: bad})
			assertWorkStatus(t, err, 422)
			_, err = m.listRequirementsHTTP(ctx, &requirementListInput{EntityID: bad})
			assertWorkStatus(t, err, 422)
			_, err = m.baselineRequirementHTTP(ctx, &requirementBaselineInput{EntityID: bad, RequirementID: "1", IfMatch: `"1"`})
			assertWorkStatus(t, err, 422)
			_, err = m.baselineRequirementHTTP(ctx, &requirementBaselineInput{EntityID: "99", RequirementID: bad, IfMatch: `"1"`})
			assertWorkStatus(t, err, 422)
			_, err = m.patchCriterionHTTP(ctx, &criterionPatchInput{EntityID: "99", RequirementID: "1", CriterionID: bad, IfMatch: `"1"`})
			assertWorkStatus(t, err, 422)
		})
	}
	for _, match := range []string{"", "1", `W/"1"`, `"0"`, `"-1"`, `"01"`, `"9223372036854775808"`} {
		t.Run("etag/"+match, func(t *testing.T) {
			ctx := t.Context()
			_, err := m.baselineRequirementHTTP(ctx, &requirementBaselineInput{EntityID: "99", RequirementID: "1", IfMatch: match})
			assertWorkStatus(t, err, 422)
			_, err = m.patchRequirementHTTP(ctx, &requirementEditInput[requirementPatchBody]{EntityID: "99", RequirementID: "1", IfMatch: match})
			assertWorkStatus(t, err, 422)
			_, err = m.createCriterionHTTP(ctx, &requirementEditInput[criterionCreateBody]{EntityID: "99", RequirementID: "1", IfMatch: match})
			assertWorkStatus(t, err, 422)
			_, err = m.patchCriterionHTTP(ctx, &criterionPatchInput{EntityID: "99", RequirementID: "1", CriterionID: "1", IfMatch: match})
			assertWorkStatus(t, err, 422)
		})
	}
	_, err := m.getRequirementHTTP(t.Context(), &RequirementGetInput{EntityID: "99", RequirementID: "1"})
	assertWorkStatus(t, err, 503)
	_, err = m.listRequirementsHTTP(t.Context(), &requirementListInput{EntityID: "99"})
	assertWorkStatus(t, err, 503)
	_, err = m.createRequirementHTTP(t.Context(), &requirementCreateInput{EntityID: "99"})
	assertWorkStatus(t, err, 503)
	require.Nil(t, m.runtime, "validation must not require storage or alter configuration")
}
