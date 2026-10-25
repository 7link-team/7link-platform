package fleet

import (
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestTerminalReservationStorageFailuresNeverLeaveAnActiveShell(t *testing.T) {
	for _, operation := range []string{"query", "create"} {
		exercise := func(t *testing.T, failAt int) int {
			m, db, ctx, existing, _ := terminalFixture(t)
			_, err := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
			require.NoError(t, err)
			q := query.BindTerminalSessions(db, m.names)
			_, err = q.WithContext(ctx).Where(q.ID.Eq(existing.ID)).Delete()
			require.NoError(t, err)
			m.revokeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
			count := 0
			injected := errors.New("terminal reservation storage unavailable")
			// The fixture uses its resolver-selected physical table names.
			hook := func(tx *gorm.DB) {
				if tx.Statement.Table != m.names.Machines.String() && tx.Statement.Table != m.names.TerminalAccess.String() && tx.Statement.Table != m.names.TerminalSessions.String() {
					return
				}
				count++
				if count == failAt {
					tx.AddError(injected)
				}
			}
			if operation == "query" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("terminal_reservation_failure", hook))
			} else {
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("terminal_reservation_failure", hook))
			}
			err = db.Transaction(func(tx *gorm.DB) error {
				_, _, e := m.reserveTerminalOn(ctx, tx, 7, 1, 91, "browser", "fresh")
				return e
			})
			require.NoError(t, db.Callback().Query().Remove("terminal_reservation_failure"))
			require.NoError(t, db.Callback().Create().Remove("terminal_reservation_failure"))
			if failAt == 0 {
				require.NoError(t, err)
				return count
			}
			require.ErrorIs(t, err, injected)
			n, e := q.WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, n)
			return count
		}
		count := exercise(t, 0)
		require.Positive(t, count)
		for point := 1; point <= count; point++ {
			t.Run(fmt.Sprintf("%s/%d", operation, point), func(t *testing.T) { exercise(t, point) })
		}
	}
}

func TestTerminalRecordingReadRequiresExactEntityAndCurrentAuditAuthority(t *testing.T) {
	m, db, ctx, row, _ := terminalFixture(t)
	permissions := []string{}
	m.terminal.execution = func(_ context.Context, _ *gorm.DB, tenant, execution guid.ID, permission string) (TerminalExecution, error) {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 91, execution)
		permissions = append(permissions, permission)
		return TerminalExecution{}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.authorizeTerminalRecordingOn(ctx, tx, row.ID, files.ResourceOwner{Scope: "entity", ID: 9}))
		require.Equal(t, []string{"audit_read"}, permissions)
		for _, owner := range []files.ResourceOwner{{Scope: "tenant", ID: 9}, {Scope: "entity", ID: 8}} {
			require.ErrorIs(t, m.authorizeTerminalRecordingOn(ctx, tx, row.ID, owner), ErrMachineForbidden)
		}
		m.terminal.execution = nil
		require.ErrorIs(t, m.authorizeTerminalRecordingOn(ctx, tx, row.ID, files.ResourceOwner{Scope: "entity", ID: 9}), ErrUnavailable)
		require.ErrorIs(t, m.authorizeTerminalRecordingOn(ctx, tx, 999, files.ResourceOwner{Scope: "entity", ID: 9}), ErrMachineNotFound)
		return nil
	}))
}

func TestTerminalReservationRechecksEveryCurrentPrerequisite(t *testing.T) {
	for _, fault := range []string{"actor", "tenant", "missing-lock", "lock", "proof", "execution", "other-machine", "offline", "missing-access", "disabled", "identifier", "zero-identifier"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, row, _ := terminalFixture(t)
			_, err := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
			require.NoError(t, err)
			q := query.BindTerminalSessions(db, m.names)
			_, err = q.WithContext(ctx).Where(q.ID.Eq(row.ID)).Delete()
			require.NoError(t, err)
			m.revokeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
			injected := errors.New("terminal prerequisite unavailable")
			switch fault {
			case "actor":
				ctx = t.Context()
			case "tenant":
				ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 8, PrincipalID: 11})
			case "missing-lock":
				m.principals.Lock = nil
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "proof":
				m.revokeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return injected }
			case "execution":
				m.terminal.execution = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (TerminalExecution, error) {
					return TerminalExecution{}, injected
				}
			case "other-machine":
				m.terminal.execution = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (TerminalExecution, error) {
					return TerminalExecution{MachineID: 2}, nil
				}
			case "offline":
				require.NoError(t, db.Table(m.names.Machines.String()).Where("id=?", 1).Update("state", "OFFLINE").Error)
			case "missing-access":
				require.NoError(t, db.Table(m.names.TerminalAccess.String()).Where("machine_id=?", 1).Delete(&model.TerminalAccess{}).Error)
			case "disabled":
				require.NoError(t, db.Table(m.names.TerminalAccess.String()).Where("machine_id=?", 1).Update("enabled", false).Error)
			case "identifier":
				m.ids = failedFleetIDs{err: injected}
			case "zero-identifier":
				m.ids = failedFleetIDs{}
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, _, e := m.reserveTerminalOn(ctx, tx, 7, 1, 91, "browser", "fresh")
				return e
			}))
			n, e := q.WithContext(t.Context()).Count()
			require.NoError(t, e)
			require.Zero(t, n)
		})
	}
}

func TestTerminalWriterOutageNeverReturnsSessionOrAcceptsBytes(t *testing.T) {
	m, db, ctx, row, _ := terminalFixture(t)
	bindTerminalRecordingFixture(t, m, db, ctx)
	_, err := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
	require.NoError(t, err)
	m.terminal.transport = TerminalTransport{Available: func(guid.ID, guid.ID) bool { return true }, Open: func(context.Context, TerminalSession, int, int) (bool, error) {
		t.Fatal("outage reached transport")
		return false, nil
	}, Close: func(context.Context, TerminalSession, string) error { t.Fatal("outage reached transport"); return nil }}
	m.runtime = nil
	view, err := m.GetTerminal(ctx, 7, row.ID)
	require.ErrorIs(t, err, ErrUnavailable)
	require.Zero(t, view)
	require.ErrorIs(t, m.AuthorizeTerminalIO(ctx, 7, row.ID, true), ErrUnavailable)
	view, err = m.ValidateTerminalRunner(ctx, 7, row.ID, 1, row.RunnerSessionID, row.ConnEpoch)
	require.ErrorIs(t, err, ErrUnavailable)
	require.Zero(t, view)
	require.ErrorIs(t, m.RecordTerminal(ctx, 7, row.ID, "i", []byte("no shell input")), ErrUnavailable)
	require.ErrorIs(t, m.recordTerminalBytes(ctx, row, []byte("recording"), false), ErrUnavailable)
	view, err = m.CloseTerminal(ctx, 7, row.ID, "USER")
	require.ErrorIs(t, err, ErrUnavailable)
	require.Zero(t, view)
	view, err = m.OpenTerminal(ctx, 7, 1, 91, "browser", "fresh", 80, 24)
	require.ErrorIs(t, err, ErrUnavailable)
	require.Zero(t, view)
	_, err = m.activateTerminal(ctx, row, 16, 80, 24)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.GetTerminalAccess(ctx, 7, 1)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.SetTerminalAccess(ctx, 7, 1, 2, false, "", "")
	require.ErrorIs(t, err, ErrUnavailable)
	q := query.BindTerminalSessions(db, m.names)
	stored, err := q.WithContext(ctx).Where(q.ID.Eq(row.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "OPEN", stored.State)
	require.NotNil(t, stored.ActiveExecution)
}

func TestTerminalOpeningHeaderDurablyAppendsThroughFilesWithSingleWriter(t *testing.T) {
	m, db, ctx, row, _ := terminalFixture(t)
	bindTerminalRecordingFixture(t, m, db, ctx)
	raw, err := db.DB()
	require.NoError(t, err)
	q := query.BindTerminalSessions(db, m.names)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(row.ID)).Update(q.State, "OPENING")
	require.NoError(t, err)
	// Real staging opens its own transactions: neither snapshot nor attach may
	// retain the only writer while storage is read or uploaded.
	raw.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	header := []byte("{\"version\":2,\"width\":80,\"height\":24}\n")
	require.NoError(t, m.recordTerminalBytes(ctx, row, header, false))
	current, err := q.WithContext(ctx).Where(q.ID.Eq(row.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "OPENING", current.State)
	var first []guid.ID
	require.NoError(t, json.Unmarshal([]byte(current.RecordingChunks), &first))
	require.Len(t, first, 1)
	require.NoError(t, m.recordTerminalBytes(ctx, row, []byte("[0.1,\"o\",\"hello\"]\n"), false))
	current, err = q.WithContext(ctx).Where(q.ID.Eq(row.ID)).First()
	require.NoError(t, err)
	var appended []guid.ID
	require.NoError(t, json.Unmarshal([]byte(current.RecordingChunks), &appended))
	require.Len(t, appended, 1)
	require.NotEqual(t, first[0], appended[0], "header and output share one durably replaced tail")
}

func terminalFixture(t *testing.T) (*Module, *gorm.DB, context.Context, *model.TerminalSession, *fleetPublisher) {
	m, db, ctx, p := machineHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.OpenSessionOn(ctx, tx, 7, 1, "terminal-test"); return e }))
	ctx = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, true, nil
	}
	m.terminal.permission = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true}, nil
	}
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.revokeProof = func(_ context.Context, _ *gorm.DB, _ guid.ID, cookie, proof, purpose, target string) error {
		if cookie != "browser" || proof != "fresh" || purpose != "machine_terminal_enable" || target != "machine:1" {
			return authn.ErrInvalidCredentials
		}
		return nil
	}
	m.terminal.execution = func(ctx context.Context, _ *gorm.DB, tenant, id guid.ID, permission string) (TerminalExecution, error) {
		if authn.PrincipalIDFromContext(ctx) != 11 || tenant != 7 || id != 91 {
			return TerminalExecution{}, ErrMachineForbidden
		}
		return TerminalExecution{ID: 91, ProjectID: 9, MachineID: 1, Workspace: "/actual/execution/worktree", DispatchEpoch: 4}, nil
	}
	machine, err := query.BindMachines(db, m.names).WithContext(ctx).First()
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	active := guid.ID(91)
	row := &model.TerminalSession{ID: 501, TenantID: 7, MachineID: 1, ExecutionID: 91, ProjectID: 9, OpenedBy: 11, RunnerSessionID: *machine.CurrentSessionID, ConnEpoch: machine.ConnEpoch, DispatchEpoch: 4, ActiveExecution: &active, State: "OPEN", Workspace: "/actual/execution/worktree", OpenedAt: now, LastInputAt: now, ExpiresAt: now.Add(2 * time.Hour), RecordingChunks: "[]"}
	require.NoError(t, query.BindTerminalSessions(db, m.names).WithContext(ctx).Create(row))
	return m, db, ctx, row, p
}
func TestTerminalPolicyHumanProofVersionAndAtomicEffects(t *testing.T) {
	m, db, ctx, _, p := terminalFixture(t)
	a, err := m.GetTerminalAccess(ctx, 7, 1)
	require.NoError(t, err)
	require.False(t, a.AllowRemoteTerminal)
	require.Equal(t, 1, a.Version)
	_, err = m.SetTerminalAccess(ctx, 7, 1, 1, true, "", "")
	require.ErrorIs(t, err, authn.ErrInvalidCredentials)
	a, err = m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
	require.NoError(t, err)
	require.True(t, a.AllowRemoteTerminal)
	require.Equal(t, 2, a.Version)
	_, err = m.SetTerminalAccess(ctx, 7, 1, 1, false, "", "")
	require.ErrorIs(t, err, ErrVersionConflict)
	p.fail = true
	_, err = m.SetTerminalAccess(ctx, 7, 1, 2, false, "", "")
	require.Error(t, err)
	a, err = m.GetTerminalAccess(ctx, 7, 1)
	require.NoError(t, err)
	require.True(t, a.AllowRemoteTerminal)
	require.Equal(t, 2, a.Version)
	p.fail = false
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	_, err = m.SetTerminalAccess(ctx, 7, 1, 2, false, "", "")
	require.ErrorIs(t, err, ErrMachineForbidden)
	var count int64
	require.NoError(t, db.Table(m.names.TerminalAccess.String()).Count(&count).Error)
	require.Equal(t, int64(1), count)
}
func TestTerminalCurrentAuthorityWorkspaceRunnerFenceAndTimeouts(t *testing.T) {
	m, db, ctx, row, _ := terminalFixture(t)
	_, err := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
	require.NoError(t, err)
	require.NoError(t, m.AuthorizeTerminalIO(ctx, 7, row.ID, false))
	bad := authn.WithClaims(ctx, &authn.Claims{TenantID: 7, PrincipalID: 12})
	require.ErrorIs(t, m.AuthorizeTerminalIO(bad, 7, row.ID, false), ErrMachineForbidden)
	runner := authn.WithClaims(ctx, &authn.Claims{TenantID: 7, PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount})
	_, err = m.ValidateTerminalRunner(runner, 7, row.ID, 1, row.RunnerSessionID, row.ConnEpoch+1)
	require.ErrorIs(t, err, ErrSessionStale)
	_, err = m.ValidateTerminalRunner(runner, 7, row.ID, 1, row.RunnerSessionID, row.ConnEpoch)
	require.NoError(t, err)
	q := query.BindTerminalSessions(db, m.names)
	for _, change := range []map[string]any{{"last_input_at": time.Now().Add(-15 * time.Minute)}, {"expires_at": time.Now().Add(-time.Second)}, {"dispatch_epoch": 5}, {"runner_session_id": 999}, {"workspace": "/other/root"}, {"state": "CLOSED"}} {
		require.NoError(t, db.Table(m.names.TerminalSessions.String()).Where("id = ?", row.ID).Updates(change).Error)
		require.Error(t, m.AuthorizeTerminalIO(ctx, 7, row.ID, false))
		require.NoError(t, db.Table(m.names.TerminalSessions.String()).Where("id = ?", row.ID).Updates(map[string]any{"last_input_at": row.LastInputAt, "expires_at": row.ExpiresAt, "dispatch_epoch": row.DispatchEpoch, "runner_session_id": row.RunnerSessionID, "workspace": row.Workspace, "state": "OPEN"}).Error)
	}
	_, err = q.WithContext(ctx).Where(q.ID.Eq(row.ID)).Update(q.State, "OPENING")
	require.NoError(t, err)
	require.NoError(t, m.AuthorizeTerminalIO(ctx, 7, row.ID, false))
	require.ErrorIs(t, m.AuthorizeTerminalIO(ctx, 7, row.ID, true), ErrTerminalClosed)
	_, err = m.SetTerminalAccess(ctx, 7, 1, 2, false, "", "")
	require.NoError(t, err)
	require.ErrorIs(t, m.AuthorizeTerminalIO(ctx, 7, row.ID, false), ErrTerminalAccessDisabled)
}
func TestTerminalUniqueActiveExecutionAndEventRollback(t *testing.T) {
	m, db, ctx, row, p := terminalFixture(t)
	duplicate := *row
	duplicate.ID++
	require.Error(t, query.BindTerminalSessions(db, m.names).WithContext(ctx).Create(&duplicate))
	p.fail = true
	err := db.Transaction(func(tx *gorm.DB) error { return m.terminalEventOn(ctx, tx, row, "execution.terminal_opened") })
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Table("test_fleet_effects").Where("kind = ?", "execution.terminal_opened").Count(&count).Error)
	require.Zero(t, count)
	_, err = m.OpenTerminal(ctx, 7, 1, 91, "browser", "fresh", 80, 24)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.OpenTerminal(ctx, 7, 1, 91, "browser", "fresh", 80, 201)
	require.ErrorIs(t, err, ErrInvalidArgument)
	m.terminal.execution = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (TerminalExecution, error) {
		return TerminalExecution{}, errors.New("current IAM denied")
	}
	require.Error(t, m.AuthorizeTerminalIO(ctx, 7, row.ID, false))
}

func TestTerminalAccessOrdinaryPermissionUsesCurrentTenantAuthority(t *testing.T) {
	m, _, ctx, _, _ := terminalFixture(t)
	m.policy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		require.NotEqual(t, "machine_access_manage", permission, "non-data action must not use data constraint API")
		return authz.DataConstraint{AllowAll: true}, nil
	}
	calls := 0
	m.terminal.permission = func(_ context.Context, tx *gorm.DB, request core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		calls++
		require.NotNil(t, tx)
		require.Equal(t, core.AuthorizationExplanationRequest{TenantID: 7, PrincipalID: 11, Permission: "machine_access_manage"}, request)
		return authz.Explanation{Allowed: false}, nil
	}
	_, err := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
	require.ErrorIs(t, err, ErrMachineForbidden)
	require.Equal(t, 1, calls)
	m.terminal.permission = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true}, nil
	}
	_, err = m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
	require.NoError(t, err)
}

func TestTerminalAccessQuotaDefaultsBoundsAndConfigurationWithoutStepUp(t *testing.T) {
	m, _, ctx, _, _ := terminalFixture(t)
	initial, err := m.GetTerminalAccess(ctx, 7, 1)
	require.NoError(t, err)
	require.Equal(t, 20000, initial.TunnelBandwidthKbps)
	require.Equal(t, 256, initial.TunnelMaxStreams)
	_, err = m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
	require.NoError(t, err)
	m.revokeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error {
		t.Fatal("configuration-only update must not consume proof")
		return nil
	}
	bandwidth, streams := 1000, 16
	updated, err := m.SetTerminalAccessOptions(ctx, 7, 1, 2, TerminalAccessOptions{Enabled: true, TunnelBandwidthKbps: &bandwidth, TunnelMaxStreams: &streams}, "", "")
	require.NoError(t, err)
	require.Equal(t, 3, updated.Version)
	require.Equal(t, 1000, updated.TunnelBandwidthKbps)
	require.Equal(t, 16, updated.TunnelMaxStreams)
	same, err := m.SetTerminalAccess(ctx, 7, 1, 3, true, "", "")
	require.NoError(t, err)
	require.Equal(t, updated, same)
	for _, invalid := range []int{999, 1000001} {
		_, err = m.SetTerminalAccessOptions(ctx, 7, 1, 3, TerminalAccessOptions{Enabled: true, TunnelBandwidthKbps: &invalid}, "", "")
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, invalid := range []int{15, 4097} {
		_, err = m.SetTerminalAccessOptions(ctx, 7, 1, 3, TerminalAccessOptions{Enabled: true, TunnelMaxStreams: &invalid}, "", "")
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	current, err := m.GetTerminalAccess(ctx, 7, 1)
	require.NoError(t, err)
	require.Equal(t, updated, current)
}

func bindTerminalRecordingFixture(t *testing.T, m *Module, db *gorm.DB, ctx context.Context) {
	t.Helper()
	fm := files.NewModule(m.runtime, files.Config{LocalRoot: t.TempDir()})
	raw, err := db.DB()
	require.NoError(t, err)
	source := fm.MigrationSource()
	source.Prefix = m.runtime.TablePrefix()
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	services := module.NewServices()
	require.NoError(t, fm.ProvideServices(services))
	require.NoError(t, module.Provide[*authz.Registrar](services, authz.NewUnboundRegistrar(authz.MustRegistry())))
	require.NoError(t, module.Provide[core.EntityConstraintOn](services, func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}))
	require.NoError(t, module.Provide[core.TenantConstraintOn](services, m.policy))
	require.NoError(t, module.Provide[core.PlatformPermissionOn](services, func(context.Context, *gorm.DB, string, guid.ID) (bool, error) { return false, nil }))
	require.NoError(t, module.Provide[core.AuthorizationSubjectsOn](services, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (core.AuthorizationSubjects, error) {
		return core.AuthorizationSubjects{}, nil
	}))
	require.NoError(t, module.Provide[core.AuthorizationReferencesOn](services, func(_ context.Context, _ *gorm.DB, tenant guid.ID, refs core.AuthorizationReferences) (bool, error) {
		return tenant == 7 && refs.EntityID == 9 && refs.DepartmentID == 0, nil
	}))
	require.NoError(t, module.Provide[core.EntityOwnerResolver](services, func(_ context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{TenantID: 7, EntityID: id}, id == 9, nil
	}))
	require.NoError(t, module.Provide[core.DepartmentOwnerResolver](services, func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, nil
	}))
	require.NoError(t, module.Provide[guid.Source](services, m.ids))
	require.NoError(t, module.Provide[audit.Appender](services, m.audit))
	require.NoError(t, module.Provide[*core.SystemMenus](services, &core.SystemMenus{}))
	require.NoError(t, module.Provide[core.AuthorizationExplanation](services, m.terminal.permission))
	require.NoError(t, services.Seal())
	require.NoError(t, fm.InjectServices(services))
	require.NoError(t, m.injectTerminal(services))
}

func TestTerminalActualRecordingLifecycleSealsTranscriptAndReleasesExecution(t *testing.T) {
	m, db, ctx, old, _ := terminalFixture(t)
	bindTerminalRecordingFixture(t, m, db, ctx)
	_, e := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
	require.NoError(t, e)
	require.NoError(t, db.Table(m.names.TerminalSessions.String()).Where("id=?", old.ID).Delete(old).Error)
	m.revokeProof = func(_ context.Context, _ *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		require.EqualValues(t, 11, actor)
		require.Equal(t, "terminal_interactive", purpose)
		require.Equal(t, "execution:91", target)
		return nil
	}
	m.terminalBroker = newTerminalBroker()
	c, e := m.terminalBroker.connect(7, 1, old.RunnerSessionID, old.ConnEpoch)
	require.NoError(t, e)
	require.NoError(t, m.BindTerminalTransport(TerminalTransport{Available: m.terminalBroker.available, Open: m.openTerminalStream, Close: func(_ context.Context, s TerminalSession, reason string) error {
		return m.terminalBroker.close(s, reason)
	}}))
	nativeDone := make(chan error, 1)
	go func() {
		select {
		case f := <-c.frames:
			if f.Kind != "open" {
				nativeDone <- errors.New("missing open frame")
				return
			}
			id, e := guid.Parse(f.SessionID)
			if e != nil {
				nativeDone <- e
				return
			}
			stream, e := m.terminalBroker.lookup(id)
			if e != nil {
				nativeDone <- e
				return
			}
			stream.opened <- terminalFrame{Kind: "opened", SessionID: f.SessionID, Seq: 1}
			nativeDone <- nil
		case <-time.After(time.Second):
			nativeDone <- errors.New("open frame timeout")
		}
	}()
	opened, e := m.OpenTerminal(ctx, 7, 1, 91, "browser", "fresh", 80, 24)
	require.NoError(t, e)
	require.NoError(t, <-nativeDone)
	require.Equal(t, "OPEN", opened.State)
	require.False(t, opened.Sandboxed)
	require.NotEqual(t, old.ID, opened.ID)
	require.NoError(t, m.RecordTerminal(ctx, 7, opened.ID, "i", []byte("echo recorded\r")))
	runner := authn.WithClaims(ctx, &authn.Claims{TenantID: 7, PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount})
	require.NoError(t, m.RecordTerminal(runner, 7, opened.ID, "o", []byte("recorded\n")))
	require.ErrorIs(t, m.RecordTerminal(runner, 7, opened.ID, "i", []byte("not allowed")), ErrMachineForbidden)
	metadata, e := m.GetTerminal(ctx, 7, opened.ID)
	require.NoError(t, e)
	require.Equal(t, opened.ID, metadata.ID)
	closed, e := m.CloseTerminal(ctx, 7, opened.ID, "")
	require.NoError(t, e)
	require.Equal(t, "CLOSED", closed.State)
	require.Equal(t, "USER", closed.CloseReason)
	require.NotNil(t, closed.RecordingFileID)
	require.Len(t, closed.RecordingSHA256, 64)
	require.NotNil(t, closed.ClosedAt)
	again, e := m.CloseTerminal(ctx, 7, opened.ID, "OTHER")
	require.NoError(t, e)
	require.Equal(t, closed.RecordingSHA256, again.RecordingSHA256)
	require.Equal(t, "USER", again.CloseReason)
	q := query.BindTerminalSessions(db, m.names)
	stored, e := q.WithContext(ctx).Where(q.ID.Eq(opened.ID)).First()
	require.NoError(t, e)
	require.Nil(t, stored.ActiveExecution)
	require.ErrorIs(t, m.RecordTerminal(ctx, 7, opened.ID, "i", []byte("late")), ErrTerminalClosed)
	require.Zero(t, m.tunnelBudget(7, 1).streams)
}

func TestTerminalWatchdogPersistsSpecificExpiryReasonsAndNeverResumes(t *testing.T) {
	for _, reason := range []string{"MAX_DURATION", "IDLE", "SERVER_RESTART", "CONNECTION_LOST"} {
		t.Run(reason, func(t *testing.T) {
			m, db, ctx, row, _ := terminalFixture(t)
			now := time.Now().UTC()
			changes := map[string]any{}
			switch reason {
			case "MAX_DURATION":
				changes["expires_at"] = now.Add(-time.Second)
			case "IDLE":
				changes["last_input_at"] = now.Add(-16 * time.Minute)
			default:
				changes["opened_at"] = now.Add(-time.Minute)
			}
			if reason == "CONNECTION_LOST" {
				m.terminalBroker = newTerminalBroker()
			}
			require.NoError(t, db.Table(m.names.TerminalSessions.String()).Where("id=?", row.ID).Updates(changes).Error)
			require.NoError(t, m.expireTerminalSessions(ctx, db, 7, now))
			closed, e := m.GetTerminal(ctx, 7, row.ID)
			require.NoError(t, e)
			require.Equal(t, "CLOSED", closed.State)
			require.Equal(t, reason, closed.CloseReason)
			require.NoError(t, m.expireTerminalSessions(ctx, db, 7, now))
		})
	}
}

func TestTerminalMetadataRecordingAndCloseStorageFailuresPreserveOpenSession(t *testing.T) {
	for _, operation := range []string{"metadata", "authorization", "record", "close-read", "close-update", "expiry"} {
		t.Run(operation, func(t *testing.T) {
			m, db, ctx, row, _ := terminalFixture(t)
			injected := errors.New("terminal persistence unavailable")
			hook := func(tx *gorm.DB) {
				if tx.Statement.Table == m.names.TerminalSessions.String() {
					tx.AddError(injected)
				}
			}
			if operation == "close-update" {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("terminal_storage_failure", hook))
			} else {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("terminal_storage_failure", hook))
			}
			var e error
			switch operation {
			case "metadata":
				_, e = m.GetTerminal(ctx, 7, row.ID)
			case "authorization":
				e = m.AuthorizeTerminalIO(ctx, 7, row.ID, false)
			case "record":
				e = m.RecordTerminal(ctx, 7, row.ID, "o", []byte("unrecorded"))
			case "expiry":
				e = m.expireTerminalSessions(ctx, db, 7, time.Now())
			default:
				_, e = m.CloseTerminal(ctx, 7, row.ID, "USER")
			}
			require.ErrorIs(t, e, injected)
			require.NoError(t, db.Callback().Query().Remove("terminal_storage_failure"))
			require.NoError(t, db.Callback().Update().Remove("terminal_storage_failure"))
			stored, e := query.BindTerminalSessions(db, m.names).WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, "OPEN", stored.State)
			require.NotNil(t, stored.ActiveExecution)
		})
	}
}

func TestTerminalRealRecordingAndActivationStorageFailuresNeverExposeOpenShell(t *testing.T) {
	for _, operation := range []string{"activate", "record", "runner", "authorize"} {
		for _, kind := range []string{"query", "update"} {
			total := 0
			run := func(t *testing.T, at int) {
				m, db, ctx, row, _ := terminalFixture(t)
				bindTerminalRecordingFixture(t, m, db, ctx)
				_, err := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
				require.NoError(t, err)
				if operation == "activate" {
					require.NoError(t, db.Table(m.names.TerminalSessions.String()).Where("id=?", row.ID).Update("state", "OPENING").Error)
					row.State = "OPENING"
					m.terminal.transport = TerminalTransport{Available: func(guid.ID, guid.ID) bool { return true }, Open: func(context.Context, TerminalSession, int, int) (bool, error) { return true, nil }, Close: func(context.Context, TerminalSession, string) error { return nil }}
				}
				runner := authn.WithClaims(ctx, &authn.Claims{TenantID: 7, PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount})
				injected := errors.New("terminal current storage failure")
				count := 0
				hook := func(tx *gorm.DB) {
					if tx.Statement.Table != m.names.TerminalSessions.String() && tx.Statement.Table != m.names.Machines.String() && tx.Statement.Table != m.names.Sessions.String() && tx.Statement.Table != m.names.TerminalAccess.String() {
						return
					}
					count++
					if at > 0 && count == at {
						tx.AddError(injected)
					}
				}
				if kind == "query" {
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("terminal_real_failure", hook))
				} else {
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("terminal_real_failure", hook))
				}
				switch operation {
				case "activate":
					_, err = m.activateTerminal(ctx, row, 4, 80, 24)
				case "record":
					err = m.RecordTerminal(ctx, 7, row.ID, "i", []byte("echo actual input\r"))
				case "runner":
					_, err = m.ValidateTerminalRunner(runner, 7, row.ID, 1, row.RunnerSessionID, row.ConnEpoch)
				case "authorize":
					err = m.AuthorizeTerminalIO(ctx, 7, row.ID, true)
				}
				require.NoError(t, db.Callback().Query().Remove("terminal_real_failure"))
				require.NoError(t, db.Callback().Update().Remove("terminal_real_failure"))
				if at == 0 {
					require.NoError(t, err)
					total = count
					return
				}
				require.ErrorIs(t, err, injected)
				stored, e := query.BindTerminalSessions(db, m.names).WithContext(ctx).First()
				require.NoError(t, e)
				if operation == "activate" {
					require.Equal(t, "CLOSED", stored.State)
					require.Nil(t, stored.ActiveExecution)
				} else {
					require.Equal(t, "OPEN", stored.State)
					require.Equal(t, "[]", stored.RecordingChunks)
					require.True(t, row.LastInputAt.Equal(stored.LastInputAt))
				}
			}
			t.Run(operation+"/"+kind+"/success", func(t *testing.T) { run(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/%d", operation, kind, at), func(t *testing.T) { run(t, at) })
			}
		}
	}
}

func TestTerminalActivationTransportAndAuthorityFailureCloseRecordedSession(t *testing.T) {
	for _, fault := range []string{"transport", "authority", "audit", "events"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, row, _ := terminalFixture(t)
			bindTerminalRecordingFixture(t, m, db, ctx)
			_, err := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
			require.NoError(t, err)
			require.NoError(t, db.Table(m.names.TerminalSessions.String()).Where("id=?", row.ID).Update("state", "OPENING").Error)
			row.State = "OPENING"
			injected := errors.New("native terminal unavailable")
			closed := 0
			m.terminal.transport = TerminalTransport{Available: func(guid.ID, guid.ID) bool { return true }, Open: func(context.Context, TerminalSession, int, int) (bool, error) {
				switch fault {
				case "transport":
					return false, injected
				case "authority":
					m.terminal.execution = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (TerminalExecution, error) {
						return TerminalExecution{}, injected
					}
				case "audit":
					previous := m.audit
					m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
						if event.EventType == "execution.terminal_opened" {
							return injected
						}
						return previous(ctx, tx, event)
					}
				case "events":
					m.events = &fleetPublisher{fail: true}
				}
				return false, nil
			}, Close: func(context.Context, TerminalSession, string) error { closed++; return nil }}
			_, err = m.activateTerminal(ctx, row, 4, 80, 24)
			require.Error(t, err)
			require.Equal(t, 1, closed)
			stored, e := query.BindTerminalSessions(db, m.names).WithContext(ctx).First()
			require.NoError(t, e)
			if fault == "events" {
				require.Equal(t, "OPENING", stored.State, "unavailable outbox also rolls back the compensating close")
			} else {
				require.Equal(t, "CLOSED", stored.State)
				require.NotNil(t, stored.RecordingFileID)
				require.Nil(t, stored.ActiveExecution)
			}
		})
	}
}

func TestTerminalExecutionCapabilityRejectsReplacementAndUsesBoundCurrentResolver(t *testing.T) {
	m, _, ctx, row, _ := terminalFixture(t)
	m.terminal.execution = nil
	s := module.NewServices()
	require.NoError(t, m.provideTerminal(s))
	require.NoError(t, s.Seal())
	connector, e := module.Resolve[TerminalExecutionConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, connector.Bind(nil), ErrInvalidArgument)
	calls := 0
	require.NoError(t, connector.Bind(func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (TerminalExecution, error) {
		calls++
		return TerminalExecution{ID: row.ExecutionID, ProjectID: row.ProjectID, MachineID: row.MachineID, Workspace: row.Workspace, DispatchEpoch: row.DispatchEpoch}, nil
	}))
	require.ErrorIs(t, connector.Bind(func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (TerminalExecution, error) {
		t.Fatal("replacement execution owner invoked")
		return TerminalExecution{}, nil
	}), ErrInvalidArgument)
	_, e = m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
	require.NoError(t, e)
	view, e := m.GetTerminal(ctx, 7, row.ID)
	require.NoError(t, e)
	require.Equal(t, row.ID, view.ID)
	require.Positive(t, calls)
}

func TestTerminalRecordedCloseEveryStorageFailurePreservesTranscriptAndActiveExecution(t *testing.T) {
	for _, operation := range []string{"query", "create", "update", "delete"} {
		total := 0
		run := func(t *testing.T, at int) {
			m, db, ctx, row, _ := terminalFixture(t)
			bindTerminalRecordingFixture(t, m, db, ctx)
			_, e := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
			require.NoError(t, e)
			require.NoError(t, m.RecordTerminal(ctx, 7, row.ID, "i", []byte("echo preserve transcript\r")))
			q := query.BindTerminalSessions(db, m.names)
			before, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			injected := errors.New("terminal recording seal storage unavailable")
			count := 0
			hook := func(tx *gorm.DB) {
				count++
				if count == at {
					tx.AddError(injected)
				}
			}
			switch operation {
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("terminal_seal_failure", hook))
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("terminal_seal_failure", hook))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("terminal_seal_failure", hook))
			case "delete":
				require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("terminal_seal_failure", hook))
			}
			_, err := m.CloseTerminal(ctx, 7, row.ID, "USER")
			require.NoError(t, db.Callback().Query().Remove("terminal_seal_failure"))
			require.NoError(t, db.Callback().Create().Remove("terminal_seal_failure"))
			require.NoError(t, db.Callback().Update().Remove("terminal_seal_failure"))
			require.NoError(t, db.Callback().Delete().Remove("terminal_seal_failure"))
			if at == 0 {
				require.NoError(t, err)
				total = count
				return
			}
			require.Error(t, err)
			current, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, before, current)
			require.NoError(t, m.RecordTerminal(ctx, 7, row.ID, "o", []byte("still recoverable\n")))
			closed, e := m.CloseTerminal(ctx, 7, row.ID, "USER")
			require.NoError(t, e)
			require.Equal(t, "CLOSED", closed.State)
			require.NotNil(t, closed.RecordingFileID)
		}
		t.Run(operation+"/success", func(t *testing.T) { run(t, 0) })
		for at := 1; at <= total; at++ {
			t.Run(fmt.Sprintf("%s/%d", operation, at), func(t *testing.T) { run(t, at) })
		}
	}
}
