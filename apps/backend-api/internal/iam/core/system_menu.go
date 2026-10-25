package core

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"7link.com/platform/internal/iam/audit"
	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// SystemRootSlug cannot be supplied through ordinary organization creation.
const SystemRootSlug = "__system__"

func legacySystemDefaultMenus() []Menu {
	menus := []Menu{
		{ID: 1, Label: "Organization", Icon: "building-2", SortOrder: 10, Status: MenuActive},
		{ParentID: 1, Label: "Global accounts", Route: "/system/accounts", Icon: "users", SortOrder: 10, PermissionCode: "system_account_view", Status: MenuActive},
		{ParentID: 1, Label: "Operating platforms", Route: "/system/operating-platforms", Icon: "network", SortOrder: 20, PermissionCode: "operating_platform_view", Status: MenuActive},
		{ParentID: 1, Label: "Tenants", Route: "/system/tenants", Icon: "building-2", SortOrder: 30, PermissionCode: "tenant_view", Status: MenuActive},
		{ParentID: 1, Label: "Closure requests", Route: "/system/closure-requests", Icon: "clipboard-check", SortOrder: 40, PermissionCode: "platform_administer", Status: MenuActive},
		{ID: 2, Label: "Access", Icon: "key-round", SortOrder: 20, Status: MenuActive},
		{ParentID: 2, Label: "System access grants", Route: "/system/administrators", Icon: "shield-check", SortOrder: 10, PermissionCode: "platform_administer", Status: MenuActive},
		{ParentID: 2, Label: "Menus", Route: "/system/menus", Icon: "boxes", SortOrder: 20, PermissionCode: "system_menu_view", Status: MenuActive},
		{ID: 3, Label: "Audit", Icon: "file-clock", SortOrder: 30, Status: MenuActive},
		{ParentID: 3, Label: "Audit events", Route: "/system/audit/events", Icon: "file-clock", SortOrder: 10, PermissionCode: "system_audit_event_view", Status: MenuActive},
		{ParentID: 3, Label: "Audit governance", Route: "/system/audit-governance", Icon: "shield-check", SortOrder: 20, PermissionCode: "platform_administer", Status: MenuActive},
	}
	for i := range menus {
		key := "menu_system_" + strings.NewReplacer("-", "_", "/", "_").Replace(strings.TrimPrefix(menus[i].Route, "/system/"))
		if menus[i].Route == "" {
			key = "menu_system_" + strings.ToLower(menus[i].Label)
		}
		if menus[i].Route == "/system/accounts" {
			key = "menu_system_legacy_accounts"
		}
		menus[i].Translations = corei18n.Translations(key)
	}
	return menus
}

// SystemMenuBasicManagement labels the default system branch that holds
// platform-wide basic pages; a module contributing such a page names it.
const SystemMenuBasicManagement = "Basic management"

// SystemDefaultMenus is the system menu core owns. The menu a deployment seeds
// is SystemMenus.Defaults: these pages plus the ones other modules contribute.
func SystemDefaultMenus() []Menu {
	menus := []Menu{
		{ID: 1, Label: "Account management", Icon: "users", SortOrder: 10, Status: MenuActive},
		{ParentID: 1, Label: "System accounts", Route: "/system/accounts/system", Icon: "shield-check", SortOrder: 10, PermissionCode: "system_account_view", Status: MenuActive},
		{ParentID: 1, Label: "Platform accounts", Route: "/system/accounts/platform", Icon: "network", SortOrder: 20, PermissionCode: "system_account_view", Status: MenuActive},
		{ParentID: 1, Label: "Tenant accounts", Route: "/system/accounts/tenant", Icon: "building-2", SortOrder: 30, PermissionCode: "system_account_view", Status: MenuActive},
		{ParentID: 1, Label: "User accounts", Route: "/system/accounts", Icon: "users", SortOrder: 40, PermissionCode: "system_account_view", Status: MenuActive},
		{ParentID: 1, Label: "Closure requests", Route: "/system/closure-requests", Icon: "clipboard-check", SortOrder: 50, PermissionCode: "platform_administer", Status: MenuActive},
		{ID: 2, Label: "Permission management", Icon: "key-round", SortOrder: 20, Status: MenuActive},
		{ParentID: 2, Label: "Departments", Route: "/system/departments", Icon: "building-2", SortOrder: 10, PermissionCode: "system_department_view", Status: MenuActive},
		{ParentID: 2, Label: "Roles and permissions", Route: "/system/roles", Icon: "key-round", SortOrder: 20, PermissionCode: "system_role_view", Status: MenuActive},
		{ID: 3, Label: SystemMenuBasicManagement, Icon: "boxes", SortOrder: 30, Status: MenuActive},
		{ParentID: 3, Label: "Menus", Route: "/system/menus", Icon: "boxes", SortOrder: 10, PermissionCode: "system_menu_view", Status: MenuActive},
		{ID: 4, Label: "Organization management", Icon: "network", SortOrder: 40, Status: MenuActive},
		{ParentID: 4, Label: "Operating platforms", Route: "/system/operating-platforms", Icon: "network", SortOrder: 10, PermissionCode: "operating_platform_view", Status: MenuActive},
		{ParentID: 4, Label: "Tenants", Route: "/system/tenants", Icon: "building-2", SortOrder: 20, PermissionCode: "tenant_view", Status: MenuActive},
		{ID: 5, Label: "Audit", Icon: "file-clock", SortOrder: 50, Status: MenuActive},
		{ParentID: 5, Label: "Audit events", Route: "/system/audit/events", Icon: "file-clock", SortOrder: 10, PermissionCode: "system_audit_event_view", Status: MenuActive},
		{ParentID: 5, Label: "Audit governance", Route: "/system/audit-governance", Icon: "shield-check", SortOrder: 20, PermissionCode: "platform_administer", Status: MenuActive},
	}
	for i := range menus {
		key := "menu_system_" + strings.NewReplacer("-", "_", "/", "_", " ", "_").Replace(strings.TrimPrefix(menus[i].Route, "/system/"))
		if menus[i].Route == "" {
			key = "menu_system_" + strings.ReplaceAll(strings.ToLower(menus[i].Label), " ", "_")
		}
		menus[i].Translations = corei18n.Translations(key)
	}
	return menus
}

// SystemMenuPage is a system menu page a module outside core owns. Branch is
// the label of the default branch it sits under; Menu carries the page itself
// (Label, Translations, Route under /system/, Icon, SortOrder, PermissionCode).
type SystemMenuPage struct {
	Branch string
	Menu   Menu
}

// SystemMenus is the default system menu a deployment seeds: the pages core
// owns plus the pages other modules contribute. Core publishes it; a module
// that owns a system page contributes it during InjectServices, the phase in
// which it also contributes that page's permission to the shared registry, so
// IAM core never names another module's page or permission code.
type SystemMenus struct {
	mu          sync.RWMutex
	contributed []SystemMenuPage
}

// Contribute adds pages all-or-nothing: each must name a default branch, an
// active /system/ route no other page uses, and the permission that gates it.
func (m *SystemMenus) Contribute(pages ...SystemMenuPage) error {
	if m == nil {
		return fmt.Errorf("%w: system menu contribution requires a catalog", ErrInvalidArgument)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	routes := map[string]bool{}
	branches := map[string]bool{}
	for _, menu := range SystemDefaultMenus() {
		routes[menu.Route] = menu.Route != ""
		if menu.ParentID.Zero() {
			branches[menu.Label] = true
		}
	}
	for _, page := range m.contributed {
		routes[page.Menu.Route] = true
	}
	for _, page := range pages {
		menu := page.Menu
		if !branches[page.Branch] || !strings.HasPrefix(menu.Route, "/system/") || routes[menu.Route] ||
			menu.Label == "" || menu.PermissionCode == "" || menu.Status != MenuActive || !menu.ID.Zero() || !menu.ParentID.Zero() {
			return fmt.Errorf("%w: invalid system menu contribution %q", ErrInvalidArgument, menu.Route)
		}
		routes[menu.Route] = true
	}
	m.contributed = append(m.contributed, pages...)
	return nil
}

// Defaults returns SystemDefaultMenus with each contributed page placed after
// the last page of its branch, in contribution order.
func (m *SystemMenus) Defaults() []Menu {
	menus := SystemDefaultMenus()
	if m == nil {
		return menus
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, page := range m.contributed {
		at := len(menus)
		for index, menu := range menus {
			if menu.ParentID.Zero() && menu.Label == page.Branch {
				page.Menu.ParentID, at = menu.ID, index+1
			} else if !page.Menu.ParentID.Zero() && menu.ParentID == page.Menu.ParentID {
				at = index + 1
			}
		}
		page.Menu.Translations = maps.Clone(page.Menu.Translations)
		menus = slices.Insert(menus, at, page.Menu)
	}
	return menus
}

// A system root seeded between 211e2d4 and e36c727 named the organization
// branch "Operations management". The upgrade gives it the current label, and
// the current translations when it still carries the ones seeded then; the
// locale key they came from no longer exists, so they are recorded here.
const retiredOrganizationBranch = "Operations management"

var retiredOrganizationBranchTranslations = map[string]string{
	"en-US": "Operations management", "zh-CN": "杩愯惀绠＄悊", "ms-MY": "Pengurusan operasi",
}

// systemMenuDefaults is the system menu this repository seeds and upgrades to.
func (r *Repository) systemMenuDefaults() []Menu {
	if r == nil {
		return SystemDefaultMenus()
	}
	return r.systemMenus.Defaults()
}

func (r *Repository) upgradeSystemMenus(ctx context.Context, owner guid.ID, _ time.Time) (bool, error) {
	stored, err := r.ListMenus(ctx, owner, false)
	if err != nil {
		return false, err
	}
	accountBranch, permissionBranch, localized := false, false, false
	organizationBranch := false
	for _, menu := range stored {
		if menu.Route == "" && menu.ParentID.Zero() {
			accountBranch = accountBranch || menu.Label == "Account management"
			permissionBranch = permissionBranch || menu.Label == "Permission management"
			organizationBranch = organizationBranch || menu.Label == "Organization management"
		}
	}
	if !organizationBranch {
		for i, menu := range stored {
			if menu.Route != "" || !menu.ParentID.Zero() || menu.Label != retiredOrganizationBranch {
				continue
			}
			menu.Label = "Organization management"
			if reflect.DeepEqual(menu.Translations, retiredOrganizationBranchTranslations) {
				menu.Translations = corei18n.Translations("menu_system_organization_management")
			}
			updated, err := r.UpdateMenu(ctx, menu)
			if err != nil {
				return false, err
			}
			stored[i], localized = updated, true
			break
		}
	}
	if accountBranch && permissionBranch {
		for i, menu := range stored {
			if menu.Route == "/system/accounts" && menu.Label == "User accounts" && reflect.DeepEqual(menu.Translations, corei18n.Translations("menu_system_legacy_accounts")) {
				menu.Translations = corei18n.Translations("menu_system_accounts")
				updated, err := r.UpdateMenu(ctx, menu)
				if err != nil {
					return false, err
				}
				stored[i], localized = updated, true
			}
		}
	}
	var oldAccess Menu
	var accessParent guid.ID
	for _, menu := range legacySystemDefaultMenus() {
		if menu.Route == "/system/administrators" {
			oldAccess = menu
		}
	}
	for _, menu := range stored {
		if menu.Route == "" && menu.ParentID.Zero() && menu.Label == "Access" {
			accessParent = menu.ID
			break
		}
	}
	pristineLegacyAccess := func(menu Menu) bool {
		return !accessParent.Zero() && menu.ParentID == accessParent && menu.Route == oldAccess.Route && menu.Label == oldAccess.Label && menu.Icon == oldAccess.Icon && menu.SortOrder == oldAccess.SortOrder && menu.PermissionCode == oldAccess.PermissionCode && menu.Status == oldAccess.Status && reflect.DeepEqual(menu.Translations, oldAccess.Translations)
	}
	legacy := false
	for _, menu := range stored {
		if menu.Route == "" && (menu.Label == "Organization" || menu.Label == "Access") {
			legacy = true
		}
		if pristineLegacyAccess(menu) {
			legacy = true
		}
	}
	if !legacy {
		return localized, nil
	}
	defaults := r.unversionedSystemMenuDefaults()
	parents := map[guid.ID]guid.ID{}
	oldDefaults := map[string]Menu{}
	oldBranches := map[string]Menu{}
	oldParents := map[guid.ID]guid.ID{}
	for _, menu := range legacySystemDefaultMenus() {
		if menu.Route != "" {
			oldDefaults[menu.Route] = menu
		} else {
			oldBranches[menu.Label] = menu
			for _, storedMenu := range stored {
				if storedMenu.Route == "" && storedMenu.ParentID.Zero() && storedMenu.Label == menu.Label {
					oldParents[menu.ID] = storedMenu.ID
					break
				}
			}
		}
	}
	for _, branch := range defaults {
		if !branch.ParentID.Zero() {
			continue
		}
		oldLabel := branch.Label
		if branch.ID == 1 {
			oldLabel = "Organization"
		}
		if branch.ID == 2 {
			oldLabel = "Access"
		}
		var match *Menu
		for i := range stored {
			if stored[i].Route == "" && stored[i].ParentID.Zero() && (stored[i].Label == branch.Label || stored[i].Label == oldLabel) {
				match = &stored[i]
				break
			}
		}
		if match != nil {
			updated := *match
			old, wasDefault := oldBranches[oldLabel]
			if wasDefault {
				updated.Label = branch.Label
				if reflect.DeepEqual(updated.Translations, old.Translations) {
					updated.Translations = branch.Translations
				}
				if updated.Icon == old.Icon {
					updated.Icon = branch.Icon
				}
				if updated.SortOrder == old.SortOrder {
					updated.SortOrder = branch.SortOrder
				}
			}
			if _, err := r.UpdateMenu(ctx, updated); err != nil {
				return false, err
			}
			parents[branch.ID] = updated.ID
		} else {
			branch.ID = 0
			branch.TenantID = owner
			created, err := r.CreateMenu(ctx, branch)
			if err != nil {
				return false, err
			}
			parents[defaultsBranchID(defaults, branch.Label)] = created.ID
		}
	}
	for _, leaf := range defaults {
		if leaf.ParentID.Zero() {
			continue
		}
		var match *Menu
		for i := range stored {
			if stored[i].Route == leaf.Route {
				match = &stored[i]
				break
			}
		}
		if match != nil {
			old, wasDefault := oldDefaults[match.Route]
			if wasDefault && match.Label == old.Label && match.PermissionCode == old.PermissionCode && match.Status == MenuActive {
				updated := *match
				updated.Label = leaf.Label
				if originalParent := oldParents[old.ParentID]; !originalParent.Zero() && updated.ParentID == originalParent {
					updated.ParentID = parents[leaf.ParentID]
				}
				if reflect.DeepEqual(updated.Translations, old.Translations) {
					updated.Translations = leaf.Translations
				}
				if updated.SortOrder == old.SortOrder {
					updated.SortOrder = leaf.SortOrder
				}
				if _, err := r.UpdateMenu(ctx, updated); err != nil {
					return false, err
				}
			}
			continue
		}
		leaf.TenantID = owner
		leaf.ParentID = parents[leaf.ParentID]
		if _, err := r.CreateMenu(ctx, leaf); err != nil {
			return false, err
		}
	}
	for _, menu := range stored {
		if !pristineLegacyAccess(menu) {
			continue
		}
		hasChildren := false
		for _, child := range stored {
			if child.ParentID == menu.ID {
				hasChildren = true
			}
		}
		if !hasChildren {
			if err := r.DeleteMenu(ctx, owner, menu.ID); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

func defaultsBranchID(defaults []Menu, label string) guid.ID {
	for _, menu := range defaults {
		if menu.ParentID.Zero() && menu.Label == label {
			return menu.ID
		}
	}
	return 0
}

func (s *Service) SystemMenuCatalog(context.Context) []MenuDeclaration {
	defaults := s.repo.systemMenuDefaults()
	var result []MenuDeclaration
	for _, menu := range defaults {
		if !menu.ParentID.Zero() {
			continue
		}
		branch := MenuDeclaration{Key: menu.Label, Label: menu.Label, Translations: menu.Translations, Icon: menu.Icon, SortOrder: menu.SortOrder}
		for _, leaf := range defaults {
			if leaf.ParentID != menu.ID {
				continue
			}
			branch.Children = append(branch.Children, MenuDeclaration{Key: leaf.Route, Label: leaf.Label, Translations: leaf.Translations, Path: leaf.Route, PermissionCode: leaf.PermissionCode, Icon: leaf.Icon, SortOrder: leaf.SortOrder})
		}
		result = append(result, branch)
	}
	return result
}

func (s *Service) systemMenuService(ctx context.Context) (*Service, guid.ID, error) {
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	tables, err := repo.directoryTables()
	if err != nil {
		return nil, 0, err
	}
	owner, err := systemRootID(ctx, repo.writer(), tables.Tenants.String())
	if err != nil {
		return nil, 0, fmt.Errorf("resolve system menu owner: %w", err)
	}
	clone := *s
	clone.writes = newTransactionCoordinator(s.repo, func(ctx context.Context, db *gorm.DB, event audit.DomainEvent) error {
		event.TenantID = 0
		if event.Detail == nil {
			event.Detail = map[string]any{}
		}
		event.Detail["scope_kind"], event.Detail["scope_id"] = "system", owner.String()
		return s.writes.audit(ctx, db, event)
	})
	return &clone, owner, nil
}
