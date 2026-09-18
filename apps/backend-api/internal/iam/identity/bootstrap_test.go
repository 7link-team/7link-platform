package identity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestBootstrapPrincipalStoreCreatesThenFindsTheAdministratorLogin walks the
// capability the composition boundary actually holds: the closures the module
// provides before injection, not the repository behind them.
func TestBootstrapPrincipalStoreCreatesThenFindsTheAdministratorLogin(t *testing.T) {
	harness := newIdentityHarness(t, "bootfind_")
	store := (&Module{
		runtime:  harness.runtime,
		ids:      newIdentityTestIDs(7000),
		appender: harness.repository.appender,
		storage:  harness.repository.storage,
	}).bootstrapPrincipalStore()

	_, found, err := store.Find(t.Context(), harness.db, "Admin")
	require.NoError(t, err)
	assert.False(t, found, "a fresh database owes the bootstrap a free login")

	id, err := store.Create(t.Context(), harness.db, BootstrapPrincipalInput{
		LoginName: "  Admin  ", Email: "Admin@Example.COM",
	}, identityTestNow)
	require.NoError(t, err)
	require.NotZero(t, id)

	principal, found, err := store.Find(t.Context(), harness.db, "ADMIN")
	require.NoError(t, err)
	require.True(t, found, "the login must be found under any casing the operator typed")
	assert.Equal(t, id, principal.ID)
	assert.Equal(t, "admin", principal.LoginName)
	assert.Equal(t, "admin@example.com", principal.Email)
	assert.True(t, principal.EmailVerified, "an operator-supplied address needs no round trip to verify")
	assert.Equal(t, "admin", principal.DisplayName, "the display name defaults to the login")
	assert.Equal(t, "active", principal.Status)
}

// TestBootstrapPrincipalCreationOmitsEmailVerificationWithoutAnAddress keeps the
// verified flag tied to an address actually being present.
func TestBootstrapPrincipalCreationOmitsEmailVerificationWithoutAnAddress(t *testing.T) {
	harness := newIdentityHarness(t, "bootnomail_")
	id, err := harness.repository.CreateBootstrapPrincipalOn(t.Context(), harness.db, BootstrapPrincipalInput{
		LoginName: "root", DisplayName: "Platform Root",
	}, identityTestNow)
	require.NoError(t, err)

	principal, found, err := harness.repository.FindPrincipalByLoginOn(t.Context(), harness.db, "root")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, id, principal.ID)
	assert.Empty(t, principal.Email)
	assert.False(t, principal.EmailVerified)
	assert.Equal(t, "Platform Root", principal.DisplayName)
}

func TestNormalizeBootstrapLoginMatchesTheLookupItFeeds(t *testing.T) {
	assert.Equal(t, "admin", NormalizeBootstrapLogin("  ADMIN "))
	assert.Empty(t, NormalizeBootstrapLogin("   "))
}

// TestBootstrapPrincipalLookupRejectsIncompleteArguments proves the capability
// refuses to run outside a caller-owned transaction rather than quietly
// reaching for the module writer instead.
func TestBootstrapPrincipalLookupRejectsIncompleteArguments(t *testing.T) {
	harness := newIdentityHarness(t, "bootargs_")
	var missing *Repository

	for name, call := range map[string]func() error{
		"nil repository": func() error {
			_, _, err := missing.FindPrincipalByLoginOn(t.Context(), harness.db, "admin")
			return err
		},
		"nil transaction": func() error {
			_, _, err := harness.repository.FindPrincipalByLoginOn(t.Context(), nil, "admin")
			return err
		},
		"blank login": func() error {
			_, _, err := harness.repository.FindPrincipalByLoginOn(t.Context(), harness.db, "   ")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, call(), ErrInvalid)
		})
	}
}

func TestBootstrapPrincipalCreationRejectsIncompleteArguments(t *testing.T) {
	harness := newIdentityHarness(t, "bootcreateargs_")
	var missing *Repository

	_, err := missing.CreateBootstrapPrincipalOn(
		t.Context(), harness.db, BootstrapPrincipalInput{LoginName: "admin"}, identityTestNow)
	assert.ErrorIs(t, err, ErrInvalid)

	_, err = harness.repository.CreateBootstrapPrincipalOn(
		t.Context(), nil, BootstrapPrincipalInput{LoginName: "admin"}, identityTestNow)
	assert.ErrorIs(t, err, ErrInvalid)
}

// TestBootstrapPrincipalCreationRejectsMalformedInput pins every normalization
// bound, because the bootstrap runs with no HTTP layer in front of it to
// validate the operator flags first.
func TestBootstrapPrincipalCreationRejectsMalformedInput(t *testing.T) {
	harness := newIdentityHarness(t, "bootmalformed_")
	for name, input := range map[string]BootstrapPrincipalInput{
		"blank login":      {LoginName: "   "},
		"overlong login":   {LoginName: strings.Repeat("a", 201)},
		"overlong name":    {LoginName: "admin", DisplayName: strings.Repeat("n", 129)},
		"overlong email":   {LoginName: "admin", Email: strings.Repeat("e", 314) + "@ex.com"},
		"unparsable email": {LoginName: "admin", Email: "not-an-address"},
		"decorated email":  {LoginName: "admin", Email: "Ops <ops@example.com>"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := harness.repository.CreateBootstrapPrincipalOn(
				t.Context(), harness.db, input, identityTestNow)
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
}

// TestBootstrapPrincipalSurfacesStorageFailures separates "the login is free"
// from "the database could not answer". Confusing the two would let the
// bootstrap create a second administrator.
func TestBootstrapPrincipalSurfacesStorageFailures(t *testing.T) {
	harness := newIdentityHarness(t, "bootstorage_")
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_principals")).Error)

	_, found, err := harness.repository.FindPrincipalByLoginOn(t.Context(), harness.db, "admin")
	require.Error(t, err)
	assert.False(t, found)
	assert.NotErrorIs(t, err, gorm.ErrRecordNotFound, "a broken table must not read as a free login")

	_, err = harness.repository.CreateBootstrapPrincipalOn(
		t.Context(), harness.db, BootstrapPrincipalInput{LoginName: "admin"}, identityTestNow)
	assert.Error(t, err)
}

// TestBootstrapPrincipalCreationAbortsWhenTheIDGeneratorFails proves a failed
// lease aborts instead of inserting a row identified by zero.
func TestBootstrapPrincipalCreationAbortsWhenTheIDGeneratorFails(t *testing.T) {
	harness := newIdentityHarness(t, "bootnoid_")
	repository := NewRepository(harness.runtime, failingIDSource{}, harness.repository.appender)

	_, err := repository.CreateBootstrapPrincipalOn(
		t.Context(), harness.db, BootstrapPrincipalInput{LoginName: "admin"}, identityTestNow)
	require.Error(t, err)

	var count int64
	require.NoError(t, harness.db.Table(harness.table("iam_principals")).Count(&count).Error)
	assert.Zero(t, count, "a failed identifier lease must leave no principal behind")
}

// TestBootstrapPrincipalStoreReportsUnavailableBeforeWiring covers the guard
// inside the closures: they are handed out during ProvideServices, so they can
// be called on a module whose collaborators never arrived.
func TestBootstrapPrincipalStoreReportsUnavailableBeforeWiring(t *testing.T) {
	harness := newIdentityHarness(t, "bootunwired_")
	for name, unwired := range map[string]*Module{
		"nil module":        nil,
		"no runtime":        {ids: newIdentityTestIDs(1), appender: harness.repository.appender},
		"no id generator":   {runtime: harness.runtime, appender: harness.repository.appender},
		"no audit appender": {runtime: harness.runtime, ids: newIdentityTestIDs(1)},
	} {
		t.Run(name, func(t *testing.T) {
			store := unwired.bootstrapPrincipalStore()

			_, _, err := store.Find(t.Context(), harness.db, "admin")
			assert.ErrorIs(t, err, ErrUnavailable)

			_, err = store.Create(
				t.Context(), harness.db, BootstrapPrincipalInput{LoginName: "admin"}, identityTestNow)
			assert.ErrorIs(t, err, ErrUnavailable)
		})
	}
}
