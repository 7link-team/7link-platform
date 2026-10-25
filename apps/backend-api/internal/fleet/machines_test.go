package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"math"
	"testing"
	"time"
)

type fleetPublisher struct{ fail bool }

func (p *fleetPublisher) Publish(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	if p.fail {
		return 0, errors.New("outbox unavailable")
	}
	return 1, tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES (?)", e.Type).Error
}
func machineHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *fleetPublisher) {
	t.Helper()
	m, db := directoryHarness(t)
	ids, err := guid.New(7)
	require.NoError(t, err)
	m.ids = ids
	m.principals = identity.PrincipalStateStore{Lock: func(context.Context, *gorm.DB, guid.ID) error { return nil }}
	m.accounts = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID, _ time.Time) (bool, error) {
		return tenant == 7 && principal == 101, nil
	}
	require.NoError(t, db.Exec("CREATE TABLE test_fleet_effects (kind TEXT NOT NULL)").Error)
	m.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		return tx.Exec("INSERT INTO test_fleet_effects(kind) VALUES (?)", e.EventType).Error
	}
	p := &fleetPublisher{}
	m.events = p
	key := "machine-one"
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := &model.Machine{ID: 1, TenantID: 7, PrincipalID: 101, Name: "Machine one", NameKey: &key, State: "OFFLINE", OS: "WINDOWS", SlotCount: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, query.BindMachines(db, m.names).WithContext(t.Context()).Create(row))
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 101, SubjectType: authn.SubjectTypePrincipal})
	return m, db, ctx, p
}

func TestBindRunnerContextUsesPersistedOwnershipAndClonesClaims(t *testing.T) {
	m, db, _, _, _ := enrollmentHarness(t)
	claims := &authn.Claims{PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount, OrganizationID: "untrusted-external-organization", EntityID: 99}
	ctx := authn.WithClaims(t.Context(), claims)
	bound, err := m.BindRunnerContext(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, guid.ID(7), authn.TenantIDFromContext(bound))
	require.Zero(t, authn.EntityIDFromContext(bound))
	require.Equal(t, guid.ID(101), authn.PrincipalIDFromContext(bound))
	require.Zero(t, claims.TenantID, "binding must not mutate verifier's shared claims")
	require.Equal(t, guid.ID(99), claims.EntityID)
	for _, tc := range []struct {
		name    string
		claims  *authn.Claims
		machine guid.ID
	}{
		{"wrong principal", &authn.Claims{PrincipalID: 102}, 1},
		{"foreign bound tenant", &authn.Claims{PrincipalID: 101, TenantID: 8}, 1},
		{"zero machine", &authn.Claims{PrincipalID: 101}, 0},
		{"zero principal", &authn.Claims{}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.BindRunnerContext(authn.WithClaims(t.Context(), tc.claims), tc.machine)
			require.ErrorIs(t, err, ErrMachineForbidden)
		})
	}
	_, err = m.BindRunnerContext(ctx, 999)
	require.ErrorIs(t, err, ErrMachineNotFound)
	_, err = m.BindRunnerContext(t.Context(), 1)
	require.ErrorIs(t, err, ErrMachineForbidden)
	var unavailable *Module
	_, err = unavailable.BindRunnerContext(ctx, 1)
	require.Error(t, err)
	require.NoError(t, db.Exec("DROP TABLE "+m.names.Machines.String()).Error)
	_, err = m.BindRunnerContext(ctx, 1)
	require.Error(t, err, "database failure must not yield a bound context")
}
func TestMachineSessionFenceAndCurrentIAM(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	var session SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		session, err = m.OpenSessionOn(ctx, tx, 7, 1, "process-one")
		return err
	}))
	require.Equal(t, int64(1), session.ConnEpoch)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		replay, err := m.OpenSessionOn(ctx, tx, 7, 1, "process-one")
		require.NoError(t, err)
		require.Equal(t, session, replay)
		_, err = m.OpenSessionOn(ctx, tx, 7, 1, "process-two")
		require.ErrorIs(t, err, ErrSessionActive)
		require.ErrorIs(t, m.HeartbeatOn(ctx, tx, 7, 1, session.ID, 2), ErrSessionStale)
		return m.HeartbeatOn(ctx, tx, 7, 1, session.ID, 1)
	}))
	// A token still in memory cannot keep a disabled service account alive.
	m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, nil }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.HeartbeatOn(ctx, tx, 7, 1, session.ID, 1) }), ErrMachineForbidden)
	m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return true, nil }
	other := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 102})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.HeartbeatOn(other, tx, 7, 1, session.ID, 1) }), ErrMachineForbidden)
	// Expire both server timestamps; a heartbeat cannot revive the old epoch.
	old := time.Now().UTC().Add(-31 * time.Second)
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Update("last_seen_at", old).Error)
	require.NoError(t, db.Table(m.names.Sessions.String()).Where("id = ?", session.ID).Update("last_seen_at", old).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.HeartbeatOn(ctx, tx, 7, 1, session.ID, 1) }), ErrSessionStale)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		fact, err := m.MachineByIDOn(ctx, tx, 7, 1)
		require.NoError(t, err)
		require.Equal(t, "OFFLINE", fact.State)
		next, err := m.OpenSessionOn(ctx, tx, 7, 1, "process-two")
		require.NoError(t, err)
		require.Equal(t, int64(2), next.ConnEpoch)
		require.NotEqual(t, session.ID, next.ID)
		return nil
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.HeartbeatOn(ctx, tx, 7, 1, session.ID, 1) }), ErrSessionStale)
}

func TestMachineSlotsReplayCapacityReleaseAndRollback(t *testing.T) {
	m, db, ctx, p := machineHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.OpenSessionOn(ctx, tx, 7, 1, "process"); return err }))
	var first SlotFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = m.ReserveSlotOn(ctx, tx, 7, 1, 1000)
		require.NoError(t, err)
		require.Equal(t, 1, first.SlotNo)
		again, err := m.ReserveSlotOn(ctx, tx, 7, 1, 1000)
		require.NoError(t, err)
		require.Equal(t, first, again)
		_, err = m.ReserveSlotOn(ctx, tx, 7, 1, 1001)
		require.ErrorIs(t, err, ErrSlotsFull)
		return nil
	}))
	// Audit/outbox failure must roll back the release and leave ownership intact.
	p.fail = true
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.ReleaseSlotOn(ctx, tx, 7, 1, 1000) }))
	stored, err := query.BindSlots(db, m.names).WithContext(ctx).First()
	require.NoError(t, err)
	require.NotNil(t, stored.ActiveSlot)
	require.Nil(t, stored.ReleasedAt)
	p.fail = false
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.ReleaseSlotOn(ctx, tx, 7, 1, 1000))
		require.NoError(t, m.ReleaseSlotOn(ctx, tx, 7, 1, 1000))
		_, err := m.ReserveSlotOn(ctx, tx, 7, 1, 1000)
		require.ErrorIs(t, err, ErrSlotReleased)
		next, err := m.ReserveSlotOn(ctx, tx, 7, 1, 1001)
		require.NoError(t, err)
		require.Equal(t, 1, next.SlotNo)
		require.NotEqual(t, first.ID, next.ID)
		return nil
	}))
	var count int64
	require.NoError(t, db.Table("test_fleet_effects").Count(&count).Error)
	require.Equal(t, int64(8), count, "session, reserve, release, reserve each commit one audit and one outbox row")
}

func TestMachineDrainingIsolationWorkspaceAndValidation(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	require.ErrorIs(t, m.HeartbeatOn(ctx, db, 7, 1, 1, 1), ErrInvalidArgument)
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Update("drain_requested", true).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.OpenSessionOn(ctx, tx, 7, 1, "process")
		require.NoError(t, err)
		fact, err := m.MachineByIDOn(ctx, tx, 7, 1)
		require.NoError(t, err)
		require.Equal(t, "DRAINING", fact.State)
		_, err = m.ReserveSlotOn(ctx, tx, 7, 1, 2000)
		require.ErrorIs(t, err, ErrMachineDraining)
		_, err = m.MachineByIDOn(ctx, tx, 8, 1)
		require.ErrorIs(t, err, ErrMachineNotFound)
		_, err = m.OpenSessionOn(ctx, tx, 7, 1, "bad\nprocess")
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, query.BindWorkdirs(db, m.names).WithContext(ctx).Create(&model.Workdir{ID: 2, TenantID: 7, MachineID: 1, ProjectID: 10, RepositoryID: 11, Path: "D:/work", PathKey: strings64(), CreatedAt: now}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		w, err := m.ResolveWorkspaceOn(ctx, tx, 7, 1, 10, 11)
		require.NoError(t, err)
		require.Equal(t, "D:/work", w.Path)
		_, err = m.ResolveWorkspaceOn(ctx, tx, 7, 1, 12, 11)
		require.ErrorIs(t, err, ErrMachineNotFound)
		return nil
	}))
}
func strings64() string { return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }

type failedFleetIDs struct {
	err   error
	value int64
}

func (s failedFleetIDs) Next() (int64, error) { return s.value, s.err }

func TestMachineRejectsInvalidBoundariesAndMissingCapabilities(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	require.ErrorIs(t, fleetTransaction(nil, 7, 1), ErrInvalidArgument)
	require.ErrorIs(t, fleetTransaction(db, 0, 1), ErrInvalidArgument)
	_, err := (*Module)(nil).MachineByIDOn(ctx, nil, 7, 1)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.OpenSessionOn(ctx, db, 7, 1, "p")
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := (*Module)(nil).OpenSessionOn(ctx, tx, 7, 1, "p")
		require.ErrorIs(t, err, ErrUnavailable)
		_, err = m.OpenSessionOn(t.Context(), tx, 7, 1, "p")
		require.ErrorIs(t, err, ErrMachineForbidden)
		_, err = m.OpenSessionOn(ctx, tx, 7, 99, "p")
		require.ErrorIs(t, err, ErrMachineNotFound)
		require.ErrorIs(t, m.HeartbeatOn(ctx, tx, 7, 1, 0, 0), ErrInvalidArgument)
		require.ErrorIs(t, (*Module)(nil).HeartbeatOn(ctx, tx, 7, 1, 1, 1), ErrUnavailable)
		require.ErrorIs(t, m.HeartbeatOn(t.Context(), tx, 7, 1, 1, 1), ErrMachineForbidden)
		require.ErrorIs(t, m.HeartbeatOn(ctx, tx, 7, 99, 1, 1), ErrMachineNotFound)
		_, err = m.ReserveSlotOn(ctx, tx, 7, 1, 0)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = m.ReserveSlotOn(ctx, tx, 8, 1, 1)
		require.ErrorIs(t, err, ErrMachineNotFound)
		_, err = m.ReserveSlotOn(ctx, tx, 7, 1, 1)
		require.ErrorIs(t, err, ErrMachineOffline)
		require.ErrorIs(t, m.ReleaseSlotOn(ctx, tx, 7, 1, 0), ErrInvalidArgument)
		require.ErrorIs(t, m.ReleaseSlotOn(ctx, tx, 8, 1, 1), ErrMachineNotFound)
		_, err = m.ResolveWorkspaceOn(ctx, tx, 7, 1, 0, 0)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = m.ResolveWorkspaceOn(ctx, tx, 8, 1, 1, 1)
		require.ErrorIs(t, err, ErrMachineNotFound)
		return nil
	}))
	// A corrupt/stale current-session reference must not mint another session.
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Update("current_session_id", 999).Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.OpenSessionOn(ctx, tx, 7, 1, "p"); return err }))
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Updates(map[string]any{"current_session_id": nil, "conn_epoch": int64(math.MaxInt64)}).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.OpenSessionOn(ctx, tx, 7, 1, "p"); return err }), ErrUnavailable)
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Update("conn_epoch", 0).Error)
	for _, source := range []failedFleetIDs{{err: errors.New("ID lease lost")}, {value: 0}} {
		m.ids = source
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.OpenSessionOn(ctx, tx, 7, 1, "p"); return err }))
	}
}

func TestMachinePolicyAndAuditFailuresPropagate(t *testing.T) {
	for _, fault := range []string{"principal_lock", "account_check", "account_inactive", "audit", "outbox", "missing_audit"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, p := machineHarness(t)
			failure := errors.New("injected dependency failure")
			switch fault {
			case "principal_lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "account_check":
				m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, failure }
			case "account_inactive":
				m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, nil }
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox":
				p.fail = true
			case "missing_audit":
				m.audit = nil
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.OpenSessionOn(ctx, tx, 7, 1, "p"); return err }))
			q := query.BindSessions(db, m.names)
			count, err := q.WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count)
			if fault == "principal_lock" || fault == "account_check" || fault == "account_inactive" {
				require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.HeartbeatOn(ctx, tx, 7, 1, 1, 1) }))
				require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ReserveSlotOn(ctx, tx, 7, 1, 1); return err }))
			}
		})
	}
}

func TestMachineRevocationAndClosedSessionsFenceWrites(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	var session SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; session, err = m.OpenSessionOn(ctx, tx, 7, 1, "p"); return err }))
	require.NoError(t, db.Table(m.names.Sessions.String()).Where("id = ?", session.ID).Update("state", "CLOSED").Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.HeartbeatOn(ctx, tx, 7, 1, session.ID, 1) }), ErrSessionStale)
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Updates(map[string]any{"name_key": nil, "state": "REVOKED"}).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.OpenSessionOn(ctx, tx, 7, 1, "p"); return err }), ErrMachineRevoked)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.HeartbeatOn(ctx, tx, 7, 1, session.ID, 1) }), ErrMachineRevoked)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ReserveSlotOn(ctx, tx, 7, 1, 99); return err }), ErrMachineRevoked)
}

func TestMachineExplicitRecoveryPreservesReplayAndCapacity(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	var original SlotFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.OpenSessionOn(ctx, tx, 7, 1, "recovery")
		if err != nil {
			return err
		}
		original, err = m.ReserveSlotOn(ctx, tx, 7, 1, 900)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ReleaseSlotOn(ctx, tx, 7, 1, 900) }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ReserveSlotOn(ctx, tx, 7, 1, 900); return err }), ErrSlotReleased)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ReserveSlotOn(ctx, tx, 7, 1, 901); return err }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ReacquireSlotOn(ctx, tx, 7, 1, 900); return err }), ErrSlotsFull)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ReleaseSlotOn(ctx, tx, 7, 1, 901) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		recovered, err := m.ReacquireSlotOn(ctx, tx, 7, 1, 900)
		if err != nil {
			return err
		}
		require.Equal(t, original, recovered)
		replay, err := m.ReacquireSlotOn(ctx, tx, 7, 1, 900)
		require.Equal(t, recovered, replay)
		return err
	}))
	var active, total int64
	require.NoError(t, db.Table(m.names.Slots.String()).Where("released_at IS NULL").Count(&active).Error)
	require.NoError(t, db.Table(m.names.Slots.String()).Count(&total).Error)
	require.Equal(t, int64(1), active)
	require.Equal(t, int64(2), total, "recovery reuses ownership rather than adding another active reservation")
}

func TestMachineRecoveryDenialsPreserveReleasedOwnership(t *testing.T) {
	for _, denied := range []string{"draining", "drain_intent", "offline", "expired", "revoked", "iam"} {
		t.Run(denied, func(t *testing.T) {
			m, db, ctx, _ := machineHarness(t)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.OpenSessionOn(ctx, tx, 7, 1, "recovery")
				if err != nil {
					return err
				}
				_, err = m.ReserveSlotOn(ctx, tx, 7, 1, 900)
				return err
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ReleaseSlotOn(ctx, tx, 7, 1, 900) }))
			expected := ErrMachineDraining
			values := map[string]any{}
			switch denied {
			case "draining":
				values["state"] = "DRAINING"
			case "drain_intent":
				values["drain_requested"] = true
			case "offline":
				values["state"] = "OFFLINE"
				expected = ErrMachineOffline
			case "expired":
				values["last_seen_at"] = time.Now().UTC().Add(-time.Minute)
				expected = ErrMachineOffline
			case "revoked":
				values["state"] = "REVOKED"
				values["name_key"] = nil
				expected = ErrMachineRevoked
			case "iam":
				expected = ErrMachineForbidden
				m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, nil }
			}
			if len(values) > 0 {
				require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Updates(values).Error)
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ReacquireSlotOn(ctx, tx, 7, 1, 900); return err }), expected)
			q := query.BindSlots(db, m.names)
			stored, err := q.WithContext(ctx).Where(q.ExecutionID.Eq(guid.ID(900))).First()
			require.NoError(t, err)
			require.NotNil(t, stored.ReleasedAt)
			require.Nil(t, stored.ActiveSlot)
		})
	}
}

func TestMachineRecoveryRollsBackEffectsAndRejectsOtherMachine(t *testing.T) {
	for _, fault := range []string{"audit", "outbox", "caller"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, publisher := machineHarness(t)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.OpenSessionOn(ctx, tx, 7, 1, "recovery")
				if err != nil {
					return err
				}
				_, err = m.ReserveSlotOn(ctx, tx, 7, 1, 900)
				return err
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ReleaseSlotOn(ctx, tx, 7, 1, 900) }))
			var before int64
			require.NoError(t, db.Table("test_fleet_effects").Count(&before).Error)
			abort := errors.New("test recovery rollback")
			if fault == "audit" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return abort }
			}
			if fault == "outbox" {
				publisher.fail = true
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.ReacquireSlotOn(ctx, tx, 7, 1, 900)
				if fault == "caller" {
					require.NoError(t, err)
					return abort
				}
				return err
			}))
			q := query.BindSlots(db, m.names)
			stored, err := q.WithContext(ctx).Where(q.ExecutionID.Eq(guid.ID(900))).First()
			require.NoError(t, err)
			require.NotNil(t, stored.ReleasedAt)
			require.Nil(t, stored.ActiveSlot)
			var after int64
			require.NoError(t, db.Table("test_fleet_effects").Count(&after).Error)
			require.Equal(t, before, after)
		})
	}
	t.Run("other_machine", func(t *testing.T) {
		m, db, ctx, _ := machineHarness(t)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.OpenSessionOn(ctx, tx, 7, 1, "recovery")
			if err != nil {
				return err
			}
			_, err = m.ReserveSlotOn(ctx, tx, 7, 1, 900)
			return err
		}))
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ReleaseSlotOn(ctx, tx, 7, 1, 900) }))
		now := time.Now().UTC()
		key := "machine-two"
		require.NoError(t, query.BindMachines(db, m.names).WithContext(ctx).Create(&model.Machine{ID: 2, TenantID: 7, PrincipalID: 102, Name: "Machine two", NameKey: &key, State: "ONLINE", OS: "LINUX", SlotCount: 1, LastSeenAt: &now, Version: 1, CreatedAt: now, UpdatedAt: now}))
		m.accounts = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID, _ time.Time) (bool, error) {
			return tenant == 7 && (principal == 101 || principal == 102), nil
		}
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ReacquireSlotOn(ctx, tx, 7, 2, 900); return err }), ErrInvalidArgument)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ReacquireSlotOn(ctx, tx, 8, 1, 900); return err }), ErrMachineNotFound)
	})
}
