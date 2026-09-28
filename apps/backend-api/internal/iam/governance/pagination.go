package governance

import (
	"context"
	"fmt"

	"7link.com/platform/internal/iam/governance/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// maxPageLimit is the widest window these listings will honor. It mirrors the
// maximum the list inputs declare, so a service caller cannot ask for more than
// an HTTP caller is allowed to.
const maxPageLimit = 200

// normalizePage clamps a requested window into what the listings will honor. A
// zero limit still means "no limit" here, which is why the HTTP inputs declare a
// minimum of one and a default: the bound a caller is held to has to come from
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

// RequestableRolesPage returns one page of the roles a tenant member may request
// and the tenant total before pagination. RequestableRoles stays for callers
// that need the whole set in one call.
func (s *Service) RequestableRolesPage(ctx context.Context, tenantID guid.ID, offset, limit int) ([]RequestableRole, int64, error) {
	if tenantID.Zero() || s == nil || s.repo == nil {
		return nil, 0, ErrInvalid
	}
	db := s.repo.writer()
	if db == nil {
		return nil, 0, errRepositoryUnavailable
	}
	offset, limit = normalizePage(offset, limit)
	roleTable := s.repo.foreign.access.Roles.String()
	var total int64
	if err := db.WithContext(ctx).Table(roleTable).Where("tenant_id = ?", tenantID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count requestable roles: %w", err)
	}
	listing := db.WithContext(ctx).Table(roleTable).
		Select("id, name, description").Where("tenant_id = ?", tenantID).Order("name ASC, id ASC")
	roles := make([]RequestableRole, 0)
	if err := pageQuery(listing, offset, limit).Find(&roles).Error; err != nil {
		return nil, 0, fmt.Errorf("list requestable roles: %w", err)
	}
	return roles, total, nil
}

// ListPage returns one page of the tenant's access requests and the total before
// pagination, narrowed to one requester when requesterID is set. List stays for
// callers that want the fixed ceiling rather than a window.
func (s *Service) ListPage(ctx context.Context, tenantID, requesterID guid.ID, offset, limit int) ([]AccessRequest, int64, error) {
	if tenantID.Zero() || s == nil || s.repo == nil {
		return nil, 0, ErrInvalid
	}
	db := s.repo.writer()
	if db == nil {
		return nil, 0, errRepositoryUnavailable
	}
	offset, limit = normalizePage(offset, limit)
	scope := func() *gorm.DB {
		query := db.WithContext(ctx).Table(s.repo.tables(db).AccessRequests.TableName()).Where("tenant_id = ?", tenantID)
		if !requesterID.Zero() {
			query = query.Where("requester_id = ?", requesterID)
		}
		return query
	}
	var total int64
	if err := scope().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count access requests: %w", err)
	}
	rows := make([]model.AccessRequest, 0)
	if err := pageQuery(scope().Order("created_at DESC, id DESC"), offset, limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list access requests: %w", err)
	}
	now := s.now().UTC()
	items := make([]AccessRequest, 0, len(rows))
	for _, row := range rows {
		items = append(items, requestFromRow(row, now))
	}
	return items, total, nil
}

// ListReviewsPage returns one page of the tenant's access review campaigns and
// the total before pagination. The item counts are joined per review exactly as
// ListReviews does, so a page carries the same progress figures as the whole
// listing.
func (s *Service) ListReviewsPage(ctx context.Context, tenantID guid.ID, offset, limit int) ([]AccessReview, int64, error) {
	if tenantID.Zero() || s == nil || s.repo == nil {
		return nil, 0, ErrInvalidReview
	}
	db := s.repo.writer()
	if db == nil {
		return nil, 0, errRepositoryUnavailable
	}
	offset, limit = normalizePage(offset, limit)
	table := s.repo.tables(db).AccessReviews.TableName()
	var total int64
	if err := db.WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count access reviews: %w", err)
	}
	listing := db.WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Order("created_at DESC, id DESC")
	rows := make([]model.AccessReview, 0)
	if err := pageQuery(listing, offset, limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list access reviews: %w", err)
	}
	counts, err := s.repo.reviewCounts(ctx, tenantID)
	if err != nil {
		return nil, 0, err
	}
	now := s.now().UTC()
	reviews := make([]AccessReview, 0, len(rows))
	for _, row := range rows {
		review := reviewFromRows(row, nil, now)
		if count, ok := counts[row.ID]; ok {
			review.Total, review.Pending, review.Kept, review.Revoked = count.Total, count.Pending, count.Kept, count.Revoked
		}
		reviews = append(reviews, review)
	}
	return reviews, total, nil
}
