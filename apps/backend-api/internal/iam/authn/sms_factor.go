package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// A proven phone number as a second factor. There is no enrollment to record: the
// number already on the principal, already proven, is the enrollment -- the same
// fact the profile screen shows and the same one the sign-in path reads.
//
// It is the weakest second factor on offer here, because a number can be moved
// to another SIM by somebody who talks a carrier into it, and a passkey or a
// TOTP secret cannot. It is available because the alternative for most people is
// no second factor at all, and it is never the only one offered: an account that
// can enroll TOTP is told to.
//
// One rule makes it a second factor rather than the first one again: a session
// that was opened with a code sent to a number cannot be completed with another
// code sent to the same number. The challenge records what proved the first
// factor so that rule can be enforced where the code is asked for.

func (s *Service) SMSFactorEnabled() bool {
	return s.PhoneVerificationEnabled() && s.cfg.PhoneVerification.SecondFactorEnabled
}

// smsFactorAvailable reports whether this principal could answer a challenge
// with a code. The number has to be proven, and the first factor must not have
// been that same number.
func (s *Service) smsFactorAvailable(principal *identity.Principal, firstFactor string) bool {
	return s.SMSFactorEnabled() && firstFactor != "sms" &&
		principal != nil && principal.Phone != "" && principal.PhoneVerified
}

// SendMFAPhoneCode queues a code for an outstanding login challenge. The
// challenge says who it is for and what proved their first factor, so nothing in
// the request names a principal or a number.
func (s *Service) SendMFAPhoneCode(ctx context.Context, challengeID string) error {
	if !s.SMSFactorEnabled() {
		return authnext.ErrSMSFactorUnavailable
	}
	if challengeID == "" {
		return ErrMFAChallenge
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.cfg.PhoneVerification.CodeTTL)
	err := s.repo.runInTx(ctx, func(repository *Repository) error {
		challenge, err := repository.getMFAChallenge(ctx, tokenHash(challengeID))
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err != nil || challenge.ConsumedAt != nil || !challenge.ExpiresAt.After(now) ||
			challenge.Attempts >= s.cfg.MFA.MaxAttempts {
			return ErrMFAChallenge
		}
		// A code for the number that already opened this login would be the same
		// factor twice, which is not a second factor.
		if challenge.FirstFactor == "sms" {
			return authnext.ErrSMSFactorUnavailable
		}
		principal, err := repository.getActivePrincipal(ctx, challenge.PrincipalID)
		if err != nil {
			return err
		}
		if principal.Phone == "" || !principal.PhoneVerified {
			return authnext.ErrSMSFactorUnavailable
		}
		code, err := verificationCode()
		if err != nil {
			return fmt.Errorf("generate second factor code: %w", err)
		}
		row := model.PhoneVerificationCode{
			PrincipalID: principal.ID, Purpose: phoneCodeMFA, Phone: principal.Phone,
			Code: code, CreatedAt: now, ExpiresAt: expiresAt,
		}
		if err := repository.replacePhoneCode(ctx, &row); err != nil {
			return err
		}
		if err := s.enqueueNotification(ctx, repository, principal.ID, notificationPayload{
			Type: phoneMFANotification, Recipient: principal.Phone,
			Code: code, OccurredAt: now, ExpiresAt: expiresAt,
		}); err != nil {
			return err
		}
		return repository.appendAudit(ctx, securityAudit(principal.ID, "mfa_sms_requested", "success"))
	})
	if errors.Is(err, ErrMFAChallenge) || errors.Is(err, authnext.ErrSMSFactorUnavailable) {
		return err
	}
	if err != nil {
		return fmt.Errorf("send second factor code: %w", err)
	}
	return nil
}

// consumeSMSFactor spends an outstanding second-factor code. It is tried before
// a TOTP step because a six-digit code is the same shape either way: a value
// that matches a stored code is unambiguous, where a TOTP comparison would
// accept a code this account was never sent.
func (s *Service) consumeSMSFactor(
	ctx context.Context,
	repository *Repository,
	principalID guid.ID,
	code string,
	now time.Time,
) (bool, error) {
	if !s.SMSFactorEnabled() || !validVerificationCode(code) {
		return false, nil
	}
	row, err := repository.activePhoneCode(ctx, principalID, phoneCodeMFA, now)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if row.Attempts >= maxCodeAttempts || row.Code != code {
		return false, nil
	}
	// The number is re-read rather than trusted from the row: a code proves the
	// number it was sent to, and that has to still be the number on the account.
	principal, err := repository.getActivePrincipal(ctx, principalID)
	if err != nil {
		return false, err
	}
	if !principal.PhoneVerified || principal.Phone != row.Phone {
		return false, nil
	}
	return repository.consumePhoneCode(ctx, principalID, phoneCodeMFA, maxCodeAttempts, now)
}
