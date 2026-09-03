package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type tenantMemberRecord struct {
	model.TenantMember
	DepartmentID guid.ID `gorm:"column:department_id"`
}

func (r *Repository) PutMember(ctx context.Context, member TenantMember) (TenantMember, error) {
	repo, err := r.pinWriter()
	if err != nil {
		return TenantMember{}, err
	}
	tables, db, err := repo.tables()
	if err != nil {
		return TenantMember{}, err
	}
	now := repo.now().UTC().Truncate(time.Millisecond)
	var disabledAt *time.Time
	if member.Status == MemberDisabled {
		disabledAt = &now
	}
	row := model.TenantMember{
		TenantID: member.TenantID, PrincipalID: member.PrincipalID,
		DisplayName: member.DisplayName, Email: member.Email, Status: string(member.Status),
		CreatedAt: now, UpdatedAt: now, DisabledAt: disabledAt,
	}
	result := db.WithContext(ctx).Table(tables.TenantMembers.TableName()).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "principal_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"display_name", "email", "status", "updated_at", "disabled_at",
		}),
	}).Create(&row)
	if result.Error != nil {
		return TenantMember{}, fmt.Errorf("upsert tenant member: %w", result.Error)
	}
	return repo.GetMember(ctx, member.TenantID, member.PrincipalID)
}

func (r *Repository) GetMember(ctx context.Context, tenantID, principalID guid.ID) (TenantMember, error) {
	tables, db, err := r.tables()
	if err != nil {
		return TenantMember{}, err
	}
	var row tenantMemberRecord
	members := tables.TenantMembers.TableName()
	departments := r.prefix() + "iam_member_departments"
	err = db.WithContext(ctx).Table(members+" AS tm").
		Select("tm.*, COALESCE(md.department_id, 0) AS department_id").
		Joins("LEFT JOIN "+departments+" AS md ON md.tenant_id = tm.tenant_id AND md.principal_id = tm.principal_id").
		Where("tm.tenant_id = ? AND tm.principal_id = ?", tenantID, principalID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TenantMember{}, ErrMemberNotFound
	}
	if err != nil {
		return TenantMember{}, fmt.Errorf("get tenant member: %w", err)
	}
	return memberFromRecord(&row), nil
}

func (r *Repository) ListMembersPage(
	ctx context.Context,
	tenantID guid.ID,
	filter MemberFilter,
) ([]TenantMember, int64, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	offset, limit := normalizePage(filter.Offset, filter.Limit)
	members := tables.TenantMembers.TableName()
	base := func() *gorm.DB {
		query := db.WithContext(ctx).Table(members+" AS tm").Where("tm.tenant_id = ?", tenantID)
		if filter.Status != "" {
			query = query.Where("tm.status = ?", filter.Status)
		}
		if search := strings.TrimSpace(filter.Search); search != "" {
			like := "%" + strings.ToLower(search) + "%"
			query = query.Where("(LOWER(tm.display_name) LIKE ? OR LOWER(tm.email) LIKE ?)", like, like)
		}
		return query
	}
	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count tenant members: %w", err)
	}

	departments := r.prefix() + "iam_member_departments"
	listing := base().Select("tm.*, COALESCE(md.department_id, 0) AS department_id").
		Joins("LEFT JOIN " + departments + " AS md ON md.tenant_id = tm.tenant_id AND md.principal_id = tm.principal_id").
		Order("tm.display_name ASC, tm.principal_id ASC")
	if limit > 0 {
		listing = listing.Limit(limit)
	}
	if offset > 0 {
		listing = listing.Offset(offset)
	}
	var rows []tenantMemberRecord
	if err := listing.Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list tenant members: %w", err)
	}
	result := make([]TenantMember, len(rows))
	for index := range rows {
		result[index] = memberFromRecord(&rows[index])
	}
	return result, total, nil
}

func (r *Repository) SetMemberStatus(
	ctx context.Context,
	tenantID, principalID guid.ID,
	status MemberStatus,
) (TenantMember, error) {
	var updated TenantMember
	err := r.runInTx(ctx, func(repo *Repository) error {
		member, err := repo.GetMember(ctx, tenantID, principalID)
		if err != nil {
			return err
		}
		member.Status = status
		updated, err = repo.PutMember(ctx, member)
		return err
	})
	return updated, err
}

func (r *Repository) IsMemberActive(ctx context.Context, tenantID, principalID guid.ID) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	count, err := tables.TenantMembers.WithContext(ctx).
		Where(
			tables.TenantMembers.TenantID.Eq(tenantID),
			tables.TenantMembers.PrincipalID.Eq(principalID),
			tables.TenantMembers.Status.Eq(string(MemberActive)),
		).Count()
	if err != nil {
		return false, fmt.Errorf("check tenant member: %w", err)
	}
	return count == 1, nil
}

func (r *Repository) ListMemberRoleIDs(ctx context.Context, tenantID, principalID guid.ID) ([]guid.ID, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	prefix := r.prefix()
	now := r.now().UTC().Truncate(time.Millisecond)
	var rows []struct {
		RoleID guid.ID `gorm:"column:role_id"`
	}
	err = db.WithContext(ctx).Raw(`
SELECT role_id FROM `+prefix+`iam_role_members WHERE tenant_id = ? AND principal_id = ?
UNION
SELECT role_id FROM `+prefix+`iam_temporary_role_grants
WHERE tenant_id = ? AND principal_id = ? AND starts_at <= ? AND ends_at > ?
ORDER BY role_id ASC`, tenantID, principalID, tenantID, principalID, now, now).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list tenant member roles: %w", err)
	}
	ids := make([]guid.ID, len(rows))
	for index, row := range rows {
		ids[index] = row.RoleID
	}
	return ids, nil
}

func (r *Repository) CreateMenu(ctx context.Context, menu Menu) (Menu, error) {
	tables, _, err := r.tables()
	if err != nil {
		return Menu{}, err
	}
	id, err := r.nextID()
	if err != nil {
		return Menu{}, fmt.Errorf("generate menu id: %w", err)
	}
	now := r.now().UTC().Truncate(time.Millisecond)
	row := menuToRow(menu)
	row.ID, row.CreatedAt, row.UpdatedAt = id, now, now
	if err := tables.Menus.WithContext(ctx).Create(&row); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return Menu{}, ErrMenuConflict
		}
		return Menu{}, fmt.Errorf("insert menu: %w", err)
	}
	return menuFromRow(&row), nil
}

func (r *Repository) GetMenu(ctx context.Context, tenantID, menuID guid.ID) (Menu, error) {
	tables, _, err := r.tables()
	if err != nil {
		return Menu{}, err
	}
	row, err := tables.Menus.WithContext(ctx).
		Where(tables.Menus.TenantID.Eq(tenantID), tables.Menus.ID.Eq(menuID)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Menu{}, ErrMenuNotFound
	}
	if err != nil {
		return Menu{}, fmt.Errorf("get menu: %w", err)
	}
	return menuFromRow(row), nil
}

func (r *Repository) ListMenus(ctx context.Context, tenantID guid.ID, activeOnly bool) ([]Menu, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	listing := tables.Menus.WithContext(ctx).Where(tables.Menus.TenantID.Eq(tenantID))
	if activeOnly {
		listing = listing.Where(tables.Menus.Status.Eq(string(MenuActive)))
	}
	rows, err := listing.Order(tables.Menus.SortOrder, tables.Menus.ID).Find()
	if err != nil {
		return nil, fmt.Errorf("list menus: %w", err)
	}
	menus := make([]Menu, len(rows))
	for index, row := range rows {
		menus[index] = menuFromRow(row)
	}
	return menus, nil
}

func (r *Repository) UpdateMenu(ctx context.Context, menu Menu) (Menu, error) {
	var updated Menu
	err := r.runInTx(ctx, func(repo *Repository) error {
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		row := menuToRow(menu)
		updates := map[string]any{
			"parent_id": row.ParentID, "label": row.Label, "route": row.Route,
			"icon": row.Icon, "sort_order": row.SortOrder,
			"permission_code": row.PermissionCode, "status": row.Status,
			"updated_at": repo.now().UTC().Truncate(time.Millisecond),
		}
		result := db.WithContext(ctx).Table(tables.Menus.TableName()).
			Where("tenant_id = ? AND id = ?", menu.TenantID, menu.ID).Updates(updates)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
				return ErrMenuConflict
			}
			return fmt.Errorf("update menu: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrMenuNotFound
		}
		updated, err = repo.GetMenu(ctx, menu.TenantID, menu.ID)
		return err
	})
	return updated, err
}

func (r *Repository) DeleteMenu(ctx context.Context, tenantID, menuID guid.ID) error {
	return r.runInTx(ctx, func(repo *Repository) error {
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		children, err := tables.Menus.WithContext(ctx).
			Where(tables.Menus.TenantID.Eq(tenantID), tables.Menus.ParentID.Eq(menuID)).Count()
		if err != nil {
			return fmt.Errorf("count menu children: %w", err)
		}
		if children > 0 {
			return ErrMenuHasChildren
		}
		info, err := tables.Menus.WithContext(ctx).
			Where(tables.Menus.TenantID.Eq(tenantID), tables.Menus.ID.Eq(menuID)).Delete()
		if err != nil {
			return fmt.Errorf("delete menu: %w", err)
		}
		if info.RowsAffected == 0 {
			return ErrMenuNotFound
		}
		return nil
	})
}

func memberFromRecord(row *tenantMemberRecord) TenantMember {
	member := TenantMember{
		TenantID: row.TenantID, PrincipalID: row.PrincipalID,
		DisplayName: row.DisplayName, Email: row.Email, DepartmentID: row.DepartmentID,
		Status: MemberStatus(row.Status), CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.DisabledAt != nil {
		member.DisabledAt = row.DisabledAt.UTC()
	}
	return member
}

func menuToRow(menu Menu) model.Menu {
	var parentID *guid.ID
	var route *string
	if !menu.ParentID.Zero() {
		parentID = &menu.ParentID
	}
	if menu.Route != "" {
		route = &menu.Route
	}
	return model.Menu{
		TenantID: menu.TenantID, ID: menu.ID, ParentID: parentID,
		Label: menu.Label, Route: route, Icon: menu.Icon, SortOrder: menu.SortOrder,
		PermissionCode: menu.PermissionCode, Status: string(menu.Status),
	}
}

func menuFromRow(row *model.Menu) Menu {
	menu := Menu{
		ID: row.ID, TenantID: row.TenantID, Label: row.Label, Icon: row.Icon,
		SortOrder: row.SortOrder, PermissionCode: row.PermissionCode, Status: MenuStatus(row.Status),
		CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.ParentID != nil {
		menu.ParentID = *row.ParentID
	}
	if row.Route != nil {
		menu.Route = *row.Route
	}
	return menu
}
