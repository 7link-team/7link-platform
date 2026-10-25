package migration_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/execution/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestExecutionProgressPopulatedUpgradeAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					db := engine.open(t)
					source := (&execution.Module{}).MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 15))
					table, receipts := prefix+"execution_executions", prefix+"execution_receipts"
					_, err := db.Exec(fmt.Sprintf("INSERT INTO %s (id,tenant_id,project_id,work_item_id,assignment_id,machine_id,initiator_id,state,state_reason,result_state,plan_json,plan_hash,dispatch_epoch,last_seq,version,created_at,updated_at,branch_reason,start_point,feedback_snapshot,autostart) VALUES (1,7,9,3,6,1,11,'SUCCEEDED','','CONFIRMED','immutable-plan','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',2,3,4,'2040-01-01 00:00:00','2040-01-01 00:00:00','','','',false)", table))
					require.NoError(t, err)
					_, err = db.Exec(fmt.Sprintf("INSERT INTO %s (id,tenant_id,execution_id,dispatch_epoch,seq,kind,content_hash,state,created_at) VALUES (2,7,1,2,1,'ACCEPTED','%s','RUNNING','2040-01-01 00:00:00')", receipts, strings.Repeat("c", 64)))
					require.NoError(t, err)
					assertPreserved := func() {
						t.Helper()
						var plan, hash, state, receipt string
						var epoch, seq, version int
						require.NoError(t, db.QueryRow(fmt.Sprintf("SELECT plan_json,plan_hash,state,dispatch_epoch,last_seq,version FROM %s WHERE id=1", table)).Scan(&plan, &hash, &state, &epoch, &seq, &version))
						require.Equal(t, "immutable-plan", plan)
						require.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", hash)
						require.Equal(t, "SUCCEEDED", state)
						require.Equal(t, []int{2, 3, 4}, []int{epoch, seq, version})
						require.NoError(t, db.QueryRow(fmt.Sprintf("SELECT content_hash FROM %s WHERE id=2", receipts)).Scan(&receipt))
						require.Equal(t, strings.Repeat("c", 64), receipt)
					}
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 16))
					assertModelMatchesNamedTable(t, db, engine.dialect, model.Execution{}, table)
					assertPreserved()
					var progress sql.NullString
					require.NoError(t, db.QueryRow(fmt.Sprintf("SELECT progress_json FROM %s WHERE id=1", table)).Scan(&progress))
					require.False(t, progress.Valid, "migration must not fabricate historical observations")
					const recorded = `{"consecutive_no_progress":2,"stop_reason":"NO_PROGRESS"}`
					_, err = db.Exec(fmt.Sprintf("UPDATE %s SET progress_json='%s' WHERE id=1", table, recorded))
					require.NoError(t, err)
					require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 15), "populated progress rollback must fail")
					require.NoError(t, db.QueryRow(fmt.Sprintf("SELECT progress_json FROM %s WHERE id=1", table)).Scan(&progress))
					require.Equal(t, recorded, progress.String)
					assertPreserved()
					_, err = db.Exec(fmt.Sprintf("UPDATE %s SET progress_json=NULL WHERE id=1", table))
					require.NoError(t, err)
					require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 15))
					assertPreserved()
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 16))
					assertPreserved()
					require.NoError(t, db.QueryRow(fmt.Sprintf("SELECT progress_json FROM %s WHERE id=1", table)).Scan(&progress))
					require.False(t, progress.Valid)
				})
			}
		})
	}
}
