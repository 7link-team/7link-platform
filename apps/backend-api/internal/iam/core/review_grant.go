package core

import (
	"context"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// NewRoleGrantStore binds governance's transaction callbacks to one core IAM
// repository and tenant-administrator guard.
func NewRoleGrantStore(repository *Repository, guard AdministratorGuard) RoleGrantStore {
	if repository == nil || guard == nil {
		panic("IAM role grant store requires repository and administrator guard")
	}
	removeGroup := repository.removeDirectoryMembershipOn("group")
	removePosition := repository.removeDirectoryMembershipOn("position")
	return RoleGrantStore{
		Grant:                    repository.grantTemporaryRoleOn,
		Revoke:                   repository.revokeTemporaryRoleOn,
		RoleGranted:              repository.roleGrantedOn,
		RemovePermanent:          repository.removePermanentRoleOn(guard),
		RemoveGroupMembership:    protectDirectoryRemoval(guard, removeGroup),
		RemovePositionMembership: protectDirectoryRemoval(guard, removePosition),
		RemoveEntityRoleBinding:  protectEntityRoleRemoval(guard, repository.deleteEntityRoleBindingOn),
	}
}

// roleGrantedOn reports whether a principal already holds a role, as a
// permanent member or through an unexpired temporary grant. It is the read that
// keeps an access request from opening for access the requester already has.
//
// The two counts are added rather than short-circuited on the first, because the
// statement this replaces summed them: a UNION ALL is a bag union, so a role
// held both ways contributed two rows there and contributes two here. The two
// statements read through the caller's transaction, so the answer and the write
// that follows it share one snapshot.
func (r *Repository) roleGrantedOn(
	ctx context.Context,
	db *gorm.DB,
	tenantID, roleID, principalID guid.ID,
	now time.Time,
) (bool, error) {
	if db == nil || tenantID.Zero() || roleID.Zero() || principalID.Zero() || now.IsZero() {
		return false, fmt.Errorf("check existing role grant: %w", ErrInvalidArgument)
	}
	tables, _, err := r.withDB(db).tables()
	if err != nil {
		return false, fmt.Errorf("check existing role grant: %w", err)
	}
	memberCount, err := tables.RoleMembers.WithContext(ctx).
		Where(
			tables.RoleMembers.TenantID.Eq(tenantID),
			tables.RoleMembers.RoleID.Eq(roleID),
			tables.RoleMembers.PrincipalID.Eq(principalID),
		).Count()
	if err != nil {
		return false, fmt.Errorf("check existing role grant: %w", err)
	}
	grantCount, err := tables.TemporaryRoleGrants.WithContext(ctx).
		Where(
			tables.TemporaryRoleGrants.TenantID.Eq(tenantID),
			tables.TemporaryRoleGrants.RoleID.Eq(roleID),
			tables.TemporaryRoleGrants.PrincipalID.Eq(principalID),
			tables.TemporaryRoleGrants.StartsAt.Lte(now),
			tables.TemporaryRoleGrants.EndsAt.Gt(now),
		).Count()
	if err != nil {
		return false, fmt.Errorf("check existing role grant: %w", err)
	}
	return memberCount+grantCount > 0, nil
}

func protectDirectoryRemoval(
	guard AdministratorGuard,
	remove func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error),
) func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) {
	return func(ctx context.Context, db *gorm.DB, tenantID, directoryID, principalID guid.ID) (bool, error) {
		if db == nil || tenantID.Zero() || directoryID.Zero() || principalID.Zero() {
			return false, fmt.Errorf("remove directory membership: %w", ErrInvalidArgument)
		}
		verify, err := guard.Protect(ctx, db, tenantID)
		if err != nil {
			return false, err
		}
		changed, err := remove(ctx, db, tenantID, directoryID, principalID)
		if err != nil || !changed {
			return changed, err
		}
		if err := verify(); err != nil {
			return false, err
		}
		return true, nil
	}
}

func protectEntityRoleRemoval(
	guard AdministratorGuard,
	remove func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error),
) func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error) {
	return func(ctx context.Context, db *gorm.DB, tenantID, entityID, roleID, principalID guid.ID) (bool, error) {
		if db == nil || tenantID.Zero() || entityID.Zero() || roleID.Zero() || principalID.Zero() {
			return false, fmt.Errorf("remove entity role binding: %w", ErrInvalidArgument)
		}
		verify, err := guard.Protect(ctx, db, tenantID)
		if err != nil {
			return false, err
		}
		changed, err := remove(ctx, db, tenantID, entityID, roleID, principalID)
		if err != nil || !changed {
			return changed, err
		}
		if err := verify(); err != nil {
			return false, err
		}
		return true, nil
	}
}

func (r *Repository) removePermanentRoleOn(guard AdministratorGuard) func(
	context.Context,
	*gorm.DB,
	guid.ID,
	guid.ID,
	guid.ID,
	time.Time,
) (bool, error) {
	return func(
		ctx context.Context,
		db *gorm.DB,
		tenantID, roleID, principalID guid.ID,
		createdAt time.Time,
	) (bool, error) {
		if db == nil || tenantID.Zero() || roleID.Zero() || principalID.Zero() || createdAt.IsZero() {
			return false, fmt.Errorf("remove permanent role: %w", ErrInvalidArgument)
		}
		verify, err := guard.Protect(ctx, db, tenantID)
		if err != nil {
			return false, err
		}
		result := db.WithContext(ctx).Table(query.BindNames(r.prefix()).RoleMembers.String()).
			Where(
				"tenant_id = ? AND role_id = ? AND principal_id = ? AND created_at = ?",
				tenantID, roleID, principalID, createdAt.UTC().Truncate(time.Millisecond),
			).Delete(&model.RoleMember{})
		if result.Error != nil {
			return false, fmt.Errorf("delete reviewed role member: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return false, nil
		}
		if err := verify(); err != nil {
			return false, err
		}
		return true, nil
	}
}

func (r *Repository) removeDirectoryMembershipOn(kind string) func(
	context.Context,
	*gorm.DB,
	guid.ID,
	guid.ID,
	guid.ID,
) (bool, error) {
	return func(ctx context.Context, db *gorm.DB, tenantID, directoryID, principalID guid.ID) (bool, error) {
		if db == nil || tenantID.Zero() || directoryID.Zero() || principalID.Zero() {
			return false, fmt.Errorf("remove %s membership: %w", kind, ErrInvalidArgument)
		}
		var table, idColumn string
		switch kind {
		case "group":
			table, idColumn = "iam_group_members", "group_id"
		case "position":
			table, idColumn = "iam_position_members", "position_id"
		default:
			return false, fmt.Errorf("remove directory membership: %w", ErrInvalidArgument)
		}
		result := db.WithContext(ctx).Table(r.table(table)).Where(
			"tenant_id = ? AND "+idColumn+" = ? AND principal_id = ?",
			tenantID, directoryID, principalID,
		).Delete(&struct{}{})
		if result.Error != nil {
			return false, fmt.Errorf("remove %s membership: %w", kind, result.Error)
		}
		return result.RowsAffected > 0, nil
	}
}

func (r *Repository) deleteEntityRoleBindingOn(
	ctx context.Context,
	db *gorm.DB,
	tenantID, entityID, roleID, principalID guid.ID,
) (bool, error) {
	if db == nil || tenantID.Zero() || entityID.Zero() || roleID.Zero() || principalID.Zero() {
		return false, fmt.Errorf("remove entity role binding: %w", ErrInvalidArgument)
	}
	return r.withDB(db).DeleteEntityRoleBinding(ctx, tenantID, entityID, roleID, principalID)
}
