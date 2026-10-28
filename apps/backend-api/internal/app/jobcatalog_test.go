package app

import (
	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	businessgovernance "7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
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
	translations, found := app.modules.ByName(platformi18n.Name)
	require.True(t, found)
	require.NoError(t, translations.(module.Initializer).Initialize(t.Context()))
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
	app := &App{config: AppConfig{Authn: IAMAuthn{Enabled: true}, Secrets: testBusinessSecretsConfig(t)}}
	require.NoError(t, app.initializeRuntime(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })
	require.NoError(t, app.composeModules())
	translations, found := app.modules.ByName(platformi18n.Name)
	require.True(t, found)
	require.NoError(t, translations.(module.Initializer).Initialize(t.Context()))
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

func TestProductionJobProvidersHaveLocalizedNamesAndPurposes(t *testing.T) {
	app := bootstrapComposedApp(t)
	_, api := humatest.New(t)
	require.NoError(t, app.modules.RegisterHTTP(t.Context(), api))
	count := 0
	for _, current := range app.modules.Modules() {
		provider, ok := current.(jobs.HandlerProvider)
		if !ok {
			continue
		}
		for _, handler := range provider.JobHandlers() {
			count++
			identity := ".activity_job_" + strings.ReplaceAll(handler.Kind, ".", "_")
			require.True(t, strings.HasSuffix(handler.DisplayNameKey, identity+"_name"), handler.Kind)
			require.True(t, strings.HasSuffix(handler.DescriptionKey, identity+"_description"), handler.Kind)
			for _, key := range []string{handler.DisplayNameKey, handler.DescriptionKey} {
				require.NotEmpty(t, key, "%s: %s", current.ModuleName(), handler.Kind)
				for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
					text := platformi18n.TContext(platformi18n.WithLocale(t.Context(), locale), key)
					require.NotEqual(t, key, text, "%s: %s in %s", handler.Kind, key, locale)
					require.NotEmpty(t, text)
				}
			}
		}
	}
	require.Greater(t, count, 50, "exercise the production providers, not only test declarations")
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
	// Repeated module starts (including new instances in another replica) use
	// the same persisted receipt instead of growing the pending queue.
	for range 3 {
		require.NoError(t, governance.Start(t.Context()))
	}
	var startupCount int64
	require.NoError(t, db.Table("background_jobs").Where("kind = ?", startup.Kind).Count(&startupCount).Error)
	require.EqualValues(t, 1, startupCount)

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
	require.EqualValues(t, 15, count("governance_role_seeds", 101))
	require.EqualValues(t, 15, count("iam_roles", 101))
	auditCount := count("iam_audit_events", 101)
	require.Greater(t, auditCount, int64(15))
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
	require.EqualValues(t, 15, count("governance_role_seeds", 102))
	require.EqualValues(t, 15, count("iam_roles", 102))
}

type jobEventRecorder struct{}

func (jobEventRecorder) Record(context.Context, audit.Event) {}

type failingJobEventPublisher struct{ events.Publisher }

func (p failingJobEventPublisher) Publish(ctx context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
	id, err := p.Publisher.Publish(ctx, tx, event)
	if err != nil {
		return id, err
	}
	return id, errors.New("failure after actual outbox insert")
}

func TestJobDeadLetterAndRealOutboxCommitAtomically(t *testing.T) {
	for _, tenant := range []guid.ID{0, 1} {
		for _, rollback := range []bool{false, true} {
			t.Run(fmt.Sprintf("tenant%d/rollback%t", tenant, rollback), func(t *testing.T) {
				router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "jobevents.sqlite"), Writable: true, Readable: true}})
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, router.Close()) })
				db := router.Write()
				raw, err := db.DB()
				require.NoError(t, err)
				rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix("je_"))
				locks := dlock.NewModule(rt)
				publisher := events.NewModule(rt)
				worker := jobs.NewModule(rt, jobs.Config{Queues: map[string]int{"default": 1}, PollInterval: 10 * time.Millisecond, DrainTimeout: time.Second})
				for _, source := range []goosex.Source{locks.MigrationSource(), publisher.MigrationSource(), worker.MigrationSource()} {
					source.Prefix = "je_"
					require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
				}
				services := module.NewServices()
				require.NoError(t, locks.ProvideServices(services))
				ids, err := guid.New(1)
				require.NoError(t, err)
				require.NoError(t, module.Provide[guid.Source](services, ids))
				require.NoError(t, module.Provide[audit.Recorder](services, jobEventRecorder{}))
				var outbox events.Publisher = publisher
				if rollback {
					outbox = failingJobEventPublisher{Publisher: publisher}
				}
				require.NoError(t, module.Provide[events.Publisher](services, outbox))
				require.NoError(t, module.Provide[events.Catalog](services, publisher))
				require.NoError(t, worker.ProvideServices(services))
				require.NoError(t, services.Seal())
				require.NoError(t, publisher.InjectServices(services))
				require.NoError(t, worker.InjectServices(services))
				require.NoError(t, jobs.WireJobEvents(services))
				entered := make(chan struct{})
				contributions := module.NewRegistry()
				require.NoError(t, contributions.Register(jobHandlerContribution{name: "jobs", enabled: true, handlers: []jobs.Handler{{Kind: "jobs.work", Run: func(context.Context, jobs.Job) error {
					close(entered)
					return jobs.Permanent(errors.New("password=private-token"))
				}}}}))
				require.NoError(t, contributions.Activate())
				require.NoError(t, contributions.Wire(func(s *module.Services) error {
					return module.Provide[jobs.Catalog](s, worker)
				}))
				require.NoError(t, wireJobCatalog(contributions))
				require.NoError(t, publisher.Initialize(t.Context()))
				require.NoError(t, worker.Initialize(t.Context()))
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					require.NoError(t, worker.Stop(ctx))
				})
				queued, err := worker.Enqueue(t.Context(), nil, jobs.EnqueueRequest{TenantID: tenant, Kind: "jobs.work", Payload: map[string]string{"password": "private-token"}})
				require.NoError(t, err)
				require.NoError(t, worker.Start(t.Context()))
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("job did not execute")
				}
				require.NoError(t, worker.Quiesce(t.Context()))
				var job struct {
					State    string
					Version  int
					Attempts int
				}
				require.NoError(t, db.Table("je_background_jobs").Where("id = ?", queued.JobID).Take(&job).Error)
				var rows []struct {
					TenantID       guid.ID
					SubjectID      guid.ID
					SubjectVersion int
					EventType      string
					RealtimeMode   string
					Data           string
					Topics         string
					ActorKind      string
				}
				require.NoError(t, db.Table("je_domain_events").Find(&rows).Error)
				if rollback {
					require.Equal(t, "RUNNING", job.State)
					require.Empty(t, rows)
					return
				}
				require.Equal(t, "DEAD", job.State)
				require.Len(t, rows, 1)
				event := rows[0]
				require.Equal(t, tenant, event.TenantID)
				require.Equal(t, queued.JobID, event.SubjectID)
				require.Equal(t, job.Version, event.SubjectVersion)
				require.Equal(t, "job.dead_lettered", event.EventType)
				require.Equal(t, "SYSTEM", event.ActorKind)
				require.Equal(t, "NONE", event.RealtimeMode)
				require.JSONEq(t, `{"kind":"jobs.work","attempts":1}`, event.Data)
				require.NotContains(t, event.Data, "private-token")
				require.JSONEq(t, fmt.Sprintf(`["tenant/%d/background_job/%s"]`, tenant, queued.JobID.String()), event.Topics)
				require.NoError(t, worker.Stop(t.Context()))
				var count int64
				require.NoError(t, db.Table("je_domain_events").Count(&count).Error)
				require.EqualValues(t, 1, count)
			})
		}
	}
}

func TestJobEventWiringRequiresDependenciesOnlyWhenJobsPresent(t *testing.T) {
	services := module.NewServices()
	require.ErrorIs(t, jobs.WireJobEvents(services), module.ErrServicesProviding)
	require.NoError(t, services.Seal())
	require.NoError(t, jobs.WireJobEvents(services))
	services = module.NewServices()
	require.NoError(t, module.Provide[jobs.DeadLetterConnector](services, jobs.DeadLetterConnector{}))
	require.NoError(t, services.Seal())
	require.ErrorIs(t, jobs.WireJobEvents(services), jobs.ErrUnavailable)
	services = module.NewServices()
	require.NoError(t, module.Provide[jobs.DeadLetterConnector](services, jobs.DeadLetterConnector{Bind: func(func(context.Context, *gorm.DB, jobs.DeadLetter) error) error { return nil }}))
	require.NoError(t, services.Seal())
	require.ErrorIs(t, jobs.WireJobEvents(services), module.ErrServiceMissing)
}
