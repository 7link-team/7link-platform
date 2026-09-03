package migration_test

import (
	"database/sql"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	iamaudit "7link.com/platform/internal/iam/audit"
	iamcore "7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	infraaudit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/wuid"
	"7link.com/platform/internal/todo"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	deploymentPrefix = "sl_"
	maximumPrefix    = "p12345678901234567890"
)

var prefixedIdentifierPattern = regexp.MustCompile(`\{\{\.Prefix\}\}([a-z_][a-z0-9_]*)`)

type databaseEngine struct {
	name            string
	dialect         string
	identifierLimit int
	open            func(*testing.T) *sql.DB
}

type moduleMigration struct {
	name    string
	table   string
	target  int64
	seedSQL string
	source  goosex.Source
}

func TestModuleMigrationsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", identifierLimit: 64, open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", identifierLimit: 63, open: testdb.OpenPostgres},
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			for _, module := range moduleMigrations() {
				t.Run(module.name, func(t *testing.T) {
					verifyPrefixedMigration(t, db, engine, module)
					verifyMaximumPrefixMigration(t, db, engine, module)
					verifyLifecycle(t, db, engine, module)
				})
			}
		})
	}
}

func TestMigrationIdentifiersFitEngineLimitsAtMaximumPrefix(t *testing.T) {
	require.NoError(t, goosex.ValidatePrefix(maximumPrefix))
	require.Error(t, goosex.ValidatePrefix(maximumPrefix+"x"))

	identifierModules := append(moduleMigrations(), []moduleMigration{
		{name: "organization", table: "iam_tenants", target: 8, source: (&organization.Module{}).MigrationSource()},
		{name: "governance", table: "iam_access_requests", target: 3, source: (&governance.Module{}).MigrationSource()},
		{name: "provisioning", table: "iam_scim_directories", target: 3, source: (&provisioning.Module{}).MigrationSource()},
		{name: "federation", table: "iam_identity_providers", target: 3, source: (&federation.Module{}).MigrationSource()},
	}...)
	for _, module := range identifierModules {
		for _, engine := range []databaseEngine{
			{name: "mysql", dialect: "mysql", identifierLimit: 64},
			{name: "postgres", dialect: "postgres", identifierLimit: 63},
		} {
			t.Run(engine.name+"/"+module.name, func(t *testing.T) {
				identifiers := migrationIdentifiers(t, module, engine.dialect)
				require.NotEmpty(t, identifiers)
				for _, identifier := range identifiers {
					assert.LessOrEqual(t, len(maximumPrefix+identifier), engine.identifierLimit,
						"identifier %q exceeds the %s limit with the maximum prefix", identifier, engine.name)
				}
			})
		}
	}
}

func moduleMigrations() []moduleMigration {
	return []moduleMigration{
		{
			name:    "todo",
			table:   "todos",
			target:  1,
			seedSQL: `INSERT INTO todos (id, title, description, status, created_at, updated_at) VALUES (1, 'first', '', 'pending', '2026-09-02 00:00:00', '2026-09-02 00:00:00')`,
			source:  (&todo.Module{}).MigrationSource(),
		},
		{
			name:    "dlock",
			table:   "dlocks",
			target:  1,
			seedSQL: `INSERT INTO dlocks (name, owner, expires_at) VALUES ('job', 'worker', 1)`,
			source:  (&dlock.Module{}).MigrationSource(),
		},
		{
			name:    "wuid",
			table:   "worker_ids",
			target:  1,
			seedSQL: `INSERT INTO worker_ids (id, token, expires_at) VALUES (1, 'token', 1)`,
			source:  (&wuid.Module{}).MigrationSource(),
		},
		{
			name:    "audit",
			table:   "operation_logs",
			target:  2,
			seedSQL: `INSERT INTO operation_logs (id, occurred_at, entry, operation, access, outcome) VALUES (1, '2026-09-02 00:00:00', 'http', 'POST /tests', 'write', 'success')`,
			source:  (&infraaudit.Module{}).MigrationSource(),
		},
		{
			name:    "identity",
			table:   "iam_principals",
			target:  2,
			seedSQL: `INSERT INTO iam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (1, 'first', 'First', 'active', '2026-09-02 00:00:00', '2026-09-02 00:00:00')`,
			source:  (&identity.Module{}).MigrationSource(),
		},
		{
			name:    "iam-audit",
			table:   "iam_audit_events",
			target:  2,
			seedSQL: `INSERT INTO iam_audit_events (id, tenant_id, event_type, outcome, detail, created_at, sequence, event_hash) VALUES (1, 0, 'platform_event', 'success', '{}', '2026-09-02 00:00:00', 1, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
			source:  (&iamaudit.Module{}).MigrationSource(),
		},
		{
			name:    "iam-core",
			table:   "iam_roles",
			target:  10,
			seedSQL: `INSERT INTO iam_roles (tenant_id, id, name, name_key, created_at, updated_at) VALUES (1, 1, 'First', 'first', '2026-09-02 00:00:00', '2026-09-02 00:00:00')`,
			source:  (&iamcore.Module{}).MigrationSource(),
		},
	}
}

func verifyPrefixedMigration(t *testing.T, db *sql.DB, engine databaseEngine, module moduleMigration) {
	t.Helper()

	source := module.source
	source.Prefix = deploymentPrefix
	require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
	require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
	assertTableExists(t, db, engine.dialect, deploymentPrefix+module.table, true)
	assertTableExists(t, db, engine.dialect, deploymentPrefix+source.TableName, true)
	assertTableExists(t, db, engine.dialect, module.table, false)
	assertTableExists(t, db, engine.dialect, source.TableName, false)
	assertTableExists(t, db, engine.dialect, deploymentPrefix+deploymentPrefix+module.table, false)
	assertTableExists(t, db, engine.dialect, deploymentPrefix+deploymentPrefix+source.TableName, false)
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 0))
	assertTableExists(t, db, engine.dialect, deploymentPrefix+module.table, false)
	assertTableExists(t, db, engine.dialect, deploymentPrefix+source.TableName, true)
}

func verifyMaximumPrefixMigration(t *testing.T, db *sql.DB, engine databaseEngine, module moduleMigration) {
	t.Helper()

	source := module.source
	source.Prefix = maximumPrefix
	require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))
	assertTableExists(t, db, engine.dialect, maximumPrefix+module.table, true)
	assertTableExists(t, db, engine.dialect, maximumPrefix+source.TableName, true)
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, engine.dialect, 0))
	assertTableExists(t, db, engine.dialect, maximumPrefix+module.table, false)
}

func verifyLifecycle(t *testing.T, db *sql.DB, engine databaseEngine, module moduleMigration) {
	t.Helper()

	status, err := goosex.Inspect(t.Context(), db, module.source, engine.dialect)
	require.NoError(t, err)
	assert.Equal(t, goosex.StatePending, status.State)
	assert.Zero(t, status.Current)
	assert.Equal(t, module.target, status.Target)
	assertTableExists(t, db, engine.dialect, module.table, false)

	require.NoError(t, goosex.Migrate(t.Context(), db, module.source, engine.dialect))
	assertCleanAtTarget(t, db, engine, module)
	assertTableExists(t, db, engine.dialect, module.table, true)
	assertTableExists(t, db, engine.dialect, module.source.TableName, true)
	_, err = db.ExecContext(t.Context(), module.seedSQL)
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), db, module.source, engine.dialect))
	assert.Equal(t, 1, tableRowCount(t, db, module.table))

	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, module.source, engine.dialect, 0))
	assertTableExists(t, db, engine.dialect, module.table, false)
	assertTableExists(t, db, engine.dialect, module.source.TableName, true)
	status, err = goosex.Inspect(t.Context(), db, module.source, engine.dialect)
	require.NoError(t, err)
	assert.Equal(t, goosex.StatePending, status.State)
	assert.Zero(t, status.Current)
	assert.Equal(t, module.target, status.Target)

	require.NoError(t, goosex.MigrateUp(t.Context(), db, module.source, engine.dialect))
	assertCleanAtTarget(t, db, engine, module)
	assert.Zero(t, tableRowCount(t, db, module.table))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, module.source, engine.dialect, 0))
	assertTableExists(t, db, engine.dialect, module.table, false)
}

func assertCleanAtTarget(t *testing.T, db *sql.DB, engine databaseEngine, module moduleMigration) {
	t.Helper()
	status, err := goosex.Inspect(t.Context(), db, module.source, engine.dialect)
	require.NoError(t, err)
	assert.Equal(t, goosex.StateClean, status.State)
	assert.Equal(t, module.target, status.Current)
	assert.Equal(t, module.target, status.Target)
}

func tableRowCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count))
	return count
}

func assertTableExists(t *testing.T, db *sql.DB, dialect, table string, expected bool) {
	t.Helper()
	var (
		count int
		err   error
	)
	switch dialect {
	case "sqlite":
		err = db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count)
	case "mysql":
		err = db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`, table).Scan(&count)
	case "postgres":
		err = db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1`, table).Scan(&count)
	default:
		t.Fatalf("unsupported dialect %q", dialect)
	}
	require.NoError(t, err)
	assert.Equal(t, expected, count == 1, "table %s existence", table)
}

func migrationIdentifiers(t *testing.T, module moduleMigration, dialect string) []string {
	t.Helper()
	_, subdirectory, err := goosex.ResolveDialect(dialect)
	require.NoError(t, err)
	root, err := fs.Sub(module.source.FS, subdirectory)
	require.NoError(t, err)
	identifiers := map[string]struct{}{module.source.TableName: {}}
	require.NoError(t, fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".sql" {
			return walkErr
		}
		contents, readErr := fs.ReadFile(root, path)
		if readErr != nil {
			return readErr
		}
		for _, match := range prefixedIdentifierPattern.FindAllSubmatch(contents, -1) {
			identifiers[string(match[1])] = struct{}{}
		}
		return nil
	}))
	if dialect == "postgres" {
		identifiers[module.table+"_pkey"] = struct{}{}
		identifiers[module.source.TableName+"_pkey"] = struct{}{}
	}
	result := make([]string, 0, len(identifiers))
	for identifier := range identifiers {
		result = append(result, identifier)
	}
	sort.Strings(result)
	return result
}
