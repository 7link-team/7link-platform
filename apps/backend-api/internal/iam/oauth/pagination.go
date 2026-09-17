package oauth

import (
	"context"
	"fmt"

	"7link.com/platform/internal/iam/oauth/model"
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

// listClientsPage returns one page of a tenant's OAuth clients in the order
// listClients uses, plus the tenant total before pagination.
func (r *Repository) listClientsPage(ctx context.Context, tenantID guid.ID, offset, limit int) ([]*model.Client, int64, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	table := tables.Clients.TableName()
	var total int64
	if err := db.WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count OAuth clients: %w", err)
	}
	listing := db.WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Order("name ASC, id ASC")
	rows := make([]*model.Client, 0)
	if err := pageQuery(listing, offset, limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list OAuth clients: %w", err)
	}
	return rows, total, nil
}

// ListClientsPage returns one page of the tenant's OAuth clients with the
// unfiltered tenant total. ListClients stays for callers that need the whole
// registry in one call.
func (s *Service) ListClientsPage(ctx context.Context, tenantID guid.ID, offset, limit int) ([]Client, int64, error) {
	if tenantID.Zero() {
		return nil, 0, ErrInvalidRequest
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	rows, total, err := repo.listClientsPage(ctx, tenantID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	clients := make([]Client, len(rows))
	for index, row := range rows {
		clients[index] = clientFromRow(row)
	}
	return clients, total, nil
}
