package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// secretRewrapLockName serializes rewraps across processes. Two of them would
// not corrupt anything -- each write matches on the ciphertext it read, so the
// loser of a race skips rather than clobbers -- but they would produce two
// reports that each understate what the other converted, and an operator
// deciding whether a key may come out deserves one answer.
const secretRewrapLockName = "iam/rewrap/secrets"

// ErrSecretRewrapUnavailable marks a missing capability: IAM disabled, no
// writable database, or no module that seals anything.
var ErrSecretRewrapUnavailable = errors.New("secret rewrap is unavailable")

// RewrapSecretsRequest carries the command's operation-only settings.
type RewrapSecretsRequest struct {
	// DryRun surveys without writing. It exists because the question an
	// operator has before a rotation ("what still depends on the old key")
	// and the action that answers it ("reseal everything") are different
	// decisions, and running the second to learn the first is not a choice
	// anyone should have to make on a live database.
	DryRun bool `mapstructure:"dry_run" description:"report what a retired key still holds without writing anything"`
}

// RewrapSecretsConfig combines the normal application configuration with the
// command's own settings, the same way TenantRepairConfig does.
type RewrapSecretsConfig struct {
	AppConfig            `mapstructure:",squash"`
	RewrapSecretsRequest `mapstructure:",squash"`
}

// ModuleSecretReport is one module's account of its own dependence on retired
// keys. The module name is carried alongside because the store names are table
// names, and an operator reading a table name should not have to know which
// module owns it.
type ModuleSecretReport struct {
	Module string `json:"module"`
	// The result is embedded so a module report reads as one flat object: the
	// module name and what it found, not a name wrapping a nested container.
	keyringx.RewrapResult
}

// SecretRewrapReport is the whole answer to "may the retired keys come out".
//
// The totals are deliberately three numbers rather than one verdict. A rewrap
// can bring Pending to zero; nothing it does touches Outstanding, and Unreadable
// means a key is already missing. Collapsing them into a single yes or no would
// hide exactly the case that matters -- everything resealed, and a hundred
// unused recovery codes still hanging off a key somebody is about to delete.
type SecretRewrapReport struct {
	// DryRun records how this report was produced, so a survey pasted into a
	// ticket cannot later be read as proof that a rewrap ran.
	DryRun  bool                 `json:"dry_run"`
	Modules []ModuleSecretReport `json:"modules"`
	// Rewrapped is how many sealed values this run resealed.
	Rewrapped int64 `json:"rewrapped"`
	// Pending is how many sealed values still depend on a retired key once this
	// run has ended. A survey reports everything it found; a rewrap that
	// finished reports zero, because whatever it could open it also converted.
	Pending int64 `json:"pending"`
	// Unreadable is how many sealed values no configured key opens. Every one
	// of these is already broken for the running service, so it is a finding
	// about the present rather than a warning about a future deletion.
	Unreadable int64 `json:"unreadable"`
	// Outstanding is how many derived values are still live. No rewrap reduces
	// it; only expiry or reissue does.
	Outstanding int64 `json:"outstanding"`
	// RetiredKeysStillNeeded is the one thing an operator actually asked. It
	// stays true while anything at all -- sealed or derived -- still depends on
	// a key other than the active one.
	RetiredKeysStillNeeded bool `json:"retired_keys_still_needed"`
	// ClearAfter is when the last derived value expires, so a deployment that
	// cannot reissue can at least wait. It is zero when nothing is outstanding,
	// and also when something outstanding never expires, which is why it must
	// be read together with Outstanding rather than on its own.
	ClearAfter time.Time `json:"clear_after,omitzero"`
}

// secretRewrappers groups the narrow capabilities the sealing modules publish.
// Each module keeps deciding what its own ciphertext means; this boundary only
// runs them in one transaction and adds up what they report.
type secretRewrappers struct {
	run    []func(context.Context, *gorm.DB, keyringx.RewrapMode) (keyringx.RewrapResult, error)
	names  []string
	locker *dlock.Module
	db     *gorm.DB
}

// RunSecretRewrap reseals what a retired key still holds, without starting any
// server.
//
// A deployment that adds a replacement key and leaves the old one listed is
// safe but not finished: every stored secret still opens, and the old key can
// never come out, because nothing has told anyone which rows still need it.
// This command is what ends that state -- and, just as importantly, what says
// when it cannot be ended yet.
func (app *App) RunSecretRewrap(
	ctx context.Context,
	config RewrapSecretsConfig,
) (report SecretRewrapReport, resultErr error) {
	if err := configureApplication(&config.AppConfig); err != nil {
		return SecretRewrapReport{}, err
	}
	app.config = config.AppConfig
	defer func() {
		resultErr = errors.Join(resultErr, app.shutdown())
	}()
	admin, err := app.startForPlatformAdministration(ctx)
	if err != nil {
		return SecretRewrapReport{}, err
	}
	rewrappers, err := app.secretRewrappers(admin)
	if err != nil {
		return SecretRewrapReport{}, err
	}
	return rewrappers.rewrap(ctx, config.DryRun)
}

// secretRewrappers resolves whichever sealing modules this deployment turned
// on. A module that is switched off publishes nothing, and that is an absence
// rather than a fault: a deployment with no federation has no federated client
// secret to reseal.
func (app *App) secretRewrappers(admin platformAdminStores) (secretRewrappers, error) {
	if app.modules == nil {
		return secretRewrappers{}, fmt.Errorf("%w: modules are not composed", ErrSecretRewrapUnavailable)
	}
	services := app.modules.Services()
	rewrappers := secretRewrappers{locker: admin.locker, db: admin.db}
	if rewrapper, err := module.Resolve[iamauthn.SecretRewrapper](services); err == nil {
		rewrappers.add("authn", rewrapper.RewrapSecrets)
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return secretRewrappers{}, errors.Join(ErrSecretRewrapUnavailable, err)
	}
	if rewrapper, err := module.Resolve[federation.SecretRewrapper](services); err == nil {
		rewrappers.add("federation", rewrapper.RewrapSecrets)
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return secretRewrappers{}, errors.Join(ErrSecretRewrapUnavailable, err)
	}
	if rewrapper, err := module.Resolve[provisioning.SecretRewrapper](services); err == nil {
		rewrappers.add("provisioning", rewrapper.RewrapSecrets)
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return secretRewrappers{}, errors.Join(ErrSecretRewrapUnavailable, err)
	}
	if len(rewrappers.run) == 0 {
		return secretRewrappers{}, fmt.Errorf("%w: no module seals anything", ErrSecretRewrapUnavailable)
	}
	return rewrappers, nil
}

func (rewrappers *secretRewrappers) add(
	name string,
	run func(context.Context, *gorm.DB, keyringx.RewrapMode) (keyringx.RewrapResult, error),
) {
	rewrappers.names = append(rewrappers.names, name)
	rewrappers.run = append(rewrappers.run, run)
}

// rewrap takes the cross-process lock and then runs every module in one
// transaction, so a failure anywhere leaves every row exactly as it was rather
// than half converted across two modules.
func (rewrappers secretRewrappers) rewrap(ctx context.Context, dryRun bool) (SecretRewrapReport, error) {
	lockCtx, cancelLock := context.WithTimeout(ctx, platformAdminLockWait)
	defer cancelLock()
	lock, err := rewrappers.locker.Lock(lockCtx, secretRewrapLockName)
	if err != nil {
		return SecretRewrapReport{}, fmt.Errorf("acquire %s lock: %w", secretRewrapLockName, err)
	}
	defer func() {
		// Releasing must not inherit a canceled context, or a timed-out run
		// would leave its lease held until the TTL expires.
		_ = lock.Unlock(context.WithoutCancel(ctx))
	}()

	mode := keyringx.RewrapStale
	if dryRun {
		mode = keyringx.SurveyOnly
	}
	report := SecretRewrapReport{DryRun: dryRun}
	err = rewrappers.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		report.Modules = nil
		for index, run := range rewrappers.run {
			result, err := run(ctx, tx, mode)
			if err != nil {
				return fmt.Errorf("rewrap %s secrets: %w", rewrappers.names[index], err)
			}
			report.Modules = append(report.Modules, ModuleSecretReport{
				Module: rewrappers.names[index], RewrapResult: result,
			})
		}
		return nil
	})
	if err != nil {
		return SecretRewrapReport{}, err
	}
	return summarizeSecretRewrap(report), nil
}

// summarizeSecretRewrap adds up what the modules reported. It is separate from
// the transaction so the totals are computed from exactly what will be printed,
// rather than accumulated as the pass runs and then diverging from it.
func summarizeSecretRewrap(report SecretRewrapReport) SecretRewrapReport {
	// noExpiry records that something outstanding will never age out, which is
	// the difference between "wait until Tuesday" and "this will not clear on
	// its own". It has to suppress ClearAfter entirely: a date printed next to
	// a store that has no expiry is worse than no date, because it reads as
	// permission.
	noExpiry := false
	for _, moduleReport := range report.Modules {
		for _, sealed := range moduleReport.Sealed {
			report.Rewrapped += sealed.Rewrapped
			report.Pending += sealed.Pending()
			report.Unreadable += sealed.Unreadable
		}
		for _, derived := range moduleReport.Derived {
			if derived.Outstanding == 0 {
				continue
			}
			report.Outstanding += derived.Outstanding
			if derived.Until.IsZero() {
				noExpiry = true
				continue
			}
			if derived.Until.After(report.ClearAfter) {
				report.ClearAfter = derived.Until
			}
		}
	}
	if noExpiry {
		report.ClearAfter = time.Time{}
	}
	report.RetiredKeysStillNeeded = report.Pending > 0 || report.Outstanding > 0
	return report
}
