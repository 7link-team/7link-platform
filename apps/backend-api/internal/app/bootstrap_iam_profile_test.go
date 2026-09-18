package app

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposedSCIMProfileFieldPolicies(t *testing.T) {
	for _, prefix := range []string{"", "profile_"} {
		t.Run("prefix_"+prefix, func(t *testing.T) {
			f := newIAMTenantFixtureWithDatabase(t, Database{
				Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "scim-profile.sqlite"), Writable: true, Readable: true,
			}, prefix)
			f.runSCIMProfileLifecycle(t)
		})
	}
}

func (f *iamTenantFixture) runSCIMProfileLifecycle(t *testing.T) {
	t.Helper()
	directory, token := f.createSCIMCredential(t, iamTenantA, f.tokenA, "Profile policy")
	require.NotNil(t, directory.ServiceAccountID)
	response := f.request(t, http.MethodPost, "/scim/v2/Users", 0, token, map[string]any{
		"schemas": []string{provisioning.UserSchema}, "userName": "scim-profile-user", "displayName": "Private name",
		"emails": []map[string]any{{"value": "private@example.test", "primary": true}},
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	user := decodeBody[provisioning.UserResource](t, response)
	userID, err := guid.Parse(user.ID)
	require.NoError(t, err)
	userPath := "/scim/v2/Users/" + user.ID
	response = f.request(t, http.MethodPost, "/iam/roles", iamTenantA, f.tokenA,
		map[string]any{"name": "Restricted SCIM profile", "description": "Bound actor field policy"}, nil)
	requireStatus(t, http.StatusCreated, response)
	role := decodeEnvelope[core.APIRole](t, response)
	roleID, err := guid.Parse(role.ID.String())
	require.NoError(t, err)
	memberPath := "/iam/roles/" + role.ID.String() + "/members/" + directory.ServiceAccountID.String()
	response = f.request(t, http.MethodPut, memberPath, iamTenantA, f.tokenA, nil, nil)
	requireStatus(t, http.StatusOK, response)
	setPolicy := func(field, mask string) {
		t.Helper()
		policy := authz.FieldPolicy{
			Read:  authz.FieldAccess{Default: "allow", DenyRoles: []guid.ID{roleID}},
			Write: authz.FieldAccess{Default: "allow", DenyRoles: []guid.ID{roleID}}, Mask: mask,
		}
		response := f.request(t, http.MethodPut, "/iam/field-policies/user_profile/"+field, iamTenantA, f.tokenA,
			map[string]any{"version": 0, "policy": policy}, nil)
		requireStatus(t, http.StatusOK, response)
	}
	setPolicy("email", "redact")
	setPolicy("display_name", "omit")
	for _, path := range []string{userPath, "/scim/v2/Users"} {
		response = f.request(t, http.MethodGet, path, 0, token, nil, nil)
		requireStatus(t, http.StatusOK, response)
		view := decodeBody[map[string]any](t, response)
		if path != userPath {
			resources, ok := view["Resources"].([]any)
			require.True(t, ok)
			require.Len(t, resources, 1)
			view = resources[0].(map[string]any)
		}
		assert.NotContains(t, view, "displayName")
		assert.NotContains(t, view, "name")
		assert.Equal(t, "[REDACTED]", view["emails"].([]any)[0].(map[string]any)["value"])
	}
	for _, filter := range []string{`emails.value eq "private@example.test"`, `not (name.formatted pr)`, `userName pr or displayName pr`} {
		response = f.request(t, http.MethodGet, "/scim/v2/Users?filter="+url.QueryEscape(filter), 0, token, nil, nil)
		requireStatus(t, http.StatusForbidden, response)
	}
	patch := func(path string, value any) map[string]any {
		return map[string]any{"schemas": []string{provisioning.PatchSchema}, "Operations": []map[string]any{{"op": "replace", "path": path, "value": value}}}
	}
	beforeAudit := f.auditCount(t, iamTenantA)
	for _, body := range []map[string]any{
		patch("emails", nil), patch("name.formatted", "Changed"), patch("", map[string]any{"displayName": nil}),
	} {
		response = f.request(t, http.MethodPatch, userPath, 0, token, body, nil)
		requireStatus(t, http.StatusForbidden, response)
	}
	response = f.request(t, http.MethodPut, userPath, 0, token,
		map[string]any{"schemas": []string{provisioning.UserSchema}, "userName": user.UserName}, nil)
	requireStatus(t, http.StatusForbidden, response)
	response = f.request(t, http.MethodPost, "/scim/v2/Users", 0, token,
		map[string]any{"schemas": []string{provisioning.UserSchema}, "userName": "denied-profile-user", "emails": nil}, nil)
	requireStatus(t, http.StatusForbidden, response)
	response = f.request(t, http.MethodPost, "/scim/v2/Bulk", 0, token, map[string]any{
		"schemas": []string{provisioning.BulkSchema}, "Operations": []map[string]any{{
			"method": "PATCH", "path": "/Users/" + user.ID, "data": patch("emails", nil),
		}},
	}, nil)
	requireStatus(t, http.StatusOK, response)
	bulk := decodeBody[provisioning.BulkResponse](t, response)
	require.Len(t, bulk.Operations, 1)
	assert.Equal(t, "403", bulk.Operations[0].Status)
	assert.Equal(t, beforeAudit, f.auditCount(t, iamTenantA))
	response = f.request(t, http.MethodPatch, userPath, 0, token, patch("active", false), nil)
	requireStatus(t, http.StatusOK, response)
	identities, err := module.Resolve[identity.IdentityProvisioner](f.app.modules.Services())
	require.NoError(t, err)
	raw, err := identities.Get(t.Context(), iamTenantA, userID)
	require.NoError(t, err)
	assert.Equal(t, "private@example.test", raw.Email)
	assert.Equal(t, "Private name", raw.DisplayName, "PATCH preserves raw hidden fields")
	response = f.request(t, http.MethodGet, "/iam/principals/"+user.ID, iamTenantA, f.tokenA, nil, nil)
	requireStatus(t, http.StatusOK, response)
	assert.Equal(t, raw.Email, decodeEnvelope[map[string]any](t, response)["email"], "the administrator is not in the restricted role")

	var calls atomic.Int64
	remoteUsers := make(chan provisioning.UserResource, 4)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body provisioning.UserResource
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		remoteUsers <- body
		w.Header().Set("Content-Type", provisioning.SCIMContentType)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "remote-profile-user"})
	}))
	t.Cleanup(remote.Close)
	response = f.request(t, http.MethodPost, "/iam/scim/targets", iamTenantA, f.tokenA, map[string]any{
		"name": "Profile target", "base_url": remote.URL, "bearer_token": "profile-test", "service_account_id": directory.ServiceAccountID,
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	target := decodeEnvelope[provisioning.TargetSecret](t, response).Target
	pushPath := "/iam/scim/targets/" + target.ID + "/push"
	pushBody := map[string]any{"resource_type": "User", "resource_id": user.ID}
	response = f.request(t, http.MethodPost, pushPath, iamTenantA, f.tokenA, pushBody, nil)
	requireStatus(t, http.StatusOK, response)
	require.Equal(t, int64(1), calls.Load())
	projected := <-remoteUsers
	assert.Nil(t, projected.Name)
	assert.Empty(t, projected.DisplayName)
	require.Len(t, projected.Emails, 1)
	assert.Equal(t, "[REDACTED]", projected.Emails[0].Value, "outbound uses the bound actor, not the administrator")
	setPolicy("login_name", "omit")
	response = f.request(t, http.MethodGet, "/scim/v2/Users", 0, token, nil, nil)
	requireStatus(t, http.StatusForbidden, response)
	response = f.request(t, http.MethodGet, userPath, 0, token, nil, nil)
	requireStatus(t, http.StatusOK, response)
	assert.NotContains(t, decodeBody[map[string]any](t, response), "userName")
	response = f.request(t, http.MethodPost, pushPath, iamTenantA, f.tokenA, pushBody, nil)
	requireStatus(t, http.StatusForbidden, response)
	assert.Equal(t, int64(1), calls.Load(), "no remote write with an unreadable required identifier")
	response = f.request(t, http.MethodDelete, memberPath, iamTenantA, f.tokenA, nil, nil)
	requireStatus(t, http.StatusOK, response)
	response = f.request(t, http.MethodGet, userPath, 0, token, nil, nil)
	requireStatus(t, http.StatusOK, response)
	assert.Equal(t, "Private name", decodeBody[provisioning.UserResource](t, response).DisplayName, "role changes affect an existing credential immediately")
	response = f.request(t, http.MethodPost, pushPath, iamTenantA, f.tokenA, pushBody, nil)
	requireStatus(t, http.StatusOK, response)
	assert.Equal(t, int64(2), calls.Load())
	assert.Equal(t, "private@example.test", (<-remoteUsers).Emails[0].Value)
	response = f.request(t, http.MethodPut, "/iam/scim/directories/"+directory.ID.String(), iamTenantA, f.tokenA,
		map[string]any{"name": directory.Name, "status": "active", "version": directory.Version, "service_account_id": nil}, nil)
	requireStatus(t, http.StatusOK, response)
	response = f.request(t, http.MethodGet, userPath, 0, token, nil, nil)
	requireStatus(t, http.StatusUnauthorized, response)
}

func TestComposedIAMProfileFieldPolicies(t *testing.T) {
	for _, prefix := range []string{"", "profile_"} {
		t.Run("prefix_"+prefix, func(t *testing.T) {
			f := newIAMTenantFixtureWithDatabase(t, Database{
				Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "profile.sqlite"), Writable: true, Readable: true,
			}, prefix)
			web, err := module.Resolve[iamauthn.WebService](f.app.modules.Services())
			require.NoError(t, err)
			issue := func(scope string) string {
				t.Helper()
				token, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), f.requesterB.ID, iamTenantB.String(), "", scope, authnext.Assurance{})
				require.NoError(t, err)
				return token
			}
			token := issue("openid profile email")
			response := f.request(t, http.MethodGet, "/oauth/userinfo", 0, token, nil, nil)
			requireStatus(t, http.StatusOK, response)
			assert.Equal(t, f.requesterB.Email, decodeBody[map[string]any](t, response)["email"])
			response = f.request(t, http.MethodPost, "/authn/sessions", 0, "", map[string]any{
				"login_name": f.requesterB.LoginName, "password": "correct horse battery staple",
			}, map[string]string{"Origin": "https://app.example.test"})
			requireStatus(t, http.StatusOK, response)
			headers := map[string]string{"Cookie": response.Cookies[0].String()}
			set := func(field, mask string) core.ManagedFieldPolicy {
				t.Helper()
				policy := authz.FieldPolicy{Read: authz.FieldAccess{Default: "deny"}, Write: authz.FieldAccess{Default: "deny"}, Mask: mask}
				response := f.request(t, http.MethodPut, "/iam/field-policies/user_profile/"+field, iamTenantB, f.tokenB,
					map[string]any{"version": 0, "policy": policy}, nil)
				requireStatus(t, http.StatusOK, response)
				return decodeEnvelope[core.ManagedFieldPolicy](t, response)
			}
			emailPolicy := set("email", "redact")
			for _, path := range []string{"/authn/me", "/authn/sessions/current"} {
				response = f.request(t, http.MethodGet, path, 0, "", nil, headers)
				requireStatus(t, http.StatusOK, response)
				view := decodeEnvelope[map[string]any](t, response)
				assert.Equal(t, "[REDACTED]", view["email"])
				assert.NotContains(t, view, "email_verified")
			}
			response = f.request(t, http.MethodGet, "/oauth/userinfo", 0, token, nil, nil)
			requireStatus(t, http.StatusOK, response)
			view := decodeBody[map[string]any](t, response)
			assert.Equal(t, "[REDACTED]", view["email"], "an existing token must obey the current policy")
			assert.NotContains(t, view, "email_verified")
			for _, path := range []string{"/iam/principals/" + f.requesterB.ID.String(), "/iam/members/" + f.requesterB.ID.String()} {
				response = f.request(t, http.MethodGet, path, iamTenantB, f.tokenB, nil, nil)
				requireStatus(t, http.StatusOK, response)
				assert.Equal(t, "[REDACTED]", decodeEnvelope[map[string]any](t, response)["email"], "platform administrators do not bypass field denies")
				for _, email := range []any{"changed@example.test", nil} {
					before := f.auditCount(t, iamTenantB)
					response = f.request(t, http.MethodPatch, path, iamTenantB, f.tokenB, map[string]any{"email": email}, nil)
					requireStatus(t, http.StatusForbidden, response)
					assert.Equal(t, before, f.auditCount(t, iamTenantB))
				}
			}
			for _, path := range []string{"/iam/principals?search=requester", "/iam/members?search=requester"} {
				response = f.request(t, http.MethodGet, path, iamTenantB, f.tokenB, nil, nil)
				requireStatus(t, http.StatusForbidden, response)
			}
			identities, err := module.Resolve[identity.IdentityProvisioner](f.app.modules.Services())
			require.NoError(t, err)
			raw, err := identities.Get(t.Context(), iamTenantB, f.requesterB.ID)
			require.NoError(t, err)
			assert.Equal(t, f.requesterB.Email, raw.Email, "internal provisioning keeps raw values")
			response = f.request(t, http.MethodGet, "/iam/principals/"+f.adminA.ID.String(), iamTenantA, f.tokenA, nil, nil)
			requireStatus(t, http.StatusOK, response)
			assert.Equal(t, f.adminA.Email, decodeEnvelope[map[string]any](t, response)["email"])
			for _, scope := range []string{"openid", "openid profile", "openid email", "openid profile email"} {
				access := issue(scope)
				idToken, err := web.IssueTenantIDTokenWithAssurance(t.Context(), f.requesterB.ID, iamTenantB.String(), "client", "nonce", scope, authnext.Assurance{})
				require.NoError(t, err)
				for _, value := range []string{access, idToken} {
					payload, err := base64.RawURLEncoding.DecodeString(strings.Split(value, ".")[1])
					require.NoError(t, err)
					var claims map[string]any
					require.NoError(t, json.Unmarshal(payload, &claims))
					assert.NotContains(t, claims, "email_verified")
					if strings.Contains(scope, "email") {
						assert.Equal(t, "[REDACTED]", claims["email"])
					} else {
						assert.NotContains(t, claims, "email")
					}
					_, hasUsername := claims["preferred_username"]
					assert.Equal(t, strings.Contains(scope, "profile"), hasUsername)
				}
			}
			set("login_name", "omit")
			set("display_name", "omit")
			for _, path := range []string{"/iam/principals", "/iam/members"} {
				response = f.request(t, http.MethodGet, path, iamTenantB, f.tokenB, nil, nil)
				requireStatus(t, http.StatusForbidden, response)
			}
			response = f.request(t, http.MethodGet, "/oauth/userinfo", 0, token, nil, nil)
			requireStatus(t, http.StatusOK, response)
			assert.NotContains(t, decodeBody[map[string]any](t, response), "preferred_username")
			response = f.request(t, http.MethodDelete, "/iam/field-policies/user_profile/email?version="+strconv.FormatInt(emailPolicy.Version, 10), iamTenantB, f.tokenB, nil, nil)
			requireStatus(t, http.StatusOK, response)
			response = f.request(t, http.MethodGet, "/oauth/userinfo", 0, token, nil, nil)
			requireStatus(t, http.StatusOK, response)
			assert.Equal(t, f.requesterB.Email, decodeBody[map[string]any](t, response)["email"])
			require.NoError(t, f.db.Table(f.table("iam_tenant_members")).Where("tenant_id = ? AND principal_id = ?", iamTenantB, f.requesterB.ID).Update("status", "disabled").Error)
			response = f.request(t, http.MethodGet, "/oauth/userinfo", 0, token, nil, nil)
			requireStatus(t, http.StatusForbidden, response)
		})
	}
}
