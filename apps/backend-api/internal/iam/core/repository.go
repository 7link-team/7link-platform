package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository persists core IAM policy through the runtime's current writer.
// A transactional clone pins one *gorm.DB so every statement in the mutation
// uses the same connection and transaction.
type Repository struct {
	runtime   *appenv.Runtime
	ids       guid.Source
	db        *gorm.DB
	revisions *RevisionStore
	now       func() time.Time
	foreign   *foreignTables
}

func NewRepository(runtime *appenv.Runtime, ids guid.Source) *Repository {
	if runtime == nil || ids == nil {
		panic("iam repository requires runtime and id generator")
	}
	revisions, err := NewRevisionStore(runtime)
	if err != nil {
		panic(err)
	}
	return &Repository{
		runtime: runtime, ids: ids, revisions: revisions,
		now:     func() time.Time { return time.Now().UTC() },
		foreign: &foreignTables{},
	}
}

// authorizationTables binds the names a written statement reads: core's own
// tables from its deployment prefix, and the ones it only joins from the
// modules that own them. An owner that has not published is ErrUnavailable,
// not an empty table name in a statement.
func (r *Repository) authorizationTables() (query.AuthorizationTables, error) {
	return r.foreign.authorizationTables(r.prefix())
}

// directoryTables returns the published directory names, or ErrUnavailable
// when the organization module never published them.
func (r *Repository) directoryTables() (query.DirectoryTables, error) {
	return r.foreign.directoryNames()
}

// BindForeignTables records the table-name sets of the tables this repository
// reads but does not own. The modules that own them call it through the
// connectors core publishes; a caller that builds a repository outside the
// module has to call it itself, because an unbound repository reports
// ErrUnavailable rather than running a statement against an empty table name.
func (r *Repository) BindForeignTables(directory DirectoryTableNames, principal PrincipalTableNames) error {
	if r == nil {
		return ErrUnavailable
	}
	if r.foreign == nil {
		r.foreign = &foreignTables{}
	}
	return r.foreign.bind(directory, principal)
}

func (r *Repository) writer() *gorm.DB {
	if r == nil {
		return nil
	}
	if r.db != nil {
		return r.db
	}
	if r.runtime == nil || r.runtime.Database() == nil {
		return nil
	}
	return r.runtime.Database().Write()
}

func (r *Repository) prefix() string {
	if r == nil || r.runtime == nil {
		return ""
	}
	return r.runtime.TablePrefix()
}

func (r *Repository) tables() (*query.Tables, *gorm.DB, error) {
	db := r.writer()
	if db == nil {
		return nil, nil, ErrUnavailable
	}
	return query.Bind(db, r.prefix()), db, nil
}

func (r *Repository) withDB(db *gorm.DB) *Repository {
	transactional := *r
	transactional.db = db
	return &transactional
}

func (r *Repository) pinWriter() (*Repository, error) {
	if r == nil {
		return nil, ErrUnavailable
	}
	if r.db != nil {
		return r, nil
	}
	db := r.writer()
	if db == nil {
		return nil, ErrUnavailable
	}
	return r.withDB(db), nil
}

func (r *Repository) runInTx(ctx context.Context, fn func(*Repository) error) error {
	if r == nil || fn == nil {
		return errors.New("invalid IAM transaction")
	}
	if r.db != nil {
		return fn(r)
	}
	db := r.writer()
	if db == nil {
		return ErrUnavailable
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(r.withDB(tx))
	})
}

func (r *Repository) nextID() (guid.ID, error) {
	if r == nil || r.ids == nil {
		return 0, ErrUnavailable
	}
	id, err := r.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, errors.New("generated IAM ID must be positive")
	}
	return guid.ID(id), nil
}

func (r *Repository) CreateRole(ctx context.Context, tenantID guid.ID, name, description string) (Role, error) {
	tables, _, err := r.tables()
	if err != nil {
		return Role{}, err
	}
	id, err := r.nextID()
	if err != nil {
		return Role{}, fmt.Errorf("generate role id: %w", err)
	}
	now := r.now().UTC().Truncate(time.Millisecond)
	row := model.Role{
		TenantID: tenantID, ID: id, Name: name, NameKey: roleNameKey(name),
		Description: description, CreatedAt: now, UpdatedAt: now,
	}
	if err := tables.Roles.WithContext(ctx).Create(&row); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return Role{}, ErrRoleNameConflict
		}
		return Role{}, fmt.Errorf("insert role: %w", err)
	}
	return roleFromRow(&row), nil
}

func (r *Repository) GrantPlatformAdministrator(ctx context.Context, principalID guid.ID) (bool, error) {
	if principalID.Zero() {
		return false, errors.New("invalid platform administrator principal")
	}
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	row := model.PlatformAdministrator{
		PrincipalID: principalID,
		CreatedAt:   r.now().UTC().Truncate(time.Millisecond),
	}
	result := db.WithContext(ctx).Table(tables.PlatformAdministrators.TableName()).
		Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return false, fmt.Errorf("grant platform administrator: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

func (r *Repository) ListRoles(ctx context.Context, tenantID guid.ID) ([]Role, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	rows, err := tables.Roles.WithContext(ctx).Where(tables.Roles.TenantID.Eq(tenantID)).
		Order(tables.Roles.Name, tables.Roles.ID).Find()
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	roles := make([]Role, len(rows))
	for index, row := range rows {
		roles[index] = roleFromRow(row)
	}
	return roles, nil
}

func (r *Repository) GetRole(ctx context.Context, tenantID, roleID guid.ID) (Role, error) {
	row, err := r.getRoleRow(ctx, tenantID, roleID)
	if err != nil {
		return Role{}, err
	}
	return roleFromRow(row), nil
}

func (r *Repository) UpdateRole(ctx context.Context, tenantID, roleID guid.ID, name, description string) (Role, error) {
	var updated Role
	err := r.runInTx(ctx, func(repo *Repository) error {
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		info, err := tables.Roles.WithContext(ctx).
			Where(tables.Roles.TenantID.Eq(tenantID), tables.Roles.ID.Eq(roleID)).
			UpdateColumnSimple(
				tables.Roles.Name.Value(name),
				tables.Roles.NameKey.Value(roleNameKey(name)),
				tables.Roles.Description.Value(description),
				tables.Roles.UpdatedAt.Value(repo.now().UTC().Truncate(time.Millisecond)),
			)
		if err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return ErrRoleNameConflict
			}
			return fmt.Errorf("update role: %w", err)
		}
		if info.RowsAffected == 0 {
			return ErrRoleNotFound
		}
		updated, err = repo.GetRole(ctx, tenantID, roleID)
		return err
	})
	return updated, err
}

func (r *Repository) DeleteRole(ctx context.Context, tenantID, roleID guid.ID) error {
	return r.runInTx(ctx, func(repo *Repository) error {
		if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
			return err
		}
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		if _, err := tables.Roles.WithContext(ctx).
			Where(tables.Roles.TenantID.Eq(tenantID), tables.Roles.ID.Eq(roleID)).Delete(); err != nil {
			return fmt.Errorf("delete role: %w", err)
		}
		return nil
	})
}

func (r *Repository) GrantPermission(ctx context.Context, tenantID, roleID guid.ID, code string) (bool, error) {
	return r.changePermission(ctx, tenantID, roleID, code, true)
}

func (r *Repository) RevokePermission(ctx context.Context, tenantID, roleID guid.ID, code string) (bool, error) {
	return r.changePermission(ctx, tenantID, roleID, code, false)
}

func (r *Repository) changePermission(ctx context.Context, tenantID, roleID guid.ID, code string, grant bool) (bool, error) {
	var changed bool
	err := r.runInTx(ctx, func(repo *Repository) error {
		if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
			return err
		}
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		if grant {
			row := model.RolePermission{
				TenantID: tenantID, RoleID: roleID, PermissionCode: code,
				CreatedAt: repo.now().UTC().Truncate(time.Millisecond),
			}
			result := db.WithContext(ctx).Table(tables.RolePermissions.TableName()).
				Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if result.Error != nil {
				return fmt.Errorf("insert role permission: %w", result.Error)
			}
			changed = result.RowsAffected > 0
			return nil
		}
		info, err := tables.RolePermissions.WithContext(ctx).
			Where(
				tables.RolePermissions.TenantID.Eq(tenantID),
				tables.RolePermissions.RoleID.Eq(roleID),
				tables.RolePermissions.PermissionCode.Eq(code),
			).Delete()
		if err != nil {
			return fmt.Errorf("delete role permission: %w", err)
		}
		changed = info.RowsAffected > 0
		return nil
	})
	return changed, err
}

func (r *Repository) ListPermissions(ctx context.Context, tenantID, roleID guid.ID) ([]string, error) {
	grants, err := r.ListPermissionGrants(ctx, tenantID, roleID)
	if err != nil {
		return nil, err
	}
	codes := make([]string, len(grants))
	for index, grant := range grants {
		codes[index] = grant.PermissionCode
	}
	return codes, nil
}

func (r *Repository) ListPermissionGrants(ctx context.Context, tenantID, roleID guid.ID) ([]RolePermissionGrant, error) {
	repo, err := r.pinWriter()
	if err != nil {
		return nil, err
	}
	if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
		return nil, err
	}
	tables, _, err := repo.tables()
	if err != nil {
		return nil, err
	}
	rows, err := tables.RolePermissions.WithContext(ctx).
		Where(tables.RolePermissions.TenantID.Eq(tenantID), tables.RolePermissions.RoleID.Eq(roleID)).
		Order(tables.RolePermissions.PermissionCode).Find()
	if err != nil {
		return nil, fmt.Errorf("list role permissions: %w", err)
	}
	grants := make([]RolePermissionGrant, len(rows))
	for index, row := range rows {
		grants[index] = rolePermissionGrantFromRow(row)
	}
	return grants, nil
}

func (r *Repository) SetPermissionCondition(
	ctx context.Context,
	tenantID, roleID guid.ID,
	code string,
	condition json.RawMessage,
) (RolePermissionGrant, bool, error) {
	var grant RolePermissionGrant
	var changed bool
	err := r.runInTx(ctx, func(repo *Repository) error {
		if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
			return err
		}
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		row, err := tables.RolePermissions.WithContext(ctx).
			Where(
				tables.RolePermissions.TenantID.Eq(tenantID),
				tables.RolePermissions.RoleID.Eq(roleID),
				tables.RolePermissions.PermissionCode.Eq(code),
			).Take()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRolePermissionNotGranted
		}
		if err != nil {
			return fmt.Errorf("get role permission: %w", err)
		}
		if row.ConditionJSON == string(condition) {
			grant = rolePermissionGrantFromRow(row)
			return nil
		}
		info, err := tables.RolePermissions.WithContext(ctx).
			Where(
				tables.RolePermissions.TenantID.Eq(tenantID),
				tables.RolePermissions.RoleID.Eq(roleID),
				tables.RolePermissions.PermissionCode.Eq(code),
			).UpdateSimple(tables.RolePermissions.ConditionJSON.Value(string(condition)))
		if err != nil {
			return fmt.Errorf("update role permission condition: %w", err)
		}
		if info.RowsAffected != 1 {
			return ErrRolePermissionNotGranted
		}
		row.ConditionJSON = string(condition)
		grant, changed = rolePermissionGrantFromRow(row), true
		return nil
	})
	return grant, changed, err
}

func (r *Repository) AddMember(ctx context.Context, tenantID, roleID, principalID guid.ID) (bool, error) {
	return r.changeMember(ctx, tenantID, roleID, principalID, true)
}

func (r *Repository) RemoveMember(ctx context.Context, tenantID, roleID, principalID guid.ID) (bool, error) {
	return r.changeMember(ctx, tenantID, roleID, principalID, false)
}

func (r *Repository) changeMember(ctx context.Context, tenantID, roleID, principalID guid.ID, add bool) (bool, error) {
	var changed bool
	err := r.runInTx(ctx, func(repo *Repository) error {
		if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
			return err
		}
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		if add {
			row := model.RoleMember{
				TenantID: tenantID, RoleID: roleID, PrincipalID: principalID,
				CreatedAt: repo.now().UTC().Truncate(time.Millisecond),
			}
			result := db.WithContext(ctx).Table(tables.RoleMembers.TableName()).
				Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if result.Error != nil {
				return fmt.Errorf("insert role member: %w", result.Error)
			}
			changed = result.RowsAffected > 0
			return nil
		}
		info, err := tables.RoleMembers.WithContext(ctx).
			Where(
				tables.RoleMembers.TenantID.Eq(tenantID),
				tables.RoleMembers.RoleID.Eq(roleID),
				tables.RoleMembers.PrincipalID.Eq(principalID),
			).Delete()
		if err != nil {
			return fmt.Errorf("delete role member: %w", err)
		}
		changed = info.RowsAffected > 0
		return nil
	})
	return changed, err
}

func (r *Repository) ListMembers(ctx context.Context, tenantID, roleID guid.ID) ([]guid.ID, error) {
	repo, err := r.pinWriter()
	if err != nil {
		return nil, err
	}
	if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
		return nil, err
	}
	tables, _, err := repo.tables()
	if err != nil {
		return nil, err
	}
	rows, err := tables.RoleMembers.WithContext(ctx).
		Select(tables.RoleMembers.PrincipalID).
		Where(tables.RoleMembers.TenantID.Eq(tenantID), tables.RoleMembers.RoleID.Eq(roleID)).
		Order(tables.RoleMembers.PrincipalID).Find()
	if err != nil {
		return nil, fmt.Errorf("list role members: %w", err)
	}
	principals := make([]guid.ID, len(rows))
	for index, row := range rows {
		principals[index] = row.PrincipalID
	}
	return principals, nil
}

func (r *Repository) getRoleRow(ctx context.Context, tenantID, roleID guid.ID) (*model.Role, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	row, err := tables.Roles.WithContext(ctx).
		Where(tables.Roles.TenantID.Eq(tenantID), tables.Roles.ID.Eq(roleID)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRoleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get role: %w", err)
	}
	return row, nil
}

func (r *Repository) ensureRole(ctx context.Context, tenantID, roleID guid.ID) error {
	_, err := r.getRoleRow(ctx, tenantID, roleID)
	return err
}

func roleNameKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func roleFromRow(row *model.Role) Role {
	return Role{
		ID: row.ID, TenantID: row.TenantID, Name: row.Name, Description: row.Description,
		CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
}

func rolePermissionGrantFromRow(row *model.RolePermission) RolePermissionGrant {
	var condition json.RawMessage
	if row.ConditionJSON != "" {
		condition = json.RawMessage(row.ConditionJSON)
	}
	return RolePermissionGrant{
		PermissionCode: row.PermissionCode,
		Condition:      condition,
		CreatedAt:      row.CreatedAt.UTC(),
	}
}
