package core

import (
	"context"
	"net/http"
	"sort"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
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
	_, err = harness.service.AddMember(t.Context(), tenantID, role.ID, principalID, nil)
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
	seedSystemAccessFixture(t, harness.db, "sl_")
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

func TestEffectivePermissionsEndpointAllowsPlatformDiscoveryWithoutTenant(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)

	selected := harness.api.Get("/tenants/1/principals/"+"11"+"/permissions", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, selected.Code, selected.Body.String())
	assert.Contains(t, selected.Body.String(), `"user_create"`)
	assert.Contains(t, selected.Body.String(), `"platform_administer"`,
		"platform codes travel with the reply so platform pages can be trimmed too")

	// No selector returns platform authority only, never a default tenant's grants.
	unselected := harness.api.Get("/principals/"+"11"+"/permissions", "Authorization: Bearer test")
	assert.Equal(t, http.StatusOK, unselected.Code, unselected.Body.String())
	assert.Contains(t, unselected.Body.String(), `"platform_administer"`)
	assert.NotContains(t, unselected.Body.String(), `"user_create"`)
	assert.Equal(t, http.StatusForbidden, harness.api.Get("/tenants/invalid/principals/"+"11"+"/permissions", "Authorization: Bearer test").Code)
}

// entityPermissionEvaluator answers like the permissive stub except for the one
// kind of check a scenario deliberately breaks, so a reported failure can be
// traced to the check it came from.
type entityPermissionEvaluator struct {
	httpAuthorizationEvaluator
	platform, data, explain error
}

func (e entityPermissionEvaluator) CheckPlatform(ctx context.Context, permission string, subject guid.ID) (bool, error) {
	if e.platform != nil {
		return false, e.platform
	}
	return e.httpAuthorizationEvaluator.CheckPlatform(ctx, permission, subject)
}

func (e entityPermissionEvaluator) EntityConstraint(
	ctx context.Context, tenantID, entityID guid.ID, permission string, subject guid.ID,
) (authz.DataConstraint, error) {
	if e.data != nil {
		return authz.DataConstraint{}, e.data
	}
	return e.httpAuthorizationEvaluator.EntityConstraint(ctx, tenantID, entityID, permission, subject)
}

func (e entityPermissionEvaluator) ExplainEntity(
	ctx context.Context, tenantID, entityID guid.ID, permission string, subject guid.ID,
) (authz.Explanation, error) {
	if e.explain != nil {
		return authz.Explanation{}, e.explain
	}
	return e.httpAuthorizationEvaluator.ExplainEntity(ctx, tenantID, entityID, permission, subject)
}

func TestEffectiveEntityPermissionsReportOnlyWhatReachesThatEntity(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	entity, err := harness.service.CreateEntity(t.Context(), Entity{TenantID: tenantID, Type: "store", Name: "Workspace"})
	require.NoError(t, err)
	other, err := harness.service.CreateEntity(t.Context(), Entity{TenantID: tenantID, Type: "store", Name: "Other"})
	require.NoError(t, err)

	role, err := harness.service.CreateRole(t.Context(), tenantID, "Store viewers", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "store_view")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "role_view")
	require.NoError(t, err)
	_, _, err = harness.service.PutEntityRoleBinding(
		t.Context(), tenantID, entity.ID, role.ID, principalID, BindingAllow, time.Time{},
	)
	require.NoError(t, err)

	granted, err := harness.service.EffectiveEntityPermissions(t.Context(), tenantID, entity.ID, principalID)
	require.NoError(t, err)
	assert.Contains(t, granted, "store_view", "a data grant that reaches this entity belongs in the reply")
	assert.Contains(t, granted, "role_view", "a non-data grant at this entity belongs in the reply")
	assert.NotContains(t, granted, "platform_administer",
		"a caller without platform authority must not be told they hold it")
	assert.True(t, sort.StringsAreSorted(granted), "the reply is ordered so a console can diff it")

	// The binding is what made those verbs reachable; another entity in the same
	// tenant must not inherit them.
	elsewhere, err := harness.service.EffectiveEntityPermissions(t.Context(), tenantID, other.ID, principalID)
	require.NoError(t, err)
	assert.NotContains(t, elsewhere, "store_view")

	for _, absent := range []struct{ tenant, entity, principal guid.ID }{
		{0, entity.ID, principalID}, {tenantID, 0, principalID}, {tenantID, entity.ID, 0},
	} {
		_, err := harness.service.EffectiveEntityPermissions(t.Context(), absent.tenant, absent.entity, absent.principal)
		assert.ErrorIs(t, err, ErrInvalidArgument)
	}
}

func TestEffectiveEntityPermissionsCarryPlatformAuthorityAndReportCheckFailures(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, entityID, principalID := guid.ID(1), guid.ID(2), guid.ID(11)
	harness.service.checker = entityPermissionEvaluator{}

	granted, err := harness.service.EffectiveEntityPermissions(t.Context(), tenantID, entityID, principalID)
	require.NoError(t, err)
	assert.Contains(t, granted, "platform_administer",
		"platform authority is not entity scoped, so it travels with the entity reply too")
	assert.Contains(t, granted, "store_view")
	assert.Contains(t, granted, "role_view")

	// A check that cannot be answered is never reported as a denial: a console
	// that hid the action would look identical to a revoked grant.
	for name, evaluator := range map[string]entityPermissionEvaluator{
		"platform": {platform: assert.AnError},
		"data":     {data: assert.AnError},
		"entity":   {explain: assert.AnError},
	} {
		t.Run(name, func(t *testing.T) {
			harness.service.checker = evaluator
			codes, err := harness.service.EffectiveEntityPermissions(t.Context(), tenantID, entityID, principalID)
			require.ErrorIs(t, err, assert.AnError)
			assert.Nil(t, codes)
		})
	}
}
