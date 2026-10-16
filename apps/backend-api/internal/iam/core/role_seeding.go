package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/policyx"
	"gorm.io/gorm"
)

// RoleSeeder exposes IAM writes in a caller-owned seeding transaction. The
// caller owns the role-key ledger and must consult it before EnsureRoleOn:
// an existing name alone is never proof that a role belongs to that caller.
// A detected name conflict returns a nonzero existing role with
// ErrRoleNameConflict without writing, so the caller can record its conflict
// ledger. Any error with a zero role (including a concurrent name collision)
// requires rollback, as does a later ledger write failure.
// This is an internal system capability, not a user-facing role editor. Only
// code-owned manifests may call it; caller claims are retained for attribution.
type RoleSeeder struct {
	EnsureRoleOn      func(context.Context, *gorm.DB, guid.ID, string, string, string) (Role, error)
	GrantPermissionOn func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (bool, error)
	// GrantConditionalPermissionOn attaches a condition only to a new grant.
	// Existing administrator-managed grants and conditions remain untouched.
	GrantConditionalPermissionOn func(context.Context, *gorm.DB, guid.ID, guid.ID, string, json.RawMessage) (bool, error)
	// SeedAdministratorMembersOn copies current direct Administrator members
	// once, preserving assignment expiry. The consumer's ledger owns "once".
	SeedAdministratorMembersOn func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)
}

func (m *Module) roleSeeder() RoleSeeder {
	return RoleSeeder{
		SeedAdministratorMembersOn: m.seedAdministratorMembersOn,
		GrantConditionalPermissionOn: func(ctx context.Context, tx *gorm.DB, tenantID, roleID guid.ID, code string, condition json.RawMessage) (bool, error) {
			service, err := m.entityServiceOn(tx)
			if err != nil {
				return false, err
			}
			service.seedRoleWrites = true
			canonical, err := policyx.CanonicalCondition(condition)
			if err != nil {
				return false, fmt.Errorf("%w: %w", ErrInvalidRolePermissionCondition, err)
			}
			if len(canonical) == 0 {
				return false, ErrInvalidRolePermissionCondition
			}
			changed, err := service.GrantPermission(ctx, tenantID, roleID, code)
			if err != nil || !changed {
				return changed, err
			}
			_, _, err = service.SetPermissionCondition(ctx, tenantID, roleID, code, canonical)
			return changed, err
		},
		EnsureRoleOn: func(ctx context.Context, tx *gorm.DB, tenantID guid.ID, nameKey, name, description string) (Role, error) {
			service, err := m.entityServiceOn(tx)
			if err != nil {
				return Role{}, err
			}
			// IAM name_key is the normalized display name, not the consumer's
			// stable role key or translation key. Preserve its unique index.
			if nameKey != roleNameKey(name) {
				return Role{}, fmt.Errorf("%w: role name key does not match name", ErrInvalidArgument)
			}
			if err := validateTenant(tenantID); err != nil {
				return Role{}, err
			}
			existing, err := service.repo.seedRoleByNameKey(ctx, tenantID, nameKey)
			if err == nil {
				return existing, ErrRoleNameConflict
			}
			if !errors.Is(err, ErrRoleNotFound) {
				return Role{}, err
			}
			return service.CreateRole(ctx, tenantID, name, description)
		},
		GrantPermissionOn: func(ctx context.Context, tx *gorm.DB, tenantID, roleID guid.ID, code string) (bool, error) {
			service, err := m.entityServiceOn(tx)
			if err != nil {
				return false, err
			}
			service.seedRoleWrites = true
			return service.GrantPermission(ctx, tenantID, roleID, code)
		},
	}
}

func (m *Module) seedAdministratorMembersOn(ctx context.Context, tx *gorm.DB, tenantID, targetRoleID guid.ID) ([]guid.ID, error) {
	service, err := m.entityServiceOn(tx)
	if err != nil {
		return nil, err
	}
	if _, err := service.GetRole(ctx, tenantID, targetRoleID); err != nil {
		return nil, err
	}
	admin, err := service.repo.seedRoleByNameKey(ctx, tenantID, roleNameKey(administratorRoleName))
	if errors.Is(err, ErrRoleNotFound) {
		return []guid.ID{}, nil
	}
	if err != nil {
		return nil, err
	}
	// A display name does not establish authority: the original role can be
	// renamed or have its permissions removed through ordinary IAM editing.
	permissions, err := service.ListPermissionGrants(ctx, tenantID, admin.ID)
	if err != nil {
		return nil, err
	}
	full := false
	for _, permission := range permissions {
		condition := strings.TrimSpace(string(permission.Condition))
		if permission.PermissionCode == tenantAdministerPermission && (condition == "" || condition == "null") {
			full = true
		}
	}
	if !full {
		return []guid.ID{}, nil
	}
	tables, _, err := service.repo.tables()
	if err != nil {
		return nil, err
	}
	now := service.repo.now().UTC()
	members, err := tables.RoleMembers.WithContext(ctx).
		Where(tables.RoleMembers.TenantID.Eq(tenantID), tables.RoleMembers.RoleID.Eq(admin.ID)).
		Order(tables.RoleMembers.PrincipalID).Find()
	if err != nil {
		return nil, err
	}
	added := make([]guid.ID, 0, len(members))
	service.seedRoleWrites = true
	for _, member := range members {
		if member.ExpiresAt != nil && !member.ExpiresAt.After(now) {
			continue
		}
		active, err := service.repo.IsMemberActive(ctx, tenantID, member.PrincipalID)
		if err != nil {
			return nil, err
		}
		if !active {
			continue
		}
		changed, err := service.AddMember(ctx, tenantID, targetRoleID, member.PrincipalID, member.ExpiresAt)
		if err != nil {
			return nil, err
		}
		if changed {
			added = append(added, member.PrincipalID)
		}
	}
	return added, nil
}

func (r *Repository) seedRoleByNameKey(ctx context.Context, tenantID guid.ID, nameKey string) (Role, error) {
	tables, _, err := r.tables()
	if err != nil {
		return Role{}, err
	}
	row, err := tables.Roles.WithContext(ctx).
		Where(tables.Roles.TenantID.Eq(tenantID), tables.Roles.NameKey.Eq(nameKey)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Role{}, ErrRoleNotFound
	}
	if err != nil {
		return Role{}, err
	}
	return roleFromRow(row), nil
}
