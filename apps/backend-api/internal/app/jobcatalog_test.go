package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	businessgovernance "7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type jobHandlerContribution struct {
	name     string
	enabled  bool
	handlers []jobs.Handler
}

func (m jobHandlerContribution) ModuleName() string          { return m.name }
func (m jobHandlerContribution) Enabled() bool               { return m.enabled }
func (m jobHandlerContribution) JobHandlers() []jobs.Handler { return m.handlers }

type jobScheduleContribution struct {
	name      string
	schedules []jobs.Schedule
}

func (m jobScheduleContribution) ModuleName() string            { return m.name }
func (m jobScheduleContribution) JobSchedules() []jobs.Schedule { return m.schedules }

func catalogApp(t *testing.T, contributions ...module.Module) *App {
	t.Helper()
	app := &App{}
	require.NoError(t, app.initializeRuntime(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })
	require.NoError(t, app.composeModules())
	require.NoError(t, app.modules.Register(contributions...))
	return app
}

func TestJobCatalogDiscoversActivatedProvidersBeforeSealing(t *testing.T) {
	handler := jobs.Handler{Kind: "worker.run", Run: func(context.Context, jobs.Job) error { return nil }}
	schedule := jobs.Schedule{Name: "timer.hourly", Kind: handler.Kind, Interval: time.Hour}
	app := catalogApp(t,
		jobScheduleContribution{name: "timer", schedules: []jobs.Schedule{schedule}},
		jobHandlerContribution{name: "disabled", handlers: []jobs.Handler{{Kind: "invalid"}}},
		jobHandlerContribution{name: "worker", enabled: true, handlers: []jobs.Handler{handler}},
	)
	require.NoError(t, app.prepareModules())
	catalog, err := module.Resolve[jobs.Catalog](app.modules.Services())
	require.NoError(t, err)
	// Both declarations reached the actual jobs catalog; duplicate registration
	// must fail before sealing rather than hiding a missing discovery call.
	require.ErrorIs(t, catalog.AddHandlers("worker", handler), jobs.ErrInvalidDeclaration)
	require.ErrorIs(t, catalog.AddSchedules("timer", schedule), jobs.ErrInvalidDeclaration)
	worker, ok := app.modules.ByName(jobs.Name)
	require.True(t, ok)
	require.NoError(t, worker.(*jobs.Module).Initialize(t.Context()))
	require.ErrorIs(t, catalog.AddHandlers("worker", handler), jobs.ErrCatalogSealed)
}

func TestJobCatalogRejectsInvalidDeclarationsAndUnknownReferences(t *testing.T) {
	t.Run("handler", func(t *testing.T) {
		app := catalogApp(t, jobHandlerContribution{name: "worker", enabled: true, handlers: []jobs.Handler{{Kind: "foreign.run", Run: func(context.Context, jobs.Job) error { return nil }}}})
		require.ErrorIs(t, app.prepareModules(), jobs.ErrInvalidDeclaration)
	})
	t.Run("schedule", func(t *testing.T) {
		app := catalogApp(t, jobScheduleContribution{name: "timer", schedules: []jobs.Schedule{{Name: "timer.bad", Kind: "worker.run"}}})
		require.ErrorIs(t, app.prepareModules(), jobs.ErrInvalidDeclaration)
	})
	t.Run("reference", func(t *testing.T) {
		app := catalogApp(t, jobScheduleContribution{name: "timer", schedules: []jobs.Schedule{{Name: "timer.hourly", Kind: "missing.run", Interval: time.Hour}}})
		require.NoError(t, app.prepareModules())
		worker, ok := app.modules.ByName(jobs.Name)
		require.True(t, ok)
		require.ErrorIs(t, worker.(*jobs.Module).Initialize(t.Context()), jobs.ErrUnknownKind)
	})
}

func TestJobCatalogRequiresServiceOnlyForContributors(t *testing.T) {
	for _, contributes := range []bool{false, true} {
		registry := module.NewRegistry()
		require.NoError(t, registry.Register(&namedHealthModule{name: "plain"}))
		if contributes {
			require.NoError(t, registry.Register(jobHandlerContribution{name: "worker", enabled: true}))
		}
		require.NoError(t, registry.Activate())
		require.NoError(t, registry.Wire(nil))
		err := wireJobCatalog(registry)
		if contributes {
			require.ErrorIs(t, err, module.ErrServiceMissing)
		} else {
			require.NoError(t, err)
		}
	}
}

func TestJobCatalogPreservesGovernanceLogicalNamespace(t *testing.T) {
	app := &App{config: AppConfig{Authn: IAMAuthn{Enabled: true}}}
	require.NoError(t, app.initializeRuntime(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })
	require.NoError(t, app.composeModules())
	current, found := app.modules.ByName(businessgovernance.Name)
	require.True(t, found)
	governance := current.(*businessgovernance.Module)
	require.NoError(t, app.prepareModules())
	worker, ok := app.modules.ByName(jobs.Name)
	require.True(t, ok)
	require.NoError(t, worker.(*jobs.Module).Initialize(t.Context()))
	// Preparation succeeded with the real business_governance module even
	// though its handler and schedule names use the frozen governance prefix.
	require.Equal(t, "governance.seed_reconcile", governance.JobHandlers()[0].Kind)
}

func TestGovernanceSeedUsesRealIAMTransactionsAndAudit(t *testing.T) {
	app := bootstrapComposedApp(t)
	db := app.runtime.Database().Write()
	current, found := app.modules.ByName(businessgovernance.Name)
	require.True(t, found)
	governance := current.(*businessgovernance.Module)
	roles, err := module.Resolve[businessgovernance.SeededRoles](app.modules.Services())
	require.NoError(t, err)
	var startup struct {
		Kind           string
		TenantID       guid.ID
		ConcurrencyKey string
	}
	require.NoError(t, db.Table("background_jobs").Where("kind = ?", "governance.seed_reconcile").Take(&startup).Error)
	require.Zero(t, startup.TenantID)
	require.Equal(t, startup.Kind, startup.ConcurrencyKey)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, tenant := range []int{101, 102} {
		require.NoError(t, db.Exec("INSERT INTO iam_tenants (id,slug,name,status,version,created_at,updated_at) VALUES (?,?,?,'active',1,?,?)", tenant, fmt.Sprint(tenant), "Seed integration", now, now).Error)
		require.NoError(t, db.Exec("INSERT INTO iam_principals (id,login_name,display_name,status,created_at,updated_at) VALUES (?,?,?,'active',?,?)", tenant, fmt.Sprintf("seed-%d", tenant), "Seed initiator", now, now).Error)
		require.NoError(t, db.Exec("INSERT INTO iam_tenant_members (tenant_id,principal_id,status,created_at,updated_at) VALUES (?,?,'active',?,?)", tenant, tenant, now, now).Error)
	}
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 101, PrincipalID: 101})
	require.NoError(t, roles.EnsureTenant(actor, 101))
	count := func(table string, tenant int) int64 {
		t.Helper()
		var n int64
		require.NoError(t, db.Table(table).Where("tenant_id = ?", tenant).Count(&n).Error)
		return n
	}
	require.EqualValues(t, 14, count("governance_role_seeds", 101))
	require.EqualValues(t, 14, count("iam_roles", 101))
	auditCount := count("iam_audit_events", 101)
	require.Greater(t, auditCount, int64(14))
	var event struct {
		PrincipalID guid.ID
		Detail      string
	}
	require.NoError(t, db.Table("iam_audit_events").Where("tenant_id=101 AND event_type='governance.role_seed_applied'").Take(&event).Error)
	require.Equal(t, guid.ID(101), event.PrincipalID)
	require.Contains(t, event.Detail, "team_owner")
	var owner guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		owner, err = roles.RoleIDOn(actor, tx, 101, "team_owner")
		return err
	}))
	var projectManage int64
	require.NoError(t, db.Table("iam_role_permissions").Where("tenant_id=101 AND role_id=? AND permission_code='project_manage'", owner).Count(&projectManage).Error)
	require.EqualValues(t, 1, projectManage, "delivery must contribute project permissions before governance seeds roles")
	var original int64
	require.NoError(t, db.Table("iam_role_permissions").Where("tenant_id=101 AND role_id=? AND permission_code='role_view'", owner).Count(&original).Error)
	require.EqualValues(t, 1, original)
	// Simulate a persisted administrator revocation. The seed ledger, rather
	// than the continued presence of the grant, governs subsequent reconciliation.
	require.NoError(t, db.Exec("DELETE FROM iam_role_permissions WHERE tenant_id=101 AND role_id=? AND permission_code='role_view'", owner).Error)
	require.NoError(t, roles.EnsureTenant(actor, 101))
	require.Equal(t, auditCount, count("iam_audit_events", 101))
	require.NoError(t, db.Table("iam_role_permissions").Where("tenant_id=101 AND role_id=? AND permission_code='role_view'", owner).Count(&original).Error)
	require.Zero(t, original)
	require.NoError(t, db.Exec("CREATE TRIGGER fail_seed_final_audit BEFORE INSERT ON iam_audit_events WHEN NEW.tenant_id=102 AND NEW.event_type='governance.role_seed_applied' BEGIN SELECT RAISE(ABORT,'injected final audit failure'); END").Error)
	other := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 102, PrincipalID: 102})
	require.ErrorContains(t, roles.EnsureTenant(other, 102), "injected final audit failure")
	for _, table := range []string{"governance_role_seeds", "iam_roles", "iam_role_permissions", "iam_audit_events", "iam_audit_heads", "iam_policy_revisions"} {
		require.Zero(t, count(table, 102), table)
	}
	require.NoError(t, db.Exec("DROP TRIGGER fail_seed_final_audit").Error)
	require.NoError(t, governance.JobHandlers()[0].Run(t.Context(), jobs.Job{}))
	require.EqualValues(t, 14, count("governance_role_seeds", 102))
	require.EqualValues(t, 14, count("iam_roles", 102))
}
