package authn

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	authnext "7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

const (
	emailVerificationNotification = "email_verification"
	emailVerificationTokenPrefix  = "cpe1_"
	maxCodeAttempts               = 5
)

func emailVerificationCode() (string, error) {
	var data [4]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(data[:])%1_000_000), nil
}

func (s *Service) configureEmailVerification() error {
	if !s.cfg.EmailVerification.Enabled {
		return nil
	}
	if !s.web.Enabled || s.cfg.EmailVerification.TokenTTL < 5*time.Minute ||
		s.cfg.EmailVerification.TokenTTL > 7*24*time.Hour {
		return errors.New("authn email verification configuration exceeds security limits")
	}
	return validateAuthnURL("email_verification.verify_url", s.cfg.EmailVerification.VerifyURL)
}

func (s *Service) EmailVerificationEnabled() bool {
	return s != nil && s.cfg.Enabled && s.web.Enabled && s.cfg.EmailVerification.Enabled &&
		s.repo != nil && s.repo.writer() != nil
}

func (s *Service) BeginEmailVerification(
	ctx context.Context,
	authorization, cookieHeader string,
) error {
	if !s.EmailVerificationEnabled() {
		return authnext.ErrEmailVerificationDisabled
	}
	claims, err := s.Authenticate(ctx, authorization, cookieHeader)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.cfg.EmailVerification.TokenTTL)
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		principal, err := repository.lockPrincipal(ctx, claims.PrincipalID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return authnext.ErrInvalidSession
		}
		if err != nil {
			return err
		}
		if principal.EmailVerified {
			return nil
		}
		if principal.Email == "" {
			return authnext.ErrEmailRequired
		}
		secret, err := randomToken(32)
		if err != nil {
			return fmt.Errorf("generate email verification credential: %w", err)
		}
		token := emailVerificationTokenPrefix + secret
		code, err := emailVerificationCode()
		if err != nil {
			return fmt.Errorf("generate email verification code: %w", err)
		}
		verificationURL, err := credentialURL(s.cfg.EmailVerification.VerifyURL, token)
		if err != nil {
			return err
		}
		row := model.EmailVerificationToken{
			TokenHMAC: s.emailVerificationHMAC(token), PrincipalID: principal.ID,
			Email: principal.Email, CreatedAt: now, ExpiresAt: expiresAt, Code: code,
		}
		if err := repository.replaceEmailVerification(ctx, &row, now); err != nil {
			return err
		}
		if err := s.enqueueNotification(ctx, repository, notificationPayload{
			Type: emailVerificationNotification, Recipient: principal.Email,
			VerificationURL: verificationURL, Code: code, OccurredAt: now, ExpiresAt: expiresAt,
		}); err != nil {
			return err
		}
		event := securityAudit(principal.ID, "email_verification_requested", "success")
		event.Detail["email_snapshot_bound"] = true
		return repository.appendAudit(ctx, event)
	})
	if errors.Is(err, authnext.ErrInvalidSession) || errors.Is(err, authnext.ErrEmailRequired) {
		return err
	}
	if err != nil {
		return fmt.Errorf("begin email verification: %w", err)
	}
	return nil
}

// CompleteEmailVerification accepts either the link token or its six-digit
// companion code with its email address and consumes the persisted credential exactly once.
func (s *Service) CompleteEmailVerification(ctx context.Context, token, email, code string) error {
	if !s.EmailVerificationEnabled() {
		return authnext.ErrEmailVerificationDisabled
	}
	now := s.now().UTC()
	var verification *model.EmailVerificationToken
	var computedHMAC string
	attemptLimit := 0
	switch {
	case token != "":
		if !strings.HasPrefix(token, emailVerificationTokenPrefix) || len(token) > 128 {
			return authnext.ErrInvalidEmailVerification
		}
		computedHMAC = s.emailVerificationHMAC(token)
		var err error
		verification, err = s.repo.activeEmailVerificationByHMAC(ctx, computedHMAC, now)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return authnext.ErrInvalidEmailVerification
		}
		if err != nil {
			return fmt.Errorf("load email verification credential: %w", err)
		}
		if subtle.ConstantTimeCompare([]byte(verification.TokenHMAC), []byte(computedHMAC)) != 1 {
			return authnext.ErrInvalidEmailVerification
		}
	case code != "":
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" || len(email) > 320 || len(code) != 6 ||
			strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) != -1 {
			return authnext.ErrInvalidEmailVerification
		}
		var err error
		verification, err = s.repo.activeEmailVerificationByEmailAndCode(
			ctx, email, code, maxCodeAttempts, now,
		)
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, errAmbiguousEmailVerificationCode) {
			if recordErr := s.repo.recordEmailVerificationAttempt(ctx, email, maxCodeAttempts, now); recordErr != nil {
				return fmt.Errorf("record email verification attempt: %w", recordErr)
			}
			return authnext.ErrInvalidEmailVerification
		}
		if err != nil {
			return fmt.Errorf("load email verification credential: %w", err)
		}
		attemptLimit = maxCodeAttempts
	default:
		return authnext.ErrInvalidEmailVerification
	}
	err := s.repo.runInTx(ctx, func(repository *Repository) error {
		consumed, err := repository.consumeEmailVerification(
			ctx, verification.TokenHMAC, attemptLimit, now,
		)
		if err != nil {
			return err
		}
		if !consumed {
			return authnext.ErrInvalidEmailVerification
		}
		changed, err := repository.verifyPrincipalEmail(
			ctx, verification.PrincipalID, verification.Email, now,
		)
		if err != nil {
			return err
		}
		if !changed {
			return authnext.ErrInvalidEmailVerification
		}
		if s.verifiedHook != nil {
			if err := s.verifiedHook(ctx, repository.writer(), verification.PrincipalID, verification.Email, now); err != nil {
				return fmt.Errorf("complete verified registration: %w", err)
			}
		}
		event := securityAudit(verification.PrincipalID, "email_verification_completed", "success")
		event.Detail["email_snapshot_matched"] = true
		return repository.appendAudit(ctx, event)
	})
	if errors.Is(err, authnext.ErrInvalidEmailVerification) {
		return err
	}
	if err != nil {
		return fmt.Errorf("complete email verification: %w", err)
	}
	return nil
}

func (s *Service) emailVerificationHMAC(token string) string {
	mac := hmac.New(sha256.New, s.mfaPurposeKey("email-verification:v1"))
	_, _ = mac.Write([]byte("platform:email-verification:v1\x00" + token))
	return hex.EncodeToString(mac.Sum(nil))
}
