package migration_test

import (
	"database/sql"
	"reflect"
	"sync"
	"testing"
	"time"

	iamcore "7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/core/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

type tableModel interface {
	TableName() string
}

func TestIAMCoreModelsMatchMigratedSchemas(t *testing.T) {
	models := []tableModel{
		model.Role{}, model.RolePermission{}, model.RoleMember{}, model.TenantMember{},
		model.Menu{}, model.Entity{}, model.RoleBinding{}, model.PolicyRevision{},
		model.PlatformAdministrator{}, model.Relationship{}, model.ResourceRelationship{},
		model.TemporaryRoleGrant{}, model.PlatformGrant{},
	}
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}

	require.Len(t, models, 13)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			source := (&iamcore.Module{}).MigrationSource()
			require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))

			for _, persisted := range models {
				t.Run(persisted.TableName(), func(t *testing.T) {
					assertModelMatchesTable(t, db, engine.dialect, persisted)
				})
			}
		})
	}
}

func assertModelMatchesTable(t *testing.T, db *sql.DB, dialect string, persisted tableModel) {
	t.Helper()
	assertModelMatchesNamedTable(t, db, dialect, persisted, persisted.TableName())
}

func assertModelMatchesNamedTable(t *testing.T, db *sql.DB, dialect string, persisted tableModel, table string) {
	t.Helper()

	parsed, err := schema.Parse(persisted, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	expected := make(map[string]bool, len(parsed.Fields))
	for _, field := range parsed.Fields {
		if field.DBName == "" {
			continue
		}
		expected[field.DBName] = field.FieldType.Kind() == reflect.Pointer
		if isTemporalColumn(field.DBName) {
			assert.Equal(t, reflect.TypeFor[time.Time](), indirectType(field.FieldType), field.DBName)
		}
	}

	actual := readDatabaseColumns(t, db, dialect, table)
	if dialect == "mysql" && persisted.TableName() == "fleet_agents" {
		_, exists := actual["active_name_key"]
		require.True(t, exists, "fleet active name is the explicit generated-column allowlist")
		delete(actual, "active_name_key")
	}
	if dialect == "mysql" && persisted.TableName() == (model.Entity{}).TableName() {
		_, ok := actual["parent_key"]
		require.True(t, ok, "the entity generated-column allowlist must remain explicit")
		delete(actual, "parent_key")
	}
	if dialect == "mysql" && persisted.TableName() == "iam_departments" {
		_, ok := actual["parent_key"]
		require.True(t, ok, "the department generated-column allowlist must remain explicit")
		delete(actual, "parent_key")
	}
	if dialect == "mysql" && persisted.TableName() == "collab_channels" {
		for _, column := range []string{"default_project", "custom_name", "agent_pair", "direct_pair"} {
			_, exists := actual[column]
			require.True(t, exists, "generated partial-index key %s must remain explicit", column)
			delete(actual, column)
		}
	}
	if dialect == "mysql" && (persisted.TableName() == "delivery_project_contacts" || persisted.TableName() == "delivery_project_repositories") {
		_, exists := actual["primary_lock"]
		require.True(t, exists, "delivery primary lock is the explicit generated-column allowlist")
		delete(actual, "primary_lock")
	}
	assert.Equal(t, expected, actual, persisted.TableName())
}

func readDatabaseColumns(t *testing.T, db *sql.DB, dialect, table string) map[string]bool {
	t.Helper()

	columns := make(map[string]bool)
	switch dialect {
	case "sqlite":
		rows, err := db.QueryContext(t.Context(), "PRAGMA table_info("+table+")")
		require.NoError(t, err)
		defer func() { require.NoError(t, rows.Close()) }()
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, dataType string
			var defaultValue sql.NullString
			require.NoError(t, rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey))
			columns[name] = notNull == 0
		}
		require.NoError(t, rows.Err())
	case "mysql":
		readInformationSchemaColumns(t, db,
			`SELECT column_name, is_nullable FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ordinal_position`,
			table, columns)
	case "postgres":
		readInformationSchemaColumns(t, db,
			`SELECT column_name, is_nullable FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position`,
			table, columns)
	default:
		t.Fatalf("unsupported dialect %q", dialect)
	}
	return columns
}

func readInformationSchemaColumns(t *testing.T, db *sql.DB, query, table string, columns map[string]bool) {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query, table)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	for rows.Next() {
		var name, nullable string
		require.NoError(t, rows.Scan(&name, &nullable))
		columns[name] = nullable == "YES"
	}
	require.NoError(t, rows.Err())
}

func isTemporalColumn(name string) bool {
	for _, suffix := range []string{"_at", "_before", "_after", "_time"} {
		if len(name) >= len(suffix) && name[len(name)-len(suffix):] == suffix {
			return true
		}
	}
	return false
}

func indirectType(fieldType reflect.Type) reflect.Type {
	for fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	return fieldType
}
