package authz_test

import (
	"errors"
	"net/http"
	"testing"

	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
)

func TestPlatformInboxStreamUsesCanonicalRejectionKeys(t *testing.T) {
	for _, failure := range []string{"denied", "checker_error", "account_view", "allowed"} {
		t.Run(failure, func(t *testing.T) {
			_, api := humatest.New(t)
			verifier := validVerifier()
			checker := &fakeChecker{allowed: failure == "allowed"}
			var capability authz.PermissionChecker = checker
			status, key := http.StatusForbidden, respx.Forbidden
			switch failure {
			case "checker_error":
				checker.err = errors.New("private checker failure")
				status, key = http.StatusServiceUnavailable, respx.AuthorizationUnavailable
			case "account_view":
				verifier.claims.AccountView = &authn.AccountViewIdentity{ID: 91, ActorID: 41, ScopeType: "personal", ScopeID: 31, ReadOnly: true}
			case "allowed":
				status = http.StatusOK
			}
			r := authz.NewRegistrar(testRegistry(), verifier, capability, &fakeMembership{active: true})
			called := false
			authz.RegisterPrincipalPlatformInboxStream(r, api, huma.Operation{OperationID: "platform-inbox-stream", Method: "GET", Path: "/principals/{principal_id}/inbox/stream", Tags: []string{"streams"}}, func(ctx huma.Context) {
				called = true
				checked, err := r.RecheckPrincipalPlatformInboxStream(api, ctx)
				require.NoError(t, err)
				require.EqualValues(t, 31, authn.PrincipalIDFromContext(checked))
				checker.allowed = false
				_, err = r.RecheckPrincipalPlatformInboxStream(api, ctx)
				require.Error(t, err)
				ctx.SetStatus(http.StatusOK)
				_, err = ctx.BodyWriter().Write([]byte("stream-only"))
				require.NoError(t, err)
			})
			response := api.Get("/principals/31/inbox/stream", "Authorization: Bearer original")
			require.Equal(t, status, response.Code)
			require.Equal(t, failure == "allowed", called)
			if called {
				require.Equal(t, "stream-only", response.Body.String())
			} else {
				require.Contains(t, response.Body.String(), key)
				require.NotContains(t, response.Body.String(), "private checker failure")
			}
		})
	}
}

func TestTenantStreamUsesCompleteMemberGuardAndRechecksOriginalCredentials(t *testing.T) {
	for _, failure := range []string{"none", "token", "member", "oauth", "csrf"} {
		t.Run(failure, func(t *testing.T) {
			_, api := humatest.New(t)
			verifier := validVerifier()
			members := &fakeMembership{active: true}
			r := authz.NewRegistrar(testRegistry(), verifier, &fakeChecker{allowed: true}, members)
			called := false
			authz.RegisterTenantMemberStream(r, api, huma.Operation{OperationID: "stream", Method: "GET", Path: "/stream", Tags: []string{"streams"}}, func(ctx huma.Context) {
				called = true
				require.EqualValues(t, 11, authn.TenantIDFromContext(ctx.Context()))
				require.EqualValues(t, 31, authn.PrincipalIDFromContext(ctx.Context()))
				checked, err := r.RecheckTenantMemberStream(api, ctx)
				require.NoError(t, err)
				require.EqualValues(t, 31, authn.PrincipalIDFromContext(checked))
				members.active = false
				_, err = r.RecheckTenantMemberStream(api, ctx)
				require.Error(t, err)
				// A rejection during recheck must never write a second HTTP response.
				ctx.SetStatus(http.StatusOK)
				_, err = ctx.BodyWriter().Write([]byte("stream-only"))
				require.NoError(t, err)
			})
			switch failure {
			case "token":
				verifier.err = authn.ErrInvalidToken
			case "member":
				members.active = false
			case "oauth":
				verifier.claims.ClientID = "delegated"
			case "csrf":
				verifier.csrfErr = authn.ErrInvalidToken
			}
			response := api.Get("/tenants/11/stream", "Authorization: Bearer original", "Cookie: session=original")
			if failure == "none" {
				require.True(t, called)
				require.Equal(t, 200, response.Code)
				require.Equal(t, "stream-only", response.Body.String())
				require.Len(t, verifier.csrfCalls, 3)
			} else {
				require.False(t, called)
				if failure == "token" {
					require.Equal(t, 401, response.Code)
				} else {
					require.Equal(t, 403, response.Code)
				}
			}
		})
	}
}

func TestTenantStreamRejectsIncompleteRegistration(t *testing.T) {
	_, api := humatest.New(t)
	r := authz.NewRegistrar(testRegistry(), validVerifier(), &fakeChecker{}, &fakeMembership{})
	for _, run := range []func(){func() { authz.RegisterTenantMemberStream(nil, api, huma.Operation{}, func(huma.Context) {}) }, func() { authz.RegisterTenantMemberStream(r, api, huma.Operation{}, func(huma.Context) {}) }, func() {
		authz.RegisterTenantMemberStream(r, nil, huma.Operation{OperationID: "x"}, func(huma.Context) {})
	}, func() { authz.RegisterTenantMemberStream(r, api, huma.Operation{OperationID: "x"}, nil) }} {
		require.Panics(t, run)
	}
	_, err := (*authz.Registrar)(nil).RecheckTenantMemberStream(api, nil)
	require.Error(t, err)
}
