package maintenance

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/app"
	iamaudit "7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// bootstrapTestPassword is obviously not real key material; the bootstrap only
// checks length, and the hash never leaves the credential store.
const bootstrapTestPassword = "not-a-real-bootstrap-password"

// newPlatformAdminConfig builds the production-shaped configuration the
// bootstrap command requires. The command deliberately reuses the service
// configuration, so authentication must be configured for it to run at all.
func newPlatformAdminConfig(t *testing.T, dsn string) PlatformAdminConfig {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	key := base64.RawStdEncoding.EncodeToString(seed)
	return PlatformAdminConfig{
		AppConfig: app.AppConfig{
			Name: "platform-admin-bootstrap-test", Version: "1", Timezone: "UTC",
			// The command reuses the service configuration wholesale, so the
			// listen address is still required even though nothing listens.
			// The real CLI fills these from flag defaults; a struct literal
			// has to state them.
			HttpServer: app.HttpServer{Address: "0.0.0.0", Port: 8080},
			GrpcServer: app.GrpcServer{Address: "0.0.0.0"},
			Database: map[string]app.Database{
				"primary": {Type: "sqlite", Dsn: dsn, Writable: true, Readable: true},
			},
			Authn: app.IAMAuthn(authnext.Config{
				Enabled: true, Issuer: "https://iam.example.test", SigningKey: key,
				AccessTokenTTL: time.Hour,
				MFA:            authnext.MFAConfig{EncryptionKey: key},
			}),
		},
		PlatformAdminRequest: PlatformAdminRequest{Login: "admin"},
	}
}

// migratePlatformAdminDatabase brings the schema to the state the bootstrap
// requires. The command validates rather than migrates, so this stays a
// separate operator step here too.
func migratePlatformAdminDatabase(t *testing.T, config PlatformAdminConfig) {
	t.Helper()
	_, err := (&app.App{}).RunMigrations(t.Context(), app.MigrationConfig{
		AppConfig:        config.AppConfig,
		MigrationRequest: app.MigrationRequest{Action: app.MigrationAuto, To: -1},
	})
	require.NoError(t, err)
}

func newPlatformAdminDSN(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "platform-admin.sqlite")
}

// TestPlatformAdminBootstrapCreatesTheFirstAdministrator is the end-to-end
// contract: one command turns a migrated but unusable database into one an
// operator can sign in to.
func TestPlatformAdminBootstrapCreatesTheFirstAdministrator(t *testing.T) {
	config := newPlatformAdminConfig(t, newPlatformAdminDSN(t))
	config.DisplayName = "Platform Operator"
	config.Email = "ops@example.test"
	migratePlatformAdminDatabase(t, config)

	report, err := RunPlatformAdminBootstrap(t.Context(), config, bootstrapTestPassword)
	require.NoError(t, err)
	assert.Equal(t, PlatformAdminCreated, report.Outcome)
	assert.Equal(t, "admin", report.Login)
	assert.NotZero(t, report.PrincipalID, "the report must name the principal an operator can now audit")
}

// TestPlatformAdminBootstrapIsCreateOnlyOnRerun keeps a rerun from silently
// rotating a password. Rotation is a separate audited flow.
func TestPlatformAdminBootstrapIsCreateOnlyOnRerun(t *testing.T) {
	config := newPlatformAdminConfig(t, newPlatformAdminDSN(t))
	migratePlatformAdminDatabase(t, config)

	first, err := RunPlatformAdminBootstrap(t.Context(), config, bootstrapTestPassword)
	require.NoError(t, err)

	second, err := RunPlatformAdminBootstrap(t.Context(), config, "a-completely-different-password")
	require.NoError(t, err)
	assert.Equal(t, PlatformAdminAlreadyBootstrapped, second.Outcome)
	assert.Equal(t, first.PrincipalID, second.PrincipalID, "a rerun must report the administrator that exists")
}

// TestPlatformAdminBootstrapRefusesASecondAdministrator is the safety property
// the whole command exists for: bootstrap creates the first administrator, and
// only the first.
func TestPlatformAdminBootstrapRefusesASecondAdministrator(t *testing.T) {
	config := newPlatformAdminConfig(t, newPlatformAdminDSN(t))
	migratePlatformAdminDatabase(t, config)
	_, err := RunPlatformAdminBootstrap(t.Context(), config, bootstrapTestPassword)
	require.NoError(t, err)

	config.Login = "second-admin"
	_, err = RunPlatformAdminBootstrap(t.Context(), config, bootstrapTestPassword)
	assert.ErrorIs(t, err, ErrPlatformAdminConflict)
}

// TestPlatformAdminBootstrapReportsAnUnmigratedSchemaAsItsOwnOutcome keeps the
// operator pointed at "run migrations" instead of an opaque missing-table error.
func TestPlatformAdminBootstrapReportsAnUnmigratedSchemaAsItsOwnOutcome(t *testing.T) {
	config := newPlatformAdminConfig(t, newPlatformAdminDSN(t))

	_, err := RunPlatformAdminBootstrap(t.Context(), config, bootstrapTestPassword)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPlatformAdminSchemaNotReady)
	assert.NotErrorIs(t, err, ErrPlatformAdminConflict)
}

// TestPlatformAdminBootstrapValidatesBeforeTouchingTheDatabase proves an
// operator typo costs nothing: no connection, no lock, no half-open
// transaction.
func TestPlatformAdminBootstrapValidatesBeforeTouchingTheDatabase(t *testing.T) {
	base := newPlatformAdminConfig(t, newPlatformAdminDSN(t))

	t.Run("blank login", func(t *testing.T) {
		config := base
		config.Login = "   "
		_, err := RunPlatformAdminBootstrap(t.Context(), config, bootstrapTestPassword)
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrPlatformAdminSchemaNotReady, "the login is rejected before any schema check")
	})

	t.Run("short password", func(t *testing.T) {
		config := base
		_, err := RunPlatformAdminBootstrap(t.Context(), config, "short")
		require.ErrorIs(t, err, iamauthn.ErrBootstrapPassword)
		assert.NotErrorIs(t, err, ErrPlatformAdminSchemaNotReady)
	})
}

// TestPlatformAdminStoresReportUnavailableWithoutModules covers the resolution
// guard: nothing here may fall back to reaching into a module directly.
func TestPlatformAdminStoresReportUnavailableWithoutModules(t *testing.T) {
	_, err := resolvePlatformAdminStores(nil, nil, nil)
	assert.ErrorIs(t, err, ErrPlatformAdminUnavailable)
}

// TestPlatformAdminBootstrapReportsUnavailableWithoutIAM proves a deployment
// that never enabled IAM gets its own outcome rather than a resolution panic.
func TestPlatformAdminBootstrapReportsUnavailableWithoutIAM(t *testing.T) {
	config := newPlatformAdminConfig(t, newPlatformAdminDSN(t))
	migratePlatformAdminDatabase(t, config)
	config.Authn.Enabled = false

	_, err := RunPlatformAdminBootstrap(t.Context(), config, bootstrapTestPassword)
	assert.ErrorIs(t, err, ErrPlatformAdminUnavailable)
}

// --- decision-level tests -------------------------------------------------
//
// The decision is what an operator's outcome depends on, so it is exercised
// directly against a real transaction with stubbed capabilities. That keeps
// each failure path reachable without corrupting a real schema to reach it.

type stubBootstrapStores struct {
	principal      identity.Principal
	principalFound bool
	findErr        error
	administrators []core.PlatformAdministrator
	listErr        error
	credential     bool
	credentialErr  error
	resetReplaced  bool
	resetErr       error
	createdID      guid.ID
	createErr      error
	credCreateErr  error
	granted        bool
	grantErr       error
	appendErr      error

	appended []iamaudit.DomainEvent
}

func (stub *stubBootstrapStores) stores(db *gorm.DB) platformAdminStores {
	return platformAdminStores{
		db: db,
		principals: identity.BootstrapPrincipalStore{
			Find: func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
				return stub.principal, stub.principalFound, stub.findErr
			},
			Create: func(context.Context, *gorm.DB, identity.BootstrapPrincipalInput, time.Time) (guid.ID, error) {
				return stub.createdID, stub.createErr
			},
		},
		credentials: iamauthn.BootstrapCredentialStore{
			Exists: func(context.Context, *gorm.DB, guid.ID) (bool, error) {
				return stub.credential, stub.credentialErr
			},
			Create: func(context.Context, *gorm.DB, guid.ID, string, time.Time) error {
				return stub.credCreateErr
			},
			Reset: func(context.Context, *gorm.DB, guid.ID, string, time.Time) (bool, error) {
				return stub.resetReplaced, stub.resetErr
			},
		},
		administs: core.PlatformAdministratorStore{
			List: func(context.Context, *gorm.DB) ([]core.PlatformAdministrator, error) {
				return stub.administrators, stub.listErr
			},
			GrantFull: func(context.Context, *gorm.DB, guid.ID) (bool, error) {
				return stub.granted, stub.grantErr
			},
		},
		appender: func(_ context.Context, _ *gorm.DB, event iamaudit.DomainEvent) error {
			stub.appended = append(stub.appended, event)
			return stub.appendErr
		},
	}
}

func bootstrapDecisionInput() identity.BootstrapPrincipalInput {
	return identity.BootstrapPrincipalInput{LoginName: "admin", DisplayName: "Admin"}
}

// TestBootstrapDecisionWritesAnAuditEventNamingTheNewAdministrator keeps the
// creation traceable. A platform administrator appearing with no audit record
// is exactly the event an auditor would ask about first.
func TestBootstrapDecisionWritesAnAuditEventNamingTheNewAdministrator(t *testing.T) {
	stub := &stubBootstrapStores{createdID: 7100, granted: true}
	report, err := stub.stores(nil).decideAndWrite(
		t.Context(), nil, bootstrapDecisionInput(), bootstrapTestPassword, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, PlatformAdminCreated, report.Outcome)
	assert.EqualValues(t, 7100, report.PrincipalID)

	require.Len(t, stub.appended, 1)
	event := stub.appended[0]
	assert.Equal(t, "platform_administrator_bootstrapped", event.EventType)
	assert.Equal(t, "principal", event.TargetType)
	assert.EqualValues(t, 7100, event.TargetID)
	assert.Equal(t, platformAuditTenant, event.TenantID)
	assert.EqualValues(t, 7100, event.PrincipalID)
	assert.Equal(t, "admin", event.Detail["login_name"])
	assert.Equal(t, true, event.Detail["full_administrator"])
	assert.NotContains(t, event.Detail, "password", "the audit trail must not carry the credential")
}

// TestBootstrapDecisionTreatsEveryIncompleteAdministratorAsAConflict is the
// core judgment: reporting success for a login nobody can sign in with would
// hand an operator a working deployment that is not one.
func TestBootstrapDecisionTreatsEveryIncompleteAdministratorAsAConflict(t *testing.T) {
	existing := identity.Principal{ID: 7200, LoginName: "admin"}
	for name, stub := range map[string]*stubBootstrapStores{
		"administrator is another principal": {
			principal: existing, principalFound: true,
			administrators: []core.PlatformAdministrator{{PrincipalID: 9999, FullAdministrator: true}},
		},
		"administrator exists but the login is free": {
			administrators: []core.PlatformAdministrator{{PrincipalID: 9999, FullAdministrator: true}},
		},
		"login exists with no administrator row": {
			principal: existing, principalFound: true,
		},
		"administrator is only partially privileged": {
			principal: existing, principalFound: true,
			administrators: []core.PlatformAdministrator{{PrincipalID: 7200, FullAdministrator: false}},
		},
		"administrator holds no credential": {
			principal: existing, principalFound: true,
			administrators: []core.PlatformAdministrator{{PrincipalID: 7200, FullAdministrator: true}},
			credential:     false,
		},
		"grant reports no write": {
			createdID: 7200, granted: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := stub.stores(nil).decideAndWrite(
				t.Context(), nil, bootstrapDecisionInput(), bootstrapTestPassword, time.Now().UTC())
			assert.ErrorIs(t, err, ErrPlatformAdminConflict)
			assert.Empty(t, stub.appended, "a conflict must write no audit event")
		})
	}
}

// TestBootstrapDecisionReportsAnAlreadyBootstrappedAdministrator is the one
// existing-login shape that is not a conflict.
func TestBootstrapDecisionReportsAnAlreadyBootstrappedAdministrator(t *testing.T) {
	stub := &stubBootstrapStores{
		principal: identity.Principal{ID: 7300, LoginName: "admin"}, principalFound: true,
		administrators: []core.PlatformAdministrator{{PrincipalID: 7300, FullAdministrator: true}},
		credential:     true,
	}
	report, err := stub.stores(nil).decideAndWrite(
		t.Context(), nil, bootstrapDecisionInput(), bootstrapTestPassword, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, PlatformAdminAlreadyBootstrapped, report.Outcome)
	assert.EqualValues(t, 7300, report.PrincipalID)
	assert.Equal(t, "admin", report.Login)
	assert.Empty(t, stub.appended, "reporting an existing administrator writes nothing")
}

// TestBootstrapDecisionSurfacesEveryStoreFailureDistinctly keeps a storage
// fault from being reported as a conflict, which would send an operator
// looking for an administrator that does not exist.
func TestBootstrapDecisionSurfacesEveryStoreFailureDistinctly(t *testing.T) {
	fault := errors.New("store is down")
	existing := identity.Principal{ID: 7400, LoginName: "admin"}
	for name, stub := range map[string]*stubBootstrapStores{
		"principal lookup":   {findErr: fault},
		"administrator list": {listErr: fault},
		"credential lookup": {
			principal: existing, principalFound: true,
			administrators: []core.PlatformAdministrator{{PrincipalID: 7400, FullAdministrator: true}},
			credentialErr:  fault,
		},
		"principal creation":  {createErr: fault},
		"credential creation": {createdID: 7400, credCreateErr: fault},
		"administrator grant": {createdID: 7400, grantErr: fault},
		"audit append":        {createdID: 7400, granted: true, appendErr: fault},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := stub.stores(nil).decideAndWrite(
				t.Context(), nil, bootstrapDecisionInput(), bootstrapTestPassword, time.Now().UTC())
			require.ErrorIs(t, err, fault)
			assert.NotErrorIs(t, err, ErrPlatformAdminConflict, "a fault is not a conflict")
		})
	}
}

// TestBootstrapRequestAndReportCarryNoSecretMaterial is enforced structurally
// rather than by review. The request feeds the configuration surface the config
// inspector can read back, and the report is printed to an operator terminal,
// so a future field named for a password would leak by construction.
func TestBootstrapRequestAndReportCarryNoSecretMaterial(t *testing.T) {
	// The request may name a file holding the password, but never the password.
	// Anything in this struct is reachable from a config file, an environment
	// variable, and the config inspector endpoint.
	request := reflect.TypeOf(PlatformAdminRequest{})
	for index := range request.NumField() {
		field := request.Field(index)
		assert.NotEqual(t, "Password", field.Name,
			"the password is an argument, not configuration")
		if strings.Contains(strings.ToLower(field.Name), "password") {
			assert.Equal(t, "PasswordFile", field.Name,
				"only a path to the password may be configured")
		}
	}

	report := reflect.TypeOf(PlatformAdminReport{})
	for index := range report.NumField() {
		name := strings.ToLower(report.Field(index).Name)
		for _, forbidden := range []string{"password", "secret", "hash", "credential"} {
			assert.NotContains(t, name, forbidden,
				"the report is printed to a terminal and may be redirected to a file")
		}
	}
}
