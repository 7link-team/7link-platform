package migration_test

import (
	"database/sql"
	"os"
	"sort"
	"strings"
	"testing"

	"7link.com/platform/internal/app"
	iamaudit "7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	iamcore "7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestIAMMigrationsOnRealMySQL8(t *testing.T) {
	dsn := os.Getenv("MYSQL8_DSN")
	if dsn == "" {
		t.Skip("run through make test-mysql8")
	}
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(t.Context()))
	verifyRealMySQLServer(t, db)

	gormDB, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}))
	require.NoError(t, err)
	modules := realMySQLMigrationModules()
	for _, prefix := range []string{"", deploymentPrefix} {
		t.Run(prefixTestName(prefix), func(t *testing.T) {
			assertRealMySQLUp(t, gormDB, modules, prefix)
			assertRealMySQLSchemaSet(t, db, prefix)
			verifyCoreServiceBehavior(t, "mysql", dsn, prefix)
			if prefix == "" {
				verifyCoreRoleContracts(t, db, prefix)
				verifyCoreMenuContracts(t, db, prefix)
				verifyCoreEntityContracts(t, db, prefix, "mysql")
				verifyCoreBindingAndGrantContracts(t, db, prefix)
				verifyFederationContracts(t, db, prefix)
				verifyRealMySQLAppendOnly(t, db, prefix)
			}
			assertRealMySQLDown(t, db, gormDB, modules, prefix)
			assertRealMySQLUp(t, gormDB, modules, prefix)
			assertRealMySQLSchemaSet(t, db, prefix)
		})
	}
}

func verifyRealMySQLServer(t *testing.T, db *sql.DB) {
	t.Helper()
	var version, sqlMode string
	var trustFunctionCreators int
	require.NoError(t, db.QueryRowContext(t.Context(),
		"SELECT VERSION(), @@GLOBAL.sql_mode, @@GLOBAL.log_bin_trust_function_creators",
	).Scan(&version, &sqlMode, &trustFunctionCreators))
	assert.Regexp(t, `^8\.4\.`, version, "the pinned gate must stay on MySQL 8.4")
	expected := strings.Split(os.Getenv("MYSQL8_EXPECTED_SQL_MODE"), ",")
	actual := strings.Split(sqlMode, ",")
	sort.Strings(expected)
	sort.Strings(actual)
	assert.Equal(t, expected, actual, "MySQL sql_mode must be deterministic")
	assert.Equal(t, 1, trustFunctionCreators, "non-root migration users must be allowed to create triggers")
	t.Logf("verified MySQL %s with sql_mode=%s", version, sqlMode)
}

func realMySQLMigrationModules() []module.Module {
	// Deliberately scrambled: production milestone dependencies own the order.
	return []module.Module{
		&federation.Module{}, &oauth.Module{}, &governance.Module{}, &authn.Module{},
		&organization.Module{}, &iamaudit.Module{}, &provisioning.Module{}, &identity.Module{}, &iamcore.Module{},
	}
}

func assertRealMySQLUp(t *testing.T, db *gorm.DB, modules []module.Module, prefix string) {
	t.Helper()
	reports, err := app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
		Action: app.MigrationUp,
	}, prefix)
	require.NoError(t, err)
	require.Len(t, reports, 9)
	for _, report := range reports {
		assert.Equal(t, goosex.StateClean, report.Status.State, report.Module)
		assert.Equal(t, report.Status.Target, report.Status.Current, report.Module)
	}
	validated, err := app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
		Action: app.MigrationValidate,
	}, prefix)
	require.NoError(t, err)
	require.Len(t, validated, 9)
}

func assertRealMySQLDown(t *testing.T, sqlDB *sql.DB, db *gorm.DB, modules []module.Module, prefix string) {
	t.Helper()
	for _, name := range []string{
		federation.Name, authn.Name, oauth.Name, governance.Name, provisioning.Name,
		organization.Name, identity.Name, iamcore.Name, iamaudit.Name,
	} {
		_, err := app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
			Action: app.MigrationDownTo, Module: name, To: 0,
		}, prefix)
		require.NoError(t, err, "roll back %s", name)
	}
	actual := realMySQLIAMTables(t, sqlDB, prefix)
	assert.Empty(t, actual, "all IAM tables must be removed by reverse-DAG rollback")
}

func assertRealMySQLSchemaSet(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	expectedTables := make([]string, 0, 63)
	expectedVersions := make([]string, 0, 9)
	for _, contract := range iamMigrationContracts() {
		for _, table := range contract.tables {
			expectedTables = append(expectedTables, prefix+table)
		}
		provider, ok := contract.module.(module.MigrationSourceProvider)
		require.True(t, ok, "%s must provide migrations", contract.name)
		expectedVersions = append(expectedVersions, prefix+provider.MigrationSource().TableName)
	}
	sort.Strings(expectedTables)
	sort.Strings(expectedVersions)
	assert.Equal(t, expectedTables, realMySQLIAMTables(t, db, prefix))
	for _, table := range expectedVersions {
		assertTableExists(t, db, "mysql", table, true)
	}
}

func realMySQLIAMTables(t *testing.T, db *sql.DB, prefix string) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(),
		"SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()")
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	wantPrefix := prefix + "iam_"
	var tables []string
	for rows.Next() {
		var table string
		require.NoError(t, rows.Scan(&table))
		if strings.HasPrefix(table, wantPrefix) {
			tables = append(tables, table)
		}
	}
	require.NoError(t, rows.Err())
	sort.Strings(tables)
	return tables
}

func verifyRealMySQLAppendOnly(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	events := prefix + "iam_audit_events"
	insertAuditEvent(t, db, events, 900, 0, "success", 1, auditHashA)
	_, err := db.ExecContext(t.Context(), "UPDATE "+events+" SET outcome = 'failure' WHERE id = 900")
	assert.ErrorContains(t, err, "append-only")
	_, err = db.ExecContext(t.Context(), "DELETE FROM "+events+" WHERE id = 900")
	assert.ErrorContains(t, err, "append-only")
}
