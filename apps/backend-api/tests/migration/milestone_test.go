package migration_test

import (
	"database/sql"
	"fmt"
	"testing"
	"testing/fstest"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/iam/authn"
	iamcore "7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
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

func TestIAMIdentityAuthnMigrationsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					db := engine.open(t)
					gormDB := openMilestoneGORM(t, db, engine.dialect)
					modules := []module.Module{&identity.Module{}, &authn.Module{}, &organization.Module{}, &iamcore.Module{}}

					reports, err := app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
						Action: app.MigrationUp,
					}, prefix)
					require.NoError(t, err)
					require.Len(t, reports, 4)
					milestones := map[string]string{}
					for _, report := range reports {
						milestones[report.Module] = report.Milestone
					}
					assert.Equal(t, "public-ids", milestones[authn.Name])
					assert.Equal(t, "administration-scopes", milestones[organization.Name])
					assertTableExists(t, db, engine.dialect, prefix+"iam_principals", true)
					assertTableExists(t, db, engine.dialect, prefix+"iam_credentials", true)
					assertTableExists(t, db, engine.dialect, prefix+"goose_identity", true)
					assertTableExists(t, db, engine.dialect, prefix+"goose_authn", true)
					verifyAuthnQueryIndexLifecycle(t, db, engine.dialect, prefix)

					now := "2026-09-02 00:00:00"
					credentials := prefix + "iam_credentials"
					principals := prefix + "iam_principals"
					_, err = db.ExecContext(t.Context(), fmt.Sprintf(
						"INSERT INTO %s (principal_id, password_hash, password_changed_at, updated_at) VALUES (999, 'hash', '%s', '%s')",
						credentials, now, now,
					))
					assert.Error(t, err, "authn constraints must reject an unknown identity principal")
					require.NoError(t, execIAMIdentitySeed(t, db, principals, credentials, now))
					require.NoError(t, execIAMDeletePrincipal(t, db, principals))
					assert.Zero(t, tableRowCount(t, db, credentials), "principal deletion must cascade to credentials")

					_, err = app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
						Action: app.MigrationDownTo,
						Module: identity.Name,
						To:     0,
					}, prefix)
					assert.ErrorContains(t, err, `cannot roll back migration milestones "identity.avatars", "identity.base", "identity.closure-requests", "identity.lifecycle"`)
					assert.ErrorContains(t, err, `"authn.constraints"`)

					_, err = app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
						Action: app.MigrationDownTo,
						Module: authn.Name,
						To:     0,
					}, prefix)
					require.NoError(t, err)
					_, err = app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{Action: app.MigrationDownTo, Module: iamcore.Name, To: 16}, prefix)
					require.NoError(t, err)
					for _, name := range []string{organization.Name, iamcore.Name} {
						_, err = app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{Action: app.MigrationDownTo, Module: name, To: 0}, prefix)
						require.NoError(t, err)
					}
					_, err = app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
						Action: app.MigrationDownTo,
						Module: identity.Name,
						To:     0,
					}, prefix)
					require.NoError(t, err)
					assertTableExists(t, db, engine.dialect, prefix+"iam_credentials", false)
					assertTableExists(t, db, engine.dialect, prefix+"iam_principals", false)

					_, err = app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
						Action: app.MigrationUp,
					}, prefix)
					require.NoError(t, err)
					assertTableExists(t, db, engine.dialect, prefix+"iam_credentials", true)
					assertTableExists(t, db, engine.dialect, prefix+"iam_principals", true)
				})
			}
		})
	}
}

func verifyAuthnQueryIndexLifecycle(t *testing.T, db *sql.DB, dialect, prefix string) {
	t.Helper()
	source := (&authn.Module{}).MigrationSource()
	source.Prefix = prefix
	index := prefix + "idx_iam_email_verification_code_active"
	want := []string{"code", "consumed_at", "expires_at"}
	assert.Equal(t, want, coreIndexColumns(t, db, prefix+"iam_email_verification_tokens", index, dialect))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 9))
	assert.Empty(t, coreIndexColumns(t, db, prefix+"iam_email_verification_tokens", index, dialect))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, dialect, 10))
	assert.Equal(t, want, coreIndexColumns(t, db, prefix+"iam_email_verification_tokens", index, dialect))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, dialect, 11))
	assert.Contains(t, readDatabaseColumns(t, db, dialect, prefix+"iam_email_verification_tokens"), "attempts")
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 10))
	assert.NotContains(t, readDatabaseColumns(t, db, dialect, prefix+"iam_email_verification_tokens"), "attempts")
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, dialect, 11))
	assert.Contains(t, readDatabaseColumns(t, db, dialect, prefix+"iam_email_verification_tokens"), "attempts")
}

func prefixTestName(prefix string) string {
	if prefix == "" {
		return "plain"
	}
	return "prefixed"
}

func execIAMIdentitySeed(t *testing.T, db *sql.DB, principals, credentials, now string) error {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), fmt.Sprintf(
		"INSERT INTO %s (id, login_name, display_name, status, created_at, updated_at) VALUES (1, 'first', 'First', 'active', '%s', '%s')",
		principals, now, now,
	)); err != nil {
		return err
	}
	_, err := db.ExecContext(t.Context(), fmt.Sprintf(
		"INSERT INTO %s (principal_id, password_hash, password_changed_at, updated_at) VALUES (1, 'hash', '%s', '%s')",
		credentials, now, now,
	))
	return err
}

func execIAMDeletePrincipal(t *testing.T, db *sql.DB, principals string) error {
	t.Helper()
	_, err := db.ExecContext(t.Context(), "DELETE FROM "+principals+" WHERE id = 1")
	return err
}

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

// TestIrreversibleMilestoneRefusesBeforeRollingBack covers the declaration
// itself rather than one module's use of it. The distinction that matters is
// when the refusal happens: a migration that only refuses once its own down
// section runs has already undone everything above it, leaving the database
// between two milestones. Declaring it lets the orchestrator refuse while the
// database is still untouched.
func TestIrreversibleMilestoneRefusesBeforeRollingBack(t *testing.T) {
	for _, engine := range []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	} {
		t.Run(engine.name, func(t *testing.T) {
			db := engine.open(t)
			gormDB := openMilestoneGORM(t, db, engine.dialect)
			const prefix = "irr_"
			modules := []module.Module{irreversibleMilestoneModule()}

			_, err := app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
				Action: app.MigrationUp,
			}, prefix)
			require.NoError(t, err)
			for _, table := range []string{"milestone_step1", "milestone_step2", "milestone_step3"} {
				assertTableExists(t, db, engine.dialect, prefix+table, true)
			}

			// Below the irreversible milestone: refused, and the table the
			// reversible migration above it created is still there, which is
			// what shows the rollback never started.
			_, err = app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
				Action: app.MigrationDownTo, Module: "irreversible", To: 1,
			}, prefix)
			require.Error(t, err)
			assert.ErrorContains(t, err, "locked")
			assert.ErrorContains(t, err, "is irreversible")
			assert.ErrorContains(t, err, "nothing was rolled back")
			assertTableExists(t, db, engine.dialect, prefix+"milestone_step3", true)
			assertTableExists(t, db, engine.dialect, prefix+"milestone_step2", true)

			// Down to the irreversible milestone itself does not cross it, so it
			// is allowed: only the reversible migration above it is undone.
			_, err = app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
				Action: app.MigrationDownTo, Module: "irreversible", To: 2,
			}, prefix)
			require.NoError(t, err)
			assertTableExists(t, db, engine.dialect, prefix+"milestone_step3", false)
			assertTableExists(t, db, engine.dialect, prefix+"milestone_step2", true)

			// And it is still refused from there, with nothing further undone.
			_, err = app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
				Action: app.MigrationDownTo, Module: "irreversible", To: 1,
			}, prefix)
			require.Error(t, err)
			assert.ErrorContains(t, err, "is irreversible")
			assertTableExists(t, db, engine.dialect, prefix+"milestone_step2", true)

			// Stepping down one milestone crosses it just as surely as naming a
			// version below it, so the plain down action is refused too. Guarding
			// only the explicit target would leave the commoner command able to
			// start a rollback it cannot finish.
			_, err = app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
				Action: app.MigrationDown, Module: "irreversible",
			}, prefix)
			require.Error(t, err)
			assert.ErrorContains(t, err, "is irreversible")
			assert.ErrorContains(t, err, "nothing was rolled back")
			assertTableExists(t, db, engine.dialect, prefix+"milestone_step2", true)
			assertTableExists(t, db, engine.dialect, prefix+"milestone_step1", true)
		})
	}
}

// TestIrreversibleMilestoneDoesNotBlockWhatWasNeverApplied records the other
// half of the rule: an irreversible milestone that has not been reached cannot
// have anything to undo, so it must not stand in the way.
func TestIrreversibleMilestoneDoesNotBlockWhatWasNeverApplied(t *testing.T) {
	db := testdb.OpenSQLite(t)
	gormDB := openMilestoneGORM(t, db, "sqlite")
	const prefix = "irrpart_"
	modules := []module.Module{irreversibleMilestoneModule()}

	_, err := app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
		Action: app.MigrationUpTo, Module: "irreversible", To: 1,
	}, prefix)
	require.NoError(t, err)
	assertTableExists(t, db, "sqlite", prefix+"milestone_step2", false)

	_, err = app.ExecuteModuleMigrations(t.Context(), gormDB, modules, app.MigrationRequest{
		Action: app.MigrationDownTo, Module: "irreversible", To: 0,
	}, prefix)
	require.NoError(t, err, "the irreversible milestone was never applied, so it blocks nothing")
	assertTableExists(t, db, "sqlite", prefix+"milestone_step1", false)
}

func irreversibleMilestoneModule() module.Module {
	return integrationMilestoneModule{
		name:   "irreversible",
		source: goosex.Source{FS: irreversibleMilestoneFS(), TableName: "goose_irreversible"},
		milestones: []module.MigrationMilestone{
			{Name: "base", Version: 1},
			{Name: "locked", Version: 2, Irreversible: true},
			{Name: "extra", Version: 3},
		},
	}
}

func irreversibleMilestoneFS() fstest.MapFS {
	files := fstest.MapFS{}
	for _, dialect := range []string{"postgres", "mysql", "sqlite"} {
		for version, name := range map[int]string{1: "step1", 2: "step2", 3: "step3"} {
			files[fmt.Sprintf("sql/%s/0000%d_%s.sql", dialect, version, name)] = &fstest.MapFile{
				Data: []byte(reversibleMilestoneStep(name)),
			}
		}
	}
	return files
}

// reversibleMilestoneStep is one trivially reversible migration. Irreversibility
// is declared on the milestone here rather than enforced in SQL, which is
// exactly what this test needs to isolate: every statement below would undo
// cleanly if the orchestrator let it run.
func reversibleMilestoneStep(name string) string {
	return `-- +goose Up
CREATE TABLE {{.Prefix}}milestone_` + name + ` (id BIGINT PRIMARY KEY);

-- +goose Down
DROP TABLE {{.Prefix}}milestone_` + name + `;
`
}
