package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

const (
	// administratorRoleName is the built-in role a new tenant is created with.
	// It is an ordinary tenant role afterwards, so an administrator may rename
	// it, widen it, or grant it to somebody else.
	administratorRoleName = "Administrator"

	// administratorRoleDescription explains the role to the next person who
	// opens the roles page and finds one they did not create.
	administratorRoleDescription = "Built-in role created with the tenant. Grants full administration of this tenant."

	// tenantAdministerPermission is the single permission the built-in role
	// carries. The authorizer already treats it as the tenant-wide override,
	// so enumerating every action here would only drift from that.
	tenantAdministerPermission = "tenant_administer"
)

// BootstrapTenantOn makes a newly created tenant administrable, inside the
// transaction that created it. Every tenant route requires an active
// membership, so a tenant that commits without this is unreachable by design
// rather than by accident, and no HTTP call can repair it.
//
// The caller owns the transaction: this writes the owner's membership, the
// built-in Administrator role and its grant, the owner's role membership, and
// the default menus, and leaves the policy revision bump and the audit event
// to the caller so a tenant creation still costs exactly one of each.
func (r *Repository) BootstrapTenantOn(
	ctx context.Context,
	db *gorm.DB,
	input TenantBootstrapInput,
	now time.Time,
) (TenantBootstrapResult, error) {
	if r == nil || db == nil {
		return TenantBootstrapResult{}, ErrUnavailable
	}
	input, err := normalizeTenantBootstrap(input)
	if err != nil {
		return TenantBootstrapResult{}, err
	}
	repo := r.withDB(db)
	kind, err := repo.rootKind(ctx, input.TenantID)
	if err != nil {
		return TenantBootstrapResult{}, err
	}
	if kind != input.ScopeKind {
		return TenantBootstrapResult{}, fmt.Errorf("%w: bootstrap owner kind mismatch", ErrInvalidArgument)
	}
	stamp := now.UTC().Truncate(time.Millisecond)

	if err := repo.createTenantOwnerMember(ctx, input, stamp); err != nil {
		return TenantBootstrapResult{}, err
	}
	roleID, err := repo.createAdministratorRole(ctx, input.TenantID, input.ScopeKind, stamp)
	if err != nil {
		return TenantBootstrapResult{}, err
	}
	if err := repo.grantAdministratorRole(ctx, input, roleID, stamp); err != nil {
		return TenantBootstrapResult{}, err
	}
	menus, err := repo.createMenus(ctx, input.TenantID, bootstrapMenus(input.ScopeKind), stamp)
	if err != nil {
		return TenantBootstrapResult{}, err
	}
	return TenantBootstrapResult{
		AdministratorRoleID: roleID,
		PermissionCount:     1,
		MenuCount:           menus,
	}, nil
}

func (r *Repository) createTenantOwnerMember(
	ctx context.Context,
	input TenantBootstrapInput,
	now time.Time,
) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	row := model.TenantMember{
		TenantID: input.TenantID, PrincipalID: input.OwnerID,
		DisplayName: input.DisplayName, Email: input.Email,
		Status:    string(MemberActive),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := tables.TenantMembers.WithContext(ctx).Create(&row); err != nil {
		return fmt.Errorf("insert tenant owner membership: %w", err)
	}
	return nil
}

func (r *Repository) createAdministratorRole(
	ctx context.Context,
	tenantID guid.ID,
	kind string,
	now time.Time,
) (guid.ID, error) {
	tables, _, err := r.tables()
	if err != nil {
		return 0, err
	}
	roleID, err := r.nextID()
	if err != nil {
		return 0, fmt.Errorf("generate administrator role id: %w", err)
	}
	description := administratorRoleDescription
	if kind == "platform" {
		description = "Built-in role created with the platform. Grants full administration of this platform."
	}
	row := model.Role{
		TenantID: tenantID, ID: roleID,
		Name: administratorRoleName, NameKey: roleNameKey(administratorRoleName),
		Description: description,
		CreatedAt:   now, UpdatedAt: now,
	}
	if err := tables.Roles.WithContext(ctx).Create(&row); err != nil {
		return 0, fmt.Errorf("insert administrator role: %w", err)
	}
	return roleID, nil
}

func (r *Repository) grantAdministratorRole(
	ctx context.Context,
	input TenantBootstrapInput,
	roleID guid.ID,
	now time.Time,
) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	permission := model.RolePermission{
		TenantID: input.TenantID, RoleID: roleID,
		PermissionCode: administratorPermission(input.ScopeKind), CreatedAt: now,
	}
	if err := tables.RolePermissions.WithContext(ctx).Create(&permission); err != nil {
		return fmt.Errorf("grant administrator role permission: %w", err)
	}
	member := model.RoleMember{
		TenantID: input.TenantID, RoleID: roleID,
		PrincipalID: input.OwnerID, CreatedAt: now,
	}
	if err := tables.RoleMembers.WithContext(ctx).Create(&member); err != nil {
		return fmt.Errorf("add owner to administrator role: %w", err)
	}
	return nil
}

// createDefaultMenus writes the tenant's navigation metadata. These are per
// tenant rows rather than a global catalog, because an administrator may
// disable, reorder, or extend them afterwards.
func (r *Repository) createDefaultMenus(
	ctx context.Context,
	tenantID guid.ID,
	now time.Time,
) (int, error) {
	return r.createMenus(ctx, tenantID, DefaultMenus(), now)
}

func bootstrapMenus(kind string) []Menu {
	if kind != "platform" {
		return DefaultMenus()
	}
	const organizationID, accessID guid.ID = 1, 2
	items := []Menu{
		{ID: organizationID, Label: "Organization", Translations: corei18n.Translations("menu_organization"), SortOrder: 10, Status: MenuActive},
		{ID: accessID, Label: "Access", Translations: corei18n.Translations("menu_access"), SortOrder: 20, Status: MenuActive},
	}
	for _, menu := range DefaultMenus() {
		switch menu.PermissionCode {
		case "user_view":
			menu.PermissionCode, menu.Route = "platform_member_view", "/members"
			menu.ParentID = organizationID
			items = append(items, menu)
		case "dept_view", "group_view":
			menu.ParentID = organizationID
			items = append(items, menu)
		case "role_view", "menu_view":
			menu.ParentID = accessID
			items = append(items, menu)
		}
	}
	return items
}

func administratorPermission(kind string) string {
	if kind == "platform" {
		return "operating_platform_administer"
	}
	return tenantAdministerPermission
}

func (r *Repository) createMenus(ctx context.Context, tenantID guid.ID, defaults []Menu, now time.Time) (int, error) {
	tables, _, err := r.tables()
	if err != nil {
		return 0, err
	}
	rows := make([]*model.Menu, 0, len(defaults))
	ids := make([]guid.ID, len(defaults))
	declarationIDs := make(map[guid.ID]guid.ID)
	for index, menu := range defaults {
		id, err := r.nextID()
		if err != nil {
			return 0, fmt.Errorf("generate menu id: %w", err)
		}
		ids[index] = id
		if !menu.ID.Zero() {
			declarationIDs[menu.ID] = id
		}
	}
	for index, menu := range defaults {
		if !menu.ParentID.Zero() {
			parentID, ok := declarationIDs[menu.ParentID]
			if !ok {
				return 0, fmt.Errorf("%w: missing default menu parent", ErrInvalidArgument)
			}
			menu.ParentID = parentID
		}
		menu.TenantID, menu.ID = tenantID, ids[index]
		row := menuToRow(menu)
		row.CreatedAt, row.UpdatedAt = now, now
		rows = append(rows, &row)
	}
	if err := tables.Menus.WithContext(ctx).Create(rows...); err != nil {
		return 0, fmt.Errorf("insert default menus: %w", err)
	}
	return len(rows), nil
}

// TenantIsUnclaimedOn reports whether a tenant still has no member and no role.
// The repair command exists for tenants created before the bootstrap did, and
// this is what keeps it from handing a second owner to a tenant somebody is
// already administering.
func (r *Repository) TenantIsUnclaimedOn(
	ctx context.Context,
	db *gorm.DB,
	tenantID guid.ID,
) (bool, error) {
	if r == nil || db == nil {
		return false, ErrUnavailable
	}
	if tenantID.Zero() {
		return false, fmt.Errorf("%w: tenant is required", ErrInvalidArgument)
	}
	names := r.names
	for table, label := range map[gormx.TableName]string{
		names.TenantMembers: "members",
		names.Roles:         "roles",
	} {
		var count int64
		err := db.WithContext(ctx).Table(table.String()).
			Where("tenant_id = ?", tenantID).Limit(1).Count(&count).Error
		if err != nil {
			return false, fmt.Errorf("count tenant %s: %w", label, err)
		}
		if count > 0 {
			return false, nil
		}
	}
	return true, nil
}

// BumpPolicyRevisionOn advances a tenant policy revision through the caller
// transaction. Repairing a tenant grants a role and a membership, so a cache
// that answered "this principal may do nothing here" a moment ago has to be
// told the answer changed, in the same commit that changed it.
func (r *Repository) BumpPolicyRevisionOn(
	ctx context.Context,
	db *gorm.DB,
	tenantID guid.ID,
	now time.Time,
) error {
	if r == nil || db == nil || r.revisions == nil {
		return ErrUnavailable
	}
	if tenantID.Zero() {
		return fmt.Errorf("%w: tenant is required", ErrInvalidArgument)
	}
	if err := r.revisions.Bump(ctx, db, tenantID, now); err != nil {
		return fmt.Errorf("bump IAM policy revision: %w", err)
	}
	return nil
}

func normalizeTenantBootstrap(input TenantBootstrapInput) (TenantBootstrapInput, error) {
	if input.ScopeKind == "" {
		input.ScopeKind = "tenant"
	}
	if input.ScopeKind != "tenant" && input.ScopeKind != "platform" {
		return TenantBootstrapInput{}, fmt.Errorf("%w: unsupported bootstrap owner kind", ErrInvalidArgument)
	}
	if input.TenantID.Zero() {
		return TenantBootstrapInput{}, fmt.Errorf("%w: tenant is required", ErrInvalidArgument)
	}
	if input.OwnerID.Zero() {
		return TenantBootstrapInput{}, fmt.Errorf("%w: owner principal is required", ErrInvalidArgument)
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.TrimSpace(input.Email)
	if input.DisplayName == "" {
		// The membership row carries the name the tenant's own member list
		// shows. Falling back to the identifier keeps that list readable
		// instead of blank.
		input.DisplayName = input.OwnerID.String()
	}
	if len(input.DisplayName) > 128 || len(input.Email) > 320 {
		return TenantBootstrapInput{}, fmt.Errorf("%w: owner profile exceeds column bounds", ErrInvalidArgument)
	}
	return input, nil
}

// tenantBootstrapper is the capability handed to the module that owns tenants.
// It is provided before injection, so it guards its own collaborators rather
// than assuming the repository has arrived.
func (m *Module) tenantBootstrapper() TenantBootstrapper {
	return TenantBootstrapper{
		SeedSystemMenus: func(ctx context.Context, db *gorm.DB, ownerID guid.ID, now time.Time) error {
			if m == nil || m.repository == nil || db == nil || ownerID.Zero() {
				return ErrUnavailable
			}
			repo := m.repository.withDB(db)
			if _, err := repo.createMenus(ctx, ownerID, SystemDefaultMenus(), now); err != nil {
				return err
			}
			return repo.bumpPolicyRevision(ctx, ownerID)
		},
		Bootstrap: func(
			ctx context.Context,
			db *gorm.DB,
			input TenantBootstrapInput,
			now time.Time,
		) (TenantBootstrapResult, error) {
			if m == nil || m.repository == nil {
				return TenantBootstrapResult{}, ErrUnavailable
			}
			return m.repository.BootstrapTenantOn(ctx, db, input, now)
		},
		IsUnclaimed: func(ctx context.Context, db *gorm.DB, tenantID guid.ID) (bool, error) {
			if m == nil || m.repository == nil {
				return false, ErrUnavailable
			}
			return m.repository.TenantIsUnclaimedOn(ctx, db, tenantID)
		},
		BumpRevision: func(ctx context.Context, db *gorm.DB, tenantID guid.ID, now time.Time) error {
			if m == nil || m.repository == nil {
				return ErrUnavailable
			}
			return m.repository.BumpPolicyRevisionOn(ctx, db, tenantID, now)
		},
	}
}
