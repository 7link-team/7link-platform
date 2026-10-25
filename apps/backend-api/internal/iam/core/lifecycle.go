package core

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

// ErrPrincipalHasPlatformAccess requires the existing protected platform-grant
// removal workflow to run before irreversible anonymization.
var ErrPrincipalHasPlatformAccess = errors.New("remove principal platform access before anonymization")

// PrincipalPlatformAccessGuard requires protected platform-grant removal before
// global suspension, using the caller's principal-locked transaction.
type PrincipalPlatformAccessGuard func(context.Context, *gorm.DB, guid.ID) error

// requireNoPrincipalPlatformAccess refuses while the principal holds system
// authority in any form. The role check reads its codes from the shared
// registry, so a system-scope action any module contributed is covered without
// core naming that module's permissions.
func requireNoPrincipalPlatformAccess(
	ctx context.Context, tx *gorm.DB, prefix string, id guid.ID, registry *authz.Registry, now time.Time,
) error {
	if registry == nil {
		return ErrUnavailable
	}
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
	// Global actions cannot be granted by tenant or operating-platform roles.
	// Keep protected authority removal mandatory even for disabled memberships.
	// An assignment that has lapsed is not authority to protect: it grants
	// nothing anywhere else either.
	codes := make([]string, 0)
	for _, action := range registry.All() {
		if action.Scope == authz.ActionScopeSystem {
			codes = append(codes, action.Code)
		}
	}
	var count int64
	err := tx.WithContext(ctx).Table(names.RoleMembers.String()+" AS member").
		Joins("JOIN "+names.Roles.String()+" AS role ON role.tenant_id = member.tenant_id AND role.id = member.role_id").
		Joins("JOIN "+names.RolePermissions.String()+" AS permission ON permission.tenant_id = member.tenant_id AND permission.role_id = member.role_id").
		Where("member.principal_id = ? AND permission.permission_code IN ?", id, codes).
		Where("member.expires_at IS NULL OR member.expires_at > ?", now).Count(&count).Error
	if err != nil {
		return err
	}
	if count != 0 {
		return ErrPrincipalHasPlatformAccess
	}
	return nil
}

// PrincipalErasureVersion is the schema version ErasePrincipalData needs: the
// later of this module's principal-erasure guard migration and the migration
// that created the newest table or column erasure touches. Raise it when
// erasure reaches a later one: the principallifecycle erasure-version test
// fails until then.
//
// It is 19 because erasure closes the observed membership intervals as it
// withdraws admission. Keeping an open interval would invent later eligibility.
const PrincipalErasureVersion = 19

// ErasePrincipalData redacts every membership profile and withdraws admission.
// The caller owns the transaction and the suspended principal's row lock, and
// supplies the shared authorization registry the platform-access check reads.
func ErasePrincipalData(ctx context.Context, tx *gorm.DB, prefix string, registry *authz.Registry, id guid.ID, now time.Time) error {
	if tx == nil || id <= 0 {
		return errors.New("invalid principal erasure transaction")
	}
	revisions, err := newRevisionStoreForPrefix(prefix)
	if err != nil {
		return err
	}
	if err := requireNoPrincipalPlatformAccess(ctx, tx, prefix, id, registry, now); err != nil {
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
	periods := query.Bind(tx, prefix).MembershipPeriods
	if _, err := periods.WithContext(ctx).Where(periods.PrincipalID.Eq(id), periods.EndedAt.IsNull()).Update(periods.EndedAt, now); err != nil {
		return err
	}
	return tx.WithContext(ctx).Table(members.String()).Where("principal_id = ?", id).
		Updates(map[string]any{"display_name": "Anonymized account", "email": "", "status": "disabled",
			"disabled_at": now, "updated_at": now}).Error
}
