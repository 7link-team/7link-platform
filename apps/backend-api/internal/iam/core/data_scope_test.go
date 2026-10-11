package core

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Widening a role's data scope grants every holder, the actor included, more
// data than before; it must stay within what the actor could grant directly.
func TestSetRoleDataScopeCannotWidenBeyondTheActorsOwnScope(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, actorID := guid.ID(1), guid.ID(10)
	harness.seedMember(t, tenantID, actorID, MemberActive)
	role := func(name string, scope DataScope, codes ...string) guid.ID {
		created, err := harness.repository.CreateRole(t.Context(), tenantID, name, "")
		require.NoError(t, err)
		for _, code := range codes {
			_, err = harness.repository.GrantPermission(t.Context(), tenantID, created.ID, code)
			require.NoError(t, err)
		}
		_, _, err = harness.service.SetRoleDataScope(t.Context(), tenantID, created.ID, scope, nil)
		require.NoError(t, err)
		return created.ID
	}
	own := role("Own tasks", DataScopeSelf, "todo_view", "role_update")
	_, err := harness.repository.AddMember(t.Context(), tenantID, own, actorID, nil)
	require.NoError(t, err)
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, PrincipalID: actorID, TenantID: tenantID})
	allowAll := func() bool {
		constraint, err := harness.authorizer.Constraint(t.Context(), tenantID, "todo_view", actorID)
		require.NoError(t, err)
		return constraint.AllowAll
	}
	scopeOf := func(id guid.ID) DataScope {
		scope, err := harness.service.GetRoleDataScope(t.Context(), tenantID, id)
		require.NoError(t, err)
		return scope.Scope
	}
	require.False(t, allowAll())

	// Control: the actor cannot grant a role outside its authority directly.
	exporters := role("Exporters", DataScopeSelf, "todo_export")
	_, err = harness.service.AddMember(actor, tenantID, exporters, actorID, nil)
	require.ErrorIs(t, err, ErrPrivilegeEscalation)
	for _, scope := range []DataScope{DataScopeDepartment, DataScopeAll} {
		_, _, err = harness.service.SetRoleDataScope(actor, tenantID, exporters, scope, nil)
		require.ErrorIs(t, err, ErrPrivilegeEscalation, "a role the actor cannot grant cannot be rescoped either")
	}
	assert.Equal(t, DataScopeSelf, scopeOf(exporters))

	_, _, err = harness.service.SetRoleDataScope(actor, tenantID, own, DataScopeAll, nil)
	require.ErrorIs(t, err, ErrPrivilegeEscalation, "todo_view is held only for the actor's own rows")
	peer := role("Peer tasks", DataScopeSelf, "todo_view")
	_, _, err = harness.service.SetRoleDataScope(actor, tenantID, peer, DataScopeAll, nil)
	require.ErrorIs(t, err, ErrPrivilegeEscalation)
	assert.Equal(t, DataScopeSelf, scopeOf(own))
	assert.Equal(t, DataScopeSelf, scopeOf(peer))
	assert.False(t, allowAll(), "authority is unchanged after the refused widenings")

	// Department scopes widen self just as all does, while a full administrator is unaffected.
	for _, scope := range []DataScope{DataScopeDepartment, DataScopeDepartmentAndDescendants} {
		_, _, err = harness.service.SetRoleDataScope(actor, tenantID, peer, scope, nil)
		require.ErrorIs(t, err, ErrPrivilegeEscalation, "the actor holds todo_view for its own rows only")
	}
	assert.Equal(t, DataScopeSelf, scopeOf(peer))
	platformAdministratorID := guid.ID(12)
	harness.seedAdditionalMember(t, tenantID, platformAdministratorID, MemberActive)
	require.NoError(t, harness.db.Exec("INSERT INTO sl_iam_platform_administrators(principal_id,created_at) VALUES (?,?)", platformAdministratorID, repositoryTestTime).Error)
	platformAdministrator := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, PrincipalID: platformAdministratorID, TenantID: tenantID})
	_, changed, err := harness.service.SetRoleDataScope(platformAdministrator, tenantID, own, DataScopeAll, nil)
	require.NoError(t, err)
	assert.True(t, changed, "a platform administrator holds no tenant role yet is a full administrator")
	administrators := role("Administrators", DataScopeAll, "tenant_administer")
	_, err = harness.repository.AddMember(t.Context(), tenantID, administrators, actorID, nil)
	require.NoError(t, err)
	_, changed, err = harness.service.SetRoleDataScope(actor, tenantID, peer, DataScopeAll, nil)
	require.NoError(t, err)
	assert.True(t, changed)
}
