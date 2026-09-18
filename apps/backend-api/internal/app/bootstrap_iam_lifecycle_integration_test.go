package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	iamaudit "7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	iamLifecycleDatabaseType = "IAM_LIFECYCLE_DATABASE_TYPE"
	iamLifecycleDatabaseDSN  = "IAM_LIFECYCLE_DATABASE_DSN"
	iamLifecycleTablePrefix  = "IAM_LIFECYCLE_TABLE_PREFIX"
)

func TestIAMLifecycleExternalDatabase(t *testing.T) {
	databaseType := strings.TrimSpace(os.Getenv(iamLifecycleDatabaseType))
	dsn := strings.TrimSpace(os.Getenv(iamLifecycleDatabaseDSN))
	if databaseType == "" || dsn == "" {
		t.Skip("external IAM lifecycle database is not configured")
	}
	fixture := newIAMTenantFixtureWithDatabase(t, Database{
		Type: databaseType, Dsn: dsn, Writable: true, Readable: true,
		MaxOpenConns: 8, MaxIdleConns: 4,
	}, os.Getenv(iamLifecycleTablePrefix))
	fixture.runIAMLifecycle(t)
	fixture.runTodoDataPermissions(t)
	fixture.runSCIMTenantLifecycle(t)
	fixture.runSCIMActorLifecycle(t)
	fixture.runBrowserSessionTenant(t)
	fixture.runSCIMProfileLifecycle(t)
}

func (f *iamTenantFixture) runIAMLifecycle(t *testing.T) {
	var (
		tenant         organization.Tenant
		actor          identity.Principal
		actorToken     string
		requester      identity.Principal
		requesterToken string
		department     organization.Department
		group          organization.Group
		position       organization.Position
		role           core.APIRole
		entity         core.APIEntity
		governanceRole core.APIRole
	)

	require.True(t, t.Run("tenant", func(t *testing.T) {
		before := f.auditCount(t, 0)
		response := f.request(t, http.MethodPost, "/iam/tenants", 0, f.tokenA, map[string]any{
			"slug": "lifecycle-tenant", "name": "Lifecycle Tenant",
			"owner_principal_id": f.adminA.ID.String(),
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		tenant = decodeEnvelope[organization.Tenant](t, response)
		assert.Equal(t, int64(1), f.count(t, "iam_tenants", "id = ? AND slug = ? AND status = 'active'", tenant.ID, tenant.Slug))
		assert.Equal(t, int64(1), f.count(t, "iam_tenant_members", "tenant_id = ? AND principal_id = ? AND status = 'active'", tenant.ID, f.adminA.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_role_members", "tenant_id = ? AND principal_id = ?", tenant.ID, f.adminA.ID))
		assert.Positive(t, f.count(t, "iam_menus", "tenant_id = ?", tenant.ID))
		assert.Equal(t, before+1, f.auditCount(t, 0))
		f.assertAuditEvent(t, 0, "tenant_created", tenant.ID)
	}))

	require.True(t, t.Run("principal", func(t *testing.T) {
		identityModule := requireIAMModule[*identity.Module](t, f, identity.Name)
		actor = f.createPrincipal(t, identityModule, tenant.ID, "lifecycle-admin", "lifecycle-admin@example.test")
		requester = f.createPrincipal(t, identityModule, tenant.ID, "lifecycle-requester", "lifecycle-requester@example.test")
		assert.Equal(t, int64(1), f.count(t, "iam_principals", "id = ? AND status = 'active'", actor.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_principals", "id = ? AND status = 'active'", requester.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_credentials", "principal_id = ?", actor.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_tenant_members", "tenant_id = ? AND principal_id = ? AND status = 'active'", tenant.ID, actor.ID))
		f.assertAuditEvent(t, tenant.ID, "principal_created", actor.ID)
		f.assertAuditEvent(t, tenant.ID, "principal_created", requester.ID)

		require.NoError(t, f.db.Table(f.table("iam_platform_administrators")).Create(map[string]any{
			"principal_id": actor.ID, "created_at": time.Now().UTC().Truncate(time.Millisecond),
		}).Error)
		authnModule := requireIAMModule[*iamauthn.Module](t, f, iamauthn.Name)
		actorToken, _, _ = f.issueToken(t, authnModule, actor.ID, tenant.ID)
		requesterToken, _, _ = f.issueToken(t, authnModule, requester.ID, tenant.ID)
	}))

	require.True(t, t.Run("organization", func(t *testing.T) {
		response := f.request(t, http.MethodPost, "/iam/departments", tenant.ID, actorToken, map[string]any{
			"name": "Operations", "status": "active", "sort_order": 10,
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		department = decodeEnvelope[organization.Department](t, response)

		response = f.request(t, http.MethodPost, "/iam/groups", tenant.ID, actorToken, map[string]any{
			"name": "Operators", "type": "static", "description": "Lifecycle group", "status": "active",
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		group = decodeEnvelope[organization.Group](t, response)

		response = f.request(t, http.MethodPost, "/iam/positions", tenant.ID, actorToken, map[string]any{
			"code": "operator", "name": "Operator", "status": "active",
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		position = decodeEnvelope[organization.Position](t, response)

		response = f.request(t, http.MethodPut, "/iam/groups/"+group.ID.String()+"/members/"+actor.ID.String(), tenant.ID, actorToken, map[string]any{}, nil)
		requireStatus(t, http.StatusOK, response)
		response = f.request(t, http.MethodPut, "/iam/positions/"+position.ID.String()+"/members/"+actor.ID.String(), tenant.ID, actorToken, map[string]any{}, nil)
		requireStatus(t, http.StatusOK, response)

		assert.Equal(t, int64(1), f.count(t, "iam_departments", "tenant_id = ? AND id = ?", tenant.ID, department.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_department_closure", "tenant_id = ? AND ancestor_id = ? AND descendant_id = ? AND depth = 0", tenant.ID, department.ID, department.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_groups", "tenant_id = ? AND id = ?", tenant.ID, group.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_group_members", "tenant_id = ? AND group_id = ? AND principal_id = ?", tenant.ID, group.ID, actor.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_positions", "tenant_id = ? AND id = ?", tenant.ID, position.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_position_members", "tenant_id = ? AND position_id = ? AND principal_id = ?", tenant.ID, position.ID, actor.ID))
		for eventType, targetID := range map[string]guid.ID{
			"department_created": department.ID, "group_created": group.ID, "group_member_assigned": group.ID,
			"position_created": position.ID, "position_member_assigned": position.ID,
		} {
			f.assertAuditEvent(t, tenant.ID, eventType, targetID)
		}
	}))

	require.True(t, t.Run("role-permission-member", func(t *testing.T) {
		response := f.request(t, http.MethodPost, "/iam/roles", tenant.ID, actorToken, map[string]any{
			"name": "Entity viewers", "description": "Lifecycle role",
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		role = decodeEnvelope[core.APIRole](t, response)
		response = f.request(t, http.MethodPut, "/iam/roles/"+role.ID.String()+"/permissions/entity_view", tenant.ID, actorToken, nil, nil)
		requireStatus(t, http.StatusOK, response)
		response = f.request(t, http.MethodPut, "/iam/roles/"+role.ID.String()+"/members/"+actor.ID.String(), tenant.ID, actorToken, nil, nil)
		requireStatus(t, http.StatusOK, response)
		response = f.request(t, http.MethodPost, "/iam/entities", tenant.ID, actorToken, map[string]any{
			"type": "company", "name": "Lifecycle Company", "status": "active",
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		entity = decodeEnvelope[core.APIEntity](t, response)

		assert.Equal(t, int64(1), f.count(t, "iam_roles", "tenant_id = ? AND id = ?", tenant.ID, role.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_role_permissions", "tenant_id = ? AND role_id = ? AND permission_code = 'entity_view'", tenant.ID, role.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_role_members", "tenant_id = ? AND role_id = ? AND principal_id = ?", tenant.ID, role.ID, actor.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_entities", "tenant_id = ? AND id = ?", tenant.ID, entity.ID))
		roleID := requireLifecycleID(t, role.ID.String())
		entityID := requireLifecycleID(t, entity.ID.String())
		f.assertAuditEvent(t, tenant.ID, "role_created", roleID)
		f.assertAuditEvent(t, tenant.ID, "role_permission_granted", roleID)
		f.assertAuditEvent(t, tenant.ID, "role_member_added", roleID)
		f.assertAuditEvent(t, tenant.ID, "entity_created", entityID)
	}))

	require.True(t, t.Run("authorization", func(t *testing.T) {
		beforeAudit := f.auditCount(t, tenant.ID)
		beforeRevision := f.policyRevision(t, tenant.ID)
		request := map[string]any{"entity_id": entity.ID, "permission_code": "entity_view", "subject": actor.ID.String()}
		response := f.request(t, http.MethodPost, "/iam/authorization/check", tenant.ID, actorToken, request, nil)
		requireStatus(t, http.StatusOK, response)
		decision := decodeEnvelope[core.AuthorizationDecision](t, response)
		assert.True(t, decision.Allowed)
		response = f.request(t, http.MethodPost, "/iam/authorization/explain", tenant.ID, actorToken, request, nil)
		requireStatus(t, http.StatusOK, response)
		explanation := decodeEnvelope[authz.Explanation](t, response)
		assert.True(t, explanation.Allowed)
		assert.NotEmpty(t, explanation.Matches)
		assert.Equal(t, beforeAudit, f.auditCount(t, tenant.ID))
		assert.Equal(t, beforeRevision, f.policyRevision(t, tenant.ID))
	}))

	require.True(t, t.Run("oauth", func(t *testing.T) {
		response := f.request(t, http.MethodPost, "/iam/oauth-clients", tenant.ID, actorToken, map[string]any{
			"name": "Lifecycle automation", "grant_types": []string{"client_credentials"},
			"scopes": []string{"entity_view"}, "public_client": false,
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		created := decodeEnvelope[struct {
			Client       oauth.Client `json:"client"`
			ClientSecret string       `json:"client_secret"`
		}](t, response)
		require.NotEmpty(t, created.ClientSecret)
		assert.Equal(t, int64(1), f.count(t, "iam_oauth_clients", "tenant_id = ? AND id = ? AND status = 'active'", tenant.ID, created.Client.ID))
		f.assertAuditEvent(t, tenant.ID, "oauth_client_created", requireLifecycleID(t, created.Client.ID))

		beforeAudit := f.auditCount(t, tenant.ID)
		response = f.request(t, http.MethodPost, "/oauth/token", 0, "", url.Values{
			"grant_type": {"client_credentials"}, "client_id": {created.Client.ID},
			"client_secret": {created.ClientSecret}, "scope": {"entity_view"},
		}, nil)
		requireStatus(t, http.StatusOK, response)
		token := decodeBody[oauth.TokenResponse](t, response)
		require.NotEmpty(t, token.AccessToken)
		basic := base64.StdEncoding.EncodeToString([]byte(created.Client.ID + ":" + created.ClientSecret))
		response = f.request(t, http.MethodPost, "/oauth/introspect", 0, "", url.Values{
			"token": {token.AccessToken},
		}, map[string]string{"Authorization": "Basic " + basic})
		requireStatus(t, http.StatusOK, response)
		introspection := decodeBody[struct {
			Active bool `json:"active"`
		}](t, response)
		assert.True(t, introspection.Active)
		assert.Equal(t, beforeAudit, f.auditCount(t, tenant.ID))
	}))

	require.True(t, t.Run("scim", func(t *testing.T) {
		directory, scimToken := f.createSCIMCredential(t, tenant.ID, actorToken, "Lifecycle directory")
		credentialIDText, _, ok := strings.Cut(scimToken, ".")
		require.True(t, ok)
		credentialID := requireLifecycleID(t, credentialIDText)
		active := true
		response := f.request(t, http.MethodPost, "/scim/v2/Users", 0, scimToken, provisioning.UserInput{
			Schemas: []string{provisioning.UserSchema}, ExternalID: "lifecycle-user", UserName: "lifecycle-scim",
			DisplayName: "Lifecycle SCIM", Active: &active,
			Emails: []provisioning.UserEmail{{Value: "lifecycle-scim@example.test", Type: "work", Primary: true}},
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		user := decodeBody[provisioning.UserResource](t, response)
		response = f.request(t, http.MethodPost, "/scim/v2/Groups", 0, scimToken, provisioning.GroupInput{
			Schemas: []string{provisioning.GroupSchema}, ExternalID: "lifecycle-group", DisplayName: "Lifecycle SCIM Group",
			Members: []provisioning.SCIMGroupMember{{Value: user.ID}},
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		scimGroup := decodeBody[provisioning.GroupResource](t, response)
		userID := requireLifecycleID(t, user.ID)
		groupID := requireLifecycleID(t, scimGroup.ID)

		assert.Equal(t, int64(1), f.count(t, "iam_scim_directories", "tenant_id = ? AND id = ?", tenant.ID, directory.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_scim_credentials", "directory_id = ? AND id = ? AND revoked_at IS NULL", directory.ID, credentialID))
		assert.Equal(t, int64(2), f.count(t, "iam_scim_resources", "directory_id = ? AND deleted_at IS NULL", directory.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_principals", "id = ? AND status = 'active'", userID))
		assert.Equal(t, int64(1), f.count(t, "iam_groups", "tenant_id = ? AND id = ?", tenant.ID, groupID))
		assert.Equal(t, int64(1), f.count(t, "iam_group_members", "tenant_id = ? AND group_id = ? AND principal_id = ?", tenant.ID, groupID, userID))
		f.assertAuditEvent(t, tenant.ID, "scim_directory_created", directory.ID)
		f.assertAuditEvent(t, tenant.ID, "scim_credential_created", credentialID)
		f.assertAuditEvent(t, tenant.ID, "scim_user_created", userID)
		f.assertAuditEvent(t, tenant.ID, "scim_group_created", groupID)
	}))

	require.True(t, t.Run("federation", func(t *testing.T) {
		idp := newLifecycleOIDCProvider(t)
		response := f.request(t, http.MethodPost, "/iam/identity-providers", tenant.ID, actorToken, map[string]any{
			"name": "Lifecycle OIDC", "provider_type": "oidc", "issuer": idp.issuer,
			"client_id": idp.clientID, "client_secret": idp.clientSecret,
			"auto_provision": true, "status": "active",
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		provider := decodeEnvelope[federation.Provider](t, response)
		assert.Equal(t, int64(1), f.count(t, "iam_identity_providers", "tenant_id = ? AND id = ? AND auto_provision = ?", tenant.ID, provider.ID, true))

		response = f.request(t, http.MethodGet, "/federation/"+provider.ID.String()+"/start?return_url="+url.QueryEscape("https://app.example.test/home"), 0, "", nil, nil)
		requireStatus(t, http.StatusFound, response)
		stateCookie := requireCookie(t, response.Cookies, "federation_state")
		authorize, err := idp.server.Client().Get(response.Header.Get("Location"))
		require.NoError(t, err)
		defer authorize.Body.Close()
		require.Equal(t, http.StatusOK, authorize.StatusCode)
		var authorization struct {
			Code  string `json:"code"`
			State string `json:"state"`
		}
		require.NoError(t, json.NewDecoder(authorize.Body).Decode(&authorization))

		response = f.request(t, http.MethodGet, "/federation/"+provider.ID.String()+"/callback?code="+url.QueryEscape(authorization.Code)+"&state="+url.QueryEscape(authorization.State), 0, "", nil, map[string]string{
			"Cookie": stateCookie.Name + "=" + stateCookie.Value,
		})
		requireStatus(t, http.StatusFound, response)
		assert.Equal(t, "https://app.example.test/home", response.Header.Get("Location"))
		require.NotNil(t, requireCookie(t, response.Cookies, authnext.DefaultSessionCookieName))
		assert.Equal(t, int64(1), f.count(t, "iam_identity_links", "tenant_id = ? AND provider_id = ? AND external_subject = ?", tenant.ID, provider.ID, "lifecycle-external-user"))
		var federatedPrincipalID guid.ID
		require.NoError(t, f.db.Table(f.table("iam_identity_links")).Select("principal_id").
			Where("tenant_id = ? AND provider_id = ? AND external_subject = ?", tenant.ID, provider.ID, "lifecycle-external-user").
			Scan(&federatedPrincipalID).Error)
		require.False(t, federatedPrincipalID.Zero())
		assert.Equal(t, int64(1), f.count(t, "iam_principals", "id = ? AND email = ? AND status = 'active'", federatedPrincipalID, "lifecycle-federated@example.test"))
		assert.Equal(t, int64(1), f.count(t, "iam_tenant_members", "tenant_id = ? AND principal_id = ? AND status = 'active'", tenant.ID, federatedPrincipalID))
		assert.Equal(t, int64(1), f.count(t, "iam_sessions", "principal_id = ? AND revoked_at IS NULL", federatedPrincipalID))
		f.assertAuditEvent(t, tenant.ID, "federation_login", provider.ID)
	}))

	require.True(t, t.Run("governance", func(t *testing.T) {
		response := f.request(t, http.MethodPost, "/iam/roles", tenant.ID, actorToken, map[string]any{
			"name": "Temporary operators", "description": "Governance lifecycle role",
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		governanceRole = decodeEnvelope[core.APIRole](t, response)
		response = f.request(t, http.MethodPost, "/iam/access-requests", tenant.ID, requesterToken, map[string]any{
			"role_id": governanceRole.ID, "reason": "Lifecycle approval",
			"access_expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
		}, nil)
		requireStatus(t, http.StatusCreated, response)
		request := decodeEnvelope[governance.AccessRequest](t, response)
		response = f.request(t, http.MethodPost, "/iam/access-requests/"+request.ID.String()+"/approve", tenant.ID, actorToken, map[string]any{"note": "approved"}, nil)
		requireStatus(t, http.StatusOK, response)
		approved := decodeEnvelope[governance.AccessRequest](t, response)
		assert.Equal(t, governance.StatusApproved, approved.Status)
		assert.Equal(t, int64(1), f.count(t, "iam_access_requests", "tenant_id = ? AND id = ? AND status = ?", tenant.ID, request.ID, governance.StatusApproved))
		assert.Equal(t, int64(1), f.count(t, "iam_temporary_role_grants", "tenant_id = ? AND id = ? AND principal_id = ?", tenant.ID, request.ID, requester.ID))
		f.assertAuditEvent(t, tenant.ID, "access_request_created", request.ID)
		f.assertAuditEvent(t, tenant.ID, "access_request_approved", request.ID)
	}))

	require.True(t, t.Run("audit", func(t *testing.T) {
		before := f.auditCount(t, tenant.ID)
		response := f.request(t, http.MethodGet, "/iam/audit-integrity", tenant.ID, actorToken, nil, nil)
		requireStatus(t, http.StatusOK, response)
		integrity := decodeEnvelope[iamaudit.Integrity](t, response)
		assert.True(t, integrity.Valid)
		assert.Equal(t, before, integrity.VerifiedEvents)
		assert.Equal(t, before, integrity.HeadSequence)
		assert.NotEmpty(t, integrity.HeadHash)
		assert.Equal(t, before, f.auditCount(t, tenant.ID))
		assert.Equal(t, int64(1), f.count(t, "iam_audit_heads", "tenant_id = ? AND sequence = ?", tenant.ID, before))
	}))
}

func requireIAMModule[T any](t *testing.T, fixture *iamTenantFixture, name string) T {
	t.Helper()
	value, ok := fixture.app.modules.ByName(name)
	require.True(t, ok, name)
	module, ok := value.(T)
	require.True(t, ok, name)
	return module
}

func (f *iamTenantFixture) auditCount(t *testing.T, tenantID guid.ID) int64 {
	t.Helper()
	return f.count(t, "iam_audit_events", "tenant_id = ?", tenantID)
}

func (f *iamTenantFixture) assertAuditEvent(t *testing.T, tenantID guid.ID, eventType string, targetID guid.ID) {
	t.Helper()
	assert.Equal(t, int64(1), f.count(t, "iam_audit_events", "tenant_id = ? AND event_type = ? AND target_id = ?", tenantID, eventType, targetID), eventType)
}

func (f *iamTenantFixture) policyRevision(t *testing.T, tenantID guid.ID) int64 {
	t.Helper()
	var revision int64
	require.NoError(t, f.db.Table(f.table("iam_policy_revisions")).Select("revision").Where("tenant_id = ?", tenantID).Scan(&revision).Error)
	return revision
}

func requireCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	require.FailNow(t, "missing response cookie", name)
	return nil
}

func requireLifecycleID(t *testing.T, value string) guid.ID {
	t.Helper()
	id, err := guid.Parse(value)
	require.NoError(t, err)
	return id
}

type lifecycleOIDCProvider struct {
	server       *httptest.Server
	issuer       string
	clientID     string
	clientSecret string
	key          ed25519.PrivateKey
	mu           sync.Mutex
	challenge    string
}

func newLifecycleOIDCProvider(t *testing.T) *lifecycleOIDCProvider {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	provider := &lifecycleOIDCProvider{clientID: "lifecycle-client", clientSecret: "lifecycle-secret", key: key}
	provider.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/.well-known/openid-configuration"):
			issuer := provider.server.URL + strings.TrimSuffix(request.URL.Path, "/.well-known/openid-configuration")
			writeLifecycleJSON(writer, map[string]string{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
				"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/jwks",
			})
		case strings.HasSuffix(request.URL.Path, "/authorize"):
			provider.handleAuthorize(writer, request)
		case strings.HasSuffix(request.URL.Path, "/token"):
			provider.handleToken(t, writer, request)
		case strings.HasSuffix(request.URL.Path, "/jwks"):
			publicKey := provider.key.Public().(ed25519.PublicKey)
			writeLifecycleJSON(writer, map[string]any{"keys": []any{map[string]any{
				"kty": "OKP", "crv": "Ed25519", "kid": "lifecycle-key",
				"x": base64.RawURLEncoding.EncodeToString(publicKey), "alg": "EdDSA",
			}}})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(provider.server.Close)
	provider.issuer = provider.server.URL
	return provider
}

func (p *lifecycleOIDCProvider) handleAuthorize(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	if request.Method != http.MethodGet || query.Get("client_id") != p.clientID || query.Get("state") == "" ||
		query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		http.Error(writer, "invalid authorize request", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.challenge = query.Get("code_challenge")
	p.mu.Unlock()
	writeLifecycleJSON(writer, map[string]string{"code": "lifecycle-code", "state": query.Get("state")})
}

func (p *lifecycleOIDCProvider) handleToken(t *testing.T, writer http.ResponseWriter, request *http.Request) {
	require.NoError(t, request.ParseForm())
	clientID, secret, ok := request.BasicAuth()
	verifierHash := sha256.Sum256([]byte(request.FormValue("code_verifier")))
	p.mu.Lock()
	challenge := p.challenge
	p.challenge = ""
	p.mu.Unlock()
	if request.Method != http.MethodPost || !ok || clientID != p.clientID || secret != p.clientSecret ||
		request.FormValue("code") != "lifecycle-code" || challenge == "" ||
		base64.RawURLEncoding.EncodeToString(verifierHash[:]) != challenge {
		writeLifecycleJSON(writer, map[string]string{"error": "invalid_grant"})
		return
	}
	now := time.Now().UTC()
	token := signLifecycleJWT(t, p.key, map[string]any{
		"iss": p.issuer, "sub": "lifecycle-external-user", "aud": p.clientID,
		"exp": now.Add(time.Minute).Unix(), "iat": now.Unix(), "nbf": now.Add(-time.Second).Unix(),
		"auth_time": now.Add(-time.Minute).Unix(), "email": "lifecycle-federated@example.test",
		"email_verified": true, "name": "Lifecycle Federated", "preferred_username": "lifecycle-federated",
	})
	writeLifecycleJSON(writer, map[string]string{"id_token": token})
}

func signLifecycleJWT(t *testing.T, key ed25519.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "kid": "lifecycle-key", "typ": "JWT"})
	require.NoError(t, err)
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	encoded := base64.RawURLEncoding
	signingInput := encoded.EncodeToString(header) + "." + encoded.EncodeToString(payload)
	return signingInput + "." + encoded.EncodeToString(ed25519.Sign(key, []byte(signingInput)))
}

func writeLifecycleJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
