package execution

import (
	"context"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"path/filepath"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
)

type failedExecutionIDs struct{ err error }

func (s failedExecutionIDs) Next() (int64, error) { return 0, s.err }

func TestCreateDependencyFailuresRollbackEveryOwnedEffect(t *testing.T) {
	for _, fault := range []string{"machine_missing", "machine_error", "lock_missing", "lock_error", "no_actor", "foreign_tenant", "human_missing", "human_error", "policy_missing", "policy_error", "ids_missing", "ids_zero", "ids_error", "authorize_missing", "workspace_missing", "reserve_missing", "workspace_error", "reserve_error", "budget_missing", "price_missing", "budget_error", "price_error", "budget_reserve_error", "budget_negative", "insert", "audit_missing", "events_missing", "audit_error"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			failure := errors.New("injected execution dependency failure")
			switch fault {
			case "machine_missing":
				f.m.machines.ByIDOn = nil
			case "machine_error":
				f.m.machines.ByIDOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, failure
				}
			case "lock_missing":
				f.m.principals.Lock = nil
			case "lock_error":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "no_actor":
				f.human = context.Background()
			case "foreign_tenant":
				f.human = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 8, PrincipalID: 11})
			case "human_missing":
				f.m.principals.ActiveHumanByID = nil
			case "human_error":
				f.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			case "policy_missing":
				f.m.policy = nil
			case "policy_error":
				f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "ids_missing":
				f.m.ids = nil
			case "ids_zero":
				f.m.ids = failedExecutionIDs{}
			case "ids_error":
				f.m.ids = failedExecutionIDs{failure}
			case "authorize_missing":
				f.m.work.AuthorizeOn = nil
			case "workspace_missing":
				f.m.machines.ResolveWorkspaceOn = nil
			case "reserve_missing":
				f.m.machines.ReserveSlotOn = nil
			case "workspace_error":
				f.m.machines.ResolveWorkspaceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (fleet.WorkspaceFact, error) {
					return fleet.WorkspaceFact{}, failure
				}
			case "reserve_error":
				f.m.machines.ReserveSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.SlotFact, error) {
					return fleet.SlotFact{}, failure
				}
			case "budget_missing":
				f.m.work.BudgetOn = nil
			case "price_missing":
				f.m.machines.ModelPriceOn = nil
			case "budget_error":
				f.m.work.BudgetOn = func(context.Context, *gorm.DB, WorkLink) (BudgetFact, error) { return BudgetFact{}, failure }
			case "price_error":
				f.m.machines.ModelPriceOn = func(context.Context, *gorm.DB, guid.ID, string, string) (*fleet.TokenPrice, error) {
					return nil, failure
				}
			case "budget_reserve_error":
				f.m.work.ReserveBudgetOn = func(context.Context, *gorm.DB, WorkLink, int64, bool) error { return failure }
			case "budget_negative":
				f.m.work.BudgetOn = func(context.Context, *gorm.DB, WorkLink) (BudgetFact, error) { return BudgetFact{SpentMicros: -1}, nil }
			case "insert":
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_execution BEFORE INSERT ON ex_execution_executions BEGIN SELECT RAISE(ABORT, 'test insert failure'); END").Error)
			case "audit_missing":
				f.m.audit = nil
			case "events_missing":
				f.m.events = nil
			case "audit_error":
				f.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.CreateOn(f.human, tx, 7, validCreate()); return err }))
			for _, table := range []string{f.m.names.Executions.String(), "slots", "effects"} {
				var count int64
				require.NoError(t, f.db.Table(table).Count(&count).Error)
				require.Zero(t, count, table)
			}
		})
	}
}

type testPublisher struct{ fail bool }

func (p *testPublisher) Publish(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	if p.fail {
		return 0, ErrUnavailable
	}
	return 1, tx.Exec("INSERT INTO effects(kind) VALUES (?)", e.Type).Error
}

type executionFixture struct {
	m             *Module
	db            *gorm.DB
	human, runner context.Context
	fence         RunnerFence
	pub           *testPublisher
	machineState  string
	sessionValid  bool
	authorized    bool
	humanActive   bool
}

func executionHarness(t *testing.T) *executionFixture {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "execution.sqlite"), Readable: true, Writable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("ex_"), appenv.WithDatabase(router))
	m := NewModule(rt, Config{})
	db := router.Write()
	raw, err := db.DB()
	require.NoError(t, err)
	source := m.MigrationSource()
	source.Prefix = "ex_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	require.NoError(t, db.Exec("CREATE TABLE effects(kind TEXT NOT NULL)").Error)
	require.NoError(t, db.Exec("CREATE TABLE slots(execution_id INTEGER PRIMARY KEY,active INTEGER NOT NULL)").Error)
	f := &executionFixture{m: m, db: db, human: authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11}), runner: authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 101}), fence: RunnerFence{MachineID: 1, SessionID: 2, ConnEpoch: 1}, pub: &testPublisher{}, machineState: "ONLINE", sessionValid: true, authorized: true, humanActive: true}
	m.agentRuntime.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
		machine := guid.ID(1)
		return fleet.AgentRuntimeFact{MachineID: &machine, MachineState: f.machineState, RuntimeKind: "CODEX"}, true, nil
	}
	m.ids, err = guid.New(9)
	require.NoError(t, err)
	m.events = f.pub
	m.commentActivities = delivery.WorkItemActivityRecorder{RecordOn: func(_ context.Context, tx *gorm.DB, _, _ guid.ID, in delivery.WorkActivityInput) (int64, error) {
		return 1, tx.Exec("INSERT INTO effects(kind) VALUES (?)", in.Kind).Error
	}}
	m.commentContext = delivery.WorkItemActivityReader{HeadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error) { return 0, nil }, CheckOn: func(_ context.Context, _ *gorm.DB, _, _ guid.ID, seen *int64, required bool) error {
		if required && seen == nil {
			return delivery.ErrWorkSeenSeqRequired
		}
		return nil
	}}
	staged := map[*files.PreparedExecutionArtifact][]byte{}
	m.artifacts = artifactFiles{
		stage: func(_ context.Context, _ files.ExecutionArtifactAuthority, _, _, _ string, r io.Reader, _ int64) (*files.PreparedExecutionArtifact, error) {
			b, e := io.ReadAll(r)
			p := &files.PreparedExecutionArtifact{}
			staged[p] = b
			return p, e
		},
		attach: func(_ context.Context, _ *gorm.DB, p *files.PreparedExecutionArtifact, _ guid.ID) (files.PinnedResource, error) {
			return files.PinnedResource{BindingID: 504, SpaceID: 501, FileID: 502, VersionID: 503, SHA256: hash(staged[p])}, nil
		},
		references: func(ctx context.Context, _ *gorm.DB, _ files.ExecutionArtifactAuthority, run func(context.Context) error) error {
			return run(ctx)
		},
	}
	m.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		return tx.Exec("INSERT INTO effects(kind) VALUES (?)", e.EventType).Error
	}
	m.principals = identity.PrincipalStateStore{Lock: func(_ context.Context, _ *gorm.DB, id guid.ID) error {
		if id <= 0 {
			return ErrForbidden
		}
		return nil
	}, ActiveHumanByID: func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, f.humanActive, nil
	}}
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: f.authorized}, nil
	}
	machine := func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.MachineFact, error) {
		if tenant != 7 || id != 1 {
			return fleet.MachineFact{}, ErrNotFound
		}
		return fleet.MachineFact{ID: 1, PrincipalID: 101, State: f.machineState}, nil
	}
	m.machines = fleet.MachineDirectory{BindRunnerContext: func(ctx context.Context, _ guid.ID) (context.Context, error) {
		claims, ok := authn.FromContext(ctx)
		if !ok || claims == nil {
			return ctx, ErrForbidden
		}
		bound := *claims
		bound.TenantID, bound.EntityID = 7, 0
		return authn.WithClaims(ctx, &bound), nil
	}, ModelPriceOn: func(context.Context, *gorm.DB, guid.ID, string, string) (*fleet.TokenPrice, error) {
		return &fleet.TokenPrice{InputMicros: 10, OutputMicros: 20}, nil
	}, ByIDOn: machine, LockOn: machine, ValidateSessionOn: func(ctx context.Context, _ *gorm.DB, tenant, id, session guid.ID, epoch int64) error {
		if !f.sessionValid || tenant != 7 || id != 1 || session != 2 || epoch != 1 || authn.PrincipalIDFromContext(ctx) != 101 || f.machineState == "REVOKED" {
			return ErrFenced
		}
		return nil
	}, ResolveWorkspaceOn: func(_ context.Context, _ *gorm.DB, tenant, machine, project, repository guid.ID) (fleet.WorkspaceFact, error) {
		if tenant != 7 || machine != 1 || project != 9 || repository != 4 {
			return fleet.WorkspaceFact{}, ErrNotFound
		}
		return fleet.WorkspaceFact{ID: 5, Path: "/registered/workspace", PathKey: "key"}, nil
	}, ReserveSlotOn: func(_ context.Context, tx *gorm.DB, _, _, id guid.ID) (fleet.SlotFact, error) {
		if f.machineState != "ONLINE" {
			return fleet.SlotFact{}, ErrUnavailable
		}
		return fleet.SlotFact{ExecutionID: id}, tx.Exec("INSERT INTO slots(execution_id,active) VALUES (?,1)", id).Error
	}, ReleaseSlotOn: func(_ context.Context, tx *gorm.DB, _, _, id guid.ID) error {
		return tx.Exec("UPDATE slots SET active=0 WHERE execution_id=?", id).Error
	}, ReacquireSlotOn: func(_ context.Context, tx *gorm.DB, _, _, id guid.ID) (fleet.SlotFact, error) {
		if f.machineState != "ONLINE" {
			return fleet.SlotFact{}, ErrUnavailable
		}
		return fleet.SlotFact{ExecutionID: id}, tx.Exec("UPDATE slots SET active=1 WHERE execution_id=?", id).Error
	}}
	m.work = WorkItems{ReserveBudgetOn: func(_ context.Context, tx *gorm.DB, _ WorkLink, _ int64, _ bool) error {
		return tx.Exec("INSERT INTO effects(kind) VALUES (?)", "budget.reserved").Error
	}, BudgetOn: func(context.Context, *gorm.DB, WorkLink) (BudgetFact, error) { return BudgetFact{}, nil }, AuthorizeOn: func(_ context.Context, _ *gorm.DB, l WorkLink, _ string) error {
		if !f.authorized || l.TenantID != 7 || l.ProjectID != 9 || l.WorkItemID != 3 || l.AssignmentID != 6 {
			return ErrForbidden
		}
		return nil
	}, StartedOn: func(_ context.Context, tx *gorm.DB, _ WorkLink) error {
		return tx.Exec("INSERT INTO effects(kind) VALUES ('work.started')").Error
	}, FinishedOn: func(_ context.Context, tx *gorm.DB, _ WorkLink, r WorkResult) error {
		return tx.Exec("INSERT INTO effects(kind) VALUES (?)", "work."+r.State).Error
	}}
	return f
}
func (f *executionFixture) create(t *testing.T) ExecutionView {
	t.Helper()
	var out ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; out, e = f.m.CreateOn(f.human, tx, 7, validCreate()); return e }))
	return out
}
func validCreate() ExecutionCreate {
	return ExecutionCreate{ProjectID: 9, WorkItemID: 3, AssignmentID: 6, MachineID: 1, Plan: ExecutionPlan{MaxInputTokens: 4000, MaxOutputTokens: 2000, Runtime: "CODEX", Model: "observed-model", RepositoryID: 4, Workdir: "/caller/evil", BaseCommit: strings.Repeat("a", 40), Objective: "Implement the assigned work", AuthorizationMode: "READ_ONLY"}}
}
func (f *executionFixture) pull(t *testing.T, id guid.ID) ExecutionView {
	t.Helper()
	var rows []ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; rows, e = f.m.PullOn(f.runner, tx, 7, f.fence); return e }))
	require.Len(t, rows, 1)
	require.Equal(t, id, rows[0].ID)
	return rows[0]
}
func (f *executionFixture) receipt(id guid.ID, in ReceiptInput) (ReceiptView, error) {
	var out ReceiptView
	var prepareErr error
	in, prepareErr = f.m.PrepareReceipt(f.runner, 7, id, f.fence, in)
	if prepareErr != nil {
		return out, prepareErr
	}
	err := f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = f.m.ReceiptOn(f.runner, tx, 7, id, f.fence, in)
		return e
	})
	return out, err
}
func (f *executionFixture) read(t *testing.T, id guid.ID) ExecutionView {
	t.Helper()
	row, err := query.BindExecutions(f.db, f.m.names).WithContext(t.Context()).Where(query.BindExecutions(f.db, f.m.names).ID.Eq(id)).First()
	require.NoError(t, err)
	v, err := view(row)
	require.NoError(t, err)
	return v
}

func TestCreateImmutableWorkspaceAuthorizationAndRollback(t *testing.T) {
	f := executionHarness(t)
	f.pub.fail = true
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CreateOn(f.human, tx, 7, validCreate()); return e }))
	for _, table := range []string{f.m.names.Executions.String(), "slots", "effects"} {
		var n int64
		require.NoError(t, f.db.Table(table).Count(&n).Error)
		require.Zero(t, n)
	}
	f.pub.fail = false
	v := f.create(t)
	require.Equal(t, "/registered/workspace", v.Plan.Workdir)
	require.Equal(t, guid.ID(5), v.Plan.WorkdirID)
	require.Len(t, v.PlanHash, 64)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CreateOn(f.human, tx, 7, validCreate()); return e }), ErrConflict)
	f.humanActive = false
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CreateOn(f.human, tx, 7, validCreate()); return e }), ErrForbidden)
	f.humanActive = true
	f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return ErrCapabilityUnavailable }
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CreateOn(f.human, tx, 7, validCreate()); return e }), ErrCapabilityUnavailable)
}
func TestPlanHashTamperingAndArgumentBounds(t *testing.T) {
	f := executionHarness(t)
	for _, mutate := range []func(*ExecutionCreate){func(c *ExecutionCreate) { c.Plan.Runtime = "SHELL" }, func(c *ExecutionCreate) { c.Plan.BaseCommit = "HEAD" }, func(c *ExecutionCreate) { c.Plan.Objective = " " }, func(c *ExecutionCreate) { c.Plan.Model = "x\n" }, func(c *ExecutionCreate) { c.Plan.AuthorizationMode = "BYPASS" }} {
		in := validCreate()
		mutate(&in)
		require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CreateOn(f.human, tx, 7, in); return e }), ErrInvalidArgument)
	}
	v := f.create(t)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("plan_json", "{}").Error)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.PullOn(f.runner, tx, 7, f.fence); return e }), ErrUnavailable)
}
func TestCallerTransactionRequired(t *testing.T) {
	f := executionHarness(t)
	_, err := f.m.CreateOn(f.human, f.db, 7, validCreate())
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.ErrorIs(t, transaction(nil, 7, 1), ErrInvalidArgument)
	require.False(t, commitHash(strings.Repeat("g", 40)))
	require.True(t, commitHash(strings.Repeat("a", 64)))
	require.False(t, bounded("bad\n", 8))
}

// Each storage operation in a successful command is failed in isolation. The
// entire fixture database must remain unchanged, including journals and slots.
func executionStorageFailures(t *testing.T, setup func(*testing.T) (*executionFixture, func(*gorm.DB) error), httpErrors ...bool) {
	t.Helper()
	for _, kind := range []string{"query", "create", "update"} {
		exercise := func(t *testing.T, failAt int) int {
			t.Helper()
			f, run := setup(t)
			tables, err := f.db.Migrator().GetTables()
			require.NoError(t, err)
			snapshot := func() map[string][]map[string]any {
				out := map[string][]map[string]any{}
				for _, table := range tables {
					var rows []map[string]any
					require.NoError(t, f.db.Table(table).Order("rowid").Find(&rows).Error)
					out[table] = rows
				}
				return out
			}
			before := snapshot()
			count := 0
			failure := errors.New("execution command storage unavailable")
			hook := func(tx *gorm.DB) {
				// Dry-run callbacks compile subqueries without touching storage.
				if tx.DryRun {
					return
				}
				count++
				if count == failAt {
					tx.AddError(failure)
				}
			}
			const name = "execution_command_storage_failure"
			switch kind {
			case "query":
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register(name, hook))
			case "create":
				require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register(name, hook))
			case "update":
				require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register(name, hook))
			}
			if len(httpErrors) > 0 && httpErrors[0] || len(httpErrors) > 1 && httpErrors[1] {
				err = run(f.db)
			} else {
				err = f.db.Transaction(run)
			}
			switch kind {
			case "query":
				require.NoError(t, f.db.Callback().Query().Remove(name))
			case "create":
				require.NoError(t, f.db.Callback().Create().Remove(name))
			case "update":
				require.NoError(t, f.db.Callback().Update().Remove(name))
			}
			if failAt == 0 {
				require.NoError(t, err)
			} else {
				require.GreaterOrEqual(t, count, failAt)
				if len(httpErrors) > 0 && httpErrors[0] {
					var status huma.StatusError
					require.ErrorAs(t, err, &status)
					require.Equal(t, 503, status.GetStatus())
				} else {
					require.ErrorIs(t, err, failure)
				}
				require.Equal(t, before, snapshot(), "failed command must roll back every persisted effect")
			}
			return count
		}
		total := 0
		t.Run(kind+"/success", func(t *testing.T) { total = exercise(t, 0) })
		for at := 1; at <= total; at++ {
			t.Run(fmt.Sprintf("%s/failure_%d", kind, at), func(t *testing.T) { exercise(t, at) })
		}
	}
}
