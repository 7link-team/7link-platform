package execution

import (
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestDeliveryUnavailableNeverCreatesExecution(t *testing.T) {
	f := executionHarness(t)
	f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return ErrCapabilityUnavailable }
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.CreateOn(f.human, tx, 7, validCreate()); return err }), ErrCapabilityUnavailable)
	var n int64
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, f.db.Table("slots").Count(&n).Error)
	require.Zero(t, n)
}
func TestAssignmentPrincipalsLockBeforeBusinessRows(t *testing.T) {
	f := executionHarness(t)
	var locked []guid.ID
	f.m.work.PrincipalsOn = func(_ context.Context, _ *gorm.DB, l WorkLink) ([]guid.ID, error) {
		require.Equal(t, guid.ID(7), l.TenantID)
		require.Equal(t, validCreate().AssignmentID, l.AssignmentID)
		return []guid.ID{71, 11}, nil
	}
	old := f.m.principals.Lock
	f.m.principals.Lock = func(ctx context.Context, tx *gorm.DB, id guid.ID) error {
		locked = append(locked, id)
		return old(ctx, tx, id)
	}
	f.create(t)
	require.GreaterOrEqual(t, len(locked), 3)
	require.Equal(t, []guid.ID{11, 71, 101}, locked[:3])
	failure := errors.New("assignment read failed")
	f.m.work.PrincipalsOn = func(context.Context, *gorm.DB, WorkLink) ([]guid.ID, error) { return nil, failure }
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.CreateOn(f.human, tx, 7, validCreate()); return err }), failure)
}
