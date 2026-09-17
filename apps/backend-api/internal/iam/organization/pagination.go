package organization

import (
	"context"
	"fmt"

	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// maxPageLimit is the largest page the repository will build, whatever a caller
// asks for. It matches the maximum the HTTP inputs publish, so the transport
// bound and the storage bound cannot drift apart.
const maxPageLimit = 200

// normalizePage clamps a requested window into one the repository will honor.
// A zero limit still means unbounded at this layer -- the bound has to be
// declared where a caller can be held to it, which is the HTTP input -- so the
// internal callers that page nothing keep working unchanged.
func normalizePage(offset, limit int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = 0
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	return offset, limit
}

// applyPage narrows an ordered query to one window. GORM emits OFFSET without
// LIMIT as its own clause, so the two are applied independently.
func applyPage(db *gorm.DB, offset, limit int) *gorm.DB {
	if limit > 0 {
		db = db.Limit(limit)
	}
	if offset > 0 {
		db = db.Offset(offset)
	}
	return db
}

// pageOf takes one window of an already ordered slice. It is used where the
// order or the membership itself is decided in Go rather than by SQL, so the
// rows cannot be narrowed before they are computed.
func pageOf[T any](items []T, offset, limit int) []T {
	if offset >= len(items) {
		return items[len(items):]
	}
	end := len(items)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return items[offset:end]
}

// listTenantsPage returns one page of the platform tenants and the number of
// tenants the same filter matches before pagination.
func (r *Repository) listTenantsPage(
	ctx context.Context,
	includeDeleted bool,
	offset, limit int,
) ([]model.Tenant, int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	name := tables.Tenants.TableName()
	matching := func() *gorm.DB {
		db := r.writer().WithContext(ctx).Table(name)
		if !includeDeleted {
			db = db.Where("status <> ?", TenantDeleted)
		}
		return db
	}
	var total int64
	if err := matching().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count tenants: %w", err)
	}
	offset, limit = normalizePage(offset, limit)
	var rows []model.Tenant
	if err := applyPage(matching().Order("name ASC, id ASC"), offset, limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list tenants: %w", err)
	}
	return rows, total, nil
}

// listPositionsPage returns one page of a tenant's positions and the tenant's
// total position count before pagination.
func (r *Repository) listPositionsPage(
	ctx context.Context,
	tenantID guid.ID,
	offset, limit int,
) ([]positionRow, int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	name := tables.Positions.TableName()
	matching := func() *gorm.DB {
		return r.writer().WithContext(ctx).Table(name).Where("tenant_id = ?", tenantID)
	}
	var total int64
	if err := matching().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count positions: %w", err)
	}
	offset, limit = normalizePage(offset, limit)
	var rows []positionRow
	listing := matching().Order("sort_order ASC, code ASC, id ASC")
	if err := applyPage(listing, offset, limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list positions: %w", err)
	}
	return rows, total, nil
}

// listPositionMembersPage returns one page of a position's holders and the
// number of holders before pagination. The count runs over the same join as the
// listing, so a membership whose tenant member row is gone is absent from both.
func (r *Repository) listPositionMembersPage(
	ctx context.Context,
	tenantID, positionID guid.ID,
	offset, limit int,
) ([]positionMemberRow, int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	members, positions := r.foreign.access.TenantMembers.String(), tables.PositionMembers.TableName()
	matching := func() *gorm.DB {
		return r.writer().WithContext(ctx).Table(positions+" AS pm").
			Joins("JOIN "+members+" AS tm ON tm.tenant_id = pm.tenant_id AND tm.principal_id = pm.principal_id").
			Where("pm.tenant_id = ? AND pm.position_id = ?", tenantID, positionID)
	}
	var total int64
	if err := matching().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count position members: %w", err)
	}
	offset, limit = normalizePage(offset, limit)
	var rows []positionMemberRow
	listing := matching().Select("pm.*, tm.display_name, tm.email").Order("tm.display_name ASC, pm.principal_id ASC")
	if err := applyPage(listing, offset, limit).Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list position members: %w", err)
	}
	return rows, total, nil
}

// listGroupsPage returns one page of a tenant's groups and the tenant's total
// group count before pagination.
func (r *Repository) listGroupsPage(
	ctx context.Context,
	tenantID guid.ID,
	offset, limit int,
) ([]groupRow, int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	name := tables.Groups.TableName()
	matching := func() *gorm.DB {
		return r.writer().WithContext(ctx).Table(name).Where("tenant_id = ?", tenantID)
	}
	var total int64
	if err := matching().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count groups: %w", err)
	}
	offset, limit = normalizePage(offset, limit)
	var rows []groupRow
	listing := matching().Order("sort_order ASC, name_key ASC, id ASC")
	if err := applyPage(listing, offset, limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list groups: %w", err)
	}
	return rows, total, nil
}

// listGroupMembersPage returns one page of a static group's members and the
// number of members before pagination. A dynamic group never reaches here: its
// membership is a rule's verdict rather than stored rows.
func (r *Repository) listGroupMembersPage(
	ctx context.Context,
	tenantID, groupID guid.ID,
	offset, limit int,
) ([]groupMemberRow, int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	members, groups := r.foreign.access.TenantMembers.String(), tables.GroupMembers.TableName()
	matching := func() *gorm.DB {
		return r.writer().WithContext(ctx).Table(groups+" AS gm").
			Joins("JOIN "+members+" AS tm ON tm.tenant_id = gm.tenant_id AND tm.principal_id = gm.principal_id").
			Where("gm.tenant_id = ? AND gm.group_id = ?", tenantID, groupID)
	}
	var total int64
	if err := matching().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count group members: %w", err)
	}
	offset, limit = normalizePage(offset, limit)
	var rows []groupMemberRow
	listing := matching().Select("gm.*, tm.display_name, tm.email").Order("tm.display_name ASC, gm.principal_id ASC")
	if err := applyPage(listing, offset, limit).Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list group members: %w", err)
	}
	return rows, total, nil
}

// listInvitationsPage returns one page of a tenant's invitations and the
// tenant's total invitation count before pagination.
func (r *Repository) listInvitationsPage(
	ctx context.Context,
	tenantID guid.ID,
	offset, limit int,
) ([]model.Invitation, int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	name := tables.Invitations.TableName()
	matching := func() *gorm.DB {
		return r.writer().WithContext(ctx).Table(name).Where("tenant_id = ?", tenantID)
	}
	var total int64
	if err := matching().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count invitations: %w", err)
	}
	offset, limit = normalizePage(offset, limit)
	var rows []model.Invitation
	listing := matching().Order("created_at DESC, id DESC")
	if err := applyPage(listing, offset, limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list invitations: %w", err)
	}
	return rows, total, nil
}

// ListPage returns one page of the tenant's department hierarchy and the
// tenant's total department count before pagination.
//
// The window is taken after the tree is ordered, not in SQL: a department's
// place in the listing and its Depth both depend on its ancestors, so a SQL
// window would hand back rows whose order and depth disagree with the same
// rows read unpaged. The page a caller receives is bounded either way, which is
// what the listing has to guarantee.
func (s *Service) ListPage(
	ctx context.Context,
	tenantID guid.ID,
	offset, limit int,
) ([]Department, int64, error) {
	items, err := s.List(ctx, tenantID)
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	return pageOf(items, offset, limit), int64(len(items)), nil
}

// ListPage returns one page of the platform tenants with the unfiltered total
// for the same include_deleted filter.
func (s *TenantService) ListPage(
	ctx context.Context,
	includeDeleted bool,
	offset, limit int,
) ([]Tenant, int64, error) {
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	rows, total, err := repo.listTenantsPage(ctx, includeDeleted, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	items := make([]Tenant, len(rows))
	for index := range rows {
		items[index] = tenantFromRow(&rows[index])
	}
	return items, total, nil
}

// ListPage returns one page of the tenant's positions with the unfiltered total.
func (s *PositionService) ListPage(
	ctx context.Context,
	tenantID guid.ID,
	offset, limit int,
) ([]Position, int64, error) {
	if tenantID.Zero() {
		return nil, 0, ErrPositionInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	rows, total, err := repo.listPositionsPage(ctx, tenantID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	items := make([]Position, len(rows))
	for index := range rows {
		items[index] = positionFromRow(&rows[index])
	}
	return items, total, nil
}

// ListMembersPage returns one page of a position's holders with the unfiltered
// total. The position is read first so an unknown position is reported as
// missing rather than as a position nobody holds.
func (s *PositionService) ListMembersPage(
	ctx context.Context,
	tenantID, positionID guid.ID,
	offset, limit int,
) ([]PositionMember, int64, error) {
	if tenantID.Zero() || !validID(positionID) {
		return nil, 0, ErrPositionInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	if _, err := repo.getPosition(ctx, tenantID, positionID); err != nil {
		return nil, 0, err
	}
	rows, total, err := repo.listPositionMembersPage(ctx, tenantID, positionID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	items := make([]PositionMember, len(rows))
	for index := range rows {
		items[index] = positionMemberFromRow(&rows[index])
	}
	return items, total, nil
}

// ListPage returns one page of the tenant's groups with the unfiltered total.
func (s *GroupService) ListPage(
	ctx context.Context,
	tenantID guid.ID,
	offset, limit int,
) ([]Group, int64, error) {
	if tenantID.Zero() {
		return nil, 0, ErrGroupInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	rows, total, err := repo.listGroupsPage(ctx, tenantID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	items := make([]Group, len(rows))
	for index := range rows {
		items[index] = groupFromRow(&rows[index])
	}
	return items, total, nil
}

// ListMembersPage returns one page of a group's members with the unfiltered
// total.
//
// A dynamic group is paged after evaluation rather than in SQL: its membership
// is the rule's verdict on every active tenant member, which only Go can
// decide, so neither the window nor the total exists until the rule has run.
// That path reuses ListMembers so the two listings cannot disagree about who is
// in the group.
func (s *GroupService) ListMembersPage(
	ctx context.Context,
	tenantID, groupID guid.ID,
	offset, limit int,
) ([]GroupMember, int64, error) {
	if tenantID.Zero() || !validID(groupID) {
		return nil, 0, ErrGroupInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	group, err := repo.getGroup(ctx, tenantID, groupID)
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	if group.GroupType == GroupTypeDynamic {
		matched, err := s.ListMembers(ctx, tenantID, groupID)
		if err != nil {
			return nil, 0, err
		}
		return pageOf(matched, offset, limit), int64(len(matched)), nil
	}
	rows, total, err := repo.listGroupMembersPage(ctx, tenantID, groupID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	items := make([]GroupMember, len(rows))
	for index := range rows {
		items[index] = groupMemberFromRow(&rows[index])
	}
	return items, total, nil
}

// ListPage returns one page of the tenant's invitations with the unfiltered
// total. Unlike List, whose ceiling of two hundred is fixed and silent, the
// window here is the one the caller asked for and the total says what it is a
// window into.
func (s *InvitationService) ListPage(
	ctx context.Context,
	tenantID guid.ID,
	offset, limit int,
) ([]Invitation, int64, error) {
	if tenantID.Zero() {
		return nil, 0, ErrInvitationInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	rows, total, err := repo.listInvitationsPage(ctx, tenantID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	items, err := listInvitations(ctx, repo, rows, organizationTime(s.now()))
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
