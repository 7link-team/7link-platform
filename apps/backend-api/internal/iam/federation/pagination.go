package federation

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

// listProvidersPage returns one page of a tenant's identity providers in the
// order listProviders uses, plus the tenant total before pagination.
func (r *Repository) listProvidersPage(ctx context.Context, tenantID guid.ID, offset, limit int) ([]providerRow, int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	table := tables.Providers.TableName()
	var total int64
	if err := r.writer().WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count identity providers: %w", err)
	}
	listing := r.writer().WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Order("name ASC, id ASC")
	rows := make([]providerRow, 0)
	if err := pageQuery(listing, offset, limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list identity providers: %w", err)
	}
	return rows, total, nil
}

// listSAMLServiceProvidersPage returns one page of a tenant's SAML service
// provider registry and the tenant total before pagination.
func (r *Repository) listSAMLServiceProvidersPage(ctx context.Context, tenantID guid.ID, offset, limit int) ([]samlSPRow, int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	table := tables.SAMLServiceProviders.TableName()
	var total int64
	if err := r.writer().WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count SAML service providers: %w", err)
	}
	listing := r.writer().WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Order("name ASC, id ASC")
	rows := make([]samlSPRow, 0)
	if err := pageQuery(listing, offset, limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list SAML service providers: %w", err)
	}
	return rows, total, nil
}

// ListProvidersPage returns one page of the tenant's identity providers with the
// unfiltered tenant total. ListProviders stays for callers that need the whole
// registry in one call.
func (s *Service) ListProvidersPage(ctx context.Context, tenantID guid.ID, offset, limit int) ([]Provider, int64, error) {
	if tenantID.Zero() {
		return nil, 0, ErrInvalidProvider
	}
	rows, total, err := s.repo.listProvidersPage(ctx, tenantID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	result := make([]Provider, 0, len(rows))
	for _, row := range rows {
		result = append(result, providerFromRow(row))
	}
	return result, total, nil
}

// ListSAMLServiceProvidersPage returns one page of the tenant's SAML service
// provider registry with the unfiltered tenant total.
func (s *Service) ListSAMLServiceProvidersPage(ctx context.Context, tenantID guid.ID, offset, limit int) ([]SAMLServiceProvider, int64, error) {
	if tenantID.Zero() {
		return nil, 0, ErrInvalidSAMLSP
	}
	rows, total, err := s.repo.listSAMLServiceProvidersPage(ctx, tenantID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	result := make([]SAMLServiceProvider, 0, len(rows))
	for _, row := range rows {
		result = append(result, samlSPFromRow(row))
	}
	return result, total, nil
}
