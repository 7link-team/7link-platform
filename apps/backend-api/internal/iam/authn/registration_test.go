package authn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		if err := db.WithContext(ctx).Table(harness.runtime.TablePrefix()+"iam_principals").
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
		if err := db.WithContext(ctx).Table(harness.runtime.TablePrefix() + "iam_principals").Create(&identitymodel.Principal{
			ID: principalID, LoginName: email, Email: email, DisplayName: displayName,
			Status: "active", ActivationRequired: true, SelfRegistered: true, CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			return 0, err
		}
		if err := db.WithContext(ctx).Table(harness.runtime.TablePrefix() + "iam_credentials").Create(&model.Credential{
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
	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
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
	require.NoError(t, service.CompleteEmailVerification(t.Context(), token, "", "", password))
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

// A pending principal proves nothing about who owns its address, so the
// password in force is the latest registrant's, and only a link issued for that
// password can activate it. Neither registrant can hold the account the owner
// of the address verifies with someone else's credential.
func TestReRegistrationOfPendingAddressReplacesTheCredential(t *testing.T) {
	const (
		email    = "owner@example.com"
		attacker = "attacker registration password"
		owner    = "owner registration password"
	)
	register := func(t *testing.T, service *Service, recorder *notificationRecorder, password string) notificationPayload {
		t.Helper()
		require.NoError(t, service.Register(t.Context(), email, password, "Registrant"))
		require.NoError(t, service.deliverPendingNotifications(t.Context()))
		return recorder.last(t)
	}
	principalOf := func(t *testing.T, harness *authnRepositoryHarness) identitymodel.Principal {
		t.Helper()
		var principals []identitymodel.Principal
		require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
			Where("login_name = ?", email).Find(&principals).Error)
		require.Len(t, principals, 1)
		return principals[0]
	}

	t.Run("owner registers after a pre-registration", func(t *testing.T) {
		service, harness, recorder := newRegistrationService(t)
		preRegistered := register(t, service, recorder, attacker)
		// Within the resend cooldown: a new credential is never held back by it.
		ownerMail := register(t, service, recorder, owner)
		require.NotEqual(t, preRegistered.VerificationURL, ownerMail.VerificationURL)

		assert.ErrorIs(t, service.CompleteEmailVerification(t.Context(), verificationTokenFromPayload(t, preRegistered), "", "", attacker),
			authnext.ErrInvalidEmailVerification, "a link issued for the replaced credential must not activate")
		assert.ErrorIs(t, service.CompleteEmailVerification(t.Context(), "", email, preRegistered.Code, attacker),
			authnext.ErrInvalidEmailVerification, "the replaced link's code must not activate either")
		assert.True(t, principalOf(t, harness).ActivationRequired)

		require.NoError(t, service.CompleteEmailVerification(t.Context(), verificationTokenFromPayload(t, ownerMail), "", "", owner))
		principal := principalOf(t, harness)
		assert.False(t, principal.ActivationRequired)
		_, err := service.BeginLogin(t.Context(), email, owner, "")
		require.NoError(t, err)
		_, err = service.BeginLogin(t.Context(), email, attacker, "")
		assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	})

	t.Run("a later registration withdraws the earlier link", func(t *testing.T) {
		service, harness, recorder := newRegistrationService(t)
		ownerMail := register(t, service, recorder, owner)
		register(t, service, recorder, attacker)
		assert.ErrorIs(t, service.CompleteEmailVerification(t.Context(), verificationTokenFromPayload(t, ownerMail), "", "", owner),
			authnext.ErrInvalidEmailVerification, "the owner's link must not activate the attacker's credential")
		assert.True(t, principalOf(t, harness).ActivationRequired)
	})

	t.Run("repeating the same password keeps the cooldown and the link", func(t *testing.T) {
		service, harness, recorder := newRegistrationService(t)
		first := register(t, service, recorder, owner)
		require.NoError(t, service.Register(t.Context(), email, owner, "Registrant"))
		var outstanding int64
		require.NoError(t, harness.db.Table("iam_email_verification_tokens").
			Where("email = ? AND consumed_at IS NULL", email).Count(&outstanding).Error)
		assert.Equal(t, int64(1), outstanding)
		require.NoError(t, service.CompleteEmailVerification(t.Context(), verificationTokenFromPayload(t, first), "", "", owner))
		_, err := service.BeginLogin(t.Context(), email, owner, "")
		require.NoError(t, err)
	})

	t.Run("credential storage failure answers as a failure", func(t *testing.T) {
		service, harness, recorder := newRegistrationService(t)
		register(t, service, recorder, owner)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_credentials").Error)
		assert.ErrorContains(t, service.Register(t.Context(), email, attacker, "Registrant"), "load pending registration credential")
	})

	t.Run("an activated address is never re-credentialed", func(t *testing.T) {
		service, _, recorder := newRegistrationService(t)
		ownerMail := register(t, service, recorder, owner)
		require.NoError(t, service.CompleteEmailVerification(t.Context(), verificationTokenFromPayload(t, ownerMail), "", "", owner))
		// The HTTP contract answers this conflict with the same 202 as a new address.
		assert.ErrorIs(t, service.Register(t.Context(), email, attacker, "Registrant"), authnext.ErrRegistrationConflict)
		_, err := service.BeginLogin(t.Context(), email, owner, "")
		require.NoError(t, err)
		_, err = service.BeginLogin(t.Context(), email, attacker, "")
		assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	})
}

// Following a verification link proves the mailbox, not who chose the
// password. Activating a pending registration therefore also needs that
// registration's password, and a refusal looks exactly like a spent link and
// leaves the link usable for the registrant.
func TestRegistrationActivationRequiresTheRegistrationPassword(t *testing.T) {
	const (
		email    = "owner@example.com"
		password = "registrant registration password"
	)
	service, harness, recorder := newRegistrationService(t)
	require.NoError(t, service.Register(t.Context(), email, password, "Registrant"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)
	token := verificationTokenFromPayload(t, payload)
	pending := func() {
		t.Helper()
		var principals []identitymodel.Principal
		require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
			Where("login_name = ?", email).Find(&principals).Error)
		require.Len(t, principals, 1)
		assert.True(t, principals[0].ActivationRequired)
		assert.False(t, principals[0].EmailVerified)
		_, err := harness.repository.activeEmailVerificationByHMAC(t.Context(), service.emailVerificationHMACs(token), service.now())
		require.NoError(t, err, "a refused activation consumes nothing")
	}

	for _, wrong := range []string{"", "owner of the mailbox guesses", password + " "} {
		assert.ErrorIs(t, service.CompleteEmailVerification(t.Context(), token, "", "", wrong),
			authnext.ErrInvalidEmailVerification, "link with password %q", wrong)
		assert.ErrorIs(t, service.CompleteEmailVerification(t.Context(), "", email, payload.Code, wrong),
			authnext.ErrInvalidEmailVerification, "code with password %q", wrong)
		pending()
	}
	assert.ErrorIs(t, service.CompleteEmailVerification(t.Context(), "cpe1_not-a-link", "", "", password),
		authnext.ErrInvalidEmailVerification, "a wrong link answers the same")
	assert.ErrorIs(t, service.CompleteEmailVerification(t.Context(), token, "", "", string(make([]byte, 1025))),
		authnext.ErrInvalidEmailVerification, "an oversized password is refused before hashing")
	pending()

	require.NoError(t, service.CompleteEmailVerification(t.Context(), "", email, payload.Code, password))
	_, err := service.BeginLogin(t.Context(), email, password, "")
	require.NoError(t, err)
}

// Only public self-registration creates a principal public registration may
// re-credential. A pending principal a directory created answers like an
// existing account: same response, no new credential and no link.
func TestRegistrationNeverReCredentialsADirectoryPendingPrincipal(t *testing.T) {
	const email = "provisioned@example.com"
	service, harness, recorder := newRegistrationService(t)
	now := repositoryTestNow
	principalID := guid.ID(8801)
	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").Create(&identitymodel.Principal{
		ID: principalID, LoginName: email, Email: email, DisplayName: "Provisioned",
		Status: "active", ActivationRequired: true, CreatedAt: now, UpdatedAt: now,
	}).Error)
	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_credentials").Create(&model.Credential{
		PrincipalID: principalID, PasswordHash: "directory-managed", PasswordChangedAt: now,
		CredentialVersion: 1, UpdatedAt: now,
	}).Error)

	err := service.Register(t.Context(), email, "attacker registration password", "Attacker")
	require.ErrorIs(t, err, authnext.ErrRegistrationConflict, "the HTTP contract answers this with the same 202 as a new address")
	credential, err := harness.repository.getCredential(t.Context(), principalID)
	require.NoError(t, err)
	assert.Equal(t, "directory-managed", credential.PasswordHash)
	assert.Equal(t, int64(1), credential.CredentialVersion)
	var links int64
	require.NoError(t, harness.db.Table("iam_email_verification_tokens").Where("principal_id = ?", principalID).Count(&links).Error)
	assert.Zero(t, links)
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	recorder.mu.Lock()
	assert.Empty(t, recorder.payloads)
	recorder.mu.Unlock()

	// Control: the same row marked as a self-registration is re-credentialed.
	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
		Where("id = ?", principalID).Update("self_registered", true).Error)
	require.NoError(t, service.Register(t.Context(), email, "attacker registration password", "Attacker"))
	credential, err = harness.repository.getCredential(t.Context(), principalID)
	require.NoError(t, err)
	assert.NotEqual(t, "directory-managed", credential.PasswordHash)
}

func TestRegistrationEntranceSurvivesVerifiedEmailDelivery(t *testing.T) {
	service, _, recorder := newRegistrationService(t)
	require.NoError(t, service.RegisterForEntry(t.Context(), "tenant@example.test", "correct registration password", "Tenant", "tenant"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)
	target, err := url.Parse(payload.VerificationURL)
	require.NoError(t, err)
	assert.Equal(t, "tenant", target.Query().Get("entry"))
	require.NoError(t, service.CompleteEmailVerification(t.Context(), verificationTokenFromPayload(t, payload), "", "", "correct registration password"))
	_, err = publicEntranceCredentialURL("https://app.example.test/verify", "secret", "system")
	assert.Error(t, err, "registration continuation must not accept privileged or arbitrary destinations")
}

func TestPublicEntranceCredentialsUseViewRoutes(t *testing.T) {
	for _, item := range []struct{ base, entry, path string }{
		{"https://app.example.test/verify-email", "tenant", "/admin/verify-email"},
		{"https://app.example.test/user/verify-email", "tenant", "/admin/verify-email"},
		{"https://app.example.test/recover", "user", "/user/recover"},
		{"https://app.example.test/admin/recover", "user", "/user/recover"},
		{"https://app.example.test/custom-reset", "tenant", "/custom-reset"},
	} {
		t.Run(item.base+"/"+item.entry, func(t *testing.T) {
			value, err := publicEntranceCredentialURL(item.base, "credential", item.entry)
			require.NoError(t, err)
			target, err := url.Parse(value)
			require.NoError(t, err)
			assert.Equal(t, item.path, target.Path)
			assert.Equal(t, "credential", target.Query().Get("token"))
			assert.Equal(t, item.entry, target.Query().Get("entry"))
		})
	}
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
		require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+table).Count(&count).Error)
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
