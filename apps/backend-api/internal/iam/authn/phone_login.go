package authn

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/identity"
	authnext "7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// Signing in with a number. A code goes to a number somebody has already proven,
// and typing it back proves they still hold it -- which is one factor, exactly as
// a password is. Whatever the policy then requires, it requires here too: this
// hands off to the same gate the password path does, so a second way in cannot
// become a way around a second factor.
//
// An unproven number signs nobody in. It is a string somebody typed into their
// own profile, and anybody can type any number.

func (s *Service) PhoneLoginEnabled() bool {
	return s.PhoneVerificationEnabled() && s.cfg.PhoneVerification.LoginEnabled
}

// BeginPhoneLogin queues a sign-in code. It reports the same thing whatever it
// finds: a number nobody proved, a number two accounts proved, and a number that
// signs somebody in all look identical from outside, because the answer is read
// by whoever typed the number and they are not always its owner.
func (s *Service) BeginPhoneLogin(ctx context.Context, phone string) error {
	if !s.PhoneLoginEnabled() {
		return authnext.ErrPhoneLoginDisabled
	}
	normalized, ok := identity.NormalizePhone(phone)
	if !ok {
		return nil
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.cfg.PhoneVerification.CodeTTL)
	err := s.repo.runInTx(ctx, func(repository *Repository) error {
		principal, err := repository.findPhoneLoginPrincipal(ctx, normalized)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// A locked account is not sent a way in. The lock is the same one a run of
		// wrong passwords sets, so neither way in outlasts the other.
		credential, err := repository.getCredential(ctx, principal.ID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && credential.LockedUntil != nil && credential.LockedUntil.After(now) {
			s.audit(ctx, principal.ID, "login_sms_requested", "denied")
			return nil
		}
		code, err := verificationCode()
		if err != nil {
			return fmt.Errorf("generate phone login code: %w", err)
		}
		row := model.PhoneVerificationCode{
			PrincipalID: principal.ID, Purpose: phoneCodeLogin,
			Phone: principal.Phone, Code: code, CreatedAt: now, ExpiresAt: expiresAt,
		}
		if err := repository.replacePhoneCode(ctx, &row); err != nil {
			return err
		}
		if err := s.enqueueNotification(ctx, repository, principal.ID, notificationPayload{
			Type: phoneLoginNotification, Recipient: principal.Phone,
			Code: code, OccurredAt: now, ExpiresAt: expiresAt,
		}); err != nil {
			return err
		}
		return repository.appendAudit(ctx, securityAudit(principal.ID, "login_sms_requested", "success"))
	})
	if err != nil {
		return fmt.Errorf("begin phone login: %w", err)
	}
	return nil
}

// CompletePhoneLogin spends a sign-in code and hands off to the policy gate. A
// wrong code counts against the same lockout a wrong password does, so guessing
// six digits is bounded by the thing that already bounds guessing a password.
func (s *Service) CompletePhoneLogin(
	ctx context.Context, phone, code, returnURL string,
) (authnext.LoginResult, error) {
	if !s.PhoneLoginEnabled() {
		return authnext.LoginResult{}, authnext.ErrPhoneLoginDisabled
	}
	resolvedReturnURL, err := s.ResolveReturnURL(returnURL)
	if err != nil {
		return authnext.LoginResult{}, err
	}
	normalized, ok := identity.NormalizePhone(phone)
	if !ok || !validVerificationCode(code) {
		return authnext.LoginResult{}, authnext.ErrInvalidCredentials
	}
	now := s.now().UTC()

	principal, err := s.repo.findPhoneLoginPrincipal(ctx, normalized)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authnext.LoginResult{}, authnext.ErrInvalidCredentials
	}
	if err != nil {
		return authnext.LoginResult{}, fmt.Errorf("find phone login principal: %w", err)
	}
	// A principal that only ever signed in through a federated provider has no
	// credential row. It has no password to lock and no second factor recorded,
	// which is exactly what a zero-valued credential says, so the absence is read
	// rather than reported as a fault: a proven number is a way in for somebody
	// who never had a password.
	credential, err := s.repo.getCredential(ctx, principal.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		credential = &model.Credential{PrincipalID: principal.ID}
	} else if err != nil {
		return authnext.LoginResult{}, fmt.Errorf("read credential: %w", err)
	}
	if principal.Status != "active" || principal.ActivationRequired ||
		(credential.LockedUntil != nil && credential.LockedUntil.After(now)) {
		s.audit(ctx, principal.ID, "login_sms", "denied")
		return authnext.LoginResult{}, authnext.ErrInvalidCredentials
	}

	// The code is read and a wrong one counted outside the transaction that
	// issues the session: a refusal leaves that transaction by returning an
	// error, which would roll the count back with it.
	row, err := s.repo.activePhoneCode(ctx, principal.ID, phoneCodeLogin, now)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authnext.LoginResult{}, authnext.ErrInvalidCredentials
	}
	if err != nil {
		return authnext.LoginResult{}, fmt.Errorf("load phone login code: %w", err)
	}
	// No comparison against the principal's number here: the lookup above already
	// required a proven number equal to the one typed, so a number changed since
	// the code left stops resolving at all -- changing one unverifies it, and an
	// unproven number is not a way in.
	if row.Attempts >= maxCodeAttempts || row.Code != code {
		if row.Code != code && row.Attempts < maxCodeAttempts {
			if err := s.repo.recordPhoneCodeAttempt(
				ctx, principal.ID, phoneCodeLogin, maxCodeAttempts, now,
			); err != nil {
				return authnext.LoginResult{}, fmt.Errorf("record phone login attempt: %w", err)
			}
			if err := s.repo.recordLoginFailure(
				ctx, principal.ID, s.cfg.Password.MaxFailedAttempts, s.cfg.Password.LockDuration, now,
			); err != nil {
				return authnext.LoginResult{}, fmt.Errorf("record login failure: %w", err)
			}
		}
		s.audit(ctx, principal.ID, "login_sms", "denied")
		return authnext.LoginResult{}, authnext.ErrInvalidCredentials
	}

	consumed, err := s.repo.consumePhoneCode(ctx, principal.ID, phoneCodeLogin, maxCodeAttempts, now)
	if err != nil {
		return authnext.LoginResult{}, err
	}
	if !consumed {
		return authnext.LoginResult{}, authnext.ErrInvalidCredentials
	}
	if err := s.repo.resetLoginFailures(ctx, principal.ID, now); err != nil {
		return authnext.LoginResult{}, fmt.Errorf("reset login failures: %w", err)
	}
	// One factor is proven. What happens next is not this function's decision.
	return s.completeFirstFactor(ctx, principal.ID, credential, "sms", resolvedReturnURL, now)
}
