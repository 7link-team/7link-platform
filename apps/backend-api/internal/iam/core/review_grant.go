package core

import (
	"context"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// NewRoleGrantStore binds governance's transaction callbacks to one core IAM
// repository and tenant-administrator guard.
func NewRoleGrantStore(repository *Repository, guard AdministratorGuard) RoleGrantStore {
	if repository == nil || guard == nil {
		panic("IAM role grant store requires repository and administrator guard")
	}
	return RoleGrantStore{
		Grant:                    repository.grantTemporaryRoleOn,
		Revoke:                   repository.revokeTemporaryRoleOn,
		RemovePermanent:          repository.removePermanentRoleOn(guard),
		RemoveGroupMembership:    repository.removeDirectoryMembershipOn("group"),
		RemovePositionMembership: repository.removeDirectoryMembershipOn("position"),
		RemoveEntityRoleBinding:  repository.deleteEntityRoleBindingOn,
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
		tables := r.withDB(db)
		result := db.WithContext(ctx).Table(tables.table("iam_role_members")).
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
