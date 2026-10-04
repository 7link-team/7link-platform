package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// Proving a phone number. A message carries a code; the person reads it off one
// screen and types it into another. There is no link to follow, which is the one
// structural difference from proving an address, and the reason this keeps its own
// credential row: an email credential is addressed by the link token in its
// primary key and a phone has none.
//
// What is shared with the address flow is shared rather than copied: the code
// generator, the attempt ceiling, the outbox the message leaves through, and the
// shape of the audit record.
const phoneVerificationNotification = "phone_verification"

func (s *Service) PhoneVerificationEnabled() bool {
	return s != nil && s.cfg.Enabled && s.web.Enabled && s.cfg.PhoneVerification.Enabled &&
		s.repo != nil && s.repo.writer() != nil
}

func (s *Service) configurePhoneVerification() error {
	if !s.cfg.PhoneVerification.Enabled {
		return nil
	}
	// A code lives for minutes, not days: it is read off a message that is
	// already in somebody's hand, and a long-lived six digits is worth guessing.
	if !s.web.Enabled || s.cfg.PhoneVerification.CodeTTL < time.Minute ||
		s.cfg.PhoneVerification.CodeTTL > time.Hour {
		return errors.New("authn phone verification configuration exceeds security limits")
	}
	if !s.NotificationEnabled() {
		return errors.New("authn phone verification requires notification delivery")
	}
	return nil
}

// BeginPhoneVerification queues a code for the number the authenticated
// principal currently has. Asking again replaces the outstanding code rather
// than adding a second one, so the last message somebody received is the one
// that works.
func (s *Service) BeginPhoneVerification(ctx context.Context, authorization, cookieHeader string) error {
	if !s.PhoneVerificationEnabled() {
		return authnext.ErrPhoneVerificationDisabled
	}
	claims, err := s.authenticateAccountHolder(ctx, authorization, cookieHeader)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.cfg.PhoneVerification.CodeTTL)
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		principal, err := repository.lockPrincipal(ctx, claims.PrincipalID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return authnext.ErrInvalidSession
		}
		if err != nil {
			return err
		}
		if principal.PhoneVerified {
			return nil
		}
		if principal.Phone == "" {
			return authnext.ErrPhoneRequired
		}
		code, err := verificationCode()
		if err != nil {
			return fmt.Errorf("generate phone verification code: %w", err)
		}
		row := model.PhoneVerificationCode{
			PrincipalID: principal.ID, Phone: principal.Phone, Code: code,
			CreatedAt: now, ExpiresAt: expiresAt,
		}
		if err := repository.replacePhoneVerification(ctx, &row); err != nil {
			return err
		}
		if err := s.enqueueNotification(ctx, repository, principal.ID, notificationPayload{
			Type: phoneVerificationNotification, Recipient: principal.Phone,
			Code: code, OccurredAt: now, ExpiresAt: expiresAt,
		}); err != nil {
			return err
		}
		event := securityAudit(principal.ID, "phone_verification_requested", "success")
		// That the code was bound to the number on the principal at this instant,
		// never the number: the chain is read more widely than the profile is.
		event.Detail["phone_snapshot_bound"] = true
		return repository.appendAudit(ctx, event)
	})
	if errors.Is(err, authnext.ErrInvalidSession) || errors.Is(err, authnext.ErrPhoneRequired) {
		return err
	}
	if err != nil {
		return fmt.Errorf("begin phone verification: %w", err)
	}
	return nil
}

// CompletePhoneVerification consumes the code for the authenticated principal.
// The session says whose number this is, so the request carries the code alone --
// nothing has to put a phone number in a URL or a log.
//
// The code proves the number it was sent to. If the number changed after the
// message left, the code proves nothing about the number on the principal now,
// and is refused rather than quietly promoted.
func (s *Service) CompletePhoneVerification(ctx context.Context, authorization, cookieHeader, code string) error {
	if !s.PhoneVerificationEnabled() {
		return authnext.ErrPhoneVerificationDisabled
	}
	claims, err := s.authenticateAccountHolder(ctx, authorization, cookieHeader)
	if err != nil {
		return err
	}
	if !validVerificationCode(code) {
		return authnext.ErrInvalidPhoneVerification
	}
	now := s.now().UTC()
	// The credential is read and the wrong guess counted outside the transaction
	// that promotes the number. A refusal leaves that transaction by returning an
	// error, which would roll the counter back with it -- and an attempt ceiling
	// that rolls back is no ceiling at all.
	row, err := s.repo.activePhoneVerification(ctx, claims.PrincipalID, now)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authnext.ErrInvalidPhoneVerification
	}
	if err != nil {
		return fmt.Errorf("load phone verification code: %w", err)
	}
	if row.Attempts >= maxCodeAttempts {
		return authnext.ErrInvalidPhoneVerification
	}
	if row.Code != code {
		if err := s.repo.recordPhoneVerificationAttempt(ctx, claims.PrincipalID, maxCodeAttempts, now); err != nil {
			return fmt.Errorf("record phone verification attempt: %w", err)
		}
		return authnext.ErrInvalidPhoneVerification
	}
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		principal, err := repository.lockPrincipal(ctx, claims.PrincipalID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return authnext.ErrInvalidSession
		}
		if err != nil {
			return err
		}
		// Under the lock, the number is still the one the code was sent to. A
		// number changed in between is not a wrong guess, so it is not counted --
		// it simply proves nothing about the number held now.
		if row.Phone != principal.Phone {
			return authnext.ErrInvalidPhoneVerification
		}
		consumed, err := repository.consumePhoneVerification(ctx, principal.ID, maxCodeAttempts, now)
		if err != nil {
			return err
		}
		if !consumed {
			return authnext.ErrInvalidPhoneVerification
		}
		// The column belongs to identity, so this goes through the connector
		// identity publishes, with the number in the predicate: the same write
		// that records the proof checks nothing moved underneath it.
		changed, err := repository.verifyPrincipalPhone(ctx, principal.ID, row.Phone, now)
		if err != nil {
			return err
		}
		if !changed {
			return authnext.ErrInvalidPhoneVerification
		}
		return repository.appendAudit(ctx, securityAudit(principal.ID, "phone_verified", "success"))
	})
	if errors.Is(err, authnext.ErrInvalidSession) || errors.Is(err, authnext.ErrInvalidPhoneVerification) {
		return err
	}
	if err != nil {
		return fmt.Errorf("complete phone verification: %w", err)
	}
	return nil
}

// validVerificationCode reports whether a value could be a code at all. It is
// checked before any read so that a request carrying something else costs a
// comparison rather than a statement.
func validVerificationCode(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (r *Repository) replacePhoneVerification(ctx context.Context, row *model.PhoneVerificationCode) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	// One row per principal, so replacing is a delete and an insert rather than
	// an upsert whose affected-row count differs by engine.
	if _, err := tables.PhoneVerificationCodes.WithContext(ctx).
		Where(tables.PhoneVerificationCodes.PrincipalID.Eq(row.PrincipalID)).Delete(); err != nil {
		return fmt.Errorf("replace phone verification code: %w", err)
	}
	return tables.PhoneVerificationCodes.WithContext(ctx).Create(row)
}

func (r *Repository) activePhoneVerification(
	ctx context.Context, principalID guid.ID, now time.Time,
) (*model.PhoneVerificationCode, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	row, err := tables.PhoneVerificationCodes.WithContext(ctx).Where(
		tables.PhoneVerificationCodes.PrincipalID.Eq(principalID),
		tables.PhoneVerificationCodes.ConsumedAt.IsNull(),
		tables.PhoneVerificationCodes.ExpiresAt.Gt(now),
	).Take()
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (r *Repository) recordPhoneVerificationAttempt(
	ctx context.Context, principalID guid.ID, maxAttempts int, now time.Time,
) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Table(tables.PhoneVerificationCodes.TableName()).Where(
		"principal_id = ? AND consumed_at IS NULL AND expires_at > ? AND attempts < ?",
		principalID, now, maxAttempts,
	).UpdateColumn("attempts", gorm.Expr("attempts + 1")).Error
}

// consumePhoneVerification spends the code. The attempt ceiling and the unspent
// state are part of the predicate, so two requests racing with the same code
// cannot both succeed.
func (r *Repository) consumePhoneVerification(
	ctx context.Context, principalID guid.ID, maxAttempts int, now time.Time,
) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	result := db.WithContext(ctx).Table(tables.PhoneVerificationCodes.TableName()).Where(
		"principal_id = ? AND consumed_at IS NULL AND expires_at > ? AND attempts < ?",
		principalID, now, maxAttempts,
	).Update("consumed_at", now)
	if result.Error != nil {
		return false, fmt.Errorf("consume phone verification code: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

func (r *Repository) consumePhoneVerifications(ctx context.Context, principalID guid.ID, now time.Time) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	_, err = tables.PhoneVerificationCodes.WithContext(ctx).Where(
		tables.PhoneVerificationCodes.PrincipalID.Eq(principalID),
		tables.PhoneVerificationCodes.ConsumedAt.IsNull(),
	).UpdateSimple(tables.PhoneVerificationCodes.ConsumedAt.Value(now))
	return err
}
