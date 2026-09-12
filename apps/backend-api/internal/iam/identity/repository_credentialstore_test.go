package identity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// authnCredentialFixture stands in for the capability authn provides for the
// credential tables authn owns.
//
// identity's tests cannot use the real one: authn imports identity, so identity
// cannot import authn, in its tests any more than in its source. That is the
// same import direction that made the capability necessary in the first place.
// So this writes the same rows the real closures write, on the handle it is
// given, and what the real closures actually do is asserted on authn's side in
// internal/iam/authn/credentialstore_test.go.
//
// It exists so the behavior tests around closure, provisioning and status
// changes keep asserting the outcomes they always asserted. It is a fixture, not
// evidence about authn.
func authnCredentialFixture(prefix string) PrincipalCredentialStore {
	table := func(name string) string { return prefix + name }
	requireHandle := func(tx *gorm.DB) error {
		if tx == nil {
			return fmt.Errorf("credential fixture: no handle")
		}
		return nil
	}
	return PrincipalCredentialStore{
		CreateHashed: func(
			ctx context.Context, tx *gorm.DB, id guid.ID, hash string, now time.Time,
		) error {
			if err := requireHandle(tx); err != nil {
				return err
			}
			return tx.WithContext(ctx).Table(table("iam_credentials")).Create(map[string]any{
				"principal_id": id, "password_hash": hash,
				"password_changed_at": now, "credential_version": int64(1), "updated_at": now,
			}).Error
		},
		IncrementVersion: func(
			ctx context.Context, tx *gorm.DB, id guid.ID, now time.Time,
		) (bool, error) {
			if err := requireHandle(tx); err != nil {
				return false, err
			}
			result := tx.WithContext(ctx).Table(table("iam_credentials")).
				Where("principal_id = ?", id).
				Updates(map[string]any{
					"credential_version": gorm.Expr("credential_version + 1"), "updated_at": now,
				})
			return result.RowsAffected == 1, result.Error
		},
		RevokeSessions: func(ctx context.Context, tx *gorm.DB, id guid.ID, now time.Time) error {
			if err := requireHandle(tx); err != nil {
				return err
			}
			return tx.WithContext(ctx).Table(table("iam_sessions")).
				Where("principal_id = ? AND revoked_at IS NULL", id).
				Update("revoked_at", now).Error
		},
		ConsumeCredentialTokens: func(ctx context.Context, tx *gorm.DB, id guid.ID, now time.Time) error {
			if err := requireHandle(tx); err != nil {
				return err
			}
			for _, name := range []string{"iam_password_recovery_tokens", "iam_email_verification_tokens"} {
				if err := tx.WithContext(ctx).Table(table(name)).
					Where("principal_id = ? AND consumed_at IS NULL", id).
					Update("consumed_at", now).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// bindCredentialStore supplies the capability to a repository built outside
// module wiring, where nothing has injected it.
func bindCredentialStore(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	repository.credentials = authnCredentialFixture(prefix)
}

// TestCredentialWritesRefuseWithoutTheOwnersCapability is the fail-closed edge
// the capability introduces. A deployment without authn has no credential
// tables to write, and identity must say so rather than write them itself --
// which is what it used to do, and what this whole change removes.
func TestCredentialWritesRefuseWithoutTheOwnersCapability(t *testing.T) {
	harness := newIdentityHarness(t, "sl_")
	repository := harness.repository
	repository.credentials = PrincipalCredentialStore{}
	ctx := t.Context()

	for name, call := range map[string]func() error{
		"create credential": func() error {
			return repository.createCredential(ctx, 1, "hash", identityTestNow)
		},
		"increment version": func() error {
			return repository.incrementCredentialVersion(ctx, 1, identityTestNow)
		},
		"revoke sessions": func() error {
			return repository.revokeSessionsAndRefreshTokens(ctx, 1, identityTestNow)
		},
		"consume tokens": func() error {
			return repository.revokeAllSecurityState(ctx, 1, identityTestNow)
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			require.ErrorIs(t, err, errRepositoryUnavailable)
			assert.Contains(t, err.Error(), "no credential store")
		})
	}

	// Nothing reached storage: refusing is the point, not refusing halfway.
	for _, table := range []string{"iam_credentials", "iam_sessions"} {
		var total int64
		require.NoError(t, harness.db.Table(harness.table(table)).
			Where("principal_id = ?", 1).Count(&total).Error)
		assert.Zero(t, total, table)
	}
}
