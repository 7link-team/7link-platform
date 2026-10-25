package knowledge

import (
	"errors"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLearningRulesUseExistingPendingReviewAndActivation(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	var draft EntryView
	create := func(tx *gorm.DB) error {
		var err error
		draft, err = m.proposeLearningOn(ctx, tx, 1, 3, 44, LearningRuleDraft{Title: "Verify rounding", Body: "Check boundary values against the accepted criterion."})
		return err
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		if err := create(tx); err != nil {
			return err
		}
		return errors.New("escape update failed")
	}))
	var count int64
	require.NoError(t, db.Table(m.names.Entries.String()).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Transaction(create))
	require.Equal(t, "PENDING_REVIEW", draft.State)
	require.Equal(t, "RULE", draft.Kind)
	require.Equal(t, guid.ID(3), *draft.ProjectID)
	require.Contains(t, draft.Provenance, "44")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.proposeLearningOn(knowledgeContext(t, 7), tx, 1, 3, 44, LearningRuleDraft{Title: "Agent draft", Body: "Must not bypass the Human author requirement."})
		return err
	}), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.proposeLearningOn(ctx, tx, 2, 3, 44, LearningRuleDraft{Title: "Other tenant", Body: "Must fail."})
		return err
	}), ErrForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, draft.ID, ReviewInput{Version: 1, Action: "APPROVED", Reason: "Reviewed actual escape and corrective rule independently"})
		require.Equal(t, "ACTIVE", v.State)
		return err
	}))
}
