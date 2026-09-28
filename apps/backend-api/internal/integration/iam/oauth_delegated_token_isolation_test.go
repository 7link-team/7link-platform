package iamintegration

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An access token issued through /oauth/token belongs to an OAuth client of one
// tenant and carries only identity scopes. The principal it names here holds
// administrative grants in tenant A and platform administration, and consents
// to a client registered by tenant B. That token must not reach any operation
// guarded through the Registrar -- not tenant A, not /system, not the operating
// platforms, not tenant B itself -- while /oauth/userinfo keeps answering it.
func TestComposedDelegatedOAuthTokenIsRefusedOnEveryGuardedOperation(t *testing.T) {
	f := newIAMTenantFixture(t)
	principal := f.adminA
	response := f.request(t, http.MethodPost, "/members", iamTenantB, f.tokenB, map[string]any{
		"subject": principal.ID.String(), "display_name": "Tenant A administrator", "email": principal.Email, "status": "active",
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	cookie := f.closureCookie(t, principal)
	_, _, delegated := f.closureOAuth(t, cookie, iamTenantB)
	require.NotEmpty(t, delegated.AccessToken)

	// The same principal's own token still reaches the probed routes, so the
	// refusals below are about the token's origin and not about missing grants.
	for _, path := range []string{"/system/roles", "/tenants/" + iamTenantA.String() + "/members"} {
		requireStatus(t, http.StatusOK, f.request(t, http.MethodGet, path, 0, f.tokenA, nil, nil))
	}

	response = f.request(t, http.MethodGet, "/oauth/userinfo", 0, delegated.AccessToken, nil, nil)
	requireStatus(t, http.StatusOK, response)
	assert.Contains(t, string(response.Body), principal.ID.String())

	operations := composedGuardedOperations(t, f)
	require.NotEmpty(t, operations)
	sessionOnly := 0
	for _, operation := range operations {
		path := guardedOperationPath(operation, principal.ID.String())
		t.Run(operation.Method+" "+operation.Path, func(t *testing.T) {
			response := f.request(t, operation.Method, path, 0, delegated.AccessToken, nil, nil)
			// Browser-session operations refuse every bearer before the token
			// is even read; the rest must refuse it as a forbidden origin.
			want := http.StatusForbidden
			if len(operation.Security) == 1 {
				if _, ok := operation.Security[0][authz.SessionScheme]; ok {
					want = http.StatusUnauthorized
				}
			}
			assert.Equal(t, want, response.StatusCode, string(response.Body))
		})
		if len(operation.Security) == 1 {
			if _, ok := operation.Security[0][authz.SessionScheme]; ok {
				sessionOnly++
			}
		}
	}
	assert.Less(t, sessionOnly, len(operations))
}

// composedGuardedOperations lists every operation the composed application
// registers through the Registrar. The fixture server was built from the same
// composition; this second registration only reads the declarations back.
func composedGuardedOperations(t *testing.T, f *iamTenantFixture) []*huma.Operation {
	t.Helper()
	api := humax.New(chi.NewRouter(), humax.Config{Name: "delegated-token-inventory", Version: "1"})
	require.NoError(t, f.app.Modules().RegisterHTTP(t.Context(), api))
	var operations []*huma.Operation
	for _, item := range api.OpenAPI().Paths {
		for _, operation := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete, item.Patch, item.Head} {
			if operation == nil || authz.IsPublicOperation(operation) {
				continue
			}
			_, guarded := authz.OperationGuard(operation)
			if guarded || authz.IsAuthenticatedOperation(operation) || authz.IsTenantMemberOperation(operation) ||
				authz.IsOwnedResourceOperation(operation) || authz.IsPlatformOperation(operation) ||
				authz.IsOperatingPlatformOperation(operation) {
				operations = append(operations, operation)
			}
		}
	}
	sort.Slice(operations, func(i, j int) bool {
		return operations[i].Path+" "+operations[i].Method < operations[j].Path+" "+operations[j].Method
	})
	return operations
}

// guardedOperationPath fills every path selector. Tenant and platform selectors
// name tenant A, where the principal holds real grants; the principal selector
// names the principal itself; any other identifier names nothing in particular.
func guardedOperationPath(operation *huma.Operation, principalID string) string {
	path := operation.Path
	for _, parameter := range operation.Parameters {
		if parameter == nil || parameter.In != "path" {
			continue
		}
		value := "91999"
		switch parameter.Name {
		case authz.TenantPath, authz.OperatingPlatformPath, "owner_id":
			value = iamTenantA.String()
		case authz.PrincipalPath:
			value = principalID
		}
		path = strings.ReplaceAll(path, "{"+parameter.Name+"}", value)
	}
	return path
}
