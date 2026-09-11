package federation

import (
	"context"

	"7link.com/platform/internal/iam/federation/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/keyringx"
	"gorm.io/gorm"
)

// SecretRewrapper reseals what this module sealed, under whichever key the ring
// is sealing with now.
//
// It is a named type of this module's own rather than one interface three
// modules share, because the service registry holds one value per type: a
// single shared name could only ever be published once, and the second module
// to try would fail at wiring rather than at review.
type SecretRewrapper interface {
	RewrapSecrets(ctx context.Context, tx *gorm.DB, mode keyringx.RewrapMode) (keyringx.RewrapResult, error)
}

// RewrapSecrets reports on -- and in RewrapStale mode reseals -- the two things
// this module keeps sealed: the client secret of every configured identity
// provider, and the private key behind every SAML signing certificate.
//
// Neither can be reissued from here. A client secret lives at the upstream
// provider and only its administrator can mint another; a SAML key is already
// published in metadata that relying parties have loaded. Losing either to a
// rotation is an outage somebody else has to end, which is the whole reason
// this command exists.
func (m *Module) RewrapSecrets(
	ctx context.Context,
	tx *gorm.DB,
	mode keyringx.RewrapMode,
) (keyringx.RewrapResult, error) {
	if m == nil || m.service == nil || m.service.keys == nil {
		return keyringx.RewrapResult{}, ErrUnavailable
	}
	sealed, err := keyringx.Rewrap(ctx, m.service.keys, mode,
		m.service.providerSecretStore(tx), m.service.samlKeyStore(tx))
	return keyringx.RewrapResult{Sealed: sealed}, err
}

// providerSecretStore describes the client secret column. The secret is bound
// to the provider row it belongs to, so a rewrap carries the provider
// identifier through with the ciphertext rather than resealing bytes alone.
func (s *Service) providerSecretStore(tx *gorm.DB) keyringx.SealedStore {
	const column = "client_secret_ciphertext"
	table := query.BindNames(s.repo.prefix()).Providers.String()
	return keyringx.SealedStore{
		Name:    table + "." + column,
		Purpose: "client-secret",
		Values: func(ctx context.Context) ([]keyringx.SealedValue, error) {
			var rows []struct {
				ID         guid.ID `gorm:"column:id"`
				Ciphertext string  `gorm:"column:client_secret_ciphertext"`
			}
			err := tx.WithContext(ctx).Table(table).
				Select("id, " + column).Where(column + " <> ''").Find(&rows).Error
			if err != nil {
				return nil, err
			}
			values := make([]keyringx.SealedValue, 0, len(rows))
			for _, row := range rows {
				values = append(values, keyringx.SealedValue{
					Ref: row.ID.String(), AAD: providerSecretAAD(row.ID), Ciphertext: row.Ciphertext,
				})
			}
			return values, nil
		},
		Write: func(ctx context.Context, value keyringx.SealedValue, resealed string) (bool, error) {
			id, err := guid.Parse(value.Ref)
			if err != nil {
				return false, err
			}
			// Matching on the ciphertext this pass read keeps a rewrap from
			// overwriting a secret somebody rotated while it was running.
			result := tx.WithContext(ctx).Table(table).
				Where("id = ? AND "+column+" = ?", id, value.Ciphertext).
				UpdateColumn(column, resealed)
			return result.RowsAffected == 1, result.Error
		},
	}
}

// samlKeyStore describes the SAML signing key column. These are sealed without
// associated data, which is why the store passes none: binding them to their
// row would have changed what every already-stored key authenticates against.
func (s *Service) samlKeyStore(tx *gorm.DB) keyringx.SealedStore {
	const column = "key_ciphertext"
	table := query.BindNames(s.repo.prefix()).SAMLIDPKeys.String()
	return keyringx.SealedStore{
		Name:    table + "." + column,
		Purpose: "saml-key",
		Values: func(ctx context.Context) ([]keyringx.SealedValue, error) {
			var rows []struct {
				KeyID      string `gorm:"column:key_id"`
				Ciphertext string `gorm:"column:key_ciphertext"`
			}
			err := tx.WithContext(ctx).Table(table).
				Select("key_id, " + column).Where(column + " <> ''").Find(&rows).Error
			if err != nil {
				return nil, err
			}
			values := make([]keyringx.SealedValue, 0, len(rows))
			for _, row := range rows {
				values = append(values, keyringx.SealedValue{Ref: row.KeyID, Ciphertext: row.Ciphertext})
			}
			return values, nil
		},
		Write: func(ctx context.Context, value keyringx.SealedValue, resealed string) (bool, error) {
			result := tx.WithContext(ctx).Table(table).
				Where("key_id = ? AND "+column+" = ?", value.Ref, value.Ciphertext).
				UpdateColumn(column, resealed)
			return result.RowsAffected == 1, result.Error
		},
	}
}

var _ SecretRewrapper = (*Module)(nil)
