package core

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
)

func (r *Repository) rootKind(ctx context.Context, id guid.ID) (string, error) {
	repo, err := r.pinWriter()
	if err != nil {
		return "", err
	}
	directory, err := repo.directoryTables()
	if err != nil {
		return "", err
	}
	var row struct{ ScopeKind string }
	err = repo.writer().WithContext(ctx).Table(directory.Tenants.String()).Select("scope_kind").Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("%w: organization root not found: %w", ErrInvalidArgument, gorm.ErrRecordNotFound)
	}
	return row.ScopeKind, err
}

func (s *Service) permissionAllowedForRoot(ctx context.Context, id guid.ID, action authz.Action) error {
	kind, err := s.repo.rootKind(ctx, id)
	if err != nil {
		return err
	}
	if kind == "system" && action.Scope == "platform" || kind == "platform" && authz.IsOperatingPlatformPermission(action) || kind == "tenant" && action.Scope != "platform" && action.Scope != "operating_platform" {
		return nil
	}
	return fmt.Errorf("%w: permission %s cannot be owned by %s", ErrInvalidArgument, action.Code, kind)
}

func (s *Service) OperatingPlatformPermissionCatalog(context.Context) []authz.Action {
	var actions []authz.Action
	for _, action := range s.registry.All() {
		if authz.IsOperatingPlatformPermission(action) {
			actions = append(actions, action)
		}
	}
	return actions
}

func (s *Service) EffectiveOperatingPlatformPermissions(ctx context.Context, platformID, principalID guid.ID) ([]string, error) {
	if platformID.Zero() || principalID.Zero() {
		return nil, ErrInvalidArgument
	}
	catalog := s.OperatingPlatformPermissionCatalog(ctx)
	codes := make([]string, 0, len(catalog))
	for _, action := range catalog {
		codes = append(codes, action.Code)
	}
	allowed, err := s.checkRootPermissions(ctx, platformID, codes, principalID)
	if err != nil {
		return nil, err
	}
	granted := make([]string, 0, len(codes))
	for _, code := range codes {
		if allowed[code] {
			granted = append(granted, code)
		}
	}
	return granted, nil
}

func (s *Service) GetOperatingPlatformMember(ctx context.Context, platformID, principalID guid.ID) (TenantMember, error) {
	kind, err := s.repo.rootKind(ctx, platformID)
	if err != nil {
		return TenantMember{}, err
	}
	if kind != "platform" {
		return TenantMember{}, ErrInvalidArgument
	}
	return s.GetTenantMember(ctx, platformID, principalID)
}

func (s *Service) ListOperatingPlatformMembers(ctx context.Context, platformID guid.ID, filter MemberFilter) ([]TenantMember, int64, error) {
	kind, err := s.repo.rootKind(ctx, platformID)
	if err != nil {
		return nil, 0, err
	}
	if kind != "platform" {
		return nil, 0, ErrInvalidArgument
	}
	return s.ListTenantMembers(ctx, platformID, filter)
}

func (s *Service) checkRootPermissions(ctx context.Context, id guid.ID, codes []string, subject guid.ID) (map[string]bool, error) {
	kind, err := s.repo.rootKind(ctx, id)
	if err != nil {
		return nil, err
	}
	if kind == "tenant" {
		return s.checker.CheckBulk(ctx, id, codes, subject)
	}
	allowed := make(map[string]bool, len(codes))
	if kind == "system" {
		for _, code := range codes {
			allowed[code], err = s.checker.CheckPlatform(ctx, code, subject)
			if err != nil {
				return nil, err
			}
		}
		return allowed, nil
	}
	if kind != "platform" {
		return allowed, nil
	}
	checker, ok := s.checker.(interface {
		CheckOperatingPlatform(context.Context, guid.ID, string, guid.ID) (bool, error)
	})
	if !ok {
		return nil, ErrUnavailable
	}
	for _, code := range codes {
		allowed[code], err = checker.CheckOperatingPlatform(ctx, id, code, subject)
		if err != nil {
			return nil, err
		}
	}
	return allowed, nil
}
