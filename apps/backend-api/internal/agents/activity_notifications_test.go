package agents

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestActivityObserverTransitionsRollbackAndNoDomainEvents(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	s := module.NewServices()
	require.NoError(t, m.provideActivityNotifications(s))
	require.Error(t, m.provideActivityNotifications(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[ActivityNotifications](s)
	require.NoError(t, err)
	require.ErrorIs(t, c.Bind(nil), ErrConflict)
	require.NoError(t, db.Exec("CREATE TABLE activity_probe (activity TEXT)").Error)
	fail := false
	var transitions []ActivityTransition
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	require.NoError(t, c.Bind(func(_ context.Context, tx *gorm.DB, in ActivityTransition) error {
		transitions = append(transitions, in)
		if e := tx.Exec("INSERT INTO activity_probe VALUES (?)", in.Activity).Error; e != nil {
			return e
		}
		if fail {
			return errors.New("delayed job unavailable")
		}
		return nil
	}))
	require.ErrorIs(t, c.Bind(func(context.Context, *gorm.DB, ActivityTransition) error { return nil }), ErrConflict)
	report := func(activity string) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.reportOn(ctx, tx, 1, 7, 1, activity) })
	}
	require.NoError(t, report("ERROR"))
	require.NoError(t, report("ERROR"))
	require.Len(t, transitions, 1)
	require.Equal(t, ActivityTransition{TenantID: 1, AgentPrincipalID: 7, ResponsiblePrincipalID: 2, Name: "Test agent", Previous: "OFFLINE", Activity: "ERROR", SessionEpoch: 1, OccurredAt: now}, transitions[0])
	fail = true
	require.Error(t, report("WORKING"))
	snapshot, found, err := c.SnapshotOn(ctx, db, 1, 7)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "ERROR", snapshot.Activity)
	var count int64
	require.NoError(t, db.Table("activity_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	fail = false
	require.NoError(t, report("WORKING"))
	require.NoError(t, report("ERROR"))
	require.Len(t, transitions, 4) // Failed callback was rolled back, then a new ERROR entry.
	for _, table := range []string{"audit_probe", "k_domain_events"} {
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestActivitySnapshotCurrentIdentityMissingAndErrors(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	_, found, err := m.activitySnapshotOn(ctx, db, 1, 7)
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.reportOn(ctx, tx, 1, 7, 1, "ERROR") }))
	owner := guid.ID(3)
	state := "ACTIVE"
	m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{TenantID: 1, PrincipalID: 7, OwnerPrincipalID: owner, Name: "Current name", State: state}, true, nil
	}
	snapshot, found, err := m.activitySnapshotOn(ctx, db, 1, 7)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, ActivitySnapshot{TenantID: 1, AgentPrincipalID: 7, ResponsiblePrincipalID: 3, Name: "Current name", Activity: "ERROR", SessionEpoch: 1}, snapshot)
	state = "DISABLED"
	_, found, err = m.activitySnapshotOn(ctx, db, 1, 7)
	require.NoError(t, err)
	require.False(t, found)
	_, found, err = m.activitySnapshotOn(ctx, db, 2, 7)
	require.NoError(t, err)
	require.False(t, found)
	_, _, err = m.activitySnapshotOn(ctx, nil, 1, 7)
	require.ErrorIs(t, err, ErrInvalid)
	m.agents.ByPrincipalOn = nil
	_, _, err = m.activitySnapshotOn(ctx, db, 1, 7)
	require.ErrorIs(t, err, ErrUnavailable)
	m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, errors.New("directory failed")
	}
	_, _, err = m.activitySnapshotOn(ctx, db, 1, 7)
	require.EqualError(t, err, "directory failed")
}
