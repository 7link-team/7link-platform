package core

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"gorm.io/gorm"
)

// The two permission codes that make a principal a durable administrator.
// A role holding either of them, unconditionally, keeps a tenant reachable.
const (
	tenantAdministratorPermission   = "tenant_administer"
	platformAdministratorPermission = "platform_administer"
)

type tenantAdministratorGuard struct {
	prefix    string
	revisions *RevisionStore
	now       func() time.Time
	foreign   *foreignTables
}

// NewAdministratorGuard creates the tenant-administrator safety capability.
// The tables the durable-administrator statement reads arrive from the modules
// that own them through the connectors core publishes, so the guard counts
// administrators in the same schema the rest of the module writes to.
func NewAdministratorGuard(runtime *appenv.Runtime) *tenantAdministratorGuard {
	if runtime == nil {
		panic("IAM administrator guard requires runtime")
	}
	revisions, err := NewRevisionStore(runtime.TablePrefix())
	if err != nil {
		panic(err)
	}
	return &tenantAdministratorGuard{
		prefix: runtime.TablePrefix(), revisions: revisions, now: time.Now,
		foreign: &foreignTables{},
	}
}

func (guard *tenantAdministratorGuard) Protect(
	ctx context.Context,
	db *gorm.DB,
	tenantID guid.ID,
) (func() error, error) {
	if guard == nil || guard.now == nil || guard.revisions == nil || db == nil || tenantID.Zero() {
		return nil, errors.New("invalid tenant administrator guard configuration")
	}
	if err := guard.revisions.Lock(ctx, db, tenantID); err != nil {
		return nil, err
	}
	hadAdministrator, err := guard.hasDurableAdministrator(ctx, db, tenantID)
	if err != nil {
		return nil, err
	}
	return func() error {
		hasAdministrator, err := guard.hasDurableAdministrator(ctx, db, tenantID)
		if err != nil {
			return err
		}
		if hadAdministrator && !hasAdministrator {
			return ErrLastTenantAdministrator
		}
		return nil
	}, nil
}

func (guard *tenantAdministratorGuard) hasDurableAdministrator(
	ctx context.Context,
	db *gorm.DB,
	tenantID guid.ID,
) (bool, error) {
	now := guard.now().UTC().Truncate(time.Millisecond)
	tables, err := guard.foreign.authorizationTables(guard.prefix)
	if err != nil {
		return false, err
	}
	count, err := query.CountDurableAdministrators(ctx, db, query.DurableAdministratorParams{
		AuthorizationTables:     tables,
		TenantID:                tenantID,
		Now:                     now,
		AdministratorPermission: tenantAdministratorPermission,
		PlatformPermission:      platformAdministratorPermission,
	})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

var _ AdministratorGuard = (*tenantAdministratorGuard)(nil)
