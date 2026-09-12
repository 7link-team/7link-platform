package authn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	identitymodel "7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func registrationCreatorForHarness(harness *authnRepositoryHarness) RegistrationPrincipalCreator {
	return func(
		ctx context.Context,
		db *gorm.DB,
		email, passwordHash, displayName string,
		now time.Time,
		ids guid.Source,
	) (guid.ID, error) {
		var count int64
		if err := db.WithContext(ctx).Table(harness.repository.table("iam_principals")).
			Where("login_name = ?", email).Count(&count).Error; err != nil {
			return 0, err
		}
		if count != 0 {
			return 0, authnext.ErrRegistrationConflict
		}
		rawID, err := ids.Next()
		if err != nil || rawID <= 0 {
			return 0, errors.New("generate registration principal id")
		}
		principalID := guid.ID(rawID)
		if err := db.WithContext(ctx).Table(harness.repository.table("iam_principals")).Create(&identitymodel.Principal{
			ID: principalID, LoginName: email, Email: email, DisplayName: displayName,
			Status: "active", ActivationRequired: true, CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			return 0, err
		}
		if err := db.WithContext(ctx).Table(harness.repository.table("iam_credentials")).Create(&model.Credential{
			PrincipalID: principalID, PasswordHash: passwordHash, PasswordChangedAt: now,
			CredentialVersion: 1, UpdatedAt: now,
		}).Error; err != nil {
			return 0, err
		}
		return principalID, nil
	}
}

func newRegistrationService(t *testing.T) (*Service, *authnRepositoryHarness, *notificationRecorder) {
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
	config.Registration.Enabled = true
	service, err := NewWebService(
		config, harness.repository,
		WithRegistrationPrincipalCreator(registrationCreatorForHarness(harness)),
	)
	require.NoError(t, err)
	service.now = func() time.Time { return repositoryTestNow }
	return service, harness, recorder
}

func TestRegistrationLifecycle(t *testing.T) {
	service, harness, recorder := newRegistrationService(t)
	const password = "correct registration password"
	require.NoError(t, service.Register(t.Context(), " USER@example.com ", password, "Registered User"))
	require.NoError(t, service.Register(t.Context(), "user@example.com", password, "Duplicate"))

	var principals []identitymodel.Principal
	require.NoError(t, harness.db.Table(harness.repository.table("iam_principals")).
		Where("login_name = ?", "user@example.com").Find(&principals).Error)
	require.Len(t, principals, 1)
	principal := principals[0]
	assert.True(t, principal.ActivationRequired)
	assert.False(t, principal.EmailVerified)
	assert.Equal(t, "Registered User", principal.DisplayName)
	_, err := service.BeginLogin(t.Context(), principal.LoginName, password, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)

	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)
	token := verificationTokenFromPayload(t, payload)
	require.NoError(t, service.CompleteEmailVerification(t.Context(), token, "", ""))
	principalAfter := harness.principal(t, principal.ID)
	assert.False(t, principalAfter.ActivationRequired)
	assert.True(t, principalAfter.EmailVerified)
	_, err = service.BeginLogin(t.Context(), principal.LoginName, password, "")
	require.NoError(t, err)

	var requested int
	for _, event := range harness.audits {
		if event.EventType == "principal_registration_requested" {
			requested++
		}
	}
	assert.Equal(t, 1, requested)
}

func TestRegistrationValidationAndConfiguration(t *testing.T) {
	assert.Equal(t, authnext.Capabilities{}, (*Service)(nil).Capabilities())
	disabled, err := NewWebService(authnext.Config{}, nil)
	require.NoError(t, err)
	assert.ErrorIs(t, disabled.Register(t.Context(), "user@example.com", "correct registration password", "User"), authnext.ErrRegistrationDisabled)

	service, _, _ := newRegistrationService(t)
	assert.True(t, service.Capabilities().Registration)
	assert.ErrorIs(t, service.Register(t.Context(), "not-an-email", "correct registration password", "User"), authnext.ErrInvalidRegistration)
	assert.ErrorIs(t, service.Register(t.Context(), "user@example.com", "short", "User"), authnext.ErrInvalidRegistration)
	assert.ErrorIs(t, service.Register(t.Context(), "user@example.com", "12345678901", "User"), authnext.ErrInvalidRegistration)
}

func TestRegistrationAuditFailureRollsBackAllOwnedRows(t *testing.T) {
	service, harness, _ := newRegistrationService(t)
	harness.auditErr = errors.New("audit unavailable")
	err := service.Register(t.Context(), "user@example.com", "correct registration password", "User")
	assert.ErrorContains(t, err, "audit unavailable")

	for _, table := range []string{"iam_principals", "iam_credentials", "iam_email_verification_tokens", "iam_notification_outbox"} {
		var count int64
		require.NoError(t, harness.db.Table(harness.repository.table(table)).Count(&count).Error)
		assert.Zero(t, count, table)
	}
}

func TestRegistrationResendConfigurationAndStorageFailures(t *testing.T) {
	t.Run("configuration and invalid creator result", func(t *testing.T) {
		service, harness, _ := newRegistrationService(t)
		service.registrationCreator = nil
		assert.ErrorContains(t, service.configureRegistration(), "identity creator")
		service.cfg.Registration.Enabled = false
		require.NoError(t, service.configureRegistration())
		service.cfg.Registration.Enabled = true
		service.registrationCreator = func(
			context.Context, *gorm.DB, string, string, string, time.Time, guid.Source,
		) (guid.ID, error) {
			return 0, authnext.ErrInvalidRegistration
		}
		assert.ErrorIs(t, service.Register(
			t.Context(), "user@example.com", "correct registration password", "User",
		), authnext.ErrInvalidRegistration)
		service.registrationCreator = registrationCreatorForHarness(harness)
		service.cfg.EmailVerification.VerifyURL = "://invalid"
		assert.Error(t, service.Register(
			t.Context(), "user@example.com", "correct registration password", "User",
		))
	})

	t.Run("resend after cooldown", func(t *testing.T) {
		service, harness, _ := newRegistrationService(t)
		const email = "user@example.com"
		require.NoError(t, service.Register(
			t.Context(), email, "correct registration password", "User",
		))
		service.now = func() time.Time { return repositoryTestNow.Add(registrationVerificationCooldown + time.Second) }
		require.NoError(t, service.Register(
			t.Context(), email, "correct registration password", "Duplicate",
		))
		var count int64
		require.NoError(t, harness.db.Table("iam_email_verification_tokens").
			Where("email = ? AND consumed_at IS NULL", email).Count(&count).Error)
		assert.Equal(t, int64(1), count)
	})

	for _, table := range []string{"iam_email_verification_tokens", "iam_notification_outbox"} {
		t.Run(table, func(t *testing.T) {
			service, harness, _ := newRegistrationService(t)
			require.NoError(t, harness.db.Exec("DROP TABLE "+table).Error)
			err := service.Register(
				t.Context(), table+"@example.com", "correct registration password", "User",
			)
			assert.ErrorContains(t, err, "register principal")
			var count int64
			require.NoError(t, harness.db.Table("iam_principals").
				Where("email = ?", table+"@example.com").Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}
