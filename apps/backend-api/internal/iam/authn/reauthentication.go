package authn

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/passwordx"
	"7link.com/platform/pkg/extension/webauthnx"
	"gorm.io/gorm"
)

const (
	ReauthenticationIdentityLink        = "identity_link"
	ReauthenticationIdentityUnlink      = "identity_unlink"
	ReauthenticationPersonalTokenCreate = "personal_token_create"
	ReauthenticationPrincipalClose      = "principal_close"
	ReauthenticationTenantLeave         = "tenant_leave"
	reauthenticationTTL                 = 5 * time.Minute
	reauthenticationAttempts            = 5
)

// ReauthenticationConsumer consumes a verified proof in the caller's transaction.
// It takes the principal lock first; all subsequent domain writes and audit must
// use that same transaction, and must not acquire child locks before this call.
type ReauthenticationConsumer = authnext.ReauthenticationConsumer

// FederatedReauthentication exposes only the trusted upstream completion path.
// Inspect binds an unverified challenge to a browser; VerifyOn is called only
// after federation has verified an existing linked identity in a fresh flow.
type FederatedReauthentication struct {
	Inspect           func(context.Context, string, string) (ReauthenticationBinding, error)
	VerifyOn          func(context.Context, *gorm.DB, guid.ID, string, string) error
	ValidateSessionOn func(context.Context, *gorm.DB, guid.ID, string) (int64, error)
	LocalMethodsOn    func(context.Context, *gorm.DB, guid.ID) (int, error)
}

type ReauthenticationBinding struct {
	PrincipalID   guid.ID
	ChallengeHash string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type ReauthenticationOptions struct {
	Proof     string          `json:"proof"`
	Method    string          `json:"method"`
	ExpiresAt time.Time       `json:"expires_at"`
	Options   json.RawMessage `json:"options,omitempty"`
}

type ReauthenticationMethods struct {
	Methods     []string `json:"methods"`
	MFARequired bool     `json:"mfa_required"`
}

func (s *Service) AvailableReauthenticationMethods(ctx context.Context, cookieHeader string) (ReauthenticationMethods, error) {
	claims, err := s.Authenticate(ctx, "", cookieHeader)
	if err != nil {
		return ReauthenticationMethods{}, err
	}
	result := ReauthenticationMethods{Methods: []string{}}
	credential, err := s.repo.getCredential(ctx, claims.PrincipalID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if err == nil && credential.PasswordHash != "" {
		result.Methods = append(result.Methods, "password")
		result.MFARequired = credential.MFARequired || credential.TOTPSecret != ""
	}
	if s.cfg.Passkey.Enabled && s.passkeys != nil {
		count, err := s.repo.countPasskeys(ctx, claims.PrincipalID)
		if err != nil {
			return result, err
		}
		if count > 0 {
			result.Methods = append(result.Methods, "passkey")
		}
	}
	return result, nil
}

func validReauthenticationPurpose(purpose string) bool {
	return slices.Contains([]string{ReauthenticationIdentityLink, ReauthenticationIdentityUnlink,
		ReauthenticationPersonalTokenCreate, ReauthenticationPrincipalClose, ReauthenticationTenantLeave}, purpose)
}

// BeginReauthentication never accepts a bearer in place of the browser session.
// The returned secret becomes usable only after fresh verification succeeds.
func (s *Service) BeginReauthentication(ctx context.Context, cookieHeader, purpose, target, method, origin string) (ReauthenticationOptions, error) {
	if !validReauthenticationPurpose(purpose) || target == "" || len(target) > 512 {
		return ReauthenticationOptions{}, authnext.ErrInvalidCredentials
	}
	claims, err := s.Authenticate(ctx, "", cookieHeader)
	if err != nil {
		return ReauthenticationOptions{}, err
	}
	if !slices.Contains([]string{"password", "passkey", "federation"}, method) {
		return ReauthenticationOptions{}, authnext.ErrInvalidCredentials
	}
	proof, err := randomToken(32)
	if err != nil {
		return ReauthenticationOptions{}, err
	}
	now := s.now().UTC()
	result := ReauthenticationOptions{Proof: proof, Method: method, ExpiresAt: now.Add(reauthenticationTTL)}
	row := model.ReauthenticationChallenge{IDHash: tokenHash(proof), PrincipalID: claims.PrincipalID,
		SessionHash: currentSessionHash(cookieHeader, s.web.CookieName), Purpose: purpose, Target: target,
		Method: method, CreatedAt: now, ExpiresAt: result.ExpiresAt}
	if method == "passkey" {
		if err := s.requirePasskeys(); err != nil {
			return ReauthenticationOptions{}, err
		}
		if !slices.Contains(s.cfg.Passkey.Origins, origin) {
			return ReauthenticationOptions{}, ErrCSRF
		}
		options, session, err := s.passkeys.BeginLogin(result.ExpiresAt)
		if err != nil {
			return ReauthenticationOptions{}, err
		}
		row.SessionData, err = s.encryptAuthnData("reauthentication", row.IDHash, session)
		if err != nil {
			return ReauthenticationOptions{}, err
		}
		result.Options = options
	}
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		version, err := s.lockReauthenticationSession(ctx, repo, claims.PrincipalID, row.SessionHash)
		if err != nil {
			return err
		}
		row.CredentialVersion = version
		if method == "password" {
			credential, err := repo.getCredential(ctx, claims.PrincipalID)
			if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && credential.PasswordHash == "" {
				return authnext.ErrInvalidCredentials
			}
			if err != nil {
				return err
			}
		}
		if err := repo.writer().WithContext(ctx).Table(repo.table(row.TableName())).Create(&row).Error; err != nil {
			return err
		}
		return repo.appendAudit(ctx, securityAudit(claims.PrincipalID, "reauthentication_started", "success"))
	})
	return result, err
}

// lockReauthenticationSession uses a write as the first transaction statement
// on every engine, then reads current principal and session state under the lock.
func (s *Service) lockReauthenticationSession(ctx context.Context, repo *Repository, principalID guid.ID, sessionHash string) (int64, error) {
	if principalID.Zero() || sessionHash == "" {
		return 0, authnext.ErrInvalidCredentials
	}
	if err := repo.writer().WithContext(ctx).Table(repo.table("iam_principals")).
		Where("id = ? AND status = ?", principalID, "active").Update("updated_at", gorm.Expr("updated_at")).Error; err != nil {
		return 0, err
	}
	_, version, err := repo.activePrincipalClaims(ctx, principalID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, authnext.ErrInvalidCredentials
	}
	if err != nil {
		return 0, err
	}
	if err := repo.writer().WithContext(ctx).Table(repo.table("iam_sessions")).Where("id_hash = ?", sessionHash).
		Update("last_seen_at", gorm.Expr("last_seen_at")).Error; err != nil {
		return 0, err
	}
	session, err := repo.activeSession(ctx, sessionHash, s.now().UTC())
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && session.PrincipalID != principalID {
		return 0, authnext.ErrInvalidCredentials
	}
	return version, err
}

func (s *Service) reauthenticationOn(ctx context.Context, repo *Repository, principalID guid.ID, cookieHeader, hash string) (*model.ReauthenticationChallenge, error) {
	sessionHash := currentSessionHash(cookieHeader, s.web.CookieName)
	version, err := s.lockReauthenticationSession(ctx, repo, principalID, sessionHash)
	if err != nil {
		return nil, err
	}
	var row model.ReauthenticationChallenge
	err = repo.writer().WithContext(ctx).Table(repo.table(row.TableName())).Where("id_hash = ?", hash).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, authnext.ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if row.PrincipalID != principalID || row.SessionHash != sessionHash || row.CredentialVersion != version ||
		row.ConsumedAt != nil || !row.ExpiresAt.After(s.now().UTC()) || row.Attempts >= reauthenticationAttempts {
		return nil, authnext.ErrInvalidCredentials
	}
	return &row, nil
}

// VerifyReauthentication checks a new password (+ enabled MFA) or a UV-required
// passkey assertion. It does not rotate or refresh the existing browser session.
func (s *Service) VerifyReauthentication(ctx context.Context, cookieHeader, proof, password, code, origin string, response json.RawMessage) error {
	claims, err := s.Authenticate(ctx, "", cookieHeader)
	if err != nil {
		return err
	}
	invalid := false
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		row, err := s.reauthenticationOn(ctx, repo, claims.PrincipalID, cookieHeader, tokenHash(proof))
		if err != nil {
			return err
		}
		if row.VerifiedAt != nil || row.Method == "federation" {
			return authnext.ErrInvalidCredentials
		}
		valid := false
		switch row.Method {
		case "password":
			credential, err := repo.getCredential(ctx, claims.PrincipalID)
			if err != nil {
				return err
			}
			valid, err = passwordx.Verify(credential.PasswordHash, password)
			if err != nil {
				return err
			}
			if valid && credential.TOTPSecret != "" {
				valid, err = s.consumeFactor(ctx, repo, credential, code, s.now().UTC())
				if err != nil {
					return err
				}
			}
			if credential.MFARequired && credential.TOTPSecret == "" {
				valid = false
			}
		case "passkey":
			valid, err = s.verifyReauthenticationPasskey(ctx, repo, row, origin, response)
			if err != nil {
				return err
			}
		}
		if !valid {
			invalid = true
			return repo.writer().WithContext(ctx).Table(repo.table(row.TableName())).Where("id_hash = ?", row.IDHash).
				Update("attempts", gorm.Expr("attempts + 1")).Error
		}
		return s.markReauthenticationVerified(ctx, repo, row)
	})
	if err == nil && invalid {
		return authnext.ErrInvalidCredentials
	}
	return err
}

func (s *Service) verifyReauthenticationPasskey(ctx context.Context, repo *Repository, row *model.ReauthenticationChallenge, origin string, response json.RawMessage) (bool, error) {
	if err := s.requirePasskeys(); err != nil {
		return false, err
	}
	if !slices.Contains(s.cfg.Passkey.Origins, origin) {
		return false, ErrCSRF
	}
	session, err := s.decryptAuthnData("reauthentication", row.IDHash, row.SessionData)
	if err != nil {
		return false, err
	}
	var current *model.Passkey
	var lookupError error
	_, credential, err := s.passkeys.FinishLogin(session, response, func(id, userHandle []byte) (webauthnx.User, error) {
		principalID, candidate, err := s.resolvePasskeyOn(ctx, repo, id, userHandle)
		if err != nil {
			lookupError = err
			return webauthnx.User{}, err
		}
		if principalID != row.PrincipalID {
			return webauthnx.User{}, authnext.ErrPasskeyCredential
		}
		current = candidate
		user, err := s.loadPasskeyUser(ctx, repo, principalID)
		lookupError = err
		return user, err
	})
	if lookupError != nil && !errors.Is(lookupError, authnext.ErrPasskeyCredential) && !errors.Is(lookupError, authnext.ErrInvalidCredentials) && !errors.Is(lookupError, gorm.ErrRecordNotFound) {
		return false, lookupError
	}
	if err != nil || current == nil {
		return false, nil //nolint:nilerr // Invalid assertions count as failed attempts committed by the caller.
	}
	ciphertext, err := s.encryptAuthnData(passkeyCipher, passkeyAAD(row.PrincipalID, current.IDHash), credential.Data)
	if err != nil {
		return false, err
	}
	if credential.CloneWarning {
		_, err := repo.storePasskeyCloneWarning(ctx, current.IDHash, current.CredentialCiphertext, ciphertext, s.now().UTC())
		return false, err
	}
	return repo.updatePasskeyCounter(ctx, current.IDHash, current.SignCount, credential.SignCount, current.CredentialCiphertext, ciphertext, s.now().UTC())
}

func (s *Service) markReauthenticationVerified(ctx context.Context, repo *Repository, row *model.ReauthenticationChallenge) error {
	result := repo.writer().WithContext(ctx).Table(repo.table(row.TableName())).
		Where("id_hash = ? AND verified_at IS NULL AND consumed_at IS NULL", row.IDHash).Update("verified_at", s.now().UTC())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return authnext.ErrInvalidCredentials
	}
	return repo.appendAudit(ctx, securityAudit(row.PrincipalID, "reauthentication_verified", "success"))
}

// ConsumeReauthenticationOn atomically spends a verified proof with domain work.
func (s *Service) ConsumeReauthenticationOn(ctx context.Context, tx *gorm.DB, principalID guid.ID, cookieHeader, proof, purpose, target string) error {
	if s == nil || s.repo == nil || tx == nil {
		return authnext.ErrUnavailable
	}
	if !validReauthenticationPurpose(purpose) || strings.TrimSpace(target) == "" {
		return authnext.ErrInvalidCredentials
	}
	repo := s.repo.withDB(tx, true)
	row, err := s.reauthenticationOn(ctx, repo, principalID, cookieHeader, tokenHash(proof))
	if err != nil {
		return err
	}
	if row.VerifiedAt == nil || row.Purpose != purpose || row.Target != target {
		return authnext.ErrInvalidCredentials
	}
	result := tx.WithContext(ctx).Table(repo.table(row.TableName())).
		Where("id_hash = ? AND consumed_at IS NULL", row.IDHash).Update("consumed_at", s.now().UTC())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return authnext.ErrInvalidCredentials
	}
	return repo.appendAudit(ctx, securityAudit(principalID, "reauthentication_consumed", "success"))
}

func (s *Service) inspectFederatedReauthentication(ctx context.Context, cookieHeader, proof string) (ReauthenticationBinding, error) {
	claims, err := s.Authenticate(ctx, "", cookieHeader)
	if err != nil {
		return ReauthenticationBinding{}, err
	}
	var binding ReauthenticationBinding
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		row, err := s.reauthenticationOn(ctx, repo, claims.PrincipalID, cookieHeader, tokenHash(proof))
		if err != nil {
			return err
		}
		if row.Method != "federation" || row.VerifiedAt != nil {
			return authnext.ErrInvalidCredentials
		}
		binding = ReauthenticationBinding{PrincipalID: row.PrincipalID, ChallengeHash: row.IDHash, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}
		return nil
	})
	return binding, err
}

func (s *Service) verifyFederatedReauthenticationOn(ctx context.Context, tx *gorm.DB, principalID guid.ID, cookieHeader, challengeHash string) error {
	if s == nil || s.repo == nil || tx == nil {
		return authnext.ErrUnavailable
	}
	repo := s.repo.withDB(tx, true)
	row, err := s.reauthenticationOn(ctx, repo, principalID, cookieHeader, challengeHash)
	if err != nil {
		return err
	}
	if row.Method != "federation" || row.VerifiedAt != nil {
		return authnext.ErrInvalidCredentials
	}
	return s.markReauthenticationVerified(ctx, repo, row)
}

func (s *Service) localLoginMethodsOn(ctx context.Context, tx *gorm.DB, principalID guid.ID) (int, error) {
	if s == nil || s.repo == nil || tx == nil {
		return 0, authnext.ErrUnavailable
	}
	repo := s.repo.withDB(tx, true)
	count := 0
	credential, err := repo.getCredential(ctx, principalID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	if err == nil && credential.PasswordHash != "" {
		count++
	}
	if s.cfg.Passkey.Enabled && s.passkeys != nil {
		rows, err := repo.listPasskeys(ctx, principalID)
		if err != nil {
			return 0, err
		}
		for _, row := range rows {
			plain, err := s.decryptAuthnData(passkeyCipher, passkeyAAD(principalID, row.IDHash), row.CredentialCiphertext)
			if err != nil {
				return 0, err
			}
			var stored struct {
				Authenticator struct {
					CloneWarning bool `json:"cloneWarning"`
				} `json:"authenticator"`
			}
			if err := json.Unmarshal(plain, &stored); err != nil {
				return 0, err
			}
			if !stored.Authenticator.CloneWarning {
				count++
			}
		}
	}
	return count, nil
}
