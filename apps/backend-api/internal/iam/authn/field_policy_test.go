package authn

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type profileCheckerFunc func(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.FieldConstraint, error)

func (f profileCheckerFunc) FieldConstraint(ctx context.Context, tenant, entity guid.ID, resource string, actor guid.ID) (authz.FieldConstraint, error) {
	return f(ctx, tenant, entity, resource, actor)
}

func allowProfileFields(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.FieldConstraint, error) {
	fields := authz.FieldConstraint{Fields: map[string]authz.FieldDecision{}}
	for _, field := range authz.ProfileFields() {
		fields.Fields[field.Name] = authz.DecideField(field, nil, nil)
	}
	return fields, nil
}

func TestPersonalAccountViewUsesTargetBrowserProfilePolicy(t *testing.T) {
	fields, err := allowProfileFields(t.Context(), 101, 0, authz.ProfileResource, 11)
	require.NoError(t, err)
	service := &Service{
		memberTenant: func(_ context.Context, principal, requested guid.ID) (guid.ID, error) {
			require.EqualValues(t, 11, principal)
			require.Zero(t, requested)
			return 101, nil
		},
		fields: profileCheckerFunc(func(_ context.Context, tenant, entity guid.ID, resource string, principal guid.ID) (authz.FieldConstraint, error) {
			require.EqualValues(t, 101, tenant)
			require.EqualValues(t, 11, principal)
			return fields, nil
		}),
	}
	claims := &authnext.Claims{PrincipalID: 11, Subject: "11", PreferredUsername: "alice", Email: "alice@example.test", EmailVerified: true}
	read := func(input *meInput, identity *authnext.Claims) meOutput {
		resolved, err := sessionTenant(t.Context(), service, input, identity)
		require.NoError(t, err)
		output, err := sessionView(t.Context(), service, input, resolved)
		require.NoError(t, err)
		return output
	}
	viewClaims := *claims
	viewClaims.AccountView = &authnext.AccountViewIdentity{ID: 50, ActorID: 7, ReadOnly: true, ScopeType: "personal", ScopeID: 11}
	for _, masked := range []bool{false, true} {
		if masked {
			fields.Fields["login_name"] = authz.FieldDecision{Read: "omit"}
			fields.Fields["email"] = authz.FieldDecision{Read: "redact"}
		}
		ordinary := read(&meInput{Cookie: "session=target"}, claims)
		view := read(&meInput{Authorization: "Bearer avt_view"}, &viewClaims)
		require.Equal(t, ordinary, view)
		if masked {
			require.Nil(t, view.PreferredUsername)
			require.Equal(t, "[REDACTED]", *view.Email)
		} else {
			require.Equal(t, "alice", *view.PreferredUsername)
			require.Equal(t, "alice@example.test", *view.Email)
		}
	}
}

func TestProfileProjectionScopesMaskingAndFailClosed(t *testing.T) {
	claims := &authnext.Claims{PrincipalID: 11, OrganizationID: "101", PreferredUsername: "alice", Email: "alice@example.test", EmailVerified: true}
	fields, err := allowProfileFields(t.Context(), 101, 0, authz.ProfileResource, 11)
	require.NoError(t, err)
	service := &Service{}
	WithFieldPermissionChecker(profileCheckerFunc(func(_ context.Context, tenant, entity guid.ID, resource string, actor guid.ID) (authz.FieldConstraint, error) {
		assert.Equal(t, guid.ID(101), tenant)
		assert.Zero(t, entity)
		assert.Equal(t, authz.ProfileResource, resource)
		assert.Equal(t, guid.ID(11), actor)
		return fields, nil
	}))(service)
	for _, scope := range []string{"openid", "openid profile", "openid email", "openid profile email", "profiles emails"} {
		t.Run(scope, func(t *testing.T) {
			view, err := service.ProjectProfile(t.Context(), claims, scope)
			require.NoError(t, err)
			assert.Equal(t, strings.Contains(" "+scope+" ", " profile "), view.PreferredUsername != nil)
			assert.Equal(t, strings.Contains(" "+scope+" ", " email "), view.Email != nil)
			assert.Equal(t, view.Email != nil, view.EmailVerified != nil)
		})
	}
	for _, mode := range []string{"omit", "redact", "unknown"} {
		fields.Fields["login_name"], fields.Fields["email"] = authz.FieldDecision{Read: mode}, authz.FieldDecision{Read: mode}
		view, err := service.ProjectProfile(t.Context(), claims, "profile email")
		require.NoError(t, err)
		assert.Nil(t, view.EmailVerified)
		if mode == "redact" {
			require.NotNil(t, view.Email)
			assert.Equal(t, "[REDACTED]", *view.Email)
			assert.Equal(t, "[REDACTED]", *view.PreferredUsername)
		} else {
			assert.Equal(t, ProfileClaims{}, view)
		}
	}
	assert.Equal(t, "alice@example.test", claims.Email)
	assert.True(t, claims.EmailVerified)
	delete(fields.Fields, "id")
	_, err = service.ProjectProfile(t.Context(), claims, "profile email")
	assert.ErrorIs(t, err, authz.ErrFieldDenied)
	service.fields = profileCheckerFunc(func(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.FieldConstraint, error) {
		return authz.FieldConstraint{}, assert.AnError
	})
	_, err = service.ProjectProfile(t.Context(), claims, "email")
	assert.ErrorIs(t, err, authz.ErrFieldsUnavailable)
	service.fields = nil
	_, err = service.ProjectProfile(t.Context(), claims, "email")
	assert.ErrorIs(t, err, authz.ErrFieldsUnavailable)
	_, err = (*Service)(nil).ProjectProfile(t.Context(), claims, "email")
	assert.ErrorIs(t, err, authz.ErrFieldsUnavailable)
	_, err = service.ProjectProfile(t.Context(), nil, "email")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	for _, tenant := range []string{"invalid", "0", "-1"} {
		claims.OrganizationID = tenant
		_, err = service.ProjectProfile(t.Context(), claims, "email")
		assert.ErrorIs(t, err, authz.ErrFieldDenied)
	}
	claims.OrganizationID, claims.TenantID = "101", 102
	_, err = service.ProjectProfile(t.Context(), claims, "email")
	assert.ErrorIs(t, err, authz.ErrFieldDenied)
	claims.OrganizationID = ""
	view, err := service.ProjectProfile(t.Context(), claims, "profile email")
	require.NoError(t, err)
	assert.Equal(t, ProfileClaims{}, view)
}

func TestSessionAndTokenProfileEnforcement(t *testing.T) {
	db, service, subject := newAuthenticationService(t)
	principalID := parseGUID(subject)
	fields, err := allowProfileFields(t.Context(), 101, 0, authz.ProfileResource, principalID)
	require.NoError(t, err)
	service.fields = profileCheckerFunc(func(_ context.Context, tenant, entity guid.ID, resource string, actor guid.ID) (authz.FieldConstraint, error) {
		assert.Equal(t, guid.ID(101), tenant)
		assert.Equal(t, principalID, actor)
		assert.Zero(t, entity)
		assert.Equal(t, authz.ProfileResource, resource)
		return fields, nil
	})
	require.NoError(t, db.Exec("INSERT INTO iam_tenants (id,status) VALUES (101,'active')").Error)
	require.NoError(t, db.Exec("INSERT INTO iam_tenant_members (tenant_id,principal_id,status) VALUES (101,?,'active')", subject).Error)
	_, api := humatest.New(t)
	RegisterHTTP(api, service, service)
	login := api.Post("/login", map[string]any{"login_name": "alice", "password": "correct horse battery staple"}, "Origin: https://app.example")
	require.Equal(t, http.StatusCreated, login.Code)
	cookie := strings.Split(login.Header().Get("Set-Cookie"), ";")[0]
	read := func(path, header string) map[string]any {
		t.Helper()
		response := api.Get(path, header)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		return body.Data
	}
	for _, path := range []string{"/me"} {
		view := read(path, "Cookie: "+cookie)
		assert.Equal(t, "alice", view["preferred_username"])
		assert.Equal(t, true, view["email_verified"])
	}
	access, _, err := service.IssueTenantAccessTokenWithAssurance(t.Context(), principalID, "101", "api", "openid profile email", authnext.Assurance{})
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", tokenPayload(t, access)["email"])
	fields.Fields["login_name"], fields.Fields["email"] = authz.FieldDecision{Read: "omit"}, authz.FieldDecision{Read: "redact"}
	for _, header := range []string{"Cookie: " + cookie, "Authorization: Bearer " + access} {
		for _, path := range []string{"/me"} {
			view := read(path, header)
			assert.NotContains(t, view, "preferred_username")
			assert.Equal(t, "[REDACTED]", view["email"])
			assert.NotContains(t, view, "email_verified")
		}
	}
	for _, scope := range []string{"openid", "openid profile", "openid email", "openid profile email"} {
		idToken, err := service.IssueTenantIDTokenWithAssurance(t.Context(), principalID, "101", "client", "nonce", scope, authnext.Assurance{})
		require.NoError(t, err)
		accessToken, _, err := service.IssueTenantAccessTokenWithAssurance(t.Context(), principalID, "101", "api", scope, authnext.Assurance{})
		require.NoError(t, err)
		for _, token := range []string{idToken, accessToken} {
			payload := tokenPayload(t, token)
			assert.NotContains(t, payload, "preferred_username")
			assert.NotContains(t, payload, "email_verified")
			if strings.Contains(scope, "email") {
				assert.Equal(t, "[REDACTED]", payload["email"])
			} else {
				assert.NotContains(t, payload, "email")
			}
		}
	}
	raw, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", raw.Email, "raw identity must not be masked")
	assert.True(t, raw.EmailVerified)
	fields, err = allowProfileFields(t.Context(), 101, 0, authz.ProfileResource, principalID)
	require.NoError(t, err)
	require.NoError(t, db.Exec("UPDATE iam_principals SET email = 'new@example.test', email_verified = false WHERE id = ?", principalID).Error)
	view := read("/me", "Authorization: Bearer "+access)
	assert.Equal(t, "new@example.test", view["email"], "bearer profile reads current identity")
	assert.Equal(t, false, view["email_verified"])
	delete(fields.Fields, "id")
	assert.Equal(t, http.StatusForbidden, api.Get("/me", "Cookie: "+cookie).Code)
	_, _, err = service.IssueTenantAccessTokenWithAssurance(t.Context(), principalID, "101", "api", "openid email", authnext.Assurance{})
	assert.ErrorIs(t, err, authz.ErrFieldDenied)
	_, err = service.IssueTenantIDTokenWithAssurance(t.Context(), principalID, "101", "client", "", "openid email", authnext.Assurance{})
	assert.ErrorIs(t, err, authz.ErrFieldDenied)
	service.fields = nil
	assert.Equal(t, http.StatusServiceUnavailable, api.Get("/me", "Cookie: "+cookie).Code)
	global, _, err := service.IssueAccessToken(t.Context(), principalID, "api", "openid profile email")
	require.NoError(t, err)
	assert.NotContains(t, tokenPayload(t, global), "email")
}
