package authn

import (
	"context"
	"errors"
	"testing"
	"time"

	identitymodel "7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func directCreatorForHarness(h *authnRepositoryHarness) DirectRegistrationPrincipalCreator {
	return func(ctx context.Context, db *gorm.DB, login, email, hash, name string, now time.Time, ids guid.Source) (guid.ID, error) {
		id, err := registrationCreatorForHarness(h)(ctx, db, login, hash, name, now, ids)
		if err != nil {
			return 0, err
		}
		err = db.Table(h.runtime.TablePrefix()+"iam_principals").Where("id = ?", id).Updates(map[string]any{"email": email, "activation_required": false}).Error
		return id, err
	}
}

func newDirectRegistrationService(t *testing.T) (*Service, *authnRepositoryHarness) {
	t.Helper()
	h := newAuthnRepositoryHarness(t, "direct_")
	cfg := localAuthnConfig()
	cfg.Registration.Enabled = true
	s, err := NewWebService(cfg, h.repository, WithDirectRegistrationPrincipalCreator(directCreatorForHarness(h)))
	require.NoError(t, err)
	return s, h
}

func TestDirectRegistrationNeedsNoEmailAndGrantsNoMailboxProof(t *testing.T) {
	s, h := newDirectRegistrationService(t)
	const password = "correct registration password"
	require.True(t, s.Capabilities().Registration)
	require.False(t, s.Capabilities().RegistrationEmailVerification)
	require.NoError(t, s.RegisterAccountForEntry(t.Context(), " AccountName ", "", password, "Account", "user"))
	var principal identitymodel.Principal
	require.NoError(t, h.db.Table(h.runtime.TablePrefix()+"iam_principals").Where("login_name = ?", "accountname").First(&principal).Error)
	assert.False(t, principal.ActivationRequired)
	assert.False(t, principal.EmailVerified)
	assert.Empty(t, principal.Email)
	_, err := s.BeginLogin(t.Context(), "accountname", password, "")
	require.NoError(t, err)
	require.NoError(t, s.RegisterAccountForEntry(t.Context(), "other", "other@example.test", password, "", "user"))
	var other identitymodel.Principal
	require.NoError(t, h.db.Table(h.runtime.TablePrefix()+"iam_principals").Where("login_name = ?", "other").First(&other).Error)
	assert.False(t, other.EmailVerified)
	assert.False(t, other.ActivationRequired)
	assert.Equal(t, "other@example.test", other.Email)
	assert.ErrorIs(t, s.RegisterAccountForEntry(t.Context(), "accountname", "", "replacement password", "", ""), authnext.ErrRegistrationConflict)
	_, err = s.BeginLogin(t.Context(), "accountname", password, "")
	require.NoError(t, err, "duplicate signup must not replace a directly activated account credential")
	for _, table := range []string{"iam_email_verification_tokens", "iam_notification_outbox", "iam_tenant_members"} {
		var count int64
		require.NoError(t, h.db.Table(h.runtime.TablePrefix()+table).Count(&count).Error)
		assert.Zero(t, count, table)
	}
}

func TestDirectRegistrationHTTPAndMandatoryVerificationCannotBeBypassed(t *testing.T) {
	s, _ := newDirectRegistrationService(t)
	_, api := humatest.New(t)
	RegisterHTTP(api, s, s)
	body := registrationRequestWithCaptcha(t, api, "", "correct registration password", "Account")
	delete(body, "email")
	body["login_name"] = "http-account"
	response := api.Post("/registrations", "Origin: https://app.example.test", body)
	require.Equal(t, 202, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"verification_required":false`)
	verified, _, _ := newRegistrationService(t)
	assert.ErrorIs(t, verified.RegisterAccountForEntry(t.Context(), "username", "", "correct registration password", "", ""), authnext.ErrInvalidRegistration)
	assert.ErrorIs(t, verified.RegisterAccountForEntry(t.Context(), "username", "email@example.test", "correct registration password", "", ""), authnext.ErrInvalidRegistration)
}

func TestRegistrationDefaultDoesNotPreventWebDisabledStartup(t *testing.T) {
	h := newAuthnRepositoryHarness(t, "no_web_")
	cfg := localAuthnConfig()
	cfg.Web.Enabled = false
	cfg.Registration.Enabled = true
	s, err := NewWebService(cfg, h.repository)
	require.NoError(t, err)
	assert.False(t, s.Capabilities().Registration)
}

func TestDirectRegistrationValidationAndAuditRollback(t *testing.T) {
	s, h := newDirectRegistrationService(t)
	for _, input := range []struct{ login, email, password string }{
		{"", "", "correct registration password"},
		{"account", "invalid-email", "correct registration password"},
		{"account", "", "short"},
	} {
		assert.ErrorIs(t, s.RegisterAccountForEntry(t.Context(), input.login, input.email, input.password, "", ""), authnext.ErrInvalidRegistration)
	}
	h.auditErr = errors.New("audit unavailable")
	require.ErrorContains(t, s.RegisterAccountForEntry(t.Context(), "rollback", "", "correct registration password", "", ""), "audit unavailable")
	var count int64
	require.NoError(t, h.db.Table(h.runtime.TablePrefix()+"iam_principals").Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, h.db.Table(h.runtime.TablePrefix()+"iam_credentials").Count(&count).Error)
	assert.Zero(t, count)
}
