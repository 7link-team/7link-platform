package provisioning

import (
	"context"
	"fmt"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// maxPageLimit is the widest window the repository will honor. It mirrors the
// maximum the list inputs declare, so a service caller cannot ask for more than
// an HTTP caller is allowed to.
const maxPageLimit = 200

// normalizePage clamps a requested window into what the repository will honor.
// A zero limit still means "no limit" here, which is why the HTTP inputs declare
// a minimum of one and a default: the bound a caller is held to has to come from
// the transport, where the caller actually is.
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

// pageQuery applies a normalized window to a listing. A zero limit is left
// unbounded, so the clamp in normalizePage is the only place the maximum lives.
func pageQuery(listing *gorm.DB, offset, limit int) *gorm.DB {
	if limit > 0 {
		listing = listing.Limit(limit)
	}
	if offset > 0 {
		listing = listing.Offset(offset)
	}
	return listing
}

// listDirectoriesPage returns one page of a tenant's SCIM directories in the
// order listDirectories uses, plus the tenant total before pagination.
func (r *Repository) listDirectoriesPage(ctx context.Context, tenantID guid.ID, offset, limit int) ([]directoryRow, int64, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	table := tables.Directories.TableName()
	var total int64
	if err := db.WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count SCIM directories: %w", err)
	}
	listing := db.WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Order("name_key ASC, id ASC")
	rows := make([]directoryRow, 0)
	if err := pageQuery(listing, offset, limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list SCIM directories: %w", err)
	}
	return rows, total, nil
}

// listTargetsPage returns one page of a tenant's outbound SCIM targets and the
// tenant total before pagination.
func (r *Repository) listTargetsPage(ctx context.Context, tenantID guid.ID, offset, limit int) ([]targetRow, int64, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	table := tables.Targets.TableName()
	var total int64
	if err := db.WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count SCIM targets: %w", err)
	}
	listing := db.WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Order("name_key ASC, id ASC")
	rows := make([]targetRow, 0)
	if err := pageQuery(listing, offset, limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list SCIM targets: %w", err)
	}
	return rows, total, nil
}

// listCredentialsPage returns one page of a directory's credentials and the
// directory total before pagination.
func (r *Repository) listCredentialsPage(ctx context.Context, directoryID guid.ID, offset, limit int) ([]credentialRow, int64, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	table := tables.Credentials.TableName()
	var total int64
	if err := db.WithContext(ctx).Table(table).Where("directory_id = ?", directoryID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count SCIM credentials: %w", err)
	}
	listing := db.WithContext(ctx).Table(table).Where("directory_id = ?", directoryID).Order("created_at DESC, id ASC")
	rows := make([]credentialRow, 0)
	if err := pageQuery(listing, offset, limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list SCIM credentials: %w", err)
	}
	return rows, total, nil
}

// ListDirectoriesPage returns one page of the tenant's SCIM directories with the
// unfiltered tenant total. ListDirectories stays for internal callers that need
// the whole registry.
func (s *Service) ListDirectoriesPage(ctx context.Context, tenantID guid.ID, offset, limit int) ([]Directory, int64, error) {
	if tenantID.Zero() {
		return nil, 0, ErrInvalidDirectory
	}
	rows, total, err := s.repo.listDirectoriesPage(ctx, tenantID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	result := make([]Directory, 0, len(rows))
	for _, row := range rows {
		result = append(result, directoryFromRow(row))
	}
	return result, total, nil
}

// ListTargetsPage returns one page of the tenant's outbound SCIM targets with
// the unfiltered tenant total.
func (s *Service) ListTargetsPage(ctx context.Context, tenantID guid.ID, offset, limit int) ([]Target, int64, error) {
	if tenantID.Zero() {
		return nil, 0, ErrInvalidTarget
	}
	rows, total, err := s.repo.listTargetsPage(ctx, tenantID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	result := make([]Target, 0, len(rows))
	for _, row := range rows {
		result = append(result, targetFromRow(row))
	}
	return result, total, nil
}

// ListCredentialsPage returns one page of a directory's credentials with the
// unfiltered total. The directory is resolved first so an unknown directory is
// reported as missing rather than as an empty credential set.
func (s *Service) ListCredentialsPage(ctx context.Context, tenantID, directoryID guid.ID, offset, limit int) ([]Credential, int64, error) {
	if _, err := s.repo.getDirectory(ctx, tenantID, directoryID); err != nil {
		return nil, 0, err
	}
	rows, total, err := s.repo.listCredentialsPage(ctx, directoryID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	result := make([]Credential, 0, len(rows))
	for _, row := range rows {
		result = append(result, credentialFromRow(row))
	}
	return result, total, nil
}
