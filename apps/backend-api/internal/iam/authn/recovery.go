package authn

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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
	resetURL, err := credentialURL(s.cfg.Recovery.ResetURL, token)
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
		if err := repository.createPasswordRecovery(ctx, &recovery, now); err != nil {
			return err
		}
		return s.enqueueNotification(ctx, repository, notification)
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

func (s *Service) CompletePasswordRecovery(ctx context.Context, token, newPassword string) error {
	if !s.RecoveryEnabled() {
		return authnext.ErrRecoveryDisabled
	}
	if !strings.HasPrefix(token, recoveryTokenPrefix) || len(token) > 128 ||
		len(newPassword) < 12 || len(newPassword) > 1024 {
		return authnext.ErrInvalidRecovery
	}
	now := s.now().UTC()
	computedHMAC := s.passwordRecoveryHMAC(token)
	recovery, err := s.repo.activePasswordRecovery(ctx, computedHMAC, now)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authnext.ErrInvalidRecovery
	}
	if err != nil {
		return fmt.Errorf("load password recovery credential: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(recovery.TokenHMAC), []byte(computedHMAC)) != 1 {
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
	if err != nil {
		return fmt.Errorf("load recovery credential: %w", err)
	}
	history, err := s.repo.listPasswordHistory(ctx, recovery.PrincipalID, 5)
	if err != nil {
		return fmt.Errorf("load recovery password history: %w", err)
	}
	hashes := make([]string, 0, len(history)+1)
	hashes = append(hashes, credential.PasswordHash)
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
		consumed, err := repository.consumePasswordRecovery(ctx, computedHMAC, now)
		if err != nil {
			return err
		}
		if !consumed {
			return authnext.ErrInvalidRecovery
		}
		if err := repository.appendPasswordHistory(ctx, recovery.PrincipalID, credential.PasswordHash, now); err != nil {
			return err
		}
		if err := repository.trimPasswordHistory(ctx, recovery.PrincipalID, passwordHistoryDepth); err != nil {
			return err
		}
		cooldownUntil := now.Add(s.cfg.Recovery.Cooldown)
		changed, err := repository.updatePassword(
			ctx, recovery.PrincipalID, credential.PasswordHash, newHash, now, true, &cooldownUntil,
		)
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
			if err := s.enqueueNotification(ctx, repository, notification); err != nil {
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

func (s *Service) passwordRecoveryHMAC(token string) string {
	mac := hmac.New(sha256.New, s.mfaPurposeKey("password-recovery:v1"))
	_, _ = mac.Write([]byte("platform:password-recovery:v1\x00" + token))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) enqueueNotification(
	ctx context.Context,
	repository *Repository,
	payload notificationPayload,
) error {
	id, err := repository.nextID()
	if err != nil {
		return err
	}
	payload.ID = id
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode notification: %w", err)
	}
	ciphertext, err := s.encryptAuthnData("notification:v1", id.String(), encoded)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	row := model.NotificationOutbox{
		ID: id, Kind: payload.Type, Recipient: payload.Recipient, PayloadCiphertext: ciphertext,
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
	payload, err := s.decryptAuthnData("notification:v1", row.ID.String(), row.PayloadCiphertext)
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
