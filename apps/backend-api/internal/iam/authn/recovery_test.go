package authn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/passwordx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type notificationRecorder struct {
	mu       sync.Mutex
	payloads []notificationPayload
	status   int
}

func (recorder *notificationRecorder) handler(response http.ResponseWriter, request *http.Request) {
	var payload notificationPayload
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	recorder.mu.Lock()
	recorder.payloads = append(recorder.payloads, payload)
	status := recorder.status
	recorder.mu.Unlock()
	if status == 0 {
		status = http.StatusNoContent
	}
	response.WriteHeader(status)
}

func (recorder *notificationRecorder) last(t *testing.T) notificationPayload {
	t.Helper()
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	require.NotEmpty(t, recorder.payloads)
	return recorder.payloads[len(recorder.payloads)-1]
}

func newRecoveryService(t *testing.T) (*Service, *authnRepositoryHarness, *notificationRecorder) {
	t.Helper()
	harness := newAuthnRepositoryHarness(t, "")
	recorder := &notificationRecorder{}
	server := httptest.NewServer(http.HandlerFunc(recorder.handler))
	t.Cleanup(server.Close)
	config := localAuthnConfig()
	config.Recovery = authnext.RecoveryConfig{
		Enabled: true, TokenTTL: 15 * time.Minute, Cooldown: 24 * time.Hour,
		ResetURL: "https://app.example.test/recover",
	}
	config.Notification = authnext.NotificationConfig{
		URL: server.URL, PollInterval: time.Second, RequestTimeout: time.Second, MaxAttempts: 3,
	}
	service, err := NewWebService(config, harness.repository)
	require.NoError(t, err)
	service.now = func() time.Time { return repositoryTestNow }
	return service, harness, recorder
}

func recoveryTokenFromPayload(t *testing.T, payload notificationPayload) string {
	t.Helper()
	target, err := url.Parse(payload.RecoveryURL)
	require.NoError(t, err)
	token := target.Query().Get("token")
	require.NotEmpty(t, token)
	return token
}

func TestPasswordRecoveryLifecycle(t *testing.T) {
	service, harness, recorder := newRecoveryService(t)
	principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	oldToken, _, err := service.IssueAccessToken(t.Context(), principalID, "", "")
	require.NoError(t, err)

	require.NoError(t, service.BeginPasswordRecovery(t.Context(), " ALICE@EXAMPLE.TEST "))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)
	assert.Equal(t, passwordRecoveryNotification, payload.Type)
	token := recoveryTokenFromPayload(t, payload)

	require.NoError(t, service.CompletePasswordRecovery(
		t.Context(), token, "different horse battery staple",
	))
	assert.ErrorIs(t, service.CompletePasswordRecovery(
		t.Context(), token, "another horse battery staple",
	), authnext.ErrInvalidRecovery)
	_, err = service.Authenticate(t.Context(), "", service.SessionCookie(login.SessionID))
	assert.ErrorIs(t, err, ErrInvalidSession)
	_, err = service.Authenticate(t.Context(), "Bearer "+oldToken, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken)
	_, err = service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	_, err = service.BeginLogin(t.Context(), "alice", "different horse battery staple", "")
	require.NoError(t, err)

	credential, err := harness.repository.getCredential(t.Context(), principalID)
	require.NoError(t, err)
	require.NotNil(t, credential.RecoveryCooldownUntil)
	assert.Equal(t, repositoryTestNow.Add(24*time.Hour), *credential.RecoveryCooldownUntil)
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	assert.Equal(t, passwordChangedNotification, recorder.last(t).Type)
}

func TestPasswordlessRecoveryOwnershipGateAndAuditRollback(t *testing.T) {
	service, harness, recorder := newRecoveryService(t)
	principalID := bootstrapLocalPrincipal(t, harness, "passwordless", "correct horse battery staple")
	require.NoError(t, harness.db.Where("principal_id = ?", principalID).Delete(&model.Credential{}).Error)
	require.NoError(t, service.BeginPasswordRecovery(t.Context(), "passwordless"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	token := recoveryTokenFromPayload(t, recorder.last(t))
	service.recoveryEligibility = func(context.Context, *gorm.DB, guid.ID) error { return authnext.ErrInvalidRecovery }
	assert.ErrorIs(t, service.CompletePasswordRecovery(t.Context(), token, "different horse battery staple"), authnext.ErrInvalidRecovery)
	_, err := service.repo.getCredential(t.Context(), principalID)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = service.repo.activePasswordRecovery(t.Context(), service.passwordRecoveryHMACs(token), service.now())
	require.NoError(t, err)
	service.recoveryEligibility = nil
	harness.auditErr = assert.AnError
	assert.ErrorIs(t, service.CompletePasswordRecovery(t.Context(), token, "different horse battery staple"), assert.AnError)
	_, err = service.repo.getCredential(t.Context(), principalID)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = service.repo.activePasswordRecovery(t.Context(), service.passwordRecoveryHMACs(token), service.now())
	require.NoError(t, err)
	harness.auditErr = nil
	require.NoError(t, service.CompletePasswordRecovery(t.Context(), token, "different horse battery staple"))
	_, err = service.BeginLogin(t.Context(), "passwordless", "different horse battery staple", "")
	require.NoError(t, err)
}

func TestPasswordRecoveryDoesNotIssueToEmailChangedAfterLookup(t *testing.T) {
	service, harness, recorder := newRecoveryService(t)
	principalID := bootstrapLocalPrincipal(t, harness, "email-race", "correct horse battery staple")
	lookup := service.repo.principals.ActiveHumanByIdentifier
	service.repo.principals.ActiveHumanByIdentifier = func(ctx context.Context, db *gorm.DB, identifier string) ([]identity.Principal, error) {
		principals, err := lookup(ctx, db, identifier)
		if err != nil {
			return nil, err
		}
		require.Len(t, principals, 1)
		require.NoError(t, harness.db.Table("iam_principals").Where("id = ?", principalID).
			Updates(map[string]any{"email": "replacement@example.test", "email_verified": true}).Error)
		return principals, nil
	}
	require.NoError(t, service.BeginPasswordRecovery(t.Context(), "email-race"))
	for _, row := range []any{&model.PasswordRecoveryToken{}, &model.NotificationOutbox{}} {
		var count int64
		require.NoError(t, harness.db.Model(row).Where("principal_id = ?", principalID).Count(&count).Error)
		require.Zero(t, count, "a stale mailbox lookup must issue neither a recovery token nor a notification")
	}
	service.repo.principals.ActiveHumanByIdentifier = lookup
	require.NoError(t, service.BeginPasswordRecovery(t.Context(), "email-race"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	require.Equal(t, "replacement@example.test", recorder.last(t).Recipient)
	token := recoveryTokenFromPayload(t, recorder.last(t))
	require.NoError(t, service.CompletePasswordRecovery(t.Context(), token, "different horse battery staple"))
}

func TestPasswordRecoveryDoesNotEnumeratePrincipals(t *testing.T) {
	service, harness, _ := newRecoveryService(t)
	bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	assert.NoError(t, service.BeginPasswordRecovery(t.Context(), "missing@example.test"))
	assert.ErrorIs(t, service.BeginPasswordRecovery(t.Context(), ""), authnext.ErrInvalidRecovery)

	disabled, err := NewWebService(authnext.Config{
		Captcha: authnext.CaptchaConfig{TestAnswer: testCaptchaAnswer}}, nil)
	require.NoError(t, err)
	assert.ErrorIs(t, disabled.BeginPasswordRecovery(t.Context(), "alice"), authnext.ErrRecoveryDisabled)
}

func TestPasswordRecoveryAuditFailureRollsBackSecurityMutation(t *testing.T) {
	service, harness, recorder := newRecoveryService(t)
	principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	token := recoveryTokenFromPayload(t, recorder.last(t))
	before, err := harness.repository.getCredential(t.Context(), principalID)
	require.NoError(t, err)

	harness.auditErr = errors.New("audit unavailable")
	err = service.CompletePasswordRecovery(t.Context(), token, "different horse battery staple")
	assert.ErrorContains(t, err, "audit unavailable")
	after, err := harness.repository.getCredential(t.Context(), principalID)
	require.NoError(t, err)
	assert.Equal(t, before.PasswordHash, after.PasswordHash)
	assert.Equal(t, before.CredentialVersion, after.CredentialVersion)
	recovery, err := harness.repository.activePasswordRecovery(
		t.Context(), service.passwordRecoveryHMACs(token), service.now(),
	)
	require.NoError(t, err)
	assert.Nil(t, recovery.ConsumedAt)

	harness.auditErr = nil
	require.NoError(t, service.CompletePasswordRecovery(t.Context(), token, "different horse battery staple"))
}

func TestNotificationDeliveryRetryAndWorkerLifecycle(t *testing.T) {
	service, harness, recorder := newRecoveryService(t)
	bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	recorder.status = http.StatusServiceUnavailable
	require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))

	rows, err := harness.repository.pendingNotificationIDs(
		t.Context(), repositoryTestNow.Add(3*time.Second), 20,
	)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	recorder.mu.Lock()
	recorder.status = http.StatusNoContent
	recorder.mu.Unlock()
	service.now = func() time.Time { return repositoryTestNow.Add(3 * time.Second) }
	require.NoError(t, service.deliverPendingNotifications(t.Context()))

	require.NoError(t, service.StartNotificationWorker(t.Context()))
	require.NoError(t, service.StartNotificationWorker(t.Context()))
	stopContext, cancel := contextWithTimeout(t, time.Second)
	defer cancel()
	require.NoError(t, service.StopNotificationWorker(stopContext))
	require.NoError(t, service.StopNotificationWorker(t.Context()))
}

func TestNotificationDeliveryRejectsRedirects(t *testing.T) {
	service, _, _ := newRecoveryService(t)
	var redirected bool
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected = true
	}))
	t.Cleanup(destination.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, destination.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)
	service.cfg.Notification.URL = redirector.URL
	require.NoError(t, service.configureNotification())

	err := service.postNotification(t.Context(), 1, []byte(`{"code":"123456"}`))
	assert.ErrorContains(t, err, "status 307")
	assert.False(t, redirected, "notification credentials must not be replayed to a redirect target")
}

func TestNotificationWorkerReportsPollFailures(t *testing.T) {
	service, harness, _ := newRecoveryService(t)
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	sqlDB, err := harness.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	service.deliverPendingNotificationsAndReport(t.Context())
	assert.Contains(t, output.String(), "authn notification poll failed")
	assert.Contains(t, output.String(), "database is closed")
}

func TestNotificationWorkerCanRestartWhilePreviousStopWaits(t *testing.T) {
	service, _, _ := newRecoveryService(t)
	oldDone := make(chan struct{})
	cancelCalled := make(chan struct{})
	var cancelOnce sync.Once
	service.notificationMu.Lock()
	service.notificationCancel = func() { cancelOnce.Do(func() { close(cancelCalled) }) }
	service.notificationDone = oldDone
	service.notificationMu.Unlock()

	stopResult := make(chan error, 1)
	go func() { stopResult <- service.StopNotificationWorker(t.Context()) }()
	<-cancelCalled
	require.NoError(t, service.StartNotificationWorker(t.Context()))
	service.notificationMu.Lock()
	newDone := service.notificationDone
	service.notificationMu.Unlock()
	require.NotNil(t, newDone)
	assert.NotEqual(t, oldDone, newDone)

	close(oldDone)
	require.NoError(t, <-stopResult)
	require.NoError(t, service.StopNotificationWorker(t.Context()))

	service.notificationMu.Lock()
	service.notificationCancel = func() {}
	service.notificationDone = make(chan struct{})
	service.notificationMu.Unlock()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, service.StopNotificationWorker(canceled), context.Canceled)
}

func TestPasswordRecoveryInvalidAndStorageStatesFailClosed(t *testing.T) {
	t.Run("invalid input and expired token", func(t *testing.T) {
		service, harness, recorder := newRecoveryService(t)
		bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		assert.ErrorIs(t, service.CompletePasswordRecovery(
			t.Context(), "invalid", "different horse battery staple",
		), authnext.ErrInvalidRecovery)
		require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
		require.NoError(t, service.deliverPendingNotifications(t.Context()))
		token := recoveryTokenFromPayload(t, recorder.last(t))
		service.now = func() time.Time { return repositoryTestNow.Add(service.cfg.Recovery.TokenTTL + time.Second) }
		assert.ErrorIs(t, service.CompletePasswordRecovery(
			t.Context(), token, "different horse battery staple",
		), authnext.ErrInvalidRecovery)
	})

	t.Run("first credential after mailbox proof", func(t *testing.T) {
		service, harness, recorder := newRecoveryService(t)
		principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
		require.NoError(t, service.deliverPendingNotifications(t.Context()))
		token := recoveryTokenFromPayload(t, recorder.last(t))
		require.NoError(t, harness.db.Table("iam_credentials").Where("principal_id = ?", principalID).Delete(nil).Error)
		require.NoError(t, service.CompletePasswordRecovery(
			t.Context(), token, "different horse battery staple",
		))
		credential, err := service.repo.getCredential(t.Context(), principalID)
		require.NoError(t, err)
		assert.Equal(t, int64(1), credential.CredentialVersion)
		valid, err := passwordx.Verify(credential.PasswordHash, "different horse battery staple")
		require.NoError(t, err)
		assert.True(t, valid)
		assert.ErrorIs(t, service.CompletePasswordRecovery(t.Context(), token, "another different horse battery staple"), authnext.ErrInvalidRecovery)
	})

	t.Run("corrupt password hash", func(t *testing.T) {
		service, harness, recorder := newRecoveryService(t)
		principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
		require.NoError(t, service.deliverPendingNotifications(t.Context()))
		token := recoveryTokenFromPayload(t, recorder.last(t))
		require.NoError(t, harness.db.Table("iam_credentials").Where("principal_id = ?", principalID).
			Update("password_hash", "invalid").Error)
		assert.ErrorContains(t, service.CompletePasswordRecovery(
			t.Context(), token, "different horse battery staple",
		), "verify recovery password history")
	})

	t.Run("password history unavailable", func(t *testing.T) {
		service, harness, recorder := newRecoveryService(t)
		bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
		require.NoError(t, service.deliverPendingNotifications(t.Context()))
		token := recoveryTokenFromPayload(t, recorder.last(t))
		require.NoError(t, harness.db.Exec("DROP TABLE iam_password_history").Error)
		assert.ErrorContains(t, service.CompletePasswordRecovery(
			t.Context(), token, "different horse battery staple",
		), "load recovery password history")
	})

	t.Run("principal verification changed", func(t *testing.T) {
		service, harness, recorder := newRecoveryService(t)
		principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
		require.NoError(t, service.deliverPendingNotifications(t.Context()))
		token := recoveryTokenFromPayload(t, recorder.last(t))
		require.NoError(t, harness.db.Table("iam_principals").Where("id = ?", principalID).
			Update("email_verified", false).Error)
		assert.ErrorIs(t, service.CompletePasswordRecovery(
			t.Context(), token, "different horse battery staple",
		), authnext.ErrInvalidRecovery)
	})

	t.Run("notification outbox unavailable", func(t *testing.T) {
		service, harness, _ := newRecoveryService(t)
		bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		require.NoError(t, harness.db.Exec("DROP TABLE iam_notification_outbox").Error)
		assert.ErrorContains(t, service.BeginPasswordRecovery(t.Context(), "alice"), "store password recovery request")
	})
}

func TestNotificationPermanentFailureClassification(t *testing.T) {
	t.Run("maximum attempts", func(t *testing.T) {
		service, harness, recorder := newRecoveryService(t)
		bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		service.cfg.Notification.MaxAttempts = 2
		recorder.status = http.StatusServiceUnavailable
		require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
		ids, err := harness.repository.pendingNotificationIDs(t.Context(), service.now(), 20)
		require.NoError(t, err)
		require.Len(t, ids, 1)
		assert.ErrorContains(t, service.deliverNotification(t.Context(), ids[0]), "status 503")
		service.now = func() time.Time { return repositoryTestNow.Add(3 * time.Second) }
		assert.ErrorContains(t, service.deliverNotification(t.Context(), ids[0]), "status 503")
		var row model.NotificationOutbox
		require.NoError(t, harness.db.Table(row.TableName()).Where("id = ?", ids[0]).Take(&row).Error)
		assert.Equal(t, "failed", row.Status)
		assert.Equal(t, 2, row.Attempts)
	})

	t.Run("corrupt ciphertext", func(t *testing.T) {
		service, harness, _ := newRecoveryService(t)
		bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
		ids, err := harness.repository.pendingNotificationIDs(t.Context(), service.now(), 20)
		require.NoError(t, err)
		require.Len(t, ids, 1)
		require.NoError(t, harness.db.Table(model.NotificationOutbox{}.TableName()).Where("id = ?", ids[0]).
			Update("payload_ciphertext", "v1.%").Error)
		assert.Error(t, service.deliverNotification(t.Context(), ids[0]))
		var row model.NotificationOutbox
		require.NoError(t, harness.db.Table(row.TableName()).Where("id = ?", ids[0]).Take(&row).Error)
		assert.Equal(t, "failed", row.Status)
	})

	t.Run("missing client", func(t *testing.T) {
		service, harness, _ := newRecoveryService(t)
		bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
		ids, err := harness.repository.pendingNotificationIDs(t.Context(), service.now(), 20)
		require.NoError(t, err)
		require.Len(t, ids, 1)
		service.notificationClient = nil
		assert.ErrorContains(t, service.deliverNotification(t.Context(), ids[0]), "not configured")
	})
}

func contextWithTimeout(t *testing.T, timeout time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(t.Context(), timeout)
}

// TestNotificationDeliveryFailuresAreLoggedAndBatchContinues pins the two
// properties of the delivery loop: a per-notification failure is observable
// rather than silently swallowed, and it does not abandon the rest of the batch.
func TestNotificationDeliveryFailuresAreLoggedAndBatchContinues(t *testing.T) {
	service, harness, recorder := newRecoveryService(t)
	bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	bootstrapLocalPrincipal(t, harness, "bob", "correct horse battery staple")

	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	recorder.mu.Lock()
	recorder.status = http.StatusServiceUnavailable
	recorder.mu.Unlock()
	require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
	require.NoError(t, service.BeginPasswordRecovery(t.Context(), "bob"))

	require.NoError(t, service.deliverPendingNotifications(t.Context()))

	recorder.mu.Lock()
	attempts := len(recorder.payloads)
	recorder.mu.Unlock()
	assert.Equal(t, 2, attempts, "a failed notification must not abandon the rest of the batch")

	logged := output.String()
	assert.Contains(t, logged, "authn notification delivery failed")
	assert.Contains(t, logged, "notification_id")
	assert.Equal(t, 2, strings.Count(logged, "authn notification delivery failed"))
}

// TestNotificationDeliveryStopsOnCancelledContext keeps cancellation shutting
// the batch down promptly instead of logging one warning per queued row.
func TestNotificationDeliveryStopsOnCancelledContext(t *testing.T) {
	service, harness, recorder := newRecoveryService(t)
	bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	bootstrapLocalPrincipal(t, harness, "bob", "correct horse battery staple")
	require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice"))
	require.NoError(t, service.BeginPasswordRecovery(t.Context(), "bob"))

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, service.deliverPendingNotifications(canceled), context.Canceled)

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	assert.Empty(t, recorder.payloads)
}

// TestPasswordRecoveryStorageFaultIsNotAnInvalidRecovery covers the re-read of
// the principal inside the reset transaction. The same principal is read twice
// before it: once to check the recovery, once to lock the row.
func TestPasswordRecoveryStorageFaultIsNotAnInvalidRecovery(t *testing.T) {
	service, harness, recorder := newRecoveryService(t)
	bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	require.NoError(t, service.BeginPasswordRecovery(t.Context(), "alice@example.test"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	token := recoveryTokenFromPayload(t, recorder.last(t))

	failReadsAfter(t, harness.db, "iam_principals", 2)
	err := service.CompletePasswordRecovery(t.Context(), token, "different horse battery staple")
	require.Error(t, err)
	assert.NotErrorIs(t, err, authnext.ErrInvalidRecovery)
}
