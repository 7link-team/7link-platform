package app

import (
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/extension/keyringx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSecretRewrapConfig turns the platform-administrator configuration into one
// where browser authentication is switched on, because that is what makes the
// authentication module hold a key at all. Everything else is deliberately the
// service configuration unchanged: this command runs against the deployment as
// it is, not against a shape invented for it.
func newSecretRewrapConfig(t *testing.T, dsn string) RewrapSecretsConfig {
	t.Helper()
	admin := newPlatformAdminConfig(t, dsn)
	authn := authnext.Config(admin.Authn)
	authn.Web = authnext.WebConfig{
		Enabled: true, CookieSecure: true,
		PostLoginURL:      "https://app.example.test/home",
		AllowedReturnURLs: []string{"https://app.example.test/home"},
		AllowedOrigins:    []string{"https://app.example.test"},
		SessionTTL:        time.Hour, IdleTTL: 15 * time.Minute,
	}
	admin.AppConfig.Authn = IAMAuthn(authn)
	return RewrapSecretsConfig{AppConfig: admin.AppConfig}
}

// seedRecoveryCodesAndVerification writes the two kinds of derived value this
// report has to keep apart: codes that never expire, and a link that does.
// Neither is a ciphertext -- they are digests of secrets the deployment does
// not hold -- so plain strings are exactly what these tables contain.
func seedRecoveryCodesAndVerification(t *testing.T, config RewrapSecretsConfig, principalID any) time.Time {
	t.Helper()
	db, err := (gormx.Datasource{
		Type: "sqlite", DSN: config.Database["primary"].Dsn, Writable: true,
	}).Open(t.Context())
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()

	now := time.Now().UTC()
	for _, digest := range []string{"not-a-real-code-digest-1", "not-a-real-code-digest-2"} {
		require.NoError(t, db.Exec(
			"INSERT INTO iam_recovery_codes (principal_id, code_hash, created_at) VALUES (?, ?, ?)",
			principalID, digest, now,
		).Error)
	}
	expiry := now.Add(90 * time.Minute)
	require.NoError(t, db.Exec(
		"INSERT INTO iam_email_verification_tokens"+
			" (token_hmac, principal_id, email, created_at, expires_at, code, attempts)"+
			" VALUES (?, ?, ?, ?, ?, ?, 0)",
		"not-a-real-verification-digest", principalID, "ops@example.test", now, expiry, "000000",
	).Error)
	return expiry
}

// TestSecretRewrapAnswersWhetherARetiredKeyMayComeOut is the end-to-end
// contract. The command reaches every sealed column through the real migrated
// schema, and it reports the outstanding recovery codes that make the answer no
// -- which is the case a rewrap that only counted ciphertext would get wrong,
// because it would have converted everything and reported success.
func TestSecretRewrapAnswersWhetherARetiredKeyMayComeOut(t *testing.T) {
	dsn := newPlatformAdminDSN(t)
	config := newSecretRewrapConfig(t, dsn)
	migratePlatformAdminDatabase(t, PlatformAdminConfig{AppConfig: config.AppConfig})
	owner, err := (&App{}).RunPlatformAdminBootstrap(
		t.Context(), newPlatformAdminConfig(t, dsn), bootstrapTestPassword)
	require.NoError(t, err)
	seedRecoveryCodesAndVerification(t, config, owner.PrincipalID)

	survey := config
	survey.DryRun = true
	report, err := (&App{}).RunSecretRewrap(t.Context(), survey)
	require.NoError(t, err)

	assert.True(t, report.DryRun, "a survey pasted into a ticket must not read as proof a rewrap ran")
	// Provisioning is switched on but pushes nowhere, so it holds no key and has
	// nothing to reseal. It still reports: a module that answered "nothing" and a
	// module that was never asked are the same line in the output otherwise.
	require.Len(t, report.Modules, 2)
	assert.Equal(t, "authn", report.Modules[0].Module)
	assert.Equal(t, "provisioning", report.Modules[1].Module)
	assert.Empty(t, report.Modules[1].Sealed)
	assert.Empty(t, report.Modules[1].Derived)
	// Every sealed column has to come back, because a store that silently failed
	// to run would look exactly like one with nothing left to convert.
	require.Len(t, report.Modules[0].Sealed, 4)
	for _, sealed := range report.Modules[0].Sealed {
		assert.Equal(t, int64(0), sealed.Total, sealed.Store)
	}
	assert.Equal(t, int64(0), report.Pending)
	assert.Equal(t, int64(0), report.Unreadable)
	assert.Equal(t, int64(3), report.Outstanding)
	assert.True(t, report.RetiredKeysStillNeeded,
		"unused recovery codes are the reason the key stays, even with nothing left to reseal")
	assert.True(t, report.ClearAfter.IsZero(),
		"recovery codes never expire, so there is no date after which waiting has worked")

	// The real pass has nothing to convert here, and has to say so rather than
	// letting an empty conversion read as a finished rotation.
	applied, err := (&App{}).RunSecretRewrap(t.Context(), config)
	require.NoError(t, err)
	assert.False(t, applied.DryRun)
	assert.Equal(t, int64(0), applied.Rewrapped)
	assert.Equal(t, int64(3), applied.Outstanding)
	assert.True(t, applied.RetiredKeysStillNeeded)
}

// TestSecretRewrapSummaryRefusesToPrintADateThatReadsAsPermission covers the
// arithmetic an operator acts on. A date next to a store that never expires
// would be read as "wait until then and delete the key", and the account whose
// recovery codes were still live is the one that never comes back.
func TestSecretRewrapSummaryRefusesToPrintADateThatReadsAsPermission(t *testing.T) {
	soon := time.Date(2026, time.September, 10, 9, 0, 0, 0, time.UTC)
	later := soon.Add(48 * time.Hour)

	t.Run("nothing outstanding and nothing pending", func(t *testing.T) {
		report := summarizeSecretRewrap(SecretRewrapReport{Modules: []ModuleSecretReport{{
			Module: "authn", RewrapResult: keyringx.RewrapResult{
				Sealed: []keyringx.StoreReport{{Total: 3, Current: 3}},
				// An empty store still reports, and a spent expiry on it must not
				// become the deployment's answer.
				Derived: []keyringx.DerivedStoreReport{{Outstanding: 0, Until: later}},
			},
		}}})
		assert.False(t, report.RetiredKeysStillNeeded)
		assert.True(t, report.ClearAfter.IsZero())
		assert.Equal(t, int64(0), report.Outstanding)
	})

	t.Run("everything outstanding expires", func(t *testing.T) {
		report := summarizeSecretRewrap(SecretRewrapReport{Modules: []ModuleSecretReport{
			{Module: "authn", RewrapResult: keyringx.RewrapResult{
				Derived: []keyringx.DerivedStoreReport{
					{Outstanding: 1, Until: soon}, {Outstanding: 2, Until: later},
				},
			}},
			{Module: "federation", RewrapResult: keyringx.RewrapResult{
				Derived: []keyringx.DerivedStoreReport{{Outstanding: 4, Until: soon}},
			}},
		}})
		assert.Equal(t, int64(7), report.Outstanding)
		assert.True(t, report.RetiredKeysStillNeeded)
		assert.Equal(t, later, report.ClearAfter, "the last value to expire is the one that decides")
	})

	t.Run("one store never expires", func(t *testing.T) {
		report := summarizeSecretRewrap(SecretRewrapReport{Modules: []ModuleSecretReport{{
			Module: "authn", RewrapResult: keyringx.RewrapResult{
				Derived: []keyringx.DerivedStoreReport{
					{Outstanding: 2, Until: later},
					{Outstanding: 1, Reissue: "each person generates a new set"},
				},
			},
		}}})
		assert.Equal(t, int64(3), report.Outstanding)
		assert.True(t, report.ClearAfter.IsZero(),
			"a date beside a store that never clears is worse than no date at all")
	})

	t.Run("sealed values left on a retired key", func(t *testing.T) {
		report := summarizeSecretRewrap(SecretRewrapReport{Modules: []ModuleSecretReport{{
			Module: "provisioning", RewrapResult: keyringx.RewrapResult{
				Sealed: []keyringx.StoreReport{
					{Total: 4, Current: 1, Retired: 2, Legacy: 1, Rewrapped: 1},
					{Total: 2, Current: 1, Unreadable: 1},
				},
			},
		}}})
		assert.Equal(t, int64(1), report.Rewrapped)
		assert.Equal(t, int64(2), report.Pending)
		assert.Equal(t, int64(1), report.Unreadable,
			"a value no key opens is already broken, not a warning about a future deletion")
		assert.True(t, report.RetiredKeysStillNeeded)
		assert.True(t, report.ClearAfter.IsZero(), "sealed values are converted, not waited out")
	})
}

// TestSecretRewrapReportsAnUncomposedApplicationAsUnavailable keeps a missing
// capability from reporting zero. Zero pending is the sentence that ends with a
// key being deleted, so it may only ever come from a pass that actually ran.
func TestSecretRewrapReportsAnUncomposedApplicationAsUnavailable(t *testing.T) {
	_, err := (&App{}).secretRewrappers(platformAdminStores{})
	assert.ErrorIs(t, err, ErrSecretRewrapUnavailable)
}
