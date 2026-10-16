package migration_test

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/idempotency/model"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestIdempotencySchemasAndTransactionsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			for _, prefix := range []string{"", deploymentPrefix, maximumPrefix} {
				source := (&idempotency.Module{}).MigrationSource()
				source.Prefix = prefix
				require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
				assertModelMatchesNamedTable(t, raw, engine.dialect, model.KeyRow{}, prefix+"platform_idempotency_keys")
			}
			db := openMilestoneGORM(t, raw, engine.dialect)
			// The in-memory MySQL parser validates the schema, not transactions/savepoints.
			if engine.dialect == "mysql" {
				return
			}
			m := idempotency.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix(deploymentPrefix)))
			services := module.NewServices()
			ids, err := guid.New(1)
			require.NoError(t, err)
			require.NoError(t, module.Provide[guid.Source](services, ids))
			require.NoError(t, m.ProvideServices(services))
			require.NoError(t, services.Seal())
			require.NoError(t, m.InjectServices(services))
			require.NoError(t, platformi18n.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), platformi18n.Base).Initialize(t.Context()))
			require.NoError(t, m.Initialize(t.Context()))
			store, err := module.Resolve[idempotency.Store](services)
			require.NoError(t, err)
			hash, err := idempotency.HashRequest("POST", "/projects", nil, map[string]int{"amount": 9})
			require.NoError(t, err)
			key := idempotency.Key{TenantID: 1, SubjectKind: "PRINCIPAL", SubjectID: 11, Operation: "project.create", Value: "one", RequestHash: hash}
			require.NoError(t, db.Exec("CREATE TABLE idempotency_business_probe (id BIGINT PRIMARY KEY)").Error)
			callback := func(tx *gorm.DB) (idempotency.Result, error) {
				return idempotency.Result{StatusCode: 201, Body: json.RawMessage(`{"data":{"id":"1"}}`), ResourceKind: "project", ResourceID: 1}, tx.Exec("INSERT INTO idempotency_business_probe VALUES (1)").Error
			}
			abort := errors.New("outer abort")
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, _, err := store.Do(t.Context(), tx, key, callback)
				require.NoError(t, err)
				return abort
			}), abort)
			first := db.Begin()
			require.NoError(t, first.Error)
			t.Cleanup(func() { _ = first.Rollback().Error })
			_, replayed, err := store.Do(t.Context(), first, key, callback)
			require.NoError(t, err)
			require.False(t, replayed)
			started, done := make(chan struct{}), make(chan error, 1)
			go func() {
				close(started)
				done <- db.Transaction(func(tx *gorm.DB) error {
					result, replayed, err := store.Do(t.Context(), tx, key, func(*gorm.DB) (idempotency.Result, error) {
						return idempotency.Result{}, errors.New("duplicate callback")
					})
					if err != nil {
						return err
					}
					if !replayed || result.ResourceID != 1 {
						return errors.New("expected replay")
					}
					return nil
				})
			}()
			<-started
			select {
			case err := <-done:
				t.Fatalf("duplicate returned before commit: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			require.NoError(t, first.Commit().Error)
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("duplicate timed out")
			}
			var count int64
			require.NoError(t, db.Table("idempotency_business_probe").Count(&count).Error)
			require.EqualValues(t, 1, count)
			key.Value = "error"
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, _, err := store.Do(t.Context(), tx, key, func(work *gorm.DB) (idempotency.Result, error) {
					return idempotency.Result{StatusCode: 422, Body: json.RawMessage(`{"code":127044}`)}, work.Exec("INSERT INTO idempotency_business_probe VALUES (2)").Error
				})
				return err
			}))
			require.NoError(t, db.Table("idempotency_business_probe").Count(&count).Error)
			require.EqualValues(t, 1, count, "cached business errors cannot commit partial writes")
		})
	}
}
