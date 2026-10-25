package fleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDrainPreservesReservationsAndOfflineIntent(t *testing.T) {
	m, db, ctx, p := machineHarness(t)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, true, nil
	}
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.OpenSessionOn(ctx, tx, 7, 1, "one"); return err }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ReserveSlotOn(ctx, tx, 7, 1, 91); return err }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, err := m.SetDrainOn(ctx, tx, 7, 1, 2, true)
		require.Equal(t, "DRAINING", v.State)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ReserveSlotOn(ctx, tx, 7, 1, 92); return err }), ErrMachineDraining)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SetDrainOn(ctx, tx, 7, 1, 2, false); return err }), ErrVersionConflict)
	old := time.Now().UTC().Add(-31 * time.Second)
	q := query.BindMachines(db, m.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(guid.ID(1))).Update(q.LastSeenAt, old)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ExpireMachineOn(ctx, tx, 7, 1, time.Now().UTC()) }))
	row, err := q.WithContext(ctx).Where(q.ID.Eq(guid.ID(1))).First()
	require.NoError(t, err)
	require.Equal(t, "OFFLINE", row.State)
	require.True(t, row.DrainRequested)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, err := m.ReserveSlotOn(ctx, tx, 7, 1, 91)
		require.Equal(t, 91, int(v.ExecutionID))
		return err
	}))
	p.fail = true
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SetDrainOn(ctx, tx, 7, 1, row.Version, false); return err }))
	row, err = q.WithContext(ctx).Where(q.ID.Eq(guid.ID(1))).First()
	require.NoError(t, err)
	require.True(t, row.DrainRequested)
	p.fail = false
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SetDrainOn(ctx, tx, 7, 1, row.Version, false); return err }), ErrMachineForbidden)
}

func TestWatchdogRechecksHeartbeatAndRollsBackOutboxFailure(t *testing.T) {
	m, db, ctx, p := machineHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.OpenSessionOn(ctx, tx, 7, 1, "one"); return err }))
	q := query.BindMachines(db, m.names)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ExpireMachineOn(ctx, tx, 7, 1, time.Now().UTC()) }))
	row, err := q.WithContext(ctx).Where(q.ID.Eq(guid.ID(1))).First()
	require.NoError(t, err)
	require.Equal(t, "ONLINE", row.State)
	p.fail = true
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.ExpireMachineOn(ctx, tx, 7, 1, time.Now().UTC().Add(31*time.Second)) }))
	row, err = q.WithContext(ctx).Where(q.ID.Eq(guid.ID(1))).First()
	require.NoError(t, err)
	require.Equal(t, "ONLINE", row.State)
	p.fail = false
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ExpireMachineOn(ctx, tx, 7, 1, time.Now().UTC().Add(31*time.Second)) }))
	sessions := query.BindSessions(db, m.names)
	session, err := sessions.WithContext(ctx).Where(sessions.MachineID.Eq(guid.ID(1))).First()
	require.NoError(t, err)
	require.Equal(t, "EXPIRED", session.State)
	require.NotNil(t, session.ClosedAt)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ExpireMachineOn(ctx, tx, 7, 1, time.Now().UTC().Add(time.Minute)) }))
}

type recordingFleetEvents struct {
	delegate    events.Publisher
	published   []events.Event
	rejectState string
}

func (p *recordingFleetEvents) Publish(ctx context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
	if event.Type == "machine.enrollment_changed" && p.rejectState != "" && event.Data.(map[string]any)["state"] == p.rejectState {
		return 0, errors.New("test enrollment event publication failure")
	}
	id, err := p.delegate.Publish(ctx, tx, event)
	if err == nil {
		p.published = append(p.published, event)
	}
	return id, err
}

func TestDrainEventsAttributeCurrentHumanAndDescribeExpiry(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "expired"}[expired], func(t *testing.T) {
			m, db, machineCtx, _, publisher := enrollmentHarness(t)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.OpenSessionOn(machineCtx, tx, 7, 1, "one"); return err }))
			if expired {
				require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Update("last_seen_at", time.Now().UTC().Add(-time.Minute)).Error)
			}
			recorded := &recordingFleetEvents{delegate: publisher}
			m.events = recorded
			humanCtx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SetDrainOn(humanCtx, tx, 7, 1, 2, true); return err }))
			if expired {
				require.Len(t, recorded.published, 2)
				require.Equal(t, "HEARTBEAT_EXPIRED", recorded.published[0].Data.(map[string]any)["reason"])
			} else {
				require.Len(t, recorded.published, 1)
			}
			changed := recorded.published[len(recorded.published)-1]
			require.Equal(t, "HUMAN", changed.Actor.Kind)
			require.Equal(t, guid.ID(11), changed.Actor.ID)
			require.NotEmpty(t, changed.Actor.AuthoritySource)
		})
	}
}

func TestDrainAuthorityFailuresNeverChangeMachine(t *testing.T) {
	for _, fault := range []string{"actor", "tenant", "lock_missing", "human_missing", "policy_missing", "lock_error", "human_error", "inactive", "policy_error"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, _ := enrollmentHarness(t)
			failure := errors.New("authority lookup failed")
			want := ErrMachineForbidden
			switch fault {
			case "actor":
				ctx = context.Background()
			case "tenant":
				ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 8, PrincipalID: 101})
			case "lock_missing":
				m.principals.Lock = nil
				want = ErrUnavailable
			case "human_missing":
				m.principals.ActiveHumanByID = nil
				want = ErrUnavailable
			case "policy_missing":
				m.policy = nil
				want = ErrUnavailable
			case "lock_error":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
				want = failure
			case "human_error":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
				want = failure
			case "inactive":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "policy_error":
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
				want = failure
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SetDrainOn(ctx, tx, 7, 1, 1, true); return err }), want)
			var row struct {
				Version        int
				DrainRequested bool
			}
			require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Take(&row).Error)
			require.Equal(t, 1, row.Version)
			require.False(t, row.DrainRequested)
		})
	}
}

func TestDrainReplayAndStorageFailurePreserveCurrentState(t *testing.T) {
	for _, fault := range []string{"replay", "missing", "revoked", "update", "expire_machine", "expire_session"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, _ := enrollmentHarness(t)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.OpenSessionOn(ctx, tx, 7, 1, "control"); return err }))
			machine, drain := guid.ID(1), true
			switch fault {
			case "replay":
				drain = false
			case "missing":
				machine = 999
			case "revoked":
				require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Updates(map[string]any{"state": "REVOKED", "name_key": nil}).Error)
			case "update":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_machine_update BEFORE UPDATE ON dir_fleet_machines BEGIN SELECT RAISE(ABORT, 'machine write failure'); END").Error)
			case "expire_machine", "expire_session":
				require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Update("last_seen_at", time.Now().UTC().Add(-time.Minute)).Error)
				table := "dir_fleet_machines"
				if fault == "expire_session" {
					table = "dir_fleet_runner_sessions"
				}
				require.NoError(t, db.Exec("CREATE TRIGGER reject_expiry BEFORE UPDATE ON "+table+" BEGIN SELECT RAISE(ABORT, 'expiry write failure'); END").Error)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				fact, err := m.SetDrainOn(ctx, tx, 7, machine, 2, drain)
				if fault == "replay" {
					require.Equal(t, "ONLINE", fact.State)
				}
				return err
			})
			if fault == "replay" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			var row struct {
				Version        int
				DrainRequested bool
				State          string
			}
			require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Take(&row).Error)
			require.Equal(t, 2, row.Version)
			require.False(t, row.DrainRequested)
			if fault != "revoked" {
				require.Equal(t, "ONLINE", row.State)
			}
			var session struct{ State string }
			require.NoError(t, db.Table(m.names.Sessions.String()).Where("machine_id = ?", 1).Take(&session).Error)
			require.Equal(t, "ACTIVE", session.State)
		})
	}
}
