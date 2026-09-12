package authn

import (
	"context"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// principalCredentialStore is the capability identity resolves to have authn
// write the credential tables authn owns. It is provided before injection, so
// every closure builds its repository at call time from collaborators Wire has
// since supplied -- the same shape the bootstrap credential store uses.
//
// Each closure pins the caller's transaction with withDB, which is the whole
// point: identity calls these while holding a principal lock, and a write that
// opened its own connection would survive identity rolling the rest back.
func (m *Module) principalCredentialStore() identity.PrincipalCredentialStore {
	onCaller := func(tx *gorm.DB) (*Repository, error) {
		if m == nil || m.runtime == nil || m.ids == nil || m.appender == nil {
			return nil, authnext.ErrUnavailable
		}
		if tx == nil {
			return nil, authnext.ErrUnavailable
		}
		repository := NewRepository(m.runtime, m.ids, m.appender)
		repository.foreign = &m.foreign
		repository.principals = m.principals
		return repository.withDB(tx, true), nil
	}
	return identity.PrincipalCredentialStore{
		CreateHashed: func(
			ctx context.Context, tx *gorm.DB, principalID guid.ID, hash string, now time.Time,
		) error {
			repository, err := onCaller(tx)
			if err != nil {
				return err
			}
			return repository.createCredential(ctx, &model.Credential{
				PrincipalID: principalID, PasswordHash: hash, PasswordChangedAt: now,
				CredentialVersion: 1, UpdatedAt: now,
			})
		},
		IncrementVersion: func(
			ctx context.Context, tx *gorm.DB, principalID guid.ID, now time.Time,
		) (bool, error) {
			repository, err := onCaller(tx)
			if err != nil {
				return false, err
			}
			moved, err := repository.bumpCredentialVersion(ctx, principalID, now)
			if err != nil {
				return false, err
			}
			return moved == 1, nil
		},
		RevokeSessions: func(ctx context.Context, tx *gorm.DB, principalID guid.ID, now time.Time) error {
			repository, err := onCaller(tx)
			if err != nil {
				return err
			}
			_, err = repository.revokeSessions(ctx, principalID, "", now)
			return err
		},
		ConsumeCredentialTokens: func(
			ctx context.Context, tx *gorm.DB, principalID guid.ID, now time.Time,
		) error {
			repository, err := onCaller(tx)
			if err != nil {
				return err
			}
			if err := repository.consumePasswordRecoveries(ctx, principalID, now); err != nil {
				return err
			}
			return repository.consumeEmailVerifications(ctx, principalID, now)
		},
	}
}
