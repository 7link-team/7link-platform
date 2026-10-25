package core

import (
	"errors"
	"testing"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func menuTestPage(key, route string) MenuPage {
	return MenuPage{Key: key, Scope: "TENANT", App: "delivery", Domain: "TEAM", Space: "TEAM", Route: route, PermissionCode: "menu_view", TranslationKey: "test.menu", Version: 1, Translations: map[string]string{"en-US": key, "zh-CN": "鑿滃崟" + key, "ms-MY": "Menu " + key}}
}

func TestMenuContributionsValidateAtomicDeclarations(t *testing.T) {
	valid := menuTestPage("one", "/t/:tenantID/one")
	for _, field := range []string{"owner", "key", "app", "scope", "permission", "permission-scope", "route", "duplicate", "domain", "space", "project-route", "project-domain", "team-route", "translation", "version", "group", "icon"} {
		t.Run(field, func(t *testing.T) {
			c := &menuContributions{registry: authz.DefaultRegistry()}
			p := valid
			owner := "test"
			switch field {
			case "owner":
				owner = "bad.owner"
			case "key":
				p.Key = "../key"
			case "app":
				p.App = "invalid/app"
			case "scope":
				p.Scope = "MERCHANT"
			case "permission":
				p.PermissionCode = "unregistered"
			case "permission-scope":
				p.Scope = "SYSTEM"
			case "route":
				p.Route = "//external.test/path"
			case "duplicate":
				p.Key = "two"
			case "domain":
				p.Domain = "COLLAB"
			case "space":
				p.Space = ""
			case "project-route":
				p.Space = "PROJECT"
				p.Domain = "PROJECTS"
			case "project-domain":
				p.Space = "PROJECT"
				p.Route = "/t/:tenantID/p/:projectID/page"
			case "team-route":
				p.Route = "/t/:tenantID/p/:projectID/page"
			case "translation":
				p.TranslationKey = "other.menu"
			case "version":
				p.Version = 0
			case "group":
				p.Group = "absent"
			case "icon":
				p.Icon = string(make([]byte, 65))
			}
			err := c.ContributeMenus(owner, valid, p)
			require.ErrorIs(t, err, ErrInvalidArgument)
			require.Empty(t, c.snapshot())
			require.Contains(t, err.Error(), owner)
		})
	}
	c := &menuContributions{registry: authz.DefaultRegistry()}
	require.NoError(t, c.ContributeMenus("test", valid))
	valid.Translations["en-US"] = "mutated caller"
	require.Equal(t, "one", c.snapshot()[0].Translations["en-US"])
	other := menuTestPage("two", "/t/:tenantID/one")
	other.App = "other"
	other.Domain = ""
	other.Space = ""
	require.NoError(t, c.ContributeMenus("test", other), "route uniqueness is scoped by app")
}

func TestMenuContributionsSeedUpgradeCustomizeDeleteRemoveAndRollback(t *testing.T) {
	h := newRepositoryHarness(t, "menuledger_")
	r := h.repository
	seedTenantAndPrincipal(t, h.db, "menuledger_", 1, 11)
	pages := []MenuPage{menuTestPage("one", "/t/:tenantID/one"), menuTestPage("two", "/t/:tenantID/two"), menuTestPage("three", "/t/:tenantID/three")}
	r.contributions = &menuContributions{registry: authz.DefaultRegistry()}
	require.NoError(t, r.contributions.ContributeMenus("test", pages...))
	var result TenantBootstrapResult
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = r.BootstrapTenantOn(t.Context(), tx, TenantBootstrapInput{TenantID: 1, OwnerID: 11, DisplayName: "Owner"}, repositoryTestTime)
		return err
	}))
	require.Equal(t, len(DefaultMenus())+3, result.MenuCount)
	q := query.BindWithNames(h.db, r.names)
	ledgers, err := q.MenuContributions.WithContext(t.Context()).Order(q.MenuContributions.PageKey).Find()
	require.NoError(t, err)
	require.Len(t, ledgers, 3)
	for _, l := range ledgers {
		require.NotNil(t, l.MenuID)
		require.False(t, l.Customized)
		require.Len(t, l.SeededHash, 64)
	}
	var changed int
	syncNow := func() {
		t.Helper()
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			changed, err = r.withDB(tx).syncContributedMenus(t.Context(), 1, "tenant")
			return err
		}))
	}
	syncNow()
	require.Zero(t, changed)
	byKey := map[string]*model.MenuContribution{}
	for _, l := range ledgers {
		byKey[l.PageKey] = l
	}
	one, err := r.GetMenu(t.Context(), 1, *byKey["one"].MenuID)
	require.NoError(t, err)
	one.Label = "Administrator label"
	one.Domain = "COLLAB"
	_, err = r.UpdateMenu(t.Context(), one)
	require.NoError(t, err)
	require.NoError(t, r.DeleteMenu(t.Context(), 1, *byKey["two"].MenuID))
	for i := range pages {
		pages[i].Version = 2
		pages[i].Translations = map[string]string{"en-US": "Upgraded " + pages[i].Key, "zh-CN": "鏂扮増", "ms-MY": "Baharu"}
	}
	r.contributions = &menuContributions{registry: authz.DefaultRegistry()}
	require.NoError(t, r.contributions.ContributeMenus("test", pages...))
	syncNow()
	require.Positive(t, changed)
	one, err = r.GetMenu(t.Context(), 1, one.ID)
	require.NoError(t, err)
	require.Equal(t, "Administrator label", one.Label)
	require.Equal(t, "COLLAB", one.Domain)
	two, err := q.MenuContributions.WithContext(t.Context()).Where(q.MenuContributions.PageKey.Eq("two")).Take()
	require.NoError(t, err)
	require.Nil(t, two.MenuID)
	three, err := r.GetMenu(t.Context(), 1, *byKey["three"].MenuID)
	require.NoError(t, err)
	require.Equal(t, "Upgraded three", three.Label)
	syncNow()
	require.Zero(t, changed)
	// Removal disables but does not erase history. Repeated sync changes nothing.
	r.contributions = &menuContributions{registry: authz.DefaultRegistry()}
	require.NoError(t, r.contributions.ContributeMenus("test", pages[:2]...))
	syncNow()
	three, err = r.GetMenu(t.Context(), 1, three.ID)
	require.NoError(t, err)
	require.Equal(t, MenuDisabled, three.Status)
	syncNow()
	require.Zero(t, changed)
	// A later version cannot resurrect a deletion.
	pages[1].Version = 9
	r.contributions = &menuContributions{registry: authz.DefaultRegistry()}
	require.NoError(t, r.contributions.ContributeMenus("test", pages[:2]...))
	syncNow()
	_, err = r.GetMenu(t.Context(), 1, *byKey["two"].MenuID)
	require.ErrorIs(t, err, ErrMenuNotFound)
	seedTenantAndPrincipal(t, h.db, "menuledger_", 2, 22)
	sentinel := errors.New("outer transaction rejected")
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, e := r.BootstrapTenantOn(t.Context(), tx, TenantBootstrapInput{TenantID: 2, OwnerID: 22, DisplayName: "Other"}, repositoryTestTime)
		require.NoError(t, e)
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	n, err := q.MenuContributions.WithContext(t.Context()).Where(q.MenuContributions.TenantID.Eq(guid.ID(2))).Count()
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestMenuContributionsAdoptExistingWithoutOverwritingAndSeparateApps(t *testing.T) {
	h := newRepositoryHarness(t, "menuadopt_")
	r := h.repository
	seedTenantAndPrincipal(t, h.db, "menuadopt_", 1, 11)
	p := menuTestPage("one", "/same")
	existing, err := r.CreateMenu(t.Context(), Menu{TenantID: 1, App: "delivery", Label: "Custom before upgrade", Route: p.Route, Status: MenuActive})
	require.NoError(t, err)
	r.contributions = &menuContributions{registry: authz.DefaultRegistry()}
	require.NoError(t, r.contributions.ContributeMenus("test", p))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := r.withDB(tx).syncContributedMenus(t.Context(), 1, "tenant"); return e }))
	stored, err := r.GetMenu(t.Context(), 1, existing.ID)
	require.NoError(t, err)
	require.Equal(t, existing.Label, stored.Label)
	q := query.BindWithNames(h.db, r.names)
	l, err := q.MenuContributions.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, existing.ID, *l.MenuID)
	require.True(t, l.Customized)
	_, err = r.CreateMenu(t.Context(), Menu{TenantID: 1, App: "iam", Label: "Separate app", Route: p.Route, Status: MenuActive})
	require.NoError(t, err)
}

func TestMenuContributionsEffectiveCanonicalGateAndPlacement(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	p := menuTestPage("one", "/t/:tenantID/one")
	p.PermissionCode = "user_view"
	h.service.repo.contributions = &menuContributions{registry: h.service.registry}
	require.NoError(t, h.service.repo.contributions.ContributeMenus("test", p))
	menu, err := h.service.repo.CreateMenu(t.Context(), Menu{TenantID: 1, App: p.App, Domain: "COLLAB", Space: "", Label: "Customized", Route: p.Route, Status: MenuActive})
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.service.repo.withDB(tx).syncContributedMenus(t.Context(), 1, "tenant")
		return e
	}))
	tree, err := h.service.EffectiveMenus(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Empty(t, tree)
	role, err := h.service.CreateRole(t.Context(), 1, "User reader", "")
	require.NoError(t, err)
	_, err = h.service.GrantPermission(t.Context(), 1, role.ID, "user_view")
	require.NoError(t, err)
	_, err = h.service.AddMember(t.Context(), 1, role.ID, 11, nil)
	require.NoError(t, err)
	tree, err = h.service.EffectiveMenus(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Len(t, tree, 1)
	require.Equal(t, menu.ID, tree[0].ID)
	require.Equal(t, "TEAM", tree[0].Domain)
	require.Equal(t, "TEAM", tree[0].Space)
	h.service.repo.contributions = &menuContributions{registry: h.service.registry}
	tree, err = h.service.EffectiveMenus(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Empty(t, tree, "disabled module hidden before daily synchronization")
}

func TestMenuContributionsLegacyUpgradeCannotReseedVersionedPage(t *testing.T) {
	h := newRepositoryHarness(t, "menulegacy_")
	r := h.repository
	p := menuTestPage("system-files", "/system/files")
	p.Scope = "SYSTEM"
	p.App = "iam"
	p.Domain = ""
	p.Space = ""
	p.PermissionCode = "system_menu_view"
	p.Group = SystemMenuBasicManagement
	r.contributions = &menuContributions{registry: authz.DefaultRegistry()}
	require.NoError(t, r.contributions.ContributeMenus("test", p))
	for _, m := range r.unversionedSystemMenuDefaults() {
		require.NotEqual(t, "/system/files", m.Route)
	}
	found := false
	for _, m := range r.systemMenuDefaults() {
		found = found || m.Route == "/system/files"
	}
	require.True(t, found, "legacy declaration clients retain their catalog")
}
