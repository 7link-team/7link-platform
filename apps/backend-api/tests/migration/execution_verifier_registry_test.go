package migration_test

import (
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/execution/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExecutionVerifierRegistryAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					db := engine.open(t)
					source := (&execution.Module{}).MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 8))
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 9))
					registry := prefix + "execution_verifiers"
					versions := prefix + "execution_verifier_versions"
					assertModelMatchesNamedTable(t, db, engine.dialect, model.Verifier{}, registry)
					assertModelMatchesNamedTable(t, db, engine.dialect, model.VerifierVersion{}, versions)
					keyColumn := `"key"`
					if engine.dialect == "mysql" {
						keyColumn = "`key`"
					}
					insert := func(id, tenant int, key string) error {
						_, err := db.Exec(fmt.Sprintf("INSERT INTO %s (id,tenant_id,%s,name,layer,state,current_version_no,applies_to,required,version,created_by,created_at,updated_at) VALUES (%d,%d,'%s','Tests','AUTOMATED','ACTIVE',1,'{}',false,1,11,'2040-01-01 00:00:00','2040-01-01 00:00:00')", registry, keyColumn, id, tenant, key))
						return err
					}
					require.NoError(t, insert(1, 7, "tests"))
					require.Error(t, insert(2, 7, "tests"))
					require.NoError(t, insert(3, 8, "tests"))
					version := func(id, tenant, verifier, no, timeout int) error {
						_, err := db.Exec(fmt.Sprintf("INSERT INTO %s (id,tenant_id,verifier_id,version_no,spec,timeout_ms,change_note,created_by,created_at) VALUES (%d,%d,%d,%d,'{}',%d,'initial',11,'2040-01-01 00:00:00')", versions, id, tenant, verifier, no, timeout))
						return err
					}
					require.NoError(t, version(10, 7, 1, 1, 1000))
					require.Error(t, version(11, 7, 1, 1, 1000))
					require.Error(t, version(12, 8, 1, 1, 1000))
					require.Error(t, version(13, 7, 1, 2, 999))
					require.Error(t, version(14, 7, 1, 2, 3600001))
					_, err := db.Exec(fmt.Sprintf("DELETE FROM %s WHERE id=1", registry))
					require.Error(t, err)
					require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 8))
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 9))
				})
			}
		})
	}
}
