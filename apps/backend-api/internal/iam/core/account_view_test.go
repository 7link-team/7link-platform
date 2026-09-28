package core

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAccountViewAdmissionUsesLiveOperatorAndExactTargetOwner(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	h.seedMember(t, 2, 22, MemberActive)
	h.seedAdditionalMember(t, 2, 33, MemberActive)
	_, err := h.repository.GrantPlatformAdministrator(t.Context(), 33)
	require.NoError(t, err)
	members := NewMembershipChecker(h.runtime)
	require.NoError(t, members.BindForeignTables(publishedDirectoryTables("sl_"), publishedPrincipalTable("sl_")))
	m := &Module{runtime: h.runtime, checker: h.authorizer, members: members, registrar: authz.NewDeclarationOnlyRegistrar(h.service.registry)}
	entity, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 2, Name: "Entity", Type: "unit", Status: EntityActive})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, scope string
		target, id  guid.ID
		allowed     bool
	}{
		{"personal", "personal", 22, 22, true},
		{"personal-other-id", "personal", 22, 11, false},
		{"own-platform", "platform", 11, 1, true},
		{"tenant-member-not-platform", "platform", 22, 1, false},
		{"own-tenant", "tenant", 22, 2, true},
		{"platform-root-not-tenant", "tenant", 11, 1, false},
		{"platform-member-not-tenant", "tenant", 11, 2, false},
		{"own-entity", "entity", 22, entity.ID, true},
		{"other-entity-member", "entity", 11, entity.ID, false},
		{"ordinary-system", "system", 22, 1, false},
		{"administrator-system", "system", 33, 1, true},
		{"system-wrong-id", "system", 33, 2, false},
		{"organization-platform", "platform", 0, 1, true},
		{"organization-tenant", "tenant", 0, 2, true},
		{"organization-wrong-kind", "tenant", 0, 1, false},
		{"organization-not-a-personal-view", "personal", 0, 33, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := h.db.Transaction(func(tx *gorm.DB) error {
				return m.authorizeAccountView(t.Context(), tx, 33, tc.target, tc.scope, tc.id)
			})
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, authnext.ErrInvalidCredentials)
			}
		})
	}
	require.NoError(t, h.db.Exec("DELETE FROM sl_iam_platform_administrators WHERE principal_id = ?", 33).Error)
	err = h.db.Transaction(func(tx *gorm.DB) error { return m.authorizeAccountView(t.Context(), tx, 33, 22, "personal", 22) })
	require.ErrorIs(t, err, authnext.ErrInvalidCredentials, "removed operator authority must be effective on next use")
	require.ErrorIs(t, (&Module{}).authorizeAccountView(t.Context(), h.db, 33, 22, "system", 1), authnext.ErrInvalidCredentials)
}

func TestAccountViewWorkspaceFollowsVerifiedScope(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	h.seedMember(t, 2, 22, MemberActive)
	entity, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 2, Name: "Selected entity", Type: "unit", Status: EntityActive})
	require.NoError(t, err)
	workspace, err := h.service.accountViewWorkspace(t.Context(), "entity", entity.ID)
	require.NoError(t, err)
	require.Equal(t, entity.ID, workspace.Entity.ID)
	require.EqualValues(t, 2, workspace.Tenant.ID)
	require.Nil(t, workspace.OperatingPlatform)
	workspace, err = h.service.accountViewWorkspace(t.Context(), "platform", 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, workspace.OperatingPlatform.ID)
	require.Nil(t, workspace.Tenant)
	_, err = h.service.accountViewWorkspace(t.Context(), "tenant", 1)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = h.service.accountViewWorkspace(t.Context(), "platform", 2)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestOrganizationViewAuthorityIsScopedAndDoesNotCreateMembership(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	h.seedMember(t, 2, 22, MemberActive)
	h.seedMember(t, 3, 33, MemberActive)
	_, err := h.repository.GrantPlatformAdministrator(t.Context(), 33)
	require.NoError(t, err)
	members := NewMembershipChecker(h.runtime)
	require.NoError(t, members.BindForeignTables(publishedDirectoryTables("sl_"), publishedPrincipalTable("sl_")))
	for _, kind := range []string{"platform", "tenant"} {
		id := guid.ID(1)
		if kind == "tenant" {
			id = 2
		}
		ctx := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: 33, AccountView: &authnext.AccountViewIdentity{ID: 50, ActorID: 33, ScopeType: kind, ScopeID: id, TargetType: kind}})
		if kind == "platform" {
			allowed, err := h.authorizer.CheckOperatingPlatform(ctx, id, "role_create", 33)
			require.NoError(t, err)
			require.True(t, allowed)
			allowed, err = h.authorizer.CheckOperatingPlatform(t.Context(), id, "role_create", 33)
			require.NoError(t, err)
			require.False(t, allowed, "ordinary system cookie authority must not bypass platform membership")
		} else {
			allowed, err := h.authorizer.Check(ctx, id, "role_create", 33)
			require.NoError(t, err)
			require.True(t, allowed)
			constraint, err := h.authorizer.Constraint(ctx, id, "file_read", 33)
			require.NoError(t, err)
			require.True(t, constraint.AllowAll, "data constraints must agree with exact organization administration")
			allowed, err = h.authorizer.Check(t.Context(), id, "role_create", 33)
			require.NoError(t, err)
			require.False(t, allowed)
		}
		var count int64
		require.NoError(t, h.db.Table("sl_iam_tenant_members").Where("tenant_id = ? AND principal_id = ?", id, 33).Count(&count).Error)
		require.Zero(t, count)
	}
	require.NoError(t, h.db.Exec("DELETE FROM sl_iam_platform_administrators WHERE principal_id = ?", 33).Error)
	seedSystemPermissions(t, h, 33, "account_view_create")
	m := &Module{runtime: h.runtime, checker: h.authorizer, members: members, registrar: authz.NewDeclarationOnlyRegistrar(h.service.registry)}
	for _, scope := range []string{"platform", "tenant"} {
		id := guid.ID(1)
		if scope == "tenant" {
			id = 2
		}
		err := h.db.Transaction(func(tx *gorm.DB) error { return m.authorizeAccountView(t.Context(), tx, 33, 0, scope, id) })
		require.ErrorIs(t, err, authnext.ErrInvalidCredentials, "view creation authority alone must not grant organization administration")
	}
}

// A system view acts with the target's system authority, so admitting it is a
// grant of that authority to the operator. account_view_create alone once
// admitted a view of a full administrator, which made it a path to full
// authority. Every case is checked both on issuance and on reuse, which share
// authorizeAccountView.
func TestSystemAccountViewNeverExceedsOperatorAuthority(t *testing.T) {
	const operator, target guid.ID = 22, 33
	cases := []struct {
		name         string
		operatorFull bool
		operator     []string
		targetFull   bool
		target       []string
		admitted     bool
	}{
		{name: "delegated operator cannot view a full administrator", operator: []string{"account_view_create"}, targetFull: true},
		{name: "delegated operator cannot view a wider account", operator: []string{"account_view_create"}, target: []string{"system_menu_view"}},
		{name: "one missing permission refuses the view", operator: []string{"account_view_create", "system_menu_view"}, target: []string{"system_menu_view", "system_role_view"}},
		{name: "delegated operator views an account within its authority", operator: []string{"account_view_create", "system_menu_view"}, target: []string{"system_menu_view"}, admitted: true},
		{name: "full operator views a full administrator", operatorFull: true, targetFull: true, admitted: true},
		{name: "full operator views a delegated account", operatorFull: true, target: []string{"system_role_view"}, admitted: true},
		{name: "an account without system authority has no system view", operatorFull: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := seedSystemMenuHarness(t)
			seedTenantAndPrincipal(t, h.db, "sl_", 2, operator)
			h.seedAdditionalMember(t, 2, target, MemberActive)
			grant := func(principal guid.ID, full bool, codes []string) {
				if full {
					_, err := h.repository.GrantPlatformAdministrator(t.Context(), principal)
					require.NoError(t, err)
				}
				if len(codes) > 0 {
					seedSystemPermissions(t, h, principal, codes...)
				}
			}
			grant(operator, tc.operatorFull, tc.operator)
			grant(target, tc.targetFull, tc.target)
			members := NewMembershipChecker(h.runtime)
			require.NoError(t, members.BindForeignTables(publishedDirectoryTables("sl_"), publishedPrincipalTable("sl_")))
			m := &Module{runtime: h.runtime, checker: h.authorizer, members: members, registrar: authz.NewDeclarationOnlyRegistrar(h.service.registry)}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				return m.authorizeAccountView(t.Context(), tx, operator, target, "system", systemScopeID)
			})
			if tc.admitted {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, authnext.ErrInvalidCredentials)
			}
		})
	}
}

func TestAccountViewWorkspaceHTTPReportsOnlyTheVerifiedViewScope(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	h.seedMember(t, 2, 22, MemberActive)
	entity, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 2, Name: "Viewed entity", Type: "unit", Status: EntityActive})
	require.NoError(t, err)
	require.NoError(t, initializeHTTPLocales())
	verifier := &systemAccessVerifier{principal: 22}
	registrar := authz.NewRegistrar(h.service.registry, verifier, h.authorizer, httpMembershipChecker{})
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("account-view-workspace-test", "1"))
	registerAccountViewWorkspaceHTTP(api, h.service, registrar)
	client := humatest.Wrap(t, api)
	get := func() *httptest.ResponseRecorder {
		return client.Get("/account-views/workspace", "Authorization: Bearer test")
	}

	require.Equal(t, http.StatusForbidden, get().Code, "an ordinary session has no view workspace")

	verifier.view = &authnext.AccountViewIdentity{ID: 50, ActorID: 33, ScopeType: "personal", ScopeID: 22}
	response := get()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	workspace := decodeEnvelope[AccountViewWorkspace](t, response).Data
	require.Equal(t, AccountViewWorkspace{ScopeType: "personal", ScopeID: 22}, workspace)

	verifier.view = &authnext.AccountViewIdentity{ID: 50, ActorID: 33, ScopeType: "platform", ScopeID: 1}
	response = get()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	workspace = decodeEnvelope[AccountViewWorkspace](t, response).Data
	require.NotNil(t, workspace.OperatingPlatform)
	require.Equal(t, guid.ID(1), workspace.OperatingPlatform.ID)
	require.Equal(t, "platform-1", workspace.OperatingPlatform.Slug)
	require.Equal(t, "active", workspace.OperatingPlatform.Status)
	require.Nil(t, workspace.Tenant)
	require.Nil(t, workspace.Entity)

	verifier.view = &authnext.AccountViewIdentity{ID: 50, ActorID: 33, ScopeType: "entity", ScopeID: entity.ID}
	response = get()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	workspace = decodeEnvelope[AccountViewWorkspace](t, response).Data
	require.NotNil(t, workspace.Entity)
	require.Equal(t, entity.ID, workspace.Entity.ID)
	require.Equal(t, "Viewed entity", workspace.Entity.Name)
	require.NotNil(t, workspace.Tenant)
	require.Equal(t, guid.ID(2), workspace.Tenant.ID, "an entity view resolves its owning tenant")

	// A view whose scope no longer resolves to an active root of its kind admits
	// nothing, exactly like a request without a view; it once answered 500.
	failsClosed := func(name string) {
		response := get()
		require.Equal(t, http.StatusForbidden, response.Code, name)
		require.Nil(t, decodeEnvelope[*AccountViewWorkspace](t, response).Data, name)
	}
	for name, view := range map[string]*authnext.AccountViewIdentity{
		"tenant view of a platform root": {ID: 50, ActorID: 33, ScopeType: "tenant", ScopeID: 1},
		"platform view of a tenant root": {ID: 50, ActorID: 33, ScopeType: "platform", ScopeID: 2},
		"missing entity":                 {ID: 50, ActorID: 33, ScopeType: "entity", ScopeID: 987654321},
	} {
		verifier.view = view
		failsClosed(name)
	}
	require.NoError(t, h.db.Exec("UPDATE sl_iam_tenants SET status = 'suspended' WHERE id = 2").Error)
	verifier.view = &authnext.AccountViewIdentity{ID: 50, ActorID: 33, ScopeType: "tenant", ScopeID: 2}
	failsClosed("a suspended root has no workspace")
}
