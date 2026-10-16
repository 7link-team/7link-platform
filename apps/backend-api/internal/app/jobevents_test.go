package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

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
				require.NoError(t, wireJobEvents(services))
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
	require.ErrorIs(t, wireJobEvents(services), module.ErrServicesProviding)
	require.NoError(t, services.Seal())
	require.NoError(t, wireJobEvents(services))
	services = module.NewServices()
	require.NoError(t, module.Provide[jobs.DeadLetterConnector](services, jobs.DeadLetterConnector{}))
	require.NoError(t, services.Seal())
	require.ErrorIs(t, wireJobEvents(services), jobs.ErrUnavailable)
	services = module.NewServices()
	require.NoError(t, module.Provide[jobs.DeadLetterConnector](services, jobs.DeadLetterConnector{Bind: func(func(context.Context, *gorm.DB, jobs.DeadLetter) error) error { return nil }}))
	require.NoError(t, services.Seal())
	require.ErrorIs(t, wireJobEvents(services), module.ErrServiceMissing)
}

func TestJobTenantBridgePreservesActiveReaderContract(t *testing.T) {
	services := module.NewServices()
	require.ErrorIs(t, wireJobTenants(services), module.ErrServicesProviding)
	require.NoError(t, services.Seal())
	require.NoError(t, wireJobTenants(services))
	services = module.NewServices()
	require.NoError(t, module.Provide[jobs.TenantFanoutConnector](services, jobs.TenantFanoutConnector{}))
	require.NoError(t, services.Seal())
	require.ErrorIs(t, wireJobTenants(services), jobs.ErrUnavailable)
	services = module.NewServices()
	var captured func(context.Context, guid.ID, int) ([]guid.ID, error)
	require.NoError(t, module.Provide[jobs.TenantFanoutConnector](services, jobs.TenantFanoutConnector{Bind: func(reader func(context.Context, guid.ID, int) ([]guid.ID, error)) error {
		captured = reader
		return nil
	}}))
	sentinel := errors.New("active reader failed")
	require.NoError(t, module.Provide[organization.ActiveTenantEnumerator](services, func(_ context.Context, after guid.ID, limit int) ([]guid.ID, error) {
		require.Equal(t, guid.ID(500), after)
		require.Equal(t, 500, limit)
		return nil, sentinel
	}))
	require.NoError(t, services.Seal())
	require.NoError(t, wireJobTenants(services))
	require.NotNil(t, captured)
	_, err := captured(t.Context(), 500, 500)
	require.ErrorIs(t, err, sentinel)
}
