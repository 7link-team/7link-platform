package governance

import (
	"strings"
	"sync"
	"testing"

	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemActionsOnlyAllowsExactSealedRegistrations(t *testing.T) {
	var catalog systemActionRegistry
	registry := authz.DefaultRegistry()
	require.NoError(t, catalog.Register("delivery.verification", "user_view"))
	require.NoError(t, catalog.Register("delivery.verification", "role_view"))
	require.False(t, catalog.allows("delivery.verification", "user_view"), "unvalidated declarations cannot authorize")
	require.NoError(t, catalog.seal(registry))
	require.NoError(t, catalog.seal(registry), "repeated module initialization is harmless")
	require.True(t, catalog.allows("delivery.verification", "user_view"))
	require.True(t, catalog.allows("delivery.verification", "role_view"))
	require.False(t, catalog.allows("delivery.verification", "role_edit"))
	require.False(t, catalog.allows("delivery.*", "user_view"))
	require.False(t, catalog.allows("delivery.verification", "*"))
	require.False(t, catalog.allows("other.verification", "user_view"))
	require.Error(t, catalog.Register("delivery.late", "user_view"))
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				assert.True(t, catalog.allows("delivery.verification", "user_view"))
				assert.Error(t, catalog.Register("late", "role_view"))
			}
		}()
	}
	wg.Wait()
}

func TestSystemActionsRejectsPrivilegeExpansionAndInvalidDeclarations(t *testing.T) {
	for _, job := range []string{"", "has space", "job\n", strings.Repeat("x", 81)} {
		var catalog systemActionRegistry
		require.ErrorIs(t, catalog.Register(job, "user_view"), ErrInvalidArgument)
	}
	for _, code := range []string{"", "project_create", "tool_catalog_manage", "scope_change", "preview_share_approve"} {
		var catalog systemActionRegistry
		require.ErrorIs(t, catalog.Register("worker", code), ErrInvalidArgument)
		require.NoError(t, catalog.Register("worker", "role_view"))
		require.NoError(t, catalog.seal(authz.DefaultRegistry()))
		require.False(t, catalog.allows("worker", code))
	}

	var catalog systemActionRegistry
	require.NoError(t, catalog.Register("worker", "user_view"))
	require.Error(t, catalog.Register("worker", "user_view"))
	require.NoError(t, catalog.Register("worker", "role_view"))
	require.NoError(t, catalog.seal(authz.DefaultRegistry()))
	require.True(t, catalog.allows("worker", "role_view"))
	require.ErrorIs(t, (*systemActionRegistry)(nil).Register("worker", "user_view"), ErrUnavailable)
	require.ErrorIs(t, (*systemActionRegistry)(nil).seal(authz.DefaultRegistry()), ErrUnavailable)
	require.False(t, (*systemActionRegistry)(nil).allows("worker", "user_view"))
}

func TestSystemActionsValidatesCatalogAfterAllModulesContribute(t *testing.T) {
	var catalog systemActionRegistry
	require.NoError(t, catalog.Register("delivery.verification", "decision_pause"))
	registry := authz.DefaultRegistry()
	require.Error(t, catalog.seal(registry))
	require.False(t, catalog.allows("delivery.verification", "decision_pause"))
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "decision", Verb: "pause", Scope: authz.ActionScopeTenant}}, nil))
	require.NoError(t, catalog.seal(registry))
	require.True(t, catalog.allows("delivery.verification", "decision_pause"))
	var invalid systemActionRegistry
	require.NoError(t, invalid.Register("worker", "platform_administer"))
	require.Error(t, invalid.seal(registry), "business system jobs cannot acquire platform administration")
	require.ErrorIs(t, invalid.seal(nil), ErrUnavailable)
}
