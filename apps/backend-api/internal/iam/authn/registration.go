package authn

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/passwordx"
	"gorm.io/gorm"
)

const registrationVerificationCooldown = time.Minute

func (s *Service) configureRegistration() error {
	if !s.cfg.Registration.Enabled {
		return nil
	}
	if !s.Enabled() || !s.EmailVerificationEnabled() || !s.NotificationEnabled() || s.registrationCreator == nil {
		return errors.New("authn registration requires the web flow, email verification, notification delivery, and an identity creator")
	}
	return nil
}

func (s *Service) RegistrationEnabled() bool {
	return s != nil && s.Enabled() && s.cfg.Registration.Enabled &&
		s.EmailVerificationEnabled() && s.NotificationEnabled() && s.registrationCreator != nil
}

func (s *Service) Capabilities() authnext.Capabilities {
	if s == nil {
		return authnext.Capabilities{}
	}
	return authnext.Capabilities{
		Registration: s.RegistrationEnabled(), PasswordRecovery: s.RecoveryEnabled(),
		Passkey: s.cfg.Enabled && s.web.Enabled && s.cfg.Passkey.Enabled && s.passkeys != nil,
	}
}

func (s *Service) Register(ctx context.Context, email, password, displayName string) error {
	return s.RegisterForEntry(ctx, email, password, displayName, "")
}

func (s *Service) RegisterForEntry(ctx context.Context, email, password, displayName, entry string) error {
	if !s.RegistrationEnabled() {
		return authnext.ErrRegistrationDisabled
	}
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	parsed, err := mail.ParseAddress(email)
	if len(email) > 320 || len(password) < 12 || len(password) > 1024 || len(displayName) > 128 ||
		err != nil || !strings.EqualFold(parsed.Address, email) {
		return authnext.ErrInvalidRegistration
	}
	passwordHash, err := passwordx.Hash(password)
	if err != nil {
		return fmt.Errorf("hash registration password: %w", err)
	}
	secret, err := randomToken(32)
	if err != nil {
		return fmt.Errorf("generate registration verification credential: %w", err)
	}
	token := emailVerificationTokenPrefix + secret
	verificationURL, err := publicEntranceCredentialURL(s.cfg.EmailVerification.VerifyURL, token, entry)
	if err != nil {
		return err
	}
	code, err := emailVerificationCode()
	if err != nil {
		return fmt.Errorf("generate verification code: %w", err)
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.cfg.EmailVerification.TokenTTL)
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		principalID, err := s.registrationCreator(
			ctx, repository.writer(), email, passwordHash, displayName, now, repository.ids,
		)
		if err != nil {
			return err
		}
		verification := model.EmailVerificationToken{
			TokenHMAC: s.emailVerificationHMAC(token), PrincipalID: principalID,
			Email: email, CreatedAt: now, ExpiresAt: expiresAt, Code: code,
		}
		if err := repository.createEmailVerification(ctx, &verification); err != nil {
			return err
		}
		if err := s.enqueueNotification(ctx, repository, principalID, notificationPayload{
			Type: emailVerificationNotification, Recipient: email,
			VerificationURL: verificationURL, Code: code, OccurredAt: now, ExpiresAt: expiresAt,
		}); err != nil {
			return err
		}
		event := securityAudit(principalID, "principal_registration_requested", "success")
		event.Detail["activation_required"] = true
		event.Detail["tenant_membership_created"] = false
		return repository.appendAudit(ctx, event)
	})
	if errors.Is(err, authnext.ErrRegistrationConflict) {
		return s.resendRegistrationCode(ctx, email, password, passwordHash, entry)
	}
	if errors.Is(err, authnext.ErrInvalidRegistration) {
		return err
	}
	if err != nil {
		return fmt.Errorf("register principal: %w", err)
	}
	return nil
}

// resendRegistrationCode answers a registration for an address that already
// has a principal. For one still pending verification the latest registrant's
// password becomes the credential: nobody has proven the address yet, so the
// first registrant's password has no better claim, and keeping it let anyone
// pre-register an address and hold the account its owner later verified.
func (s *Service) resendRegistrationCode(ctx context.Context, email, password, passwordHash, entry string) error {
	secret, err := randomToken(32)
	if err != nil {
		return fmt.Errorf("generate resend credential: %w", err)
	}
	token := emailVerificationTokenPrefix + secret
	code, err := emailVerificationCode()
	if err != nil {
		return fmt.Errorf("generate resend code: %w", err)
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.cfg.EmailVerification.TokenTTL)
	verificationURL, err := publicEntranceCredentialURL(s.cfg.EmailVerification.VerifyURL, token, entry)
	if err != nil {
		return err
	}
	return s.repo.runInTx(ctx, func(repository *Repository) error {
		principal, err := repository.lockPrincipalByLogin(ctx, email)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return authnext.ErrRegistrationConflict
		}
		if err != nil {
			return fmt.Errorf("load pending principal for resend: %w", err)
		}
		if !principal.ActivationRequired {
			return authnext.ErrRegistrationConflict
		}
		credential, err := repository.getCredential(ctx, principal.ID)
		if err != nil {
			return fmt.Errorf("load pending registration credential: %w", err)
		}
		if same, verifyErr := passwordx.Verify(credential.PasswordHash, password); verifyErr != nil || !same {
			// The credential row was just read under the principal lock.
			if _, err := repository.resetPassword(ctx, principal.ID, passwordHash, now); err != nil {
				return fmt.Errorf("replace pending registration credential: %w", err)
			}
			// Every outstanding link would now activate the account with this
			// credential, including links sent for another registrant's password.
			// The replacement below consumes them all, and the registrant whose
			// password is now in force gets a fresh link without waiting out the
			// resend cooldown, which exists for repeats of the same credential.
		} else {
			latestCreatedAt, err := repository.latestEmailVerificationTime(ctx, principal.ID)
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("load latest registration verification: %w", err)
			}
			if latestCreatedAt != nil && now.Sub(latestCreatedAt.UTC()) < registrationVerificationCooldown {
				return nil
			}
		}
		row := model.EmailVerificationToken{
			TokenHMAC: s.emailVerificationHMAC(token), PrincipalID: principal.ID,
			Email: email, CreatedAt: now, ExpiresAt: expiresAt, Code: code,
		}
		if err := repository.replaceEmailVerification(ctx, &row, now); err != nil {
			return err
		}
		return s.enqueueNotification(ctx, repository, principal.ID, notificationPayload{
			Type: emailVerificationNotification, Recipient: email,
			VerificationURL: verificationURL, Code: code, OccurredAt: now, ExpiresAt: expiresAt,
		})
	})
}
