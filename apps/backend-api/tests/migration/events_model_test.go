package migration_test

import (
	"errors"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/events/model"
	goosex "7link.com/platform/pkg/extension/goose"
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
				})
			}
		})
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
