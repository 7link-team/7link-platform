package migration_test

import (
	"database/sql"
	"testing"
	"testing/fstest"

	"7link.com/platform/internal/app"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type integrationMilestoneModule struct {
	name       string
	source     goosex.Source
	milestones []module.MigrationMilestone
}

func (m integrationMilestoneModule) ModuleName() string { return m.name }

func (m integrationMilestoneModule) MigrationSource() goosex.Source { return m.source }

func (m integrationMilestoneModule) MigrationMilestones() []module.MigrationMilestone {
	return m.milestones
}

func TestMigrationMilestonesAcrossEngines(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			gormDB := openMilestoneGORM(t, db, engine.dialect)

			assertMissingPeerBaseFails(t, db, engine.dialect, "missing_a_", integrationMilestoneModules()[0])
			assertMissingPeerBaseFails(t, db, engine.dialect, "missing_b_", integrationMilestoneModules()[1])

			modules := integrationMilestoneModules()
			reports, err := app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
				Action: app.MigrationUp,
			}, "full_")
			require.NoError(t, err)
			require.Len(t, reports, 2)
			for _, report := range reports {
				assert.Equal(t, goosex.StateClean, report.Status.State)
				assert.Equal(t, "constraints", report.Milestone)
			}
			assertTableExists(t, db, engine.dialect, "full_milestone_a", true)
			assertTableExists(t, db, engine.dialect, "full_milestone_b", true)
			_, err = db.ExecContext(t.Context(), `INSERT INTO full_milestone_a (id, b_id) VALUES (1, 999)`)
			assert.Error(t, err, "A must enforce its foreign key to B")
			_, err = db.ExecContext(t.Context(), `INSERT INTO full_milestone_b (id, a_id) VALUES (1, 999)`)
			assert.Error(t, err, "B must enforce its foreign key to A")

			_, err = app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
				Action: app.MigrationDownTo,
				Module: "a",
				To:     0,
			}, "full_")
			assert.ErrorContains(t, err, `cannot roll back migration milestones "a.base", "a.constraints"`)
			assert.ErrorContains(t, err, `"b.base"`)
			assertTableExists(t, db, engine.dialect, "full_milestone_a", true)

			assertMilestoneDown(t, gormDB, modules, "b", "base")
			assertMilestoneDown(t, gormDB, modules, "a", "base")
			assertMilestoneDown(t, gormDB, modules, "b", "")
			assertMilestoneDown(t, gormDB, modules, "a", "")
			assertTableExists(t, db, engine.dialect, "full_milestone_a", false)
			assertTableExists(t, db, engine.dialect, "full_milestone_b", false)
		})
	}
}

func assertMissingPeerBaseFails(
	t *testing.T,
	db *sql.DB,
	dialect string,
	prefix string,
	appModule module.Module,
) {
	t.Helper()
	provider := appModule.(module.MigrationSourceProvider)
	source := provider.MigrationSource()
	source.Prefix = prefix
	err := goosex.MigrateUpTo(t.Context(), db, source, dialect, 2)
	assert.Error(t, err, "constraints must fail before the referenced module base exists")
}

func assertMilestoneDown(
	t *testing.T,
	db *gorm.DB,
	modules []module.Module,
	moduleName string,
	wantMilestone string,
) {
	t.Helper()
	reports, err := app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
		Action: app.MigrationDown,
		Module: moduleName,
	}, "full_")
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, wantMilestone, reports[0].Milestone)
}

func openMilestoneGORM(t *testing.T, db *sql.DB, dialect string) *gorm.DB {
	t.Helper()
	var dialector gorm.Dialector
	switch dialect {
	case "sqlite":
		dialector = sqlite.New(sqlite.Config{Conn: db})
	case "mysql":
		dialector = mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true})
	case "postgres":
		dialector = postgres.New(postgres.Config{Conn: db})
	default:
		t.Fatalf("unsupported dialect %q", dialect)
	}
	gormDB, err := gorm.Open(dialector)
	require.NoError(t, err)
	return gormDB
}

func integrationMilestoneModules() []module.Module {
	return []module.Module{
		integrationMilestoneModule{
			name:   "a",
			source: goosex.Source{FS: integrationMilestoneFS("a"), TableName: "goose_a"},
			milestones: []module.MigrationMilestone{
				{Name: "base", Version: 1},
				{
					Name:         "constraints",
					Version:      2,
					Dependencies: []module.MigrationMilestoneRef{{Module: "b", Name: "base"}},
				},
			},
		},
		integrationMilestoneModule{
			name:   "b",
			source: goosex.Source{FS: integrationMilestoneFS("b"), TableName: "goose_b"},
			milestones: []module.MigrationMilestone{
				{
					Name:         "base",
					Version:      1,
					Dependencies: []module.MigrationMilestoneRef{{Module: "a", Name: "base"}},
				},
				{
					Name:         "constraints",
					Version:      2,
					Dependencies: []module.MigrationMilestoneRef{{Module: "a", Name: "base"}},
				},
			},
		},
	}
}

func integrationMilestoneFS(moduleName string) fstest.MapFS {
	otherName := "b"
	foreignColumn := "b_id"
	if moduleName == "b" {
		otherName = "a"
		foreignColumn = "a_id"
	}
	return fstest.MapFS{
		"sql/postgres/00001_base.sql": {Data: []byte(postgresMilestoneBase(moduleName, foreignColumn))},
		"sql/postgres/00002_constraints.sql": {
			Data: []byte(postgresMilestoneConstraint(moduleName, otherName, foreignColumn)),
		},
		"sql/mysql/00001_base.sql": {Data: []byte(mysqlMilestoneBase(moduleName, foreignColumn))},
		"sql/mysql/00002_constraints.sql": {
			Data: []byte(mysqlMilestoneConstraint(moduleName, otherName, foreignColumn)),
		},
		"sql/sqlite/00001_base.sql": {Data: []byte(sqliteMilestoneBase(moduleName, foreignColumn))},
		"sql/sqlite/00002_constraints.sql": {
			Data: []byte(sqliteMilestoneConstraint(moduleName, otherName, foreignColumn)),
		},
	}
}

func postgresMilestoneBase(moduleName, foreignColumn string) string {
	return `-- +goose Up
CREATE TABLE {{.Prefix}}milestone_` + moduleName + ` (
    id BIGINT PRIMARY KEY,
    ` + foreignColumn + ` BIGINT NULL
);
-- +goose Down
DROP TABLE {{.Prefix}}milestone_` + moduleName + `;
`
}

func postgresMilestoneConstraint(moduleName, otherName, foreignColumn string) string {
	return `-- +goose Up
ALTER TABLE {{.Prefix}}milestone_` + moduleName + `
    ADD CONSTRAINT {{.Prefix}}fk_milestone_` + moduleName + `_` + otherName + `
    FOREIGN KEY (` + foreignColumn + `) REFERENCES {{.Prefix}}milestone_` + otherName + ` (id);
-- +goose Down
ALTER TABLE {{.Prefix}}milestone_` + moduleName + `
    DROP CONSTRAINT {{.Prefix}}fk_milestone_` + moduleName + `_` + otherName + `;
`
}

func mysqlMilestoneBase(moduleName, foreignColumn string) string {
	return `-- +goose Up
CREATE TABLE {{.Prefix}}milestone_` + moduleName + ` (
    id BIGINT NOT NULL PRIMARY KEY,
    ` + foreignColumn + ` BIGINT NULL
);
-- +goose Down
DROP TABLE {{.Prefix}}milestone_` + moduleName + `;
`
}

func mysqlMilestoneConstraint(moduleName, otherName, foreignColumn string) string {
	return `-- +goose Up
ALTER TABLE {{.Prefix}}milestone_` + moduleName + `
    ADD CONSTRAINT {{.Prefix}}fk_milestone_` + moduleName + `_` + otherName + `
    FOREIGN KEY (` + foreignColumn + `) REFERENCES {{.Prefix}}milestone_` + otherName + ` (id);
-- +goose Down
ALTER TABLE {{.Prefix}}milestone_` + moduleName + `
    DROP FOREIGN KEY {{.Prefix}}fk_milestone_` + moduleName + `_` + otherName + `;
`
}

func sqliteMilestoneBase(moduleName, foreignColumn string) string {
	return `-- +goose Up
CREATE TABLE {{.Prefix}}milestone_` + moduleName + ` (
    id INTEGER NOT NULL PRIMARY KEY,
    ` + foreignColumn + ` INTEGER NULL
);
-- +goose Down
DROP TABLE {{.Prefix}}milestone_` + moduleName + `;
`
}

func sqliteMilestoneConstraint(moduleName, otherName, foreignColumn string) string {
	return `-- +goose Up
CREATE TABLE {{.Prefix}}milestone_` + moduleName + `_next (
    id INTEGER NOT NULL PRIMARY KEY,
    ` + foreignColumn + ` INTEGER NULL,
    FOREIGN KEY (` + foreignColumn + `) REFERENCES {{.Prefix}}milestone_` + otherName + ` (id)
);
SELECT id FROM {{.Prefix}}milestone_` + otherName + ` WHERE 1 = 0;
INSERT INTO {{.Prefix}}milestone_` + moduleName + `_next (id, ` + foreignColumn + `)
    SELECT id, ` + foreignColumn + ` FROM {{.Prefix}}milestone_` + moduleName + `;
DROP TABLE {{.Prefix}}milestone_` + moduleName + `;
ALTER TABLE {{.Prefix}}milestone_` + moduleName + `_next RENAME TO {{.Prefix}}milestone_` + moduleName + `;
-- +goose Down
CREATE TABLE {{.Prefix}}milestone_` + moduleName + `_next (
    id INTEGER NOT NULL PRIMARY KEY,
    ` + foreignColumn + ` INTEGER NULL
);
INSERT INTO {{.Prefix}}milestone_` + moduleName + `_next (id, ` + foreignColumn + `)
    SELECT id, ` + foreignColumn + ` FROM {{.Prefix}}milestone_` + moduleName + `;
DROP TABLE {{.Prefix}}milestone_` + moduleName + `;
ALTER TABLE {{.Prefix}}milestone_` + moduleName + `_next RENAME TO {{.Prefix}}milestone_` + moduleName + `;
`
}
