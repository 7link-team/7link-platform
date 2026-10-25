package intake

import (
	"7link.com/platform/internal/iam/audit"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestClarificationCallbacksUseSourceIdentityAndRollbackWithState(t *testing.T) {
	m, db, ctx, _, publisher := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"status": "RECEIVED", "version": 1}).Error)
	var kinds []string
	original := m.brief.audit
	m.brief.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		require.EqualValues(t, 10, event.TargetID)
		require.Equal(t, "intake", event.TargetType)
		require.NotContains(t, event.Detail, "questions")
		kinds = append(kinds, event.EventType)
		return original(ctx, tx, event)
	}
	call := func(action ClarificationAction) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.clarificationStateOn(ctx, tx, 7, 20, 10, 1, 900, action)
			return err
		})
	}
	publisher.fail = true
	require.Error(t, call(ClarificationRequest))
	var version int
	require.NoError(t, db.Table(m.names.Records.String()).Select("version").Where("id=10").Scan(&version).Error)
	require.Equal(t, 1, version)
	publisher.fail = false
	kinds = nil
	require.NoError(t, call(ClarificationRequest))
	require.NoError(t, call(ClarificationComplete))
	require.Equal(t, []string{"intake.clarification_requested", "intake.clarification_answered"}, kinds)
}

func TestClarificationStateFencesSourceVersionAndRound(t *testing.T) {
	m, db, ctx, allowed, _ := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"status": "RECEIVED", "version": 1}).Error)
	call := func(version int, action ClarificationAction) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.clarificationStateOn(ctx, tx, 7, 20, 10, version, 900, action)
			return err
		})
	}
	*allowed = false
	require.ErrorIs(t, call(1, ClarificationRequest), ErrForbidden)
	*allowed = true
	require.NoError(t, call(1, ClarificationRequest))
	require.ErrorIs(t, call(1, ClarificationRequest), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.clarificationStateOn(ctx, tx, 7, 20, 10, 2, 901, ClarificationComplete)
		return err
	}), ErrConflict)
	require.NoError(t, call(2, ClarificationComplete))
	require.ErrorIs(t, call(3, ClarificationComplete), ErrConflict)
	_, err := m.clarificationStateOn(ctx, db, 7, 20, 10, 3, 900, ClarificationRequest)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.clarificationStateOn(context.Background(), nil, 7, 20, 10, 3, 900, ClarificationRequest)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.clarificationStateOn(context.Background(), tx, 7, 20, 10, 3, 900, ClarificationRequest)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.clarificationStateOn(ctx, tx, 7, 21, 10, 3, 900, ClarificationRequest)
		return err
	}), gorm.ErrRecordNotFound)
}
