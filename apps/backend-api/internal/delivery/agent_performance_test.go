package delivery

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentPerformanceLoadUsesLiveExecutorNotAccountabilityOrExpiredLease(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	claimed := assignmentClaim(t, m, db, ctx, work, false)
	a := query.BindAssignments(db, m.service.names)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		n, err := m.agentPerformanceLoadOn(ctx, tx, 1, 11)
		require.NoError(t, err)
		require.EqualValues(t, 1, n)
		n, err = m.agentPerformanceLoadOn(ctx, tx, 1, 12)
		require.NoError(t, err)
		require.Zero(t, n)
		return nil
	}))
	_, err := a.WithContext(ctx).Where(a.ID.Eq(claimed.Assignment.ID)).Update(a.LeaseExpiresAt, time.Now().UTC().Add(-time.Hour))
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		n, err := m.agentPerformanceLoadOn(ctx, tx, 1, 11)
		require.Zero(t, n)
		return err
	}))
	_, err = a.WithContext(ctx).Where(a.ID.Eq(claimed.Assignment.ID)).UpdateSimple(a.LeaseExpiresAt.Null())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		n, err := m.agentPerformanceLoadOn(ctx, tx, 1, 11)
		require.EqualValues(t, 1, n)
		return err
	}))
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		n, err := m.agentPerformanceLoadOn(ctx, tx, 1, 11)
		require.Zero(t, n)
		return err
	}))
	_, err = m.agentPerformanceLoadOn(ctx, db, 1, 11)
	require.ErrorIs(t, err, ErrInvalidArgument)
	s := module.NewServices()
	require.NoError(t, m.provideAgentPerformanceLoad(s))
	require.NoError(t, s.Seal())
	v, err := module.Resolve[AgentPerformanceLoad](s)
	require.NoError(t, err)
	require.NotNil(t, v.CountOn)
}
