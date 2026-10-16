package migration_test

import (
	"strings"
	"testing"

	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/governance/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestGovernanceRoleSeedsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	} {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			source := (&governance.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
			table := deploymentPrefix + "governance_role_seeds"
			assertModelMatchesNamedTable(t, db, engine.dialect, model.RoleSeed{}, table)
			insert := func(id, tenant, key, role string) error {
				_, err := db.ExecContext(t.Context(), "INSERT INTO "+table+" (id,tenant_id,role_key,role_id,manifest_version,applied_codes,skipped_codes,created_at,updated_at) VALUES ("+id+","+tenant+",'"+key+"',"+role+",1,'[]','[]','2026-10-16 00:00:00','2026-10-16 00:00:00')")
				return err
			}
			require.NoError(t, insert("1", "1", "delivery_lead", "11"))
			require.NoError(t, insert("2", "2", "delivery_lead", "11"), "keys and role references are tenant qualified")
			require.Error(t, insert("3", "1", "delivery_lead", "12"), "a role key has one owner")
			require.Error(t, insert("3", "1", "engineer", "11"), "a role cannot back two seed keys")
			require.Error(t, insert("3", "1", strings.Repeat("x", 49), "12"), "role key length is enforced")
			var managed, conflict bool
			var version int
			var applied, skipped string
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT managed,seed_conflict,version,applied_codes,skipped_codes FROM "+table+" WHERE id=1").Scan(&managed, &conflict, &version, &applied, &skipped))
			require.True(t, managed)
			require.False(t, conflict)
			require.Equal(t, 1, version)
			require.JSONEq(t, "[]", applied)
			require.JSONEq(t, "[]", skipped)
			_, err := db.ExecContext(t.Context(), "UPDATE "+table+" SET applied_codes='not-json' WHERE id=1")
			require.Error(t, err)
			_, err = db.ExecContext(t.Context(), "UPDATE "+table+" SET managed=false,seed_conflict=true,applied_codes='[\"project_read\"]',skipped_codes='[\"work_item_read\"]' WHERE id=1")
			require.NoError(t, err)
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT managed,seed_conflict,applied_codes,skipped_codes FROM "+table+" WHERE id=1").Scan(&managed, &conflict, &applied, &skipped))
			require.False(t, managed)
			require.True(t, conflict)
			require.JSONEq(t, `["project_read"]`, applied)
			require.JSONEq(t, `["work_item_read"]`, skipped)
		})
	}
}
