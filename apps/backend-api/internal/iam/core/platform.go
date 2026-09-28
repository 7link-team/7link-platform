package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const platformAuditTenant guid.ID = 0

type platformAdministratorRow struct {
	PrincipalID    guid.ID   `gorm:"column:principal_id"`
	RecordKind     string    `gorm:"column:record_kind"`
	PermissionCode string    `gorm:"column:permission_code"`
	RoleName       string    `gorm:"column:role_name"`
	CreatedAt      time.Time `gorm:"column:created_at"`
}

// PlatformPermissionCodes returns declared platform permission codes in stable
// order.
func PlatformPermissionCodes(registry *authz.Registry) []string {
	codes := make([]string, 0, 8)
	if registry == nil {
		return codes
	}
	for _, action := range registry.All() {
		if action.Scope == authz.ActionScopeSystem {
			codes = append(codes, action.Code)
		}
	}
	sort.Strings(codes)
	return codes
}

func validatePlatformPrincipal(principalID guid.ID) (guid.ID, error) {
	if principalID.Zero() {
		return 0, fmt.Errorf("%w: invalid platform administrator principal", ErrInvalidArgument)
	}
	return principalID, nil
}

// platformRecordKindRank orders the two record kinds the way the merged
// statement's DESC clause did: under any collation this repository uses, 'grant'
// sorts above 'full', so grants precede the full-administrator row of the same
// principal.
func platformRecordKindRank(kind string) int {
	if kind == "grant" {
		return 0
	}
	return 1
}

// ListPlatformAdministrators returns full and restricted platform
// administrators. The statement this replaced read both sources at once and the
// two arm queries below cannot, so the function runs them in one transaction,
// which is what keeps the parts of that guarantee its callers rely on. A caller
// already inside a transaction sees its own uncommitted writes on both arms; and
// under an isolation level that gives a transaction a repeatable snapshot —
// MySQL's default — a grant committed mid-read cannot land on one side of the
// result but in neither arm.
func (r *Repository) ListPlatformAdministrators(ctx context.Context) ([]PlatformAdministrator, error) {
	return r.listPlatformAdministrators(ctx, nil)
}

func (r *Repository) listPlatformAdministrators(
	ctx context.Context,
	principalID *guid.ID,
) ([]PlatformAdministrator, error) {
	rows := make([]platformAdministratorRow, 0)
	err := r.runInTx(ctx, func(repo *Repository) error {
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		fullQuery := tables.PlatformAdministrators.WithContext(ctx).
			Select(tables.PlatformAdministrators.PrincipalID, tables.PlatformAdministrators.CreatedAt)
		names, err := repo.authorizationTables()
		if err != nil {
			return err
		}
		grantQuery := repo.writer().WithContext(ctx).Table(repo.names.RoleMembers.String()+" AS rm").
			Select("rm.principal_id, rp.permission_code, rm.created_at, r.name AS role_name").
			Joins("JOIN "+repo.names.Roles.String()+" AS r ON r.tenant_id = rm.tenant_id AND r.id = rm.role_id").
			Joins("JOIN "+repo.names.RolePermissions.String()+" AS rp ON rp.tenant_id = rm.tenant_id AND rp.role_id = rm.role_id").
			Joins("JOIN "+repo.names.TenantMembers.String()+" AS m ON m.tenant_id = rm.tenant_id AND m.principal_id = rm.principal_id").
			Joins("JOIN "+names.Principal.String()+" AS p ON p.id = rm.principal_id").
			Joins("JOIN "+names.Directory.Tenants.String()+" AS root ON root.id = rm.tenant_id").
			Where("root.slug = ? AND root.scope_kind = ? AND root.status = ? AND m.status = ? AND p.status = ?", SystemRootSlug, "system", "active", "active", "active").
			Where("r.name <> ? AND (rp.condition_json = ? OR rp.condition_json = ?)", SystemSuperadministratorRoleName, "", "null")
		if principalID != nil {
			fullQuery = fullQuery.Where(tables.PlatformAdministrators.PrincipalID.Eq(*principalID))
			grantQuery = grantQuery.Where("rm.principal_id = ?", *principalID)
		}
		fullRows := make([]platformAdministratorRow, 0)
		if err := fullQuery.Scan(&fullRows); err != nil {
			return err
		}
		grantRows := make([]platformAdministratorRow, 0)
		if err := grantQuery.Scan(&grantRows).Error; err != nil {
			return err
		}
		for index := range fullRows {
			fullRows[index].RecordKind = "full"
		}
		rows = append(rows, fullRows...)
		for _, row := range grantRows {
			if row.PermissionCode == "platform_administer" && !strings.HasPrefix(row.RoleName, SystemLegacyRolePrefix) {
				continue
			}
			row.RecordKind = "grant"
			rows = append(rows, row)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list platform administrators: %w", err)
	}
	// Reproduces the ORDER BY the merged statement carried. principal_id and
	// permission_code are read the way the statement compared them: a signed
	// BIGINT, and a column declared COLLATE utf8mb4_bin / COLLATE "C", both of
	// which order the same way Go compares int64 and string.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].PrincipalID != rows[j].PrincipalID {
			return rows[i].PrincipalID < rows[j].PrincipalID
		}
		if rows[i].RecordKind != rows[j].RecordKind {
			return platformRecordKindRank(rows[i].RecordKind) < platformRecordKindRank(rows[j].RecordKind)
		}
		if rows[i].PermissionCode != rows[j].PermissionCode {
			return rows[i].PermissionCode < rows[j].PermissionCode
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})

	result := make([]PlatformAdministrator, 0, len(rows))
	for _, row := range rows {
		if len(result) == 0 || result[len(result)-1].PrincipalID != row.PrincipalID {
			result = append(result, PlatformAdministrator{
				PrincipalID: row.PrincipalID, Permissions: []string{}, CreatedAt: row.CreatedAt.UTC(),
			})
		}
		administrator := &result[len(result)-1]
		if row.RecordKind == "full" {
			administrator.FullAdministrator = true
			administrator.CreatedAt = row.CreatedAt.UTC()
		} else {
			codes := administrator.Permissions
			if len(codes) == 0 || codes[len(codes)-1] != row.PermissionCode {
				administrator.Permissions = append(codes, row.PermissionCode)
			}
		}
	}
	return result, nil
}

// GetPlatformAdministrator returns one full or restricted platform
// administrator.
func (r *Repository) GetPlatformAdministrator(
	ctx context.Context,
	principalID guid.ID,
) (PlatformAdministrator, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return PlatformAdministrator{}, err
	}
	administrators, err := r.listPlatformAdministrators(ctx, &principalID)
	if err != nil {
		return PlatformAdministrator{}, err
	}
	if len(administrators) == 1 {
		return administrators[0], nil
	}
	return PlatformAdministrator{}, ErrPlatformAdministratorNotFound
}

// SetPlatformAdministrator atomically replaces one principal's full or
// restricted platform grants.
func (r *Repository) SetPlatformAdministrator(
	ctx context.Context,
	principalID guid.ID,
	full bool,
	permissions []string,
) (PlatformAdministrator, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return PlatformAdministrator{}, err
	}
	var administrator PlatformAdministrator
	err = r.runInTx(ctx, func(repo *Repository) error {
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		owner, err := repo.lockSystemAuthorization(ctx, principalID)
		if err != nil {
			return err
		}
		if err := repo.ensureSystemAdministratorMember(ctx, owner, principalID); err != nil {
			return err
		}
		if !full {
			if err := repo.lockPlatformAdministratorMutations(ctx); err != nil {
				return err
			}
		}
		if result := db.WithContext(ctx).Table(repo.names.RoleMembers.String()).
			Where("tenant_id = ? AND principal_id = ?", owner, principalID).Delete(&model.RoleMember{}); result.Error != nil {
			return fmt.Errorf("clear system role assignments: %w", result.Error)
		}
		now := repo.now().UTC().Truncate(time.Millisecond)
		if full {
			row := model.PlatformAdministrator{PrincipalID: principalID, CreatedAt: now}
			result := db.WithContext(ctx).Table(tables.PlatformAdministrators.TableName()).
				Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if result.Error != nil {
				return fmt.Errorf("grant platform administrator: %w", result.Error)
			}
			administrator, err = repo.GetPlatformAdministrator(ctx, principalID)
			return err
		}
		result := db.WithContext(ctx).Table(tables.PlatformAdministrators.TableName()).
			Where("principal_id = ?", principalID).Delete(&model.PlatformAdministrator{})
		if result.Error != nil {
			return fmt.Errorf("revoke platform administrator: %w", result.Error)
		}
		if len(permissions) == 0 {
			return ErrPlatformAdministratorNotFound
		}
		var role model.Role
		name := SystemLegacyRolePrefix + principalID.String()
		err = db.WithContext(ctx).Table(repo.names.Roles.String()).Where("tenant_id = ? AND name_key = ?", owner, roleNameKey(name)).Take(&role).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			created, createErr := repo.CreateRole(ctx, owner, name, "Legacy system permissions")
			if createErr != nil {
				return createErr
			}
			role.ID = created.ID
		} else if err != nil {
			return err
		}
		if err := db.WithContext(ctx).Table(repo.names.RolePermissions.String()).Where("tenant_id = ? AND role_id = ?", owner, role.ID).Delete(&model.RolePermission{}).Error; err != nil {
			return err
		}
		for _, code := range permissions {
			if _, err := repo.GrantPermission(ctx, owner, role.ID, code); err != nil {
				return err
			}
		}
		if _, err := repo.AddMember(ctx, owner, role.ID, principalID); err != nil {
			return err
		}
		if result.RowsAffected > 0 {
			if err := repo.assertPlatformAdministratorRemains(ctx); err != nil {
				return err
			}
		}
		administrator, err = repo.GetPlatformAdministrator(ctx, principalID)
		return err
	})
	return administrator, err
}

// DeletePlatformAdministrator revokes all platform access while preserving at
// least one full administrator.
func (r *Repository) DeletePlatformAdministrator(ctx context.Context, principalID guid.ID) (bool, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return false, err
	}
	var changed bool
	err = r.runInTx(ctx, func(repo *Repository) error {
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		owner, err := repo.lockSystemAuthorization(ctx, principalID)
		if err != nil {
			return err
		}
		if err := repo.lockPlatformAdministratorMutations(ctx); err != nil {
			return err
		}
		administrator := db.WithContext(ctx).Table(tables.PlatformAdministrators.TableName()).
			Where("principal_id = ?", principalID).Delete(&model.PlatformAdministrator{})
		if administrator.Error != nil {
			return fmt.Errorf("revoke platform administrator: %w", administrator.Error)
		}
		grants := db.WithContext(ctx).Table(repo.names.RoleMembers.String()).
			Where("tenant_id = ? AND principal_id = ?", owner, principalID).Delete(&model.RoleMember{})
		if grants.Error != nil {
			return fmt.Errorf("revoke platform grants: %w", grants.Error)
		}
		changed = administrator.RowsAffected+grants.RowsAffected > 0
		if administrator.RowsAffected > 0 {
			return repo.assertPlatformAdministratorRemains(ctx)
		}
		return nil
	})
	return changed, err
}

// lockPlatformAdministratorMutations serializes operations that may remove a
// full administrator. Locking every current row makes a later count observe
// the preceding transaction before it can remove another administrator.
func (r *Repository) lockPlatformAdministratorMutations(ctx context.Context) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	database := db.WithContext(ctx).Table(tables.PlatformAdministrators.TableName())
	if db.Dialector.Name() == "sqlite" {
		result := database.Where("principal_id > ?", 0).
			UpdateColumn("created_at", gorm.Expr("created_at"))
		if result.Error != nil {
			return fmt.Errorf("lock platform administrators: %w", result.Error)
		}
		return nil
	}
	var principalIDs []guid.ID
	if err := database.Select("principal_id").Order("principal_id").
		Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).
		Scan(&principalIDs).Error; err != nil {
		return fmt.Errorf("lock platform administrators: %w", err)
	}
	return nil
}

// CountFullPlatformAdministrators returns the durable full-administrator
// count.
func (r *Repository) CountFullPlatformAdministrators(ctx context.Context) (int, error) {
	tables, _, err := r.tables()
	if err != nil {
		return 0, err
	}
	count, err := tables.PlatformAdministrators.WithContext(ctx).Count()
	if err != nil {
		return 0, fmt.Errorf("count platform administrators: %w", err)
	}
	return int(count), nil
}

func (r *Repository) assertPlatformAdministratorRemains(ctx context.Context) error {
	count, err := r.CountFullPlatformAdministrators(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrLastPlatformAdministrator
	}
	return nil
}

func (s *Service) lockPlatformAccessMutation(ctx context.Context, repo *Repository, target guid.ID) (guid.ID, error) {
	actor := authn.PrincipalIDFromContext(ctx)
	owner, err := repo.lockSystemAuthorization(ctx, actor, target)
	if err != nil {
		return 0, err
	}
	if !actor.Zero() {
		names, err := repo.authorizationTables()
		if err != nil {
			return 0, err
		}
		full, err := systemFullAdministrator(ctx, repo.writer(), names.Tables.PlatformAdministrators.String(), actor)
		if err != nil {
			return 0, err
		}
		if !full {
			return 0, ErrPrivilegeEscalation
		}
	}
	return owner, nil
}

// PlatformPermissionCatalog returns all declared platform permissions in
// stable code order.
func (s *Service) PlatformPermissionCatalog(context.Context) []authz.Action {
	actions := make([]authz.Action, 0, 8)
	for _, action := range s.registry.All() {
		if action.Scope == authz.ActionScopeSystem {
			actions = append(actions, action)
		}
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Code < actions[j].Code })
	return actions
}

// ListPlatformAdministrators returns every full and restricted platform
// administrator.
func (s *Service) ListPlatformAdministrators(ctx context.Context) ([]PlatformAdministrator, error) {
	return s.repo.ListPlatformAdministrators(ctx)
}

// SetPlatformAdministrator validates and atomically replaces a principal's
// platform access.
func (s *Service) SetPlatformAdministrator(
	ctx context.Context,
	principalID guid.ID,
	full bool,
	permissions []string,
) (PlatformAdministrator, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return PlatformAdministrator{}, err
	}
	normalized, err := s.normalizePlatformPermissions(full, permissions)
	if err != nil {
		return PlatformAdministrator{}, err
	}
	record := newAuditRecord(ctx, platformAuditTenant, "platform_administrator_updated", "principal", principalID)
	record.PolicyChanged = true
	record.Detail["full_administrator"] = full
	record.Detail["permissions"] = normalized
	var administrator PlatformAdministrator
	err = s.writes.Run(ctx, record, func(repo *Repository) error {
		owner, lockErr := s.lockPlatformAccessMutation(ctx, repo, principalID)
		if lockErr != nil {
			return lockErr
		}
		administrator, err = repo.SetPlatformAdministrator(ctx, principalID, full, normalized)
		if err == nil {
			err = repo.bumpPolicyRevision(ctx, owner)
		}
		return err
	})
	return administrator, err
}

// DeletePlatformAdministrator revokes a principal's platform access while
// preserving the last full administrator.
func (s *Service) DeletePlatformAdministrator(ctx context.Context, principalID guid.ID) (bool, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return false, err
	}
	record := newAuditRecord(ctx, platformAuditTenant, "platform_administrator_revoked", "principal", principalID)
	record.PolicyChanged = true
	var changed bool
	err = s.writes.Run(ctx, record, func(repo *Repository) error {
		owner, lockErr := s.lockPlatformAccessMutation(ctx, repo, principalID)
		if lockErr != nil {
			return lockErr
		}
		changed, err = repo.DeletePlatformAdministrator(ctx, principalID)
		if err != nil {
			return err
		}
		record.Detail["changed"] = changed
		if !changed {
			return ErrPlatformAdministratorNotFound
		}
		return repo.bumpPolicyRevision(ctx, owner)
	})
	return changed, err
}

func (s *Service) normalizePlatformPermissions(full bool, permissions []string) ([]string, error) {
	if full {
		if len(permissions) > 0 {
			return nil, fmt.Errorf("%w: a full platform administrator does not accept explicit permissions", ErrInvalidArgument)
		}
		return []string{}, nil
	}
	if len(permissions) == 0 {
		return nil, fmt.Errorf("%w: a restricted platform administrator requires at least one permission", ErrInvalidArgument)
	}
	seen := map[string]bool{}
	normalized := make([]string, 0, len(permissions))
	for _, code := range permissions {
		code = strings.TrimSpace(code)
		action, ok := s.registry.Find(code)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrPermissionNotFound, code)
		}
		if action.Scope != authz.ActionScopeSystem {
			return nil, fmt.Errorf("%w: %s", ErrPlatformPermissionScope, code)
		}
		if !seen[code] {
			seen[code] = true
			normalized = append(normalized, code)
		}
	}
	sort.Strings(normalized)
	return normalized, nil
}

// ListPlatformAdministratorsOn reads full and restricted platform
// administrators through the caller's transaction. The bootstrap command needs
// the check and the following grant to observe one snapshot, so it cannot use
// the connection-picking read above.
func (r *Repository) ListPlatformAdministratorsOn(
	ctx context.Context,
	db *gorm.DB,
) ([]PlatformAdministrator, error) {
	if r == nil || db == nil {
		return nil, ErrUnavailable
	}
	return r.withDB(db).listPlatformAdministrators(ctx, nil)
}

// GrantFullPlatformAdministratorOn inserts one full administrator row through
// the caller's transaction and reports whether it created it. Reporting rather
// than failing on a duplicate is what keeps bootstrap create-only: a rerun
// says "already bootstrapped" instead of rewriting the row.
func (r *Repository) GrantFullPlatformAdministratorOn(
	ctx context.Context,
	db *gorm.DB,
	principalID guid.ID,
) (bool, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return false, err
	}
	if r == nil || db == nil {
		return false, ErrUnavailable
	}
	repo := r.withDB(db)
	tables, database, err := repo.tables()
	if err != nil {
		return false, err
	}
	row := model.PlatformAdministrator{
		PrincipalID: principalID,
		CreatedAt:   repo.now().UTC().Truncate(time.Millisecond),
	}
	result := database.WithContext(ctx).Table(tables.PlatformAdministrators.TableName()).
		Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return false, fmt.Errorf("grant platform administrator: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}
