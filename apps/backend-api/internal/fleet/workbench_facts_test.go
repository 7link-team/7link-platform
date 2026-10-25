package fleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMachineNoticeUsesActualExpiredSessionAndRecovery(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	read := func(tenant, session guid.ID) (MachineNoticeFact, bool) {
		f, found, err := m.machineNoticeFactsOn(ctx, db, tenant, 1, session)
		require.NoError(t, err)
		return f, found
	}
	_, found := read(7, 0)
	require.False(t, found, "never-online enrollment is a distinct source")
	var session SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		session, err = m.OpenSessionOn(ctx, tx, 7, 1, "source-notice-first")
		return err
	}))
	_, found = read(7, session.ID)
	require.False(t, found, "active session has no outage receipt")
	seen := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Millisecond)
	q := query.BindMachines(db, m.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(guid.ID(1))).Update(q.LastSeenAt, seen)
	require.NoError(t, err)
	s := query.BindSessions(db, m.names)
	_, err = s.WithContext(ctx).Where(s.ID.Eq(session.ID)).Update(s.LastSeenAt, seen)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ExpireMachineOn(ctx, tx, 7, 1, time.Now().UTC()) }))
	f, found := read(7, session.ID)
	require.True(t, found)
	require.True(t, f.Offline)
	require.False(t, f.Recovered)
	require.Equal(t, seen, f.OfflineAt, "watchdog update time does not shift the outage start")
	_, found = read(8, session.ID)
	require.False(t, found, "tenant fence")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.OpenSessionOn(ctx, tx, 7, 1, "source-notice-second"); return err }))
	f, found = read(7, session.ID)
	require.True(t, found)
	require.False(t, f.Offline)
	require.True(t, f.Recovered)
	require.Equal(t, seen, f.OfflineAt)
}

func TestHomeCapacityUsesConfiguredAgentCapAndCurrentInventoryAuthority(t *testing.T) {
	m, db, ctx, _, _, _, in := agentRegistrationHarness(t)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.RegisterAgentOn(ctx, tx, 7, in); return err }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		capacity, err := m.workbenchCapacityOn(ctx, tx, 7, in.PrincipalID)
		require.NoError(t, err)
		require.NotNil(t, capacity)
		require.Equal(t, 1, *capacity)
		capacity, err = m.workbenchCapacityOn(ctx, tx, 7, 11)
		require.NoError(t, err)
		require.Nil(t, capacity, "Human capacity is not inferred from Agent defaults")
		m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{}, nil
		}
		capacity, err = m.workbenchCapacityOn(ctx, tx, 7, in.PrincipalID)
		require.NoError(t, err)
		require.Nil(t, capacity, "revoked inventory permission suppresses configuration facts")
		return nil
	}))
}

func TestRegistrationProviderUsesCanonicalHumanCommand(t *testing.T) {
	m, db, human, _, _, _, in := agentRegistrationHarness(t)
	s := module.NewServices()
	require.NoError(t, m.provideWorkbenchFacts(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[AgentRegistrationCommands](s)
	require.NoError(t, err)
	require.NotNil(t, c.RegisterOn)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := c.RegisterOn(human, tx, 7, in); return err }))
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		child, err := c.RegisterOn(human, tx, 7, in)
		require.Positive(t, child.ID)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := c.RegisterOn(human, tx, 7, in); return err }), ErrVersionConflict)
}

func TestMachineLoadIsCurrentAuthorizedReservationFact(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	allowed := true
	var sourceErr error
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: allowed}, sourceErr
	}
	q := query.BindSlots(db, m.names)
	active := 1
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, q.WithContext(ctx).Create(&model.SlotReservation{ID: 91, TenantID: 7, MachineID: 1, ExecutionID: 101, SlotNo: 1, ActiveSlot: &active, CreatedAt: now}))
	require.NoError(t, q.WithContext(ctx).Create(&model.SlotReservation{ID: 92, TenantID: 7, MachineID: 1, ExecutionID: 102, SlotNo: 1, ReleasedAt: &now, CreatedAt: now}))
	_, err := m.workbenchMachinesOn(ctx, db, 7)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, err := m.workbenchMachinesOn(ctx, tx, 7)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.EqualValues(t, 1, rows[0].Reserved, "offline reservations remain real load")
		require.Equal(t, "OFFLINE", rows[0].State)
		allowed = false
		rows, err = m.workbenchMachinesOn(ctx, tx, 7)
		require.ErrorIs(t, err, ErrMachineForbidden)
		require.Nil(t, rows)
		allowed = true
		sourceErr = errors.New("IAM unavailable")
		_, err = m.workbenchMachinesOn(ctx, tx, 7)
		require.ErrorIs(t, err, sourceErr)
		_, err = m.workbenchMachinesOn(ctx, tx, 8)
		require.ErrorIs(t, err, ErrMachineForbidden)
		return nil
	}))
}

func TestEnrollmentFactsUseImmutableMachineCreationAndRealSessionHistory(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	actor, machine := guid.ID(11), guid.ID(1)
	now := time.Now().UTC().Truncate(time.Millisecond)
	q := query.BindEnrollments(db, m.names)
	require.NoError(t, q.WithContext(ctx).Create(&model.Enrollment{ID: 91, TenantID: 7, ClientID: 101, DeviceHash: "device", UserHash: "user", Name: "Machine one", OS: "WINDOWS", State: "COMPLETE", ApprovedBy: &actor, MachineID: &machine, CreatedAt: now, UpdatedAt: now.Add(time.Hour), ExpiresAt: now.Add(2 * time.Hour)}))
	fact, found, err := m.enrollmentNotificationFactsOn(ctx, db, 7, 91)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, actor, fact.ApproverID)
	require.False(t, fact.EverOnline)
	require.True(t, fact.ApprovedAt.Before(now.Add(time.Hour)), "mutable enrollment update time cannot move the warning deadline")
	_, found, err = m.enrollmentNotificationFactsOn(ctx, db, 8, 91)
	require.NoError(t, err)
	require.False(t, found)
	closed := now.Add(time.Minute)
	require.NoError(t, query.BindSessions(db, m.names).WithContext(ctx).Create(&model.RunnerSession{ID: 92, TenantID: 7, MachineID: 1, IncarnationID: "observed", ConnEpoch: 1, State: "CLOSED", CreatedAt: now, LastSeenAt: now, ClosedAt: &closed}))
	fact, found, err = m.enrollmentNotificationFactsOn(ctx, db, 7, 91)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, fact.EverOnline)
}

func TestMachineOfflineCursorAndNotificationSourceFailuresDoNotExposePartialFacts(t *testing.T) {
	for _, failure := range []string{"", "sessions", "machines", "agents", "slots"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx, _ := machineHarness(t)
			var session SessionFact
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				session, e = m.OpenSessionOn(ctx, tx, 7, 1, "notification-sources")
				return e
			}))
			old := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Millisecond)
			require.NoError(t, db.Table(m.names.Machines.String()).Where("id=1").Update("last_seen_at", old).Error)
			require.NoError(t, db.Table(m.names.Sessions.String()).Where("id=?", session.ID).Update("last_seen_at", old).Error)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ExpireMachineOn(ctx, tx, 7, 1, time.Now().UTC()) }))
			if failure != "" {
				table := map[string]string{"sessions": m.names.Sessions.String(), "machines": m.names.Machines.String(), "agents": m.names.Agents.String(), "slots": m.names.Slots.String()}[failure]
				injected := errors.New("notice source unavailable")
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("notice_source_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(injected)
					}
				}))
				_, found, e := m.machineNoticeFactsOn(ctx, db, 7, 0, session.ID)
				require.ErrorIs(t, e, injected)
				require.False(t, found)
				return
			}
			ids, e := m.machineOfflineMachinesOn(ctx, db, 7, 0, 1)
			require.NoError(t, e)
			require.Equal(t, []guid.ID{1}, ids)
			ids, e = m.machineOfflineMachinesOn(ctx, db, 7, 1, 1)
			require.NoError(t, e)
			require.Empty(t, ids)
			ids, e = m.machineOfflineMachinesOn(ctx, db, 8, 0, 100)
			require.NoError(t, e)
			require.Empty(t, ids)
			f, found, e := m.machineNoticeFactsOn(ctx, db, 7, 0, session.ID)
			require.NoError(t, e)
			require.True(t, found)
			require.True(t, f.Offline)
			require.Equal(t, session.ID, f.SessionID)
			_, found, e = m.machineNoticeFactsOn(ctx, db, 7, 0, 999)
			require.NoError(t, e)
			require.False(t, found)
			_, found, e = m.machineNoticeFactsOn(ctx, db, 7, 999, session.ID)
			require.NoError(t, e)
			require.False(t, found)
			_, _, e = m.machineNoticeFactsOn(ctx, nil, 7, 1, 0)
			require.ErrorIs(t, e, ErrInvalidArgument)
			_, _, e = m.machineNoticeFactsOn(ctx, db, 7, 0, 0)
			require.ErrorIs(t, e, ErrInvalidArgument)
			_, e = m.machineOfflineMachinesOn(ctx, nil, 7, 0, 1)
			require.ErrorIs(t, e, ErrInvalidArgument)
			_, e = m.machineOfflineMachinesOn(ctx, db, 7, -1, 1)
			require.ErrorIs(t, e, ErrInvalidArgument)
		})
	}
}

func TestWorkbenchReadersFailClosedOnEachPersistedSourceAndInvalidScope(t *testing.T) {
	for _, source := range []string{"machines", "slots", "agents"} {
		t.Run(source, func(t *testing.T) {
			m, db, ctx, _ := machineHarness(t)
			m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			table := map[string]string{"machines": m.names.Machines.String(), "slots": m.names.Slots.String(), "agents": m.names.Agents.String()}[source]
			injected := errors.New("workbench source unavailable")
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("workbench_source_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(injected)
				}
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				if source == "agents" {
					capacity, e := m.workbenchCapacityOn(ctx, tx, 7, 101)
					require.ErrorIs(t, e, injected)
					require.Nil(t, capacity)
				} else {
					rows, e := m.workbenchMachinesOn(ctx, tx, 7)
					require.ErrorIs(t, e, injected)
					require.Nil(t, rows)
				}
				return nil
			}))
		})
	}
	m, db, ctx, _ := machineHarness(t)
	_, e := m.workbenchCapacityOn(ctx, db, 7, 101)
	require.ErrorIs(t, e, ErrUnavailable)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	_, e = m.workbenchCapacityOn(ctx, db, 7, 101)
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = m.workbenchMachinesOn(ctx, nil, 7)
	require.ErrorIs(t, e, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.workbenchCapacityOn(ctx, tx, 8, 101)
		require.ErrorIs(t, e, ErrMachineForbidden)
		m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{}, errors.New("current policy unavailable")
		}
		_, e = m.workbenchCapacityOn(ctx, tx, 7, 101)
		require.ErrorContains(t, e, "current policy unavailable")
		m.policy = nil
		_, e = m.workbenchMachinesOn(ctx, tx, 7)
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
}

func TestEnrollmentNotificationSourceErrorsNeverClaimOnlineHistory(t *testing.T) {
	for _, source := range []string{"enrollment", "machine", "session"} {
		t.Run(source, func(t *testing.T) {
			m, db, ctx, _ := machineHarness(t)
			actor, machine := guid.ID(11), guid.ID(1)
			now := time.Now().UTC()
			require.NoError(t, query.BindEnrollments(db, m.names).WithContext(ctx).Create(&model.Enrollment{ID: 91, TenantID: 7, ClientID: 101, DeviceHash: "device", UserHash: "user", Name: "Machine one", OS: "WINDOWS", State: "COMPLETE", ApprovedBy: &actor, MachineID: &machine, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}))
			table := map[string]string{"enrollment": m.names.Enrollments.String(), "machine": m.names.Machines.String(), "session": m.names.Sessions.String()}[source]
			injected := errors.New("enrollment source unavailable")
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("enrollment_source_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(injected)
				}
			}))
			fact, found, e := m.enrollmentNotificationFactsOn(ctx, db, 7, 91)
			require.ErrorIs(t, e, injected)
			require.False(t, found)
			require.False(t, fact.EverOnline)
		})
	}
}
