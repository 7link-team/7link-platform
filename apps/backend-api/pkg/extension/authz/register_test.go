package authz_test

import (
	"context"
	"errors"
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

type routeOutput struct{ Body string }

type fakeVerifier struct {
	claims  *authn.Claims
	err     error
	csrfErr error
	cookie  string
}

func (verifier *fakeVerifier) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	if verifier.err != nil {
		return nil, verifier.err
	}
	return verifier.claims, nil
}

func (verifier *fakeVerifier) ValidateCSRF(string, string, string, string) error {
	return verifier.csrfErr
}

func (verifier *fakeVerifier) SessionCookieName() string { return verifier.cookie }

type fakeChecker struct {
	allowed bool
	err     error
}

func (checker *fakeChecker) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return checker.allowed, checker.err
}

func (checker *fakeChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return checker.allowed, checker.err
}

func (checker *fakeChecker) CheckEntity(context.Context, guid.ID, guid.ID, string, guid.ID) (bool, error) {
	return checker.allowed, checker.err
}

type tenantOnlyChecker struct{ allowed bool }

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

func TestRegistrarDeclarationsCoverEveryRouteClass(t *testing.T) {
	registry := testRegistry()
	declarations := authz.NewDeclarationOnlyRegistrar(registry)
	assert.Same(t, registry, declarations.Registry())
	assert.True(t, declarations.IsDeclarationOnly())

	_, api := humatest.New(t)
	authz.Register(declarations, api, huma.Operation{OperationID: "store-view", Method: http.MethodGet, Path: "/stores"}, authz.Guard{Resource: "store", Verb: "view"}, okRoute)
	authz.RegisterEntity(declarations, api, huma.Operation{OperationID: "workflow-view", Method: http.MethodGet, Path: "/workflows"}, authz.Guard{Resource: "workflow", Verb: "view"}, okRoute)
	authz.RegisterTenantMember(declarations, api, huma.Operation{OperationID: "member-view", Method: http.MethodGet, Path: "/member"}, okRoute)
	authz.RegisterAuthenticated(declarations, api, huma.Operation{OperationID: "me-view", Method: http.MethodGet, Path: "/me"}, okRoute)
	authz.RegisterPlatform(declarations, api, huma.Operation{OperationID: "tenant-view", Method: http.MethodGet, Path: "/tenants"}, authz.Guard{Resource: "tenant", Verb: "view"}, okRoute)
	authz.RegisterPublic(api, huma.Operation{OperationID: "public", Method: http.MethodGet, Path: "/public"}, okRoute)
	authz.RegisterEntityAdapter(declarations, api, huma.Operation{OperationID: "workflow-stream", Method: http.MethodGet, Path: "/workflow-stream"}, authz.Guard{Resource: "workflow", Verb: "view"}, func(ctx huma.Context) {
		ctx.SetStatus(http.StatusNoContent)
	})

	require.NoError(t, authz.ValidateOperations(api, registry))
	assert.Equal(t, "store_view", api.OpenAPI().Paths["/stores"].Get.Extensions[authz.GuardExtensionKey])
	assert.True(t, authz.IsTenantMemberOperation(api.OpenAPI().Paths["/member"].Get))
	assert.True(t, authz.IsPlatformOperation(api.OpenAPI().Paths["/tenants"].Get))
	assert.False(t, authz.IsTenantMemberOperation(nil))
	assert.False(t, authz.IsPlatformOperation(nil))
	assert.Len(t, authz.UserSecurity(), 2)
	assert.Len(t, authz.BearerSecurity(), 1)
	assert.Len(t, authz.ClientSecurity(), 1)
	assert.Equal(t, http.StatusNoContent, api.Get("/workflow-stream").Code)
}

func TestRegistrarRejectsInvalidConstructionAndDeclarations(t *testing.T) {
	registry := testRegistry()
	verifier := &fakeVerifier{claims: &authn.Claims{PrincipalID: 31}}
	membership := &fakeMembership{active: true}
	checker := &fakeChecker{allowed: true}

	assert.Panics(t, func() { authz.NewRegistrar(nil, verifier, checker, membership) })
	assert.Panics(t, func() { authz.NewRegistrar(registry, nil, checker, membership) })
	assert.Panics(t, func() { authz.NewDeclarationOnlyRegistrar(nil) })
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.Register[struct{}, routeOutput](nil, api, huma.Operation{}, authz.Guard{}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.Register(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{Method: http.MethodGet, Path: "/missing-id"}, authz.Guard{Resource: "store", Verb: "view"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.Register(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{OperationID: "unknown", Method: http.MethodGet, Path: "/unknown"}, authz.Guard{Resource: "missing", Verb: "view"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		registrar := authz.NewRegistrar(registry, verifier, tenantOnlyChecker{allowed: true}, membership)
		authz.RegisterEntity(registrar, api, huma.Operation{OperationID: "entity", Method: http.MethodGet, Path: "/entity"}, authz.Guard{Resource: "workflow", Verb: "view"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterPlatform(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{OperationID: "wrong-scope", Method: http.MethodGet, Path: "/wrong-scope"}, authz.Guard{Resource: "store", Verb: "view"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterTenantMember(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{Method: http.MethodGet, Path: "/member"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterAuthenticated(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{Method: http.MethodGet, Path: "/me"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterPlatform(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{Method: http.MethodGet, Path: "/platform"}, authz.Guard{Resource: "tenant", Verb: "view"}, okRoute)
	})
	assert.Panics(t, func() {
		_, api := humatest.New(t)
		authz.RegisterEntityAdapter(nil, api, huma.Operation{}, authz.Guard{}, func(huma.Context) {})
	})
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
			authz.Register(registrar, api, huma.Operation{OperationID: "store-view", Method: http.MethodGet, Path: "/stores"}, authz.Guard{Resource: "store", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
				claims, ok := authn.FromContext(ctx)
				require.True(t, ok)
				assert.Equal(t, guid.ID(31), claims.PrincipalID)
				return &routeOutput{Body: "ok"}, nil
			})
			response := api.Get("/stores", authz.TenantHeader+": "+test.tenant)
			assert.Equal(t, test.want, response.Code)
		})
	}
}

func TestTenantMemberAndAuthenticatedMiddleware(t *testing.T) {
	_, api := humatest.New(t)
	registrar := authz.NewRegistrar(testRegistry(), validVerifier(), &fakeChecker{allowed: true}, &fakeMembership{active: true})
	authz.RegisterTenantMember(registrar, api, huma.Operation{OperationID: "member", Method: http.MethodGet, Path: "/member"}, okRoute)
	authz.RegisterAuthenticated(registrar, api, huma.Operation{OperationID: "me", Method: http.MethodGet, Path: "/me"}, okRoute)
	assert.Equal(t, http.StatusOK, api.Get("/member", authz.TenantHeader+": 11").Code)
	assert.Equal(t, http.StatusOK, api.Get("/me").Code)

	_, rejected := humatest.New(t)
	bad := authz.NewRegistrar(testRegistry(), &fakeVerifier{err: authn.ErrInvalidToken}, &fakeChecker{allowed: true}, &fakeMembership{active: true})
	authz.RegisterAuthenticated(bad, rejected, huma.Operation{OperationID: "me", Method: http.MethodGet, Path: "/me"}, okRoute)
	assert.Equal(t, http.StatusUnauthorized, rejected.Get("/me").Code)
}

func TestEntityAndPlatformAuthorizationMiddleware(t *testing.T) {
	t.Run("entity", func(t *testing.T) {
		_, api := humatest.New(t)
		registrar := authz.NewRegistrar(testRegistry(), validVerifier(), &fakeChecker{allowed: true}, &fakeMembership{active: true})
		authz.RegisterEntity(registrar, api, huma.Operation{OperationID: "workflow", Method: http.MethodGet, Path: "/workflow"}, authz.Guard{Resource: "workflow", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
			claims, ok := authn.FromContext(ctx)
			require.True(t, ok)
			assert.Equal(t, guid.ID(11), claims.TenantID)
			assert.Equal(t, guid.ID(21), claims.EntityID)
			return &routeOutput{Body: "ok"}, nil
		})
		assert.Equal(t, http.StatusOK, api.Get("/workflow", authz.TenantHeader+": 11", authz.EntityHeader+": 21").Code)
		assert.Equal(t, http.StatusForbidden, api.Get("/workflow", authz.TenantHeader+": bad", authz.EntityHeader+": 21").Code)
	})

	t.Run("entity subject fallback and adapter", func(t *testing.T) {
		verifier := &fakeVerifier{claims: &authn.Claims{Subject: "31"}}
		_, api := humatest.New(t)
		registrar := authz.NewRegistrar(testRegistry(), verifier, &fakeChecker{allowed: true}, &fakeMembership{active: true})
		authz.RegisterEntityAdapter(registrar, api, huma.Operation{OperationID: "stream", Method: http.MethodGet, Path: "/stream"}, authz.Guard{Resource: "workflow", Verb: "view"}, func(ctx huma.Context) {
			ctx.SetStatus(http.StatusNoContent)
		})
		assert.Equal(t, http.StatusNoContent, api.Get("/stream?tenant_id=11&entity_id=21").Code)
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
			authz.RegisterEntity(registrar, api, huma.Operation{OperationID: "entity", Method: http.MethodGet, Path: "/entity"}, authz.Guard{Resource: "workflow", Verb: "view"}, okRoute)
			response := api.Get("/entity", authz.TenantHeader+": "+test.tenant, authz.EntityHeader+": "+test.entity)
			assert.Equal(t, test.want, response.Code)
		})
	}
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
			authz.RegisterPlatform(registrar, api, huma.Operation{OperationID: "platform", Method: http.MethodGet, Path: "/platform"}, authz.Guard{Resource: "tenant", Verb: "view"}, okRoute)
			assert.Equal(t, test.want, api.Get("/platform").Code)
		})
	}
}

func validClaims() *authn.Claims {
	return &authn.Claims{Subject: "31", PrincipalID: 31, ACR: 2, AMR: []string{"pwd"}, ClientID: "console", NetworkZone: "internal"}
}

func validVerifier() *fakeVerifier {
	return &fakeVerifier{claims: validClaims(), cookie: "session"}
}
