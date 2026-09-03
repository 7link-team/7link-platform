package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"gorm.io/gorm"
)

type tenantAdministratorGuard struct {
	revisions *RevisionStore
	prefix    string
	now       func() time.Time
}

// NewAdministratorGuard creates the tenant-administrator safety capability.
// It uses the deployment prefix carried by the application runtime for every
// table participating in the durable-administrator query.
func NewAdministratorGuard(runtime *appenv.Runtime) AdministratorGuard {
	if runtime == nil {
		panic("IAM administrator guard requires runtime")
	}
	revisions, err := NewRevisionStore(runtime.TablePrefix())
	if err != nil {
		panic(err)
	}
	return &tenantAdministratorGuard{
		revisions: revisions, prefix: runtime.TablePrefix(), now: time.Now,
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
	var count int64
	err := db.WithContext(ctx).Raw(`
SELECT COUNT(DISTINCT administrators.principal_id)
FROM (
    SELECT members.principal_id
    FROM `+guard.prefix+`iam_role_members AS members
    JOIN `+guard.prefix+`iam_role_permissions AS permissions
      ON permissions.tenant_id = members.tenant_id AND permissions.role_id = members.role_id
    WHERE members.tenant_id = ? AND permissions.permission_code IN (?, ?) AND permissions.condition_json = ''
    UNION
    SELECT group_members.principal_id
    FROM `+guard.prefix+`iam_group_role_bindings AS bindings
    JOIN `+guard.prefix+`iam_role_permissions AS permissions
      ON permissions.tenant_id = bindings.tenant_id AND permissions.role_id = bindings.role_id
    JOIN `+guard.prefix+`iam_groups AS grp
      ON grp.tenant_id = bindings.tenant_id AND grp.id = bindings.group_id
     AND grp.status = 'active' AND grp.group_type = 'static'
    JOIN `+guard.prefix+`iam_group_members AS group_members
      ON group_members.tenant_id = bindings.tenant_id AND group_members.group_id = bindings.group_id
    WHERE bindings.tenant_id = ? AND permissions.permission_code IN (?, ?) AND permissions.condition_json = ''
      AND (group_members.starts_at IS NULL OR group_members.starts_at <= ?)
      AND group_members.ends_at IS NULL
    UNION
    SELECT position_members.principal_id
    FROM `+guard.prefix+`iam_position_role_bindings AS bindings
    JOIN `+guard.prefix+`iam_role_permissions AS permissions
      ON permissions.tenant_id = bindings.tenant_id AND permissions.role_id = bindings.role_id
    JOIN `+guard.prefix+`iam_positions AS positions
      ON positions.tenant_id = bindings.tenant_id AND positions.id = bindings.position_id
     AND positions.status = 'active'
    JOIN `+guard.prefix+`iam_position_members AS position_members
      ON position_members.tenant_id = bindings.tenant_id AND position_members.position_id = bindings.position_id
    WHERE bindings.tenant_id = ? AND permissions.permission_code IN (?, ?) AND permissions.condition_json = ''
      AND (position_members.starts_at IS NULL OR position_members.starts_at <= ?)
      AND position_members.ends_at IS NULL
) AS administrators
JOIN `+guard.prefix+`iam_principals AS principals
  ON principals.id = administrators.principal_id AND principals.status = 'active'
JOIN `+guard.prefix+`iam_tenant_members AS tenant_members
  ON tenant_members.tenant_id = ?
 AND tenant_members.principal_id = administrators.principal_id
 AND tenant_members.status = 'active'`,
		tenantID, "tenant_administer", "platform_administer",
		tenantID, "tenant_administer", "platform_administer", now,
		tenantID, "tenant_administer", "platform_administer", now,
		tenantID,
	).Scan(&count).Error
	if err != nil {
		return false, fmt.Errorf("count durable tenant administrators: %w", err)
	}
	return count > 0, nil
}

var _ AdministratorGuard = (*tenantAdministratorGuard)(nil)
