package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func systemCurrentRead(db *gorm.DB) *gorm.DB {
	if db.Dialector.Name() == "sqlite" {
		return db
	}
	return db.Clauses(clause.Locking{Strength: "UPDATE"})
}

// Principal locks precede the policy fence everywhere, including account-view callbacks.
func (r *Repository) lockSystemAuthorization(ctx context.Context, subjects ...guid.ID) (guid.ID, error) {
	db := r.writer()
	if db == nil || r.revisions == nil {
		return 0, ErrUnavailable
	}
	if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	names, err := r.authorizationTables()
	if err != nil {
		return 0, err
	}
	ids := slices.Clone(subjects)
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if id.Zero() {
			continue
		}
		var row struct{ ID guid.ID }
		if err := systemCurrentRead(db.WithContext(ctx)).Table(names.Principal.String()).Select("id").Where("id = ?", id).Take(&row).Error; err != nil {
			return 0, err
		}
	}
	var root struct{ ID guid.ID }
	err = systemCurrentRead(db.WithContext(ctx)).Table(names.Directory.Tenants.String()).Select("id").
		Where("slug = ? AND scope_kind = ? AND status = ?", SystemRootSlug, "system", "active").Take(&root).Error
	if err != nil {
		return 0, err
	}
	if err := r.revisions.Lock(ctx, db, root.ID); err != nil {
		return 0, err
	}
	return root.ID, nil
}

func (a *Authorizer) checkSystemPermission(ctx context.Context, permission string, subject guid.ID) (bool, error) {
	names, err := a.authorizationTables()
	if err != nil {
		return false, err
	}
	var principal struct{ ID guid.ID }
	err = systemCurrentRead(a.db.WithContext(ctx)).Table(names.Principal.String()).Select("id").Where("id = ? AND status = ?", subject, "active").Take(&principal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("evaluate system principal: %w", err)
	}
	repo := &Repository{db: a.db, revisions: a.revisions, foreign: a.foreign, repositoryTables: a.repositoryTables}
	owner, rootErr := repo.lockSystemAuthorization(ctx, subject)
	if rootErr != nil && !errors.Is(rootErr, gorm.ErrRecordNotFound) {
		return false, rootErr
	}
	full, err := systemFullAdministrator(ctx, a.db, names.Tables.PlatformAdministrators.String(), subject)
	if err != nil || full {
		return full, err
	}
	if rootErr != nil {
		return false, nil
	}
	var member struct{ PrincipalID guid.ID }
	err = systemCurrentRead(a.db.WithContext(ctx)).Table(names.Tables.TenantMembers.String()).Select("principal_id").
		Where("tenant_id = ? AND principal_id = ? AND status = ?", owner, subject, "active").Take(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var rows []struct {
		Name          string
		ConditionJSON string
	}
	// No CTE, UNION, nullable join, group, temporary grant, or tenant administrator expansion.
	err = systemCurrentRead(a.db.WithContext(ctx)).Table(names.Tables.RoleMembers.String()+" AS rm").
		Select("r.name, rp.condition_json").
		Joins("JOIN "+names.Tables.Roles.String()+" AS r ON r.tenant_id = rm.tenant_id AND r.id = rm.role_id").
		Joins("JOIN "+names.Tables.RolePermissions.String()+" AS rp ON rp.tenant_id = rm.tenant_id AND rp.role_id = rm.role_id").
		Where("rm.tenant_id = ? AND rm.principal_id = ? AND rp.permission_code = ? AND r.name <> ?", owner, subject, permission, SystemSuperadministratorRoleName).
		Find(&rows).Error
	if err != nil {
		return false, fmt.Errorf("evaluate system role permission: %w", err)
	}
	for _, row := range rows {
		if condition := strings.TrimSpace(row.ConditionJSON); condition != "" && condition != "null" {
			continue
		}
		if permission == "platform_administer" && !strings.HasPrefix(row.Name, SystemLegacyRolePrefix) {
			continue
		}
		return true, nil
	}
	return false, nil
}

func systemFullAdministrator(ctx context.Context, db *gorm.DB, table string, subject guid.ID) (bool, error) {
	var row struct{ PrincipalID guid.ID }
	err := systemCurrentRead(db.WithContext(ctx)).Table(table).Select("principal_id").Where("principal_id = ?", subject).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

// CheckPlatformOn evaluates system authority using the caller's mutation transaction.
func (s *Service) CheckPlatformOn(ctx context.Context, tx *gorm.DB, permission string, subject guid.ID) (bool, error) {
	if s == nil || tx == nil {
		return false, ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return false, ErrInvalidArgument
	}
	checker, ok := s.checker.(*Authorizer)
	if !ok {
		return false, ErrUnavailable
	}
	clone := *checker
	clone.db = tx
	return clone.CheckPlatform(ctx, permission, subject)
}
