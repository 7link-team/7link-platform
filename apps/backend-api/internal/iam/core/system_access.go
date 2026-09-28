package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const SystemSuperadministratorRoleName = "__system_superadministrator__"
const SystemLegacyRolePrefix = "__system_legacy_"

func reservedSystemRoleName(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "__system_")
}

func (s *Service) systemAccessMutation(ctx context.Context, target guid.ID, permission string, mutate func(*Service, guid.ID) error) error {
	return s.repo.runInTx(ctx, func(repo *Repository) error {
		owner, err := repo.lockSystemAuthorization(ctx, authn.PrincipalIDFromContext(ctx), target)
		if err != nil {
			return err
		}
		clone := *s
		clone.repo = repo
		if checker, ok := s.checker.(*Authorizer); ok {
			pinned := *checker
			pinned.db = repo.writer()
			clone.checker = &pinned
		} else {
			return ErrUnavailable
		}
		clone.lockPrincipal = func(context.Context, *gorm.DB, guid.ID) error { return nil }
		clone.writes = newTransactionCoordinator(repo, s.writes.audit)
		system, resolved, err := clone.systemMenuService(ctx)
		if err != nil {
			return err
		}
		if owner != resolved {
			return ErrInvalidArgument
		}
		allowed, err := system.checker.CheckPlatform(ctx, permission, authn.PrincipalIDFromContext(ctx))
		if err != nil {
			return err
		}
		if !allowed {
			return ErrPrivilegeEscalation
		}
		return mutate(system, owner)
	})
}

func requireMutableSystemRole(role Role) error {
	if role.Name == SystemSuperadministratorRoleName {
		return ErrPrivilegeEscalation
	}
	return nil
}

func (r *Repository) isFullSystemAdministrator(ctx context.Context, principal guid.ID) (bool, error) {
	var row model.PlatformAdministrator
	err := r.writer().WithContext(ctx).Table(r.names.PlatformAdministrators.String()).Where("principal_id = ?", principal).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (r *Repository) ensureSystemAdministratorMember(ctx context.Context, owner, principal guid.ID) error {
	var existing model.TenantMember
	err := r.writer().WithContext(ctx).Table(r.names.TenantMembers.String()).Where("tenant_id = ? AND principal_id = ?", owner, principal).Take(&existing).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	names, err := r.authorizationTables()
	if err != nil {
		return err
	}
	var identity struct{ DisplayName, Email string }
	if err := r.writer().WithContext(ctx).Table(names.Principal.String()).Select("display_name,email").Where("id = ?", principal).Take(&identity).Error; err != nil {
		return err
	}
	if identity.DisplayName == "" {
		identity.DisplayName = principal.String()
	}
	_, err = r.putMember(ctx, TenantMember{TenantID: owner, PrincipalID: principal, DisplayName: identity.DisplayName, Email: identity.Email, Status: MemberActive}, true)
	return err
}

func (s *Service) changeSystemRoleMember(ctx context.Context, roleID, principal guid.ID, add bool) (bool, error) {
	var changed bool
	err := s.systemAccessMutation(ctx, principal, "system_role_manage_member", func(tx *Service, owner guid.ID) error {
		role, err := tx.GetRole(ctx, owner, roleID)
		if err != nil {
			return err
		}
		if role.Name == SystemSuperadministratorRoleName {
			full, err := tx.repo.isFullSystemAdministrator(ctx, authn.PrincipalIDFromContext(ctx))
			if err != nil {
				return err
			}
			if !full {
				return ErrPrivilegeEscalation
			}
			if add {
				_, err = tx.SetPlatformAdministrator(ctx, principal, true, nil)
				changed = err == nil
				return err
			}
			changed, err = tx.DeletePlatformAdministrator(ctx, principal)
			return err
		}
		if add {
			changed, err = tx.AddMember(ctx, owner, roleID, principal)
		} else {
			changed, err = tx.RemoveMember(ctx, owner, roleID, principal)
		}
		return err
	})
	return changed, err
}

func (r *Repository) bootstrapSystemAccess(ctx context.Context, owner guid.ID, now time.Time, registry *authz.Registry, appender audit.Appender) error {
	var legacy []model.PlatformGrant
	if err := r.writer().WithContext(ctx).Table(r.names.PlatformGrants.String()).Clauses(clause.Locking{Strength: "UPDATE"}).Order("principal_id,permission_code").Find(&legacy).Error; err != nil {
		return err
	}
	subjects := make([]guid.ID, 0, len(legacy))
	for _, grant := range legacy {
		subjects = append(subjects, grant.PrincipalID)
	}
	var fullPrincipals []guid.ID
	if err := r.writer().WithContext(ctx).Table(r.names.PlatformAdministrators.String()).Order("principal_id").Pluck("principal_id", &fullPrincipals).Error; err != nil {
		return err
	}
	subjects = append(subjects, fullPrincipals...)
	resolved, err := r.lockSystemAuthorization(ctx, subjects...)
	if err != nil {
		return err
	}
	if resolved != owner {
		return ErrInvalidArgument
	}
	var full model.Role
	err = r.writer().WithContext(ctx).Table(r.names.Roles.String()).Where("tenant_id = ? AND name_key = ?", owner, roleNameKey(SystemSuperadministratorRoleName)).Take(&full).Error
	changed := false
	for _, principal := range fullPrincipals {
		if err := r.ensureSystemAdministratorMember(ctx, owner, principal); err != nil {
			return err
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		role, err := r.CreateRole(ctx, owner, SystemSuperadministratorRoleName, "Full system authority; permissions are immutable")
		if err != nil {
			return err
		}
		full.ID = role.ID
		changed = true
	} else if err != nil {
		return err
	}
	byPrincipal := map[guid.ID][]string{}
	for _, grant := range legacy {
		// Full administrators' legacy rows are never imported, so a code retired
		// since they were written must not stop every later startup.
		if slices.Contains(fullPrincipals, grant.PrincipalID) {
			continue
		}
		action, ok := registry.Find(grant.PermissionCode)
		if !ok || action.Scope != authz.ActionScopeSystem {
			return fmt.Errorf("%w: legacy system permission %s", ErrInvalidArgument, grant.PermissionCode)
		}
		byPrincipal[grant.PrincipalID] = append(byPrincipal[grant.PrincipalID], grant.PermissionCode)
	}
	for _, principal := range subjects {
		codes, ok := byPrincipal[principal]
		if !ok {
			continue
		}
		if err := r.ensureSystemAdministratorMember(ctx, owner, principal); err != nil {
			return err
		}
		name := SystemLegacyRolePrefix + principal.String()
		var role model.Role
		err = r.writer().WithContext(ctx).Table(r.names.Roles.String()).Where("tenant_id = ? AND name_key = ?", owner, roleNameKey(name)).Take(&role).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			created, err := r.CreateRole(ctx, owner, name, "Imported system permissions")
			if err != nil {
				return err
			}
			role.ID = created.ID
		} else if err != nil {
			return err
		}
		for _, code := range codes {
			if _, err := r.GrantPermission(ctx, owner, role.ID, code); err != nil {
				return err
			}
		}
		if _, err := r.AddMember(ctx, owner, role.ID, principal); err != nil {
			return err
		}
		if err := r.writer().WithContext(ctx).Table(r.names.PlatformGrants.String()).Where("principal_id = ?", principal).Delete(&model.PlatformGrant{}).Error; err != nil {
			return err
		}
		changed = true
	}
	menusChanged, err := r.upgradeSystemMenus(ctx, owner, now)
	if err != nil {
		return err
	}
	if changed || menusChanged {
		if err := r.bumpPolicyRevision(ctx, owner); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, 0, "system_access_upgraded", "system", owner)
		event.Detail["scope_id"], event.Detail["scope_kind"], event.Detail["legacy_grant_count"], event.Detail["menus_upgraded"] = owner.String(), "system", len(legacy), menusChanged
		return appender(ctx, r.writer(), event)
	}
	return nil
}
