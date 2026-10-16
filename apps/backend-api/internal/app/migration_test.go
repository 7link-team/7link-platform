package app

import (
	"path/filepath"
	"testing"

	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRunMigrationsUpStatusValidateAndDown(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "migrations.sqlite")
	config := migrationTestConfig(map[string]Database{
		"primary": {
			Type:     "sqlite",
			Dsn:      databasePath,
			Writable: true,
		},
	})
	config.Action = MigrationUp

	reports, err := (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 6)
	assert.Equal(t, []string{"dlock", "wuid", "events", "idempotency", "audit", "jobs"}, migrationReportNames(reports))
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)
	assert.Equal(t, goosex.StateClean, reports[1].Status.State)

	// The standalone command stops after migration. In particular, wuid must not
	// enter Start and allocate a worker lease.
	db, err := gorm.Open(sqlite.Open(databasePath))
	require.NoError(t, err)
	var workerCount int64
	require.NoError(t, db.Table("worker_ids").Count(&workerCount).Error)
	assert.Zero(t, workerCount)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 6)
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)
	assert.Equal(t, goosex.StateClean, reports[1].Status.State)

	config.Action = MigrationStatus
	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 6)

	config.Action = MigrationValidate
	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 6)

	config.Action = MigrationDown
	config.Module = "wuid"
	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StatePending, reports[0].Status.State)

	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StatePending, reports[0].Status.State)

	config.Action = MigrationValidate
	config.Module = ""
	_, err = (&App{}).RunMigrations(t.Context(), config)
	assert.ErrorContains(t, err, "wuid migration state is pending")

	config.Action = MigrationUpTo
	config.Module = "wuid"
	config.To = 1
	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)

	config.Action = MigrationDownTo
	config.To = 0
	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StatePending, reports[0].Status.State)

	config.Action = MigrationAuto
	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)
}

func migrationTestConfig(database map[string]Database) MigrationConfig {
	config := validTestAppConfig()
	config.Database = database
	return MigrationConfig{AppConfig: config}
}

func migrationReportNames(reports []MigrationReport) []string {
	names := make([]string, len(reports))
	for index, report := range reports {
		names[index] = report.Module
	}
	return names
}

func TestRunMigrationsRejectsUnknownModuleAndMissingWriter(t *testing.T) {
	base := migrationTestConfig(nil)
	base.Action = MigrationStatus
	_, err := (&App{}).RunMigrations(t.Context(), base)
	assert.ErrorContains(t, err, "writable database is not configured")

	config := migrationTestConfig(map[string]Database{
		"primary": {Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "unknown.sqlite"), Writable: true},
	})
	config.Action = MigrationStatus
	config.Module = "missing"
	_, err = (&App{}).RunMigrations(t.Context(), config)
	assert.ErrorContains(t, err, `module "missing" is not registered`)
}

func TestRunMigrationsUsesSingleWriteHandle(t *testing.T) {
	firstPath := filepath.Join(t.TempDir(), "writer-a.sqlite")
	secondPath := filepath.Join(t.TempDir(), "writer-b.sqlite")
	config := migrationTestConfig(map[string]Database{
		"writer-a": {Type: "sqlite", Dsn: firstPath, Writable: true},
		"writer-b": {Type: "sqlite", Dsn: secondPath, Writable: true},
	})
	config.Action = MigrationUp

	_, err := (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)

	first, err := gorm.Open(sqlite.Open(firstPath))
	require.NoError(t, err)
	second, err := gorm.Open(sqlite.Open(secondPath))
	require.NoError(t, err)
	for _, db := range []*gorm.DB{first, second} {
		sqlDB, sqlErr := db.DB()
		require.NoError(t, sqlErr)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}

	assert.True(t, first.Migrator().HasTable("dlocks"), "the first selected writer must execute the migration")
	assert.False(t, second.Migrator().HasTable("dlocks"), "one migration must not advance to another writer")
}

func TestMigrationInspectorDoesNotAdvanceTheWriteHandleBeforeInspection(t *testing.T) {
	firstPath := filepath.Join(t.TempDir(), "writer-a.sqlite")
	secondPath := filepath.Join(t.TempDir(), "writer-b.sqlite")
	seed := migrationTestConfig(map[string]Database{
		"writer-a": {Type: "sqlite", Dsn: firstPath, Writable: true},
	})
	seed.Action = MigrationUp
	_, err := (&App{}).RunMigrations(t.Context(), seed)
	require.NoError(t, err)

	config := migrationTestConfig(map[string]Database{
		"writer-a": {Type: "sqlite", Dsn: firstPath, Writable: true},
		"writer-b": {Type: "sqlite", Dsn: secondPath, Writable: true},
	})
	app := &App{config: config.AppConfig}
	require.NoError(t, app.prepare(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })

	reports, err := app.migrationInspector(t.Context())
	require.NoError(t, err)
	require.Len(t, reports, 6)
	for _, report := range reports {
		assert.Equal(t, goosex.StateClean, report.Status.State, report.Module)
	}
}

func TestMigrateModulesRequiresRuntimeAndComposedModules(t *testing.T) {
	_, err := (&App{}).migrateModules(t.Context(), MigrationRequest{Action: MigrationStatus})
	assert.EqualError(t, err, "migration: writable database is not configured")

	app := &App{config: AppConfig{Timezone: "UTC", Database: map[string]Database{
		"primary": {
			Type:     "sqlite",
			Dsn:      filepath.Join(t.TempDir(), "composition.sqlite"),
			Writable: true,
		},
	}}}
	require.NoError(t, app.initializeRuntime(t.Context()))
	t.Cleanup(func() { require.NoError(t, app.runtime.Close()) })

	_, err = app.migrateModules(t.Context(), MigrationRequest{Action: MigrationStatus})
	assert.EqualError(t, err, "migration: modules are not composed")
}

func TestRunModuleMigrationsValidatesRequests(t *testing.T) {
	_, err := executeModuleMigrations(t.Context(), nil, nil, MigrationRequest{}, "")
	assert.EqualError(t, err, "migration: writable database is unavailable")

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "validation.sqlite")))
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})

	_, err = executeModuleMigrations(t.Context(), db, nil, MigrationRequest{Action: "sideways"}, "")
	assert.EqualError(t, err, `migration: unsupported action "sideways"`)
	_, err = executeModuleMigrations(t.Context(), db, nil, MigrationRequest{Action: MigrationDown}, "")
	assert.EqualError(t, err, "migration: down requires a module")
	_, err = executeModuleMigrations(t.Context(), db, nil, MigrationRequest{Action: MigrationDownTo, Module: "wuid", To: -1}, "")
	assert.EqualError(t, err, "migration: target version cannot be negative")
}

func TestRunMigrationsRejectsInvalidDownTargets(t *testing.T) {
	config := migrationTestConfig(map[string]Database{
		"primary": {
			Type:     "sqlite",
			Dsn:      filepath.Join(t.TempDir(), "down-target.sqlite"),
			Writable: true,
		},
	})
	config.Action = MigrationUp
	config.Module = "wuid"
	reports, err := (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)

	config.Action = MigrationDownTo
	config.To = reports[0].Status.Target + 1
	_, err = (&App{}).RunMigrations(t.Context(), config)
	assert.ErrorContains(t, err, "higher version")
}
