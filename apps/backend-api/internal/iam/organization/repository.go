package organization

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/iam/organization/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

var errRepositoryUnavailable = errors.New("organization repository unavailable")

type departmentRow = model.Department
type closureRow = model.DepartmentClosure

// Repository persists organization state through the runtime's current writer.
type Repository struct {
	runtime   *appenv.Runtime
	ids       guid.Source
	appender  audit.Appender
	revisions *core.RevisionStore
	db        *gorm.DB
	inTx      bool
}

func NewRepository(runtime *appenv.Runtime, ids guid.Source, appender audit.Appender) *Repository {
	if runtime == nil || ids == nil || appender == nil {
		panic("organization repository requires runtime, id generator, and audit appender")
	}
	revisions, err := core.NewRevisionStore(runtime.TablePrefix())
	if err != nil {
		panic(err)
	}
	return &Repository{runtime: runtime, ids: ids, appender: appender, revisions: revisions}
}

func (r *Repository) writer() *gorm.DB {
	if r == nil {
		return nil
	}
	if r.db != nil {
		return r.db
	}
	if r.runtime == nil || r.runtime.Database() == nil {
		return nil
	}
	return r.runtime.Database().Write()
}

func (r *Repository) prefix() string {
	if r == nil || r.runtime == nil {
		return ""
	}
	return r.runtime.TablePrefix()
}

func (r *Repository) table(name string) string { return r.prefix() + name }

func (r *Repository) withDB(db *gorm.DB, inTx bool) *Repository {
	clone := *r
	clone.db, clone.inTx = db, inTx
	return &clone
}

func (r *Repository) on(db *gorm.DB) (*Repository, error) {
	if r == nil || db == nil {
		return nil, errRepositoryUnavailable
	}
	return r.withDB(db, true), nil
}

func (r *Repository) pinWriter() (*Repository, error) {
	if r == nil {
		return nil, errRepositoryUnavailable
	}
	if r.db != nil {
		return r, nil
	}
	db := r.writer()
	if db == nil {
		return nil, errRepositoryUnavailable
	}
	return r.withDB(db, false), nil
}

func (r *Repository) runInTx(ctx context.Context, fn func(*Repository) error) error {
	if r == nil || fn == nil {
		return errors.New("invalid organization transaction")
	}
	if r.inTx {
		return fn(r)
	}
	db := r.writer()
	if db == nil {
		return errRepositoryUnavailable
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(r.withDB(tx, true)) })
}

func (r *Repository) nextID() (guid.ID, error) {
	if r == nil || r.ids == nil {
		return 0, errRepositoryUnavailable
	}
	id, err := r.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, errors.New("generated organization ID must be positive")
	}
	return guid.ID(id), nil
}

func (r *Repository) appendAudit(ctx context.Context, event audit.DomainEvent) error {
	if r == nil || r.appender == nil || r.writer() == nil {
		return errRepositoryUnavailable
	}
	if err := r.appender(ctx, r.writer(), event); err != nil {
		return fmt.Errorf("append organization audit event: %w", err)
	}
	return nil
}

func (r *Repository) bumpPolicyRevision(ctx context.Context, tenantID guid.ID, now time.Time) error {
	if r == nil || r.revisions == nil || r.writer() == nil {
		return errRepositoryUnavailable
	}
	return r.revisions.Bump(ctx, r.writer(), tenantID, now)
}

func (r *Repository) countRelationshipSubject(ctx context.Context, tenantID guid.ID, subjectType string, subjectID guid.ID) (int64, error) {
	var total int64
	for _, table := range []string{"iam_relationships", "iam_resource_relationships"} {
		var count int64
		if err := r.writer().WithContext(ctx).Table(r.table(table)).
			Where("tenant_id = ? AND subject_type = ? AND subject_id = ?", tenantID, subjectType, subjectID).Count(&count).Error; err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func (r *Repository) listDepartments(ctx context.Context, tenantID guid.ID) ([]departmentRow, error) {
	var rows []departmentRow
	err := r.writer().WithContext(ctx).Table(r.table(model.Department{}.TableName())).Where("tenant_id = ?", tenantID).Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list departments: %w", err)
	}
	return rows, nil
}

func (r *Repository) getDepartment(ctx context.Context, tenantID, id guid.ID) (*departmentRow, error) {
	var row departmentRow
	err := r.writer().WithContext(ctx).Table(r.table(row.TableName())).Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get department: %w", err)
	}
	return &row, nil
}

func (r *Repository) insertDepartment(ctx context.Context, row *departmentRow) error {
	if row.ParentID != nil {
		if _, err := r.getDepartment(ctx, row.TenantID, *row.ParentID); err != nil {
			return err
		}
	}
	db := r.writer().WithContext(ctx)
	if err := db.Table(r.table(row.TableName())).Create(row).Error; err != nil {
		if isSiblingNameViolation(err) {
			return ErrNameConflict
		}
		return fmt.Errorf("insert department: %w", err)
	}
	self := closureRow{TenantID: row.TenantID, AncestorID: row.ID, DescendantID: row.ID, Depth: 0}
	if err := db.Table(r.table(self.TableName())).Create(&self).Error; err != nil {
		return fmt.Errorf("insert department self closure: %w", err)
	}
	if row.ParentID == nil {
		return nil
	}
	ancestors, err := r.departmentAncestors(ctx, row.TenantID, *row.ParentID)
	if err != nil {
		return err
	}
	links := make([]closureRow, len(ancestors))
	for index, ancestor := range ancestors {
		links[index] = closureRow{TenantID: row.TenantID, AncestorID: ancestor.AncestorID, DescendantID: row.ID, Depth: ancestor.Depth + 1}
	}
	if len(links) > 0 {
		if err := db.Table(r.table(self.TableName())).Create(&links).Error; err != nil {
			return fmt.Errorf("insert department ancestor closures: %w", err)
		}
	}
	return nil
}

func (r *Repository) updateDepartment(ctx context.Context, row *departmentRow, expectedVersion int64) error {
	result := r.writer().WithContext(ctx).Table(r.table(row.TableName())).
		Where("tenant_id = ? AND id = ? AND version = ?", row.TenantID, row.ID, expectedVersion).
		Select("parent_id", "name", "name_key", "status", "sort_order", "version", "updated_at").Updates(row)
	if result.Error != nil {
		if isSiblingNameViolation(result.Error) {
			return ErrNameConflict
		}
		return fmt.Errorf("update department: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrVersionConflict
	}
	return nil
}

func (r *Repository) moveDepartment(ctx context.Context, tenantID, id guid.ID, parentID *guid.ID) error {
	subtree, err := r.departmentDescendants(ctx, tenantID, id)
	if err != nil {
		return err
	}
	oldAncestors, err := r.departmentAncestors(ctx, tenantID, id)
	if err != nil {
		return err
	}
	subtreeIDs := make([]guid.ID, len(subtree))
	for index, link := range subtree {
		subtreeIDs[index] = link.DescendantID
	}
	oldAncestorIDs := make([]guid.ID, 0, len(oldAncestors))
	for _, link := range oldAncestors {
		if link.AncestorID != id {
			oldAncestorIDs = append(oldAncestorIDs, link.AncestorID)
		}
	}
	db := r.writer().WithContext(ctx).Table(r.table(model.DepartmentClosure{}.TableName()))
	if len(oldAncestorIDs) > 0 {
		if err := db.Where("tenant_id = ? AND ancestor_id IN ? AND descendant_id IN ?", tenantID, oldAncestorIDs, subtreeIDs).Delete(&closureRow{}).Error; err != nil {
			return fmt.Errorf("remove old department closures: %w", err)
		}
	}
	if parentID == nil {
		return nil
	}
	newAncestors, err := r.departmentAncestors(ctx, tenantID, *parentID)
	if err != nil {
		return err
	}
	links := make([]closureRow, 0, len(newAncestors)*len(subtree))
	for _, ancestor := range newAncestors {
		for _, descendant := range subtree {
			links = append(links, closureRow{TenantID: tenantID, AncestorID: ancestor.AncestorID, DescendantID: descendant.DescendantID, Depth: ancestor.Depth + descendant.Depth + 1})
		}
	}
	if len(links) > 0 {
		if err := db.Create(&links).Error; err != nil {
			return fmt.Errorf("insert moved department closures: %w", err)
		}
	}
	return nil
}

func (r *Repository) deleteDepartment(ctx context.Context, tenantID, id guid.ID, version int64) error {
	db := r.writer().WithContext(ctx)
	var count int64
	if err := db.Table(r.table(model.Department{}.TableName())).Where("tenant_id = ? AND parent_id = ?", tenantID, id).Count(&count).Error; err != nil {
		return fmt.Errorf("count department children: %w", err)
	}
	if count > 0 {
		return ErrHasChildren
	}
	names := query.BindNames(r.prefix())
	for _, table := range []gormx.TableName{names.MemberDepartments, names.RoleScopeDepartments} {
		if err := db.Table(table.String()).Where("tenant_id = ? AND department_id = ?", tenantID, id).Count(&count).Error; err != nil {
			return fmt.Errorf("check department references in %s: %w", table.Logical(), err)
		}
		if count > 0 {
			return ErrInUse
		}
	}
	result := db.Table(r.table(model.Department{}.TableName())).Where("tenant_id = ? AND id = ? AND version = ?", tenantID, id, version).Delete(&departmentRow{})
	if result.Error != nil {
		return fmt.Errorf("delete department: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrVersionConflict
	}
	return nil
}

func (r *Repository) isDepartmentDescendant(ctx context.Context, tenantID, ancestorID, descendantID guid.ID) (bool, error) {
	var count int64
	err := r.writer().WithContext(ctx).Table(r.table(model.DepartmentClosure{}.TableName())).
		Where("tenant_id = ? AND ancestor_id = ? AND descendant_id = ?", tenantID, ancestorID, descendantID).Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("check department hierarchy: %w", err)
	}
	return count > 0, nil
}

func (r *Repository) departmentAncestors(ctx context.Context, tenantID, descendantID guid.ID) ([]closureRow, error) {
	var rows []closureRow
	err := r.writer().WithContext(ctx).Table(r.table(model.DepartmentClosure{}.TableName())).
		Where("tenant_id = ? AND descendant_id = ?", tenantID, descendantID).Order("depth ASC, ancestor_id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list department ancestors: %w", err)
	}
	return rows, nil
}

func (r *Repository) departmentDescendants(ctx context.Context, tenantID, ancestorID guid.ID) ([]closureRow, error) {
	var rows []closureRow
	err := r.writer().WithContext(ctx).Table(r.table(model.DepartmentClosure{}.TableName())).
		Where("tenant_id = ? AND ancestor_id = ?", tenantID, ancestorID).Order("depth ASC, descendant_id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list department descendants: %w", err)
	}
	return rows, nil
}

func orderDepartments(rows []departmentRow) ([]Department, error) {
	children := make(map[guid.ID][]departmentRow, len(rows))
	for _, row := range rows {
		children[optionalID(row.ParentID)] = append(children[optionalID(row.ParentID)], row)
	}
	for parentID := range children {
		sort.Slice(children[parentID], func(i, j int) bool {
			left, right := children[parentID][i], children[parentID][j]
			if left.SortOrder != right.SortOrder {
				return left.SortOrder < right.SortOrder
			}
			if left.NameKey != right.NameKey {
				return left.NameKey < right.NameKey
			}
			return left.ID < right.ID
		})
	}
	result := make([]Department, 0, len(rows))
	visited := make(map[guid.ID]bool, len(rows))
	var visit func(guid.ID, int)
	visit = func(parentID guid.ID, depth int) {
		for _, row := range children[parentID] {
			if visited[row.ID] {
				continue
			}
			visited[row.ID] = true
			result = append(result, departmentFromRow(&row, depth))
			visit(row.ID, depth+1)
		}
	}
	visit(0, 0)
	if len(result) != len(rows) {
		return nil, ErrHierarchyCorrupt
	}
	return result, nil
}

func departmentFromRow(row *departmentRow, depth int) Department {
	return Department{ID: row.ID, TenantID: row.TenantID, ParentID: optionalID(row.ParentID), Name: row.Name, Status: row.Status, SortOrder: row.SortOrder, Depth: depth, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func optionalID(value *guid.ID) guid.ID {
	if value == nil {
		return 0
	}
	return *value
}

func idOrNil(value guid.ID) *guid.ID {
	if value.Zero() {
		return nil
	}
	return &value
}

func isSiblingNameViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sibling_name") || strings.Contains(message, "root_name") || strings.Contains(message, "parent") && strings.Contains(message, "name_key")
}
