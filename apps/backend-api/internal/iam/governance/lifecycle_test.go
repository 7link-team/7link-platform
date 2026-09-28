package governance

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var governanceErasureTime = time.Date(2026, 9, 6, 14, 0, 0, 0, time.UTC)

func TestGovernanceErasePrincipalDataRefusesUnusableArguments(t *testing.T) {
	h := newGovernanceHarness(t, "sl_")

	assert.ErrorContains(t, ErasePrincipalData(t.Context(), nil, "", 1, governanceErasureTime), "invalid principal erasure transaction")
	assert.ErrorContains(t, ErasePrincipalData(t.Context(), h.db, "", 0, governanceErasureTime), "invalid principal erasure transaction")
	assert.Error(t, ErasePrincipalData(t.Context(), h.db, "bad prefix; DROP TABLE x;--", 1, governanceErasureTime))
}

// TestGovernanceErasePrincipalDataRedactsNamesButKeepsTheDecisions covers the
// shape of this owner's erasure: an access review is the record of a decision
// somebody made and may have to answer for. The reviewed person's name is
// theirs and goes; the decision, who made it and what it was about stay, or
// erasing a subject would quietly destroy the audit of the review itself.
func TestGovernanceErasePrincipalDataRedactsNamesButKeepsTheDecisions(t *testing.T) {
	h := newGovernanceHarness(t, "sl_")
	seedReviewableGrants(t, h)

	review, err := h.service.CreateReview(t.Context(), 1, 10, CreateAccessReview{
		Name: "Quarterly access review", DueAt: governanceTestNow.Add(24 * time.Hour),
	})
	require.NoError(t, err)
	require.NotZero(t, review.Total, "the fixture must produce something to review")

	const subject, reviewer = guid.ID(11), guid.ID(12)
	itemsBefore := h.count(t, "iam_access_review_items", "principal_id = ?", subject)
	require.NotZero(t, itemsBefore, "the subject must appear in the review")

	require.NoError(t, ErasePrincipalData(t.Context(), h.db, h.prefix, subject, governanceErasureTime))

	assert.Equal(t, itemsBefore,
		h.count(t, "iam_access_review_items", "principal_id = ? AND principal_name = ?", subject, "Anonymized account"),
		"every cached copy of the name is redacted")
	assert.Equal(t, itemsBefore, h.count(t, "iam_access_review_items", "principal_id = ?", subject),
		"the items themselves survive: they are the record of a decision, not the subject's data")
	assert.Zero(t, h.count(t, "iam_access_review_items", "principal_name = ? AND principal_id <> ?", "Anonymized account", subject),
		"nobody else's name is touched")
	assert.NotZero(t, h.count(t, "iam_access_reviews", "tenant_id = ?", 1),
		"the review is still there to be answered for")

	// Erasing someone who was never reviewed is not an error; it is a principal
	// this owner holds nothing for.
	require.NoError(t, ErasePrincipalData(t.Context(), h.db, h.prefix, reviewer, governanceErasureTime))
}
