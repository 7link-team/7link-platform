package authz_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type routeOutput struct{ Body string }

type fakeVerifier struct {
	claims    *authn.Claims
	err       error
	csrfErr   error
	cookie    string
	csrfCalls []csrfValidationCall
	scopes    []authn.PersonalTokenScope
}

type csrfValidationCall struct {
	method        string
	origin        string
	cookie        string
	authorization string
}

func (verifier *fakeVerifier) Authenticate(ctx context.Context, _, _ string) (*authn.Claims, error) {
	if scope, ok := authn.PersonalTokenScopeFromContext(ctx); ok {
		verifier.scopes = append(verifier.scopes, scope)
	}
	if verifier.err != nil {
		return nil, verifier.err
	}
	return verifier.claims, nil
}

func (verifier *fakeVerifier) ValidateCSRF(method, origin, cookie, authorization string) error {
	verifier.csrfCalls = append(verifier.csrfCalls, csrfValidationCall{
		method: method, origin: origin, cookie: cookie, authorization: authorization,
	})
	return verifier.csrfErr
}

func (verifier *fakeVerifier) SessionCookieName() string { return verifier.cookie }

type fakeChecker struct {
	allowed       bool
	err           error
	tenantCalls   []tenantPermissionCall
	platformCalls []platformPermissionCall
	entityCalls   []entityPermissionCall
}

type tenantPermissionCall struct {
	tenantID   guid.ID
	permission string
	subject    guid.ID
}

type platformPermissionCall struct {
	permission string
	subject    guid.ID
}

type entityPermissionCall struct {
	tenantID   guid.ID
	entityID   guid.ID
	permission string
	subject    guid.ID
}

func (checker *fakeChecker) Check(_ context.Context, tenantID guid.ID, permission string, subject guid.ID) (bool, error) {
	checker.tenantCalls = append(checker.tenantCalls, tenantPermissionCall{tenantID: tenantID, permission: permission, subject: subject})
	return checker.allowed, checker.err
}

func (checker *fakeChecker) CheckPlatform(_ context.Context, permission string, subject guid.ID) (bool, error) {
	checker.platformCalls = append(checker.platformCalls, platformPermissionCall{permission: permission, subject: subject})
	return checker.allowed, checker.err
}

func (checker *fakeChecker) CheckEntity(_ context.Context, tenantID, entityID guid.ID, permission string, subject guid.ID) (bool, error) {
	checker.entityCalls = append(checker.entityCalls, entityPermissionCall{
		tenantID: tenantID, entityID: entityID, permission: permission, subject: subject,
	})
	return checker.allowed, checker.err
}

type tenantOnlyChecker struct{ allowed bool }

type dataChecker struct {
	fakeChecker
	constraint authz.DataConstraint
	dataCalls  []entityPermissionCall
}

func (checker *dataChecker) EntityConstraint(_ context.Context, tenantID, entityID guid.ID, permission string, subject guid.ID) (authz.DataConstraint, error) {
	checker.dataCalls = append(checker.dataCalls, entityPermissionCall{tenantID: tenantID, entityID: entityID, permission: permission, subject: subject})
	return checker.constraint, checker.err
}

func (checker tenantOnlyChecker) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return checker.allowed, nil
}

func (checker tenantOnlyChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return checker.allowed, nil
}

type fakeMembership struct {
	active bool
	err    error
}

func (membership *fakeMembership) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return membership.active, membership.err
}

func testRegistry() *authz.Registry {
	return authz.MustRegistry(
		authz.Action{Resource: "store", Verb: "view", Scope: "tenant"},
		authz.Action{Resource: "workflow", Verb: "view", Scope: "entity"},
		authz.Action{Resource: "tenant", Verb: "view", Scope: "platform"},
	)
}

func okRoute(context.Context, *struct{}) (*routeOutput, error) {
	return &routeOutput{Body: "ok"}, nil
}

func TestTenantRoutePublishesVerifiedBoundaryWithoutMutatingToken(t *testing.T) {
	for _, memberOnly := range []bool{false, true} {
		_, api := humatest.New(t)
		original := &authn.Claims{PrincipalID: 31, TenantID: 99, EntityID: 88}
		registrar := authz.NewRegistrar(testRegistry(), &fakeVerifier{claims: original}, &fakeChecker{allowed: true}, &fakeMembership{active: true})
		handler := func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
			claims, ok := authn.FromContext(ctx)
			require.True(t, ok)
			assert.Equal(t, guid.ID(11), claims.TenantID)
			assert.Zero(t, claims.EntityID)
			assert.Equal(t, guid.ID(31), claims.PrincipalID)
			assert.NotSame(t, original, claims)
			return &routeOutput{Body: "ok"}, nil
		}
		op := huma.Operation{OperationID: "tenant-context", Method: http.MethodGet, Path: "/tenant-context", Tags: []string{"tenants"}}
		if memberOnly {
			authz.RegisterTenantMember(registrar, api, op, handler)
		} else {
			authz.Register(registrar, api, op, authz.Guard{Resource: "store", Verb: "view"}, handler)
		}
		response := api.Get("/tenants/11/tenant-context")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, guid.ID(99), original.TenantID)
		assert.Equal(t, guid.ID(88), original.EntityID)
	}
}

func TestRegistrarDeclarationsCoverEveryRouteClass(t *testing.T) {
	registry := testRegistry()
	declarations := authz.NewDeclarationOnlyRegistrar(registry)
	assert.Same(t, registry, declarations.Registry())
	assert.True(t, declarations.IsDeclarationOnly())

	_, api := humatest.New(t)
	authz.Register(declarations, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "store-view", Method: http.MethodGet, Path: "/stores"}, authz.Guard{Resource: "store", Verb: "view"}, okRoute)
	authz.RegisterEntity(declarations, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "workflow-view", Method: http.MethodGet, Path: "/workflows"}, authz.Guard{Resource: "workflow", Verb: "view"}, okRoute)
	authz.RegisterOwned(declarations, api, huma.Operation{OperationID: "owned-store-view", Method: http.MethodGet, Path: "/stores/{store_id}", Tags: []string{"stores"}}, "store_id", func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, nil
	}, authz.Guard{Resource: "store", Verb: "view"}, okRoute)
	authz.RegisterTenantMember(declarations, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "member-view", Method: http.MethodGet, Path: "/member"}, okRoute)
	authz.RegisterAuthenticated(declarations, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "me-view", Method: http.MethodGet, Path: "/me"}, okRoute)
	authz.RegisterPlatform(declarations, api, huma.Operation{OperationID: "tenant-view", Method: http.MethodGet, Path: "/tenants"}, authz.Guard{Resource: "tenant", Verb: "view"}, okRoute)
	authz.RegisterPublic(api, huma.Operation{OperationID: "public", Method: http.MethodGet, Path: "/public"}, okRoute)
	authz.RegisterEntityAdapter(declarations, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "workflow-stream", Method: http.MethodGet, Path: "/workflow-stream"}, authz.Guard{Resource: "workflow", Verb: "view"}, func(ctx huma.Context) {
		ctx.SetStatus(http.StatusNoContent)
	})

	require.NoError(t, authz.ValidateOperations(api, registry))
	tenantStores := api.OpenAPI().Paths["/tenants/{tenant_id}/stores"].Get
	entityWorkflows := api.OpenAPI().Paths["/tenants/{tenant_id}/entities/{entity_id}/workflows"].Get
	entityStream := api.OpenAPI().Paths["/tenants/{tenant_id}/entities/{entity_id}/workflow-stream"].Get
	assert.Equal(t, "store_view", tenantStores.Extensions[authz.GuardExtensionKey])
	assert.True(t, authz.IsTenantMemberOperation(api.OpenAPI().Paths["/tenants/{tenant_id}/member"].Get))
	assert.True(t, authz.IsAuthenticatedOperation(api.OpenAPI().Paths["/me"].Get))
	assert.True(t, authz.IsPlatformOperation(api.OpenAPI().Paths["/tenants"].Get))
	assert.True(t, authz.IsPublicOperation(api.OpenAPI().Paths["/public"].Get))
	guard, guarded := authz.OperationGuard(tenantStores)
	assert.True(t, guarded)
	assert.Equal(t, "store_view", guard)
	assert.False(t, authz.IsTenantMemberOperation(nil))
	assert.False(t, authz.IsAuthenticatedOperation(nil))
	assert.False(t, authz.IsPlatformOperation(nil))
	assert.False(t, authz.IsPublicOperation(nil))
	_, guarded = authz.OperationGuard(nil)
	assert.False(t, guarded)
	assert.Len(t, authz.UserSecurity(), 2)
	assert.Len(t, authz.BearerSecurity(), 1)
	assert.Len(t, authz.ClientSecurity(), 1)
	assert.Equal(t, http.StatusNoContent, api.Get("/tenants/11/entities/21/workflow-stream").Code)
	assert.Equal(t, http.StatusNotFound, api.Get("/workflow-stream").Code)

	assertSelectorSchema(t, tenantStores, authz.TenantPath, "path")
	assertSelectorSchema(t, entityWorkflows, authz.TenantPath, "path")
	assertSelectorSchema(t, entityWorkflows, authz.EntityPath, "path")
	assertSelectorSchema(t, entityStream, authz.TenantPath, "path")
	assertSelectorSchema(t, entityStream, authz.EntityPath, "path")
}

func TestOwnedResourcePublishesResolvedBoundary(t *testing.T) {
	tests := []struct {
		name       string
		owner      authz.ResourceOwner
		wantTenant bool
	}{
		{name: "tenant resource", owner: authz.ResourceOwner{TenantID: 11}, wantTenant: true},
		{name: "entity resource", owner: authz.ResourceOwner{TenantID: 11, EntityID: 21}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, api := humatest.New(t)
			verifier := validVerifier()
			checker := &fakeChecker{allowed: true}
			registrar := authz.NewRegistrar(testRegistry(), verifier, checker, &fakeMembership{active: true})
			called := false
			authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "owned", Method: http.MethodGet, Path: "/stores/{store_id}", Tags: []string{"stores"}}, "store_id", func(_ context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
				assert.Equal(t, guid.ID(41), id)
				return test.owner, true, nil
			}, authz.Guard{Resource: map[bool]string{true: "store", false: "workflow"}[test.wantTenant], Verb: "view"}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
				called = true
				claims, ok := authn.FromContext(ctx)
				require.True(t, ok)
				assert.Equal(t, test.owner.TenantID, claims.TenantID)
				assert.Equal(t, test.owner.EntityID, claims.EntityID)
				return &routeOutput{Body: "ok"}, nil
			})
			response := api.Get("/stores/41")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.True(t, called)
			require.Len(t, verifier.scopes, 1)
			assert.Equal(t, guid.ID(11), verifier.scopes[0].TenantID)
			if test.wantTenant {
				require.Len(t, checker.tenantCalls, 1)
			} else {
				require.Len(t, checker.entityCalls, 1)
				assert.Equal(t, guid.ID(21), checker.entityCalls[0].entityID)
			}
		})
	}
}

func TestSystemOwnedResourceUsesGlobalPermissionAndVerifiedScope(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(fmt.Sprint(allowed), func(t *testing.T) {
			_, api := humatest.New(t)
			registry := testRegistry()
			require.NoError(t, registry.Register(authz.Action{Code: "system_store_view", Resource: "system_store", Verb: "view", Scope: "platform"}))
			checker := &fakeChecker{allowed: allowed}
			registrar := authz.NewRegistrar(registry, validVerifier(), checker, &fakeMembership{})
			called := false
			authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "system-owned", Method: http.MethodGet, Path: "/stores/{store_id}", Tags: []string{"stores"}}, "store_id", func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
				return authz.ResourceOwner{ScopeType: "system"}, true, nil
			}, authz.Guard{Resource: "store", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
				called = true
				require.Equal(t, "system", authz.ResolvedResourceOwner(ctx).ScopeType)
				claims, _ := authn.FromContext(ctx)
				require.Zero(t, claims.TenantID)
				require.Zero(t, claims.EntityID)
				return &routeOutput{Body: "ok"}, nil
			})
			response := api.Get("/stores/41")
			if allowed {
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			} else {
				require.Equal(t, http.StatusForbidden, response.Code)
			}
			require.Equal(t, allowed, called)
			require.Empty(t, checker.tenantCalls)
			require.Empty(t, checker.entityCalls)
		})
	}
}

func TestOwnedResourceFailurePathsDoNotLeakAcrossTenants(t *testing.T) {
	tests := []struct {
		name       string
		verifier   *fakeVerifier
		membership *fakeMembership
		checker    *fakeChecker
		found      bool
		resolveErr error
		want       int
	}{
		{name: "unauthenticated missing resource", verifier: &fakeVerifier{err: authn.ErrInvalidToken}, membership: &fakeMembership{active: true}, checker: &fakeChecker{allowed: true}, want: http.StatusUnauthorized},
		{name: "missing resource", verifier: validVerifier(), membership: &fakeMembership{active: true}, checker: &fakeChecker{allowed: true}, want: http.StatusNotFound},
		{name: "malformed resource id", verifier: validVerifier(), membership: &fakeMembership{active: true}, checker: &fakeChecker{allowed: true}, want: http.StatusUnprocessableEntity},
		{name: "different tenant member", verifier: validVerifier(), membership: &fakeMembership{}, checker: &fakeChecker{allowed: true}, found: true, want: http.StatusNotFound},
		{name: "permission denied", verifier: validVerifier(), membership: &fakeMembership{active: true}, checker: &fakeChecker{}, found: true, want: http.StatusForbidden},
		{name: "resolver unavailable", verifier: validVerifier(), membership: &fakeMembership{active: true}, checker: &fakeChecker{allowed: true}, resolveErr: assert.AnError, want: http.StatusServiceUnavailable},
		{name: "membership unavailable", verifier: validVerifier(), membership: &fakeMembership{err: assert.AnError}, checker: &fakeChecker{allowed: true}, found: true, want: http.StatusServiceUnavailable},
		{name: "authorization unavailable", verifier: validVerifier(), membership: &fakeMembership{active: true}, checker: &fakeChecker{err: assert.AnError}, found: true, want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, api := humatest.New(t)
			registrar := authz.NewRegistrar(testRegistry(), test.verifier, test.checker, test.membership)
			authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "owned", Method: http.MethodGet, Path: "/stores/{store_id}", Tags: []string{"stores"}}, "store_id", func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
				return authz.ResourceOwner{TenantID: 11}, test.found, test.resolveErr
			}, authz.Guard{Resource: "store", Verb: "view"}, okRoute)
			path := "/stores/41"
			if test.name == "malformed resource id" {
				path = "/stores/bad"
			}
			assert.Equal(t, test.want, api.Get(path).Code)
		})
	}
}

func TestRegistrarRejectsInvalidConstructionAndDeclarations(t *testing.T) {
	registry := testRegistry()
	verifier := &fakeVerifier{claims: &authn.Claims{PrincipalID: 31}}
	membership := &fakeMembership{active: true}
	checker := &fakeChecker{allowed: true}

	assert.Panics(t, func() { authz.NewRegistrar(nil, verifier, checker, membership) })
	assert.Panics(t, func() { authz.NewRegistrar(registry, nil, checker, membership) })
	assert.Panics(t, func() { authz.NewUnboundRegistrar(nil) })
	assert.Panics(t, func() { authz.NewDeclarationOnlyRegistrar(nil) })
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.Register[struct{}, routeOutput](nil, api, huma.Operation{Tags: []string{"tenants"}}, authz.Guard{}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.Register(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{Tags: []string{"tenants"}, Method: http.MethodGet, Path: "/missing-id"}, authz.Guard{Resource: "store", Verb: "view"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.Register(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{Tags: []string{"tenants"}, OperationID: "unknown", Method: http.MethodGet, Path: "/unknown"}, authz.Guard{Resource: "missing", Verb: "view"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		registrar := authz.NewRegistrar(registry, verifier, tenantOnlyChecker{allowed: true}, membership)
		authz.RegisterEntity(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "entity", Method: http.MethodGet, Path: "/entity"}, authz.Guard{Resource: "workflow", Verb: "view"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterPlatform(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{OperationID: "wrong-scope", Method: http.MethodGet, Path: "/wrong-scope"}, authz.Guard{Resource: "store", Verb: "view"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterTenantMember(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{Tags: []string{"tenants"}, Method: http.MethodGet, Path: "/member"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterAuthenticated(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{Method: http.MethodGet, Path: "/me"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterPlatform(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{Method: http.MethodGet, Path: "/system"}, authz.Guard{Resource: "tenant", Verb: "view"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterEntityAdapter(nil, api, huma.Operation{Tags: []string{"tenants"}}, authz.Guard{}, func(huma.Context) {})
	})
}

// TestRegistrationRefusesAnOperationWithoutOneResourceTag pins the contract
// that replaced the registrar writing the tag itself. While the registrar
// overwrote it, two routes shipped carrying a tag their author never got --
// the declaration read one way and the published document said another, and
// nothing failed. Requiring the caller to state exactly one tag turns that
// silent substitution into a startup panic, and keeps the Swagger group a
// property of the operation rather than of the URI the registrar builds.
func TestRegistrationRefusesAnOperationWithoutOneResourceTag(t *testing.T) {
	registry := testRegistry()
	verifier := validVerifier()
	guard := authz.Guard{Resource: "store", Verb: "view"}
	entityGuard := authz.Guard{Resource: "workflow", Verb: "view"}

	for name, register := range map[string]func(*authz.Registrar, huma.API, huma.Operation){
		"tenant": func(r *authz.Registrar, api huma.API, op huma.Operation) {
			authz.Register(r, api, op, guard, okRoute)
		},
		"entity": func(r *authz.Registrar, api huma.API, op huma.Operation) {
			authz.RegisterEntity(r, api, op, entityGuard, okRoute)
		},
		"tenant member": func(r *authz.Registrar, api huma.API, op huma.Operation) {
			authz.RegisterTenantMember(r, api, op, okRoute)
		},
		"entity adapter": func(r *authz.Registrar, api huma.API, op huma.Operation) {
			authz.RegisterEntityAdapter(r, api, op, entityGuard, func(huma.Context) {})
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Two tags are refused as well as none: a second group would publish
			// the same operation twice and leave no single owning section.
			for _, tags := range [][]string{nil, {}, {"tenants", "stores"}} {
				assert.Panics(t, func() {
					_, api := humatest.New(t)
					registrar := authz.NewRegistrar(registry, verifier, &fakeChecker{allowed: true}, &fakeMembership{active: true})
					register(registrar, api, huma.Operation{OperationID: "untagged", Method: http.MethodGet, Path: "/untagged", Tags: tags})
				}, "%v must not register", tags)
			}

			assert.NotPanics(t, func() {
				_, api := humatest.New(t)
				registrar := authz.NewRegistrar(registry, verifier, &fakeChecker{allowed: true}, &fakeMembership{active: true})
				register(registrar, api, huma.Operation{OperationID: "tagged", Method: http.MethodGet, Path: "/tagged", Tags: []string{"tenants"}})
			})
		})
	}
}

// TestTenantRegistrationRefusesAPlatformPermission is the mirror of the check
// RegisterPlatform has always made, and it exists because the missing half let
// three tenant routes ship guarded by platform_administer.
//
// The failure is quiet, which is why a startup panic is the right place to
// catch it. The tenant middleware looks the permission up in the caller's
// tenant grants, and a platform-scope action is never among them, so the route
// stops answering the tenant administrator it belongs to -- while the
// declaration still reads as though it were guarded, only more strictly.
func TestTenantRegistrationRefusesAPlatformPermission(t *testing.T) {
	registry := testRegistry()
	platform := authz.Guard{Resource: "tenant", Verb: "view"}
	tenant := authz.Guard{Resource: "store", Verb: "view"}

	operation := func(id string) huma.Operation {
		return huma.Operation{OperationID: id, Method: http.MethodGet, Path: "/" + id, Tags: []string{"tenants"}}
	}

	assert.PanicsWithValue(t, "tenant operation uses platform permission: tenant_view", func() {
		_, api := humatest.New(t)
		authz.Register(authz.NewDeclarationOnlyRegistrar(registry), api, operation("tenant-route"), platform, okRoute)
	}, "the message has to name the offending code, or the operator is left to find it by reading every route")

	assert.Panics(t, func() {
		_, api := humatest.New(t)
		registrar := authz.NewRegistrar(registry, &fakeVerifier{}, &fakeChecker{}, &fakeMembership{})
		authz.RegisterEntity(registrar, api, operation("entity-route"), platform, okRoute)
	}, "an entity route authorizes inside a tenant too, so it inherits the same rule")

	assert.Panics(t, func() {
		_, api := humatest.New(t)
		registrar := authz.NewRegistrar(registry, &fakeVerifier{}, &fakeChecker{}, &fakeMembership{})
		authz.RegisterEntityAdapter(registrar, api, operation("adapter-route"), platform, func(huma.Context) {})
	}, "the raw-transport variant may not be the way around the rule")

	// The check may only reject the scope mismatch. A tenant action on a tenant
	// route is the ordinary case and has to keep registering.
	assert.NotPanics(t, func() {
		_, api := humatest.New(t)
		authz.Register(authz.NewDeclarationOnlyRegistrar(registry), api, operation("store-route"), tenant, okRoute)
	})
}

func TestUnboundRegistrarBindsVerifierExactlyOnce(t *testing.T) {
	registrar := authz.NewUnboundRegistrar(testRegistry())
	verifier := validVerifier()
	checker := &fakeChecker{allowed: true}
	membership := &fakeMembership{active: true}

	assert.False(t, registrar.IsDeclarationOnly())
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterAuthenticated(registrar, api, huma.Operation{
			OperationID: "unbound", Method: http.MethodGet, Path: "/unbound",
		}, okRoute)
	})
	assert.ErrorContains(t, (*authz.Registrar)(nil).Bind(verifier, checker, membership), "registrar is nil")
	assert.ErrorContains(t, registrar.Bind(nil, checker, membership), "are required")
	require.NoError(t, registrar.Bind(verifier, checker, membership))
	assert.ErrorContains(t, registrar.Bind(verifier, checker, membership), "already bound")
}

func TestTenantAuthorizationMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		verifier   *fakeVerifier
		checker    *fakeChecker
		membership *fakeMembership
		tenant     string
		want       int
	}{
		{name: "allowed", verifier: validVerifier(), checker: &fakeChecker{allowed: true}, membership: &fakeMembership{active: true}, tenant: "11", want: http.StatusOK},
		{name: "unauthenticated", verifier: &fakeVerifier{err: authn.ErrInvalidToken}, checker: &fakeChecker{allowed: true}, membership: &fakeMembership{active: true}, tenant: "11", want: http.StatusUnauthorized},
		{name: "auth unavailable", verifier: &fakeVerifier{err: authn.ErrUnavailable}, checker: &fakeChecker{allowed: true}, membership: &fakeMembership{active: true}, tenant: "11", want: http.StatusServiceUnavailable},
		{name: "csrf", verifier: &fakeVerifier{claims: validClaims(), csrfErr: errors.New("bad csrf")}, checker: &fakeChecker{allowed: true}, membership: &fakeMembership{active: true}, tenant: "11", want: http.StatusForbidden},
		{name: "bad tenant", verifier: validVerifier(), checker: &fakeChecker{allowed: true}, membership: &fakeMembership{active: true}, tenant: "bad", want: http.StatusForbidden},
		{name: "inactive", verifier: validVerifier(), checker: &fakeChecker{allowed: true}, membership: &fakeMembership{}, tenant: "11", want: http.StatusForbidden},
		{name: "membership unavailable", verifier: validVerifier(), checker: &fakeChecker{allowed: true}, membership: &fakeMembership{err: assert.AnError}, tenant: "11", want: http.StatusServiceUnavailable},
		{name: "denied", verifier: validVerifier(), checker: &fakeChecker{}, membership: &fakeMembership{active: true}, tenant: "11", want: http.StatusForbidden},
		{name: "authorization unavailable", verifier: validVerifier(), checker: &fakeChecker{err: assert.AnError}, membership: &fakeMembership{active: true}, tenant: "11", want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, api := humatest.New(t)
			registrar := authz.NewRegistrar(testRegistry(), test.verifier, test.checker, test.membership)
			assert.False(t, registrar.IsDeclarationOnly())
			authz.Register(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "store-view", Method: http.MethodGet, Path: "/stores"}, authz.Guard{Resource: "store", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
				claims, ok := authn.FromContext(ctx)
				require.True(t, ok)
				assert.Equal(t, guid.ID(31), claims.PrincipalID)
				return &routeOutput{Body: "ok"}, nil
			})
			response := api.Get("/tenants/" + test.tenant + "/stores")
			assert.Equal(t, test.want, response.Code)
		})
	}
}

func TestTenantMemberAndAuthenticatedMiddleware(t *testing.T) {
	_, api := humatest.New(t)
	registrar := authz.NewRegistrar(testRegistry(), validVerifier(), &fakeChecker{allowed: true}, &fakeMembership{active: true})
	authz.RegisterTenantMember(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "member", Method: http.MethodGet, Path: "/member"}, okRoute)
	authz.RegisterAuthenticated(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "me", Method: http.MethodGet, Path: "/me"}, okRoute)
	assert.Equal(t, http.StatusOK, api.Get("/tenants/11/member").Code)
	assert.Equal(t, http.StatusOK, api.Get("/me").Code)

	_, rejected := humatest.New(t)
	bad := authz.NewRegistrar(testRegistry(), &fakeVerifier{err: authn.ErrInvalidToken}, &fakeChecker{allowed: true}, &fakeMembership{active: true})
	authz.RegisterAuthenticated(bad, rejected, huma.Operation{OperationID: "me", Method: http.MethodGet, Path: "/me"}, okRoute)
	assert.Equal(t, http.StatusUnauthorized, rejected.Get("/me").Code)

	_, unavailableAPI := humatest.New(t)
	unavailable := authz.NewRegistrar(testRegistry(), &fakeVerifier{err: authn.ErrUnavailable}, &fakeChecker{allowed: true}, &fakeMembership{active: true})
	authz.RegisterAuthenticated(unavailable, unavailableAPI, huma.Operation{OperationID: "me", Method: http.MethodGet, Path: "/me"}, okRoute)
	assert.Equal(t, http.StatusServiceUnavailable, unavailableAPI.Get("/me").Code)

	_, csrfAPI := humatest.New(t)
	csrfVerifier := &fakeVerifier{claims: validClaims(), csrfErr: assert.AnError, cookie: "session"}
	csrf := authz.NewRegistrar(testRegistry(), csrfVerifier, &fakeChecker{allowed: true}, &fakeMembership{active: true})
	authz.RegisterAuthenticated(csrf, csrfAPI, huma.Operation{OperationID: "profile-update", Method: http.MethodPost, Path: "/profile"}, okRoute)
	assert.Equal(t, http.StatusForbidden, csrfAPI.Post("/profile",
		"Origin: https://evil.example",
		"Cookie: session=value",
		"Authorization: Bearer token",
	).Code)
	assert.Equal(t, []csrfValidationCall{{
		method: http.MethodPost, origin: "https://evil.example", cookie: "session=value", authorization: "Bearer token",
	}}, csrfVerifier.csrfCalls)
}

func TestPermissionCheckerReceivesExactScope(t *testing.T) {
	checker := &fakeChecker{allowed: true}
	_, api := humatest.New(t)
	registrar := authz.NewRegistrar(testRegistry(), validVerifier(), checker, &fakeMembership{active: true})
	authz.Register(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "store", Method: http.MethodGet, Path: "/store"}, authz.Guard{Resource: "store", Verb: "view"}, okRoute)
	authz.RegisterEntity(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "workflow", Method: http.MethodGet, Path: "/workflow"}, authz.Guard{Resource: "workflow", Verb: "view"}, okRoute)
	authz.RegisterPlatform(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "tenants", Method: http.MethodGet, Path: "/tenants"}, authz.Guard{Resource: "tenant", Verb: "view"}, okRoute)

	assert.Equal(t, http.StatusOK, api.Get("/tenants/11/store").Code)
	assert.Equal(t, http.StatusOK, api.Get("/tenants/11/entities/21/workflow").Code)
	assert.Equal(t, http.StatusOK, api.Get("/tenants").Code)
	assert.Equal(t, []tenantPermissionCall{{tenantID: 11, permission: "store_view", subject: 31}}, checker.tenantCalls)
	assert.Equal(t, []entityPermissionCall{{tenantID: 11, entityID: 21, permission: "workflow_view", subject: 31}}, checker.entityCalls)
	assert.Equal(t, []platformPermissionCall{{permission: "tenant_view", subject: 31}}, checker.platformCalls)
}

func TestOpenAPISecurityMatchesAuthenticatorCapabilities(t *testing.T) {
	bareVerifier, err := authn.NewVerifier(authn.Config{Enabled: true, Issuer: "https://issuer.example"})
	require.NoError(t, err)
	_, bearerAPI := humatest.New(t)
	bearerRegistrar := authz.NewRegistrar(testRegistry(), bareVerifier, &fakeChecker{allowed: true}, &fakeMembership{active: true})
	authz.RegisterAuthenticated(bearerRegistrar, bearerAPI, huma.Operation{OperationID: "bearer-only", Method: http.MethodGet, Path: "/bearer"}, okRoute)
	assert.Equal(t, []map[string][]string{{authz.BearerScheme: {}}}, bearerAPI.OpenAPI().Paths["/bearer"].Get.Security)

	_, sessionAPI := humatest.New(t)
	sessionRegistrar := authz.NewRegistrar(testRegistry(), validVerifier(), &fakeChecker{allowed: true}, &fakeMembership{active: true})
	authz.RegisterAuthenticated(sessionRegistrar, sessionAPI, huma.Operation{OperationID: "browser", Method: http.MethodGet, Path: "/browser"}, okRoute)
	assert.Equal(t, []map[string][]string{{authz.BearerScheme: {}}, {authz.SessionScheme: {}}}, sessionAPI.OpenAPI().Paths["/browser"].Get.Security)
	assert.Equal(t, "session", sessionAPI.OpenAPI().Components.SecuritySchemes[authz.SessionScheme].Name)
}

// TestRegistrarPublishesTheCookieItActuallyReads is the regression that closes
// the gap between the request path and the API document. A public operation
// cannot resolve the configured cookie name on its own, so a document written
// only from public registrations used to advertise the compiled-in default
// while the request path read the configured name, and anybody integrating
// from the document sent a cookie the server ignores.
func TestRegistrarPublishesTheCookieItActuallyReads(t *testing.T) {
	configured := authz.NewRegistrar(
		testRegistry(), &fakeVerifier{claims: validClaims(), cookie: "configured_session"},
		&fakeChecker{allowed: true}, &fakeMembership{active: true},
	)
	assert.Equal(t, "configured_session", configured.SessionCookieName())

	_, api := humatest.New(t)
	authz.EnsureSecuritySchemes(api, configured.SessionCookieName())
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "session-reading-public", Method: http.MethodGet, Path: "/authorize",
		Security: []map[string][]string{{authz.SessionScheme: {}}},
	}, okRoute)
	assert.Equal(t, "configured_session", api.OpenAPI().Components.SecuritySchemes[authz.SessionScheme].Name)
}

// TestRegistrarFallsBackToTheSharedDefault covers the only case where the
// default is the honest answer: nothing is bound that runs browser sessions.
// It reads the constant rather than a literal so the fallback cannot drift away
// from the configuration default again.
func TestRegistrarFallsBackToTheSharedDefault(t *testing.T) {
	bare, err := authn.NewVerifier(authn.Config{Enabled: true, Issuer: "https://issuer.example"})
	require.NoError(t, err)
	registrar := authz.NewRegistrar(testRegistry(), bare, &fakeChecker{allowed: true}, &fakeMembership{active: true})
	assert.Equal(t, authn.DefaultSessionCookieName, registrar.SessionCookieName())

	empty := authz.NewRegistrar(
		testRegistry(), &fakeVerifier{claims: validClaims()},
		&fakeChecker{allowed: true}, &fakeMembership{active: true},
	)
	assert.Equal(t, authn.DefaultSessionCookieName, empty.SessionCookieName())
}

func TestPublicRegistrationPreservesConfiguredSessionCookieScheme(t *testing.T) {
	_, api := humatest.New(t)
	authz.EnsureSecuritySchemes(api, "configured_session")
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "public", Method: http.MethodGet, Path: "/public",
	}, okRoute)
	assert.Equal(t, "configured_session", api.OpenAPI().Components.SecuritySchemes[authz.SessionScheme].Name)
}

func TestAuthenticationOnlyRegistrarUsesVerifierContract(t *testing.T) {
	assert.Panics(t, func() { authz.NewAuthenticationOnlyRegistrar(nil) })
	_, api := humatest.New(t)
	registrar := authz.NewAuthenticationOnlyRegistrar(validVerifier())
	authz.RegisterAuthenticated(registrar, api, huma.Operation{
		OperationID: "profile", Method: http.MethodGet, Path: "/profile",
	}, okRoute)
	assert.Equal(t, http.StatusOK, api.Get("/profile", "Cookie: session=value").Code)
	assert.Equal(t, "session", api.OpenAPI().Components.SecuritySchemes[authz.SessionScheme].Name)
}

func TestExplicitPrincipalMountChecksAuthenticatedOwnerBeforeReadOrWrite(t *testing.T) {
	_, api := humatest.New(t)
	verifier := validVerifier()
	registrar := authz.NewAuthenticationOnlyRegistrar(verifier)
	called := 0
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		authz.RegisterPrincipalAuthenticated(registrar, api, huma.Operation{
			OperationID: "self-" + method, Method: method, Path: "/principals/{principal_id}/profile",
		}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
			claims, ok := authn.FromContext(ctx)
			require.True(t, ok)
			require.Equal(t, guid.ID(31), claims.PrincipalID)
			called++
			return &routeOutput{Body: "ok"}, nil
		})
		for _, item := range []struct {
			id     string
			status int
		}{
			{"31", 200}, {"32", 404}, {"0", 422}, {"031", 422}, {"bad", 422}, {"9999999999999999999", 422},
		} {
			response := api.Do(method, "/principals/"+item.id+"/profile", "Cookie: session=value")
			require.Equal(t, item.status, response.Code, response.Body.String())
		}
		verifier.err = authn.ErrInvalidToken
		require.Equal(t, 401, api.Do(method, "/principals/31/profile").Code)
		verifier.err = nil
	}
	require.Equal(t, 2, called, "foreign and malformed selectors never reach the handler")
	assertSelectorSchema(t, api.OpenAPI().Paths["/principals/{principal_id}/profile"].Get, "principal_id", "path")
}

func TestPrincipalReaderWithoutSelfDeclarationRetainsItsAuthorizationBoundary(t *testing.T) {
	_, api := humatest.New(t)
	registrar := authz.NewAuthenticationOnlyRegistrar(validVerifier())
	type input struct {
		ID string `path:"principal_id"`
	}
	authz.RegisterAuthenticated(registrar, api, huma.Operation{
		OperationID: "authorized-reader", Method: http.MethodGet, Path: "/principals/{principal_id}/avatar/content",
	}, func(ctx context.Context, in *input) (*routeOutput, error) {
		claims, _ := authn.FromContext(ctx)
		require.Equal(t, guid.ID(31), claims.PrincipalID)
		require.Equal(t, "32", in.ID)
		return &routeOutput{Body: "ok"}, nil
	})
	require.Equal(t, 200, api.Get("/principals/32/avatar/content", "Cookie: session=value").Code)
}

func TestEntityAndPlatformAuthorizationMiddleware(t *testing.T) {
	t.Run("entity", func(t *testing.T) {
		_, api := humatest.New(t)
		registrar := authz.NewRegistrar(testRegistry(), validVerifier(), &fakeChecker{allowed: true}, &fakeMembership{active: true})
		authz.RegisterEntity(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "workflow", Method: http.MethodGet, Path: "/workflow"}, authz.Guard{Resource: "workflow", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
			claims, ok := authn.FromContext(ctx)
			require.True(t, ok)
			assert.Equal(t, guid.ID(11), claims.TenantID)
			assert.Equal(t, guid.ID(21), claims.EntityID)
			return &routeOutput{Body: "ok"}, nil
		})
		assert.Equal(t, http.StatusOK, api.Get("/tenants/11/entities/21/workflow").Code)
		assert.Equal(t, http.StatusForbidden, api.Get("/tenants/bad/entities/21/workflow").Code)
	})

	t.Run("entity subject fallback and adapter", func(t *testing.T) {
		verifier := &fakeVerifier{claims: &authn.Claims{Subject: "31"}}
		_, api := humatest.New(t)
		registrar := authz.NewRegistrar(testRegistry(), verifier, &fakeChecker{allowed: true}, &fakeMembership{active: true})
		authz.RegisterEntityAdapter(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "stream", Method: http.MethodGet, Path: "/stream"}, authz.Guard{Resource: "workflow", Verb: "view"}, func(ctx huma.Context) {
			ctx.SetStatus(http.StatusNoContent)
		})
		assert.Equal(t, http.StatusNoContent, api.Get("/tenants/11/entities/21/stream").Code)
	})

	t.Run("platform", func(t *testing.T) {
		_, api := humatest.New(t)
		registrar := authz.NewRegistrar(testRegistry(), validVerifier(), &fakeChecker{allowed: true}, &fakeMembership{active: true})
		authz.RegisterPlatform(registrar, api, huma.Operation{OperationID: "tenants", Method: http.MethodGet, Path: "/tenants"}, authz.Guard{Resource: "tenant", Verb: "view"}, okRoute)
		assert.Equal(t, http.StatusOK, api.Get("/tenants").Code)

		_, deniedAPI := humatest.New(t)
		denied := authz.NewRegistrar(testRegistry(), validVerifier(), &fakeChecker{}, &fakeMembership{active: true})
		authz.RegisterPlatform(denied, deniedAPI, huma.Operation{OperationID: "tenants", Method: http.MethodGet, Path: "/tenants"}, authz.Guard{Resource: "tenant", Verb: "view"}, okRoute)
		assert.Equal(t, http.StatusForbidden, deniedAPI.Get("/tenants").Code)
	})
}

func TestEntityOrAuthenticatedAuthorizationKeepsContextsDistinct(t *testing.T) {
	verifier := validVerifier()
	checker := &dataChecker{constraint: authz.DataConstraint{AllowAll: true}}
	_, api := humatest.New(t)
	registry := authz.MustRegistry(authz.Action{Resource: "file", Verb: "view", Scope: "tenant", DataScoped: true})
	registrar := authz.NewRegistrar(registry, verifier, checker, &fakeMembership{active: true})
	seen := make([]authn.Claims, 0, 2)
	authz.RegisterEntityOrAuthenticated(registrar, api, huma.Operation{
		Tags:        []string{"tenants"},
		OperationID: "contextual-files", Method: http.MethodGet, Path: "/contextual-files",
	}, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
		claims, ok := authn.FromContext(ctx)
		require.True(t, ok)
		seen = append(seen, *claims)
		return &routeOutput{Body: "ok"}, nil
	})

	require.Equal(t, http.StatusOK, api.Get("/principals/"+"31"+"/contextual-files").Code)
	require.Equal(t, guid.ID(31), seen[0].PrincipalID)
	require.Zero(t, seen[0].TenantID)
	require.Zero(t, seen[0].EntityID)
	require.Empty(t, checker.dataCalls)

	require.Equal(t, http.StatusOK, api.Get("/tenants/11/entities/21/contextual-files").Code)
	require.Equal(t, guid.ID(11), seen[1].TenantID)
	require.Equal(t, guid.ID(21), seen[1].EntityID)
	require.Equal(t, []entityPermissionCall{{tenantID: 11, entityID: 21, permission: "file_view", subject: 31}}, checker.dataCalls)

	require.Equal(t, http.StatusNotFound, api.Get("/contextual-files").Code)
	require.Len(t, seen, 2)

	operation := api.OpenAPI().Paths["/tenants/{tenant_id}/entities/{entity_id}/contextual-files"].Get
	require.Equal(t, "file_view", operation.Extensions[authz.GuardExtensionKey])
	for _, name := range []string{authz.TenantPath, authz.EntityPath} {
		var found *huma.Param
		for _, parameter := range operation.Parameters {
			if parameter.Name == name && parameter.In == "path" {
				found = parameter
			}
		}
		require.NotNil(t, found)
		require.True(t, found.Required)
	}
}

func TestEntityAuthorizationFailurePaths(t *testing.T) {
	tests := []struct {
		name       string
		verifier   *fakeVerifier
		checker    *fakeChecker
		membership *fakeMembership
		tenant     string
		entity     string
		want       int
	}{
		{name: "unauthenticated", verifier: &fakeVerifier{err: authn.ErrInvalidToken}, checker: &fakeChecker{allowed: true}, membership: &fakeMembership{active: true}, tenant: "11", entity: "21", want: http.StatusUnauthorized},
		{name: "auth unavailable", verifier: &fakeVerifier{err: authn.ErrUnavailable}, checker: &fakeChecker{allowed: true}, membership: &fakeMembership{active: true}, tenant: "11", entity: "21", want: http.StatusServiceUnavailable},
		{name: "csrf", verifier: &fakeVerifier{claims: validClaims(), csrfErr: assert.AnError}, checker: &fakeChecker{allowed: true}, membership: &fakeMembership{active: true}, tenant: "11", entity: "21", want: http.StatusForbidden},
		{name: "bad entity", verifier: validVerifier(), checker: &fakeChecker{allowed: true}, membership: &fakeMembership{active: true}, tenant: "11", entity: "bad", want: http.StatusForbidden},
		{name: "bad fallback subject", verifier: &fakeVerifier{claims: &authn.Claims{Subject: "bad"}}, checker: &fakeChecker{allowed: true}, membership: &fakeMembership{active: true}, tenant: "11", entity: "21", want: http.StatusForbidden},
		{name: "membership unavailable", verifier: validVerifier(), checker: &fakeChecker{allowed: true}, membership: &fakeMembership{err: assert.AnError}, tenant: "11", entity: "21", want: http.StatusServiceUnavailable},
		{name: "inactive", verifier: validVerifier(), checker: &fakeChecker{allowed: true}, membership: &fakeMembership{}, tenant: "11", entity: "21", want: http.StatusForbidden},
		{name: "authorization unavailable", verifier: validVerifier(), checker: &fakeChecker{err: assert.AnError}, membership: &fakeMembership{active: true}, tenant: "11", entity: "21", want: http.StatusServiceUnavailable},
		{name: "denied", verifier: validVerifier(), checker: &fakeChecker{}, membership: &fakeMembership{active: true}, tenant: "11", entity: "21", want: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, api := humatest.New(t)
			registrar := authz.NewRegistrar(testRegistry(), test.verifier, test.checker, test.membership)
			authz.RegisterEntity(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "entity", Method: http.MethodGet, Path: "/entity"}, authz.Guard{Resource: "workflow", Verb: "view"}, okRoute)
			response := api.Get("/tenants/" + test.tenant + "/entities/" + test.entity + "/entity")
			assert.Equal(t, test.want, response.Code)
		})
	}
}

func TestDataFilteredEntityAuthorization(t *testing.T) {
	registry := authz.MustRegistry(authz.Action{Resource: "task", Verb: "view", Scope: "tenant", DataScoped: true})
	for _, test := range []struct {
		name       string
		constraint authz.DataConstraint
		err        error
		want       int
	}{
		{name: "all", constraint: authz.DataConstraint{AllowAll: true}, want: http.StatusOK},
		{name: "entity", constraint: authz.DataConstraint{ResourceIDs: []string{"21"}}, want: http.StatusOK},
		{name: "owner", constraint: authz.DataConstraint{OwnerIDs: []string{"31"}}, want: http.StatusOK},
		{name: "department", constraint: authz.DataConstraint{DepartmentIDs: []string{"41"}}, want: http.StatusOK},
		{name: "no scope", want: http.StatusForbidden},
		{name: "other entity", constraint: authz.DataConstraint{ResourceIDs: []string{"22"}}, want: http.StatusForbidden},
		{name: "ancestor is not a grant", constraint: authz.DataConstraint{Ancestors: []authz.ResourceRef{{Type: "entity", ID: "21"}}}, want: http.StatusForbidden},
		{name: "deny all scopes", constraint: authz.DataConstraint{AllowAll: true, ResourceIDs: []string{"21"}, OwnerIDs: []string{"31"}, DepartmentIDs: []string{"41"}, DeniedIDs: []string{"21"}}, want: http.StatusForbidden},
		{name: "unavailable even with a grant", constraint: authz.DataConstraint{AllowAll: true}, err: assert.AnError, want: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, api := humatest.New(t)
			checker := &dataChecker{constraint: test.constraint, fakeChecker: fakeChecker{err: test.err}}
			verifier := validVerifier()
			verifier.claims.TenantID, verifier.claims.EntityID = 999, 888
			registrar := authz.NewRegistrar(registry, verifier, checker, &fakeMembership{active: true})
			calls := 0
			authz.RegisterEntity(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "tasks", Method: http.MethodGet, Path: "/tasks"}, authz.Guard{Resource: "task", Verb: "view", FilterData: true}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
				calls++
				claims, ok := authn.FromContext(ctx)
				require.True(t, ok)
				assert.Equal(t, guid.ID(11), claims.TenantID)
				assert.Equal(t, guid.ID(21), claims.EntityID)
				assert.Equal(t, guid.ID(31), claims.PrincipalID)
				return &routeOutput{Body: "ok"}, nil
			})
			assert.Equal(t, test.want, api.Get("/tenants/11/entities/21/tasks").Code)
			assert.Equal(t, []entityPermissionCall{{tenantID: 11, entityID: 21, permission: "task_view", subject: 31}}, checker.dataCalls)
			assert.Empty(t, checker.entityCalls, "row-scoped authorization must not require a whole-entity grant")
			assert.Empty(t, checker.tenantCalls)
			operation := api.OpenAPI().Paths["/tenants/{tenant_id}/entities/{entity_id}/tasks"].Get
			assert.Equal(t, true, operation.Extensions[authz.DataFilterExtensionKey])
			assertSelectorSchema(t, operation, authz.EntityPath, "path")
			if test.want != http.StatusOK {
				assert.Zero(t, calls)
			} else {
				assert.Equal(t, 1, calls)
			}
			assert.Equal(t, guid.ID(999), verifier.claims.TenantID, "request selectors must not mutate cached session claims")

			authz.RegisterEntity(registrar, api, huma.Operation{Tags: []string{"tenants"}, OperationID: "whole", Method: http.MethodGet, Path: "/whole"}, authz.Guard{Resource: "task", Verb: "view"}, okRoute)
			wantWhole := http.StatusForbidden
			if test.err != nil {
				wantWhole = http.StatusServiceUnavailable
			}
			assert.Equal(t, wantWhole, api.Get("/tenants/11/entities/21/whole").Code)
			assert.Len(t, checker.entityCalls, 1, "ordinary entity routes retain whole-entity semantics")
			assert.Len(t, checker.dataCalls, 1)
		})
	}
	assert.False(t, (authz.DataConstraint{AllowAll: true}).AllowsEntityRows(0))
}

func TestDataFilteredRegistrationRejectsUnsafeDeclarations(t *testing.T) {
	registry := authz.MustRegistry(
		authz.Action{Resource: "task", Verb: "view", Scope: "tenant", DataScoped: true},
		authz.Action{Resource: "role", Verb: "view", Scope: "tenant"},
	)
	guard := authz.Guard{Resource: "task", Verb: "view", FilterData: true}
	for _, name := range []string{"platform", "adapter", "non-data", "no checker capability"} {
		t.Run(name, func(t *testing.T) {
			_, api := humatest.New(t)
			registrar := authz.NewDeclarationOnlyRegistrar(registry)
			op := huma.Operation{OperationID: "tasks", Method: http.MethodGet, Path: "/tasks", Tags: []string{"tenants"}}
			assert.Panics(t, func() {
				switch name {
				case "platform":
					authz.RegisterPlatform(registrar, api, op, guard, okRoute)
				case "adapter":
					authz.RegisterEntityAdapter(registrar, api, op, guard, func(huma.Context) {})
				case "non-data":
					authz.RegisterEntity(registrar, api, op, authz.Guard{Resource: "role", Verb: "view", FilterData: true}, okRoute)
				case "no checker capability":
					registrar = authz.NewRegistrar(registry, validVerifier(), &fakeChecker{allowed: true}, &fakeMembership{active: true})
					authz.RegisterEntity(registrar, api, op, guard, okRoute)
				}
			})
		})
	}
	_, api := humatest.New(t)
	authz.Register(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{Tags: []string{"tenants"}, OperationID: "tenant-tasks", Method: http.MethodGet, Path: "/tenant-tasks"}, guard, okRoute)
	assert.Equal(t, true, api.OpenAPI().Paths["/tenants/{tenant_id}/tenant-tasks"].Get.Extensions[authz.DataFilterExtensionKey])
	authz.RegisterEntity(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{Tags: []string{"tenants"}, OperationID: "tasks", Method: http.MethodGet, Path: "/tasks"}, guard, okRoute)
	assert.Equal(t, true, api.OpenAPI().Paths["/tenants/{tenant_id}/entities/{entity_id}/tasks"].Get.Extensions[authz.DataFilterExtensionKey])
}

func TestPlatformAuthorizationFailurePaths(t *testing.T) {
	tests := []struct {
		name     string
		verifier *fakeVerifier
		checker  *fakeChecker
		want     int
	}{
		{name: "unauthenticated", verifier: &fakeVerifier{err: authn.ErrInvalidToken}, checker: &fakeChecker{allowed: true}, want: http.StatusUnauthorized},
		{name: "auth unavailable", verifier: &fakeVerifier{err: authn.ErrUnavailable}, checker: &fakeChecker{allowed: true}, want: http.StatusServiceUnavailable},
		{name: "csrf", verifier: &fakeVerifier{claims: validClaims(), csrfErr: assert.AnError}, checker: &fakeChecker{allowed: true}, want: http.StatusForbidden},
		{name: "authorization unavailable", verifier: validVerifier(), checker: &fakeChecker{err: assert.AnError}, want: http.StatusServiceUnavailable},
		{name: "denied", verifier: validVerifier(), checker: &fakeChecker{}, want: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, api := humatest.New(t)
			registrar := authz.NewRegistrar(testRegistry(), test.verifier, test.checker, &fakeMembership{active: true})
			authz.RegisterPlatform(registrar, api, huma.Operation{OperationID: "platform", Method: http.MethodGet, Path: "/system"}, authz.Guard{Resource: "tenant", Verb: "view"}, okRoute)
			assert.Equal(t, test.want, api.Get("/system").Code)
		})
	}
}

// TestOwnedTenantMemberAuthorizesByMembershipAlone pins the property that
// distinguishes this declaration from RegisterOwned: the owning tenant's
// active membership is the whole authorization, so no administrative
// permission is invented for a self-service resource.
func TestOwnedTenantMemberAuthorizesByMembershipAlone(t *testing.T) {
	_, api := humatest.New(t)
	verifier := validVerifier()
	checker := &fakeChecker{}
	registrar := authz.NewRegistrar(testRegistry(), verifier, checker, &fakeMembership{active: true})
	var seen authn.Claims
	authz.RegisterOwnedTenantMember(registrar, api, huma.Operation{
		OperationID: "access-request-view", Method: http.MethodGet, Path: "/access-requests/{request_id}", Tags: []string{"access-requests"},
	}, "request_id", func(_ context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
		assert.Equal(t, guid.ID(41), id)
		return authz.ResourceOwner{TenantID: 11}, true, nil
	}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
		claims, ok := authn.FromContext(ctx)
		require.True(t, ok)
		seen = *claims
		return &routeOutput{Body: "ok"}, nil
	})

	response := api.Get("/access-requests/41")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, guid.ID(11), seen.TenantID)
	assert.Zero(t, seen.EntityID)
	assert.Equal(t, guid.ID(31), seen.PrincipalID)
	assert.Empty(t, checker.tenantCalls, "a member-scoped resource route must not consult an administrative permission")
	assert.Empty(t, checker.entityCalls)
	assert.Empty(t, verifier.scopes,
		"the route enforces no permission, so it publishes no personal-token scope for one to satisfy")

	operation := api.OpenAPI().Paths["/access-requests/{request_id}"].Get
	assert.True(t, authz.IsTenantMemberOperation(operation))
	assert.True(t, authz.IsOwnedResourceOperation(operation))
	assert.False(t, authz.IsOwnedResourceOperation(nil))
	assert.False(t, authz.IsOwnedResourceOperation(&huma.Operation{}))
	assert.Contains(t, operation.Errors, http.StatusNotFound)
	assertSelectorSchema(t, operation, "request_id", "path")
}

func TestOwnedTenantMemberFailurePaths(t *testing.T) {
	tests := []struct {
		name       string
		membership *fakeMembership
		want       int
	}{
		{name: "outside the owner tenant", membership: &fakeMembership{}, want: http.StatusNotFound},
		{name: "membership unavailable", membership: &fakeMembership{err: assert.AnError}, want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, api := humatest.New(t)
			registrar := authz.NewRegistrar(testRegistry(), validVerifier(), &fakeChecker{allowed: true}, test.membership)
			authz.RegisterOwnedTenantMember(registrar, api, huma.Operation{
				OperationID: "access-request-view", Method: http.MethodGet, Path: "/access-requests/{request_id}", Tags: []string{"access-requests"},
			}, "request_id", func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
				return authz.ResourceOwner{TenantID: 11}, true, nil
			}, okRoute)
			assert.Equal(t, test.want, api.Get("/access-requests/41").Code)
		})
	}
}

func TestOwnedTenantMemberRejectsIncompleteDeclarations(t *testing.T) {
	registry := testRegistry()
	resolve := func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{TenantID: 11}, true, nil
	}
	operation := func() huma.Operation {
		return huma.Operation{OperationID: "owned-member", Method: http.MethodGet, Path: "/access-requests/{request_id}", Tags: []string{"access-requests"}}
	}
	untagged := operation()
	untagged.Tags = nil

	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterOwnedTenantMember[struct{}, routeOutput](nil, api, operation(), "request_id", resolve, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterOwnedTenantMember(authz.NewDeclarationOnlyRegistrar(registry), api, operation(), "", resolve, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterOwnedTenantMember[struct{}, routeOutput](authz.NewDeclarationOnlyRegistrar(registry), api, operation(), "request_id", nil, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterOwnedTenantMember(authz.NewDeclarationOnlyRegistrar(registry), api, untagged, "request_id", resolve, okRoute)
	}, "an untagged owned route lands in the wrong OpenAPI group, so the declaration is refused")
	assert.NotPanics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterOwnedTenantMember(authz.NewDeclarationOnlyRegistrar(registry), api, operation(), "request_id", resolve, okRoute)
	})
}

// TestOwnedPersonalResourceBindsTheOwningPrincipal covers the second owner
// shape ownedResourceMiddleware accepts. A resource owned by a principal
// directly belongs to that principal alone: no tenant membership and no
// tenant grant may reach it, and no other principal may read it.
func TestOwnedPersonalResourceBindsTheOwningPrincipal(t *testing.T) {
	tests := []struct {
		name   string
		claims *authn.Claims
		owner  authz.ResourceOwner
		want   int
	}{
		{name: "owner", claims: validClaims(), owner: authz.ResourceOwner{PrincipalID: 31}, want: http.StatusOK},
		{name: "owner identified by subject only", claims: &authn.Claims{Subject: "31"}, owner: authz.ResourceOwner{PrincipalID: 31}, want: http.StatusOK},
		{name: "another principal", claims: validClaims(), owner: authz.ResourceOwner{PrincipalID: 77}, want: http.StatusNotFound},
		{name: "unresolvable principal", claims: &authn.Claims{Subject: "bad"}, owner: authz.ResourceOwner{PrincipalID: 31}, want: http.StatusForbidden},
		{name: "unclassifiable owner", claims: validClaims(), owner: authz.ResourceOwner{}, want: http.StatusNotFound},
		{name: "principal and tenant owner", claims: validClaims(), owner: authz.ResourceOwner{PrincipalID: 31, TenantID: 11}, want: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, api := humatest.New(t)
			// Both collaborators fail closed, so reaching either one turns a
			// correct 200 into a 503 or a 403 and this test notices.
			checker := &fakeChecker{}
			membership := &fakeMembership{err: assert.AnError}
			registrar := authz.NewRegistrar(testRegistry(), &fakeVerifier{claims: test.claims, cookie: "session"}, checker, membership)
			var seen authn.Claims
			authz.RegisterOwned(registrar, api, huma.Operation{
				OperationID: "owned-personal", Method: http.MethodGet, Path: "/tokens/{token_id}", Tags: []string{"tokens"},
			}, "token_id", func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
				return test.owner, true, nil
			}, authz.Guard{Resource: "store", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
				claims, ok := authn.FromContext(ctx)
				require.True(t, ok)
				seen = *claims
				return &routeOutput{Body: "ok"}, nil
			})

			response := api.Get("/tokens/41")
			require.Equal(t, test.want, response.Code, response.Body.String())
			if test.want != http.StatusOK {
				return
			}
			assert.Equal(t, guid.ID(31), seen.PrincipalID)
			assert.Zero(t, seen.TenantID, "a personally owned resource binds no tenant")
			assert.Zero(t, seen.EntityID)
			assert.Empty(t, checker.tenantCalls, "a personally owned resource is not authorized by a tenant grant")
		})
	}
}

// TestTenantEntityOrAuthenticatedPublishesThreeDistinctContexts pins the
// three URIs this declaration emits and the one authorization model each of
// them carries.
func TestTenantEntityOrAuthenticatedPublishesThreeDistinctContexts(t *testing.T) {
	_, api := humatest.New(t)
	checker := &fakeChecker{allowed: true}
	registrar := authz.NewRegistrar(testRegistry(), validVerifier(), checker, &fakeMembership{active: true})
	seen := make([]authn.Claims, 0, 3)
	authz.RegisterTenantEntityOrAuthenticated(registrar, api, huma.Operation{
		Tags:        []string{"tenants"},
		OperationID: "files", Method: http.MethodGet, Path: "/files",
	}, authz.Guard{Resource: "store", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
		claims, ok := authn.FromContext(ctx)
		require.True(t, ok)
		seen = append(seen, *claims)
		return &routeOutput{Body: "ok"}, nil
	})

	require.Equal(t, http.StatusOK, api.Get("/principals/"+"31"+"/files").Code)
	assert.Zero(t, seen[0].TenantID)
	assert.Zero(t, seen[0].EntityID)
	assert.Empty(t, checker.tenantCalls, "the personal context authorizes on authentication alone")

	require.Equal(t, http.StatusOK, api.Get("/tenants/11/files").Code)
	assert.Equal(t, guid.ID(11), seen[1].TenantID)
	assert.Zero(t, seen[1].EntityID)
	assert.Equal(t, []tenantPermissionCall{{tenantID: 11, permission: "store_view", subject: 31}}, checker.tenantCalls)

	require.Equal(t, http.StatusOK, api.Get("/tenants/11/entities/21/files").Code)
	assert.Equal(t, guid.ID(11), seen[2].TenantID)
	assert.Equal(t, guid.ID(21), seen[2].EntityID)
	assert.Equal(t, []entityPermissionCall{{tenantID: 11, entityID: 21, permission: "store_view", subject: 31}}, checker.entityCalls)
	assert.Len(t, checker.tenantCalls, 1, "the entity context authorizes against the entity, not the tenant")

	personal := api.OpenAPI().Paths["/principals/{principal_id}/files"].Get
	tenant := api.OpenAPI().Paths["/tenants/{tenant_id}/files"].Get
	entity := api.OpenAPI().Paths["/tenants/{tenant_id}/entities/{entity_id}/files"].Get
	assert.Equal(t, "files-self", personal.OperationID)
	assert.Equal(t, "files-tenant", tenant.OperationID)
	assert.Equal(t, "files", entity.OperationID)
	assert.True(t, authz.IsAuthenticatedOperation(personal))
	assert.False(t, authz.IsAuthenticatedOperation(tenant))
	assert.Equal(t, "store_view", tenant.Extensions[authz.GuardExtensionKey])
	assert.Equal(t, "store_view", entity.Extensions[authz.GuardExtensionKey])
}

func validClaims() *authn.Claims {
	return &authn.Claims{Subject: "31", PrincipalID: 31, ACR: 2, AMR: []string{"pwd"}, ClientID: "console", NetworkZone: "internal"}
}

func validVerifier() *fakeVerifier {
	return &fakeVerifier{claims: validClaims(), cookie: "session"}
}

func assertSelectorSchema(t *testing.T, operation *huma.Operation, name, location string) {
	t.Helper()
	for _, parameter := range operation.Parameters {
		if parameter.Name == name && parameter.In == location {
			assert.True(t, parameter.Required)
			require.NotNil(t, parameter.Schema)
			assert.Equal(t, huma.TypeString, parameter.Schema.Type)
			assert.Equal(t, `^[1-9][0-9]*$`, parameter.Schema.Pattern)
			require.NotNil(t, parameter.Schema.MaxLength)
			assert.Equal(t, 19, *parameter.Schema.MaxLength)
			return
		}
	}
	t.Fatalf("missing %s selector %s", location, name)
}

// logCapture records what the registrar decided was worth an operator's
// attention.
type logCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (capture *logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (capture *logCapture) WithAttrs([]slog.Attr) slog.Handler       { return capture }
func (capture *logCapture) WithGroup(string) slog.Handler            { return capture }

func (capture *logCapture) Handle(_ context.Context, record slog.Record) error {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.records = append(capture.records, record)
	return nil
}

func (capture *logCapture) levels(level slog.Level) int {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	count := 0
	for _, record := range capture.records {
		if record.Level == level {
			count++
		}
	}
	return count
}

// TestAuthorizationFailureSeparatesABrokenDependencyFromADepartedCaller keeps
// the error log usable.
//
// A browser that navigates away cancels whatever it had in flight, and the
// authorization checks running for those requests fail on the dead context.
// Reporting each one as an error buries the failures that mean something: the
// acceptance run that prompted this logged errors for an ordinary page
// navigation, and the real fault had to be found among them.
func TestAuthorizationFailureSeparatesABrokenDependencyFromADepartedCaller(t *testing.T) {
	for name, testCase := range map[string]struct {
		canceled   bool
		wantErrors int
		wantDebug  int
	}{
		"a dependency that broke": {wantErrors: 1},
		"a caller that went away": {canceled: true, wantDebug: 1},
	} {
		t.Run(name, func(t *testing.T) {
			capture := &logCapture{}
			previous := slog.Default()
			slog.SetDefault(slog.New(capture))
			t.Cleanup(func() { slog.SetDefault(previous) })

			_, api := humatest.New(t)
			registrar := authz.NewRegistrar(
				testRegistry(), validVerifier(),
				&fakeChecker{err: errors.New("checker is down")},
				&fakeMembership{active: true},
			)
			authz.Register(registrar, api, huma.Operation{
				OperationID: "store-view", Method: http.MethodGet, Path: "/stores",
				Tags: []string{"tenants"},
			}, authz.Guard{Resource: "store", Verb: "view"}, okRoute)

			ctx := t.Context()
			if testCase.canceled {
				cancellable, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancellable
			}
			response := api.DoCtx(ctx, http.MethodGet, "/tenants/11/stores",
				"Authorization: Bearer test")
			assert.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			assert.Equal(t, testCase.wantErrors, capture.levels(slog.LevelError),
				"error records")
			assert.Equal(t, testCase.wantDebug, capture.levels(slog.LevelDebug),
				"debug records")
		})
	}
}
