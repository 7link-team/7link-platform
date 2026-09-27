package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/webauthnx"
	"gorm.io/gorm"
)

const (
	passkeyRegistration = "registration"
	passkeyLogin        = "login"
	passkeyCipher       = "passkey:v1"
)

func (s *Service) ListPasskeys(
	ctx context.Context,
	authorization, cookieHeader string,
) ([]authnext.Passkey, error) {
	if err := s.requirePasskeys(); err != nil {
		return nil, err
	}
	claims, err := s.personalReadClaims(ctx, authorization, cookieHeader)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.listPasskeys(ctx, claims.PrincipalID)
	if err != nil {
		return nil, fmt.Errorf("list passkeys: %w", err)
	}
	result := make([]authnext.Passkey, 0, len(rows))
	for _, row := range rows {
		result = append(result, passkeyView(row))
	}
	return result, nil
}

func (s *Service) BeginPasskeyRegistration(
	ctx context.Context,
	authorization, cookieHeader, currentPassword string,
) (authnext.PasskeyOptions, error) {
	if err := s.requirePasskeys(); err != nil {
		return authnext.PasskeyOptions{}, err
	}
	claims, err := s.authenticateAccountHolder(ctx, authorization, cookieHeader)
	if err != nil {
		return authnext.PasskeyOptions{}, err
	}
	if err := s.ensureRecoveryCooldownExpired(ctx, claims.PrincipalID); err != nil {
		return authnext.PasskeyOptions{}, err
	}
	principal, _, err := s.verifyCurrentPassword(ctx, claims.PrincipalID, currentPassword)
	if err != nil {
		return authnext.PasskeyOptions{}, err
	}
	count, err := s.repo.countPasskeys(ctx, claims.PrincipalID)
	if err != nil {
		return authnext.PasskeyOptions{}, fmt.Errorf("count passkeys: %w", err)
	}
	if count >= int64(s.cfg.Passkey.MaxCredentials) {
		return authnext.PasskeyOptions{}, authnext.ErrPasskeyLimit
	}
	now := s.now().UTC()
	user, err := s.ensurePasskeyUser(ctx, principal, now)
	if err != nil {
		return authnext.PasskeyOptions{}, err
	}
	expires := now.Add(s.cfg.Passkey.ChallengeTTL)
	options, session, err := s.passkeys.BeginRegistration(user, expires)
	if err != nil {
		return authnext.PasskeyOptions{}, fmt.Errorf("begin passkey registration: %w", err)
	}
	result, err := s.storePasskeyChallenge(
		ctx, passkeyRegistration, claims.PrincipalID, "", session, now, expires,
	)
	if err != nil {
		return authnext.PasskeyOptions{}, err
	}
	result.Options = json.RawMessage(options)
	s.audit(ctx, claims.PrincipalID, "passkey_registration_started", "success")
	return result, nil
}

func (s *Service) FinishPasskeyRegistration(
	ctx context.Context,
	authorization, cookieHeader, challengeID, name string,
	response json.RawMessage,
) (authnext.Passkey, error) {
	if err := s.requirePasskeys(); err != nil {
		return authnext.Passkey{}, err
	}
	claims, err := s.authenticateAccountHolder(ctx, authorization, cookieHeader)
	if err != nil {
		return authnext.Passkey{}, err
	}
	name, err = normalizePasskeyName(name)
	if err != nil || len(response) == 0 {
		return authnext.Passkey{}, authnext.ErrPasskeyCredential
	}
	now := s.now().UTC()
	challenge, err := s.consumePasskeyChallenge(
		ctx, challengeID, passkeyRegistration, claims.PrincipalID, now,
	)
	if err != nil {
		return authnext.Passkey{}, err
	}
	user, err := s.loadPasskeyUser(ctx, s.repo, claims.PrincipalID)
	if err != nil {
		return authnext.Passkey{}, err
	}
	credential, err := s.passkeys.FinishRegistration(user, []byte(challenge.SessionData), response)
	if err != nil {
		s.audit(ctx, claims.PrincipalID, "passkey_registration", "denied")
		return authnext.Passkey{}, authnext.ErrPasskeyCredential
	}
	if err := s.checkPasskeyAttestation(credential); err != nil {
		s.audit(ctx, claims.PrincipalID, "passkey_registration", "denied")
		return authnext.Passkey{}, err
	}
	return s.storePasskeyCredential(ctx, claims.PrincipalID, name, credential, now)
}

func (s *Service) storePasskeyCredential(
	ctx context.Context,
	principalID guid.ID,
	name string,
	credential webauthnx.Credential,
	now time.Time,
) (authnext.Passkey, error) {
	idHash := passkeyIDHash(credential.ID)
	ciphertext, err := s.encryptAuthnData(
		passkeyCipher, passkeyAAD(principalID, idHash), credential.Data,
	)
	if err != nil {
		return authnext.Passkey{}, err
	}
	now = now.UTC()
	row := &model.Passkey{
		IDHash: idHash, PrincipalID: principalID, Name: name,
		CredentialCiphertext: ciphertext, SignCount: credential.SignCount,
		CreatedAt: now, UpdatedAt: now,
	}
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		created, err := repository.createPasskey(ctx, row, s.cfg.Passkey.MaxCredentials)
		if err != nil {
			return err
		}
		if !created {
			return authnext.ErrPasskeyLimit
		}
		return repository.appendAudit(ctx, securityAudit(principalID, "passkey_registered", "success"))
	})
	if err != nil {
		if errors.Is(err, authnext.ErrPasskeyLimit) {
			return authnext.Passkey{}, err
		}
		return authnext.Passkey{}, fmt.Errorf("store passkey: %w", err)
	}
	view := passkeyView(row)
	view.AttestationFormat = credential.AttestationFormat
	view.AttestationType = credential.AttestationType
	view.AAGUID = hex.EncodeToString(credential.AAGUID)
	return view, nil
}

func (s *Service) checkPasskeyAttestation(credential webauthnx.Credential) error {
	if len(s.cfg.Passkey.AllowedAttestationFormats) > 0 &&
		!slices.Contains(s.cfg.Passkey.AllowedAttestationFormats, credential.AttestationFormat) {
		return authnext.ErrPasskeyAttestation
	}
	if len(s.cfg.Passkey.AllowedAAGUIDs) == 0 {
		return nil
	}
	aaguid := hex.EncodeToString(credential.AAGUID)
	for _, allowed := range s.cfg.Passkey.AllowedAAGUIDs {
		if strings.EqualFold(allowed, aaguid) {
			return nil
		}
	}
	return authnext.ErrPasskeyAttestation
}

func (s *Service) BeginPasskeyLogin(
	ctx context.Context,
	origin, returnURL string,
) (authnext.PasskeyOptions, error) {
	if err := s.requirePasskeys(); err != nil {
		return authnext.PasskeyOptions{}, err
	}
	if !slices.Contains(s.cfg.Passkey.Origins, origin) {
		return authnext.PasskeyOptions{}, ErrCSRF
	}
	returnURL = strings.TrimSpace(returnURL)
	if returnURL == "" {
		returnURL = s.web.PostLoginURL
	}
	if !s.returnURLs.allows(returnURL) {
		return authnext.PasskeyOptions{}, authnext.ErrReturnURL
	}
	now := s.now().UTC()
	expires := now.Add(s.cfg.Passkey.ChallengeTTL)
	options, session, err := s.passkeys.BeginLogin(expires)
	if err != nil {
		return authnext.PasskeyOptions{}, fmt.Errorf("begin passkey login: %w", err)
	}
	result, err := s.storePasskeyChallenge(ctx, passkeyLogin, 0, returnURL, session, now, expires)
	if err != nil {
		return authnext.PasskeyOptions{}, err
	}
	result.Options = json.RawMessage(options)
	return result, nil
}

func (s *Service) FinishPasskeyLogin(
	ctx context.Context,
	origin, challengeID string,
	response json.RawMessage,
) (authnext.LoginResult, error) {
	if err := s.requirePasskeys(); err != nil {
		return authnext.LoginResult{}, err
	}
	if !slices.Contains(s.cfg.Passkey.Origins, origin) {
		return authnext.LoginResult{}, ErrCSRF
	}
	if len(response) == 0 {
		return authnext.LoginResult{}, authnext.ErrPasskeyCredential
	}
	now := s.now().UTC()
	challenge, err := s.consumePasskeyChallenge(ctx, challengeID, passkeyLogin, 0, now)
	if err != nil {
		return authnext.LoginResult{}, err
	}
	var principalID guid.ID
	var current *model.Passkey
	_, credential, err := s.passkeys.FinishLogin(
		[]byte(challenge.SessionData), response,
		func(credentialID, userHandle []byte) (webauthnx.User, error) {
			var lookupErr error
			principalID, current, lookupErr = s.resolvePasskey(ctx, credentialID, userHandle)
			if lookupErr != nil {
				return webauthnx.User{}, lookupErr
			}
			return s.loadPasskeyUser(ctx, s.repo, principalID)
		},
	)
	if err != nil || principalID.Zero() || current == nil {
		s.audit(ctx, principalID, "passkey_login", "denied")
		return authnext.LoginResult{}, authnext.ErrPasskeyCredential
	}
	sessionID, err := s.completePasskeyLogin(ctx, principalID, current, credential, now)
	if err != nil {
		return authnext.LoginResult{}, err
	}
	return authnext.LoginResult{
		Status: "authenticated", ReturnURL: challenge.ReturnURL, SessionID: sessionID,
	}, nil
}

func (s *Service) completePasskeyLogin(
	ctx context.Context,
	principalID guid.ID,
	current *model.Passkey,
	credential webauthnx.Credential,
	now time.Time,
) (string, error) {
	now = now.UTC()
	if credential.CloneWarning {
		if err := s.persistPasskeyCloneWarning(ctx, principalID, current.IDHash, now); err != nil {
			return "", fmt.Errorf("store passkey clone warning: %w", err)
		}
		s.audit(ctx, principalID, "passkey_counter_regression", "denied")
		return "", authnext.ErrPasskeyCredential
	}
	ciphertext, err := s.encryptAuthnData(
		passkeyCipher, passkeyAAD(principalID, current.IDHash), credential.Data,
	)
	if err != nil {
		return "", err
	}
	var sessionID string
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		updated, err := repository.updatePasskeyCounter(
			ctx, current.IDHash, current.SignCount, credential.SignCount,
			current.CredentialCiphertext, ciphertext, now,
		)
		if err != nil {
			return err
		}
		if !updated {
			return authnext.ErrPasskeyCredential
		}
		sessionID, err = s.insertSession(
			ctx, repository, principalID, now,
			authnext.Assurance{AuthTime: now, Level: 2, Methods: []string{"passkey"}},
		)
		if err != nil {
			return err
		}
		return repository.appendAudit(ctx, securityAudit(principalID, "passkey_login", "success"))
	})
	if err != nil {
		return "", fmt.Errorf("complete passkey login: %w", err)
	}
	return sessionID, nil
}

func (s *Service) persistPasskeyCloneWarning(
	ctx context.Context,
	principalID guid.ID,
	idHash string,
	now time.Time,
) error {
	return s.repo.runInTx(ctx, func(repository *Repository) error {
		current, err := repository.lockPasskey(ctx, principalID, idHash)
		if err != nil {
			return err
		}
		plain, err := s.decryptAuthnData(
			passkeyCipher, passkeyAAD(principalID, idHash), current.CredentialCiphertext,
		)
		if err != nil {
			return err
		}
		marked, err := webauthnx.MarkCloneWarning(plain)
		if err != nil {
			return err
		}
		ciphertext, err := s.encryptAuthnData(passkeyCipher, passkeyAAD(principalID, idHash), marked)
		if err != nil {
			return err
		}
		updated, err := repository.storePasskeyCloneWarning(
			ctx, idHash, current.CredentialCiphertext, ciphertext, now,
		)
		if err != nil {
			return err
		}
		if !updated {
			return authnext.ErrPasskeyCredential
		}
		return nil
	})
}

func (s *Service) RenamePasskey(
	ctx context.Context,
	authorization, cookieHeader, id, name string,
) (authnext.Passkey, error) {
	if err := s.requirePasskeys(); err != nil {
		return authnext.Passkey{}, err
	}
	claims, err := s.authenticateAccountHolder(ctx, authorization, cookieHeader)
	if err != nil {
		return authnext.Passkey{}, err
	}
	name, err = normalizePasskeyName(name)
	if err != nil {
		return authnext.Passkey{}, err
	}
	now := s.now().UTC()
	var row *model.Passkey
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		var changed bool
		row, changed, err = repository.renamePasskey(ctx, claims.PrincipalID, id, name, now)
		if err != nil {
			return err
		}
		if !changed {
			return authnext.ErrPasskeyNotFound
		}
		return repository.appendAudit(ctx, securityAudit(claims.PrincipalID, "passkey_renamed", "success"))
	})
	if err != nil {
		if errors.Is(err, authnext.ErrPasskeyNotFound) {
			return authnext.Passkey{}, err
		}
		return authnext.Passkey{}, fmt.Errorf("rename passkey: %w", err)
	}
	return passkeyView(row), nil
}

func (s *Service) DeletePasskey(
	ctx context.Context,
	authorization, cookieHeader, id, currentPassword string,
) error {
	if err := s.requirePasskeys(); err != nil {
		return err
	}
	claims, err := s.authenticateAccountHolder(ctx, authorization, cookieHeader)
	if err != nil {
		return err
	}
	if _, _, err := s.verifyCurrentPassword(ctx, claims.PrincipalID, currentPassword); err != nil {
		return err
	}
	now := s.now().UTC()
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		deleted, err := repository.deletePasskey(ctx, claims.PrincipalID, id)
		if err != nil {
			return err
		}
		if !deleted {
			return authnext.ErrPasskeyNotFound
		}
		if err := repository.revokeOtherAuthentication(
			ctx, claims.PrincipalID, currentSessionHash(cookieHeader, s.web.CookieName), now,
		); err != nil {
			return err
		}
		return repository.appendAudit(ctx, securityAudit(claims.PrincipalID, "passkey_deleted", "success"))
	})
	if err != nil {
		if errors.Is(err, authnext.ErrPasskeyNotFound) {
			return err
		}
		return fmt.Errorf("delete passkey: %w", err)
	}
	return nil
}

func (s *Service) requirePasskeys() error {
	if s == nil || !s.Enabled() || !s.cfg.Passkey.Enabled || s.passkeys == nil {
		return authnext.ErrPasskeyDisabled
	}
	return nil
}

func (s *Service) ensurePasskeyUser(
	ctx context.Context,
	principal *identity.Principal,
	now time.Time,
) (webauthnx.User, error) {
	handle := make([]byte, 32)
	if _, err := rand.Read(handle); err != nil {
		return webauthnx.User{}, fmt.Errorf("generate passkey user handle: %w", err)
	}
	row, err := s.repo.ensurePasskeyUser(ctx, &model.PasskeyUser{
		PrincipalID: principal.ID,
		UserHandle:  base64.RawURLEncoding.EncodeToString(handle),
		CreatedAt:   now.UTC(),
	})
	if err != nil {
		return webauthnx.User{}, fmt.Errorf("store passkey user handle: %w", err)
	}
	return s.loadPasskeyUserWithIdentity(ctx, s.repo, principal, row)
}

func (s *Service) loadPasskeyUser(
	ctx context.Context,
	repository *Repository,
	principalID guid.ID,
) (webauthnx.User, error) {
	principal, err := repository.getActivePrincipal(ctx, principalID)
	if err != nil {
		return webauthnx.User{}, authnext.ErrPasskeyCredential
	}
	user, err := repository.getPasskeyUser(ctx, principalID)
	if err != nil {
		return webauthnx.User{}, authnext.ErrPasskeyCredential
	}
	return s.loadPasskeyUserWithIdentity(ctx, repository, principal, user)
}

func (s *Service) loadPasskeyUserWithIdentity(
	ctx context.Context,
	repository *Repository,
	principal *identity.Principal,
	user *model.PasskeyUser,
) (webauthnx.User, error) {
	handle, err := base64.RawURLEncoding.DecodeString(user.UserHandle)
	if err != nil || len(handle) == 0 || len(handle) > 64 {
		return webauthnx.User{}, authnext.ErrPasskeyCredential
	}
	rows, err := repository.listPasskeys(ctx, principal.ID)
	if err != nil {
		return webauthnx.User{}, fmt.Errorf("load passkey credentials: %w", err)
	}
	stored := make([][]byte, 0, len(rows))
	for _, row := range rows {
		plain, err := s.decryptAuthnData(
			passkeyCipher, passkeyAAD(principal.ID, row.IDHash), row.CredentialCiphertext,
		)
		if err != nil {
			return webauthnx.User{}, authnext.ErrPasskeyCredential
		}
		stored = append(stored, plain)
	}
	resolved, err := webauthnx.NewUser(handle, principal.LoginName, principal.DisplayName, stored)
	if err != nil {
		return webauthnx.User{}, authnext.ErrPasskeyCredential
	}
	return resolved, nil
}

func (s *Service) resolvePasskey(
	ctx context.Context,
	credentialID, userHandle []byte,
) (guid.ID, *model.Passkey, error) {
	return s.resolvePasskeyOn(ctx, s.repo, credentialID, userHandle)
}

func (s *Service) resolvePasskeyOn(ctx context.Context, repository *Repository, credentialID, userHandle []byte) (guid.ID, *model.Passkey, error) {
	row, err := repository.getPasskey(ctx, passkeyIDHash(credentialID))
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, err
		}
		return 0, nil, authnext.ErrPasskeyCredential
	}
	_, err = repository.findPasskeyUserByHandle(
		ctx, row.PrincipalID, base64.RawURLEncoding.EncodeToString(userHandle),
	)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, err
		}
		return 0, nil, authnext.ErrPasskeyCredential
	}
	if _, err := repository.getActivePrincipal(ctx, row.PrincipalID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, authnext.ErrPasskeyCredential
		}
		return 0, nil, err
	}
	return row.PrincipalID, row, nil
}

func (s *Service) storePasskeyChallenge(
	ctx context.Context,
	kind string,
	principalID guid.ID,
	returnURL string,
	session []byte,
	now, expires time.Time,
) (authnext.PasskeyOptions, error) {
	id, err := randomToken(32)
	if err != nil {
		return authnext.PasskeyOptions{}, err
	}
	row := &model.PasskeyChallenge{
		IDHash: tokenHash(id), Kind: kind, ReturnURL: returnURL, SessionData: string(session),
		CreatedAt: now.UTC(), ExpiresAt: expires.UTC(),
	}
	if !principalID.Zero() {
		row.PrincipalID = &principalID
	}
	if err := s.repo.createPasskeyChallenge(ctx, row, now.UTC()); err != nil {
		return authnext.PasskeyOptions{}, fmt.Errorf("store passkey challenge: %w", err)
	}
	return authnext.PasskeyOptions{ChallengeID: id, ExpiresAt: expires.UTC()}, nil
}

func (s *Service) consumePasskeyChallenge(
	ctx context.Context,
	id, kind string,
	principalID guid.ID,
	now time.Time,
) (*model.PasskeyChallenge, error) {
	if strings.TrimSpace(id) == "" {
		return nil, authnext.ErrPasskeyChallenge
	}
	hash := tokenHash(id)
	row, err := s.repo.getPasskeyChallenge(ctx, hash, kind, principalID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, authnext.ErrPasskeyChallenge
		}
		return nil, fmt.Errorf("load passkey challenge: %w", err)
	}
	consumed, err := s.repo.consumePasskeyChallenge(ctx, hash, kind, principalID, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("consume passkey challenge: %w", err)
	}
	if !consumed {
		return nil, authnext.ErrPasskeyChallenge
	}
	return row, nil
}

func normalizePasskeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return "", authnext.ErrPasskeyCredential
	}
	return name, nil
}

func passkeyIDHash(id []byte) string {
	sum := sha256.Sum256(id)
	return hex.EncodeToString(sum[:])
}

func passkeyAAD(principalID guid.ID, idHash string) string {
	return principalID.String() + "\x00" + idHash
}

func passkeyView(row *model.Passkey) authnext.Passkey {
	return authnext.Passkey{
		ID: row.IDHash, Name: row.Name, SignCount: row.SignCount,
		CreatedAt: row.CreatedAt.UTC(), LastUsedAt: row.LastUsedAt,
	}
}
