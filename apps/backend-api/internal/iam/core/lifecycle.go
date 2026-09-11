package core

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

// ErrPrincipalHasPlatformAccess requires the existing protected platform-grant
// removal workflow to run before irreversible anonymization.
var ErrPrincipalHasPlatformAccess = errors.New("remove principal platform access before anonymization")

// PrincipalPlatformAccessGuard requires protected platform-grant removal before
// global suspension, using the caller's principal-locked transaction.
type PrincipalPlatformAccessGuard func(context.Context, *gorm.DB, guid.ID) error

func requireNoPrincipalPlatformAccess(ctx context.Context, tx *gorm.DB, prefix string, id guid.ID) error {
	names := query.BindNames(prefix)
	for _, table := range []gormx.TableName{names.PlatformAdministrators, names.PlatformGrants} {
		var count int64
		if err := tx.WithContext(ctx).Table(table.String()).Where("principal_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrPrincipalHasPlatformAccess
		}
	}
	return nil
}

// ErasePrincipalData redacts every membership profile and withdraws admission.
// The caller owns the transaction and the suspended principal's row lock.
func ErasePrincipalData(ctx context.Context, tx *gorm.DB, prefix string, id guid.ID, now time.Time) error {
	if tx == nil || id <= 0 {
		return errors.New("invalid principal erasure transaction")
	}
	revisions, err := NewRevisionStore(prefix)
	if err != nil {
		return err
	}
	if err := requireNoPrincipalPlatformAccess(ctx, tx, prefix, id); err != nil {
		return err
	}
	var tenants []guid.ID
	members := query.BindNames(prefix).TenantMembers
	if err := tx.WithContext(ctx).Table(members.String()).Where("principal_id = ?", id).
		Order("tenant_id ASC").Pluck("tenant_id", &tenants).Error; err != nil {
		return err
	}
	for _, tenant := range tenants {
		if err := revisions.Bump(ctx, tx, tenant, now); err != nil {
			return err
		}
	}
	return tx.WithContext(ctx).Table(members.String()).Where("principal_id = ?", id).
		Updates(map[string]any{"display_name": "Anonymized account", "email": "", "status": "disabled",
			"disabled_at": now, "updated_at": now}).Error
}
