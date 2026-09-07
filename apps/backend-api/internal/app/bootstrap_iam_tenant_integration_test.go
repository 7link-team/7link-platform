package app

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	iamTenantA guid.ID = 91001
	iamTenantB guid.ID = 91002
)

type iamTenantFixture struct {
	app        *App
	server     *httptest.Server
	client     *http.Client
	db         *gorm.DB
	prefix     string
	adminA     identity.Principal
	adminB     identity.Principal
	requesterB identity.Principal
	tokenA     string
	tokenB     string
	requester  string
	scimTokenA string
}

type iamResponseEnvelope struct {
	Data json.RawMessage `json:"data"`
}

type iamHTTPResponse struct {
	StatusCode int
	Header     http.Header
	Cookies    []*http.Cookie
	Body       []byte
}

func TestComposedIAMTenantIsolationAcrossModules(t *testing.T) {
	fixture := newIAMTenantFixture(t)

	invitation := fixture.acceptTenantBInvitation(t)
	user, group := fixture.provisionTenantBGroup(t)
	role, entity, request := fixture.approveTenantBAccessRequest(t)

	fixture.assertSCIMCannotCrossTenants(t, user, group)
	fixture.assertGovernanceRevokeCannotCrossTenants(t, request)
	fixture.assertIAMAuthorizationCannotCrossTenants(t, role, entity)
	fixture.assertFederationCallbackCannotCrossTenants(t)
	fixture.assertTenantBAuditTargetsNeverAppearInTenantA(t, invitation.ID, group.ID, request.ID, entity.ID)
}

func TestComposedSCIMTenantLifecycle(t *testing.T) {
	newIAMTenantFixture(t).runSCIMTenantLifecycle(t)
}

func TestComposedBrowserSessionTenant(t *testing.T) {
	newIAMTenantFixture(t).runBrowserSessionTenant(t)
}

func (f *iamTenantFixture) runBrowserSessionTenant(t *testing.T) {
	t.Helper()
	response := f.request(t, http.MethodPost, "/authn/sessions", 0, "", map[string]any{
		"login_name": f.requesterB.LoginName, "password": "correct horse battery staple",
	}, map[string]string{"Origin": "https://app.example.test"})
	requireStatus(t, http.StatusOK, response)
	require.NotEmpty(t, response.Cookies)
	headers := map[string]string{"Cookie": response.Cookies[0].String()}
	check := func(requested guid.ID, code int, expected string) {
		t.Helper()
		for _, path := range []string{"/authn/me", "/authn/sessions/current"} {
			response := f.request(t, http.MethodGet, path, requested, "", nil, headers)
			requireStatus(t, code, response)
			if code == http.StatusOK {
				data := decodeEnvelope[map[string]any](t, response)
				assert.Equal(t, f.requesterB.ID.String(), data["subject"])
				if expected == "" {
					assert.NotContains(t, data, "organization_id")
				} else {
					assert.Equal(t, expected, data["organization_id"])
				}
			}
		}
	}
	assert.Zero(t, f.count(t, "iam_platform_administrators", "principal_id = ?", f.requesterB.ID))
	check(0, http.StatusOK, iamTenantB.String())
	check(iamTenantA, http.StatusForbidden, "")
	response = f.request(t, http.MethodPost, "/iam/members", iamTenantA, f.tokenA, map[string]any{
		"subject": f.requesterB.ID.String(), "display_name": "Session member", "email": f.requesterB.Email, "status": "active",
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	check(0, http.StatusOK, "")
	check(iamTenantA, http.StatusOK, iamTenantA.String())
	check(iamTenantB, http.StatusOK, iamTenantB.String())
	response = f.request(t, http.MethodPatch, "/iam/members/"+f.requesterB.ID.String(), iamTenantA, f.tokenA,
		map[string]any{"status": "disabled"}, nil)
	requireStatus(t, http.StatusOK, response)
	check(iamTenantA, http.StatusForbidden, "")
	check(0, http.StatusOK, iamTenantB.String())
	require.NoError(t, f.db.Table(f.table("iam_tenants")).Where("id = ?", iamTenantB).Update("status", "suspended").Error)
	check(iamTenantB, http.StatusForbidden, "")
	check(0, http.StatusOK, "")
	require.NoError(t, f.db.Table(f.table("iam_tenants")).Where("id = ?", iamTenantB).Update("status", "active").Error)
	check(0, http.StatusOK, iamTenantB.String())
	response = f.request(t, http.MethodGet, "/authn/sessions/current", iamTenantA, f.requester, nil, nil)
	requireStatus(t, http.StatusForbidden, response)
	response = f.request(t, http.MethodGet, "/authn/sessions/current", iamTenantB, f.requester, nil, nil)
	requireStatus(t, http.StatusOK, response)
}

func (f *iamTenantFixture) runSCIMTenantLifecycle(t *testing.T) {
	t.Helper()
	response := f.request(t, http.MethodPost, "/iam/tenants", 0, f.tokenA, map[string]any{
		"slug": "scim-lifecycle", "name": "SCIM lifecycle", "owner_principal_id": f.adminA.ID.String(),
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	tenant := decodeEnvelope[organization.Tenant](t, response)
	directory, token := f.createSCIMCredential(t, tenant.ID, f.tokenA, "SCIM lifecycle")
	active := true
	userInput := provisioning.UserInput{
		Schemas: []string{provisioning.UserSchema}, UserName: "scim-lifecycle", DisplayName: "SCIM lifecycle",
		Active: &active, Emails: []provisioning.UserEmail{{Value: "scim-lifecycle@example.test", Primary: true}},
	}
	response = f.request(t, http.MethodPost, "/scim/v2/Users", 0, token, userInput, nil)
	requireStatus(t, http.StatusCreated, response)
	user := decodeBody[provisioning.UserResource](t, response)
	groupInput := provisioning.GroupInput{
		Schemas: []string{provisioning.GroupSchema}, DisplayName: "SCIM lifecycle",
		Members: []provisioning.SCIMGroupMember{{Value: user.ID}},
	}
	response = f.request(t, http.MethodPost, "/scim/v2/Groups", 0, token, groupInput, nil)
	requireStatus(t, http.StatusCreated, response)
	group := decodeBody[provisioning.GroupResource](t, response)

	setStatus := func(status string) {
		response := f.request(t, http.MethodPatch, "/iam/tenants/"+tenant.ID.String(), 0, f.tokenA,
			map[string]any{"status": status, "version": tenant.Version}, nil)
		requireStatus(t, http.StatusOK, response)
		tenant = decodeEnvelope[organization.Tenant](t, response)
		require.Equal(t, status, tenant.Status)
	}
	assertReadable := func() {
		response := f.request(t, http.MethodGet, "/scim/v2/Users/"+user.ID, 0, token, nil, nil)
		requireStatus(t, http.StatusOK, response)
		assert.Equal(t, user, decodeBody[provisioning.UserResource](t, response))
		response = f.request(t, http.MethodGet, "/scim/v2/Groups/"+group.ID, 0, token, nil, nil)
		requireStatus(t, http.StatusOK, response)
		assert.Equal(t, group, decodeBody[provisioning.GroupResource](t, response))
	}
	assertRejected := func(t *testing.T) {
		t.Helper()
		counts := make(map[string]int64)
		for _, table := range []string{"iam_audit_events", "iam_principals", "iam_groups", "iam_group_members", "iam_tenant_members", "iam_scim_resources"} {
			counts[table] = f.count(t, table, "1 = 1")
		}
		patch := provisioning.PatchRequest{Schemas: []string{provisioning.PatchSchema}, Operations: []provisioning.PatchOperation{
			{Op: "replace", Path: "displayName", Value: json.RawMessage(`"Unauthorized"`)},
		}}
		operations := []struct {
			method, path, version string
			body                  any
		}{
			{http.MethodGet, "/Users", "", nil},
			{http.MethodGet, "/Users/" + user.ID, "", nil},
			{http.MethodPost, "/Users", "", userInput},
			{http.MethodPut, "/Users/" + user.ID, user.Meta.Version, userInput},
			{http.MethodPatch, "/Users/" + user.ID, user.Meta.Version, patch},
			{http.MethodDelete, "/Users/" + user.ID, user.Meta.Version, nil},
			{http.MethodGet, "/Groups", "", nil},
			{http.MethodGet, "/Groups/" + group.ID, "", nil},
			{http.MethodPost, "/Groups", "", groupInput},
			{http.MethodPut, "/Groups/" + group.ID, group.Meta.Version, groupInput},
			{http.MethodPatch, "/Groups/" + group.ID, group.Meta.Version, patch},
			{http.MethodDelete, "/Groups/" + group.ID, group.Meta.Version, nil},
			{http.MethodPost, "/Bulk", "", provisioning.BulkRequest{Schemas: []string{provisioning.BulkSchema}, Operations: []provisioning.BulkOperation{
				{Method: http.MethodDelete, Path: "/Users/" + user.ID, Version: user.Meta.Version},
			}}},
		}
		for _, operation := range operations {
			t.Run(operation.method+operation.path, func(t *testing.T) {
				response := f.request(t, operation.method, "/scim/v2"+operation.path, 0, token, operation.body,
					map[string]string{"If-Match": operation.version})
				requireStatus(t, http.StatusUnauthorized, response)
				failure := decodeBody[provisioning.SCIMError](t, response)
				assert.Equal(t, "401", failure.Status)
				for table, count := range counts {
					assert.Equal(t, count, f.count(t, table, "1 = 1"), table)
				}
				assert.Zero(t, f.count(t, "iam_scim_resources", "directory_id = ? AND (version <> 1 OR deleted_at IS NOT NULL)", directory.ID))
			})
		}
	}

	assertReadable()
	setStatus(organization.TenantSuspended)
	require.True(t, t.Run("suspended", assertRejected))
	setStatus(organization.TenantActive)
	assertReadable()
	response = f.request(t, http.MethodDelete, "/iam/tenants/"+tenant.ID.String()+"?version="+strconv.FormatInt(tenant.Version, 10), 0, f.tokenA, nil, nil)
	requireStatus(t, http.StatusOK, response)
	assert.Equal(t, int64(1), f.count(t, "iam_tenants", "id = ? AND status = ?", tenant.ID, organization.TenantDeleted))
	require.True(t, t.Run("deleted", assertRejected))
}

func newIAMTenantFixture(t *testing.T) *iamTenantFixture {
	t.Helper()
	return newIAMTenantFixtureWithDatabase(t, Database{
		Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "iam-tenant.sqlite"), Writable: true, Readable: true,
	}, "")
}

func newIAMTenantFixtureWithDatabase(t *testing.T, database Database, prefix string, configure ...func(*AppConfig)) *iamTenantFixture {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	key := base64.RawStdEncoding.EncodeToString(seed)
	application := &App{config: AppConfig{
		Name: "iam-tenant-integration", Version: "1", Timezone: "UTC", Migrate: true,
		TablePrefix: prefix,
		Database:    map[string]Database{"primary": database},
		Authn: IAMAuthn(authnext.Config{
			Enabled: true, Issuer: "https://iam.example.test", SigningKey: key, AccessTokenTTL: time.Hour,
			Web: authnext.WebConfig{
				Enabled: true, PostLoginURL: "https://app.example.test/home",
				PostLogoutURL:     "https://app.example.test/signed-out",
				AllowedReturnURLs: []string{"https://app.example.test/home"},
				AllowedOrigins:    []string{"https://app.example.test"},
			},
			MFA: authnext.MFAConfig{EncryptionKey: key},
		}),
		Federation:   IAMFederation{Enabled: true, EncryptionKey: key},
		Provisioning: IAMProvisioning{EncryptionKey: key},
	}}
	for _, apply := range configure {
		apply(&application.config)
	}
	require.NoError(t, application.bootstrap(t.Context()))
	t.Cleanup(func() { assert.NoError(t, application.shutdown()) })

	router := chi.NewRouter()
	require.NoError(t, application.mountHTTPMiddlewares(router))
	api := humax.New(router, humax.Config{Name: application.config.Name, Version: application.config.Version})
	require.NoError(t, application.modules.RegisterHTTP(t.Context(), api))
	for _, route := range application.modules.RawHTTPRoutes() {
		mountRawRoute(router, route)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	fixture := &iamTenantFixture{
		app: application, server: server, client: client, db: application.runtime.Database().Write(), prefix: prefix,
	}
	for _, tenantID := range []guid.ID{iamTenantA, iamTenantB} {
		require.NoError(t, organization.EnsureTenant(t.Context(), application.runtime, tenantID))
	}

	identityModuleValue, ok := application.modules.ByName(identity.Name)
	require.True(t, ok)
	identityModule, ok := identityModuleValue.(*identity.Module)
	require.True(t, ok)
	fixture.adminA = fixture.createPrincipal(t, identityModule, iamTenantA, "admin-a", "admin-a@example.test")
	fixture.adminB = fixture.createPrincipal(t, identityModule, iamTenantB, "admin-b", "admin-b@example.test")
	fixture.requesterB = fixture.createPrincipal(t, identityModule, iamTenantB, "requester-b", "requester-b@example.test")

	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, principalID := range []guid.ID{fixture.adminA.ID, fixture.adminB.ID} {
		require.NoError(t, fixture.db.Table(fixture.table("iam_platform_administrators")).Create(map[string]any{
			"principal_id": principalID, "created_at": now,
		}).Error)
	}

	authnModuleValue, ok := application.modules.ByName(iamauthn.Name)
	require.True(t, ok)
	authnModule, ok := authnModuleValue.(*iamauthn.Module)
	require.True(t, ok)
	fixture.tokenA, _, _ = fixture.issueToken(t, authnModule, fixture.adminA.ID, iamTenantA)
	fixture.tokenB, _, _ = fixture.issueToken(t, authnModule, fixture.adminB.ID, iamTenantB)
	fixture.requester, _, _ = fixture.issueToken(t, authnModule, fixture.requesterB.ID, iamTenantB)
	return fixture
}

func (f *iamTenantFixture) createPrincipal(t *testing.T, module *identity.Module, tenantID guid.ID, login, email string) identity.Principal {
	t.Helper()
	principal, err := module.Service.Create(t.Context(), tenantID, login, "correct horse battery staple", login, email)
	require.NoError(t, err)
	return principal
}

func (f *iamTenantFixture) issueToken(t *testing.T, module *iamauthn.Module, principalID, tenantID guid.ID) (string, int64, error) {
	t.Helper()
	token, expiresIn, err := module.Service.IssueTenantAccessToken(t.Context(), principalID, tenantID.String(), "", "")
	require.NoError(t, err)
	return token, expiresIn, err
}

func (f *iamTenantFixture) acceptTenantBInvitation(t *testing.T) organization.Invitation {
	t.Helper()
	before := f.auditCounts(t)
	response := f.request(t, http.MethodPost, "/iam/invitations", iamTenantB, f.tokenB, map[string]any{
		"email": "invited-b@example.test", "expires_in_hours": 24,
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	issued := decodeEnvelope[struct {
		Invitation organization.Invitation `json:"invitation"`
		Token      string                  `json:"token"`
	}](t, response)
	afterCreate := f.auditCounts(t)
	assert.Equal(t, before[0], afterCreate[0])
	assert.Equal(t, before[1]+1, afterCreate[1])

	beforeAccept := afterCreate
	response = f.request(t, http.MethodPost, "/iam/invitations/accept", 0, "", map[string]any{
		"token": issued.Token, "login_name": "invited-b", "password": "correct horse battery staple",
		"display_name": "Invited B",
	}, nil)
	requireStatus(t, http.StatusOK, response)
	accepted := decodeEnvelope[organization.InvitationAcceptance](t, response)
	assert.Equal(t, iamTenantB, accepted.TenantID)
	assert.False(t, accepted.PrincipalID.Zero())
	afterAccept := f.auditCounts(t)
	assert.Equal(t, beforeAccept[0], afterAccept[0])
	assert.Equal(t, beforeAccept[1]+1, afterAccept[1])

	assert.Equal(t, int64(1), f.count(t, "iam_principals", "id = ? AND email = ?", accepted.PrincipalID, "invited-b@example.test"))
	assert.Equal(t, int64(1), f.count(t, "iam_tenant_members", "tenant_id = ? AND principal_id = ? AND status = 'active'", iamTenantB, accepted.PrincipalID))
	assert.Zero(t, f.count(t, "iam_tenant_members", "tenant_id = ? AND principal_id = ?", iamTenantA, accepted.PrincipalID))
	assert.Equal(t, int64(1), f.count(t, "iam_audit_events", "tenant_id = ? AND event_type = ? AND target_id = ?", iamTenantB, "invitation_accepted", issued.Invitation.ID))
	return issued.Invitation
}

func (f *iamTenantFixture) provisionTenantBGroup(t *testing.T) (provisioning.UserResource, provisioning.GroupResource) {
	t.Helper()
	_, tokenA := f.createSCIMCredential(t, iamTenantA, f.tokenA, "Tenant A")
	_, tokenB := f.createSCIMCredential(t, iamTenantB, f.tokenB, "Tenant B")

	active := true
	response := f.request(t, http.MethodPost, "/scim/v2/Users", 0, tokenB, provisioning.UserInput{
		Schemas: []string{provisioning.UserSchema}, ExternalID: "tenant-b-user", UserName: "scim-b",
		DisplayName: "SCIM B", Active: &active,
		Emails: []provisioning.UserEmail{{Value: "scim-b@example.test", Type: "work", Primary: true}},
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	user := decodeBody[provisioning.UserResource](t, response)

	response = f.request(t, http.MethodPost, "/scim/v2/Groups", 0, tokenB, provisioning.GroupInput{
		Schemas: []string{provisioning.GroupSchema}, ExternalID: "tenant-b-group", DisplayName: "Tenant B Operators",
		Members: []provisioning.SCIMGroupMember{{Value: user.ID}},
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	group := decodeBody[provisioning.GroupResource](t, response)
	groupID, err := guid.Parse(group.ID)
	require.NoError(t, err)
	userID, err := guid.Parse(user.ID)
	require.NoError(t, err)

	assert.Equal(t, int64(1), f.count(t, "iam_groups", "tenant_id = ? AND id = ? AND name = ?", iamTenantB, groupID, group.DisplayName))
	assert.Zero(t, f.count(t, "iam_groups", "tenant_id = ? AND id = ?", iamTenantA, groupID))
	assert.Equal(t, int64(1), f.count(t, "iam_group_members", "tenant_id = ? AND group_id = ? AND principal_id = ?", iamTenantB, groupID, userID))
	assert.Equal(t, int64(1), f.count(t, "iam_tenant_members", "tenant_id = ? AND principal_id = ?", iamTenantB, userID))
	assert.Zero(t, f.count(t, "iam_tenant_members", "tenant_id = ? AND principal_id = ?", iamTenantA, userID))

	// Retain tenant A's credential for the explicit cross-directory attempts.
	f.scimTokenA = tokenA
	return user, group
}

func (f *iamTenantFixture) createSCIMCredential(t *testing.T, tenantID guid.ID, adminToken, name string) (provisioning.Directory, string) {
	t.Helper()
	response := f.request(t, http.MethodPost, "/iam/service-accounts", tenantID, adminToken,
		map[string]any{"login_name": "scim-" + strings.ReplaceAll(strings.ToLower(name), " ", "-"), "display_name": name}, nil)
	requireStatus(t, http.StatusCreated, response)
	account := decodeEnvelope[identity.ServiceAccount](t, response)
	response = f.request(t, http.MethodPost, "/iam/scim/directories", tenantID, adminToken,
		map[string]any{"name": name, "service_account_id": account.ID.String()}, nil)
	requireStatus(t, http.StatusCreated, response)
	directory := decodeEnvelope[provisioning.Directory](t, response)
	response = f.request(t, http.MethodPost, "/iam/scim/directories/"+directory.ID.String()+"/credentials", tenantID, adminToken, map[string]any{"name": "integration"}, nil)
	requireStatus(t, http.StatusCreated, response)
	secret := decodeEnvelope[provisioning.CredentialSecret](t, response)
	return directory, secret.Token
}

func (f *iamTenantFixture) approveTenantBAccessRequest(t *testing.T) (core.APIRole, core.APIEntity, governance.AccessRequest) {
	t.Helper()
	response := f.request(t, http.MethodPost, "/iam/roles", iamTenantB, f.tokenB, map[string]any{
		"name": "Tenant B entity viewers", "description": "integration fixture",
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	role := decodeEnvelope[core.APIRole](t, response)

	response = f.request(t, http.MethodPut, "/iam/roles/"+role.ID+"/permissions/entity_view", iamTenantB, f.tokenB, nil, nil)
	requireStatus(t, http.StatusOK, response)

	response = f.request(t, http.MethodPost, "/iam/entities", iamTenantB, f.tokenB, map[string]any{
		"type": "company", "name": "Tenant B company", "status": "active",
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	entity := decodeEnvelope[core.APIEntity](t, response)

	response = f.request(t, http.MethodPost, "/iam/access-requests", iamTenantB, f.requester, map[string]any{
		"role_id": role.ID, "reason": "integration tenant isolation", "access_expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	request := decodeEnvelope[governance.AccessRequest](t, response)

	response = f.request(t, http.MethodPost, "/iam/access-requests/"+request.ID.String()+"/approve", iamTenantB, f.tokenB, map[string]any{"note": "approved"}, nil)
	requireStatus(t, http.StatusOK, response)
	approved := decodeEnvelope[governance.AccessRequest](t, response)
	require.Equal(t, governance.StatusApproved, approved.Status)
	assert.Equal(t, int64(1), f.count(t, "iam_temporary_role_grants", "tenant_id = ? AND id = ?", iamTenantB, request.ID))
	return role, entity, approved
}

func (f *iamTenantFixture) assertSCIMCannotCrossTenants(t *testing.T, user provisioning.UserResource, group provisioning.GroupResource) {
	t.Helper()
	groupID, err := guid.Parse(group.ID)
	require.NoError(t, err)
	beforeName, beforeVersion := f.groupState(t, iamTenantB, groupID)

	f.rejectWithoutAudit(t, http.StatusNotFound, func() iamHTTPResponse {
		return f.request(t, http.MethodGet, "/scim/v2/Groups/"+group.ID, 0, f.scimTokenA, nil, nil)
	})
	f.rejectWithoutAudit(t, http.StatusNotFound, func() iamHTTPResponse {
		return f.request(t, http.MethodPut, "/scim/v2/Groups/"+group.ID, 0, f.scimTokenA, provisioning.GroupInput{
			Schemas: []string{provisioning.GroupSchema}, ExternalID: "hijacked", DisplayName: "Hijacked",
			Members: []provisioning.SCIMGroupMember{{Value: user.ID}},
		}, map[string]string{"If-Match": group.Meta.Version})
	})
	afterName, afterVersion := f.groupState(t, iamTenantB, groupID)
	assert.Equal(t, beforeName, afterName)
	assert.Equal(t, beforeVersion, afterVersion)
	assert.Zero(t, f.count(t, "iam_groups", "tenant_id = ? AND id = ?", iamTenantA, groupID))
}

func (f *iamTenantFixture) assertGovernanceRevokeCannotCrossTenants(t *testing.T, request governance.AccessRequest) {
	t.Helper()
	f.rejectWithoutAudit(t, http.StatusNotFound, func() iamHTTPResponse {
		return f.request(t, http.MethodPost, "/iam/access-requests/"+request.ID.String()+"/revoke", iamTenantA, f.tokenA, map[string]any{
			"reason": "cross-tenant attempt",
		}, nil)
	})
	var stored struct {
		Status    string     `gorm:"column:status"`
		RevokedAt *time.Time `gorm:"column:revoked_at"`
	}
	require.NoError(t, f.db.Table(f.table("iam_access_requests")).Select("status, revoked_at").Where("tenant_id = ? AND id = ?", iamTenantB, request.ID).Take(&stored).Error)
	assert.Equal(t, governance.StatusApproved, stored.Status)
	assert.Nil(t, stored.RevokedAt)
	assert.Equal(t, int64(1), f.count(t, "iam_temporary_role_grants", "tenant_id = ? AND id = ?", iamTenantB, request.ID))
	assert.Zero(t, f.count(t, "iam_temporary_role_grants", "tenant_id = ? AND id = ?", iamTenantA, request.ID))
}

func (f *iamTenantFixture) assertIAMAuthorizationCannotCrossTenants(t *testing.T, role core.APIRole, entity core.APIEntity) {
	t.Helper()
	response := f.request(t, http.MethodPost, "/iam/authorization/check", iamTenantB, f.tokenB, map[string]any{
		"entity_id": entity.ID, "permission_code": "entity_view", "subject": f.requesterB.ID.String(),
	}, nil)
	requireStatus(t, http.StatusOK, response)
	decision := decodeEnvelope[core.AuthorizationDecision](t, response)
	assert.True(t, decision.Allowed)

	f.rejectWithoutAudit(t, http.StatusNotFound, func() iamHTTPResponse {
		return f.request(t, http.MethodPost, "/iam/authorization/check", iamTenantA, f.tokenA, map[string]any{
			"entity_id": entity.ID, "permission_code": "entity_view", "subject": f.requesterB.ID.String(),
		}, nil)
	})

	entityID, err := guid.Parse(entity.ID)
	require.NoError(t, err)
	allowed, err := core.NewAuthorizer(f.app.runtime).CheckEntity(t.Context(), iamTenantB, entityID, "entity_view", f.adminA.ID)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Equal(t, int64(1), f.count(t, "iam_role_permissions", "tenant_id = ? AND role_id = ? AND permission_code = ?", iamTenantB, role.ID, "entity_view"))
}

func (f *iamTenantFixture) assertFederationCallbackCannotCrossTenants(t *testing.T) {
	t.Helper()
	var tokenCalls atomic.Int64
	var discoveryURL string
	discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/.well-known/openid-configuration"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": discoveryURL + "/authorize",
				"token_endpoint":         discoveryURL + "/token",
				"jwks_uri":               discoveryURL + "/jwks",
			})
		case request.URL.Path == "/token":
			tokenCalls.Add(1)
			http.Error(w, "unexpected token exchange", http.StatusInternalServerError)
		default:
			http.NotFound(w, request)
		}
	}))
	discoveryURL = discovery.URL
	t.Cleanup(discovery.Close)

	providerA := f.createFederationProvider(t, iamTenantA, f.tokenA, "Tenant A", discovery.URL+"/tenant-a")
	providerB := f.createFederationProvider(t, iamTenantB, f.tokenB, "Tenant B", discovery.URL+"/tenant-b")
	startPath := "/federation/" + providerB.ID.String() + "/start?return_url=" + url.QueryEscape("https://app.example.test/home")
	response := f.request(t, http.MethodGet, startPath, 0, "", nil, nil)
	require.Equal(t, http.StatusFound, response.StatusCode)
	location, err := url.Parse(response.Header.Get("Location"))
	require.NoError(t, err)
	state := location.Query().Get("state")
	require.NotEmpty(t, state)
	cookies := response.Cookies
	require.NotEmpty(t, cookies)

	beforeLinks := f.count(t, "iam_identity_links", "tenant_id = ?", iamTenantB)
	beforePrincipals := f.count(t, "iam_principals", "1 = 1")
	f.rejectWithoutAudit(t, http.StatusBadRequest, func() iamHTTPResponse {
		return f.request(t, http.MethodGet, "/federation/"+providerA.ID.String()+"/callback?code=unused&state="+url.QueryEscape(state), 0, "", nil, map[string]string{
			"Cookie": cookies[0].String(),
		})
	})
	assert.Zero(t, tokenCalls.Load())
	assert.Equal(t, beforeLinks, f.count(t, "iam_identity_links", "tenant_id = ?", iamTenantB))
	assert.Equal(t, beforePrincipals, f.count(t, "iam_principals", "1 = 1"))
}

func (f *iamTenantFixture) createFederationProvider(t *testing.T, tenantID guid.ID, token, name, issuer string) federation.Provider {
	t.Helper()
	response := f.request(t, http.MethodPost, "/iam/identity-providers", tenantID, token, map[string]any{
		"name": name, "provider_type": "oidc", "issuer": issuer, "client_id": "integration-client",
		"auto_provision": false, "status": "active",
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	return decodeEnvelope[federation.Provider](t, response)
}

func (f *iamTenantFixture) assertTenantBAuditTargetsNeverAppearInTenantA(t *testing.T, invitationID guid.ID, groupID string, requestID guid.ID, entityID string) {
	t.Helper()
	parsedGroupID, err := guid.Parse(groupID)
	require.NoError(t, err)
	parsedEntityID, err := guid.Parse(entityID)
	require.NoError(t, err)
	for _, targetID := range []guid.ID{invitationID, parsedGroupID, requestID, parsedEntityID} {
		assert.Zero(t, f.count(t, "iam_audit_events", "tenant_id = ? AND target_id = ?", iamTenantA, targetID), targetID.String())
		assert.NotZero(t, f.count(t, "iam_audit_events", "tenant_id = ? AND target_id = ?", iamTenantB, targetID), targetID.String())
	}
}

func (f *iamTenantFixture) request(t *testing.T, method, path string, tenantID guid.ID, token string, body any, headers map[string]string) iamHTTPResponse {
	t.Helper()
	var reader io.Reader
	form, isForm := body.(url.Values)
	if isForm {
		reader = strings.NewReader(form.Encode())
	} else if raw, ok := body.(io.Reader); ok {
		reader = raw
	} else if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, reader)
	require.NoError(t, err)
	if body != nil {
		contentType := "application/json"
		if isForm {
			contentType = "application/x-www-form-urlencoded"
		} else if strings.HasPrefix(path, "/scim/") {
			contentType = provisioning.SCIMContentType
		}
		request.Header.Set("Content-Type", contentType)
	}
	if !tenantID.Zero() {
		request.Header.Set(authz.TenantHeader, tenantID.String())
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := f.client.Do(request)
	require.NoError(t, err)
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	require.NoError(t, readErr)
	require.NoError(t, closeErr)
	return iamHTTPResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header.Clone(),
		Cookies:    response.Cookies(),
		Body:       responseBody,
	}
}

func (f *iamTenantFixture) rejectWithoutAudit(t *testing.T, status int, call func() iamHTTPResponse) {
	t.Helper()
	before := f.auditCounts(t)
	response := call()
	require.Equal(t, status, response.StatusCode, string(response.Body))
	assert.Equal(t, before, f.auditCounts(t))
}

func (f *iamTenantFixture) auditCounts(t *testing.T) [2]int64 {
	t.Helper()
	return [2]int64{
		f.count(t, "iam_audit_events", "tenant_id = ?", iamTenantA),
		f.count(t, "iam_audit_events", "tenant_id = ?", iamTenantB),
	}
}

func (f *iamTenantFixture) count(t *testing.T, table, where string, args ...any) int64 {
	t.Helper()
	var count int64
	require.NoError(t, f.db.Table(f.table(table)).Where(where, args...).Count(&count).Error)
	return count
}

func (f *iamTenantFixture) table(name string) string {
	return f.prefix + name
}

func (f *iamTenantFixture) groupState(t *testing.T, tenantID, groupID guid.ID) (string, int64) {
	t.Helper()
	var state struct {
		Name    string `gorm:"column:name"`
		Version int64  `gorm:"column:version"`
	}
	require.NoError(t, f.db.Table(f.table("iam_groups")).Select("name, version").Where("tenant_id = ? AND id = ?", tenantID, groupID).Take(&state).Error)
	return state.Name, state.Version
}

func decodeEnvelope[T any](t *testing.T, response iamHTTPResponse) T {
	t.Helper()
	var envelope iamResponseEnvelope
	require.NoError(t, json.Unmarshal(response.Body, &envelope))
	var value T
	require.NoError(t, json.Unmarshal(envelope.Data, &value))
	return value
}

func decodeBody[T any](t *testing.T, response iamHTTPResponse) T {
	t.Helper()
	var value T
	require.NoError(t, json.Unmarshal(response.Body, &value))
	return value
}

func requireStatus(t *testing.T, expected int, response iamHTTPResponse) {
	t.Helper()
	if response.StatusCode == expected {
		return
	}
	require.Equal(t, expected, response.StatusCode, string(response.Body))
}
