package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/authn/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
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
	if err := repo.revokePrincipalAccountViews(ctx, principalID, now); err != nil {
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
	if _, err := u.WithContext(ctx).Where(u.PrincipalID.Eq(principalID), u.ConsumedAt.IsNull()).Update(u.ConsumedAt, now); err != nil {
		return err
	}
	if err := repo.consumePasswordRecoveries(ctx, principalID, now); err != nil {
		return err
	}
	return repo.consumeEmailVerifications(ctx, principalID, now)
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
	names := query.BindNames(prefix)
	outbox := names.NotificationOutbox.String()
	var count int64
	if err := tx.WithContext(ctx).Table(outbox).Where("principal_id IS NULL").Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrLegacyNotifications
	}
	if err := tx.WithContext(ctx).Table(outbox).
		Where("principal_id = ? AND status = 'delivering'", id).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrNotificationInFlight
	}
	if err := tx.WithContext(ctx).Table(names.AccountViewTokens.String()).
		Where("actor_id = ? OR principal_id = ?", id, id).Delete(map[string]any{}).Error; err != nil {
		return fmt.Errorf("erase authentication account views: %w", err)
	}
	for _, table := range []gormx.TableName{
		names.ReauthenticationChallenges, names.PersonalTokens,
		names.NotificationOutbox, names.EmailVerificationTokens, names.PasswordRecoveryTokens,
		names.StepUpChallenges, names.PasskeyChallenges, names.Passkeys, names.PasskeyUsers,
		names.MFAChallenges, names.RecoveryCodes, names.MFAEnrollments, names.PasswordHistory,
		names.Sessions, names.Credentials,
	} {
		if err := tx.WithContext(ctx).Table(table.String()).Where("principal_id = ?", id).Delete(map[string]any{}).Error; err != nil {
			return fmt.Errorf("erase authentication table %s: %w", table.Logical(), err)
		}
	}
	return nil
}
