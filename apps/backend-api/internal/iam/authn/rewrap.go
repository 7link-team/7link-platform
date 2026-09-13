package authn

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/extension/keyringx"
	"gorm.io/gorm"
)

// SecretRewrapper reseals what this module sealed, under whichever key the ring
// is sealing with now, and reports on what it cannot reseal.
//
// It is a named type of this module's own rather than one interface three
// modules share, because the service registry holds one value per type: a
// single shared name could only ever be published once, and the second module
// to try would fail at wiring rather than at review.
type SecretRewrapper interface {
	RewrapSecrets(ctx context.Context, tx *gorm.DB, mode keyringx.RewrapMode) (keyringx.RewrapResult, error)
}

// RewrapSecrets reports on -- and in RewrapStale mode reseals -- everything the
// authentication key protects, and then reports separately on what it derives
// but cannot reseal.
//
// That second half is the point. A rewrap that covered only the sealed columns
// would return zero pending and read as "the retired key may come out now",
// while unused recovery codes and links already in someone's mailbox still
// depend on it. Those are one-way digests of secrets this deployment does not
// keep, so there is nothing to reseal -- only to wait out, or to reissue.
func (m *Module) RewrapSecrets(
	ctx context.Context,
	tx *gorm.DB,
	mode keyringx.RewrapMode,
) (keyringx.RewrapResult, error) {
	if m == nil || m.Service == nil {
		return keyringx.RewrapResult{}, authnext.ErrUnavailable
	}
	// A deployment with browser authentication switched off holds no ring
	// because it seals nothing: no authenticator, no passkey, no queued
	// notification. That is an empty store rather than a failure to report on.
	if m.Service.mfaKeys == nil {
		return keyringx.RewrapResult{}, nil
	}
	service := m.Service
	sealed, err := keyringx.Rewrap(ctx, service.mfaKeys, mode,
		service.totpSecretStore(tx),
		service.mfaEnrollmentStore(tx),
		service.passkeyStore(tx),
		service.notificationStore(tx),
	)
	if err != nil {
		return keyringx.RewrapResult{}, err
	}
	derived, err := service.derivedStores(ctx, tx, time.Now().UTC())
	if err != nil {
		return keyringx.RewrapResult{}, err
	}
	return keyringx.RewrapResult{Sealed: sealed, Derived: derived}, nil
}

// principalSealedStore describes a column sealed under a principal identifier,
// which is the shape two of this module's four sealed columns share.
func (s *Service) principalSealedStore(
	tx *gorm.DB, tableName gormx.TableName, column, purpose string,
) keyringx.SealedStore {
	table := tableName.String()
	return keyringx.SealedStore{
		Name:    table + "." + column,
		Purpose: purpose,
		Values: func(ctx context.Context) ([]keyringx.SealedValue, error) {
			var rows []struct {
				PrincipalID guid.ID `gorm:"column:principal_id"`
				Ciphertext  string  `gorm:"column:ciphertext"`
			}
			err := tx.WithContext(ctx).Table(table).
				Select("principal_id, " + column + " AS ciphertext").
				Where(column + " <> ''").Find(&rows).Error
			if err != nil {
				return nil, err
			}
			values := make([]keyringx.SealedValue, 0, len(rows))
			for _, row := range rows {
				values = append(values, keyringx.SealedValue{
					Ref: row.PrincipalID.String(),
					// The associated data is the principal identifier as text,
					// spelled exactly as the sealing path spells it. A rewrap
					// that spelled it differently would report every row
					// unreadable and then, worse, offer to fix it.
					AAD:        []byte(row.PrincipalID.String()),
					Ciphertext: row.Ciphertext,
				})
			}
			return values, nil
		},
		Write: func(ctx context.Context, value keyringx.SealedValue, resealed string) (bool, error) {
			principalID, err := guid.Parse(value.Ref)
			if err != nil {
				return false, err
			}
			// Matching on the ciphertext this pass read keeps a rewrap from
			// overwriting a secret the person changed while it was running.
			result := tx.WithContext(ctx).Table(table).
				Where("principal_id = ? AND "+column+" = ?", principalID, value.Ciphertext).
				UpdateColumn(column, resealed)
			return result.RowsAffected == 1, result.Error
		},
	}
}

// totpSecretStore holds the enabled authenticator of every person who turned
// one on. Losing it locks each of them out of a second factor they cannot
// re-enroll without first passing the factor they have just lost.
func (s *Service) totpSecretStore(tx *gorm.DB) keyringx.SealedStore {
	names := s.repo.names
	return s.principalSealedStore(tx, names.Credentials, "totp_secret", "encryption:"+mfaSecretCipher)
}

// mfaEnrollmentStore holds the half-finished enrollment of somebody looking at a
// QR code right now. These expire on their own, so covering them saves one
// person one retry rather than saving an account.
func (s *Service) mfaEnrollmentStore(tx *gorm.DB) keyringx.SealedStore {
	names := s.repo.names
	return s.principalSealedStore(
		tx, names.MFAEnrollments, "secret_ciphertext", "encryption:"+mfaSecretCipher,
	)
}

// passkeyStore is bound to both the principal and the credential identifier, so
// it cannot reuse the principal-only shape above.
func (s *Service) passkeyStore(tx *gorm.DB) keyringx.SealedStore {
	const column = "credential_ciphertext"
	table := s.repo.names.Passkeys.String()
	return keyringx.SealedStore{
		Name:    table + "." + column,
		Purpose: "encryption:" + passkeyCipher,
		Values: func(ctx context.Context) ([]keyringx.SealedValue, error) {
			var rows []struct {
				IDHash      string  `gorm:"column:id_hash"`
				PrincipalID guid.ID `gorm:"column:principal_id"`
				Ciphertext  string  `gorm:"column:credential_ciphertext"`
			}
			err := tx.WithContext(ctx).Table(table).
				Select("id_hash, principal_id, " + column).
				Where(column + " <> ''").Find(&rows).Error
			if err != nil {
				return nil, err
			}
			values := make([]keyringx.SealedValue, 0, len(rows))
			for _, row := range rows {
				values = append(values, keyringx.SealedValue{
					Ref:        row.IDHash,
					AAD:        []byte(passkeyAAD(row.PrincipalID, row.IDHash)),
					Ciphertext: row.Ciphertext,
				})
			}
			return values, nil
		},
		Write: func(ctx context.Context, value keyringx.SealedValue, resealed string) (bool, error) {
			result := tx.WithContext(ctx).Table(table).
				Where("id_hash = ? AND "+column+" = ?", value.Ref, value.Ciphertext).
				UpdateColumn(column, resealed)
			return result.RowsAffected == 1, result.Error
		},
	}
}

// notificationStore is the outbox. Its rows drain within minutes, so a rewrap
// normally finds nothing here; it is covered anyway because a stuck outbox is
// exactly the case where a row would still be sitting under the old key.
func (s *Service) notificationStore(tx *gorm.DB) keyringx.SealedStore {
	const column = "payload_ciphertext"
	table := s.repo.names.NotificationOutbox.String()
	return keyringx.SealedStore{
		Name:    table + "." + column,
		Purpose: "encryption:" + notificationCipher,
		Values: func(ctx context.Context) ([]keyringx.SealedValue, error) {
			var rows []struct {
				ID         guid.ID `gorm:"column:id"`
				Ciphertext string  `gorm:"column:payload_ciphertext"`
			}
			err := tx.WithContext(ctx).Table(table).
				Select("id, " + column).Where(column + " <> ''").Find(&rows).Error
			if err != nil {
				return nil, err
			}
			values := make([]keyringx.SealedValue, 0, len(rows))
			for _, row := range rows {
				values = append(values, keyringx.SealedValue{
					Ref: row.ID.String(), AAD: []byte(row.ID.String()), Ciphertext: row.Ciphertext,
				})
			}
			return values, nil
		},
		Write: func(ctx context.Context, value keyringx.SealedValue, resealed string) (bool, error) {
			id, err := guid.Parse(value.Ref)
			if err != nil {
				return false, err
			}
			result := tx.WithContext(ctx).Table(table).
				Where("id = ? AND "+column+" = ?", id, value.Ciphertext).
				UpdateColumn(column, resealed)
			return result.RowsAffected == 1, result.Error
		},
	}
}

// derivedStores counts what the authentication key derives rather than seals. A
// digest is one-way and records no key identity, so these rows can be neither
// rewrapped nor even attributed to a key. All this can report is how many are
// still live and what ends them, which is still the difference between a date
// and a guess.
func (s *Service) derivedStores(
	ctx context.Context, tx *gorm.DB, now time.Time,
) ([]keyringx.DerivedStoreReport, error) {
	reports := make([]keyringx.DerivedStoreReport, 0, 3)
	names := s.repo.names
	// Recovery codes never expire. Waiting will not empty this store, and the
	// deployment cannot regenerate somebody's codes on their behalf, so a
	// rotation that drops the key silently removes the one route back into an
	// account whose authenticator is already gone.
	codes, _, err := s.countDerived(ctx, tx, names.RecoveryCodes, "used_at IS NULL", "", now)
	if err != nil {
		return nil, err
	}
	reports = append(reports, keyringx.DerivedStoreReport{
		Store:   names.RecoveryCodes.String() + ".code_hash",
		Reissue: "each person holding recovery codes generates a new set",
		// No Until: this store has no expiry, and saying so is the finding.
		Outstanding: codes,
	})
	for _, store := range []struct {
		table   gormx.TableName
		column  string
		reissue string
	}{
		{names.PasswordRecoveryTokens, "token_hmac", "the person requests another recovery link"},
		{names.EmailVerificationTokens, "token_hmac", "the person is sent another verification link"},
	} {
		outstanding, until, err := s.countDerived(
			ctx, tx, store.table, "consumed_at IS NULL AND expires_at > ?", "expires_at", now,
		)
		if err != nil {
			return nil, err
		}
		reports = append(reports, keyringx.DerivedStoreReport{
			Store: store.table.String() + "." + store.column,
			// These do expire, so the honest answer is a date rather than a
			// warning: past it, nothing here needs the retired key.
			Outstanding: outstanding, Until: until, Reissue: store.reissue,
		})
	}
	return reports, nil
}

// countDerived counts the live rows of one derived store and, when the store
// expires at all, finds the moment its last row stops mattering.
func (s *Service) countDerived(
	ctx context.Context, tx *gorm.DB, tableName gormx.TableName, condition, expiryColumn string, now time.Time,
) (int64, time.Time, error) {
	table := tableName.String()
	live := func() *gorm.DB {
		query := tx.WithContext(ctx).Table(table)
		if expiryColumn == "" {
			return query.Where(condition)
		}
		return query.Where(condition, now)
	}
	var outstanding int64
	if err := live().Count(&outstanding).Error; err != nil {
		return 0, time.Time{}, err
	}
	if expiryColumn == "" || outstanding == 0 {
		return outstanding, time.Time{}, nil
	}
	// The latest live row rather than MAX(...): an aggregate carries no column
	// type, so SQLite hands it back as text and the scan fails, while PostgreSQL
	// returns a timestamp and does not. A report that worked on one dialect and
	// broke on the other would be discovered during a rotation.
	var latest []struct {
		ExpiresAt time.Time `gorm:"column:expires_at"`
	}
	err := live().Select(expiryColumn + " AS expires_at").
		Order(expiryColumn + " DESC").Limit(1).Find(&latest).Error
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(latest) == 0 {
		return outstanding, time.Time{}, nil
	}
	return outstanding, latest[0].ExpiresAt.UTC(), nil
}

var _ SecretRewrapper = (*Module)(nil)
