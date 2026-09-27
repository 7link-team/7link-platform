package authn

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
)

// BeginStepUp starts a one-time MFA challenge bound to the current browser
// session. Bearer-only callers cannot rotate a browser session.
func (s *Service) BeginStepUp(
	ctx context.Context,
	authorization, cookieHeader string,
) (authnext.StepUpOptions, error) {
	claims, err := s.authenticateAccountHolder(ctx, authorization, cookieHeader)
	if err != nil {
		return authnext.StepUpOptions{}, err
	}
	credential, err := s.repo.getMFAEnabledCredential(ctx, claims.PrincipalID)
	if err != nil || credential.TOTPSecret == "" {
		return authnext.StepUpOptions{}, ErrMFANotEnabled
	}
	sessionHash := currentSessionHash(cookieHeader, s.web.CookieName)
	if sessionHash == "" {
		return authnext.StepUpOptions{}, ErrMFAChallenge
	}
	now := s.now().UTC()
	challengeID, err := randomToken(32)
	if err != nil {
		return authnext.StepUpOptions{}, err
	}
	expiresAt := now.Add(s.cfg.MFA.ChallengeTTL)
	row := model.StepUpChallenge{
		IDHash: tokenHash(challengeID), PrincipalID: claims.PrincipalID, SessionHash: sessionHash,
		CreatedAt: now, ExpiresAt: expiresAt,
	}
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		if err := repository.createStepUpChallenge(ctx, &row, now); err != nil {
			return err
		}
		return repository.appendAudit(ctx, securityAudit(claims.PrincipalID, "step_up_started", "success"))
	})
	if err != nil {
		return authnext.StepUpOptions{}, fmt.Errorf("create step-up challenge: %w", err)
	}
	return authnext.StepUpOptions{
		ChallengeID: challengeID, ExpiresAt: expiresAt,
		Methods: []string{"totp", "recovery_code"},
	}, nil
}

func (s *Service) VerifyStepUp(
	ctx context.Context,
	authorization, cookieHeader, challengeID, code string,
) (authnext.StepUpResult, error) {
	claims, err := s.authenticateAccountHolder(ctx, authorization, cookieHeader)
	if err != nil {
		return authnext.StepUpResult{}, err
	}
	if strings.TrimSpace(challengeID) == "" || strings.TrimSpace(code) == "" {
		return authnext.StepUpResult{}, ErrMFAChallenge
	}
	now := s.now().UTC()
	sessionHash := currentSessionHash(cookieHeader, s.web.CookieName)
	if sessionHash == "" {
		return authnext.StepUpResult{}, ErrMFAChallenge
	}
	var result authnext.StepUpResult
	invalidCode := false
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		challenge, err := repository.getStepUpChallenge(ctx, tokenHash(challengeID))
		if err != nil || challenge.ConsumedAt != nil || !challenge.ExpiresAt.After(now) ||
			challenge.Attempts >= s.cfg.MFA.MaxAttempts || challenge.PrincipalID != claims.PrincipalID ||
			challenge.SessionHash != sessionHash {
			return ErrMFAChallenge
		}
		credential, err := repository.getMFAEnabledCredential(ctx, claims.PrincipalID)
		if err != nil || credential.TOTPSecret == "" {
			return ErrMFAChallenge
		}
		valid, err := s.consumeFactor(ctx, repository, credential, code, now)
		if err != nil {
			return err
		}
		if !valid {
			changed, err := repository.failStepUpChallenge(
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
		consumed, err := repository.consumeStepUpChallenge(
			ctx, challenge.IDHash, claims.PrincipalID, sessionHash, now,
		)
		if err != nil {
			return err
		}
		if !consumed {
			return ErrMFAChallenge
		}
		sessionID, expiresAt, err := s.elevateSession(ctx, repository, claims.PrincipalID, sessionHash, now)
		if err != nil {
			return err
		}
		result = authnext.StepUpResult{SessionID: sessionID, ExpiresAt: expiresAt}
		return repository.appendAudit(ctx, securityAudit(claims.PrincipalID, "step_up_completed", "success"))
	})
	if err != nil {
		return authnext.StepUpResult{}, err
	}
	if invalidCode {
		s.audit(ctx, claims.PrincipalID, "step_up", "denied")
		return authnext.StepUpResult{}, ErrInvalidMFA
	}
	return result, nil
}

func (s *Service) elevateSession(
	ctx context.Context,
	repository *Repository,
	principalID guid.ID,
	sessionHash string,
	now time.Time,
) (string, time.Time, error) {
	current, err := repository.activeSession(ctx, sessionHash, now)
	if err != nil || current.PrincipalID != principalID {
		return "", time.Time{}, ErrMFAChallenge
	}
	token, err := randomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	methods := strings.Fields(current.AMR)
	if !slices.Contains(methods, "mfa") {
		methods = append(methods, "mfa")
	}
	replacement := model.Session{
		IDHash: tokenHash(token), PrincipalID: principalID, CreatedAt: now, AuthTime: now,
		ACR: max(current.ACR, 2), AMR: strings.Join(methods, " "), LastSeenAt: now,
		ExpiresAt: now.Add(s.web.IdleTTL), AbsoluteExpiresAt: current.AbsoluteExpiresAt,
		IPAddress: current.IPAddress, UserAgent: current.UserAgent,
	}
	_, changed, err := repository.rotateSession(ctx, principalID, sessionHash, &replacement, now)
	if err != nil {
		return "", time.Time{}, err
	}
	if !changed {
		return "", time.Time{}, ErrMFAChallenge
	}
	return token, current.AbsoluteExpiresAt, nil
}
