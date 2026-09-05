package authn

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	identitymodel "7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/passwordx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newVerificationService(t *testing.T) (*Service, *authnRepositoryHarness, *notificationRecorder) {
	t.Helper()
	harness := newAuthnRepositoryHarness(t, "")
	recorder := &notificationRecorder{}
	server := httptest.NewServer(http.HandlerFunc(recorder.handler))
	t.Cleanup(server.Close)
	config := localAuthnConfig()
	config.EmailVerification = authnext.EmailVerificationConfig{
		Enabled: true, TokenTTL: time.Hour, VerifyURL: "https://app.example.test/verify",
	}
	config.Notification = authnext.NotificationConfig{
		URL: server.URL, PollInterval: time.Second, RequestTimeout: time.Second, MaxAttempts: 3,
	}
	service, err := NewWebService(config, harness.repository)
	require.NoError(t, err)
	service.now = func() time.Time { return repositoryTestNow }
	return service, harness, recorder
}

func seedUnverifiedPrincipal(t *testing.T, harness *authnRepositoryHarness, id guid.ID, login string) {
	t.Helper()
	hash, err := passwordx.Hash("correct horse battery staple")
	require.NoError(t, err)
	require.NoError(t, harness.db.Table(harness.repository.table("iam_principals")).Create(&identitymodel.Principal{
		ID: id, LoginName: login, Email: login + "@example.test", DisplayName: login,
		Status: "active", CreatedAt: repositoryTestNow, UpdatedAt: repositoryTestNow,
	}).Error)
	require.NoError(t, harness.db.Table(harness.repository.table("iam_credentials")).Create(&model.Credential{
		PrincipalID: id, PasswordHash: hash, PasswordChangedAt: repositoryTestNow,
		CredentialVersion: 1, UpdatedAt: repositoryTestNow,
	}).Error)
}

func verificationTokenFromPayload(t *testing.T, payload notificationPayload) string {
	t.Helper()
	target, err := url.Parse(payload.VerificationURL)
	require.NoError(t, err)
	token := target.Query().Get("token")
	require.NotEmpty(t, token)
	return token
}

func TestEmailVerificationLifecycleByCode(t *testing.T) {
	service, harness, recorder := newVerificationService(t)
	principalID := guid.ID(501)
	seedUnverifiedPrincipal(t, harness, principalID, "alice")
	login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	cookie := service.SessionCookie(login.SessionID)

	require.NoError(t, service.BeginEmailVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)
	assert.Equal(t, emailVerificationNotification, payload.Type)
	require.Len(t, payload.Code, 6)
	assert.NotEmpty(t, verificationTokenFromPayload(t, payload))

	var hookedID guid.ID
	service.verifiedHook = func(_ context.Context, _ *gorm.DB, id guid.ID, email string, _ time.Time) error {
		hookedID = id
		assert.Equal(t, "alice@example.test", email)
		return nil
	}
	require.NoError(t, service.CompleteEmailVerification(t.Context(), "", payload.Recipient, payload.Code))
	assert.Equal(t, principalID, hookedID)
	principal, err := harness.repository.getPrincipal(t.Context(), principalID)
	require.NoError(t, err)
	assert.True(t, principal.EmailVerified)
	assert.False(t, principal.ActivationRequired)
	assert.ErrorIs(t, service.CompleteEmailVerification(
		t.Context(), "", payload.Recipient, payload.Code,
	), authnext.ErrInvalidEmailVerification)
}

func TestEmailVerificationHookFailureRollsBackCompletion(t *testing.T) {
	service, harness, recorder := newVerificationService(t)
	principalID := guid.ID(511)
	seedUnverifiedPrincipal(t, harness, principalID, "alice")
	login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	require.NoError(t, service.BeginEmailVerification(
		t.Context(), "", service.SessionCookie(login.SessionID),
	))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	token := verificationTokenFromPayload(t, recorder.last(t))

	service.verifiedHook = func(context.Context, *gorm.DB, guid.ID, string, time.Time) error {
		return errors.New("tenant bootstrap unavailable")
	}
	err = service.CompleteEmailVerification(t.Context(), token, "", "")
	assert.ErrorContains(t, err, "tenant bootstrap unavailable")
	principal, err := harness.repository.getPrincipal(t.Context(), principalID)
	require.NoError(t, err)
	assert.False(t, principal.EmailVerified)
	_, err = harness.repository.activeEmailVerificationByHMAC(
		t.Context(), service.emailVerificationHMACs(token), service.now(),
	)
	require.NoError(t, err, "hook failure must leave the verification credential retryable")

	service.verifiedHook = nil
	require.NoError(t, service.CompleteEmailVerification(t.Context(), token, "", ""))
}

func TestEmailVerificationTokenIsSnapshotBoundAndAuditAtomic(t *testing.T) {
	service, harness, recorder := newVerificationService(t)
	principalID := guid.ID(502)
	seedUnverifiedPrincipal(t, harness, principalID, "alice")
	login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	cookie := service.SessionCookie(login.SessionID)
	require.NoError(t, service.BeginEmailVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	token := verificationTokenFromPayload(t, recorder.last(t))

	harness.auditErr = errors.New("audit unavailable")
	err = service.CompleteEmailVerification(t.Context(), token, "", "")
	assert.ErrorContains(t, err, "audit unavailable")
	principal, err := harness.repository.getPrincipal(t.Context(), principalID)
	require.NoError(t, err)
	assert.False(t, principal.EmailVerified)
	_, err = harness.repository.activeEmailVerificationByHMAC(
		t.Context(), service.emailVerificationHMACs(token), service.now(),
	)
	require.NoError(t, err, "audit failure must roll back token consumption")

	harness.auditErr = nil
	require.NoError(t, harness.db.Table(harness.repository.table("iam_principals")).
		Where("id = ?", principalID).Update("email", "changed@example.test").Error)
	assert.ErrorIs(t, service.CompleteEmailVerification(t.Context(), token, "", ""), authnext.ErrInvalidEmailVerification)
}

func TestEmailVerificationCodeAttemptsAreCredentialBoundAndPersisted(t *testing.T) {
	service, harness, recorder := newVerificationService(t)
	principalID := guid.ID(507)
	seedUnverifiedPrincipal(t, harness, principalID, "alice")
	login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	require.NoError(t, service.BeginEmailVerification(
		t.Context(), "", service.SessionCookie(login.SessionID),
	))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)
	token := verificationTokenFromPayload(t, payload)
	assert.ErrorIs(t, service.CompleteEmailVerification(
		t.Context(), "", "", "",
	), authnext.ErrInvalidEmailVerification)
	assert.ErrorIs(t, service.CompleteEmailVerification(
		t.Context(), "", "", payload.Code,
	), authnext.ErrInvalidEmailVerification)
	assert.ErrorIs(t, service.CompleteEmailVerification(
		t.Context(), "", payload.Recipient, "ABCDEF",
	), authnext.ErrInvalidEmailVerification)

	for attempt := range maxCodeAttempts {
		wrongCode := fmt.Sprintf("%06d", attempt)
		if wrongCode == payload.Code {
			wrongCode = "999999"
		}
		assert.ErrorIs(t, service.CompleteEmailVerification(
			t.Context(), "", strings.ToUpper(payload.Recipient), wrongCode,
		), authnext.ErrInvalidEmailVerification)
	}
	verification, err := harness.repository.activeEmailVerificationByHMAC(
		t.Context(), service.emailVerificationHMACs(token), service.now(),
	)
	require.NoError(t, err)
	assert.Equal(t, maxCodeAttempts, verification.Attempts)
	assert.ErrorIs(t, service.CompleteEmailVerification(
		t.Context(), "", payload.Recipient, payload.Code,
	), authnext.ErrInvalidEmailVerification)

	restarted, err := NewWebService(service.cfg, harness.repository)
	require.NoError(t, err)
	restarted.now = service.now
	assert.ErrorIs(t, restarted.CompleteEmailVerification(
		t.Context(), "", payload.Recipient, payload.Code,
	), authnext.ErrInvalidEmailVerification, "attempt limits must survive process restart")
}

func TestEmailVerificationConfigurationAndAlreadyVerifiedNoop(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")
	config := localAuthnConfig()
	config.EmailVerification = authnext.EmailVerificationConfig{Enabled: true, TokenTTL: time.Minute}
	config.Notification.URL = "http://127.0.0.1"
	_, err := NewWebService(config, harness.repository)
	assert.ErrorContains(t, err, "security limits")

	service, harness, recorder := newVerificationService(t)
	bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	require.NoError(t, service.BeginEmailVerification(t.Context(), "", service.SessionCookie(login.SessionID)))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	recorder.mu.Lock()
	assert.Empty(t, recorder.payloads)
	recorder.mu.Unlock()
}

func TestEmailVerificationAuthenticationAndStorageFailures(t *testing.T) {
	t.Run("principal authentication required", func(t *testing.T) {
		service, _, _ := newVerificationService(t)
		assert.ErrorIs(t, service.BeginEmailVerification(t.Context(), "", ""), ErrInvalidSession)
		serviceToken, _, err := service.IssueSubjectToken("worker", "", "", "Worker", "")
		require.NoError(t, err)
		assert.ErrorIs(t, service.BeginEmailVerification(
			t.Context(), "Bearer "+serviceToken, "",
		), ErrInvalidSession)
	})

	t.Run("primary email required", func(t *testing.T) {
		service, harness, _ := newVerificationService(t)
		principalID := guid.ID(503)
		seedUnverifiedPrincipal(t, harness, principalID, "alice")
		require.NoError(t, harness.db.Table("iam_principals").Where("id = ?", principalID).
			Update("email", "").Error)
		login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		require.NoError(t, err)
		assert.ErrorIs(t, service.BeginEmailVerification(
			t.Context(), "", service.SessionCookie(login.SessionID),
		), authnext.ErrEmailRequired)
	})

	t.Run("verification table unavailable on begin", func(t *testing.T) {
		service, harness, _ := newVerificationService(t)
		principalID := guid.ID(504)
		seedUnverifiedPrincipal(t, harness, principalID, "alice")
		login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_email_verification_tokens").Error)
		assert.ErrorContains(t, service.BeginEmailVerification(
			t.Context(), "", service.SessionCookie(login.SessionID),
		), "begin email verification")
	})

	t.Run("notification failure rolls back replacement", func(t *testing.T) {
		service, harness, recorder := newVerificationService(t)
		principalID := guid.ID(505)
		seedUnverifiedPrincipal(t, harness, principalID, "alice")
		login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		require.NoError(t, err)
		cookie := service.SessionCookie(login.SessionID)
		require.NoError(t, service.BeginEmailVerification(t.Context(), "", cookie))
		require.NoError(t, service.deliverPendingNotifications(t.Context()))
		original := verificationTokenFromPayload(t, recorder.last(t))
		require.NoError(t, harness.db.Exec("DROP TABLE iam_notification_outbox").Error)
		assert.ErrorContains(t, service.BeginEmailVerification(t.Context(), "", cookie), "begin email verification")
		_, err = harness.repository.activeEmailVerificationByHMAC(
			t.Context(), service.emailVerificationHMACs(original), service.now(),
		)
		require.NoError(t, err, "failed replacement must retain the prior credential")
	})

	t.Run("verification table unavailable on completion", func(t *testing.T) {
		service, harness, recorder := newVerificationService(t)
		principalID := guid.ID(506)
		seedUnverifiedPrincipal(t, harness, principalID, "alice")
		login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		require.NoError(t, err)
		require.NoError(t, service.BeginEmailVerification(
			t.Context(), "", service.SessionCookie(login.SessionID),
		))
		require.NoError(t, service.deliverPendingNotifications(t.Context()))
		token := verificationTokenFromPayload(t, recorder.last(t))
		require.NoError(t, harness.db.Exec("DROP TABLE iam_email_verification_tokens").Error)
		assert.ErrorContains(t, service.CompleteEmailVerification(t.Context(), token, "", ""),
			"load email verification credential")
	})
}
