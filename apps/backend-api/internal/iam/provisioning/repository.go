package provisioning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/provisioning/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"gorm.io/gorm"
)

type directoryRow = model.Directory
type credentialRow = model.Credential
type resourceRow = model.Resource
type targetRow = model.Target
type targetResourceRow = model.TargetResource

type credentialAuthRow struct {
	model.Credential `gorm:"embedded"`
	TenantID         guid.ID `gorm:"column:tenant_id"`
	DirectoryStatus  string  `gorm:"column:directory_status"`
	TenantStatus     string  `gorm:"column:tenant_status"`
	PrincipalID      guid.ID `gorm:"column:service_account_id"`
}

func (row credentialAuthRow) active(now time.Time) bool {
	return row.TenantStatus == "active" && row.DirectoryStatus == DirectoryActive &&
		row.RevokedAt == nil && (row.ExpiresAt == nil || row.ExpiresAt.After(now))
}

// authorizeSCIM revalidates the authenticated tuple at the service boundary,
// including credentials retained by a caller after revocation or suspension.
func (r *Repository) authorizeSCIM(ctx context.Context, auth AuthContext, now time.Time, actors identity.ServiceAccountValidator) error {
	if auth.TenantID <= 0 || auth.DirectoryID <= 0 || auth.CredentialID <= 0 || auth.PrincipalID <= 0 {
		return ErrUnauthorized
	}
	pinned, err := r.pinWriter()
	if err != nil {
		return err
	}
	row, err := pinned.credentialForAuth(ctx, auth.CredentialID, auth.TenantID, auth.DirectoryID)
	if err != nil {
		return err
	}
	if row.TenantID != auth.TenantID || row.DirectoryID != auth.DirectoryID || row.PrincipalID != auth.PrincipalID || !row.active(now) {
		return ErrUnauthorized
	}
	if row.PrincipalID > 0 {
		return pinned.requireServiceAccount(ctx, row.TenantID, &row.PrincipalID, now, actors)
	}
	return nil
}

func (r *Repository) requireServiceAccount(ctx context.Context, tenantID guid.ID, id *guid.ID, now time.Time, actors identity.ServiceAccountValidator) error {
	if id == nil {
		return nil
	}
	if *id <= 0 {
		return ErrUnauthorized
	}
	if actors == nil {
		return errRepositoryUnavailable
	}
	pinned, err := r.pinWriter()
	if err != nil {
		return err
	}
	active, err := actors(ctx, pinned.writer(), tenantID, *id, now)
	if err != nil {
		return err
	}
	if !active {
		return ErrUnauthorized
	}
	return nil
}

type userRecord struct {
	model.Resource   `gorm:"embedded"`
	LoginName        string `gorm:"column:login_name"`
	Email            string `gorm:"column:email"`
	DisplayName      string `gorm:"column:display_name"`
	PrincipalStatus  string `gorm:"column:principal_status"`
	MembershipStatus string `gorm:"column:membership_status"`
}

type groupRecord struct {
	model.Resource `gorm:"embedded"`
	Name           string `gorm:"column:name"`
	Status         string `gorm:"column:status"`
}

var errRepositoryUnavailable = errors.New("provisioning repository unavailable")

type Repository struct {
	runtime *appenv.Runtime
	db      *gorm.DB
	inTx    bool
	// foreign holds the names of the tables this repository reads but does
	// not own. It is a pointer so a transactional clone reads the same
	// publication as the repository it came from.
	foreign *foreignTables
}

func NewRepository(runtime *appenv.Runtime) *Repository {
	if runtime == nil {
		panic("provisioning repository requires runtime")
	}
	return &Repository{runtime: runtime, foreign: &foreignTables{}}
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
		return errors.New("invalid provisioning transaction")
	}
	if r.inTx {
		return fn(r)
	}
	pinned, err := r.pinWriter()
	if err != nil {
		return err
	}
	return pinned.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(pinned.withDB(tx, true))
	})
}

func (r *Repository) listTargets(ctx context.Context, tenantID guid.ID) ([]targetRow, error) {
	rows := make([]targetRow, 0)
	err := r.writer().WithContext(ctx).Table(r.table(model.Target{}.TableName())).
		Where("tenant_id = ?", tenantID).Order("name_key ASC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list SCIM targets: %w", err)
	}
	return rows, nil
}

func (r *Repository) getTarget(ctx context.Context, tenantID, id guid.ID) (targetRow, error) {
	var row targetRow
	err := r.writer().WithContext(ctx).Table(r.table(model.Target{}.TableName())).
		Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrTargetMissing
	}
	if err != nil {
		return row, fmt.Errorf("get SCIM target: %w", err)
	}
	return row, nil
}

func (r *Repository) insertTarget(ctx context.Context, row *targetRow) error {
	err := r.writer().WithContext(ctx).Table(r.table(model.Target{}.TableName())).Create(row).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrTargetName
	}
	if err != nil {
		return fmt.Errorf("insert SCIM target: %w", err)
	}
	return nil
}

func (r *Repository) replaceTarget(ctx context.Context, row *targetRow, expectedVersion int64) error {
	result := r.writer().WithContext(ctx).Table(r.table(model.Target{}.TableName())).
		Where("tenant_id = ? AND id = ? AND version = ?", row.TenantID, row.ID, expectedVersion).
		Select("name", "name_key", "base_url", "bearer_token_ciphertext", "status", "version", "updated_at", "service_account_id").Updates(row)
	if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
		return ErrTargetName
	}
	if result.Error != nil {
		return fmt.Errorf("replace SCIM target: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrTargetVersion
	}
	return nil
}

func (r *Repository) deleteTarget(ctx context.Context, tenantID, id guid.ID) error {
	result := r.writer().WithContext(ctx).Table(r.table(model.Target{}.TableName())).
		Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&targetRow{})
	if result.Error != nil {
		return fmt.Errorf("delete SCIM target: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrTargetMissing
	}
	return nil
}

// countLiveTargetResources counts the mappings that still name a resource on
// the remote side. The predicate is deliberately the one ErasePrincipalData
// reads: if the two ever disagree, the mapping this count overlooks is the
// one erasure would let through.
func (r *Repository) countLiveTargetResources(ctx context.Context, targetID guid.ID) (int64, error) {
	var count int64
	err := r.writer().WithContext(ctx).Table(r.table(model.TargetResource{}.TableName())).
		Where("target_id = ? AND deleted_at IS NULL AND external_id <> ''", targetID).Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count live SCIM target resource mappings: %w", err)
	}
	return count, nil
}

func (r *Repository) listTargetResources(ctx context.Context, targetID guid.ID) ([]targetResourceRow, error) {
	rows := make([]targetResourceRow, 0)
	err := r.writer().WithContext(ctx).Table(r.table(model.TargetResource{}.TableName())).
		Where("target_id = ?", targetID).Order("resource_type ASC, resource_id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list SCIM target resources: %w", err)
	}
	return rows, nil
}

func (r *Repository) getTargetResource(ctx context.Context, targetID guid.ID, resourceType string, resourceID guid.ID) (targetResourceRow, error) {
	var row targetResourceRow
	err := r.writer().WithContext(ctx).Table(r.table(model.TargetResource{}.TableName())).
		Where("target_id = ? AND resource_type = ? AND resource_id = ?", targetID, resourceType, resourceID).Take(&row).Error
	return row, err
}

func (r *Repository) upsertTargetResource(ctx context.Context, row *targetResourceRow) error {
	existing, err := r.getTargetResource(ctx, row.TargetID, row.ResourceType, row.ResourceID)
	switch {
	case err == nil:
		existing.ExternalID = row.ExternalID
		existing.DeletedAt = nil
		existing.Version++
		existing.UpdatedAt = row.UpdatedAt
		result := r.writer().WithContext(ctx).Table(r.table(model.TargetResource{}.TableName())).
			Where("target_id = ? AND resource_type = ? AND resource_id = ? AND version = ?", existing.TargetID, existing.ResourceType, existing.ResourceID, existing.Version-1).
			Select("external_id", "deleted_at", "version", "updated_at").Updates(&existing)
		if result.Error != nil {
			return fmt.Errorf("update SCIM target resource: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrTargetVersion
		}
		*row = existing
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := r.writer().WithContext(ctx).Table(r.table(model.TargetResource{}.TableName())).Create(row).Error; err != nil {
			return fmt.Errorf("insert SCIM target resource: %w", err)
		}
	default:
		return err
	}
	return nil
}

func (r *Repository) markTargetResourceDeleted(ctx context.Context, targetID guid.ID, resourceType string, resourceID guid.ID, at time.Time) error {
	result := r.writer().WithContext(ctx).Table(r.table(model.TargetResource{}.TableName())).
		Where("target_id = ? AND resource_type = ? AND resource_id = ? AND deleted_at IS NULL", targetID, resourceType, resourceID).
		Updates(map[string]any{"deleted_at": at, "updated_at": at})
	if result.Error != nil {
		return fmt.Errorf("mark SCIM target resource deleted: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrDeprovisionMissing
	}
	return nil
}

func (r *Repository) listDirectories(ctx context.Context, tenantID guid.ID) ([]directoryRow, error) {
	rows := make([]directoryRow, 0)
	err := r.writer().WithContext(ctx).Table(r.table(model.Directory{}.TableName())).
		Where("tenant_id = ?", tenantID).Order("name_key ASC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list SCIM directories: %w", err)
	}
	return rows, nil
}

func (r *Repository) getDirectory(ctx context.Context, tenantID, id guid.ID) (directoryRow, error) {
	var row directoryRow
	err := r.writer().WithContext(ctx).Table(r.table(model.Directory{}.TableName())).
		Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrDirectoryMissing
	}
	if err != nil {
		return row, fmt.Errorf("get SCIM directory: %w", err)
	}
	return row, nil
}

func (r *Repository) insertDirectory(ctx context.Context, row *directoryRow) error {
	err := r.writer().WithContext(ctx).Table(r.table(model.Directory{}.TableName())).Create(row).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrDirectoryName
	}
	if err != nil {
		return fmt.Errorf("insert SCIM directory: %w", err)
	}
	return nil
}

func (r *Repository) updateDirectory(ctx context.Context, row *directoryRow, expectedVersion int64) error {
	result := r.writer().WithContext(ctx).Table(r.table(model.Directory{}.TableName())).
		Where("tenant_id = ? AND id = ? AND version = ?", row.TenantID, row.ID, expectedVersion).
		Select("name", "name_key", "status", "version", "updated_at", "service_account_id").Updates(row)
	if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
		return ErrDirectoryName
	}
	if result.Error != nil {
		return fmt.Errorf("update SCIM directory: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrDirectoryVersion
	}
	return nil
}

func (r *Repository) listCredentials(ctx context.Context, directoryID guid.ID) ([]credentialRow, error) {
	rows := make([]credentialRow, 0)
	err := r.writer().WithContext(ctx).Table(r.table(model.Credential{}.TableName())).
		Where("directory_id = ?", directoryID).Order("created_at DESC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list SCIM credentials: %w", err)
	}
	return rows, nil
}

func (r *Repository) activeCredentialCount(ctx context.Context, directoryID guid.ID, now time.Time) (int64, error) {
	var count int64
	err := r.writer().WithContext(ctx).Table(r.table(model.Credential{}.TableName())).
		Where("directory_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", directoryID, now).Count(&count).Error
	return count, err
}

func (r *Repository) insertCredential(ctx context.Context, row *credentialRow) error {
	if err := r.writer().WithContext(ctx).Table(r.table(model.Credential{}.TableName())).Create(row).Error; err != nil {
		return fmt.Errorf("insert SCIM credential: %w", err)
	}
	return nil
}

func (r *Repository) revokeCredential(ctx context.Context, directoryID, id guid.ID, now time.Time) (bool, error) {
	result := r.writer().WithContext(ctx).Table(r.table(model.Credential{}.TableName())).
		Where("directory_id = ? AND id = ? AND revoked_at IS NULL", directoryID, id).Update("revoked_at", now)
	if result.Error != nil {
		return false, fmt.Errorf("revoke SCIM credential: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		return true, nil
	}
	var count int64
	err := r.writer().WithContext(ctx).Table(r.table(model.Credential{}.TableName())).
		Where("directory_id = ? AND id = ?", directoryID, id).Count(&count).Error
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, ErrCredentialMissing
	}
	return false, nil
}

// Initial bearer authentication has no trusted scope yet; subsequent checks
// bind the resolved tenant and directory in SQL as well as validating the row.
func (r *Repository) credentialForAuth(ctx context.Context, id, tenantID, directoryID guid.ID) (credentialAuthRow, error) {
	var row credentialAuthRow
	credentialTable := r.table(model.Credential{}.TableName())
	directoryTable := r.table(model.Directory{}.TableName())
	tenants, err := r.foreign.directoryTable("iam_tenants")
	if err != nil {
		return row, err
	}
	query := r.writer().WithContext(ctx).Table(credentialTable+" AS credential").
		Select("credential.*, directory.tenant_id AS tenant_id, directory.status AS directory_status, tenant.status AS tenant_status, COALESCE(directory.service_account_id, 0) AS service_account_id").
		Joins("JOIN "+directoryTable+" AS directory ON directory.id = credential.directory_id").
		Joins("JOIN "+tenants+" AS tenant ON tenant.id = directory.tenant_id").
		Where("credential.id = ?", id)
	if tenantID > 0 {
		query = query.Where("tenant.id = ? AND directory.tenant_id = ?", tenantID, tenantID)
	}
	if directoryID > 0 {
		query = query.Where("directory.id = ? AND credential.directory_id = ?", directoryID, directoryID)
	}
	err = query.Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrUnauthorized
	}
	if err != nil {
		return row, fmt.Errorf("read SCIM credential: %w", err)
	}
	return row, nil
}

func (r *Repository) touchCredential(ctx context.Context, id guid.ID, now time.Time) error {
	cutoff := now.Add(-5 * time.Minute)
	return r.writer().WithContext(ctx).Table(r.table(model.Credential{}.TableName())).
		Where("id = ? AND revoked_at IS NULL AND (last_used_at IS NULL OR last_used_at < ?)", id, cutoff).
		Update("last_used_at", now).Error
}

func (r *Repository) getResource(ctx context.Context, directoryID guid.ID, resourceType string, resourceID guid.ID, includeDeleted bool) (resourceRow, error) {
	var row resourceRow
	query := r.writer().WithContext(ctx).Table(r.table(model.Resource{}.TableName())).
		Where("directory_id = ? AND resource_type = ? AND resource_id = ?", directoryID, resourceType, resourceID)
	if !includeDeleted {
		query = query.Where("deleted_at IS NULL")
	}
	err := query.Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrResourceMissing
	}
	if err != nil {
		return row, fmt.Errorf("get SCIM resource mapping: %w", err)
	}
	return row, nil
}

func (r *Repository) getResourceByExternalKey(ctx context.Context, directoryID guid.ID, resourceType, key string) (resourceRow, error) {
	var row resourceRow
	err := r.writer().WithContext(ctx).Table(r.table(model.Resource{}.TableName())).
		Where("directory_id = ? AND resource_type = ? AND external_key = ?", directoryID, resourceType, key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrResourceMissing
	}
	if err != nil {
		return row, fmt.Errorf("get SCIM resource mapping by external key: %w", err)
	}
	return row, nil
}

func (r *Repository) insertResource(ctx context.Context, row *resourceRow) error {
	err := r.writer().WithContext(ctx).Table(r.table(model.Resource{}.TableName())).Create(row).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrResourceConflict
	}
	if err != nil {
		return fmt.Errorf("insert SCIM resource mapping: %w", err)
	}
	return nil
}

func (r *Repository) updateResource(ctx context.Context, row *resourceRow, expectedVersion int64) error {
	result := r.writer().WithContext(ctx).Table(r.table(model.Resource{}.TableName())).
		Where("directory_id = ? AND resource_type = ? AND resource_id = ? AND version = ?", row.DirectoryID, row.ResourceType, row.ResourceID, expectedVersion).
		Select("external_id", "external_key", "version", "updated_at", "deleted_at").Updates(row)
	if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
		return ErrResourceConflict
	}
	if result.Error != nil {
		return fmt.Errorf("update SCIM resource mapping: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrResourceVersion
	}
	return nil
}

func (r *Repository) user(ctx context.Context, directoryID, resourceID guid.ID) (userRecord, error) {
	var row userRecord
	query, err := r.userQuery(directoryID, false)
	if err != nil {
		return row, err
	}
	err = query.WithContext(ctx).Where("resource.resource_id = ?", resourceID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrResourceMissing
	}
	if err != nil {
		return row, fmt.Errorf("get SCIM user: %w", err)
	}
	return row, nil
}

func (r *Repository) userPage(ctx context.Context, directoryID guid.ID, request ListRequest, dialect string) ([]userRecord, int, error) {
	base, err := r.userQuery(directoryID, false)
	if err != nil {
		return nil, 0, err
	}
	query, err := applySCIMFilter(base, request.Filter, ResourceUser, dialect, r.foreign)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err := query.WithContext(ctx).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count SCIM users: %w", err)
	}
	if request.Count == 0 {
		return []userRecord{}, int(total), nil
	}
	base, err = r.userQuery(directoryID, false)
	if err != nil {
		return nil, 0, err
	}
	query, err = applySCIMFilter(base, request.Filter, ResourceUser, dialect, r.foreign)
	if err != nil {
		return nil, 0, err
	}
	rows := make([]userRecord, 0, request.Count)
	err = query.WithContext(ctx).Order("principal.login_name ASC, resource.resource_id ASC").Offset(request.StartIndex - 1).Limit(request.Count).Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list SCIM users: %w", err)
	}
	return rows, int(total), nil
}

func (r *Repository) userQuery(directoryID guid.ID, includeDeleted bool) (*gorm.DB, error) {
	resourceTable := r.table(model.Resource{}.TableName())
	directoryTable := r.table(model.Directory{}.TableName())
	principals, err := r.foreign.principalTable("iam_principals")
	if err != nil {
		return nil, err
	}
	members, err := r.foreign.accessTable("iam_tenant_members")
	if err != nil {
		return nil, err
	}
	query := r.writer().Table(resourceTable+" AS resource").
		Select("resource.*, principal.login_name, principal.email, principal.display_name, principal.status AS principal_status, member.status AS membership_status").
		Joins("JOIN "+directoryTable+" AS directory ON directory.id = resource.directory_id").
		Joins("JOIN "+principals+" AS principal ON principal.id = resource.resource_id").
		Joins("JOIN "+members+" AS member ON member.tenant_id = directory.tenant_id AND member.principal_id = resource.resource_id").
		Where("resource.directory_id = ? AND resource.resource_type = ?", directoryID, ResourceUser)
	if !includeDeleted {
		query = query.Where("resource.deleted_at IS NULL")
	}
	return query, nil
}

func (r *Repository) group(ctx context.Context, directoryID, resourceID guid.ID) (groupRecord, error) {
	var row groupRecord
	query, err := r.groupQuery(directoryID, false)
	if err != nil {
		return row, err
	}
	err = query.WithContext(ctx).Where("resource.resource_id = ?", resourceID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrResourceMissing
	}
	if err != nil {
		return row, fmt.Errorf("get SCIM group: %w", err)
	}
	return row, nil
}

func (r *Repository) groupPage(ctx context.Context, directoryID guid.ID, request ListRequest, dialect string) ([]groupRecord, int, error) {
	base, err := r.groupQuery(directoryID, false)
	if err != nil {
		return nil, 0, err
	}
	query, err := applySCIMFilter(base, request.Filter, ResourceGroup, dialect, r.foreign)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err := query.WithContext(ctx).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count SCIM groups: %w", err)
	}
	if request.Count == 0 {
		return []groupRecord{}, int(total), nil
	}
	base, err = r.groupQuery(directoryID, false)
	if err != nil {
		return nil, 0, err
	}
	query, err = applySCIMFilter(base, request.Filter, ResourceGroup, dialect, r.foreign)
	if err != nil {
		return nil, 0, err
	}
	rows := make([]groupRecord, 0, request.Count)
	err = query.WithContext(ctx).Order("scim_group.name_key ASC, resource.resource_id ASC").Offset(request.StartIndex - 1).Limit(request.Count).Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list SCIM groups: %w", err)
	}
	return rows, int(total), nil
}

func (r *Repository) groupQuery(directoryID guid.ID, includeDeleted bool) (*gorm.DB, error) {
	resourceTable := r.table(model.Resource{}.TableName())
	directoryTable := r.table(model.Directory{}.TableName())
	groups, err := r.foreign.directoryTable("iam_groups")
	if err != nil {
		return nil, err
	}
	query := r.writer().Table(resourceTable+" AS resource").
		Select("resource.*, scim_group.name, scim_group.status").
		Joins("JOIN "+directoryTable+" AS directory ON directory.id = resource.directory_id").
		Joins("JOIN "+groups+" AS scim_group ON scim_group.tenant_id = directory.tenant_id AND scim_group.id = resource.resource_id").
		Where("resource.directory_id = ? AND resource.resource_type = ?", directoryID, ResourceGroup)
	if !includeDeleted {
		query = query.Where("resource.deleted_at IS NULL")
	}
	return query, nil
}

func (r *Repository) groupMembers(ctx context.Context, directoryID, groupID guid.ID) ([]guid.ID, error) {
	ids := make([]guid.ID, 0)
	members, err := r.foreign.directoryTable("iam_group_members")
	if err != nil {
		return nil, err
	}
	err = r.writer().WithContext(ctx).Table(members+" AS member").Select("member.principal_id").
		Joins("JOIN "+r.table(model.Directory{}.TableName())+" AS directory ON directory.tenant_id = member.tenant_id").
		Joins("JOIN "+r.table(model.Resource{}.TableName())+" AS resource ON resource.directory_id = directory.id AND resource.resource_type = ? AND resource.resource_id = member.principal_id AND resource.deleted_at IS NULL", ResourceUser).
		Where("directory.id = ? AND member.group_id = ?", directoryID, groupID).Order("member.principal_id ASC").Scan(&ids).Error
	if err != nil {
		return nil, fmt.Errorf("list SCIM group members: %w", err)
	}
	return ids, nil
}

func externalKey(externalID string, resourceID guid.ID) string {
	if externalID == "" {
		return "id:" + resourceID.String()
	}
	digest := sha256.Sum256([]byte(externalID))
	return "ext:" + hex.EncodeToString(digest[:])
}
