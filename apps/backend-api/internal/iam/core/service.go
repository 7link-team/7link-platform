package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"7link.com/platform/internal/iam/audit"
	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/policyx"
)

// ScopeNode describes one level in the authorization scope model exposed to
// administrative clients.
type ScopeNode struct {
	Type       string `json:"type" doc:"authorization scope type"`
	ParentType string `json:"parent_type,omitempty" doc:"parent object type"`
	Relation   string `json:"relation" doc:"relation used to connect to parent or administer"`
	Label      string `json:"label" doc:"display label"`
}

// MenuItem is one visible node in the effective navigation tree.
type MenuItem struct {
	ID             guid.ID           `json:"id"`
	Label          string            `json:"label"`
	Translations   map[string]string `json:"translations,omitempty"`
	Path           string            `json:"path,omitempty"`
	PermissionCode string            `json:"permission_code"`
	Icon           string            `json:"icon,omitempty"`
	SortOrder      int               `json:"sort_order"`
	Children       []MenuItem        `json:"children,omitempty"`
}

// MenuDeclaration is a template key, not a persisted menu identity.
type MenuDeclaration struct {
	Key            string            `json:"key"`
	Label          string            `json:"label"`
	Translations   map[string]string `json:"translations,omitempty"`
	Path           string            `json:"path,omitempty"`
	PermissionCode string            `json:"permission_code"`
	Icon           string            `json:"icon,omitempty"`
	SortOrder      int               `json:"sort_order"`
	Children       []MenuDeclaration `json:"children,omitempty"`
}

// Service coordinates core IAM validation, persistence, authorization, policy
// revision updates, and audit appends.
type Service struct {
	registry             *authz.Registry
	repo                 *Repository
	checker              AuthorizationEvaluator
	writes               *transactionCoordinator
	administrators       AdministratorGuard
	privacyBrowser       authnext.BrowserSessionSecurity
	reauthenticate       authnext.ReauthenticationConsumer
	revokePersonalTokens TenantPersonalTokenRevoker
	revokeOAuth          TenantOAuthRevoker
	roots                FileRootProvisioner
	rootContent          FileRootContentChecker
}

// NewService constructs the core IAM service from its owned repository and
// narrow authorization and audit capabilities.
func NewService(
	registry *authz.Registry,
	repository *Repository,
	checker AuthorizationEvaluator,
	appender audit.Appender,
) *Service {
	if registry == nil || repository == nil || checker == nil || appender == nil {
		panic("iam service requires registry, repository, permission checker, and audit appender")
	}
	// The guard JOINs tables core does not own, so it shares the repository's
	// holder rather than starting with one of its own: the connectors that bind
	// the repository bind the guard with it, and an unbound guard reports
	// ErrUnavailable instead of reading a table it cannot name.
	guard := NewAdministratorGuard(repository.runtime)
	guard.foreign = repository.foreign
	return &Service{
		registry: registry, repo: repository, checker: checker,
		writes:         newTransactionCoordinator(repository, appender),
		administrators: guard,
	}
}

// PermissionCatalog returns all declared non-platform permissions in stable
// code order.
func (s *Service) PermissionCatalog(context.Context) []authz.Action {
	actions := s.registry.All()
	tenantActions := make([]authz.Action, 0, len(actions))
	for _, action := range actions {
		if action.Scope != "platform" {
			tenantActions = append(tenantActions, action)
		}
	}
	return tenantActions
}

// ScopeModel returns the stable hierarchy used by authorization clients.
func (s *Service) ScopeModel(context.Context) []ScopeNode {
	return []ScopeNode{
		{Type: "platform", Relation: "owns", Label: "Platform"},
		{Type: "tenant", ParentType: "platform", Relation: "contains", Label: "Tenant"},
		{Type: "entity", ParentType: "tenant", Relation: "parent", Label: "Company / enterprise / merchant / store"},
		{Type: "business_resource", ParentType: "entity", Relation: "owner", Label: "Business resource"},
	}
}

func (s *Service) resolveRoleOwner(ctx context.Context, roleID guid.ID) (authz.ResourceOwner, bool, error) {
	tenantID, found, err := s.repo.ResolveRoleTenant(ctx, roleID)
	return authz.ResourceOwner{TenantID: tenantID}, found, err
}

func (s *Service) resolveEntityOwner(ctx context.Context, entityID guid.ID) (authz.ResourceOwner, bool, error) {
	tenantID, found, err := s.repo.ResolveEntityTenant(ctx, entityID)
	return authz.ResourceOwner{TenantID: tenantID}, found, err
}

func (s *Service) resolveMenuOwner(ctx context.Context, menuID guid.ID) (authz.ResourceOwner, bool, error) {
	tenantID, found, err := s.repo.ResolveMenuTenant(ctx, menuID)
	return authz.ResourceOwner{TenantID: tenantID}, found, err
}

// MenuCatalog returns the built-in IAM navigation declaration.
func (s *Service) MenuCatalog(context.Context) []MenuDeclaration {
	items := make([]MenuDeclaration, 0, len(DefaultMenus()))
	for _, menu := range DefaultMenus() {
		items = append(items, MenuDeclaration{
			Key: strings.TrimPrefix(menu.Route, "/"), Label: menu.Label, Translations: menu.Translations, Path: menu.Route,
			PermissionCode: menu.PermissionCode, Icon: menu.Icon, SortOrder: menu.SortOrder,
		})
	}
	return []MenuDeclaration{{Key: "iam", Label: "IAM Administration", Translations: corei18n.Translations("menu_root"), Children: items}}
}

// DefaultMenus returns the built-in tenant menu definitions.
func DefaultMenus() []Menu {
	menus := []Menu{
		{Label: "Accounts and members", Route: "/users", Icon: "users", SortOrder: 10, PermissionCode: "user_view", Status: MenuActive},
		{Label: "Invitations", Route: "/invitations", Icon: "mail-plus", SortOrder: 11, PermissionCode: "invitation_view", Status: MenuActive},
		{Label: "Service accounts", Route: "/service-accounts", Icon: "bot", SortOrder: 12, PermissionCode: "service_account_view", Status: MenuActive},
		{Label: "Entities", Route: "/entities", Icon: "network", SortOrder: 13, PermissionCode: "entity_view", Status: MenuActive},
		{Label: "Departments", Route: "/departments", Icon: "building-2", SortOrder: 15, PermissionCode: "dept_view", Status: MenuActive},
		{Label: "Positions", Route: "/positions", Icon: "briefcase-business", SortOrder: 18, PermissionCode: "position_view", Status: MenuActive},
		{Label: "Groups", Route: "/groups", Icon: "users-round", SortOrder: 19, PermissionCode: "group_view", Status: MenuActive},
		{Label: "Roles and permissions", Route: "/roles", Icon: "key-round", SortOrder: 20, PermissionCode: "role_view", Status: MenuActive},
		{Label: "Access requests", Route: "/access-requests", Icon: "clipboard-check", SortOrder: 21, PermissionCode: "access_request_view", Status: MenuActive},
		{Label: "Access reviews", Route: "/access-reviews", Icon: "list-checks", SortOrder: 22, PermissionCode: "access_review_view", Status: MenuActive},
		{Label: "Menu metadata", Route: "/menus", Icon: "boxes", SortOrder: 30, PermissionCode: "menu_view", Status: MenuActive},
		{Label: "OAuth clients", Route: "/oauth-clients", Icon: "app-window", SortOrder: 40, PermissionCode: "oauth_client_view", Status: MenuActive},
		{Label: "SCIM directories", Route: "/scim-directories", Icon: "folder-sync", SortOrder: 45, PermissionCode: "tenant_administer", Status: MenuActive},
		{Label: "Audit events", Route: "/audit/events", Icon: "file-clock", SortOrder: 50, PermissionCode: "audit_event_view", Status: MenuActive},
		{Label: "Audit governance", Route: "/audit-governance", Icon: "shield-check", SortOrder: 51, PermissionCode: "audit_event_view", Status: MenuActive},
	}
	for i := range menus {
		key := "menu_" + strings.NewReplacer("-", "_", "/", "_").Replace(strings.TrimPrefix(menus[i].Route, "/"))
		menus[i].Translations = corei18n.Translations(key)
	}
	return menus
}

// CreateRole creates a normalized role inside one tenant.
func (s *Service) CreateRole(ctx context.Context, tenantID guid.ID, name, description string) (Role, error) {
	if err := validateTenant(tenantID); err != nil {
		return Role{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || len(description) > 4096 {
		return Role{}, fmt.Errorf("%w: invalid role name or description", ErrInvalidArgument)
	}
	var role Role
	record := newAuditRecord(ctx, tenantID, "role_created", "role", 0)
	record.PolicyChanged = true
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		var err error
		role, err = repo.CreateRole(ctx, tenantID, name, description)
		record.TargetID = role.ID
		return err
	})
	return role, err
}

// ListRoles returns every role owned by one tenant.
func (s *Service) ListRoles(ctx context.Context, tenantID guid.ID) ([]Role, error) {
	if err := validateTenant(tenantID); err != nil {
		return nil, err
	}
	return s.repo.ListRoles(ctx, tenantID)
}

// GetRole returns a tenant-qualified role.
func (s *Service) GetRole(ctx context.Context, tenantID, roleID guid.ID) (Role, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return Role{}, err
	}
	return s.repo.GetRole(ctx, tenantID, roleID)
}

// UpdateRole applies the supplied fields to a tenant-qualified role.
func (s *Service) UpdateRole(
	ctx context.Context,
	tenantID, roleID guid.ID,
	name, description *string,
) (Role, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return Role{}, err
	}
	if name == nil && description == nil {
		return Role{}, fmt.Errorf("%w: no role fields supplied", ErrInvalidArgument)
	}
	var updated Role
	record := newAuditRecord(ctx, tenantID, "role_updated", "role", roleID)
	record.PolicyChanged = true
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		current, err := repo.GetRole(ctx, tenantID, roleID)
		if err != nil {
			return err
		}
		if name != nil {
			current.Name = strings.TrimSpace(*name)
		}
		if description != nil {
			current.Description = *description
		}
		if current.Name == "" || len(current.Name) > 128 || len(current.Description) > 4096 {
			return fmt.Errorf("%w: invalid role name or description", ErrInvalidArgument)
		}
		updated, err = repo.UpdateRole(ctx, tenantID, roleID, current.Name, current.Description)
		return err
	})
	return updated, err
}

// DeleteRole removes a tenant role while preserving the last durable tenant
// administrator invariant.
func (s *Service) DeleteRole(ctx context.Context, tenantID, roleID guid.ID) error {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return err
	}
	record := newAuditRecord(ctx, tenantID, "role_deleted", "role", roleID)
	record.PolicyChanged = true
	return s.writes.Run(ctx, record, func(repo *Repository) error {
		verify, err := s.administrators.Protect(ctx, repo.writer(), tenantID)
		if err != nil {
			return err
		}
		if err := repo.DeleteRole(ctx, tenantID, roleID); err != nil {
			return err
		}
		return verify()
	})
}

// ListPermissions returns the permission codes granted to a tenant role.
func (s *Service) ListPermissions(ctx context.Context, tenantID, roleID guid.ID) ([]string, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return nil, err
	}
	return s.repo.ListPermissions(ctx, tenantID, roleID)
}

// ListPermissionGrants returns role permissions with their optional policy
// conditions.
func (s *Service) ListPermissionGrants(
	ctx context.Context,
	tenantID, roleID guid.ID,
) ([]RolePermissionGrant, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return nil, err
	}
	return s.repo.ListPermissionGrants(ctx, tenantID, roleID)
}

// SetPermissionCondition replaces or clears the canonical condition attached
// to an existing role permission.
func (s *Service) SetPermissionCondition(
	ctx context.Context,
	tenantID, roleID guid.ID,
	code string,
	condition json.RawMessage,
) (RolePermissionGrant, bool, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return RolePermissionGrant{}, false, err
	}
	action, ok := s.registry.Find(code)
	if !ok {
		return RolePermissionGrant{}, false, fmt.Errorf("%w: %s", ErrPermissionNotFound, code)
	}
	if action.Scope == "platform" {
		return RolePermissionGrant{}, false, fmt.Errorf("%w: platform permissions cannot be granted to tenant roles", ErrInvalidArgument)
	}
	if err := s.requireGrantablePermissions(ctx, tenantID, code); err != nil {
		return RolePermissionGrant{}, false, err
	}
	canonical, err := policyx.CanonicalCondition(condition)
	if err != nil {
		return RolePermissionGrant{}, false, fmt.Errorf("%w: %w", ErrInvalidRolePermissionCondition, err)
	}
	record := newAuditRecord(ctx, tenantID, "role_permission_condition_updated", "role", roleID)
	record.Detail["permission_code"] = code
	if len(canonical) > 0 {
		record.Detail["condition"] = canonical
	} else {
		record.Detail["condition_cleared"] = true
	}
	var grant RolePermissionGrant
	var changed bool
	err = s.writes.Run(ctx, record, func(repo *Repository) error {
		verify := func() error { return nil }
		if isAdministratorPermission(code) {
			var err error
			verify, err = s.administrators.Protect(ctx, repo.writer(), tenantID)
			if err != nil {
				return err
			}
		}
		var err error
		grant, changed, err = repo.SetPermissionCondition(ctx, tenantID, roleID, code, canonical)
		if err == nil && changed {
			err = verify()
		}
		record.PolicyChanged, record.SkipAudit = changed, !changed
		record.Detail["changed"] = changed
		return err
	})
	return grant, changed, err
}

// GrantPermission adds a declared tenant permission to a role after checking
// the authenticated actor cannot escalate privileges.
func (s *Service) GrantPermission(
	ctx context.Context,
	tenantID, roleID guid.ID,
	code string,
) (bool, error) {
	return s.changePermission(ctx, tenantID, roleID, code, true)
}

// RevokePermission removes a role permission while preserving the last
// durable tenant administrator.
func (s *Service) RevokePermission(
	ctx context.Context,
	tenantID, roleID guid.ID,
	code string,
) (bool, error) {
	return s.changePermission(ctx, tenantID, roleID, code, false)
}

func (s *Service) changePermission(
	ctx context.Context,
	tenantID, roleID guid.ID,
	code string,
	grant bool,
) (bool, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return false, err
	}
	action, ok := s.registry.Find(code)
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrPermissionNotFound, code)
	}
	if action.Scope == "platform" {
		return false, fmt.Errorf("%w: platform permissions cannot be granted to tenant roles", ErrInvalidArgument)
	}
	if grant {
		if err := s.requireGrantablePermissions(ctx, tenantID, code); err != nil {
			return false, err
		}
	}
	eventType := "role_permission_revoked"
	if grant {
		eventType = "role_permission_granted"
	}
	record := newAuditRecord(ctx, tenantID, eventType, "role", roleID)
	record.Detail["permission_code"] = code
	var changed bool
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		var err error
		if grant {
			changed, err = repo.GrantPermission(ctx, tenantID, roleID, code)
		} else {
			verify, protectErr := s.administrators.Protect(ctx, repo.writer(), tenantID)
			if protectErr != nil {
				return protectErr
			}
			changed, err = repo.RevokePermission(ctx, tenantID, roleID, code)
			if err == nil {
				err = verify()
			}
		}
		record.PolicyChanged = changed
		record.Detail["changed"] = changed
		return err
	})
	return changed, err
}

// ListMembers returns the permanent principal members of a tenant role.
func (s *Service) ListMembers(ctx context.Context, tenantID, roleID guid.ID) ([]guid.ID, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return nil, err
	}
	return s.repo.ListMembers(ctx, tenantID, roleID)
}

// AddMember grants a role permanently to an active tenant member.
func (s *Service) AddMember(
	ctx context.Context,
	tenantID, roleID, principalID guid.ID,
) (bool, error) {
	return s.changeMember(ctx, tenantID, roleID, principalID, true)
}

// RemoveMember revokes a permanent role membership while preserving the last
// durable tenant administrator.
func (s *Service) RemoveMember(
	ctx context.Context,
	tenantID, roleID, principalID guid.ID,
) (bool, error) {
	return s.changeMember(ctx, tenantID, roleID, principalID, false)
}

func (s *Service) changeMember(
	ctx context.Context,
	tenantID, roleID, principalID guid.ID,
	add bool,
) (bool, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return false, err
	}
	if principalID.Zero() {
		return false, fmt.Errorf("%w: invalid principal subject", ErrInvalidArgument)
	}
	if add {
		if err := s.requireGrantableRole(ctx, tenantID, roleID); err != nil {
			return false, err
		}
	}
	eventType := "role_member_removed"
	if add {
		eventType = "role_member_added"
	}
	record := newAuditRecord(ctx, tenantID, eventType, "role", roleID)
	record.Detail["principal_id"] = principalID
	var changed bool
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		var err error
		if add {
			active, activeErr := repo.IsMemberActive(ctx, tenantID, principalID)
			if activeErr != nil {
				return activeErr
			}
			if !active {
				return ErrMemberInactive
			}
			changed, err = repo.AddMember(ctx, tenantID, roleID, principalID)
		} else {
			verify, protectErr := s.administrators.Protect(ctx, repo.writer(), tenantID)
			if protectErr != nil {
				return protectErr
			}
			changed, err = repo.RemoveMember(ctx, tenantID, roleID, principalID)
			if err == nil {
				err = verify()
			}
		}
		record.PolicyChanged = changed
		record.Detail["changed"] = changed
		return err
	})
	return changed, err
}

func validateTenant(tenantID guid.ID) error {
	if tenantID.Zero() {
		return fmt.Errorf("%w: invalid tenant id", ErrInvalidArgument)
	}
	return nil
}

func validateRoleRef(tenantID, roleID guid.ID) error {
	if err := validateTenant(tenantID); err != nil {
		return err
	}
	if roleID.Zero() {
		return fmt.Errorf("%w: invalid role id", ErrInvalidArgument)
	}
	return nil
}
