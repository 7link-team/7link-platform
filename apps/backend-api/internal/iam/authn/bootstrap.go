package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/passwordx"
	"gorm.io/gorm"
)

// BootstrapCredentialStore is the narrow authentication capability the
// composition boundary binds to its own bootstrap transaction. Hashing stays
// behind it so the plaintext password never travels past this package.
type BootstrapCredentialStore struct {
	Exists func(context.Context, *gorm.DB, guid.ID) (bool, error)
	Create func(context.Context, *gorm.DB, guid.ID, string, time.Time) error
	Reset  func(context.Context, *gorm.DB, guid.ID, string, time.Time) (bool, error)
}

// HasCredentialOn reports whether the principal already holds a password
// credential. A platform administrator without one could never sign in, so the
// bootstrap command treats that state as a conflict rather than a rerun.
func (r *Repository) HasCredentialOn(ctx context.Context, db *gorm.DB, principalID guid.ID) (bool, error) {
	if r == nil || db == nil || principalID.Zero() {
		return false, errors.New("credential lookup requires a repository, transaction, and principal")
	}
	_, err := r.withDB(db, true).getCredential(ctx, principalID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return false, fmt.Errorf("read principal credential: %w", err)
}

// CreateBootstrapCredentialOn hashes password and writes the principal's first
// credential through the caller's transaction.
func (r *Repository) CreateBootstrapCredentialOn(
	ctx context.Context,
	db *gorm.DB,
	principalID guid.ID,
	password string,
	now time.Time,
) error {
	if r == nil || db == nil || principalID.Zero() {
		return errors.New("credential creation requires a repository, transaction, and principal")
	}
	if err := ValidateBootstrapPassword(password); err != nil {
		return err
	}
	hash, err := passwordx.Hash(password)
	if err != nil {
		return err
	}
	now = now.UTC()
	return r.withDB(db, true).createCredential(ctx, &model.Credential{
		PrincipalID: principalID, PasswordHash: hash, PasswordChangedAt: now,
		CredentialVersion: 1, UpdatedAt: now,
	})
}

// ResetBootstrapCredentialOn replaces a principal's password without being
// given the one it replaces. It is the out-of-band recovery path for the
// platform administrator: the account that can lock everybody else out is the
// one account no in-band flow can rescue, because there is nobody above it to
// approve the rescue.
//
// It reports false when the principal holds no credential at all. That is a
// state the caller has to explain to an operator rather than a failure, since
// resetting a password nobody can sign in with would report success for an
// account that still does not work.
func (r *Repository) ResetBootstrapCredentialOn(
	ctx context.Context,
	db *gorm.DB,
	principalID guid.ID,
	password string,
	now time.Time,
) (bool, error) {
	if r == nil || db == nil || principalID.Zero() {
		return false, errors.New("credential reset requires a repository, transaction, and principal")
	}
	if err := ValidateBootstrapPassword(password); err != nil {
		return false, err
	}
	hash, err := passwordx.Hash(password)
	if err != nil {
		return false, err
	}
	now = now.UTC()
	repository := r.withDB(db, true)
	credential, err := repository.getCredential(ctx, principalID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("read principal credential: %w", err)
	}
	// The superseded hash joins the same history the ordinary change flow
	// keeps, so a reset cannot be used to walk a retired password back into
	// reuse.
	if err := repository.appendPasswordHistory(ctx, principalID, credential.PasswordHash, now); err != nil {
		return false, fmt.Errorf("record superseded password: %w", err)
	}
	if err := repository.trimPasswordHistory(ctx, principalID, passwordHistoryDepth); err != nil {
		return false, fmt.Errorf("trim password history: %w", err)
	}
	reset, err := repository.resetPassword(ctx, principalID, hash, now)
	if err != nil {
		return false, fmt.Errorf("reset principal credential: %w", err)
	}
	if !reset {
		// The row was read in this transaction, so losing it here means
		// somebody deleted the credential concurrently. Reporting absence
		// would tell the operator to bootstrap instead, which is wrong.
		return false, fmt.Errorf("credential for principal %d disappeared during reset", principalID)
	}
	// A reset is only a recovery if it also ends what the old password could
	// still reach: live sessions, and any recovery link already in an inbox.
	if err := repository.revokeOtherAuthentication(ctx, principalID, "", now); err != nil {
		return false, fmt.Errorf("revoke sessions after reset: %w", err)
	}
	if err := repository.consumePasswordRecoveries(ctx, principalID, now); err != nil {
		return false, fmt.Errorf("consume outstanding recovery tokens: %w", err)
	}
	return true, nil
}

// ErrBootstrapPassword reports a password the credential store will not
// accept. The message deliberately states the bounds and nothing about the
// value it was given.
var ErrBootstrapPassword = errors.New("bootstrap password must be 12 to 1024 characters")

// ValidateBootstrapPassword applies the credential store's length bounds. The
// bootstrap command calls it before opening a transaction so an operator
// mistake fails before any database work starts.
func ValidateBootstrapPassword(password string) error {
	if len(password) < 12 || len(password) > 1024 {
		return ErrBootstrapPassword
	}
	return nil
}
