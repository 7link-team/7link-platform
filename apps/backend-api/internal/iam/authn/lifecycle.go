package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
)

var (
	ErrLegacyNotifications  = errors.New("unowned legacy notifications require retention remediation")
	ErrNotificationInFlight = errors.New("principal notification delivery is in progress")
)

// revokePrincipalCredentialsOn ends existing authentication authority during
// approved closure processing. The caller owns the transaction and principal
// lock; retained factors may be used only for a new login after an explicit restore.
func (s *Service) revokePrincipalCredentialsOn(ctx context.Context, tx *gorm.DB, principalID guid.ID) error {
	if tx == nil || principalID <= 0 {
		return errRepositoryUnavailable
	}
	repo := s.repo.withDB(tx, true)
	tables, _, err := repo.tables()
	if err != nil {
		return err
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	credential, err := repo.getCredential(ctx, principalID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Federated-only principals previously verify against version zero.
		err = repo.createCredential(ctx, &model.Credential{PrincipalID: principalID, CredentialVersion: 1, PasswordChangedAt: now, UpdatedAt: now})
	} else if err == nil {
		_, err = tables.Credentials.WithContext(ctx).Where(tables.Credentials.PrincipalID.Eq(principalID)).
			UpdateColumnSimple(tables.Credentials.CredentialVersion.Value(credential.CredentialVersion+1), tables.Credentials.UpdatedAt.Value(now))
	}
	if err != nil {
		return err
	}
	if _, err := repo.revokeSessions(ctx, principalID, "", now); err != nil {
		return err
	}
	p := tables.PersonalTokens
	if _, err := p.WithContext(ctx).Where(p.PrincipalID.Eq(principalID), p.RevokedAt.IsNull()).Update(p.RevokedAt, now); err != nil {
		return err
	}
	r := tables.ReauthenticationChallenges
	if _, err := r.WithContext(ctx).Where(r.PrincipalID.Eq(principalID), r.ConsumedAt.IsNull()).Update(r.ConsumedAt, now); err != nil {
		return err
	}
	m := tables.MFAChallenges
	if _, err := m.WithContext(ctx).Where(m.PrincipalID.Eq(principalID), m.ConsumedAt.IsNull()).Update(m.ConsumedAt, now); err != nil {
		return err
	}
	k := tables.PasskeyChallenges
	if _, err := k.WithContext(ctx).Where(k.PrincipalID.Eq(principalID), k.ConsumedAt.IsNull()).Update(k.ConsumedAt, now); err != nil {
		return err
	}
	u := tables.StepUpChallenges
	_, err = u.WithContext(ctx).Where(u.PrincipalID.Eq(principalID), u.ConsumedAt.IsNull()).Update(u.ConsumedAt, now)
	return err
}

// ErasePrincipalData removes authentication secrets and operational PII using
// the caller's transaction, which must already lock the suspended principal.
// It is independent of module activation so disabled modules leave no data behind.
func ErasePrincipalData(ctx context.Context, tx *gorm.DB, prefix string, id guid.ID, _ time.Time) error {
	if tx == nil || id <= 0 {
		return errRepositoryUnavailable
	}
	if err := goosex.ValidatePrefix(prefix); err != nil {
		return err
	}
	var count int64
	if err := tx.WithContext(ctx).Table(prefix + "iam_notification_outbox").Where("principal_id IS NULL").Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrLegacyNotifications
	}
	if err := tx.WithContext(ctx).Table(prefix+"iam_notification_outbox").
		Where("principal_id = ? AND status = 'delivering'", id).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrNotificationInFlight
	}
	for _, table := range []string{
		"iam_reauth_challenges", "iam_personal_tokens",
		"iam_notification_outbox", "iam_email_verification_tokens", "iam_password_recovery_tokens",
		"iam_stepup_challenges", "iam_passkey_challenges", "iam_passkeys", "iam_passkey_users",
		"iam_mfa_challenges", "iam_recovery_codes", "iam_mfa_enrollments", "iam_password_history",
		"iam_sessions", "iam_credentials",
	} {
		if err := tx.WithContext(ctx).Table(prefix+table).Where("principal_id = ?", id).Delete(map[string]any{}).Error; err != nil {
			return fmt.Errorf("erase authentication table %s: %w", table, err)
		}
	}
	return nil
}
