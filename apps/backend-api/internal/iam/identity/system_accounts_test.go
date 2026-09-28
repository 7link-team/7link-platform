package identity

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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
	require.NoError(t, h.service.permissionRegistry.Register(authz.Action{Resource: "system_file", Verb: "view", Scope: "platform", Summary: "View system files"}))
	h.service.avatarPermissions = systemFileWorkspaceChecker{}
	items, err = h.service.AccountWorkspaces(t.Context(), account.ID)
	require.NoError(t, err)
	require.Contains(t, items, AccountWorkspace{ScopeType: "system", ScopeID: 1, Name: "System"})
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Where("principal_id = ? AND tenant_id = 9", account.ID).Update("status", "disabled").Error)
	items, err = h.service.AccountWorkspaces(t.Context(), account.ID)
	require.NoError(t, err)
	for _, item := range items {
		require.NotEqual(t, "platform", item.ScopeType)
	}
}

type systemFileWorkspaceChecker struct{ identityRouteAuthorizer }

func (systemFileWorkspaceChecker) CheckPlatform(_ context.Context, permission string, _ guid.ID) (bool, error) {
	return permission == "system_file_view", nil
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
	var page respx.Envelope[[]PrincipalView]
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &page))
	require.EqualValues(t, 1, page.Meta.Page.Total)
	require.Len(t, page.Data, 1)
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

func TestGlobalAccountCategoriesOverlapAndPaginateBeforeProjection(t *testing.T) {
	h := newIdentityHTTPHarnessWithPlatform(t, true, true)
	require.NoError(t, h.db.Exec("ALTER TABLE sl_iam_tenants ADD COLUMN scope_kind TEXT NOT NULL DEFAULT 'tenant'").Error)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants (id, status, scope_kind) VALUES (9, 'active', 'platform'), (10, 'active', 'system')").Error)
	a, err := h.service.Create(t.Context(), 7, "a.shared", testPassword, "Shared", "")
	require.NoError(t, err)
	b, err := h.service.CreateGlobal(t.Context(), "b.personal", testPassword, "Personal", "")
	require.NoError(t, err)
	for _, owner := range []guid.ID{9, 10} {
		require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Create(map[string]any{
			"tenant_id": owner, "principal_id": a.ID, "display_name": "Shared", "email": "", "status": "disabled", "created_at": identityTestNow, "updated_at": identityTestNow,
		}).Error)
	}
	require.NoError(t, h.db.Exec("CREATE TABLE sl_iam_platform_administrators (principal_id BIGINT PRIMARY KEY, created_at BIGINT NOT NULL)").Error)
	require.NoError(t, h.db.Table(h.table("iam_platform_administrators")).Create(map[string]any{"principal_id": b.ID, "created_at": identityTestNow}).Error)
	for _, category := range []string{"system", "platform", "tenant"} {
		items, total, err := h.service.ListGlobalCategory(t.Context(), "", 1, 0, category, 0, "")
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, a.ID, items[0].ID)
		if category == "system" {
			require.EqualValues(t, 2, total)
		} else {
			require.EqualValues(t, 1, total)
		}
	}
	items, total, err := h.service.ListGlobalCategory(t.Context(), "", 1, 1, "system", 0, "")
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Equal(t, b.ID, items[0].ID)
	items, total, err = h.service.ListGlobalCategory(t.Context(), "", 20, 0, "platform", 7, "")
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, items)
	_, _, err = h.service.ListGlobalCategory(t.Context(), "", 20, 0, "system", 7, "")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestListGlobalListsHumanAccountsAcrossTenantsWithBoundedPages(t *testing.T) {
	h := newIdentityHarness(t, "sl_")
	tenantMember, err := h.service.Create(t.Context(), 7, "alpha.member", testPassword, "Alpha Member", "")
	require.NoError(t, err)
	global, err := h.service.CreateGlobal(t.Context(), "beta.global", testPassword, "Beta Global", "")
	require.NoError(t, err)
	_, err = h.service.CreateGlobal(t.Context(), "gamma.global", testPassword, "Gamma Global", "")
	require.NoError(t, err)

	items, total, err := h.service.ListGlobal(t.Context(), "", 2, 0)
	require.NoError(t, err)
	require.EqualValues(t, 3, total, "a tenant-created account is still a global identity")
	require.Len(t, items, 2)
	rest, restTotal, err := h.service.ListGlobal(t.Context(), "", 2, 2)
	require.NoError(t, err)
	require.EqualValues(t, 3, restTotal)
	require.Len(t, rest, 1)
	seen := map[guid.ID]bool{}
	for _, item := range append(items, rest...) {
		require.False(t, seen[item.ID], "an account appeared on two pages")
		seen[item.ID] = true
	}
	require.True(t, seen[tenantMember.ID])
	require.True(t, seen[global.ID])
	beyond, beyondTotal, err := h.service.ListGlobal(t.Context(), "", 2, 3)
	require.NoError(t, err)
	require.Empty(t, beyond, "an offset past the end is an empty page, not an error")
	require.EqualValues(t, 3, beyondTotal)

	found, foundTotal, err := h.service.ListGlobal(t.Context(), "BETA", 200, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, foundTotal)
	require.Len(t, found, 1)
	require.Equal(t, global.ID, found[0].ID)

	for name, call := range map[string]func() error{
		"zero limit":      func() error { _, _, err := h.service.ListGlobal(t.Context(), "", 0, 0); return err },
		"oversized limit": func() error { _, _, err := h.service.ListGlobal(t.Context(), "", 201, 0); return err },
		"negative offset": func() error { _, _, err := h.service.ListGlobal(t.Context(), "", 1, -1); return err },
		"long search": func() error {
			_, _, err := h.service.ListGlobal(t.Context(), strings.Repeat("s", 201), 1, 0)
			return err
		},
		"unknown category": func() error {
			_, _, err := h.service.ListGlobalCategory(t.Context(), "", 1, 0, "robot", 0, "")
			return err
		},
		"negative owner": func() error {
			_, _, err := h.service.ListGlobalCategory(t.Context(), "", 1, 0, "tenant", -1, "")
			return err
		},
		"unknown status": func() error {
			_, _, err := h.service.ListGlobalCategory(t.Context(), "", 1, 0, "user", 0, "archived")
			return err
		},
	} {
		require.ErrorIs(t, call(), ErrInvalid, name)
	}

	// An empty category is the human-account default, and status narrows it.
	require.NoError(t, h.db.Table(h.table("iam_principals")).Where("id = ?", global.ID).Update("status", "disabled").Error)
	active, activeTotal, err := h.service.ListGlobalCategory(t.Context(), "", 200, 0, "", 0, "active")
	require.NoError(t, err)
	require.EqualValues(t, 2, activeTotal)
	for _, item := range active {
		require.NotEqual(t, global.ID, item.ID)
	}
	disabled, disabledTotal, err := h.service.ListGlobalCategory(t.Context(), "", 200, 0, "user", 0, "disabled")
	require.NoError(t, err)
	require.EqualValues(t, 1, disabledTotal)
	require.Equal(t, global.ID, disabled[0].ID)

	require.NoError(t, h.db.Migrator().DropTable(h.table("iam_principals")))
	_, _, err = h.service.ListGlobal(t.Context(), "", 1, 0)
	require.Error(t, err, "a storage failure is reported, not answered as an empty list")
}

func TestGlobalAccountLookupsRefuseMissingAndUnusableTargets(t *testing.T) {
	h := newIdentityHarness(t, "sl_")
	_, err := h.service.GetGlobal(t.Context(), 0)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = h.service.GetGlobal(t.Context(), 424242)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = h.service.AccountWorkspaces(t.Context(), 424242)
	require.ErrorIs(t, err, ErrNotFound)

	account, err := h.service.CreateGlobal(t.Context(), "workspace.target", testPassword, "Workspace Target", "")
	require.NoError(t, err)
	h.service.avatarPermissions = nil
	_, err = h.service.AccountWorkspaces(t.Context(), account.ID)
	require.ErrorIs(t, err, ErrUnavailable, "without platform grants the system workspace cannot be decided")

	unreadable := errors.New("platform grants unreadable")
	h.service.avatarPermissions = failingWorkspaceChecker{err: unreadable}
	_, err = h.service.AccountWorkspaces(t.Context(), account.ID)
	require.ErrorIs(t, err, unreadable)

	// A disabled account keeps only its personal workspace, and that answer
	// needs no grant lookup at all.
	require.NoError(t, h.db.Table(h.table("iam_principals")).Where("id = ?", account.ID).Update("status", "disabled").Error)
	items, err := h.service.AccountWorkspaces(t.Context(), account.ID)
	require.NoError(t, err)
	require.Equal(t, []AccountWorkspace{{ScopeType: "personal", ScopeID: account.ID, Name: "Workspace Target"}}, items)
}

type failingWorkspaceChecker struct {
	identityRouteAuthorizer
	err error
}

func (c failingWorkspaceChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return false, c.err
}

func TestSystemAccountHTTPRejectsMalformedAndUnknownTargets(t *testing.T) {
	h := newIdentityHTTPHarnessWithPlatform(t, true, true)
	auth := "Authorization: Bearer session"
	account, err := h.service.CreateGlobal(t.Context(), "known.account", testPassword, "Known", "")
	require.NoError(t, err)

	for name, response := range map[string]int{
		"workspaces bad id": h.api.Get("/system/principals/not-an-id/workspaces", auth).Code,
		"get bad id":        h.api.Get("/system/principals/not-an-id", auth).Code,
		"patch bad id":      h.api.Patch("/system/principals/not-an-id", auth, map[string]any{"display_name": "X"}).Code,
		"list bad owner":    h.api.Get("/system/principals?category=tenant&owner_id=not-an-id", auth).Code,
		"owner on users":    h.api.Get("/system/principals?category=user&owner_id=7", auth).Code,
	} {
		require.Equal(t, http.StatusUnprocessableEntity, response, name)
	}
	for name, response := range map[string]int{
		"workspaces unknown": h.api.Get("/system/principals/424242/workspaces", auth).Code,
		"get unknown":        h.api.Get("/system/principals/424242", auth).Code,
	} {
		require.Equal(t, http.StatusNotFound, response, name)
	}

	require.NoError(t, h.db.Exec("ALTER TABLE sl_iam_tenants ADD COLUMN scope_kind TEXT NOT NULL DEFAULT 'tenant'").Error)
	member, err := h.service.Create(t.Context(), 7, "tenant.member", testPassword, "Tenant Member", "")
	require.NoError(t, err)
	owned := h.api.Get("/system/principals?category=tenant&owner_id=7&limit=5", auth)
	require.Equal(t, http.StatusOK, owned.Code, owned.Body.String())
	var page respx.Envelope[[]PrincipalView]
	require.NoError(t, json.Unmarshal(owned.Body.Bytes(), &page))
	require.EqualValues(t, 1, page.Meta.Page.Total, "only tenant 7's member is listed; the global account belongs to no tenant")
	require.Len(t, page.Data, 1)
	require.Equal(t, member.ID, page.Data[0].ID)

	duplicate := h.api.Post("/system/principals", auth, map[string]any{"login_name": "known.account", "password": testPassword})
	require.Equal(t, http.StatusConflict, duplicate.Code, duplicate.Body.String())
	after, err := h.service.GetGlobal(t.Context(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "Known", after.DisplayName, "a refused duplicate left the existing account unchanged")
	require.EqualValues(t, 2, h.principalCount(t), "the refused duplicate created no account")
}
