package authz_test

import (
	"context"
	"net/http"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountViewScopeCannotFollowOtherTargetGrants(t *testing.T) {
	for _, mode := range []bool{true, false} {
		for _, tc := range []struct {
			name, scope, route string
			id                 guid.ID
			owner              authz.ResourceOwner
			allowed            bool
		}{
			{"tenant-own", "tenant", "tenant", 11, authz.ResourceOwner{}, true},
			{"tenant-other", "tenant", "tenant", 22, authz.ResourceOwner{}, false},
			{"tenant-child", "tenant", "entity", 11, authz.ResourceOwner{}, true},
			{"entity-own", "entity", "entity", 12, authz.ResourceOwner{}, true},
			{"entity-other", "entity", "entity", 13, authz.ResourceOwner{}, false},
			{"entity-parent-management", "entity", "tenant", 12, authz.ResourceOwner{}, false},
			{"tenant-system", "tenant", "system", 11, authz.ResourceOwner{}, false},
			{"system-tenant", "system", "tenant", 1, authz.ResourceOwner{}, false},
			{"platform-system", "platform", "system", 1, authz.ResourceOwner{}, false},
			{"personal-own", "personal", "personal", 9, authz.ResourceOwner{}, true},
			{"tenant-personal", "tenant", "personal", 11, authz.ResourceOwner{}, false},
			{"personal-wrong-subject", "personal", "personal", 8, authz.ResourceOwner{}, false},
			{"owned-tenant-own", "tenant", "owned", 11, authz.ResourceOwner{TenantID: 11}, true},
			{"owned-tenant-other", "tenant", "owned", 11, authz.ResourceOwner{TenantID: 22}, false},
			{"owned-entity-own", "entity", "owned", 12, authz.ResourceOwner{TenantID: 11, EntityID: 12}, true},
			{"owned-entity-other", "entity", "owned", 12, authz.ResourceOwner{TenantID: 11, EntityID: 13}, false},
			{"owned-personal-own", "personal", "owned", 9, authz.ResourceOwner{PrincipalID: 9}, true},
			{"owned-personal-other-scope", "tenant", "owned", 11, authz.ResourceOwner{PrincipalID: 9}, false},
			// Platform and tenant roots share one ID space, so the owner's kind decides.
			{"owned-platform-own", "platform", "owned", 11, authz.ResourceOwner{ScopeType: "platform", TenantID: 11}, true},
			{"owned-platform-other", "platform", "owned", 12, authz.ResourceOwner{ScopeType: "platform", TenantID: 11}, false},
			{"owned-platform-tenant-owner", "platform", "owned", 11, authz.ResourceOwner{TenantID: 11}, false},
			{"owned-tenant-platform-owner", "tenant", "owned", 11, authz.ResourceOwner{ScopeType: "platform", TenantID: 11}, false},
		} {
			t.Run(tc.name+map[bool]string{true: "/read", false: "/write"}[mode], func(t *testing.T) {
				_, api := humatest.New(t)
				checker := &operatingPlatformChecker{fakeChecker: fakeChecker{allowed: true}}
				claims := &authn.Claims{PrincipalID: 9, AccountView: &authn.AccountViewIdentity{
					ID: 50, ActorID: 7, ReadOnly: mode, ScopeType: tc.scope, ScopeID: tc.id,
				}}
				r := authz.NewRegistrar(testRegistry(), &fakeVerifier{claims: claims}, checker, &operatingPlatformMembers{fakeMembership{active: true}})
				called := false
				handler := func(context.Context, *struct{}) (*routeOutput, error) {
					called = true
					return &routeOutput{Body: "ok"}, nil
				}
				op := huma.Operation{OperationID: "scope-view", Method: http.MethodGet, Path: "/view-test", Tags: []string{"tenants"}}
				path := op.Path
				switch tc.route {
				case "tenant":
					authz.Register(r, api, op, authz.Guard{Resource: "store", Verb: "view"}, handler)
					path = "/tenants/11/view-test"
				case "entity":
					authz.RegisterEntity(r, api, op, authz.Guard{Resource: "workflow", Verb: "view"}, handler)
					path = "/tenants/11/entities/12/view-test"
				case "system":
					authz.RegisterPlatform(r, api, op, authz.Guard{Resource: "tenant", Verb: "view"}, handler)
				case "personal":
					op.Path = "/principals/{principal_id}/view-test"
					authz.RegisterPrincipalAuthenticated(r, api, op, handler)
					path = "/principals/9/view-test"
				case "owned":
					op.Path = "/files/{file_id}"
					resolve := func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) { return tc.owner, true, nil }
					if tc.owner.PrincipalID.Zero() {
						authz.RegisterOwned(r, api, op, "file_id", resolve, authz.Guard{Resource: "store", Verb: "view"}, handler)
					} else {
						authz.RegisterOwnedTenantMember(r, api, op, "file_id", resolve, handler)
					}
					path = "/files/99"
				}
				response := api.Get(path)
				if tc.allowed {
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				} else {
					require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
					assert.Empty(t, checker.tenantCalls)
					assert.Empty(t, checker.entityCalls)
					assert.Empty(t, checker.platformCalls)
					assert.Zero(t, checker.platformID)
				}
				assert.Equal(t, tc.allowed, called)
			})
		}
	}
}

func TestAccountViewReadOnlyEnforcedAcrossRouteScopes(t *testing.T) {
	for _, scope := range []string{"personal", "tenant", "entity", "platform"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			t.Run(scope+"/"+method, func(t *testing.T) {
				_, api := humatest.New(t)
				claims := &authn.Claims{
					PrincipalID: 9,
					AccountView: &authn.AccountViewIdentity{ID: 50, ActorID: 7, ReadOnly: true, ScopeType: scope, ScopeID: 9},
				}
				switch scope {
				case "tenant":
					claims.AccountView.ScopeID = 11
				case "entity":
					claims.AccountView.ScopeID = 12
				case "platform":
					claims.AccountView.ScopeType = "system"
					claims.AccountView.ScopeID = 1
				}
				checker := &fakeChecker{allowed: true}
				r := authz.NewRegistrar(testRegistry(), &fakeVerifier{claims: claims}, checker, &fakeMembership{active: true})
				called := false
				handler := func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
					called = true
					assert.EqualValues(t, 9, authn.PrincipalIDFromContext(ctx))
					assert.EqualValues(t, 7, authn.ActorIDFromContext(ctx))
					return &routeOutput{Body: "ok"}, nil
				}
				op := huma.Operation{OperationID: "view-operation", Method: method, Path: "/view-test", Tags: []string{"tenants"}}
				path := op.Path
				switch scope {
				case "personal":
					op.Path = "/principals/{principal_id}/view-test"
					path = "/principals/9/view-test"
					authz.RegisterPrincipalAuthenticated(r, api, op, handler)
				case "tenant":
					authz.Register(r, api, op, authz.Guard{Resource: "store", Verb: "view"}, handler)
					path = "/tenants/11/view-test"
				case "entity":
					authz.RegisterEntity(r, api, op, authz.Guard{Resource: "workflow", Verb: "view"}, handler)
					path = "/tenants/11/entities/12/view-test"
				case "platform":
					authz.RegisterPlatform(r, api, op, authz.Guard{Resource: "tenant", Verb: "view"}, handler)
				}
				response := api.Do(method, path)
				if method == http.MethodGet {
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					assert.True(t, called)
				} else {
					require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
					assert.False(t, called)
					assert.Empty(t, checker.tenantCalls)
					assert.Empty(t, checker.entityCalls)
					assert.Empty(t, checker.platformCalls)
				}
			})
		}
	}
}

func TestAccountViewEntitySelfDiscoveryUsesAddressedScope(t *testing.T) {
	for _, tc := range []struct {
		scope   string
		id      guid.ID
		allowed bool
	}{
		{"entity", 12, true}, {"entity", 13, false}, {"tenant", 11, true}, {"tenant", 22, false}, {"personal", 9, false}, {"system", 1, false},
	} {
		t.Run(tc.scope+tc.id.String(), func(t *testing.T) {
			_, api := humatest.New(t)
			claims := &authn.Claims{PrincipalID: 9, AccountView: &authn.AccountViewIdentity{ID: 50, ActorID: 7, ReadOnly: true, ScopeType: tc.scope, ScopeID: tc.id}}
			r := authz.NewAuthenticationOnlyRegistrar(&fakeVerifier{claims: claims})
			authz.RegisterPrincipalAuthenticated(r, api, huma.Operation{OperationID: "entity-discovery", Method: http.MethodGet, Path: "/tenants/{tenant_id}/entities/{entity_id}/principals/{principal_id}/permissions"}, okRoute)
			response := api.Get("/tenants/11/entities/12/principals/9/permissions")
			if tc.allowed {
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			} else {
				require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
			}
		})
	}
}

func TestAccountViewWriteModeDoesNotBypassTargetPermissions(t *testing.T) {
	_, api := humatest.New(t)
	checker := &fakeChecker{allowed: false}
	claims := &authn.Claims{PrincipalID: 9, AccountView: &authn.AccountViewIdentity{ID: 50, ActorID: 7, ScopeType: "system", ScopeID: 1}}
	r := authz.NewRegistrar(testRegistry(), &fakeVerifier{claims: claims}, checker, &fakeMembership{active: true})
	authz.RegisterPlatform(r, api, huma.Operation{OperationID: "write-view", Method: http.MethodPost, Path: "/view-test"}, authz.Guard{Resource: "tenant", Verb: "view"}, okRoute)
	require.Equal(t, http.StatusForbidden, api.Post("/view-test").Code)
	require.Len(t, checker.platformCalls, 1)
	assert.EqualValues(t, 9, checker.platformCalls[0].subject)
}

func TestAccountViewRejectsMissingServerIdentity(t *testing.T) {
	for _, view := range []*authn.AccountViewIdentity{
		{ID: 50}, {ActorID: 7},
		{ID: 50, ActorID: 7, ScopeType: "unknown", ScopeID: 9},
		{ID: 50, ActorID: 7, ScopeType: "personal", ScopeID: 0},
		{ID: -50, ActorID: 7, ScopeType: "personal", ScopeID: 9},
		{ID: 50, ActorID: -7, ScopeType: "personal", ScopeID: 9},
		{ID: 50, ActorID: 7, ScopeType: "personal", ScopeID: -9},
	} {
		_, api := humatest.New(t)
		r := authz.NewAuthenticationOnlyRegistrar(&fakeVerifier{claims: &authn.Claims{PrincipalID: 9, AccountView: view}})
		authz.RegisterPrincipalAuthenticated(r, api, huma.Operation{OperationID: "invalid-view", Method: http.MethodGet, Path: "/principals/{principal_id}/view-test"}, okRoute)
		assert.Equal(t, http.StatusUnauthorized, api.Get("/principals/9/view-test").Code)
	}
}

func TestAccountViewCannotEnterAccountHolderCallback(t *testing.T) {
	for _, mode := range []string{"normal", "read_only", "write"} {
		t.Run(mode, func(t *testing.T) {
			_, api := humatest.New(t)
			claims := &authn.Claims{PrincipalID: 9}
			if mode != "normal" {
				claims.AccountView = &authn.AccountViewIdentity{ID: 50, ActorID: 7, ReadOnly: mode == "read_only", ScopeType: "personal", ScopeID: 9}
			}
			r := authz.NewAuthenticationOnlyRegistrar(&fakeVerifier{claims: claims})
			op := huma.Operation{OperationID: "session-callback", Method: http.MethodGet, Path: "/session-callback"}
			authz.DenyAccountView(&op)
			called := false
			authz.RegisterSessionAuthenticated(r, api, op, func(context.Context, *struct{}) (*routeOutput, error) {
				called = true
				return &routeOutput{Body: "ok"}, nil
			})
			response := api.Get(op.Path, "Cookie: session=value")
			if mode == "normal" {
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				assert.True(t, called)
			} else {
				require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
				assert.False(t, called)
			}
		})
	}
}
