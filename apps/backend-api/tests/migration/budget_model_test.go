package migration_test

import (
	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/budget/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestBudgetReservationModelsAndConstraintsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&budget.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Reservation{}, deploymentPrefix+"budget_reservations")
			if engine.dialect == "mysql" {
				return
			}
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := deploymentPrefix + "budget_reservations"
			now := time.Now().UTC().Truncate(time.Millisecond)
			insert := func(id int64, changes map[string]any) error {
				r := map[string]any{"id": id, "tenant_id": 1, "project_id": 10, "source": "EXECUTION", "source_id": id, "reserved_micros": 1, "price_known": true, "state": "RESERVED", "created_at": now}
				for k, v := range changes {
					r[k] = v
				}
				return db.Table(table).Create(r).Error
			}
			require.NoError(t, insert(1, nil))
			require.Error(t, insert(2, map[string]any{"source_id": 1}))
			require.NoError(t, insert(3, map[string]any{"source_id": 1, "tenant_id": 2}))
			for _, changes := range []map[string]any{{"source": "OTHER"}, {"reserved_micros": -1}, {"actual_micros": 1}, {"state": "SETTLED"}, {"settled_at": now}} {
				require.Error(t, insert(4, changes))
			}
			require.NoError(t, insert(5, map[string]any{"state": "SETTLED", "actual_micros": int64(9007199254740993), "settled_at": now}))
			var r model.Reservation
			require.NoError(t, db.Table(table).Where("id=?", 5).First(&r).Error)
			require.Equal(t, int64(9007199254740993), *r.ActualMicros)
		})
	}
}
