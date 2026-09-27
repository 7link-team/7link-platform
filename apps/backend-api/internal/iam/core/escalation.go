package core

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

func (s *Service) requireGrantablePermissions(ctx context.Context, tenantID guid.ID, codes ...string) error {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID.Zero() {
		return nil
	}
	wanted := make([]string, 0, len(codes))
	seen := map[string]bool{}
	for _, code := range codes {
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		wanted = append(wanted, code)
	}
	if len(wanted) == 0 {
		return nil
	}
	allowed, err := s.checkRootPermissions(ctx, tenantID, wanted, claims.PrincipalID)
	if err != nil {
		return err
	}
	for _, code := range wanted {
		if !allowed[code] {
			return fmt.Errorf("%w: %s", ErrPrivilegeEscalation, code)
		}
	}
	return nil
}

func (s *Service) requireGrantableRole(ctx context.Context, tenantID, roleID guid.ID) error {
	return s.requireGrantableRoleWithRepository(ctx, s.repo, tenantID, roleID)
}

func (s *Service) requireGrantableRoleWithRepository(
	ctx context.Context,
	repo *Repository,
	tenantID, roleID guid.ID,
) error {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID.Zero() {
		return nil
	}
	if err := s.requireGrantablePermissionsOn(ctx, repo, tenantID); err != nil {
		return err
	}
	var role struct{ ID guid.ID }
	err := systemCurrentRead(repo.writer().WithContext(ctx)).Table(repo.names.Roles.String()).Select("id").Where("tenant_id = ? AND id = ?", tenantID, roleID).Take(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrRoleNotFound
	}
	if err != nil {
		return err
	}
	var codes []string
	if err := systemCurrentRead(repo.writer().WithContext(ctx)).Table(repo.names.RolePermissions.String()).Select("permission_code").Where("tenant_id = ? AND role_id = ?", tenantID, roleID).Find(&codes).Error; err != nil {
		return err
	}
	return s.requireGrantablePermissionsOn(ctx, repo, tenantID, codes...)
}

// The fence and checker share the mutation transaction so revocation cannot race a grant.
func (s *Service) requireGrantablePermissionsOn(ctx context.Context, repo *Repository, owner guid.ID, codes ...string) error {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID.Zero() {
		return nil
	}
	kind, err := repo.rootKind(ctx, owner)
	if err != nil {
		return err
	}
	if kind == "system" {
		resolved, err := repo.lockSystemAuthorization(ctx, claims.PrincipalID)
		if err != nil {
			return err
		}
		if resolved != owner {
			return ErrInvalidArgument
		}
	} else {
		if kind == "tenant" || kind == "platform" {
			if checker, ok := s.checker.(*Authorizer); ok {
				pinned := *checker
				pinned.db = repo.writer()
				if _, err := pinned.checkSystemPermission(ctx, "platform_administer", claims.PrincipalID); err != nil {
					return err
				}
			}
		}
		if err := repo.revisions.Lock(ctx, repo.writer(), owner); err != nil {
			return err
		}
	}
	clone := *s
	clone.repo = repo
	if checker, ok := s.checker.(*Authorizer); ok {
		pinned := *checker
		pinned.db = repo.writer()
		pinned.currentRead = repo.writer().Dialector.Name() == "mysql"
		clone.checker = &pinned
	}
	return clone.requireGrantablePermissions(ctx, owner, codes...)
}
