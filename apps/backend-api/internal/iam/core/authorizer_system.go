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
	root, err := systemRootID(ctx, systemCurrentRead(db), names.Directory.Tenants.String())
	if err != nil {
		return 0, err
	}
	if err := r.revisions.Lock(ctx, db, root); err != nil {
		return 0, err
	}
	return root, nil
}

func (a *Authorizer) checkSystemPermission(ctx context.Context, permission string, subject guid.ID) (bool, error) {
	full, codes, err := a.systemPermissions(ctx, true, subject)
	return full || codes[permission], err
}

// systemPermissions reads every unconditional system permission subject holds.
// A fenced read runs inside the caller's mutation transaction and takes the
// principal lock and system policy fence; an unfenced read is a plain read, like
// tenant authorization, so browsing never serializes behind grants (D55).
func (a *Authorizer) systemPermissions(ctx context.Context, fenced bool, subject guid.ID) (bool, map[string]bool, error) {
	names, err := a.authorizationTables()
	if err != nil {
		return false, nil, err
	}
	read := func(db *gorm.DB) *gorm.DB { return db }
	if fenced {
		read = systemCurrentRead
	}
	var principal struct{ ID guid.ID }
	err = read(a.db.WithContext(ctx)).Table(names.Principal.String()).Select("id").Where("id = ? AND status = ?", subject, "active").Take(&principal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("evaluate system principal: %w", err)
	}
	var owner guid.ID
	var rootErr error
	if fenced {
		repo := &Repository{db: a.db, revisions: a.revisions, foreign: a.foreign, repositoryTables: a.repositoryTables}
		owner, rootErr = repo.lockSystemAuthorization(ctx, subject)
	} else {
		owner, rootErr = systemRootID(ctx, a.db, names.Directory.Tenants.String())
	}
	rootMissing := errors.Is(rootErr, gorm.ErrRecordNotFound)
	if rootErr != nil && !rootMissing {
		return false, nil, rootErr
	}
	var administrator []guid.ID
	if err := read(a.db.WithContext(ctx)).Table(names.Tables.PlatformAdministrators.String()).Where("principal_id = ?", subject).Limit(1).Pluck("principal_id", &administrator).Error; err != nil {
		return false, nil, err
	}
	if len(administrator) > 0 {
		return true, nil, nil
	}
	if rootMissing {
		return false, nil, nil
	}
	var member struct{ PrincipalID guid.ID }
	err = read(a.db.WithContext(ctx)).Table(names.Tables.TenantMembers.String()).Select("principal_id").
		Where("tenant_id = ? AND principal_id = ? AND status = ?", owner, subject, "active").Take(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	var rows []struct {
		Name           string
		PermissionCode string
		ConditionJSON  string
	}
	// No CTE, UNION, nullable join, group, temporary grant, or tenant administrator expansion.
	err = read(a.db.WithContext(ctx)).Table(names.Tables.RoleMembers.String()+" AS rm").
		Select("r.name, rp.permission_code, rp.condition_json").
		Joins("JOIN "+names.Tables.Roles.String()+" AS r ON r.tenant_id = rm.tenant_id AND r.id = rm.role_id").
		Joins("JOIN "+names.Tables.RolePermissions.String()+" AS rp ON rp.tenant_id = rm.tenant_id AND rp.role_id = rm.role_id").
		Where("rm.tenant_id = ? AND rm.principal_id = ? AND r.name <> ?", owner, subject, SystemSuperadministratorRoleName).
		Find(&rows).Error
	if err != nil {
		return false, nil, fmt.Errorf("evaluate system role permission: %w", err)
	}
	codes := map[string]bool{}
	for _, row := range rows {
		if condition := strings.TrimSpace(row.ConditionJSON); condition != "" && condition != "null" {
			continue
		}
		if row.PermissionCode == "platform_administer" && !strings.HasPrefix(row.Name, SystemLegacyRolePrefix) {
			continue
		}
		codes[row.PermissionCode] = true
	}
	return false, codes, nil
}

// systemRootID resolves the active system root without locking it.
func systemRootID(ctx context.Context, db *gorm.DB, table string) (guid.ID, error) {
	var root struct{ ID guid.ID }
	err := db.WithContext(ctx).Table(table).Select("id").
		Where("slug = ? AND scope_kind = ? AND status = ?", SystemRootSlug, "system", "active").Take(&root).Error
	return root.ID, err
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
