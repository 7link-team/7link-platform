package core

import (
	"context"
	"fmt"
	"strings"

	"7link.com/platform/internal/iam/audit"
	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// SystemRootSlug cannot be supplied through ordinary organization creation.
const SystemRootSlug = "__system__"

func SystemDefaultMenus() []Menu {
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
		menus[i].Translations = corei18n.Translations(key)
	}
	return menus
}

func (s *Service) SystemMenuCatalog(context.Context) []MenuDeclaration {
	defaults := SystemDefaultMenus()
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
	var root struct{ ID guid.ID }
	err = repo.writer().WithContext(ctx).Table(tables.Tenants.String()).Select("id").Where("slug = ? AND scope_kind = ? AND status = ?", SystemRootSlug, "system", "active").Take(&root).Error
	if err != nil {
		return nil, 0, fmt.Errorf("resolve system menu owner: %w", err)
	}
	clone := *s
	clone.writes = newTransactionCoordinator(s.repo, func(ctx context.Context, db *gorm.DB, event audit.DomainEvent) error {
		event.TenantID = 0
		if event.Detail == nil {
			event.Detail = map[string]any{}
		}
		event.Detail["scope_kind"], event.Detail["scope_id"] = "system", root.ID.String()
		return s.writes.audit(ctx, db, event)
	})
	return &clone, root.ID, nil
}
