package core

import (
	"context"
	"fmt"
	"net/mail"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/language"

	"7link.com/platform/internal/infra/guid"
)

// PutTenantMember creates or replaces one tenant-qualified membership and its
// optional primary department assignment.
func (s *Service) PutTenantMember(
	ctx context.Context,
	tenantID, principalID guid.ID,
	displayName, email string,
	departmentID guid.ID,
	status MemberStatus,
) (TenantMember, error) {
	if err := validateTenant(tenantID); err != nil {
		return TenantMember{}, err
	}
	displayName, email = strings.TrimSpace(displayName), strings.TrimSpace(email)
	if principalID.Zero() || displayName == "" || len(displayName) > 128 || len(email) > 320 ||
		(status != MemberActive && status != MemberDisabled) {
		return TenantMember{}, fmt.Errorf("%w: invalid tenant member", ErrInvalidArgument)
	}
	if email != "" {
		if parsed, err := mail.ParseAddress(email); err != nil || parsed.Address != email {
			return TenantMember{}, fmt.Errorf("%w: invalid member email", ErrInvalidArgument)
		}
	}
	var member TenantMember
	record := newAuditRecord(ctx, tenantID, "tenant_member_upserted", "tenant_member", principalID)
	record.Detail["status"] = status
	record.Detail["has_department"] = !departmentID.Zero()
	record.PolicyChanged = true
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		if !departmentID.Zero() {
			departmentStatus, err := repo.departmentStatus(ctx, tenantID, departmentID)
			if err != nil {
				return err
			}
			if departmentStatus != "active" {
				if departmentStatus == "" {
					return ErrMemberDepartmentMissing
				}
				return ErrMemberDepartmentInactive
			}
		}
		var verify func() error
		var err error
		if status == MemberDisabled {
			verify, err = s.administrators.Protect(ctx, repo.writer(), tenantID)
			if err != nil {
				return err
			}
		}
		member, err = repo.PutMember(ctx, TenantMember{
			TenantID: tenantID, PrincipalID: principalID,
			DisplayName: displayName, Email: email, Status: status,
		})
		if err != nil {
			return err
		}
		if err = repo.setMemberDepartment(ctx, tenantID, principalID, departmentID); err != nil {
			return err
		}
		if verify != nil {
			if err = verify(); err != nil {
				return err
			}
		}
		member, err = repo.GetMember(ctx, tenantID, principalID)
		return err
	})
	return member, err
}

// GetTenantMember returns one tenant-qualified membership.
func (s *Service) GetTenantMember(
	ctx context.Context,
	tenantID, principalID guid.ID,
) (TenantMember, error) {
	if err := validateMemberRef(tenantID, principalID); err != nil {
		return TenantMember{}, err
	}
	return s.repo.GetMember(ctx, tenantID, principalID)
}

// ListTenantMembers returns a filtered and paginated tenant membership page.
func (s *Service) ListTenantMembers(
	ctx context.Context,
	tenantID guid.ID,
	filter MemberFilter,
) ([]TenantMember, int64, error) {
	if err := validateTenant(tenantID); err != nil {
		return nil, 0, err
	}
	if filter.Offset < 0 || filter.Limit < 1 || filter.Limit > 200 ||
		(filter.Status != "" && filter.Status != MemberActive && filter.Status != MemberDisabled) ||
		len(filter.Search) > 128 {
		return nil, 0, fmt.Errorf("%w: invalid member filter", ErrInvalidArgument)
	}
	return s.repo.ListMembersPage(ctx, tenantID, filter)
}

// SetTenantMemberStatus changes a membership status while preserving the last
// durable tenant administrator.
func (s *Service) SetTenantMemberStatus(
	ctx context.Context,
	tenantID, principalID guid.ID,
	status MemberStatus,
) (TenantMember, error) {
	return s.setTenantMemberStatus(ctx, tenantID, principalID, status, nil)
}

func (s *Service) setTenantMemberStatus(ctx context.Context, tenantID, principalID guid.ID, status MemberStatus, prepare func(*Repository) error) (TenantMember, error) {
	if err := validateMemberRef(tenantID, principalID); err != nil {
		return TenantMember{}, err
	}
	if status != MemberActive && status != MemberDisabled {
		return TenantMember{}, fmt.Errorf("%w: invalid member status", ErrInvalidArgument)
	}
	var member TenantMember
	record := newAuditRecord(ctx, tenantID, "tenant_member_status_changed", "tenant_member", principalID)
	record.Detail["status"] = status
	record.PolicyChanged = true
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		if prepare != nil {
			if err := prepare(repo); err != nil {
				return err
			}
		}
		var verify func() error
		var err error
		if status == MemberDisabled {
			verify, err = s.administrators.Protect(ctx, repo.writer(), tenantID)
			if err != nil {
				return err
			}
		}
		member, err = repo.SetMemberStatus(ctx, tenantID, principalID, status)
		if err != nil || verify == nil {
			return err
		}
		return verify()
	})
	return member, err
}

// ListTenantMemberRoles returns permanent and currently active temporary role
// IDs for one tenant member.
func (s *Service) ListTenantMemberRoles(
	ctx context.Context,
	tenantID, principalID guid.ID,
) ([]guid.ID, error) {
	if err := validateMemberRef(tenantID, principalID); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetMember(ctx, tenantID, principalID); err != nil {
		return nil, err
	}
	return s.repo.ListMemberRoleIDs(ctx, tenantID, principalID)
}

// CreateMenu creates a normalized tenant navigation node.
func (s *Service) CreateMenu(ctx context.Context, menu Menu) (Menu, error) {
	menu.ID = 0
	var created Menu
	record := newAuditRecord(ctx, menu.TenantID, "menu_created", "menu", 0)
	record.PolicyChanged = true
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		if err := s.validateMenu(ctx, repo, &menu); err != nil {
			return err
		}
		var err error
		created, err = repo.CreateMenu(ctx, menu)
		record.TargetID = created.ID
		return err
	})
	return created, err
}

// ListMenus returns all active and disabled menu definitions for one tenant.
func (s *Service) ListMenus(ctx context.Context, tenantID guid.ID) ([]Menu, error) {
	if err := validateTenant(tenantID); err != nil {
		return nil, err
	}
	return s.repo.ListMenus(ctx, tenantID, false)
}

// GetMenu returns one tenant-qualified menu definition.
func (s *Service) GetMenu(ctx context.Context, tenantID, menuID guid.ID) (Menu, error) {
	if err := validateMenuRef(tenantID, menuID); err != nil {
		return Menu{}, err
	}
	return s.repo.GetMenu(ctx, tenantID, menuID)
}

// UpdateMenu replaces one normalized menu definition and rejects hierarchy
// cycles under the tenant policy lock.
func (s *Service) UpdateMenu(ctx context.Context, menu Menu) (Menu, error) {
	if err := validateMenuRef(menu.TenantID, menu.ID); err != nil {
		return Menu{}, err
	}
	var updated Menu
	record := newAuditRecord(ctx, menu.TenantID, "menu_updated", "menu", menu.ID)
	record.PolicyChanged = true
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		if err := repo.revisions.Lock(ctx, repo.writer(), menu.TenantID); err != nil {
			return err
		}
		if _, err := repo.GetMenu(ctx, menu.TenantID, menu.ID); err != nil {
			return err
		}
		if err := s.validateMenu(ctx, repo, &menu); err != nil {
			return err
		}
		if !menu.ParentID.Zero() {
			seen := map[guid.ID]bool{menu.ID: true}
			parentID := menu.ParentID
			for depth := 0; !parentID.Zero() && depth < 100; depth++ {
				if seen[parentID] {
					return fmt.Errorf("%w: menu hierarchy cycle", ErrInvalidArgument)
				}
				seen[parentID] = true
				parent, err := repo.GetMenu(ctx, menu.TenantID, parentID)
				if err != nil {
					return fmt.Errorf("%w: parent menu", ErrInvalidArgument)
				}
				parentID = parent.ParentID
			}
			if !parentID.Zero() {
				return fmt.Errorf("%w: menu hierarchy too deep", ErrInvalidArgument)
			}
		}
		var err error
		updated, err = repo.UpdateMenu(ctx, menu)
		return err
	})
	return updated, err
}

// DeleteMenu removes one tenant-qualified menu definition.
func (s *Service) DeleteMenu(ctx context.Context, tenantID, menuID guid.ID) error {
	if err := validateMenuRef(tenantID, menuID); err != nil {
		return err
	}
	record := newAuditRecord(ctx, tenantID, "menu_deleted", "menu", menuID)
	record.PolicyChanged = true
	return s.writes.Run(ctx, record, func(repo *Repository) error {
		return repo.DeleteMenu(ctx, tenantID, menuID)
	})
}

// EffectiveMenus returns the visible menu tree for one tenant principal after
// evaluating every referenced permission.
func (s *Service) EffectiveMenus(
	ctx context.Context,
	tenantID, principalID guid.ID,
) ([]MenuItem, error) {
	if err := validateMemberRef(tenantID, principalID); err != nil {
		return nil, err
	}
	menus, err := s.repo.ListMenus(ctx, tenantID, true)
	if err != nil {
		return nil, err
	}
	codeSet := map[string]struct{}{}
	routePermissions := map[string]string{}
	for _, menu := range DefaultMenus() {
		routePermissions[menu.Route] = menu.PermissionCode
	}
	pagePermissions := map[guid.ID]string{}
	for _, menu := range menus {
		// A custom label or permission cannot weaken a built-in page's gate.
		if parsed, err := url.Parse(menu.Route); err == nil && menu.Route != "" {
			route := path.Clean(parsed.Path)
			code := routePermissions[route]
			if strings.HasPrefix(route, "/entities/") {
				code = routePermissions["/entities"]
			}
			if code != "" {
				pagePermissions[menu.ID] = code
				codeSet[code] = struct{}{}
			}
		}
		if menu.PermissionCode != "" {
			if _, ok := s.registry.Find(menu.PermissionCode); !ok {
				return nil, fmt.Errorf("unknown persisted menu permission %q", menu.PermissionCode)
			}
			codeSet[menu.PermissionCode] = struct{}{}
		}
	}
	codes := make([]string, 0, len(codeSet))
	for code := range codeSet {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	allowed := map[string]bool{}
	for start := 0; start < len(codes); start += 100 {
		end := min(start+100, len(codes))
		result, err := s.checker.CheckBulk(ctx, tenantID, codes[start:end], principalID)
		if err != nil {
			return nil, fmt.Errorf("check effective menu permissions: %w", err)
		}
		for code, value := range result {
			allowed[code] = value
		}
	}
	children := map[guid.ID][]Menu{}
	for _, menu := range menus {
		children[menu.ParentID] = append(children[menu.ParentID], menu)
	}
	var build func(guid.ID, map[guid.ID]bool) []MenuItem
	build = func(parent guid.ID, visiting map[guid.ID]bool) []MenuItem {
		result := make([]MenuItem, 0, len(children[parent]))
		for _, menu := range children[parent] {
			if visiting[menu.ID] {
				continue
			}
			next := make(map[guid.ID]bool, len(visiting)+1)
			for id, value := range visiting {
				next[id] = value
			}
			next[menu.ID] = true
			nodes := build(menu.ID, next)
			accessible := (menu.PermissionCode == "" || allowed[menu.PermissionCode]) &&
				(pagePermissions[menu.ID] == "" || allowed[pagePermissions[menu.ID]])
			if !accessible && len(nodes) == 0 {
				continue
			}
			route := menu.Route
			if !accessible {
				route = ""
			}
			result = append(result, MenuItem{
				ID: menu.ID, Label: menu.Label, Path: route,
				Translations: menu.Translations,
				Icon:         menu.Icon, SortOrder: menu.SortOrder,
				PermissionCode: menu.PermissionCode, Children: nodes,
			})
		}
		return result
	}
	return build(0, map[guid.ID]bool{}), nil
}

// EffectivePermissions returns the declared permission codes the caller holds
// right now, so a console can leave out an action instead of offering one the
// server is going to refuse. It answers only for the caller: the codes come
// from the same evaluators the route guards use, so the reply cannot describe
// authority the request itself would not have.
//
// A zero tenant, or a tenant the caller is not an active member of, yields the
// platform codes alone -- CheckBulk already returns nothing for a non-member.
// Platform codes are evaluated separately because platform authority does not
// come from a membership, and a platform administrator with no tenant of their
// own still has to be able to see the platform actions.
func (s *Service) EffectivePermissions(
	ctx context.Context,
	tenantID, principalID guid.ID,
) ([]string, error) {
	if principalID.Zero() {
		return nil, fmt.Errorf("%w: invalid principal", ErrInvalidArgument)
	}
	tenantCodes, platformCodes := []string{}, []string{}
	for _, action := range s.registry.All() {
		if action.Scope == "platform" {
			platformCodes = append(platformCodes, action.Code)
			continue
		}
		tenantCodes = append(tenantCodes, action.Code)
	}
	sort.Strings(tenantCodes)
	sort.Strings(platformCodes)
	granted := make([]string, 0, len(tenantCodes)+len(platformCodes))
	if !tenantID.Zero() {
		for start := 0; start < len(tenantCodes); start += 100 {
			end := min(start+100, len(tenantCodes))
			result, err := s.checker.CheckBulk(ctx, tenantID, tenantCodes[start:end], principalID)
			if err != nil {
				return nil, fmt.Errorf("check effective tenant permissions: %w", err)
			}
			for _, code := range tenantCodes[start:end] {
				if result[code] {
					granted = append(granted, code)
				}
			}
		}
	}
	for _, code := range platformCodes {
		allowed, err := s.checker.CheckPlatform(ctx, code, principalID)
		if err != nil {
			return nil, fmt.Errorf("check effective platform permissions: %w", err)
		}
		if allowed {
			granted = append(granted, code)
		}
	}
	sort.Strings(granted)
	return granted, nil
}

func (s *Service) validateMenu(ctx context.Context, repo *Repository, menu *Menu) error {
	if menu == nil {
		return fmt.Errorf("%w: invalid menu", ErrInvalidArgument)
	}
	if err := validateTenant(menu.TenantID); err != nil {
		return err
	}
	menu.Label = strings.TrimSpace(menu.Label)
	if len(menu.Translations) > 32 {
		return fmt.Errorf("%w: too many menu translations", ErrInvalidArgument)
	}
	if menu.Translations != nil {
		normalized := make(map[string]string, len(menu.Translations))
		for locale, label := range menu.Translations {
			tag, err := language.Parse(locale)
			label = strings.TrimSpace(label)
			if err != nil || tag == language.Und || len(locale) > 63 || label == "" || utf8.RuneCountInString(label) > 128 {
				return fmt.Errorf("%w: invalid menu translation", ErrInvalidArgument)
			}
			if _, exists := normalized[tag.String()]; exists {
				return fmt.Errorf("%w: duplicate menu language", ErrInvalidArgument)
			}
			normalized[tag.String()] = label
		}
		menu.Translations = normalized
	}
	menu.Route = strings.TrimSpace(menu.Route)
	menu.Icon = strings.TrimSpace(menu.Icon)
	menu.PermissionCode = strings.TrimSpace(menu.PermissionCode)
	if menu.Label == "" || len(menu.Label) > 128 || len(menu.Route) > 512 || len(menu.Icon) > 64 ||
		menu.SortOrder < -100000 || menu.SortOrder > 100000 ||
		(menu.Status != MenuActive && menu.Status != MenuDisabled) {
		return fmt.Errorf("%w: invalid menu", ErrInvalidArgument)
	}
	if menu.Route != "" && (!strings.HasPrefix(menu.Route, "/") || strings.Contains(menu.Route, "//")) {
		return fmt.Errorf("%w: invalid menu route", ErrInvalidArgument)
	}
	if menu.PermissionCode != "" {
		if _, ok := s.registry.Find(menu.PermissionCode); !ok {
			return fmt.Errorf("%w: %s", ErrPermissionNotFound, menu.PermissionCode)
		}
	}
	if !menu.ParentID.Zero() {
		if _, err := repo.GetMenu(ctx, menu.TenantID, menu.ParentID); err != nil {
			return fmt.Errorf("%w: parent menu", ErrInvalidArgument)
		}
	}
	return nil
}

func validateMemberRef(tenantID, principalID guid.ID) error {
	if err := validateTenant(tenantID); err != nil {
		return err
	}
	if principalID.Zero() {
		return fmt.Errorf("%w: invalid member subject", ErrInvalidArgument)
	}
	return nil
}

func validateMenuRef(tenantID, menuID guid.ID) error {
	if err := validateTenant(tenantID); err != nil {
		return err
	}
	if menuID.Zero() {
		return fmt.Errorf("%w: invalid menu id", ErrInvalidArgument)
	}
	return nil
}
