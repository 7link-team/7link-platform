package migration_test

import (
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/execution/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"fmt"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestExecutionStrategyPopulatedUpgradeAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					db := engine.open(t)
					source := (&execution.Module{}).MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 9))
					table := prefix + "execution_executions"
					base := fmt.Sprintf("INSERT INTO %s (id,tenant_id,project_id,work_item_id,assignment_id,machine_id,initiator_id,state,state_reason,result_state,plan_json,plan_hash,dispatch_epoch,last_seq,version,created_at,updated_at,branch_reason,start_point,feedback_snapshot,autostart) VALUES (1,7,9,3,6,1,11,'SUCCEEDED','','CONFIRMED','immutable-plan','immutable-hash',1,3,4,'2040-01-01 00:00:00','2040-01-01 00:00:00','','','',false)", table)
					_, err := db.Exec(base)
					require.NoError(t, err)
					_, err = db.Exec(fmt.Sprintf("INSERT INTO %s (id,tenant_id,execution_id,dispatch_epoch,seq,kind,content_hash,state,created_at) VALUES (2,7,1,1,1,'ACCEPTED','cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc','RUNNING','2040-01-01 00:00:00')", prefix+"execution_receipts"))
					require.NoError(t, err)
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 10))
					assertModelMatchesNamedTable(t, db, engine.dialect, model.Execution{}, table, "progress_json")
					var plan, receipt string
					require.NoError(t, db.QueryRow(fmt.Sprintf("SELECT plan_json FROM %s WHERE id=1", table)).Scan(&plan))
					require.Equal(t, "immutable-plan", plan)
					require.NoError(t, db.QueryRow(fmt.Sprintf("SELECT content_hash FROM %s WHERE id=2", prefix+"execution_receipts")).Scan(&receipt))
					require.Equal(t, strings.Repeat("c", 64), receipt)
					insert := func(id int, parent, reason string) error {
						_, err := db.Exec(fmt.Sprintf("INSERT INTO %s (id,tenant_id,project_id,work_item_id,assignment_id,machine_id,initiator_id,state,state_reason,result_state,plan_json,plan_hash,dispatch_epoch,last_seq,version,created_at,updated_at,branch_reason,start_point,feedback_snapshot,autostart,strategy_parent_id) VALUES (%d,7,9,3,6,1,11,'CREATED','','NONE','child','child-hash',1,0,1,'2040-01-01 00:00:00','2040-01-01 00:00:00','%s','BASELINE','Alternative',false,%s)", table, id, reason, parent))
						return err
					}
					require.NoError(t, insert(3, "1", "STRATEGY"))
					require.Error(t, insert(4, "0", "STRATEGY"))
					require.Error(t, insert(5, "-1", "STRATEGY"))
					require.Error(t, insert(6, "6", "STRATEGY"))
					require.Error(t, insert(7, "1", "QUALITY"))
					// The original quality decision invariant remains in force.
					_, err = db.Exec(fmt.Sprintf("UPDATE %s SET parent_execution_id=1 WHERE id=3", table))
					require.Error(t, err)
					require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 9))
					// Guard must fail before dropping any index/column or changing recorded bytes.
					var parent int
					require.NoError(t, db.QueryRow(fmt.Sprintf("SELECT strategy_parent_id FROM %s WHERE id=3", table)).Scan(&parent))
					require.Equal(t, 1, parent)
					_, err = db.Exec(fmt.Sprintf("DELETE FROM %s WHERE id=3", table))
					require.NoError(t, err)
					require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 9))
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 10))
					require.NoError(t, db.QueryRow(fmt.Sprintf("SELECT plan_json FROM %s WHERE id=1", table)).Scan(&plan))
					require.Equal(t, "immutable-plan", plan)
				})
			}
		})
	}
}
