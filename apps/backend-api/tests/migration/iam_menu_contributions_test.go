package migration_test

import (
	"fmt"
	"testing"

	iamcore "7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/core/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestIAMMenuContributionPopulatedUpgradeAcrossEngines(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					db := engine.open(t)
					source := iamcore.MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 19))
					menus := prefix + "iam_menus"
					ledger := prefix + "iam_menu_contributions"
					insert := func(id int, parent, route string) {
						_, err := db.Exec(fmt.Sprintf("INSERT INTO %s (tenant_id,id,parent_id,label,route,status,translations,created_at,updated_at) VALUES (1,%d,%s,'Original',%s,'active','{}','2040-01-01 00:00:00','2040-01-01 00:00:00')", menus, id, parent, route))
						require.NoError(t, err)
					}
					insert(1, "NULL", "NULL")
					insert(2, "1", "'/same'")
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 20))
					var parent int64
					var app, domain, space, label string
					require.NoError(t, db.QueryRow(fmt.Sprintf("SELECT parent_id,app,domain,space,label FROM %s WHERE tenant_id=1 AND id=2", menus)).Scan(&parent, &app, &domain, &space, &label))
					require.EqualValues(t, 1, parent)
					require.Equal(t, "iam", app)
					require.Empty(t, domain)
					require.Empty(t, space)
					require.Equal(t, "Original", label)
					_, parentDeleteErr := db.Exec(fmt.Sprintf("DELETE FROM %s WHERE id=1", menus))
					require.Error(t, parentDeleteErr, "replacement retains parent foreign-key enforcement")
					_, err := db.Exec(fmt.Sprintf("INSERT INTO %s (tenant_id,id,label,route,app,status,created_at,updated_at) VALUES (1,3,'Other app','/same','delivery','active','2040-01-01 00:00:00','2040-01-01 00:00:00')", menus))
					require.NoError(t, err)
					_, err = db.Exec(fmt.Sprintf("INSERT INTO %s (tenant_id,id,module,page_key,menu_id,version,seeded_hash,created_at,updated_at) VALUES (1,100,'test','page',NULL,1,'%s','2040-01-01 00:00:00','2040-01-01 00:00:00')", ledger, "0000000000000000000000000000000000000000000000000000000000000000"))
					require.NoError(t, err)
					_, err = db.Exec(fmt.Sprintf("INSERT INTO %s (tenant_id,id,module,page_key,version,seeded_hash,created_at,updated_at) VALUES (1,101,'test','page',1,'hash','2040-01-01 00:00:00','2040-01-01 00:00:00')", ledger))
					require.Error(t, err, "key is tenant/module/page, not snowflake ID")
					assertModelMatchesNamedTable(t, db, engine.dialect, model.Menu{}, menus)
					assertModelMatchesNamedTable(t, db, engine.dialect, model.MenuContribution{}, ledger)
					// Remove the deliberately conflicting second-app route before a
					// downgrade to the historical route-only unique constraint.
					_, err = db.Exec(fmt.Sprintf("DELETE FROM %s WHERE id=3", menus))
					require.NoError(t, err)
					require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 19))
					require.NoError(t, db.QueryRow(fmt.Sprintf("SELECT parent_id,label FROM %s WHERE id=2", menus)).Scan(&parent, &label))
					require.EqualValues(t, 1, parent)
					require.Equal(t, "Original", label)
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 20))
					var count int
					require.NoError(t, db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", menus)).Scan(&count))
					require.Equal(t, 2, count)
				})
			}
		})
	}
}
