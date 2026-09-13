package organization

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"7link.com/platform/internal/iam/organization/model"
	"gorm.io/gorm"
)

// TenantLookup resolves a tenant from the slug an operator types, inside the
// caller's transaction. It exists for the repair command: a tenant created
// before the owner bootstrap did has no member, so every HTTP route refuses it
// and nothing inside the product can name it. The slug on the invoice is the
// only handle that person has.
//
// It is a read: repairing the tenant is core's to do, and organization still
// owns which row that slug means.
type TenantLookup func(context.Context, *gorm.DB, string) (Tenant, bool, error)

// FindTenantBySlugOn returns the tenant owning slug through the caller's
// transaction, reporting false when no tenant does. It normalizes the slug the
// same way creation does, so the value an operator reads back is the value they
// typed rather than a near miss that silently matched nothing.
func (r *Repository) FindTenantBySlugOn(
	ctx context.Context,
	db *gorm.DB,
	slug string,
) (Tenant, bool, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if r == nil || db == nil {
		return Tenant{}, false, errRepositoryUnavailable
	}
	if !validTenantSlug(slug) {
		return Tenant{}, false, ErrInvalidTenant
	}
	repo, err := r.on(db)
	if err != nil {
		return Tenant{}, false, err
	}
	tables, _, err := repo.tables()
	if err != nil {
		return Tenant{}, false, err
	}
	var row model.Tenant
	err = repo.writer().WithContext(ctx).Table(tables.Tenants.TableName()).
		Where("slug = ?", slug).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Tenant{}, false, nil
	}
	if err != nil {
		return Tenant{}, false, fmt.Errorf("look up tenant by slug: %w", err)
	}
	return tenantFromRow(&row), true, nil
}

// tenantLookup is published before injection runs, so it builds its repository
// at call time from collaborators wiring has since supplied. The audit appender
// is required by the constructor rather than by this read; guarding on it here
// is what keeps a half-wired module from panicking inside a caller's
// transaction.
func (m *Module) tenantLookup() TenantLookup {
	return func(ctx context.Context, db *gorm.DB, slug string) (Tenant, bool, error) {
		if m == nil || m.runtime == nil || m.ids == nil || m.appender == nil {
			return Tenant{}, false, ErrUnavailable
		}
		return NewRepository(m.runtime, m.ids, m.appender).FindTenantBySlugOn(ctx, db, slug)
	}
}
