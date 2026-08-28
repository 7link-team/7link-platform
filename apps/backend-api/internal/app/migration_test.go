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
	config := MigrationConfig{
		Action:   MigrationUp,
		Timezone: "UTC",
		Database: map[string]Database{
			"primary": {
				Type:     "sqlite",
				Dsn:      filepath.Join(t.TempDir(), "migrations.sqlite"),
				Writable: true,
			},
		},
	}

	reports, err := RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 2)
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)
	assert.Equal(t, goosex.StateClean, reports[1].Status.State)

	reports, err = RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 2)
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)
	assert.Equal(t, goosex.StateClean, reports[1].Status.State)

	config.Action = MigrationStatus
	reports, err = RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 2)

	config.Action = MigrationValidate
	reports, err = RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 2)

	config.Action = MigrationDown
	config.Module = "wuid"
	reports, err = RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StatePending, reports[0].Status.State)

	reports, err = RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StatePending, reports[0].Status.State)

	config.Action = MigrationValidate
	config.Module = ""
	_, err = RunMigrations(t.Context(), config)
	assert.ErrorContains(t, err, "wuid migration state is pending")

	config.Action = MigrationUpTo
	config.Module = "wuid"
	config.To = 1
	reports, err = RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)

	config.Action = MigrationDownTo
	config.To = 0
	reports, err = RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StatePending, reports[0].Status.State)

	config.Action = MigrationAuto
	reports, err = RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)
}

func TestRunMigrationsRejectsUnknownModuleAndMissingWriter(t *testing.T) {
	base := MigrationConfig{Action: MigrationStatus, Timezone: "UTC"}
	_, err := RunMigrations(t.Context(), base)
	assert.ErrorContains(t, err, "writable database is not configured")

	config := MigrationConfig{
		Action:   MigrationStatus,
		Module:   "missing",
		Timezone: "UTC",
		Database: map[string]Database{
			"primary": {Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "unknown.sqlite"), Writable: true},
		},
	}
	_, err = RunMigrations(t.Context(), config)
	assert.ErrorContains(t, err, `module "missing" is not registered`)
}

func TestRunModuleMigrationsValidatesRequests(t *testing.T) {
	_, err := runModuleMigrations(t.Context(), nil, nil, MigrationConfig{})
	assert.EqualError(t, err, "migration: writable database is unavailable")

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "validation.sqlite")))
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})

	_, err = runModuleMigrations(t.Context(), db, nil, MigrationConfig{Action: "sideways"})
	assert.EqualError(t, err, `migration: unsupported action "sideways"`)
	_, err = runModuleMigrations(t.Context(), db, nil, MigrationConfig{Action: MigrationDown})
	assert.EqualError(t, err, "migration: down requires a module")
	_, err = runModuleMigrations(t.Context(), db, nil, MigrationConfig{Action: MigrationDownTo, Module: "wuid", To: -1})
	assert.EqualError(t, err, "migration: target version cannot be negative")
}

func TestRunMigrationsRejectsInvalidDownTargets(t *testing.T) {
	config := MigrationConfig{
		Action:   MigrationUp,
		Module:   "wuid",
		Timezone: "UTC",
		Database: map[string]Database{
			"primary": {
				Type:     "sqlite",
				Dsn:      filepath.Join(t.TempDir(), "down-target.sqlite"),
				Writable: true,
			},
		},
	}
	reports, err := RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)

	config.Action = MigrationDownTo
	config.To = reports[0].Status.Target + 1
	_, err = RunMigrations(t.Context(), config)
	assert.ErrorContains(t, err, "higher version")
}
