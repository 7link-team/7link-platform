package identity

import (
	"7link.com/platform/internal/infra/guid"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGlobalAccountCreationHasNoImplicitMembershipAndRollsBackAuditFailure(t *testing.T) {
	h := newIdentityHarness(t, "sl_")
	account, err := h.service.CreateGlobal(t.Context(), " Global.Account ", testPassword, "Global Account", "Global@Example.test")
	require.NoError(t, err)
	require.Equal(t, "global.account", account.LoginName)
	require.Equal(t, "global@example.test", account.Email)
	require.EqualValues(t, 1, h.credentialVersion(t, account.ID))
	for _, table := range []string{"iam_tenant_members", "iam_policy_revisions"} {
		var count int64
		require.NoError(t, h.db.Table(h.table(table)).Count(&count).Error)
		require.Zero(t, count)
	}
	require.False(t, h.events()[0].Detail["membership_created"].(bool))
	_, err = h.service.Get(t.Context(), 7, account.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = h.service.GetGlobal(t.Context(), account.ID)
	require.NoError(t, err)
	h.auditErr = errors.New("audit unavailable")
	_, err = h.service.CreateGlobal(t.Context(), "rollback", testPassword, "Rollback", "")
	require.ErrorIs(t, err, h.auditErr)
	require.EqualValues(t, 1, h.principalCount(t))
	var credentials int64
	require.NoError(t, h.db.Table(h.table("iam_credentials")).Count(&credentials).Error)
	require.EqualValues(t, 1, credentials)
}

func TestSystemAccountWorkspacesReflectOnlyActiveTargetMemberships(t *testing.T) {
	h := newIdentityHTTPHarnessWithPlatform(t, true, true)
	require.NoError(t, h.db.Exec("ALTER TABLE sl_iam_tenants ADD COLUMN scope_kind TEXT NOT NULL DEFAULT 'tenant'").Error)
	require.NoError(t, h.db.Exec("ALTER TABLE sl_iam_tenants ADD COLUMN name TEXT NOT NULL DEFAULT 'Tenant'").Error)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants (id, status, scope_kind, name) VALUES (9, 'active', 'platform', 'Operations'), (10, 'suspended', 'platform', 'Suspended Operations')").Error)
	require.NoError(t, h.db.Exec("CREATE TABLE sl_iam_entities (id BIGINT PRIMARY KEY, tenant_id BIGINT NOT NULL, name TEXT NOT NULL, status TEXT NOT NULL)").Error)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_entities (id, tenant_id, name, status) VALUES (101, 7, 'Active Entity', 'active'), (102, 7, 'Disabled Entity', 'disabled'), (103, 8, 'Other Tenant Entity', 'active'), (104, 9, 'Platform Entity', 'active')").Error)
	account, err := h.service.Create(t.Context(), 7, "target", testPassword, "Target", "")
	require.NoError(t, err)
	for _, owner := range []guid.ID{9, 10} {
		require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Create(map[string]any{
			"tenant_id": owner, "principal_id": account.ID, "display_name": "Target", "email": "", "status": "active", "created_at": identityTestNow, "updated_at": identityTestNow,
		}).Error)
	}
	response := h.api.Get("/system/principals/"+account.ID.String()+"/workspaces", "Authorization: Bearer session")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Data []AccountWorkspace `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.ElementsMatch(t, []AccountWorkspace{
		{ScopeType: "personal", ScopeID: account.ID, Name: "Target"},
		{ScopeType: "system", ScopeID: 1, Name: "System"},
		{ScopeType: "tenant", ScopeID: 7, Name: "Tenant"},
		{ScopeType: "platform", ScopeID: 9, Name: "Operations"},
		{ScopeType: "entity", ScopeID: 101, TenantID: 7, Name: "Active Entity"},
	}, body.Data)
	h.service.avatarPermissions = identityRouteAuthorizer{platformAllowed: false}
	items, err := h.service.AccountWorkspaces(t.Context(), account.ID)
	require.NoError(t, err)
	for _, item := range items {
		require.NotEqual(t, "system", item.ScopeType)
	}
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Where("principal_id = ? AND tenant_id = 9", account.ID).Update("status", "disabled").Error)
	items, err = h.service.AccountWorkspaces(t.Context(), account.ID)
	require.NoError(t, err)
	for _, item := range items {
		require.NotEqual(t, "platform", item.ScopeType)
	}
}

func TestSystemAccountHTTPUsesGlobalProfilesAndSystemAuthority(t *testing.T) {
	h := newIdentityHTTPHarnessWithPlatform(t, true, true)
	created := h.api.Post("/system/principals", "Authorization: Bearer session", map[string]any{
		"login_name": "global.account", "password": testPassword, "display_name": "Global Account", "email": "global@example.test",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var body struct {
		Data PrincipalView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &body))
	require.NotZero(t, body.Data.ID)
	require.Equal(t, "global.account", *body.Data.LoginName)
	var count int64
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Where("principal_id = ?", body.Data.ID).Count(&count).Error)
	require.Zero(t, count)
	list := h.api.Get("/system/principals?search=GLOBAL&limit=1", "Authorization: Bearer session")
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var page struct {
		Data principalListData `json:"data"`
	}
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &page))
	require.EqualValues(t, 1, page.Data.Total)
	require.Len(t, page.Data.Items, 1)
	path := "/system/principals/" + body.Data.ID.String()
	get := h.api.Get(path, "Authorization: Bearer session")
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	updated := h.api.Patch(path, "Authorization: Bearer session", map[string]any{"display_name": "Renamed Global Account", "email": "changed@example.test"})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	require.NoError(t, json.Unmarshal(updated.Body.Bytes(), &body))
	require.Equal(t, "Renamed Global Account", *body.Data.DisplayName)
	require.Equal(t, "changed@example.test", *body.Data.Email)
	require.EqualValues(t, 2, h.credentialVersion(t, body.Data.ID))
	bad := h.api.Patch(path, "Authorization: Bearer session", map[string]any{})
	require.Equal(t, http.StatusUnprocessableEntity, bad.Code, bad.Body.String())
	denied := newIdentityHTTPHarnessWithPlatform(t, true, false)
	for _, response := range []int{
		denied.api.Get("/system/principals", "Authorization: Bearer session").Code,
		denied.api.Get("/system/principals/1001", "Authorization: Bearer session").Code,
		denied.api.Post("/system/principals", "Authorization: Bearer session", map[string]any{"login_name": "blocked", "password": testPassword}).Code,
		denied.api.Patch("/system/principals/1001", "Authorization: Bearer session", map[string]any{"display_name": "Blocked"}).Code,
	} {
		require.Equal(t, http.StatusForbidden, response)
	}
}
