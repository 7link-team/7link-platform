package migration_test

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	migrationDatabaseType = "IAM_MIGRATION_DATABASE_TYPE"
	migrationDatabaseDSN  = "IAM_MIGRATION_DATABASE_DSN"
	migrationTablePrefix  = "IAM_MIGRATION_TABLE_PREFIX"
)

type migrationStatusEngine struct {
	name string
	open func(*testing.T) (*sql.DB, string)
}

func TestIAMMigrationStatusAcrossEnginesAndPrefixes(t *testing.T) {
	engines := []migrationStatusEngine{
		{name: "postgres", open: testdb.OpenPostgresWithDSN},
		{name: "mysql", open: testdb.OpenMySQLWithDSN},
		{name: "sqlite", open: func(t *testing.T) (*sql.DB, string) {
			dsn := filepath.Join(t.TempDir(), "iam-migration-status.sqlite")
			db, err := sql.Open("sqlite3", dsn)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			return db, dsn
		}},
	}
	backendRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	for _, engine := range engines {
		for _, prefix := range []string{"", deploymentPrefix} {
			t.Run(engine.name+"/"+prefixTestName(prefix), func(t *testing.T) {
				db, dsn := engine.open(t)
				command := exec.CommandContext(
					t.Context(), "go", "test", "7link.com/platform/internal/app",
					"-run", "^TestIAMMigrationStatusExternalDatabase$", "-count=1",
				)
				command.Dir = backendRoot
				command.Env = append(os.Environ(),
					migrationDatabaseType+"="+engine.name,
					migrationDatabaseDSN+"="+dsn,
					migrationTablePrefix+"="+prefix,
				)
				output, runErr := command.CombinedOutput()
				require.NoError(t, runErr, string(output))
				assertIAMDatabaseTableOwnership(t, db, engine.name, prefix)
			})
		}
	}
}

func assertIAMDatabaseTableOwnership(t *testing.T, db *sql.DB, dialect, prefix string) {
	t.Helper()
	expected := make([]string, 0, 66)
	owners := make(map[string]string, 66)
	for _, contract := range iamMigrationContracts() {
		provider, ok := contract.module.(module.MigrationSourceProvider)
		require.True(t, ok, "%s must provide migrations", contract.name)
		actualSourceTables := iamMigrationTables(t, provider.MigrationSource(), dialect)
		assert.Equal(t, contract.tables, actualSourceTables, contract.name)
		for _, table := range actualSourceTables {
			previous, duplicate := owners[table]
			assert.False(t, duplicate, "%s is owned by both %s and %s", table, previous, contract.name)
			owners[table] = contract.name
			expected = append(expected, prefix+table)
		}
	}
	require.Len(t, owners, 67)
	sort.Strings(expected)
	assert.Equal(t, expected, readIAMDatabaseTables(t, db, dialect))
}

func readIAMDatabaseTables(t *testing.T, db *sql.DB, dialect string) []string {
	t.Helper()
	var query string
	switch dialect {
	case "sqlite":
		query = "SELECT name FROM sqlite_master WHERE type = 'table'"
	case "mysql":
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()"
	case "postgres":
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema()"
	default:
		t.Fatalf("unsupported dialect %q", dialect)
	}
	rows, err := db.QueryContext(t.Context(), query)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	var tables []string
	for rows.Next() {
		var table string
		require.NoError(t, rows.Scan(&table))
		if strings.Contains(table, "iam_") {
			tables = append(tables, table)
		}
	}
	require.NoError(t, rows.Err())
	sort.Strings(tables)
	return tables
}
