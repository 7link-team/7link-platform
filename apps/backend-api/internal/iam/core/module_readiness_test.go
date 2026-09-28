package core

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	modulepkg "7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newReadinessModule wires a core module against a real schema so the readiness
// probe runs the query it runs in production rather than a stub.
func newReadinessModule(t *testing.T, prefix string, gated bool) (*Module, repositoryHarness) {
	t.Helper()
	database := newRepositoryHarness(t, prefix)
	module := NewModule(database.runtime, Config{
		Config:                       modulepkg.Config{Enabled: true},
		RequirePlatformAdministrator: gated,
	})
	bindModuleTables(t, module, prefix)
	services := modulepkg.NewServices()
	require.NoError(t, modulepkg.Provide[guid.Source](services, database.repository.ids))
	require.NoError(t, modulepkg.Provide[audit.Appender](
		services, audit.Appender(func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })))
	require.NoError(t, modulepkg.Provide[authz.TokenVerifier](services, httpTokenVerifier{principalID: 1}))
	require.NoError(t, services.Seal())
	require.NoError(t, module.InjectServices(services))
	return module, database
}

// TestReadinessFailsUntilAPlatformAdministratorExists is the whole point of the
// gate: a migrated but unbootstrapped database can serve no platform operation,
// so a green probe there would tell an operator the opposite of the truth.
func TestReadinessFailsUntilAPlatformAdministratorExists(t *testing.T) {
	module, database := newReadinessModule(t, "ready_", true)
	const tenantID, principalID guid.ID = 6100, 6101
	seedTenantAndPrincipal(t, database.db, "ready_", tenantID, principalID)

	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrPlatformAdministratorRequired)

	granted, err := database.repository.GrantFullPlatformAdministratorOn(t.Context(), database.db, principalID)
	require.NoError(t, err)
	require.True(t, granted)

	assert.NoError(t, module.CheckHealth(t.Context()))
}

// TestReadinessLatchesAfterTheFirstAdministrator keeps the union query off the
// hot path. The last-administrator guard means the count never returns to
// zero, so re-reading forever would be pure cost.
func TestReadinessLatchesAfterTheFirstAdministrator(t *testing.T) {
	module, database := newReadinessModule(t, "readylatch_", true)
	const tenantID, principalID guid.ID = 6200, 6201
	seedTenantAndPrincipal(t, database.db, "readylatch_", tenantID, principalID)
	_, err := database.repository.GrantFullPlatformAdministratorOn(t.Context(), database.db, principalID)
	require.NoError(t, err)
	require.NoError(t, module.CheckHealth(t.Context()))

	// Dropping the table would fail any probe that still queried it.
	require.NoError(t, database.db.Exec("DROP TABLE readylatch_iam_platform_administrators").Error)
	assert.NoError(t, module.CheckHealth(t.Context()), "the satisfied gate must not query again")
}

// TestReadinessIgnoresTheAdministratorGateWhenDisabled leaves recovery and
// migration tooling a way into a deployment it would otherwise be locked out
// of.
func TestReadinessIgnoresTheAdministratorGateWhenDisabled(t *testing.T) {
	module, database := newReadinessModule(t, "readyoff_", false)
	require.NoError(t, database.db.Exec("DROP TABLE readyoff_iam_platform_administrators").Error)

	assert.NoError(t, module.CheckHealth(t.Context()))
	assert.False(t, module.administratorPresent.Load(), "a disabled gate latches nothing")
}

// TestReadinessReportsStorageFailuresRatherThanAMissingAdministrator keeps the
// operator pointed at the real fault. "Bootstrap the platform" is the wrong
// instruction when the table is simply unreadable.
func TestReadinessReportsStorageFailuresRatherThanAMissingAdministrator(t *testing.T) {
	module, database := newReadinessModule(t, "readybroken_", true)
	require.NoError(t, database.db.Exec("DROP TABLE readybroken_iam_platform_administrators").Error)

	err := module.CheckHealth(t.Context())
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrPlatformAdministratorRequired)
	assert.False(t, module.administratorPresent.Load(), "a failed probe must not latch the gate open")
}
