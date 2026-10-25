package composition_test

import (
	"7link.com/platform/internal/governance"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestComposedInboxEverySourceActuallyReadsAndCountsWithCurrentMembership(t *testing.T) {
	app := bootstrapComposedApp(t)
	db := app.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table("iam_tenants").Create(map[string]any{"id": 201, "slug": "inbox-current", "name": "Inbox current", "status": "active", "version": 1, "created_at": now, "updated_at": now}).Error)
	require.NoError(t, db.Table("iam_principals").Create(map[string]any{"id": 301, "login_name": "inbox-owner", "display_name": "Inbox owner", "status": "active", "created_at": now, "updated_at": now}).Error)
	bootstrap, e := module.Resolve[core.TenantBootstrapper](app.Modules().Services())
	require.NoError(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Inbox owner"}, now)
		if e != nil {
			return e
		}
		return bootstrap.BumpRevision(t.Context(), tx, 201, now)
	}))
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 201, PrincipalID: 301})
	roles, e := module.Resolve[governance.SeededRoles](app.Modules().Services())
	require.NoError(t, e)
	require.NoError(t, roles.EnsureTenant(actor, 201))
	web, e := module.Resolve[iamauthn.WebService](app.Modules().Services())
	require.NoError(t, e)
	token, _, e := web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, e)
	router := moduleRouter(t, app)
	call := func(path, bearer string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	for _, kind := range governance.InboxKinds() {
		t.Run(kind, func(t *testing.T) {
			response := call("/tenants/201/inbox-items?kind="+kind, token)
			require.Equal(t, 200, response.Code, response.Body.String())
			var page struct {
				Data governance.InboxPage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
			require.Len(t, page.Data.Capabilities, 1)
			found := false
			for _, c := range page.Data.Capabilities {
				if c.Kind == kind {
					found = true
					require.Equal(t, "AVAILABLE", c.Status, "registered adapter must execute its actual dependencies")
				}
			}
			require.True(t, found)
			response = call("/tenants/201/inbox-counts?kind="+kind, token)
			require.Equal(t, 200, response.Code, response.Body.String())
			var counts struct {
				Data governance.InboxCounts `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &counts))
			require.Contains(t, counts.Data.ByKind, kind)
		})
	}
	require.Equal(t, 401, call("/tenants/201/inbox-counts", "").Code)
	require.NoError(t, db.Table("iam_principals").Where("id=301").Update("status", "disabled").Error)
	for _, path := range []string{"/tenants/201/inbox-items", "/tenants/201/inbox-counts", "/tenants/201/inbox-items/WORK_ITEM_REVIEW:1"} {
		require.Equal(t, 401, call(path, token).Code)
	}
}
