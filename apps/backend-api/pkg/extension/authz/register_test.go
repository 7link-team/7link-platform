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
	claims    *authn.Claims
	err       error
	csrfErr   error
	cookie    string
	csrfCalls []csrfValidationCall
}

type csrfValidationCall struct {
	method        string
	origin        string
	cookie        string
	authorization string
}

func (verifier *fakeVerifier) Authenticate(context.Context, string, string) (*authn.Claims, error) {
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
		op := huma.Operation{OperationID: "tenant-context", Method: http.MethodGet, Path: "/tenant-context"}
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
		return huma.Operation{OperationID: id, Method: http.MethodGet, Path: "/" + id}
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
			authz.Register(registrar, api, huma.Operation{OperationID: "store-view", Method: http.MethodGet, Path: "/stores"}, authz.Guard{Resource: "store", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
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
	authz.RegisterTenantMember(registrar, api, huma.Operation{OperationID: "member", Method: http.MethodGet, Path: "/member"}, okRoute)
	authz.RegisterAuthenticated(registrar, api, huma.Operation{OperationID: "me", Method: http.MethodGet, Path: "/me"}, okRoute)
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
	authz.Register(registrar, api, huma.Operation{OperationID: "store", Method: http.MethodGet, Path: "/store"}, authz.Guard{Resource: "store", Verb: "view"}, okRoute)
	authz.RegisterEntity(registrar, api, huma.Operation{OperationID: "workflow", Method: http.MethodGet, Path: "/workflow"}, authz.Guard{Resource: "workflow", Verb: "view"}, okRoute)
	authz.RegisterPlatform(registrar, api, huma.Operation{OperationID: "tenants", Method: http.MethodGet, Path: "/tenants"}, authz.Guard{Resource: "tenant", Verb: "view"}, okRoute)

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
		assert.Equal(t, http.StatusOK, api.Get("/tenants/11/entities/21/workflow").Code)
		assert.Equal(t, http.StatusForbidden, api.Get("/tenants/bad/entities/21/workflow").Code)
	})

	t.Run("entity subject fallback and adapter", func(t *testing.T) {
		verifier := &fakeVerifier{claims: &authn.Claims{Subject: "31"}}
		_, api := humatest.New(t)
		registrar := authz.NewRegistrar(testRegistry(), verifier, &fakeChecker{allowed: true}, &fakeMembership{active: true})
		authz.RegisterEntityAdapter(registrar, api, huma.Operation{OperationID: "stream", Method: http.MethodGet, Path: "/stream"}, authz.Guard{Resource: "workflow", Verb: "view"}, func(ctx huma.Context) {
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
		OperationID: "contextual-files", Method: http.MethodGet, Path: "/contextual-files",
	}, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
		claims, ok := authn.FromContext(ctx)
		require.True(t, ok)
		seen = append(seen, *claims)
		return &routeOutput{Body: "ok"}, nil
	})

	require.Equal(t, http.StatusOK, api.Get("/me/contextual-files").Code)
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
			authz.RegisterEntity(registrar, api, huma.Operation{OperationID: "entity", Method: http.MethodGet, Path: "/entity"}, authz.Guard{Resource: "workflow", Verb: "view"}, okRoute)
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
			authz.RegisterEntity(registrar, api, huma.Operation{OperationID: "tasks", Method: http.MethodGet, Path: "/tasks"}, authz.Guard{Resource: "task", Verb: "view", FilterData: true}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
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

			authz.RegisterEntity(registrar, api, huma.Operation{OperationID: "whole", Method: http.MethodGet, Path: "/whole"}, authz.Guard{Resource: "task", Verb: "view"}, okRoute)
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
			op := huma.Operation{OperationID: "tasks", Method: http.MethodGet, Path: "/tasks"}
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
	authz.Register(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{OperationID: "tenant-tasks", Method: http.MethodGet, Path: "/tenant-tasks"}, guard, okRoute)
	assert.Equal(t, true, api.OpenAPI().Paths["/tenants/{tenant_id}/tenant-tasks"].Get.Extensions[authz.DataFilterExtensionKey])
	authz.RegisterEntity(authz.NewDeclarationOnlyRegistrar(registry), api, huma.Operation{OperationID: "tasks", Method: http.MethodGet, Path: "/tasks"}, guard, okRoute)
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

func hasSelector(operation *huma.Operation, name, location string) bool {
	for _, parameter := range operation.Parameters {
		if parameter.Name == name && parameter.In == location {
			return true
		}
	}
	return false
}
