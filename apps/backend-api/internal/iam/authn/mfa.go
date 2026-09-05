package authn

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/extension/passwordx"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"
)

const totpPeriod = int64(30)

var (
	ErrInvalidMFA                   = authnext.ErrInvalidMFA
	ErrMFAChallenge                 = authnext.ErrMFAChallenge
	ErrMFAEnrollment                = authnext.ErrMFAEnrollment
	ErrMFAAlreadyOn                 = authnext.ErrMFAAlreadyOn
	ErrMFANotEnabled                = authnext.ErrMFANotEnabled
	errUnsupportedAuthnCiphertext   = errors.New("unsupported authentication ciphertext")
	errInvalidAuthnCiphertext       = errors.New("invalid authentication ciphertext")
	errDecryptAuthenticationPayload = errors.New("decrypt authentication data")
	errRetiredAuthnKey              = errors.New(
		"authentication data was sealed with a key this deployment no longer holds",
	)
)

func (s *Service) encryptMFASecret(principalID guid.ID, secret string) (string, error) {
	return s.encryptAuthnData("v1", principalID.String(), []byte(secret))
}

func (s *Service) encryptAuthnData(purpose, associatedData string, plain []byte) (string, error) {
	sealed, err := s.mfaKeys.Seal("encryption:"+purpose, []byte(associatedData), plain)
	if err != nil {
		return "", fmt.Errorf("seal authentication data: %w", err)
	}
	return sealed, nil
}

func (s *Service) decryptMFASecret(principalID guid.ID, encoded string) (string, error) {
	plain, err := s.decryptAuthnData("v1", principalID.String(), encoded)
	return string(plain), err
}

func (s *Service) decryptAuthnData(purpose, associatedData, encoded string) ([]byte, error) {
	plain, err := s.mfaKeys.Open("encryption:"+purpose, []byte(associatedData), encoded)
	switch {
	case err == nil:
		return plain, nil
	case errors.Is(err, keyringx.ErrUnsupportedEnvelope):
		return nil, errUnsupportedAuthnCiphertext
	case errors.Is(err, keyringx.ErrInvalidEnvelope):
		return nil, errInvalidAuthnCiphertext
	case errors.Is(err, keyringx.ErrUnknownKey):
		// Kept apart from a failed decrypt because the remedy differs in kind:
		// the retired key goes back into the configuration and the account
		// works again, rather than a user re-enrolling something they cannot.
		return nil, errRetiredAuthnKey
	default:
		return nil, errDecryptAuthenticationPayload
	}
}

func (s *Service) MFAStatus(
	ctx context.Context,
	authorization, cookieHeader string,
) (authnext.MFAStatus, error) {
	claims, err := s.Authenticate(ctx, authorization, cookieHeader)
	if err != nil {
		return authnext.MFAStatus{}, err
	}
	credential, err := s.repo.getCredential(ctx, claims.PrincipalID)
	if err != nil {
		return authnext.MFAStatus{}, fmt.Errorf("load MFA status: %w", err)
	}
	remaining, err := s.repo.countAvailableRecoveryCodes(ctx, claims.PrincipalID)
	if err != nil {
		return authnext.MFAStatus{}, fmt.Errorf("count recovery codes: %w", err)
	}
	return authnext.MFAStatus{
		TOTPEnabled:            credential.MFARequired && credential.TOTPSecret != "",
		RecoveryCodesRemaining: int(remaining),
	}, nil
}

func (s *Service) BeginTOTPEnrollment(
	ctx context.Context,
	authorization, cookieHeader, currentPassword string,
) (authnext.MFAEnrollment, error) {
	claims, err := s.authenticateMFAEnrollment(ctx, authorization, cookieHeader)
	if err != nil {
		return authnext.MFAEnrollment{}, err
	}
	if err := s.ensureRecoveryCooldownExpired(ctx, claims.PrincipalID); err != nil {
		return authnext.MFAEnrollment{}, err
	}
	principal, credential, err := s.verifyCurrentPassword(ctx, claims.PrincipalID, currentPassword)
	if err != nil {
		return authnext.MFAEnrollment{}, err
	}
	if credential.TOTPSecret != "" {
		return authnext.MFAEnrollment{}, ErrMFAAlreadyOn
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer: s.cfg.MFA.Issuer, AccountName: principal.LoginName,
		Period: uint(totpPeriod), SecretSize: 20, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		return authnext.MFAEnrollment{}, fmt.Errorf("generate TOTP secret: %w", err)
	}
	ciphertext, err := s.encryptMFASecret(claims.PrincipalID, key.Secret())
	if err != nil {
		return authnext.MFAEnrollment{}, err
	}
	now := s.now().UTC()
	row := model.MFAEnrollment{
		PrincipalID: claims.PrincipalID, SecretCiphertext: ciphertext,
		CreatedAt: now, ExpiresAt: now.Add(s.cfg.MFA.EnrollmentTTL),
	}
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		if err := repository.replaceMFAEnrollment(ctx, &row); err != nil {
			return err
		}
		return repository.appendAudit(ctx, securityAudit(claims.PrincipalID, "mfa_enrollment_started", "success"))
	})
	if err != nil {
		return authnext.MFAEnrollment{}, fmt.Errorf("store TOTP enrollment: %w", err)
	}
	return authnext.MFAEnrollment{Secret: key.Secret(), ProvisioningURI: key.URL(), ExpiresAt: row.ExpiresAt}, nil
}

func (s *Service) ConfirmTOTPEnrollment(
	ctx context.Context,
	authorization, cookieHeader, code string,
) (authnext.MFAConfirmation, error) {
	claims, err := s.authenticateMFAEnrollment(ctx, authorization, cookieHeader)
	if err != nil {
		return authnext.MFAConfirmation{}, err
	}
	now := s.now().UTC()
	var recoveryCodes []string
	sessionRevoked := false
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		enrollment, err := repository.activeMFAEnrollmentForUpdate(ctx, claims.PrincipalID, now)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMFAEnrollment
		}
		if err != nil {
			return err
		}
		secret, err := s.decryptMFASecret(claims.PrincipalID, enrollment.SecretCiphertext)
		if err != nil {
			return err
		}
		step, valid := validateTOTP(code, secret, now)
		if !valid {
			return ErrInvalidMFA
		}
		recoveryCodes, err = s.replaceRecoveryCodes(ctx, repository, claims.PrincipalID, now)
		if err != nil {
			return err
		}
		changed, err := repository.enableMFA(ctx, claims.PrincipalID, enrollment.SecretCiphertext, step, now)
		if err != nil {
			return err
		}
		if !changed {
			return ErrMFAAlreadyOn
		}
		if err := repository.deleteMFAEnrollment(ctx, claims.PrincipalID); err != nil {
			return err
		}
		currentHash := currentSessionHash(cookieHeader, s.web.CookieName)
		if claims.ACR == 0 {
			changed, err := repository.revokeSessionByHash(ctx, currentHash, now)
			if err != nil {
				return err
			}
			if !changed {
				return ErrInvalidSession
			}
			sessionRevoked = true
		} else if err := repository.revokeOtherAuthentication(ctx, claims.PrincipalID, currentHash, now); err != nil {
			return err
		}
		return repository.appendAudit(ctx, securityAudit(claims.PrincipalID, "mfa_enabled", "success"))
	})
	if err != nil {
		return authnext.MFAConfirmation{}, fmt.Errorf("confirm TOTP enrollment: %w", err)
	}
	return authnext.MFAConfirmation{RecoveryCodes: recoveryCodes, SessionRevoked: sessionRevoked}, nil
}

func (s *Service) authenticateMFAEnrollment(
	ctx context.Context,
	authorization, cookieHeader string,
) (*authnext.Claims, error) {
	if strings.TrimSpace(authorization) != "" {
		return s.Authenticate(ctx, authorization, cookieHeader)
	}
	return s.authenticateBrowserSession(ctx, cookieHeader, true)
}

func (s *Service) VerifyLoginMFA(
	ctx context.Context,
	challengeID, code string,
) (authnext.LoginResult, error) {
	if strings.TrimSpace(challengeID) == "" || strings.TrimSpace(code) == "" {
		return authnext.LoginResult{}, ErrMFAChallenge
	}
	now := s.now().UTC()
	var result authnext.LoginResult
	var principalID guid.ID
	invalidCode := false
	err := s.repo.runInTx(ctx, func(repository *Repository) error {
		challenge, err := repository.getMFAChallenge(ctx, tokenHash(challengeID))
		if err != nil || challenge.ConsumedAt != nil || !challenge.ExpiresAt.After(now) ||
			challenge.Attempts >= s.cfg.MFA.MaxAttempts {
			return ErrMFAChallenge
		}
		principalID = challenge.PrincipalID
		credential, err := repository.getMFAEnabledCredential(ctx, challenge.PrincipalID)
		if err != nil || credential.TOTPSecret == "" {
			return ErrMFAChallenge
		}
		valid, err := s.consumeFactor(ctx, repository, credential, code, now)
		if err != nil {
			return err
		}
		if !valid {
			changed, err := repository.failMFAChallenge(
				ctx, challenge.IDHash, challenge.Attempts, s.cfg.MFA.MaxAttempts, now,
			)
			if err != nil {
				return err
			}
			if !changed {
				return ErrMFAChallenge
			}
			invalidCode = true
			return nil
		}
		changed, err := repository.consumeMFAChallenge(ctx, challenge.IDHash, now)
		if err != nil {
			return err
		}
		if !changed {
			return ErrMFAChallenge
		}
		sessionID, err := s.insertSession(ctx, repository, challenge.PrincipalID, now, authnext.Assurance{
			AuthTime: now, Level: 2, Methods: []string{"pwd", "mfa"},
		})
		if err != nil {
			return err
		}
		result = authnext.LoginResult{
			Status: "authenticated", ReturnURL: challenge.ReturnURL, SessionID: sessionID,
		}
		return repository.appendAudit(ctx, securityAudit(challenge.PrincipalID, "mfa_login", "success"))
	})
	if err != nil {
		return authnext.LoginResult{}, err
	}
	if invalidCode {
		s.audit(ctx, principalID, "mfa_login", "denied")
		return authnext.LoginResult{}, ErrInvalidMFA
	}
	return result, nil
}

func (s *Service) RegenerateRecoveryCodes(
	ctx context.Context,
	authorization, cookieHeader, currentPassword, code string,
) (authnext.MFAConfirmation, error) {
	claims, err := s.Authenticate(ctx, authorization, cookieHeader)
	if err != nil {
		return authnext.MFAConfirmation{}, err
	}
	_, credential, err := s.verifyCurrentPassword(ctx, claims.PrincipalID, currentPassword)
	if err != nil {
		return authnext.MFAConfirmation{}, err
	}
	if !credential.MFARequired || credential.TOTPSecret == "" {
		return authnext.MFAConfirmation{}, ErrMFANotEnabled
	}
	now := s.now().UTC()
	var codes []string
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		current, err := repository.getMFAEnabledCredential(ctx, claims.PrincipalID)
		if err != nil || current.TOTPSecret == "" {
			return ErrMFANotEnabled
		}
		validPassword, err := passwordx.Verify(current.PasswordHash, currentPassword)
		if err != nil {
			return err
		}
		if !validPassword {
			return authnext.ErrInvalidCredentials
		}
		valid, err := s.consumeFactor(ctx, repository, current, code, now)
		if err != nil {
			return err
		}
		if !valid {
			return ErrInvalidMFA
		}
		codes, err = s.replaceRecoveryCodes(ctx, repository, claims.PrincipalID, now)
		if err != nil {
			return err
		}
		if err := repository.revokeOtherAuthentication(
			ctx, claims.PrincipalID, currentSessionHash(cookieHeader, s.web.CookieName), now,
		); err != nil {
			return err
		}
		return repository.appendAudit(ctx, securityAudit(claims.PrincipalID, "mfa_recovery_regenerated", "success"))
	})
	if err != nil {
		return authnext.MFAConfirmation{}, fmt.Errorf("regenerate recovery codes: %w", err)
	}
	return authnext.MFAConfirmation{RecoveryCodes: codes}, nil
}

func (s *Service) DisableTOTP(
	ctx context.Context,
	authorization, cookieHeader, currentPassword, code string,
) error {
	claims, err := s.Authenticate(ctx, authorization, cookieHeader)
	if err != nil {
		return err
	}
	_, credential, err := s.verifyCurrentPassword(ctx, claims.PrincipalID, currentPassword)
	if err != nil {
		return err
	}
	if !credential.MFARequired || credential.TOTPSecret == "" {
		return ErrMFANotEnabled
	}
	now := s.now().UTC()
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		current, err := repository.getMFAEnabledCredential(ctx, claims.PrincipalID)
		if err != nil || current.TOTPSecret == "" {
			return ErrMFANotEnabled
		}
		validPassword, err := passwordx.Verify(current.PasswordHash, currentPassword)
		if err != nil {
			return err
		}
		if !validPassword {
			return authnext.ErrInvalidCredentials
		}
		valid, err := s.consumeFactor(ctx, repository, current, code, now)
		if err != nil {
			return err
		}
		if !valid {
			return ErrInvalidMFA
		}
		if err := repository.disableMFA(ctx, claims.PrincipalID, now); err != nil {
			return err
		}
		if err := repository.purgeMFAState(ctx, claims.PrincipalID); err != nil {
			return err
		}
		if err := repository.revokeOtherAuthentication(
			ctx, claims.PrincipalID, currentSessionHash(cookieHeader, s.web.CookieName), now,
		); err != nil {
			return err
		}
		return repository.appendAudit(ctx, securityAudit(claims.PrincipalID, "mfa_disabled", "success"))
	})
	if err != nil {
		return fmt.Errorf("disable TOTP: %w", err)
	}
	return nil
}

func (s *Service) verifyCurrentPassword(
	ctx context.Context,
	principalID guid.ID,
	password string,
) (*identity.Principal, *model.Credential, error) {
	principal, err := s.repo.getActivePrincipal(ctx, principalID)
	if err != nil {
		return nil, nil, authnext.ErrInvalidCredentials
	}
	credential, err := s.repo.getCredential(ctx, principalID)
	if err != nil {
		return nil, nil, fmt.Errorf("load credential: %w", err)
	}
	valid, err := passwordx.Verify(credential.PasswordHash, password)
	if err != nil {
		return nil, nil, fmt.Errorf("verify current password: %w", err)
	}
	if !valid {
		return nil, nil, authnext.ErrInvalidCredentials
	}
	return principal, credential, nil
}

func (s *Service) consumeFactor(
	ctx context.Context,
	repository *Repository,
	credential *model.Credential,
	code string,
	now time.Time,
) (bool, error) {
	trimmed := strings.TrimSpace(code)
	normalized := normalizeRecoveryCode(code)
	if len(trimmed) == 6 && strings.IndexFunc(trimmed, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		secret, err := s.decryptMFASecret(credential.PrincipalID, credential.TOTPSecret)
		if err != nil {
			return false, err
		}
		step, valid := validateTOTP(trimmed, secret, now)
		if !valid || step <= credential.TOTPLastUsedStep {
			return false, nil
		}
		return repository.advanceTOTPStep(ctx, credential.PrincipalID, step)
	}
	if normalized == "" {
		return false, nil
	}
	return repository.consumeRecoveryCode(
		ctx, credential.PrincipalID, s.recoveryCodeHashes(normalized), now,
	)
}

func validateTOTP(code, secret string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	counter := now.Unix() / totpPeriod
	for _, candidate := range []int64{counter, counter - 1, counter + 1} {
		if candidate < 0 {
			continue
		}
		valid, err := hotp.ValidateCustom(
			code, uint64(candidate), secret,
			hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1},
		)
		if err == nil && valid {
			return candidate, true
		}
	}
	return 0, false
}

func (s *Service) replaceRecoveryCodes(
	ctx context.Context,
	repository *Repository,
	principalID guid.ID,
	now time.Time,
) ([]string, error) {
	codes := make([]string, 0, s.cfg.MFA.RecoveryCodes)
	rows := make([]*model.RecoveryCode, 0, s.cfg.MFA.RecoveryCodes)
	for range s.cfg.MFA.RecoveryCodes {
		code, err := generateRecoveryCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
		rows = append(rows, &model.RecoveryCode{
			PrincipalID: principalID, CodeHash: s.recoveryCodeHash(normalizeRecoveryCode(code)), CreatedAt: now,
		})
	}
	if err := repository.replaceRecoveryCodes(ctx, principalID, rows); err != nil {
		return nil, err
	}
	return codes, nil
}

func generateRecoveryCode() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate recovery code: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	groups := make([]string, 0, (len(encoded)+3)/4)
	for start := 0; start < len(encoded); start += 4 {
		groups = append(groups, encoded[start:min(start+4, len(encoded))])
	}
	return strings.Join(groups, "-"), nil
}

func normalizeRecoveryCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

// recoveryCodeHash derives the value to store for a freshly issued code, under
// the key that is currently sealing.
func (s *Service) recoveryCodeHash(code string) string {
	return s.mfaKeys.MAC("recovery:v1", recoveryCodeMessage(code))
}

// recoveryCodeHashes derives what a presented code would have been stored as
// under each key the ring holds. Recovery codes are looked up by their stored
// value rather than compared against a row already in hand, and a lookup cannot
// be key-agnostic, so every candidate goes into the query. These codes are the
// one credential a user falls back on after losing their authenticator, so a
// rotation must not quietly turn them all into nonsense.
func (s *Service) recoveryCodeHashes(code string) []string {
	return s.mfaKeys.MACs("recovery:v1", recoveryCodeMessage(code))
}

func recoveryCodeMessage(code string) []byte {
	return []byte("platform:mfa:recovery:v1\x00" + code)
}

func (s *Service) ensureRecoveryCooldownExpired(ctx context.Context, principalID guid.ID) error {
	cooldown, err := s.repo.recoveryCooldown(ctx, principalID)
	if err != nil {
		return err
	}
	if cooldown != nil && cooldown.After(s.now().UTC()) {
		return authnext.ErrRecoveryCooldown
	}
	return nil
}
