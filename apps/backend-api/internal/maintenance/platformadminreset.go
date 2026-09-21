package maintenance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/app"
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

// PlatformAdminPasswordReset means this run replaced the administrator
// password. It is deliberately a different outcome from a bootstrap: one
// creates access that did not exist, the other takes access back.
const PlatformAdminPasswordReset PlatformAdminOutcome = "password-reset"

// platformAdminActiveStatus is the only principal status a reset will act on.
// Handing a working password to a disabled account would quietly restore access
// somebody deliberately withdrew.
const platformAdminActiveStatus = "active"

// ErrPlatformAdminNotBootstrapped marks a reset aimed at an administrator that
// was never created. It is separate from a conflict because the fix is the
// bootstrap command, not an inspection of who holds what.
var ErrPlatformAdminNotBootstrapped = errors.New("platform administrator has not been bootstrapped")

// PlatformAdminResetRequest carries the reset command operation-only settings.
// Like the bootstrap request it holds no password: the value arrives as an
// argument read from a file or standard input, so it never enters the
// configuration surface that the config inspector, logs, or a config file can
// reach.
type PlatformAdminResetRequest struct {
	Login        string `mapstructure:"login" description:"login name of the platform administrator to reset" trim:"true"`
	PasswordFile string `mapstructure:"password_file" description:"file holding the new password; standard input is read when empty" trim:"true"`
}

// PlatformAdminResetConfig combines the application normal configuration with
// the reset command own settings, the same way PlatformAdminConfig does.
type PlatformAdminResetConfig struct {
	app.AppConfig             `mapstructure:",squash"`
	PlatformAdminResetRequest `mapstructure:",squash"`
}

// RunPlatformAdminPasswordReset replaces the platform administrator password
// without starting any server and without presenting the old one.
//
// It exists because the administrator that can lock everybody else out is the
// one account no in-band recovery can rescue: there is nobody above it to
// approve the rescue, and a self-service reset link is only as trustworthy as
// the mailbox behind it. Proving you can run a command on the server is the
// authority this path accepts instead.
func RunPlatformAdminPasswordReset(
	ctx context.Context,
	config PlatformAdminResetConfig,
	password string,
) (report PlatformAdminReport, resultErr error) {
	if err := configx.Process(&config.PlatformAdminResetRequest); err != nil {
		return PlatformAdminReport{}, err
	}
	login := identity.NormalizeBootstrapLogin(config.Login)
	if login == "" {
		return PlatformAdminReport{}, errors.New("reset requires a login name")
	}
	// Validating before any database work keeps an operator typo from leaving a
	// half-open transaction and a taken lock behind.
	if err := iamauthn.ValidateBootstrapPassword(password); err != nil {
		return PlatformAdminReport{}, err
	}
	err := app.RunMaintenance(ctx, config.AppConfig, func(services *module.Services, db *gorm.DB, locker *dlock.Module) error {
		stores, resolveErr := resolvePlatformAdminStores(services, db, locker)
		if resolveErr != nil {
			return resolveErr
		}
		report, resolveErr = stores.reset(ctx, login, password)
		return resolveErr
	})
	if err != nil {
		return PlatformAdminReport{}, normalizeMaintenanceError(err)
	}
	return report, nil
}

// reset takes the same cross-process lock the bootstrap takes, so a reset and a
// bootstrap racing on one login cannot both decide what that login is.
func (stores platformAdminStores) reset(
	ctx context.Context,
	login string,
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
		report, err = stores.resetPassword(ctx, tx, login, password, now)
		return err
	})
	if err != nil {
		return PlatformAdminReport{}, err
	}
	return report, nil
}

// resetPassword is the whole reset decision. It runs inside the caller
// transaction with the login row locked, so what it observes still holds at
// commit.
func (stores platformAdminStores) resetPassword(
	ctx context.Context,
	tx *gorm.DB,
	login string,
	password string,
	now time.Time,
) (PlatformAdminReport, error) {
	existing, found, err := stores.principals.Find(ctx, tx, login)
	if err != nil {
		return PlatformAdminReport{}, fmt.Errorf("look up administrator login: %w", err)
	}
	if !found {
		return PlatformAdminReport{}, fmt.Errorf(
			"%w: no principal owns login %q", ErrPlatformAdminNotBootstrapped, login,
		)
	}
	if err := stores.requireFullAdministrator(ctx, tx, existing); err != nil {
		return PlatformAdminReport{}, err
	}
	replaced, err := stores.credentials.Reset(ctx, tx, existing.ID, password, now)
	if err != nil {
		return PlatformAdminReport{}, fmt.Errorf("reset administrator credential: %w", err)
	}
	if !replaced {
		// The administrator exists but holds no credential, a state the
		// bootstrap command refuses to touch. Writing the first one here is
		// what keeps that state from being unrecoverable.
		if err := stores.credentials.Create(ctx, tx, existing.ID, password, now); err != nil {
			return PlatformAdminReport{}, fmt.Errorf("create missing administrator credential: %w", err)
		}
	}
	event := iamaudit.NewDomainEvent(
		ctx, platformAuditTenant, "platform_administrator_password_reset", "principal", existing.ID,
	)
	event.PrincipalID = existing.ID
	event.Detail["login_name"] = existing.LoginName
	// Whether a credential was there is the difference between a rotation and a
	// repair, and only the audit trail records which one an operator performed.
	event.Detail["credential_existed"] = replaced
	event.Detail["sessions_revoked"] = replaced
	if err := stores.appender(ctx, tx, event); err != nil {
		return PlatformAdminReport{}, fmt.Errorf("append reset audit event: %w", err)
	}
	return PlatformAdminReport{
		Outcome: PlatformAdminPasswordReset, PrincipalID: existing.ID, Login: existing.LoginName,
	}, nil
}

// requireFullAdministrator keeps the command from becoming a general password
// reset for any account somebody can name. It acts only on the principal that
// actually holds full platform access, and only while that principal is one an
// operator is allowed to sign in as.
func (stores platformAdminStores) requireFullAdministrator(
	ctx context.Context,
	tx *gorm.DB,
	existing identity.Principal,
) error {
	administrators, err := stores.administs.List(ctx, tx)
	if err != nil {
		return fmt.Errorf("read platform administrators: %w", err)
	}
	if !holdsFullPlatformAccess(administrators, existing.ID) {
		return fmt.Errorf(
			"%w: login %q belongs to principal %d, which is not a full platform administrator",
			ErrPlatformAdminConflict, existing.LoginName, existing.ID,
		)
	}
	if existing.Status != platformAdminActiveStatus {
		return fmt.Errorf(
			"%w: platform administrator %d is %s; restore the principal before resetting its password",
			ErrPlatformAdminConflict, existing.ID, existing.Status,
		)
	}
	return nil
}

// holdsFullPlatformAccess reports whether principalID is a full administrator.
// A restricted grant is not enough: resetting the password of an account that
// cannot administer the platform would not restore administration.
func holdsFullPlatformAccess(administrators []core.PlatformAdministrator, principalID guid.ID) bool {
	for _, administrator := range administrators {
		if administrator.PrincipalID == principalID && administrator.FullAdministrator {
			return true
		}
	}
	return false
}
