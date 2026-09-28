package core

import (
	"encoding/json"
	"net/http"
	"testing"

	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestSystemMyPermissionsHTTPReportsOnlyTheCallersSystemAuthority(t *testing.T) {
	h := seedSystemMenuHarness(t)
	seedHTTPPrincipal(t, h.repositoryHarness, 22)
	seedHTTPPrincipal(t, h.repositoryHarness, 33)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_administrators(principal_id,created_at) VALUES(11,?)", repositoryTestTime).Error)
	bootstrapSystemTestAccess(t, h)
	seedSystemPermissions(t, h, 22, "system_menu_view")
	require.NoError(t, initializeHTTPLocales())
	verifier := &systemAccessVerifier{principal: 11}
	registrar := authz.NewRegistrar(h.service.registry, verifier, h.authorizer, httpMembershipChecker{})
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("system-my-permissions-test", "1"))
	registerPlatformHTTP(api, h.service, registrar)
	client := humatest.Wrap(t, api)
	codes := func() []string {
		response := client.Get("/system/permissions", "Authorization: Bearer test")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		return decodeEnvelope[[]string](t, response).Data
	}

	full := codes()
	require.Contains(t, full, "platform_administer")
	require.Contains(t, full, "system_menu_view")
	for _, code := range full {
		action, ok := h.service.registry.Find(code)
		require.True(t, ok, code)
		require.Equal(t, "platform", action.Scope, "only system permissions are reported: %s", code)
	}
	verifier.principal = 22
	require.Equal(t, []string{"system_menu_view"}, codes(), "a delegated account reports exactly its granted codes")
	verifier.principal = 33
	require.Empty(t, codes(), "an account without system roles holds nothing")

	verifier.principal = 11
	for _, view := range []*authn.AccountViewIdentity{
		{ID: 50, ActorID: 11, ScopeType: "tenant", ScopeID: 1},
		{ID: 50, ActorID: 11, ScopeType: "system", ScopeID: 2},
	} {
		verifier.view = view
		require.Equal(t, http.StatusForbidden, client.Get("/system/permissions", "Authorization: Bearer test").Code, view.ScopeType)
	}
	verifier.view = &authn.AccountViewIdentity{ID: 50, ActorID: 11, ScopeType: "system", ScopeID: systemScopeID}
	require.Contains(t, codes(), "platform_administer", "a system view keeps the viewed account's authority")

	verifier.view = nil
	connection, err := h.db.DB()
	require.NoError(t, err)
	require.NoError(t, connection.Close())
	response := client.Get("/system/permissions", "Authorization: Bearer test")
	require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	require.Equal(t, "null", string(decodeEnvelope[json.RawMessage](t, response).Data), "a storage failure reports no permissions")
}
