package core

import (
	"net/http"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEffectivePermissionsReportOnlyWhatTheCallerHolds(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Store viewers", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "store_view")
	require.NoError(t, err)
	_, err = harness.service.AddMember(t.Context(), tenantID, role.ID, principalID)
	require.NoError(t, err)

	codes, err := harness.service.EffectivePermissions(t.Context(), tenantID, principalID)
	require.NoError(t, err)
	assert.Equal(t, []string{"store_view"}, codes,
		"a console must not be told about a verb the same role does not grant")

	// A tenant the caller is not a member of contributes nothing rather than
	// leaking the catalog of that tenant's actions.
	foreign, err := harness.service.EffectivePermissions(t.Context(), guid.ID(2), principalID)
	require.NoError(t, err)
	assert.Empty(t, foreign)

	_, err = harness.service.EffectivePermissions(t.Context(), tenantID, 0)
	assert.ErrorIs(t, err, ErrInvalidArgument)
}

func TestEffectivePermissionsCarryPlatformAuthorityWithoutMembership(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	_, err := harness.repository.SetPlatformAdministrator(
		t.Context(), principalID, false, []string{"tenant_view"},
	)
	require.NoError(t, err)

	// No tenant selected: the platform grant still has to come back, otherwise a
	// platform administrator with no tenant of their own sees a console with
	// every action hidden.
	platformOnly, err := harness.service.EffectivePermissions(t.Context(), 0, principalID)
	require.NoError(t, err)
	assert.Equal(t, []string{"tenant_view"}, platformOnly)

	// A restricted platform grant must not widen into tenant authority.
	withTenant, err := harness.service.EffectivePermissions(t.Context(), tenantID, principalID)
	require.NoError(t, err)
	assert.Equal(t, []string{"tenant_view"}, withTenant)
}

func TestEffectivePermissionsEndpointRequiresTheSelectedTenant(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)

	selected := harness.api.Get("/iam/me/permissions", "Authorization: Bearer test", "X-Tenant-Id: 1")
	require.Equal(t, http.StatusOK, selected.Code, selected.Body.String())
	assert.Contains(t, selected.Body.String(), `"user_create"`)
	assert.Contains(t, selected.Body.String(), `"platform_administer"`,
		"platform codes travel with the reply so platform pages can be trimmed too")

	// No tenant selector is refused rather than answered for some default tenant.
	unselected := harness.api.Get("/iam/me/permissions", "Authorization: Bearer test")
	assert.Equal(t, http.StatusForbidden, unselected.Code, unselected.Body.String())
}
