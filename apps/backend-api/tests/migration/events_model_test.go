package migration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/events/model"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestEventsModelMatchesMigratedSchemas(t *testing.T) {
	for _, engine := range []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	} {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			for _, prefix := range []string{"", deploymentPrefix, maximumPrefix} {
				t.Run(prefix, func(t *testing.T) {
					source := (&events.Module{}).MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
					assertModelMatchesNamedTable(t, db, engine.dialect, model.EventRow{}, prefix+"domain_events")
					assertModelMatchesNamedTable(t, db, engine.dialect, model.Cursor{}, prefix+"domain_event_cursors")
					assertModelMatchesNamedTable(t, db, engine.dialect, model.Receipt{}, prefix+"domain_event_receipts")
					assertModelMatchesNamedTable(t, db, engine.dialect, model.Parked{}, prefix+"domain_event_parked")
				})
			}
		})
	}
}

func TestPostgresSequencerNotifiesIndependentTailListener(t *testing.T) {
	_, dsn := testdb.OpenPostgresWithDSN(t)
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "postgres", DSN: dsn, Writable: true, Readable: true, MaxOpenConns: 1}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(deploymentPrefix))
	publisher := events.NewModule(rt)
	locks := dlock.NewModule(rt)
	db := router.Write()
	raw, err := db.DB()
	require.NoError(t, err)
	for _, source := range []goosex.Source{locks.MigrationSource(), publisher.MigrationSource()} {
		source.Prefix = deploymentPrefix
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "postgres"))
	}
	ids, err := guid.New(1)
	require.NoError(t, err)
	s := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](s, ids))
	require.NoError(t, locks.ProvideServices(s))
	require.NoError(t, publisher.ProvideServices(s))
	require.NoError(t, s.Seal())
	require.NoError(t, publisher.InjectServices(s))
	require.NoError(t, publisher.AddTypes("test", events.TypeSpec{Name: "project.created", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}))
	require.NoError(t, publisher.Initialize(t.Context()))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wake, err := publisher.Wakeups(ctx)
	require.NoError(t, err)
	require.NotNil(t, wake)
	// One writer slot proves LISTEN holds its own connection instead of starving
	// the business transaction or the sequence transaction.
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("PostgreSQL sequence notification not delivered")
		case <-wake:
			page, err := publisher.Tail(t.Context(), 0, 100, false)
			require.NoError(t, err)
			require.NotEmpty(t, page.Events)
			return
		case <-tick.C:
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := publisher.Publish(t.Context(), tx, events.Event{TenantID: 1, Type: "project.created", Actor: events.Actor{Kind: "SYSTEM"}, Subject: events.Subject{Kind: "project", ID: 2}})
				return err
			}))
			_, err = publisher.Sequence(t.Context())
			require.NoError(t, err)
		}
	}
}

// Exercise the real publisher through its public lifecycle, including the
// PostgreSQL notification query, against engines with real transaction semantics.
func TestEventsPublisherTransactionsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	} {
		t.Run(engine.name, func(t *testing.T) {
			sqlDB := engine.open(t)
			db := openMilestoneGORM(t, sqlDB, engine.dialect)
			publisher := events.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix(deploymentPrefix)))
			source := publisher.MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, engine.dialect))
			services := module.NewServices()
			ids, err := guid.New(1)
			require.NoError(t, err)
			require.NoError(t, module.Provide[guid.Source](services, ids))
			// This fixture only publishes; attempting to start a sequencer without
			// its runtime-owned writer must remain unavailable.
			require.NoError(t, module.Provide[dlock.TransactionGuard](services, func(context.Context, *gorm.DB, string) error { return events.ErrUnavailable }))
			require.NoError(t, module.Provide[dlock.TryRenewingLease](services, func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
				return nil, dlock.ErrNotAcquired
			}))
			require.NoError(t, publisher.ProvideServices(services))
			require.NoError(t, services.Seal())
			require.NoError(t, publisher.InjectServices(services))
			catalog, err := module.Resolve[events.Catalog](services)
			require.NoError(t, err)
			require.NoError(t, catalog.AddTypes("delivery", events.TypeSpec{Name: "project.created", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}))
			require.NoError(t, publisher.Initialize(t.Context()))
			outbox, err := module.Resolve[events.Publisher](services)
			require.NoError(t, err)
			require.NoError(t, db.Exec("CREATE TABLE business_probe (id BIGINT PRIMARY KEY)").Error)
			abort := errors.New("project creation failed after publication")
			for _, rollback := range []bool{true, false} {
				err = db.Transaction(func(tx *gorm.DB) error {
					if err := tx.Exec("INSERT INTO business_probe VALUES (2)").Error; err != nil {
						return err
					}
					_, err := outbox.Publish(t.Context(), tx, events.Event{
						TenantID: 1, ProjectID: 2, Type: "project.created", Actor: events.Actor{Kind: "SYSTEM"},
						Subject: events.Subject{Kind: "project", ID: 2}, Data: map[string]any{"name": "Project"},
					})
					if err != nil {
						return err
					}
					if rollback {
						return abort
					}
					return nil
				})
				if rollback {
					require.ErrorIs(t, err, abort)
				} else {
					require.NoError(t, err)
				}
				expected := int64(1)
				if rollback {
					expected = 0
				}
				for _, table := range []string{"business_probe", deploymentPrefix + "domain_events"} {
					var count int64
					require.NoError(t, db.Table(table).Count(&count).Error)
					require.Equal(t, expected, count)
				}
			}
			var rows []model.EventRow
			require.NoError(t, db.Table(deploymentPrefix+"domain_events").Find(&rows).Error)
			require.Len(t, rows, 1)
			require.Nil(t, rows[0].ActorID)
			require.Nil(t, rows[0].StreamSeq)
			require.Nil(t, rows[0].SequencedAt)
			require.JSONEq(t, `["tenant/1/project/2"]`, rows[0].Topics)
			require.JSONEq(t, `{"name":"Project"}`, rows[0].Data)
		})
	}
}

func TestEventCursorConstraintsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&events.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			if engine.dialect == "mysql" {
				return
			}
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := deploymentPrefix + "domain_event_cursors"
			insert := func(id int64, partition int, last, fence int64) error {
				return db.Table(table).Create(&model.Cursor{ID: guid.ID(id), Consumer: "test", PartitionNo: partition, LastSeq: last, FenceToken: fence, UpdatedAt: time.Now().UTC()}).Error
			}
			require.NoError(t, insert(1, 0, 0, 0))
			require.Error(t, insert(2, 0, 0, 0))
			require.NoError(t, insert(3, 15, 0, 0))
			for _, v := range []struct {
				partition   int
				last, fence int64
			}{{-1, 0, 0}, {16, 0, 0}, {1, -1, 0}, {1, 0, -1}} {
				require.Error(t, insert(4, v.partition, v.last, v.fence))
			}
		})
	}
}

type purgeSnapshotKey struct{}

func TestPostgresReplayAndPurgeCoordinateRetainedRange(t *testing.T) {
	_, dsn := testdb.OpenPostgresWithDSN(t)
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "postgres", DSN: dsn, Writable: true, Readable: true, MaxOpenConns: 4}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(deploymentPrefix))
	publisher := events.NewModule(rt)
	locks := dlock.NewModule(rt)
	db := router.Write()
	raw, err := db.DB()
	require.NoError(t, err)
	for _, source := range []goosex.Source{locks.MigrationSource(), publisher.MigrationSource()} {
		source.Prefix = deploymentPrefix
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "postgres"))
	}
	ids, err := guid.New(1)
	require.NoError(t, err)
	s := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](s, ids))
	require.NoError(t, locks.ProvideServices(s))
	require.NoError(t, publisher.ProvideServices(s))
	require.NoError(t, s.Seal())
	require.NoError(t, publisher.InjectServices(s))
	require.NoError(t, publisher.AddTypes("test", events.TypeSpec{Name: "project.created", SchemaVersion: 1, SubjectKind: "project", Realtime: events.RealtimeSignal}))
	require.NoError(t, publisher.AddSubscribers("test", events.Subscriber{Name: "test.purge", StartFrom: events.Earliest, EventTypes: []string{"project.*"}, Handle: func(context.Context, *gorm.DB, events.Envelope) error { return nil }}))
	require.NoError(t, publisher.Initialize(t.Context()))
	for i := 0; i < 3; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, x := publisher.Publish(t.Context(), tx, events.Event{TenantID: 1, Type: "project.created", Actor: events.Actor{Kind: "SYSTEM"}, Subject: events.Subject{Kind: "project", ID: 2}})
			return x
		}))
	}
	_, err = publisher.Sequence(t.Context())
	require.NoError(t, err)
	require.NoError(t, publisher.SetPaused(t.Context(), "test.purge", false))
	require.NoError(t, db.Table(deploymentPrefix+"domain_events").Where("id > 0").Update("occurred_at", time.Now().Add(-8*24*time.Hour)).Error)
	require.NoError(t, db.Table(deploymentPrefix+"domain_event_cursors").Where("consumer = ?", "test.purge").Update("last_seq", 3).Error)
	snapshot := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("hold_purge_snapshot", func(tx *gorm.DB) {
		if tx.Statement.Table == deploymentPrefix+"domain_event_cursors" && tx.Statement.Context.Value(purgeSnapshotKey{}) == true {
			once.Do(func() { close(snapshot); <-release })
		}
	}))
	defer func() { require.NoError(t, db.Callback().Query().Remove("hold_purge_snapshot")) }()
	purgeDone := make(chan error, 1)
	go func() {
		_, x := publisher.Purge(context.WithValue(t.Context(), purgeSnapshotKey{}, true))
		purgeDone <- x
	}()
	select {
	case <-snapshot:
	case <-time.After(3 * time.Second):
		t.Fatal("purge did not reach snapshot barrier")
	}
	replayDone := make(chan error, 1)
	go func() {
		replayDone <- db.Transaction(func(tx *gorm.DB) error {
			_, x := publisher.ReplayOn(t.Context(), tx, "test.purge", 1, 2, true)
			return x
		})
	}()
	completedEarly := false
	var replayErr error
	select {
	case replayErr = <-replayDone:
		completedEarly = true
	case <-time.After(150 * time.Millisecond):
	}
	unblock.Do(func() { close(release) })
	require.NoError(t, <-purgeDone)
	if !completedEarly {
		select {
		case replayErr = <-replayDone:
		case <-time.After(3 * time.Second):
			t.Fatal("replay remained blocked")
		}
	}
	require.False(t, completedEarly, "replay must wait until purge's retained-range decision commits")
	require.ErrorIs(t, replayErr, events.ErrReplayRange)
}
