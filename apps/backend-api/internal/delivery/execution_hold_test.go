package delivery

import (
	"context"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExecutionHoldLocksAllDiscoveredPrincipalsInOrder(t *testing.T) {
	m, db, ctx, _ := stageHarness(t)
	var locked []guid.ID
	m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error { locked = append(locked, id); return nil }
	m.executionHolds.PrincipalsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
		return []guid.ID{30, 3, 11}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.lockHoldPrincipalsOn(ctx, tx, 1, 99, 0, 11, 15) }))
	require.Equal(t, []guid.ID{3, 11, 15, 30}, locked)
}
