package migration_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	iamcore "7link.com/platform/internal/iam/core"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIAMDefaultMenuTranslationUpgrade(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	if dsn := os.Getenv("MYSQL8_DSN"); dsn != "" {
		engines = append(engines, databaseEngine{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
			db, err := sql.Open("mysql", dsn)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			verifyRealMySQLServer(t, db)
			return db
		}})
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					if engine.name == "mysql8" {
						prefix = "menubf_" + prefix
					}
					db := engine.open(t)
					source := (&iamcore.Module{}).MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 13))
					table := prefix + "iam_menus"
					quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
					insert := func(tenant, id int, route, label string) {
						_, err := db.ExecContext(t.Context(), fmt.Sprintf(
							"INSERT INTO %s (tenant_id,id,label,route,status,created_at,updated_at) VALUES (%d,%d,%s,%s,'active','%s','%s')",
							table, tenant, id, quote(label), quote(route), coreTestTime, coreTestTime))
						require.NoError(t, err)
					}
					// Every default is seeded, and the assertions split them: a
					// migration is a historical artifact naming the labels that
					// existed when it shipped, so it must translate those and leave
					// a default added since untouched. Checking it against whatever
					// DefaultMenus holds today instead fails the moment a page is
					// added, which says nothing about the migration.
					defaults := iamcore.DefaultMenus()
					for _, tenant := range []int{1, 2} {
						for index, menu := range defaults {
							label := menu.Label
							if tenant == 1 && menu.Route == "/users" {
								label = "Principals and members"
							}
							insert(tenant, tenant*100+index, legacyMenuRoute(menu.Route), label)
						}
					}
					insert(3, 301, "/iam/users", "Custom directory")
					insert(3, 302, "/custom/users", "Accounts and members")
					insert(4, 401, "/iam/users", "Principals and members")
					insert(5, 501, "/iam/users", "accounts and members")
					insert(6, 601, "/iam/users", "Accounts and members ")
					require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, engine.dialect, 14))
					custom := `{"zh-CN":"Custom translation","fr-FR":"Annuaire"}`
					_, err := db.ExecContext(t.Context(), "UPDATE "+table+" SET translations = "+quote(custom)+" WHERE tenant_id = 4")
					require.NoError(t, err)
					read := func(tenant, id int) string {
						var value string
						require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf("SELECT translations FROM %s WHERE tenant_id = %d AND id = %d", table, tenant, id)).Scan(&value))
						return value
					}
					assert.Equal(t, "{}", read(1, 100), "v14 alone leaves old default menus untranslated")
					require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
					for _, tenant := range []int{1, 2} {
						for index, menu := range defaults {
							var translations map[string]string
							require.NoError(t, json.Unmarshal([]byte(read(tenant, tenant*100+index)), &translations))
							if !defaultMenuMigrationRoutes[legacyMenuRoute(menu.Route)] {
								assert.Empty(t, translations, "%s postdates this migration", menu.Route)
								continue
							}
							assert.Equal(t, menu.Translations, translations, menu.Route)
							var route string
							require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf("SELECT route FROM %s WHERE tenant_id = %d AND id = %d", table, tenant, tenant*100+index)).Scan(&route))
							assert.Equal(t, menu.Route, route)
						}
					}
					assert.Equal(t, "{}", read(3, 301), "custom label must not be replaced by route matching")
					assert.Equal(t, "{}", read(3, 302), "custom route must not be replaced by label matching")
					assert.Equal(t, custom, read(4, 401), "even partial user translations remain unchanged")
					assert.Equal(t, "{}", read(5, 501), "case-only custom names must remain unchanged")
					assert.Equal(t, "{}", read(6, 601), "trailing-space custom names must remain unchanged")
					assert.Equal(t, len(defaults)*2+5, tableRowCount(t, db, table))
					_, err = db.ExecContext(t.Context(), "UPDATE "+table+" SET translations = "+quote(custom)+" WHERE tenant_id = 1 AND id = 100")
					require.NoError(t, err)
					require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
					require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 14))
					assert.Equal(t, custom, read(1, 100), "data rollback must preserve subsequent edits")
					require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
					assert.Equal(t, custom, read(1, 100), "reapplying backfill must preserve subsequent edits")
				})
			}
		})
	}
}

// defaultMenuMigrationRoutes are the routes 00015_default_menu_translations names,
// in their pre-00016 spelling. Frozen on purpose: the published migration cannot
// grow, so neither can this. It names /iam/positions, which 00017 retired -- a
// migration keeps naming what it named, so this list is not the default menu set
// and must not be compared with it.
var defaultMenuMigrationRoutes = map[string]bool{
	"/iam/users": true, "/iam/invitations": true, "/iam/service-accounts": true,
	"/iam/entities": true, "/iam/departments": true, "/iam/positions": true,
	"/iam/groups": true, "/iam/roles": true, "/iam/access-requests": true,
	"/iam/access-reviews": true, "/iam/menus": true, "/iam/oauth-clients": true,
	"/iam/scim-directories": true, "/iam/audit-events": true,
	"/iam/audit-governance": true,
}

func legacyMenuRoute(route string) string {
	if route == "/audit/events" {
		return "/iam/audit-events"
	}
	return "/iam" + route
}
