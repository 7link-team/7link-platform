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
	"github.com/stretchr/testify/require"
)

type operatingPlatformChecker struct {
	fakeChecker
	platformID guid.ID
	subject    guid.ID
	permission string
}

func (c *operatingPlatformChecker) CheckOperatingPlatform(_ context.Context, id guid.ID, permission string, subject guid.ID) (bool, error) {
	c.platformID, c.permission, c.subject = id, permission, subject
	return c.allowed, nil
}

type operatingPlatformMembers struct{ fakeMembership }

func (m *operatingPlatformMembers) IsOperatingPlatformMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return m.active, nil
}

func TestOperatingPlatformRouteBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, scope     string
		id              guid.ID
		active, allowed bool
		status          int
	}{
		{"ordinary-member", "", 0, true, true, http.StatusOK},
		{"inactive", "", 0, false, true, http.StatusForbidden},
		{"permission-denied", "", 0, true, false, http.StatusForbidden},
		{"own-view", "platform", 11, true, true, http.StatusOK},
		{"other-view", "platform", 12, true, true, http.StatusForbidden},
		{"tenant-view", "tenant", 11, true, true, http.StatusForbidden},
		{"system-view", "system", 1, true, true, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, api := humatest.New(t)
			claims := &authn.Claims{PrincipalID: 9}
			if tc.scope != "" {
				claims.AccountView = &authn.AccountViewIdentity{ID: 50, ActorID: 7, ReadOnly: true, ScopeType: tc.scope, ScopeID: tc.id}
			}
			checker := &operatingPlatformChecker{fakeChecker: fakeChecker{allowed: tc.allowed}}
			members := &operatingPlatformMembers{fakeMembership: fakeMembership{active: tc.active}}
			r := authz.NewRegistrar(authz.DefaultRegistry(), &fakeVerifier{claims: claims}, checker, members)
			called := false
			authz.RegisterOperatingPlatform(r, api, huma.Operation{OperationID: "platform-roles", Method: http.MethodGet, Path: "/roles", Tags: []string{"operating-platforms"}}, authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*routeOutput, error) {
				called = true
				verified, ok := authn.FromContext(ctx)
				require.True(t, ok)
				require.EqualValues(t, 11, verified.TenantID)
				return &routeOutput{Body: "ok"}, nil
			})
			response := api.Get("/operating-platforms/11/roles")
			require.Equal(t, tc.status, response.Code, response.Body.String())
			require.Equal(t, tc.status == http.StatusOK, called)
			if called {
				require.EqualValues(t, 11, checker.platformID)
				require.EqualValues(t, 9, checker.subject)
				require.Equal(t, "role_view", checker.permission)
			}
			require.Empty(t, checker.platformCalls)
			require.Empty(t, checker.tenantCalls)
		})
	}
}

func TestOperatingPlatformRouteRejectsBusinessPermission(t *testing.T) {
	_, api := humatest.New(t)
	r := authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry())
	require.Panics(t, func() {
		authz.RegisterOperatingPlatform(r, api, huma.Operation{OperationID: "platform-files", Method: http.MethodGet, Path: "/files", Tags: []string{"operating-platforms"}}, authz.Guard{Resource: "file", Verb: "view"}, func(context.Context, *struct{}) (*routeOutput, error) { return nil, nil })
	})
}
