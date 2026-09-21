package maintenance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/app"
	iamaudit "7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	configx "7link.com/platform/pkg/config"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// tenantRepairLockName serializes repairs across processes. It is a sibling of
// the platform-administrator lock rather than the same name, because two
// operators repairing two different tenants have no reason to wait on each
// other, and neither has any reason to wait on a bootstrap.
const tenantRepairLockName = "iam/repair/tenant-owner"

// TenantRepairOutcome reports what the repair command actually did.
type TenantRepairOutcome string

// TenantOwnerRepaired means this run wrote the membership, the Administrator
// role, its grant, and the tenant default menus.
const TenantOwnerRepaired TenantRepairOutcome = "owner-repaired"

var (
	// ErrTenantRepairConflict marks a state the operator has to resolve before
	// a repair can mean anything: the tenant already has somebody
	// administering it, it is deleted, or the login names a principal that
	// cannot administer anything.
	ErrTenantRepairConflict = errors.New("tenant owner repair conflict")
	// ErrTenantRepairNotFound marks a slug or a login that names nothing. It is
	// separate from a conflict because the fix is to correct the argument, not
	// to inspect who holds what.
	ErrTenantRepairNotFound = errors.New("tenant owner repair target not found")
)

// TenantRepairRequest carries the repair command operation-only settings. A
// tenant with no member has no reachable route and therefore no ID an operator
// could have read anywhere; the slug is the only handle they have.
type TenantRepairRequest struct {
	TenantSlug string `mapstructure:"tenant_slug" description:"slug of the tenant to repair" trim:"true"`
	OwnerLogin string `mapstructure:"owner_login" description:"login name of the principal to install as the tenant owner" trim:"true"`
}

// TenantRepairConfig combines the normal application configuration with the
// repair command own settings, the same way PlatformAdminConfig does.
type TenantRepairConfig struct {
	app.AppConfig       `mapstructure:",squash"`
	TenantRepairRequest `mapstructure:",squash"`
}

// TenantRepairReport describes what the repair wrote, in the terms an operator
// would use to confirm it: the tenant they named, and the person who can now
// sign in and administer it.
type TenantRepairReport struct {
	Outcome             TenantRepairOutcome `json:"outcome"`
	TenantID            guid.ID             `json:"tenant_id"`
	TenantSlug          string              `json:"tenant_slug"`
	OwnerPrincipalID    guid.ID             `json:"owner_principal_id"`
	OwnerLogin          string              `json:"owner_login"`
	AdministratorRoleID guid.ID             `json:"administrator_role_id"`
	PermissionCount     int                 `json:"permission_count"`
	MenuCount           int                 `json:"menu_count"`
}

// tenantRepairStores groups the narrow, transaction-bound capabilities the
// owning modules publish for this repair. Organization still decides which row
// a slug means, core still writes every membership and role table, and identity
// still owns logins; this boundary only orders them and shares one transaction.
type tenantRepairStores struct {
	tenants    organization.TenantLookup
	principals identity.BootstrapPrincipalStore
	bootstrap  core.TenantBootstrapper
	appender   iamaudit.Appender
	locker     *dlock.Module
	db         *gorm.DB
}

// RunTenantOwnerRepair installs the first owner on a tenant that has none,
// without starting any server. Tenants created before creation and bootstrap
// became one transaction can hold no member and no role at all, and every
// tenant route requires an active membership: nobody inside the product can
// reach such a tenant, so nobody inside the product can repair it either.
func RunTenantOwnerRepair(
	ctx context.Context,
	config TenantRepairConfig,
) (report TenantRepairReport, resultErr error) {
	if err := configx.Process(&config.TenantRepairRequest); err != nil {
		return TenantRepairReport{}, err
	}
	login := identity.NormalizeBootstrapLogin(config.OwnerLogin)
	// Validating before any database work keeps an operator typo from leaving a
	// half-open transaction and a taken lock behind.
	if login == "" {
		return TenantRepairReport{}, errors.New("tenant owner repair requires an owner login")
	}
	if config.TenantSlug == "" {
		return TenantRepairReport{}, errors.New("tenant owner repair requires a tenant slug")
	}
	err := app.RunMaintenance(ctx, config.AppConfig, func(services *module.Services, db *gorm.DB, locker *dlock.Module) error {
		admin, resolveErr := resolvePlatformAdminStores(services, db, locker)
		if resolveErr != nil {
			return resolveErr
		}
		stores, resolveErr := resolveTenantRepairStores(services, admin)
		if resolveErr != nil {
			return resolveErr
		}
		report, resolveErr = stores.repair(ctx, config.TenantSlug, login)
		return resolveErr
	})
	if err != nil {
		return TenantRepairReport{}, normalizeMaintenanceError(err)
	}
	return report, nil
}

// tenantRepairStores resolves the two capabilities this repair needs beyond
// what platform administration already resolved, and reuses the rest.
// Resolving them here rather than reaching into modules keeps the ownership
// rule intact: this boundary can only do what the owning modules chose to
// expose.
func resolveTenantRepairStores(services *module.Services, admin platformAdminStores) (tenantRepairStores, error) {
	if services == nil {
		return tenantRepairStores{}, fmt.Errorf("%w: modules are not composed", ErrPlatformAdminUnavailable)
	}
	tenants, err := module.Resolve[organization.TenantLookup](services)
	if err != nil {
		return tenantRepairStores{}, errors.Join(ErrPlatformAdminUnavailable, err)
	}
	bootstrap, err := module.Resolve[core.TenantBootstrapper](services)
	if err != nil {
		return tenantRepairStores{}, errors.Join(ErrPlatformAdminUnavailable, err)
	}
	return tenantRepairStores{
		tenants: tenants, principals: admin.principals, bootstrap: bootstrap,
		appender: admin.appender, locker: admin.locker, db: admin.db,
	}, nil
}

// repair takes the cross-process lock and then performs every write in one
// transaction, so a failure anywhere leaves the tenant exactly as unreachable
// as it was rather than half-owned.
func (stores tenantRepairStores) repair(
	ctx context.Context,
	slug, login string,
) (TenantRepairReport, error) {
	lockCtx, cancelLock := context.WithTimeout(ctx, platformAdminLockWait)
	defer cancelLock()
	lock, err := stores.locker.Lock(lockCtx, tenantRepairLockName)
	if err != nil {
		return TenantRepairReport{}, fmt.Errorf("acquire %s lock: %w", tenantRepairLockName, err)
	}
	defer func() {
		// Releasing must not inherit a canceled context, or a timed-out run
		// would leave its lease held until the TTL expires.
		_ = lock.Unlock(context.WithoutCancel(ctx))
	}()

	now := time.Now().UTC()
	var report TenantRepairReport
	err = stores.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		report, err = stores.decideAndWrite(ctx, tx, slug, login, now)
		return err
	})
	if err != nil {
		return TenantRepairReport{}, err
	}
	return report, nil
}

// decideAndWrite is the whole repair decision. It runs inside the caller
// transaction with the login row locked, so what it observes still holds at
// commit.
func (stores tenantRepairStores) decideAndWrite(
	ctx context.Context,
	tx *gorm.DB,
	slug, login string,
	now time.Time,
) (TenantRepairReport, error) {
	tenant, owner, err := stores.resolve(ctx, tx, slug, login)
	if err != nil {
		return TenantRepairReport{}, err
	}
	// This is what keeps the command create-only. A tenant that already has any
	// member or any role is being administered by somebody, and handing it a
	// second owner out of band would be a privilege grant disguised as a
	// repair.
	unclaimed, err := stores.bootstrap.IsUnclaimed(ctx, tx, tenant.ID)
	if err != nil {
		return TenantRepairReport{}, fmt.Errorf("check whether tenant %d is unclaimed: %w", tenant.ID, err)
	}
	if !unclaimed {
		return TenantRepairReport{}, fmt.Errorf(
			"%w: tenant %q already has a member or a role", ErrTenantRepairConflict, tenant.Slug,
		)
	}
	return stores.write(ctx, tx, tenant, owner, now)
}

// resolve turns the two names an operator typed into the two rows the repair
// acts on, refusing every shape that would leave a tenant nobody can still
// administer afterwards.
func (stores tenantRepairStores) resolve(
	ctx context.Context,
	tx *gorm.DB,
	slug, login string,
) (organization.Tenant, identity.Principal, error) {
	tenant, found, err := stores.tenants(ctx, tx, slug)
	if err != nil {
		return organization.Tenant{}, identity.Principal{}, fmt.Errorf("look up tenant %q: %w", slug, err)
	}
	if !found {
		return organization.Tenant{}, identity.Principal{}, fmt.Errorf(
			"%w: no tenant has slug %q", ErrTenantRepairNotFound, slug,
		)
	}
	if tenant.Status == organization.TenantDeleted {
		return organization.Tenant{}, identity.Principal{}, fmt.Errorf(
			"%w: tenant %q is deleted", ErrTenantRepairConflict, tenant.Slug,
		)
	}
	owner, found, err := stores.principals.Find(ctx, tx, login)
	if err != nil {
		return organization.Tenant{}, identity.Principal{}, fmt.Errorf("look up owner login: %w", err)
	}
	if !found {
		return organization.Tenant{}, identity.Principal{}, fmt.Errorf(
			"%w: no principal has login %q", ErrTenantRepairNotFound, login,
		)
	}
	if owner.Status != platformAdminActiveStatus {
		return organization.Tenant{}, identity.Principal{}, fmt.Errorf(
			"%w: principal %d is %s and cannot administer a tenant",
			ErrTenantRepairConflict, owner.ID, owner.Status,
		)
	}
	return tenant, owner, nil
}

// write performs the repair itself. It is deliberately the same core call the
// ordinary creation path makes, so a repaired tenant is indistinguishable from
// one that was bootstrapped correctly in the first place.
func (stores tenantRepairStores) write(
	ctx context.Context,
	tx *gorm.DB,
	tenant organization.Tenant,
	owner identity.Principal,
	now time.Time,
) (TenantRepairReport, error) {
	result, err := stores.bootstrap.Bootstrap(ctx, tx, core.TenantBootstrapInput{
		TenantID: tenant.ID, OwnerID: owner.ID,
		DisplayName: owner.DisplayName, Email: owner.Email,
	}, now)
	if err != nil {
		return TenantRepairReport{}, fmt.Errorf("bootstrap tenant %d: %w", tenant.ID, err)
	}
	// The repair grants a role and a membership, so anything that cached "this
	// principal may do nothing here" has to be told the answer changed, in the
	// same commit that changed it.
	if err := stores.bootstrap.BumpRevision(ctx, tx, tenant.ID, now); err != nil {
		return TenantRepairReport{}, err
	}
	event := iamaudit.NewDomainEvent(ctx, tenant.ID, "tenant_bootstrapped", "tenant", tenant.ID)
	event.PrincipalID = owner.ID
	event.Detail["owner_principal_id"] = owner.ID.String()
	event.Detail["administrator_role_id"] = result.AdministratorRoleID.String()
	event.Detail["permission_count"] = result.PermissionCount
	event.Detail["menu_count"] = result.MenuCount
	// An ordinary bootstrap and a repair write the same tables, so the audit
	// trail is the only place the difference survives. An auditor asking why a
	// tenant gained an owner long after it was created has to find it here.
	event.Detail["repaired"] = true
	if err := stores.appender(ctx, tx, event); err != nil {
		return TenantRepairReport{}, fmt.Errorf("append tenant repair audit event: %w", err)
	}
	return TenantRepairReport{
		Outcome: TenantOwnerRepaired, TenantID: tenant.ID, TenantSlug: tenant.Slug,
		OwnerPrincipalID: owner.ID, OwnerLogin: owner.LoginName,
		AdministratorRoleID: result.AdministratorRoleID,
		PermissionCount:     result.PermissionCount, MenuCount: result.MenuCount,
	}, nil
}
