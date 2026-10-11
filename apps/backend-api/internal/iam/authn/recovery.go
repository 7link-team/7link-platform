package authn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/passwordx"
	"7link.com/platform/pkg/extension/secretx"
	"gorm.io/gorm"
)

const (
	passwordRecoveryNotification = "password_recovery"
	passwordChangedNotification  = "password_changed"
	recoveryTokenPrefix          = "cpr1_"
)

// RecoveryEligibilityConnector lets federation exclude unproven legacy recovery
// addresses without making authn read federation-owned tables.
type RecoveryEligibilityConnector struct {
	Bind func(func(context.Context, *gorm.DB, guid.ID) error) error
}

type notificationPayload struct {
	ID              guid.ID   `json:"id"`
	Type            string    `json:"type"`
	Recipient       string    `json:"recipient"`
	RecoveryURL     string    `json:"recovery_url,omitempty"`
	VerificationURL string    `json:"verification_url,omitempty"`
	Code            string    `json:"code,omitempty"`
	OccurredAt      time.Time `json:"occurred_at"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
}

// notificationCipher labels the sealed outbox payload. It is a constant rather
// than a literal at each call site because a rewrap has to name the same label
// to open what this path sealed, and a typo there would report every queued
// notification as unreadable instead of failing.
const notificationCipher = "notification:v1"

func (s *Service) configureNotification() error {
	if !s.NotificationEnabled() {
		return nil
	}
	if !s.web.Enabled || s.cfg.Notification.PollInterval > time.Minute ||
		s.cfg.Notification.RequestTimeout > time.Minute || s.cfg.Notification.MaxAttempts > 20 {
		return errors.New("authn notification configuration exceeds security limits")
	}
	if err := validateAuthnURL("notification.url", s.cfg.Notification.URL); err != nil {
		return err
	}
	authorization, err := secretx.Resolve(
		"authn.notification.authorization",
		s.cfg.Notification.Authorization,
		s.cfg.Notification.AuthorizationFile,
		4096,
	)
	if err != nil {
		return err
	}
	s.notificationAuthorization = authorization
	s.notificationClient = &http.Client{
		Timeout: s.cfg.Notification.RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return nil
}

func (s *Service) configureRecovery() error {
	if !s.cfg.Recovery.Enabled {
		return nil
	}
	if !s.web.Enabled || s.cfg.Recovery.TokenTTL < 5*time.Minute || s.cfg.Recovery.TokenTTL > time.Hour ||
		s.cfg.Recovery.Cooldown < time.Hour || s.cfg.Recovery.Cooldown > 7*24*time.Hour {
		return errors.New("authn recovery configuration exceeds security limits")
	}
	return validateAuthnURL("recovery.reset_url", s.cfg.Recovery.ResetURL)
}

func validateAuthnURL(name, value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("authn %s must be an absolute URL without credentials or fragment", name)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	if parsed.Scheme == "http" && (strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()) {
		return nil
	}
	return fmt.Errorf("authn %s must use HTTPS except on loopback", name)
}

func (s *Service) NotificationEnabled() bool {
	return s != nil && s.cfg.Enabled && s.web.Enabled && s.repo != nil && s.repo.writer() != nil &&
		(s.cfg.Recovery.Enabled || s.cfg.EmailVerification.Enabled)
}

func (s *Service) RecoveryEnabled() bool {
	return s != nil && s.cfg.Enabled && s.web.Enabled && s.cfg.Recovery.Enabled &&
		s.repo != nil && s.repo.writer() != nil
}

// BeginPasswordRecovery intentionally returns nil for missing, ambiguous,
// disabled, and notification-ineligible principals to prevent enumeration.
func (s *Service) BeginPasswordRecovery(ctx context.Context, identifier string) error {
	return s.BeginPasswordRecoveryForEntry(ctx, identifier, "")
}

func (s *Service) BeginPasswordRecoveryForEntry(ctx context.Context, identifier, entry string) error {
	if !s.RecoveryEnabled() {
		return authnext.ErrRecoveryDisabled
	}
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	if identifier == "" || len(identifier) > 320 {
		return authnext.ErrInvalidRecovery
	}
	principal, err := s.repo.findRecoveryPrincipal(ctx, identifier)
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, errAmbiguousRecoveryPrincipal) ||
		err == nil && principal.Email == "" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("begin password recovery: %w", err)
	}
	secret, err := randomToken(32)
	if err != nil {
		return fmt.Errorf("generate password recovery credential: %w", err)
	}
	token := recoveryTokenPrefix + secret
	now := s.now().UTC()
	expiresAt := now.Add(s.cfg.Recovery.TokenTTL)
	resetURL, err := publicEntranceCredentialURL(s.cfg.Recovery.ResetURL, token, entry)
	if err != nil {
		return err
	}
	recovery := model.PasswordRecoveryToken{
		TokenHMAC: s.passwordRecoveryHMAC(token), PrincipalID: principal.ID,
		CreatedAt: now, ExpiresAt: expiresAt,
	}
	notification := notificationPayload{
		Type: passwordRecoveryNotification, Recipient: principal.Email,
		RecoveryURL: resetURL, OccurredAt: now, ExpiresAt: expiresAt,
	}
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		current, err := repository.lockPrincipal(ctx, principal.ID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// Email changes invalidate tokens under the same principal lock. A stale
		// lookup must not enqueue a new token to the old mailbox afterward.
		if !current.EmailVerified || current.Email != principal.Email {
			return nil
		}
		if err := repository.createPasswordRecovery(ctx, &recovery, now); err != nil {
			return err
		}
		return s.enqueueNotification(ctx, repository, principal.ID, notification)
	})
	if err != nil {
		return fmt.Errorf("store password recovery request: %w", err)
	}
	return nil
}

func credentialURL(base, token string) (string, error) {
	target, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("build credential URL: %w", err)
	}
	query := target.Query()
	query.Set("token", token)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func publicEntranceCredentialURL(base, token, entry string) (string, error) {
	if entry != "" && entry != "user" && entry != "tenant" {
		return "", authnext.ErrInvalidRegistration
	}
	target, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if entry != "" {
		prefix := "/user"
		if entry == "tenant" {
			prefix = "/admin"
		}
		for _, page := range []string{"/recover", "/verify-email"} {
			if target.Path == page || target.Path == "/user"+page || target.Path == "/admin"+page {
				target.Path = prefix + page
				break
			}
		}
		query := target.Query()
		query.Set("entry", entry)
		target.RawQuery = query.Encode()
	}
	return credentialURL(target.String(), token)
}

func (s *Service) CompletePasswordRecovery(ctx context.Context, token, newPassword string) error {
	if !s.RecoveryEnabled() {
		return authnext.ErrRecoveryDisabled
	}
	if !strings.HasPrefix(token, recoveryTokenPrefix) || len(token) > 128 ||
		len(newPassword) < 12 || len(newPassword) > 1024 {
		return authnext.ErrInvalidRecovery
	}
	now := s.now().UTC()
	recovery, err := s.repo.activePasswordRecovery(ctx, s.passwordRecoveryHMACs(token), now)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authnext.ErrInvalidRecovery
	}
	if err != nil {
		return fmt.Errorf("load password recovery credential: %w", err)
	}
	if !s.passwordRecoveryMatches(token, recovery.TokenHMAC) {
		return authnext.ErrInvalidRecovery
	}
	principal, err := s.repo.getActivePrincipal(ctx, recovery.PrincipalID)
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && !principal.EmailVerified {
		return authnext.ErrInvalidRecovery
	}
	if err != nil {
		return fmt.Errorf("load recovery principal: %w", err)
	}
	credential, err := s.repo.getCredential(ctx, recovery.PrincipalID)
	missingCredential := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !missingCredential {
		return fmt.Errorf("load recovery credential: %w", err)
	}
	history, err := s.repo.listPasswordHistory(ctx, recovery.PrincipalID, 5)
	if err != nil {
		return fmt.Errorf("load recovery password history: %w", err)
	}
	hashes := make([]string, 0, len(history)+1)
	if !missingCredential && credential.PasswordHash != "" {
		hashes = append(hashes, credential.PasswordHash)
	}
	for _, row := range history {
		hashes = append(hashes, row.PasswordHash)
	}
	for _, encoded := range hashes {
		reused, verifyErr := passwordx.Verify(encoded, newPassword)
		if verifyErr != nil {
			return fmt.Errorf("verify recovery password history: %w", verifyErr)
		}
		if reused {
			return authnext.ErrPasswordReused
		}
	}
	newHash, err := passwordx.Hash(newPassword)
	if err != nil {
		return err
	}
	notification := notificationPayload{
		Type: passwordChangedNotification, Recipient: principal.Email, OccurredAt: now,
	}
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		if _, err := repository.lockPrincipal(ctx, recovery.PrincipalID); err != nil {
			return err
		}
		current, err := repository.getActivePrincipal(ctx, recovery.PrincipalID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err != nil || !current.EmailVerified || current.Email != principal.Email {
			return authnext.ErrInvalidRecovery
		}
		if s.recoveryEligibility != nil {
			if err := s.recoveryEligibility(ctx, repository.writer(), recovery.PrincipalID); err != nil {
				return err
			}
		}
		consumed, err := repository.consumePasswordRecovery(ctx, recovery.TokenHMAC, now)
		if err != nil {
			return err
		}
		if !consumed {
			return authnext.ErrInvalidRecovery
		}
		if !missingCredential && credential.PasswordHash != "" {
			if err := repository.appendPasswordHistory(ctx, recovery.PrincipalID, credential.PasswordHash, now); err != nil {
				return err
			}
		}
		if err := repository.trimPasswordHistory(ctx, recovery.PrincipalID, passwordHistoryDepth); err != nil {
			return err
		}
		cooldownUntil := now.Add(s.cfg.Recovery.Cooldown)
		changed := false
		if missingCredential {
			err = repository.createCredential(ctx, &model.Credential{PrincipalID: recovery.PrincipalID,
				PasswordHash: newHash, CredentialVersion: 1, PasswordChangedAt: now, UpdatedAt: now, RecoveryCooldownUntil: &cooldownUntil})
			changed = err == nil
		} else {
			changed, err = repository.updatePassword(ctx, recovery.PrincipalID, credential.PasswordHash, newHash, now, true, &cooldownUntil)
		}
		if err != nil {
			return err
		}
		if !changed {
			return authnext.ErrInvalidRecovery
		}
		if err := repository.revokeOtherAuthentication(ctx, recovery.PrincipalID, "", now); err != nil {
			return err
		}
		if principal.Email != "" {
			if err := s.enqueueNotification(ctx, repository, principal.ID, notification); err != nil {
				return err
			}
		}
		event := securityAudit(recovery.PrincipalID, "password_recovery_completed", "success")
		event.Detail["sessions_revoked"] = true
		event.Detail["credential_version_incremented"] = true
		return repository.appendAudit(ctx, event)
	})
	if errors.Is(err, authnext.ErrInvalidRecovery) {
		return err
	}
	if err != nil {
		return fmt.Errorf("complete password recovery: %w", err)
	}
	return nil
}

// passwordRecoveryHMAC derives the value to store for a freshly issued token,
// under the key that is currently sealing.
func (s *Service) passwordRecoveryHMAC(token string) string {
	return s.mfaKeys.MAC("password-recovery:v1", passwordRecoveryMessage(token))
}

// passwordRecoveryHMACs derives what a presented token would have been stored
// as under each key the ring holds, because the stored value is the column the
// row is found by. A recovery mail already in someone's inbox when the key
// changed still works.
func (s *Service) passwordRecoveryHMACs(token string) []string {
	return s.mfaKeys.MACs("password-recovery:v1", passwordRecoveryMessage(token))
}

func (s *Service) passwordRecoveryMatches(token, stored string) bool {
	return s.mfaKeys.MACMatches("password-recovery:v1", passwordRecoveryMessage(token), stored)
}

func passwordRecoveryMessage(token string) []byte {
	return []byte("platform:password-recovery:v1\x00" + token)
}

func (s *Service) enqueueNotification(
	ctx context.Context,
	repository *Repository,
	principalID guid.ID,
	payload notificationPayload,
) error {
	if principalID <= 0 {
		return errors.New("notification requires a principal")
	}
	id, err := repository.nextID()
	if err != nil {
		return err
	}
	payload.ID = id
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode notification: %w", err)
	}
	ciphertext, err := s.encryptAuthnData(notificationCipher, id.String(), encoded)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	row := model.NotificationOutbox{
		PrincipalID: &principalID,
		ID:          id, Kind: payload.Type, Recipient: payload.Recipient, PayloadCiphertext: ciphertext,
		Status: "pending", AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.enqueueNotification(ctx, &row); err != nil {
		return fmt.Errorf("enqueue notification: %w", err)
	}
	return nil
}

func (s *Service) StartNotificationWorker(ctx context.Context) error {
	if !s.NotificationEnabled() {
		return nil
	}
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()
	if s.notificationCancel != nil {
		return nil
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.notificationCancel = cancel
	s.notificationDone = done
	go s.runNotificationWorker(workerCtx, done)
	return nil
}

func (s *Service) StopNotificationWorker(ctx context.Context) error {
	s.notificationMu.Lock()
	cancel, done := s.notificationCancel, s.notificationDone
	s.notificationCancel, s.notificationDone = nil, nil
	s.notificationMu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) runNotificationWorker(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	s.deliverPendingNotificationsAndReport(ctx)
	ticker := time.NewTicker(s.cfg.Notification.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.deliverPendingNotificationsAndReport(ctx)
		}
	}
}

func (s *Service) deliverPendingNotificationsAndReport(ctx context.Context) {
	if err := s.deliverPendingNotifications(ctx); err != nil && ctx.Err() == nil {
		s.notificationPollFailures.Add(1)
		slog.ErrorContext(ctx, "authn notification poll failed", "error", err)
	}
}

func (s *Service) deliverPendingNotifications(ctx context.Context) error {
	now := s.now().UTC()
	if err := s.repo.resetStaleNotifications(ctx, now.Add(-2*s.cfg.Notification.RequestTimeout)); err != nil {
		return fmt.Errorf("reset stale notifications: %w", err)
	}
	ids, err := s.repo.pendingNotificationIDs(ctx, now, 20)
	if err != nil {
		return fmt.Errorf("list pending notifications: %w", err)
	}
	for _, id := range ids {
		if err := s.deliverNotification(ctx, id); err != nil {
			// A canceled context ends the batch; anything else is specific to
			// one notification, so record it and keep draining the queue.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.WarnContext(ctx, "authn notification delivery failed", "notification_id", id, "error", err)
		}
	}
	return nil
}

func (s *Service) deliverNotification(ctx context.Context, id guid.ID) error {
	now := s.now().UTC()
	row, claimed, err := s.repo.claimNotification(ctx, id, now)
	if err != nil || !claimed {
		return err
	}
	payload, err := s.decryptAuthnData(notificationCipher, row.ID.String(), row.PayloadCiphertext)
	if err == nil {
		err = s.postNotification(ctx, row.ID, payload)
	}
	if err == nil {
		updated, updateErr := s.repo.markNotificationSent(ctx, row.ID, row.Attempts, now)
		if updateErr != nil {
			return updateErr
		}
		if !updated {
			return errors.New("notification delivery lease lost before completion")
		}
		return nil
	}
	status := "pending"
	if row.Attempts >= s.cfg.Notification.MaxAttempts || errors.Is(err, errUnsupportedAuthnCiphertext) ||
		errors.Is(err, errInvalidAuthnCiphertext) || errors.Is(err, errDecryptAuthenticationPayload) {
		status = "failed"
	}
	retryAt := s.now().UTC().Add(time.Duration(1<<min(row.Attempts, 8)) * s.cfg.Notification.PollInterval)
	message := err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	updated, updateErr := s.repo.markNotificationFailed(
		ctx, row.ID, row.Attempts, status, message, retryAt, now,
	)
	if updateErr != nil {
		return updateErr
	}
	if !updated {
		return errors.New("notification delivery lease lost before failure update")
	}
	if status == "failed" {
		s.notificationTerminalFailures.Add(1)
	} else {
		s.notificationRetryableFailures.Add(1)
	}
	return err
}

func (s *Service) postNotification(ctx context.Context, id guid.ID, payload []byte) error {
	if s.notificationClient == nil {
		return errors.New("notification client is not configured")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.Notification.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create notification request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", id.String())
	if s.notificationAuthorization != "" {
		request.Header.Set("Authorization", s.notificationAuthorization)
	}
	response, err := s.notificationClient.Do(request)
	if err != nil {
		return fmt.Errorf("deliver notification: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("deliver notification: status %d", response.StatusCode)
	}
	return nil
}
