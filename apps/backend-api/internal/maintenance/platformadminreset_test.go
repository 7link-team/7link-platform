package maintenance

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetTestPassword is obviously not real key material. Like the bootstrap
// password it only has to satisfy the length rule.
const resetTestPassword = "not-a-real-replacement-password"

// newPlatformAdminResetConfig reuses the bootstrap configuration so both
// commands are exercised against the same deployment shape. Only the request
// half differs, which is the point: the reset is not a second application.
func newPlatformAdminResetConfig(t *testing.T, dsn string) PlatformAdminResetConfig {
	t.Helper()
	bootstrap := newPlatformAdminConfig(t, dsn)
	return PlatformAdminResetConfig{
		AppConfig:                 bootstrap.AppConfig,
		PlatformAdminResetRequest: PlatformAdminResetRequest{Login: bootstrap.Login},
	}
}

// TestPlatformAdminPasswordResetRestoresAccessToTheBootstrappedAdministrator is
// the end-to-end contract: the one account no in-band recovery can rescue is
// recoverable by somebody who can run a command on the server.
func TestPlatformAdminPasswordResetRestoresAccessToTheBootstrappedAdministrator(t *testing.T) {
	dsn := newPlatformAdminDSN(t)
	bootstrap := newPlatformAdminConfig(t, dsn)
	migratePlatformAdminDatabase(t, bootstrap)
	created, err := RunPlatformAdminBootstrap(t.Context(), bootstrap, bootstrapTestPassword)
	require.NoError(t, err)

	report, err := RunPlatformAdminPasswordReset(
		t.Context(), newPlatformAdminResetConfig(t, dsn), resetTestPassword)
	require.NoError(t, err)
	assert.Equal(t, PlatformAdminPasswordReset, report.Outcome)
	assert.Equal(t, "admin", report.Login)
	assert.Equal(t, created.PrincipalID, report.PrincipalID,
		"the reset must act on the administrator that exists, never create a second one")

	rerun, err := RunPlatformAdminBootstrap(t.Context(), bootstrap, bootstrapTestPassword)
	require.NoError(t, err)
	assert.Equal(t, PlatformAdminAlreadyBootstrapped, rerun.Outcome,
		"a reset leaves exactly one administrator behind")
}

// TestPlatformAdminPasswordResetRefusesAnAdministratorThatWasNeverCreated
// keeps the operator pointed at the bootstrap command instead of at a
// permissions investigation.
func TestPlatformAdminPasswordResetRefusesAnAdministratorThatWasNeverCreated(t *testing.T) {
	config := newPlatformAdminResetConfig(t, newPlatformAdminDSN(t))
	migratePlatformAdminDatabase(t, PlatformAdminConfig{AppConfig: config.AppConfig})

	_, err := RunPlatformAdminPasswordReset(t.Context(), config, resetTestPassword)
	require.ErrorIs(t, err, ErrPlatformAdminNotBootstrapped)
	assert.NotErrorIs(t, err, ErrPlatformAdminConflict, "an absent administrator is not a conflict")
}

// TestPlatformAdminPasswordResetReportsAnUnmigratedSchemaAsItsOwnOutcome keeps
// the reset from blaming the administrator for a database that was never
// brought up to date.
func TestPlatformAdminPasswordResetReportsAnUnmigratedSchemaAsItsOwnOutcome(t *testing.T) {
	config := newPlatformAdminResetConfig(t, newPlatformAdminDSN(t))

	_, err := RunPlatformAdminPasswordReset(t.Context(), config, resetTestPassword)
	require.ErrorIs(t, err, ErrPlatformAdminSchemaNotReady)
	assert.NotErrorIs(t, err, ErrPlatformAdminNotBootstrapped)
}

// TestPlatformAdminPasswordResetValidatesBeforeTouchingTheDatabase proves an
// operator typo costs nothing: no connection, no lock, no half-open
// transaction.
func TestPlatformAdminPasswordResetValidatesBeforeTouchingTheDatabase(t *testing.T) {
	base := newPlatformAdminResetConfig(t, newPlatformAdminDSN(t))

	t.Run("blank login", func(t *testing.T) {
		config := base
		config.Login = "   "
		_, err := RunPlatformAdminPasswordReset(t.Context(), config, resetTestPassword)
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrPlatformAdminSchemaNotReady, "the login is rejected before any schema check")
	})

	t.Run("short password", func(t *testing.T) {
		config := base
		_, err := RunPlatformAdminPasswordReset(t.Context(), config, "short")
		require.ErrorIs(t, err, iamauthn.ErrBootstrapPassword)
		assert.NotErrorIs(t, err, ErrPlatformAdminSchemaNotReady)
	})
}

// TestPlatformAdminPasswordResetReportsUnavailableWithoutIAM proves a
// deployment that never enabled IAM gets its own outcome rather than a
// resolution panic.
func TestPlatformAdminPasswordResetReportsUnavailableWithoutIAM(t *testing.T) {
	config := newPlatformAdminResetConfig(t, newPlatformAdminDSN(t))
	migratePlatformAdminDatabase(t, PlatformAdminConfig{AppConfig: config.AppConfig})
	config.Authn.Enabled = false

	_, err := RunPlatformAdminPasswordReset(t.Context(), config, resetTestPassword)
	assert.ErrorIs(t, err, ErrPlatformAdminUnavailable)
}

// --- decision-level tests -------------------------------------------------

// activeAdministrator is the only principal shape a reset will act on, so every
// refusal test below is this one with a single property taken away.
func activeAdministrator() identity.Principal {
	return identity.Principal{ID: 7500, LoginName: "admin", Status: platformAdminActiveStatus}
}

func fullAdministrators() []core.PlatformAdministrator {
	return []core.PlatformAdministrator{{PrincipalID: 7500, FullAdministrator: true}}
}

// TestResetDecisionWritesAnAuditEventNamingTheAdministrator keeps the reset
// traceable. A password change on the account that can lock everybody else out
// is the first thing an auditor would ask about.
func TestResetDecisionWritesAnAuditEventNamingTheAdministrator(t *testing.T) {
	stub := &stubBootstrapStores{
		principal: activeAdministrator(), principalFound: true,
		administrators: fullAdministrators(), resetReplaced: true,
	}
	report, err := stub.stores(nil).resetPassword(
		t.Context(), nil, "admin", resetTestPassword, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, PlatformAdminPasswordReset, report.Outcome)
	assert.EqualValues(t, 7500, report.PrincipalID)

	require.Len(t, stub.appended, 1)
	event := stub.appended[0]
	assert.Equal(t, "platform_administrator_password_reset", event.EventType)
	assert.Equal(t, "principal", event.TargetType)
	assert.EqualValues(t, 7500, event.TargetID)
	assert.EqualValues(t, platformAuditTenant, event.TenantID)
	assert.Equal(t, "admin", event.Detail["login_name"])
	assert.Equal(t, true, event.Detail["credential_existed"])
	assert.Equal(t, true, event.Detail["sessions_revoked"])
	assert.NotContains(t, event.Detail, "password", "the audit trail must not carry the credential")
}

// TestResetDecisionRepairsAnAdministratorHoldingNoCredential covers the state
// the bootstrap refuses to touch. Leaving it unrepairable would mean a
// half-created administrator is permanent.
func TestResetDecisionRepairsAnAdministratorHoldingNoCredential(t *testing.T) {
	stub := &stubBootstrapStores{
		principal: activeAdministrator(), principalFound: true,
		administrators: fullAdministrators(), resetReplaced: false,
	}
	report, err := stub.stores(nil).resetPassword(
		t.Context(), nil, "admin", resetTestPassword, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, PlatformAdminPasswordReset, report.Outcome)

	require.Len(t, stub.appended, 1)
	assert.Equal(t, false, stub.appended[0].Detail["credential_existed"],
		"the audit trail is the only record of whether this was a rotation or a repair")
	assert.Equal(t, false, stub.appended[0].Detail["sessions_revoked"])
}

// TestResetDecisionRefusesAnythingButTheActiveFullAdministrator is the safety
// property the command exists under: it is a break-glass path for one account,
// not a password reset for any account somebody can name.
func TestResetDecisionRefusesAnythingButTheActiveFullAdministrator(t *testing.T) {
	for name, stub := range map[string]*stubBootstrapStores{
		"login belongs to an ordinary principal": {
			principal: activeAdministrator(), principalFound: true,
		},
		"login belongs to another administrator": {
			principal: activeAdministrator(), principalFound: true,
			administrators: []core.PlatformAdministrator{{PrincipalID: 9999, FullAdministrator: true}},
		},
		"administrator is only partially privileged": {
			principal: activeAdministrator(), principalFound: true,
			administrators: []core.PlatformAdministrator{{PrincipalID: 7500, FullAdministrator: false}},
		},
		"administrator is disabled": {
			principal:      identity.Principal{ID: 7500, LoginName: "admin", Status: "disabled"},
			principalFound: true, administrators: fullAdministrators(),
		},
		"administrator is locked": {
			principal:      identity.Principal{ID: 7500, LoginName: "admin", Status: "locked"},
			principalFound: true, administrators: fullAdministrators(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := stub.stores(nil).resetPassword(
				t.Context(), nil, "admin", resetTestPassword, time.Now().UTC())
			assert.ErrorIs(t, err, ErrPlatformAdminConflict)
			assert.Empty(t, stub.appended, "a refusal must write no audit event")
		})
	}
}

// TestResetDecisionSurfacesEveryStoreFailureDistinctly keeps a storage fault
// from being reported as a refusal, which would send an operator looking for a
// privilege problem that does not exist.
func TestResetDecisionSurfacesEveryStoreFailureDistinctly(t *testing.T) {
	fault := errors.New("store is down")
	for name, stub := range map[string]*stubBootstrapStores{
		"principal lookup": {findErr: fault},
		"administrator list": {
			principal: activeAdministrator(), principalFound: true, listErr: fault,
		},
		"credential reset": {
			principal: activeAdministrator(), principalFound: true,
			administrators: fullAdministrators(), resetErr: fault,
		},
		"credential repair": {
			principal: activeAdministrator(), principalFound: true,
			administrators: fullAdministrators(), credCreateErr: fault,
		},
		"audit append": {
			principal: activeAdministrator(), principalFound: true,
			administrators: fullAdministrators(), resetReplaced: true, appendErr: fault,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := stub.stores(nil).resetPassword(
				t.Context(), nil, "admin", resetTestPassword, time.Now().UTC())
			require.ErrorIs(t, err, fault)
			assert.NotErrorIs(t, err, ErrPlatformAdminConflict, "a fault is not a conflict")
			assert.NotErrorIs(t, err, ErrPlatformAdminNotBootstrapped)
		})
	}
}

// TestResetRequestCarriesNoSecretMaterial is enforced structurally rather than
// by review, for the same reason the bootstrap request is: anything in this
// struct is reachable from a config file, an environment variable, and the
// config inspector endpoint.
func TestResetRequestCarriesNoSecretMaterial(t *testing.T) {
	request := reflect.TypeOf(PlatformAdminResetRequest{})
	for index := range request.NumField() {
		field := request.Field(index)
		assert.NotEqual(t, "Password", field.Name, "the password is an argument, not configuration")
		if strings.Contains(strings.ToLower(field.Name), "password") {
			assert.Equal(t, "PasswordFile", field.Name, "only a path to the password may be configured")
		}
	}
}
