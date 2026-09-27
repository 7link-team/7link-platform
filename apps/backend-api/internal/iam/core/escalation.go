package core

import (
	"context"
	"fmt"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
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
	codes, err := repo.ListPermissions(ctx, tenantID, roleID)
	if err != nil {
		return err
	}
	return s.requireGrantablePermissions(ctx, tenantID, codes...)
}
