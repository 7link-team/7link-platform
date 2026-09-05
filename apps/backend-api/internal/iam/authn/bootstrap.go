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
