package identity

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// TenantOwnerLookup resolves the principal a new tenant names as its owner,
// inside the caller's tenant-creation transaction. Organization needs the
// display name and email for the membership row it writes, and needs the
// status so a suspended principal cannot become an administrator of a tenant
// that does not exist yet. It reports false when no such principal exists.
type TenantOwnerLookup func(context.Context, *gorm.DB, guid.ID) (Principal, bool, error)

// FindPrincipalOn returns the principal with the given ID through the caller's
// transaction. It deliberately takes no tenant argument: the owner of a tenant
// being created cannot yet be a member of it.
func (r *Repository) FindPrincipalOn(
	ctx context.Context,
	db *gorm.DB,
	principalID guid.ID,
) (Principal, bool, error) {
	if r == nil || db == nil || principalID.Zero() {
		return Principal{}, false, ErrInvalid
	}
	repo, err := r.on(db)
	if err != nil {
		return Principal{}, false, err
	}
	tables, database, err := repo.tables()
	if err != nil {
		return Principal{}, false, err
	}
	var row model.Principal
	err = database.WithContext(ctx).Table(tables.Principals.TableName()).
		Where("id = ?", principalID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Principal{}, false, nil
	}
	if err != nil {
		return Principal{}, false, fmt.Errorf("load principal: %w", err)
	}
	return principalFromRow(&row), true, nil
}

// tenantOwnerLookup is provided before injection, so it builds its repository
// at call time from collaborators Wire has since supplied.
func (m *Module) tenantOwnerLookup() TenantOwnerLookup {
	return func(ctx context.Context, db *gorm.DB, principalID guid.ID) (Principal, bool, error) {
		if m == nil || m.runtime == nil || m.ids == nil || m.appender == nil {
			return Principal{}, false, ErrUnavailable
		}
		return NewRepository(m.runtime, m.ids, m.appender).FindPrincipalOn(ctx, db, principalID)
	}
}
