package app

import (
	"path/filepath"
	"testing"

	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunMigrationsUpStatusValidateAndDown(t *testing.T) {
	config := MigrationConfig{
		Timezone: "UTC",
		Database: map[string]Database{
			"primary": {
				Type:     "sqlite",
				Dsn:      filepath.Join(t.TempDir(), "migrations.sqlite"),
				Writable: true,
			},
		},
	}

	reports, err := RunMigrations(t.Context(), config, MigrationRequest{Action: MigrationUpOnly})
	require.NoError(t, err)
	require.Len(t, reports, 2)
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)
	assert.Equal(t, goosex.StateClean, reports[1].Status.State)

	reports, err = RunMigrations(t.Context(), config, MigrationRequest{Action: MigrationValidate})
	require.NoError(t, err)
	require.Len(t, reports, 2)

	reports, err = RunMigrations(t.Context(), config, MigrationRequest{
		Action: MigrationDown,
		Module: "wuid",
		To:     0,
	})
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StatePending, reports[0].Status.State)

	_, err = RunMigrations(t.Context(), config, MigrationRequest{Action: MigrationValidate})
	assert.ErrorContains(t, err, "wuid migration state is pending")

	reports, err = RunMigrations(t.Context(), config, MigrationRequest{
		Action: MigrationUpOnly,
		Module: "wuid",
	})
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)
}

func TestRunMigrationsRejectsUnknownModuleAndMissingWriter(t *testing.T) {
	base := MigrationConfig{Timezone: "UTC"}
	_, err := RunMigrations(t.Context(), base, MigrationRequest{Action: MigrationStatus})
	assert.ErrorContains(t, err, "writable database is not configured")

	config := MigrationConfig{
		Timezone: "UTC",
		Database: map[string]Database{
			"primary": {Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "unknown.sqlite"), Writable: true},
		},
	}
	_, err = RunMigrations(t.Context(), config, MigrationRequest{Action: MigrationStatus, Module: "missing"})
	assert.ErrorContains(t, err, `module "missing" is not registered`)
}
