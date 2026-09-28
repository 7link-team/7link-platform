package provisioning

import (
	"context"

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

// RewrapSecrets reports on -- and in RewrapStale mode reseals -- every bearer
// token this module holds for an outbound SCIM target.
//
// A deployment that pushes to no target configures no key and therefore has no
// ring. That is not a failure to report on; it is a store with nothing in it.
func (m *Module) RewrapSecrets(
	ctx context.Context,
	tx *gorm.DB,
	mode keyringx.RewrapMode,
) (keyringx.RewrapResult, error) {
	if m == nil || m.service == nil {
		return keyringx.RewrapResult{}, ErrUnavailable
	}
	if m.service.keys == nil {
		return keyringx.RewrapResult{}, nil
	}
	sealed, err := keyringx.Rewrap(ctx, m.service.keys, mode, m.service.targetTokenStore(tx))
	return keyringx.RewrapResult{Sealed: sealed}, err
}

// targetTokenStore describes the one column this module seals. The bearer token
// is bound to its target row, so a rewrap has to carry the target identifier
// through with the ciphertext rather than reseal the bytes on their own.
func (s *Service) targetTokenStore(tx *gorm.DB) keyringx.SealedStore {
	const column = "bearer_token_ciphertext"
	table := s.repo.names.Targets.String()
	return keyringx.SealedStore{
		Name:    table + "." + column,
		Purpose: "scim-target-bearer",
		Values: func(ctx context.Context) ([]keyringx.SealedValue, error) {
			var rows []struct {
				ID         guid.ID `gorm:"column:id"`
				Ciphertext string  `gorm:"column:bearer_token_ciphertext"`
			}
			err := tx.WithContext(ctx).Table(table).
				Select("id, " + column).Where(column + " <> ''").Find(&rows).Error
			if err != nil {
				return nil, err
			}
			values := make([]keyringx.SealedValue, 0, len(rows))
			for _, row := range rows {
				values = append(values, keyringx.SealedValue{
					Ref: row.ID.String(), AAD: targetTokenAAD(row.ID), Ciphertext: row.Ciphertext,
				})
			}
			return values, nil
		},
		Write: func(ctx context.Context, value keyringx.SealedValue, resealed string) (bool, error) {
			id, err := guid.Parse(value.Ref)
			if err != nil {
				return false, err
			}
			// Matching on the ciphertext this pass read is what keeps a rewrap
			// from overwriting a token somebody changed while it was running.
			result := tx.WithContext(ctx).Table(table).
				Where("id = ? AND "+column+" = ?", id, value.Ciphertext).
				UpdateColumn(column, resealed)
			return result.RowsAffected == 1, result.Error
		},
	}
}

var _ SecretRewrapper = (*Module)(nil)
