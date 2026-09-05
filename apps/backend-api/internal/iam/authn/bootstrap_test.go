package authn

import (
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/passwordx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const bootstrapTestPassword = "not-a-real-bootstrap-password"

// TestBootstrapCredentialStoreWritesAVerifiableFirstCredential walks the
// capability the composition boundary holds. It asserts through Verify rather
// than by comparing stored text, because the point of keeping hashing inside
// this package is that the plaintext never leaves it.
func TestBootstrapCredentialStoreWritesAVerifiableFirstCredential(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "bootcred_")
	const principalID guid.ID = 4100
	harness.seedPrincipal(t, principalID, "admin")
	store := (&Module{
		runtime:  harness.runtime,
		ids:      newRepositoryTestIDs(9000),
		appender: harness.repository.audit,
	}).bootstrapCredentialStore()

	exists, err := store.Exists(t.Context(), harness.db, principalID)
	require.NoError(t, err)
	assert.False(t, exists, "a freshly created principal holds no credential yet")

	require.NoError(t, store.Create(t.Context(), harness.db, principalID, bootstrapTestPassword, repositoryTestNow))

	exists, err = store.Exists(t.Context(), harness.db, principalID)
	require.NoError(t, err)
	assert.True(t, exists)

	credential, err := harness.repository.withDB(harness.db, true).getCredential(t.Context(), principalID)
	require.NoError(t, err)
	assert.NotContains(t, credential.PasswordHash, bootstrapTestPassword, "the password must not be recoverable from storage")
	assert.EqualValues(t, 1, credential.CredentialVersion, "the bootstrap writes the first version, not a rotation")
	matched, err := passwordx.Verify(credential.PasswordHash, bootstrapTestPassword)
	require.NoError(t, err)
	assert.True(t, matched, "the administrator must be able to sign in with what the operator supplied")
}

// TestValidateBootstrapPasswordEnforcesLengthWithoutEchoingTheValue pins both
// bounds and the wording, because the error reaches an operator terminal.
func TestValidateBootstrapPasswordEnforcesLengthWithoutEchoingTheValue(t *testing.T) {
	assert.NoError(t, ValidateBootstrapPassword(strings.Repeat("p", 12)))
	assert.NoError(t, ValidateBootstrapPassword(strings.Repeat("p", 1024)))

	for name, password := range map[string]string{
		"empty":     "",
		"too short": strings.Repeat("p", 11),
		"too long":  strings.Repeat("p", 1025),
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateBootstrapPassword(password)
			require.ErrorIs(t, err, ErrBootstrapPassword)
			if password != "" {
				assert.NotContains(t, err.Error(), password, "the rejection must not quote the value")
			}
		})
	}
}

// TestBootstrapCredentialCreationRejectsAPasswordTheStoreWouldNotAccept keeps
// the length gate on the write path too, so a caller that skipped the
// pre-transaction check cannot install an unusable credential.
func TestBootstrapCredentialCreationRejectsAPasswordTheStoreWouldNotAccept(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "bootshortpw_")
	const principalID guid.ID = 4101
	harness.seedPrincipal(t, principalID, "admin")

	err := harness.repository.CreateBootstrapCredentialOn(
		t.Context(), harness.db, principalID, "short", repositoryTestNow)
	assert.ErrorIs(t, err, ErrBootstrapPassword)

	exists, err := harness.repository.HasCredentialOn(t.Context(), harness.db, principalID)
	require.NoError(t, err)
	assert.False(t, exists, "a rejected password must leave no credential behind")
}

// TestBootstrapCredentialCapabilitiesRejectIncompleteArguments proves both
// halves refuse to run outside a caller-owned transaction.
func TestBootstrapCredentialCapabilitiesRejectIncompleteArguments(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "bootcredargs_")
	var missing *Repository

	for name, call := range map[string]func() error{
		"exists without repository": func() error {
			_, err := missing.HasCredentialOn(t.Context(), harness.db, 1)
			return err
		},
		"exists without transaction": func() error {
			_, err := harness.repository.HasCredentialOn(t.Context(), nil, 1)
			return err
		},
		"exists without principal": func() error {
			_, err := harness.repository.HasCredentialOn(t.Context(), harness.db, 0)
			return err
		},
		"create without repository": func() error {
			return missing.CreateBootstrapCredentialOn(t.Context(), harness.db, 1, bootstrapTestPassword, repositoryTestNow)
		},
		"create without transaction": func() error {
			return harness.repository.CreateBootstrapCredentialOn(t.Context(), nil, 1, bootstrapTestPassword, repositoryTestNow)
		},
		"create without principal": func() error {
			return harness.repository.CreateBootstrapCredentialOn(t.Context(), harness.db, 0, bootstrapTestPassword, repositoryTestNow)
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, call())
		})
	}
}

// TestBootstrapCredentialLookupSurfacesStorageFailures separates "no credential
// yet" from "the database could not answer". The bootstrap treats the first as
// a rerun and the second as a fault, so they must not collapse.
func TestBootstrapCredentialLookupSurfacesStorageFailures(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "bootcredstorage_")
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.runtime.TablePrefix()+"iam_credentials").Error)

	exists, err := harness.repository.HasCredentialOn(t.Context(), harness.db, 4102)
	require.Error(t, err)
	assert.False(t, exists)

	assert.Error(t, harness.repository.CreateBootstrapCredentialOn(
		t.Context(), harness.db, 4102, bootstrapTestPassword, repositoryTestNow))
}

// TestBootstrapCredentialStoreReportsUnavailableBeforeWiring covers the guard
// inside the closures, which are handed out during ProvideServices and so can
// be called on a module whose collaborators never arrived.
func TestBootstrapCredentialStoreReportsUnavailableBeforeWiring(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "bootcredunwired_")
	for name, unwired := range map[string]*Module{
		"nil module":        nil,
		"no runtime":        {ids: newRepositoryTestIDs(1), appender: harness.repository.audit},
		"no id generator":   {runtime: harness.runtime, appender: harness.repository.audit},
		"no audit appender": {runtime: harness.runtime, ids: newRepositoryTestIDs(1)},
	} {
		t.Run(name, func(t *testing.T) {
			store := unwired.bootstrapCredentialStore()

			_, err := store.Exists(t.Context(), harness.db, 1)
			assert.ErrorIs(t, err, authnext.ErrUnavailable)

			assert.ErrorIs(t, store.Create(
				t.Context(), harness.db, 1, bootstrapTestPassword, repositoryTestNow),
				authnext.ErrUnavailable)
		})
	}
}
