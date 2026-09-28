package core

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The two permission codes that make a principal a durable administrator.
// A role holding either of them, unconditionally, keeps a tenant reachable.
const (
	tenantAdministratorPermission   = "tenant_administer"
	platformAdministratorPermission = "platform_administer"
)

type tenantAdministratorGuard struct {
	names     query.TableNames
	revisions *RevisionStore
	now       func() time.Time
	foreign   *foreignTables
	registry  *authz.Registry
}

// RequireGrantableRoles checks indirect directory grants on the mutation transaction.
func (guard *tenantAdministratorGuard) RequireGrantableRoles(ctx context.Context, db *gorm.DB, ownerID guid.ID, roleIDs []guid.ID) error {
	if guard == nil || db == nil || guard.registry == nil {
		return ErrUnavailable
	}
	checker := &Authorizer{db: db, repositoryTables: repositoryTables{names: guard.names}, registry: guard.registry, revisions: guard.revisions, now: guard.now, foreign: guard.foreign}
	repo := &Repository{db: db, repositoryTables: repositoryTables{names: guard.names}, foreign: guard.foreign, revisions: guard.revisions}
	kind, err := repo.rootKind(ctx, ownerID)
	if err != nil {
		return err
	}
	if kind == "system" {
		return ErrPrivilegeEscalation
	}
	service := &Service{repo: repo, checker: checker, registry: guard.registry}
	if err := service.requireGrantablePermissionsOn(ctx, repo, ownerID); err != nil {
		return err
	}
	if err := guard.revisions.Lock(ctx, db, ownerID); err != nil {
		return err
	}
	for _, roleID := range roleIDs {
		if err := service.requireGrantableRoleWithRepository(ctx, repo, ownerID, roleID); err != nil {
			return err
		}
	}
	return nil
}

// RequireGrantableDepartmentPlacement checks, on the mutation transaction, that
// placing a new member in departmentID with roleIDs reaches no department the
// actor's own data scope does not cover (requireGrantableDepartmentPlacement).
func (guard *tenantAdministratorGuard) RequireGrantableDepartmentPlacement(ctx context.Context, db *gorm.DB, ownerID, departmentID guid.ID, roleIDs []guid.ID) error {
	if guard == nil || db == nil || guard.registry == nil {
		return ErrUnavailable
	}
	checker := &Authorizer{db: db, repositoryTables: repositoryTables{names: guard.names}, registry: guard.registry, revisions: guard.revisions, now: guard.now, foreign: guard.foreign}
	repo := &Repository{db: db, repositoryTables: repositoryTables{names: guard.names}, foreign: guard.foreign, revisions: guard.revisions}
	service := &Service{repo: repo, checker: checker, registry: guard.registry}
	return service.requireGrantableDepartmentPlacement(ctx, repo, ownerID, departmentID, roleIDs)
}

// RequireGrantableDepartmentMove checks, on the mutation transaction, that the
// actor may re-parent a department (requireGrantableDepartmentMove).
func (guard *tenantAdministratorGuard) RequireGrantableDepartmentMove(ctx context.Context, db *gorm.DB, ownerID guid.ID) error {
	if guard == nil || db == nil || guard.registry == nil {
		return ErrUnavailable
	}
	checker := &Authorizer{db: db, repositoryTables: repositoryTables{names: guard.names}, registry: guard.registry, revisions: guard.revisions, now: guard.now, foreign: guard.foreign}
	repo := &Repository{db: db, repositoryTables: repositoryTables{names: guard.names}, foreign: guard.foreign, revisions: guard.revisions}
	service := &Service{repo: repo, checker: checker, registry: guard.registry}
	return service.requireGrantableDepartmentMove(ctx, repo, ownerID)
}

// PrepareGrantableRoles acquires global authority before the local policy fence.
func (guard *tenantAdministratorGuard) PrepareGrantableRoles(ctx context.Context, db *gorm.DB, ownerID guid.ID) error {
	return guard.RequireGrantableRoles(ctx, db, ownerID, nil)
}

// NewAdministratorGuard creates the tenant-administrator safety capability.
// The tables the durable-administrator statement reads arrive from the modules
// that own them through the connectors core publishes, so the guard counts
// administrators in the same schema the rest of the module writes to.
func NewAdministratorGuard(runtime *appenv.Runtime) *tenantAdministratorGuard {
	if runtime == nil {
		panic("IAM administrator guard requires runtime")
	}
	revisions, err := NewRevisionStore(runtime)
	if err != nil {
		panic(err)
	}
	return &tenantAdministratorGuard{
		names: query.BindNames(runtime.TablePrefix()), revisions: revisions, now: time.Now,
		foreign: &foreignTables{},
	}
}

func (guard *tenantAdministratorGuard) Protect(
	ctx context.Context,
	db *gorm.DB,
	tenantID guid.ID,
) (func() error, error) {
	if guard == nil || guard.now == nil || guard.revisions == nil || db == nil || tenantID.Zero() {
		return nil, errors.New("invalid tenant administrator guard configuration")
	}
	if err := guard.revisions.Lock(ctx, db, tenantID); err != nil {
		return nil, err
	}
	hadAdministrator, err := guard.hasDurableAdministrator(ctx, db, tenantID)
	if err != nil {
		return nil, err
	}
	return func() error {
		hasAdministrator, err := guard.hasDurableAdministrator(ctx, db, tenantID)
		if err != nil {
			return err
		}
		if hadAdministrator && !hasAdministrator {
			return ErrLastTenantAdministrator
		}
		return nil
	}, nil
}

func (guard *tenantAdministratorGuard) hasDurableAdministrator(
	ctx context.Context,
	db *gorm.DB,
	tenantID guid.ID,
) (bool, error) {
	now := guard.now().UTC().Truncate(time.Millisecond)
	tables, err := guard.foreign.authorizationTables(guard.names)
	if err != nil {
		return false, err
	}
	var root struct{ ScopeKind string }
	if err := db.WithContext(ctx).Table(tables.Directory.Tenants.String()).Select("scope_kind").Where("id = ?", tenantID).Take(&root).Error; err != nil {
		return false, err
	}
	permission, globalPermission := tenantAdministratorPermission, platformAdministratorPermission
	if root.ScopeKind == "platform" {
		permission, globalPermission = "operating_platform_administer", "operating_platform_administer"
	}
	if root.ScopeKind == "system" {
		var rows []struct{ PrincipalID guid.ID }
		protected := tables.Tables.PlatformAdministrators.String()
		read := db.WithContext(ctx)
		if db.Dialector.Name() == "mysql" {
			protected = "(SELECT principal_id FROM " + protected + " FOR UPDATE)"
		} else if db.Dialector.Name() == "postgres" {
			read = read.Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "pa"}})
		}
		// Lifecycle guards prohibit disabling principals that retain full authority.
		// Lock protected rows only: other principal mutexes precede the system fence.
		err := read.Table(protected+" AS pa").
			Select("pa.principal_id").Joins("JOIN "+tables.Principal.String()+" AS p ON p.id = pa.principal_id").
			Where("p.status = ?", "active").Find(&rows).Error
		return len(rows) > 0, err
	}
	count, err := query.CountDurableAdministrators(ctx, db, query.DurableAdministratorParams{
		AuthorizationTables:     tables,
		TenantID:                tenantID,
		Now:                     now,
		AdministratorPermission: permission,
		PlatformPermission:      globalPermission,
	})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

var _ AdministratorGuard = (*tenantAdministratorGuard)(nil)
