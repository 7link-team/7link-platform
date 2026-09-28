package iamintegration

import (
	"net/http"
	"testing"
	"time"

	iamauthn "7link.com/platform/internal/iam/authn"
	coremodel "7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	organizationmodel "7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every path that grants authority must require the actor to be able to grant
// it. The actor below holds each route's permission, so every refusal comes
// from the grantability check rather than the route guard, and each probe is
// paired with the direct grant it would otherwise stand in for.
func TestComposedIndirectGrantPathsRequireGrantableAuthority(t *testing.T) {
	f := newIAMTenantFixture(t)
	identities := requireIAMModule[*identity.Module](t, f, identity.Name)
	authns := requireIAMModule[*iamauthn.Module](t, f, iamauthn.Name)
	actor := f.createPrincipal(t, identities, iamTenantB, "limited-b", "limited-b@example.test")
	token, _, _ := f.issueToken(t, authns, actor.ID, iamTenantB)
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	role := func(id guid.ID, codes ...string) guid.ID {
		require.NoError(t, f.db.Table(f.table("iam_roles")).Create(&coremodel.Role{TenantID: iamTenantB, ID: id, Name: "Role " + id.String(), NameKey: "role-" + id.String(), CreatedAt: stamp, UpdatedAt: stamp}).Error)
		for _, code := range codes {
			require.NoError(t, f.db.Table(f.table("iam_role_permissions")).Create(&coremodel.RolePermission{TenantID: iamTenantB, RoleID: id, PermissionCode: code, CreatedAt: stamp}).Error)
		}
		return id
	}
	limited := role(301, "entity_manage_binding", "role_update", "role_view", "role_grant_permission", "role_manage_member",
		"todo_view", "invitation_create", "invitation_resend", "access_request_approve", "user_update", "user_view")
	require.NoError(t, f.db.Table(f.table("iam_role_members")).Create(&coremodel.RoleMember{TenantID: iamTenantB, RoleID: limited, PrincipalID: actor.ID, CreatedAt: stamp}).Error)
	require.NoError(t, f.db.Table(f.table("iam_role_data_scopes")).Create(&organizationmodel.RoleDataScope{TenantID: iamTenantB, RoleID: limited, ScopeType: "self", UpdatedAt: stamp}).Error)
	administrators := role(302, "tenant_administer")
	directGrant := func(t *testing.T, permission string) {
		t.Helper()
		target := role(guid.ID(400 + f.count(t, "iam_roles", "tenant_id = ?", iamTenantB)))
		requireStatus(t, http.StatusForbidden, f.request(t, http.MethodPut, "/roles/"+target.String()+"/permissions/"+permission, 0, token, nil, nil))
		assert.Zero(t, f.count(t, "iam_role_permissions", "tenant_id = ? AND role_id = ?", iamTenantB, target))
	}
	requireAdministratorNotGranted := func(t *testing.T) {
		t.Helper()
		requireStatus(t, http.StatusForbidden, f.request(t, http.MethodPut, "/roles/"+administrators.String()+"/members/"+f.requesterB.ID.String(), 0, token, nil, nil))
		assert.Zero(t, f.count(t, "iam_role_members", "tenant_id = ? AND role_id = ?", iamTenantB, administrators))
		assert.Zero(t, f.count(t, "iam_temporary_role_grants", "tenant_id = ? AND role_id = ?", iamTenantB, administrators))
	}

	t.Run("relationship", func(t *testing.T) {
		response := f.request(t, http.MethodPost, "/entities", iamTenantB, f.tokenB, map[string]any{"type": "company", "name": "Owned company", "status": "active"}, nil)
		requireStatus(t, http.StatusCreated, response)
		entity := decodeEnvelope[struct {
			ID guid.ID `json:"id"`
		}](t, response)
		directGrant(t, "entity_delete")
		for _, relation := range []string{"owner", "editor"} {
			response = f.request(t, http.MethodPost, "/authorization-relationships", iamTenantB, token, map[string]any{
				"subject_type": "principal", "subject_id": actor.ID.String(), "relation": relation,
				"resource_type": "company", "resource_id": entity.ID.String(),
			}, nil)
			requireStatus(t, http.StatusForbidden, response)
		}
		assert.Zero(t, f.count(t, "iam_relationships", "tenant_id = ? AND subject_id = ?", iamTenantB, actor.ID))
		requireStatus(t, http.StatusForbidden, f.request(t, http.MethodDelete, "/entities/"+entity.ID.String(), 0, token, nil, nil))
	})

	t.Run("data-scope", func(t *testing.T) {
		directGrant(t, "todo_export")
		requireStatus(t, http.StatusForbidden, f.request(t, http.MethodPut, "/roles/"+limited.String()+"/data-scope", 0, token, map[string]any{"scope": "all"}, nil))
		assert.Equal(t, int64(1), f.count(t, "iam_role_data_scopes", "tenant_id = ? AND role_id = ? AND scope_type = 'self'", iamTenantB, limited))
	})

	t.Run("invitation", func(t *testing.T) {
		requireAdministratorNotGranted(t)
		response := f.request(t, http.MethodPost, "/membership-invitations", iamTenantB, token, map[string]any{
			"email": "escalated@example.test", "role_ids": []string{administrators.String()},
		}, nil)
		requireStatus(t, http.StatusForbidden, response)
		assert.Zero(t, f.count(t, "iam_invitations", "tenant_id = ? AND email_key = ?", iamTenantB, "escalated@example.test"))

		response = f.request(t, http.MethodPost, "/membership-invitations", iamTenantB, f.tokenB, map[string]any{
			"email": "issued@example.test", "role_ids": []string{administrators.String()},
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		issued := decodeEnvelope[struct {
			Invitation organization.Invitation `json:"invitation"`
		}](t, response)
		var before organizationmodel.Invitation
		require.NoError(t, f.db.Table(f.table("iam_invitations")).Where("id = ?", issued.Invitation.ID).Take(&before).Error)
		requireStatus(t, http.StatusForbidden, f.request(t, http.MethodPost, "/membership-invitations/"+issued.Invitation.ID.String()+"/deliveries", 0, token, map[string]any{}, nil))
		var after organizationmodel.Invitation
		require.NoError(t, f.db.Table(f.table("iam_invitations")).Where("id = ?", issued.Invitation.ID).Take(&after).Error)
		assert.Equal(t, before.TokenHMAC, after.TokenHMAC, "the credential the actor could hand out is unchanged")
	})

	t.Run("access-request", func(t *testing.T) {
		requireAdministratorNotGranted(t)
		response := f.request(t, http.MethodPost, "/role-access-requests", iamTenantB, f.requester, map[string]any{
			"role_id": administrators, "reason": "escalation probe", "access_expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		request := decodeEnvelope[governance.AccessRequest](t, response)
		response = f.request(t, http.MethodPut, "/role-access-requests/"+request.ID.String()+"/decision", 0, token, map[string]any{"decision": "approved"}, nil)
		requireStatus(t, http.StatusForbidden, response)
		assert.Equal(t, int64(1), f.count(t, "iam_access_requests", "tenant_id = ? AND id = ? AND status = ?", iamTenantB, request.ID, governance.StatusPending))
		requireAdministratorNotGranted(t)
	})

	t.Run("suspension", func(t *testing.T) {
		shared := f.createPrincipal(t, identities, iamTenantA, "shared-ab", "shared-ab@example.test")
		require.NoError(t, f.db.Table(f.table("iam_tenant_members")).Create(&coremodel.TenantMember{TenantID: iamTenantB, PrincipalID: shared.ID, DisplayName: shared.DisplayName, Email: shared.Email, Status: "active", CreatedAt: stamp, UpdatedAt: stamp}).Error)
		for _, target := range []identity.Principal{f.adminB, shared} {
			requireStatus(t, http.StatusForbidden, f.request(t, http.MethodPut, "/system/principals/"+target.ID.String()+"/suspension", 0, token, map[string]any{}, nil))
			requireStatus(t, http.StatusConflict, f.request(t, http.MethodPut, "/principals/"+target.ID.String()+"/suspension", iamTenantB, token, nil, nil))
			assert.Equal(t, int64(1), f.count(t, "iam_principals", "id = ? AND status = 'active'", target.ID))
		}
		requireStatus(t, http.StatusOK, f.request(t, http.MethodPut, "/principals/"+f.requesterB.ID.String()+"/suspension", iamTenantB, token, nil, nil))
	})
}
