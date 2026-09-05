package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	iamaudit "7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	configx "7link.com/platform/pkg/config"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// platformAdminLockName serializes bootstrap across processes. The in-transaction
// re-check still decides the outcome; the lock only keeps two operators from
// racing into a rollback they would both have to interpret.
const platformAdminLockName = "iam/bootstrap/platform-admin"

// platformAdminLockWait bounds how long the command waits for another bootstrap
// to finish. Waiting forever would turn a stuck peer into a hung deployment
// step with no diagnosis.
const platformAdminLockWait = 30 * time.Second

// platformAuditTenant is the tenant every platform-scoped audit event belongs
// to. Platform administration sits outside any tenant.
const platformAuditTenant guid.ID = 0

// PlatformAdminOutcome reports what the bootstrap command actually did.
type PlatformAdminOutcome string

const (
	// PlatformAdminCreated means this run wrote the principal, its credential,
	// and the administrator row.
	PlatformAdminCreated PlatformAdminOutcome = "created"
	// PlatformAdminAlreadyBootstrapped means the login was already a full
	// platform administrator and nothing was written. Bootstrap is create-only:
	// password rotation belongs to a separate audited reset flow.
	PlatformAdminAlreadyBootstrapped PlatformAdminOutcome = "already-bootstrapped"
)

var (
	// ErrPlatformAdminConflict marks a state an operator has to resolve before
	// bootstrap can mean anything: someone else already holds platform access,
	// or the requested login belongs to a principal that is not that
	// administrator.
	ErrPlatformAdminConflict = errors.New("platform administrator bootstrap conflict")
	// ErrPlatformAdminSchemaNotReady marks an unmigrated or dirty database.
	// It is separate from a conflict because the fix is to run migrations, not
	// to inspect who holds what.
	ErrPlatformAdminSchemaNotReady = errors.New("platform administrator bootstrap requires clean migrations")
	// ErrPlatformAdminUnavailable marks a missing capability: IAM disabled, no
	// writable database, or a module that did not publish its bootstrap store.
	ErrPlatformAdminUnavailable = errors.New("platform administrator bootstrap is unavailable")
)

// PlatformAdminRequest carries the bootstrap command's operation-only settings.
// The password is deliberately absent: it arrives as an argument read from a
// file or standard input, so it never enters the configuration surface that
// the config inspector, logs, or a config file can reach.
type PlatformAdminRequest struct {
	Login        string `mapstructure:"login" description:"login name of the platform administrator to create" trim:"true"`
	DisplayName  string `mapstructure:"display_name" description:"display name; defaults to the login name" trim:"true"`
	Email        string `mapstructure:"email" description:"contact email address; optional" trim:"true"`
	PasswordFile string `mapstructure:"password_file" description:"file holding the password; standard input is read when empty" trim:"true"`
}

// PlatformAdminConfig combines the application's normal configuration with the
// bootstrap command's own settings, the same way MigrationConfig does.
type PlatformAdminConfig struct {
	AppConfig            `mapstructure:",squash"`
	PlatformAdminRequest `mapstructure:",squash"`
}

// PlatformAdminReport describes the bootstrap result. It never carries the
// password or its hash.
type PlatformAdminReport struct {
	Outcome     PlatformAdminOutcome `json:"outcome"`
	PrincipalID guid.ID              `json:"principal_id"`
	Login       string               `json:"login"`
}

// platformAdminStores groups the narrow, transaction-bound capabilities the
// three owning modules publish. Each module still writes its own tables; this
// boundary only decides the order and shares one transaction.
type platformAdminStores struct {
	principals  identity.BootstrapPrincipalStore
	credentials iamauthn.BootstrapCredentialStore
	administs   core.PlatformAdministratorStore
	appender    iamaudit.Appender
	locker      *dlock.Module
	db          *gorm.DB
}

// RunPlatformAdminBootstrap creates the first platform administrator without
// starting any server. It composes and prepares the same Module Registry as the
// service path, then drives one transaction across identity, authentication,
// core, and audit through capabilities those modules publish.
func (app *App) RunPlatformAdminBootstrap(
	ctx context.Context,
	config PlatformAdminConfig,
	password string,
) (report PlatformAdminReport, resultErr error) {
	if err := configx.Process(&config.PlatformAdminRequest); err != nil {
		return PlatformAdminReport{}, err
	}
	login := identity.NormalizeBootstrapLogin(config.Login)
	if login == "" {
		return PlatformAdminReport{}, errors.New("bootstrap requires a login name")
	}
	// Validating before any database work keeps an operator typo from leaving a
	// half-open transaction and a taken lock behind.
	if err := iamauthn.ValidateBootstrapPassword(password); err != nil {
		return PlatformAdminReport{}, err
	}
	if err := configureApplication(&config.AppConfig); err != nil {
		return PlatformAdminReport{}, err
	}
	app.config = config.AppConfig
	defer func() {
		resultErr = errors.Join(resultErr, app.shutdown())
	}()
	if err := app.prepare(ctx); err != nil {
		return PlatformAdminReport{}, fmt.Errorf("prepare bootstrap application: %w", err)
	}
	// A dirty or unmigrated schema would surface as an opaque "no such table";
	// checking first turns it into its own outcome with its own exit status.
	if _, err := app.migrateModules(ctx, MigrationRequest{Action: MigrationValidate}); err != nil {
		return PlatformAdminReport{}, errors.Join(ErrPlatformAdminSchemaNotReady, err)
	}
	// The bootstrap writes through the same ID generator, password hashing, and
	// audit chain the running service uses, and the ID generator only leases a
	// worker after the module lifecycle starts. Driving the lifecycle is what
	// makes "the same as production" true rather than approximately true. No
	// listener opens: the HTTP and gRPC servers are started by Run, not here.
	if err := app.modules.Initialize(ctx); err != nil {
		return PlatformAdminReport{}, fmt.Errorf("initialize bootstrap modules: %w", err)
	}
	if err := app.modules.Start(ctx); err != nil {
		return PlatformAdminReport{}, fmt.Errorf("start bootstrap modules: %w", err)
	}
	stores, err := app.platformAdminStores()
	if err != nil {
		return PlatformAdminReport{}, err
	}
	return stores.bootstrap(ctx, identity.BootstrapPrincipalInput{
		LoginName: login, DisplayName: config.DisplayName, Email: config.Email,
	}, password)
}

// platformAdminStores resolves the published bootstrap capabilities. Resolving
// them here rather than reaching into modules keeps the ownership rule intact:
// this boundary can only do what the owning modules chose to expose.
func (app *App) platformAdminStores() (platformAdminStores, error) {
	if app.modules == nil {
		return platformAdminStores{}, fmt.Errorf("%w: modules are not composed", ErrPlatformAdminUnavailable)
	}
	if app.runtime == nil || app.runtime.Database() == nil || app.runtime.Database().Write() == nil {
		return platformAdminStores{}, fmt.Errorf("%w: no writable database", ErrPlatformAdminUnavailable)
	}
	services := app.modules.Services()
	principals, err := module.Resolve[identity.BootstrapPrincipalStore](services)
	if err != nil {
		return platformAdminStores{}, errors.Join(ErrPlatformAdminUnavailable, err)
	}
	credentials, err := module.Resolve[iamauthn.BootstrapCredentialStore](services)
	if err != nil {
		return platformAdminStores{}, errors.Join(ErrPlatformAdminUnavailable, err)
	}
	administrators, err := module.Resolve[core.PlatformAdministratorStore](services)
	if err != nil {
		return platformAdminStores{}, errors.Join(ErrPlatformAdminUnavailable, err)
	}
	appender, err := module.Resolve[iamaudit.Appender](services)
	if err != nil {
		return platformAdminStores{}, errors.Join(ErrPlatformAdminUnavailable, err)
	}
	locks, found := app.modules.ByName(dlock.Name)
	locker, isLocker := locks.(*dlock.Module)
	if !found || !isLocker {
		return platformAdminStores{}, fmt.Errorf("%w: distributed lock module is not active", ErrPlatformAdminUnavailable)
	}
	return platformAdminStores{
		principals: principals, credentials: credentials, administs: administrators,
		appender: appender, locker: locker, db: app.runtime.Database().Write(),
	}, nil
}

// bootstrap takes the cross-process lock and then performs every write in one
// transaction, so a failure anywhere leaves no principal, no credential, and no
// administrator row behind.
func (stores platformAdminStores) bootstrap(
	ctx context.Context,
	input identity.BootstrapPrincipalInput,
	password string,
) (PlatformAdminReport, error) {
	lockCtx, cancelLock := context.WithTimeout(ctx, platformAdminLockWait)
	defer cancelLock()
	lock, err := stores.locker.Lock(lockCtx, platformAdminLockName)
	if err != nil {
		return PlatformAdminReport{}, fmt.Errorf("acquire %s lock: %w", platformAdminLockName, err)
	}
	defer func() {
		// Releasing must not inherit a canceled context, or a timed-out run
		// would leave its lease held until the TTL expires.
		_ = lock.Unlock(context.WithoutCancel(ctx))
	}()

	now := time.Now().UTC()
	var report PlatformAdminReport
	err = stores.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		report, err = stores.decideAndWrite(ctx, tx, input, password, now)
		return err
	})
	if err != nil {
		return PlatformAdminReport{}, err
	}
	return report, nil
}

// decideAndWrite is the whole bootstrap decision. It runs inside the caller's
// transaction with the login row locked, so what it observes still holds at
// commit.
func (stores platformAdminStores) decideAndWrite(
	ctx context.Context,
	tx *gorm.DB,
	input identity.BootstrapPrincipalInput,
	password string,
	now time.Time,
) (PlatformAdminReport, error) {
	existing, found, err := stores.principals.Find(ctx, tx, input.LoginName)
	if err != nil {
		return PlatformAdminReport{}, fmt.Errorf("look up bootstrap login: %w", err)
	}
	administrators, err := stores.administs.List(ctx, tx)
	if err != nil {
		return PlatformAdminReport{}, fmt.Errorf("read platform administrators: %w", err)
	}
	for _, administrator := range administrators {
		if !found || administrator.PrincipalID != existing.ID {
			return PlatformAdminReport{}, fmt.Errorf(
				"%w: principal %d already holds platform access", ErrPlatformAdminConflict, administrator.PrincipalID,
			)
		}
	}
	if found {
		return stores.reportExisting(ctx, tx, existing, administrators)
	}
	return stores.create(ctx, tx, input, password, now)
}

// reportExisting handles a login that already exists. Only a full administrator
// with a usable credential counts as bootstrapped; every other shape is a
// conflict, because reporting success would tell an operator a login works when
// nobody can sign in with it.
func (stores platformAdminStores) reportExisting(
	ctx context.Context,
	tx *gorm.DB,
	existing identity.Principal,
	administrators []core.PlatformAdministrator,
) (PlatformAdminReport, error) {
	if len(administrators) != 1 || !administrators[0].FullAdministrator {
		return PlatformAdminReport{}, fmt.Errorf(
			"%w: login %q already belongs to principal %d, which is not a full platform administrator",
			ErrPlatformAdminConflict, existing.LoginName, existing.ID,
		)
	}
	hasCredential, err := stores.credentials.Exists(ctx, tx, existing.ID)
	if err != nil {
		return PlatformAdminReport{}, fmt.Errorf("read bootstrap credential: %w", err)
	}
	if !hasCredential {
		return PlatformAdminReport{}, fmt.Errorf(
			"%w: platform administrator %d has no password credential", ErrPlatformAdminConflict, existing.ID,
		)
	}
	return PlatformAdminReport{
		Outcome: PlatformAdminAlreadyBootstrapped, PrincipalID: existing.ID, Login: existing.LoginName,
	}, nil
}

// create writes the principal, its credential, the administrator row, and the
// audit event. Ordering puts the administrator grant last so a duplicate there
// rolls back the identity it would otherwise have orphaned.
func (stores platformAdminStores) create(
	ctx context.Context,
	tx *gorm.DB,
	input identity.BootstrapPrincipalInput,
	password string,
	now time.Time,
) (PlatformAdminReport, error) {
	principalID, err := stores.principals.Create(ctx, tx, input, now)
	if err != nil {
		return PlatformAdminReport{}, fmt.Errorf("create bootstrap principal: %w", err)
	}
	if err := stores.credentials.Create(ctx, tx, principalID, password, now); err != nil {
		return PlatformAdminReport{}, fmt.Errorf("create bootstrap credential: %w", err)
	}
	granted, err := stores.administs.GrantFull(ctx, tx, principalID)
	if err != nil {
		return PlatformAdminReport{}, fmt.Errorf("grant platform administrator: %w", err)
	}
	if !granted {
		return PlatformAdminReport{}, fmt.Errorf(
			"%w: principal %d already holds platform access", ErrPlatformAdminConflict, principalID,
		)
	}
	event := iamaudit.NewDomainEvent(
		ctx, platformAuditTenant, "platform_administrator_bootstrapped", "principal", principalID,
	)
	event.PrincipalID = principalID
	event.Detail["login_name"] = input.LoginName
	event.Detail["full_administrator"] = true
	if err := stores.appender(ctx, tx, event); err != nil {
		return PlatformAdminReport{}, fmt.Errorf("append bootstrap audit event: %w", err)
	}
	return PlatformAdminReport{
		Outcome: PlatformAdminCreated, PrincipalID: principalID, Login: input.LoginName,
	}, nil
}
