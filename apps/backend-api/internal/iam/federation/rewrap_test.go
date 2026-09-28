package federation

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/keyringx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The material below is obviously not a key: a test that carried a
// plausible-looking signing key would be a secret in the repository.
const (
	notARealSAMLKeyPEM  = "-----BEGIN PRIVATE KEY-----\nnot-a-real-saml-signing-key\n-----END PRIVATE KEY-----\n"
	notARealSAMLCertPEM = "-----BEGIN CERTIFICATE-----\nnot-a-real-certificate\n-----END CERTIFICATE-----\n"
)

// TestFederationRewrapMovesBothSealedColumnsOntoTheActiveKey covers the two
// things this module cannot reissue on its own. A client secret lives at the
// upstream provider and a SAML key is already published in metadata relying
// parties have loaded, so losing either to a rotation is an outage somebody
// else has to end.
func TestFederationRewrapMovesBothSealedColumnsOntoTheActiveKey(t *testing.T) {
	env := newFederationEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	sealedKey, err := env.service.encryptSAMLKey(notARealSAMLKeyPEM)
	require.NoError(t, err)
	require.NoError(t, env.service.repo.insertSAMLKey(t.Context(), &samlKeyRow{
		KeyID: "not-a-real-key-id", CertPEM: notARealSAMLCertPEM,
		KeyCiphertext: sealedKey, CreatedAt: federationTestNow,
	}))

	replacement := []byte("fedcba9876543210fedcba9876543210")
	env.service.keys = newFederationTestKeyring(t, replacement, env.key)
	federationModule := &Module{service: env.service}

	survey, err := federationModule.RewrapSecrets(t.Context(), env.db, keyringx.SurveyOnly)
	require.NoError(t, err)
	require.Len(t, survey.Sealed, 2)
	assert.Equal(t, "iam_identity_providers.client_secret_ciphertext", survey.Sealed[0].Store)
	assert.Equal(t, "iam_saml_idp_keys.key_ciphertext", survey.Sealed[1].Store)
	assert.Equal(t, int64(2), survey.Pending())
	assert.Empty(t, survey.Derived, "nothing this module holds is a one-way digest")
	assert.True(t, env.service.keys.NeedsRewrap(storedProviderSecret(t, env, provider.ID)),
		"a survey that writes is not a survey")

	result, err := federationModule.RewrapSecrets(t.Context(), env.db, keyringx.RewrapStale)
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.Sealed[0].Rewrapped)
	assert.Equal(t, int64(1), result.Sealed[1].Rewrapped)
	assert.Equal(t, int64(0), result.Pending())

	// Both values have to still work afterwards. The SAML key is the one that
	// proves the store descriptions were right: it is sealed without associated
	// data, so a rewrap that helpfully supplied some would have destroyed it.
	secret, err := env.service.decryptSecret(provider.ID, storedProviderSecret(t, env, provider.ID))
	require.NoError(t, err)
	assert.Equal(t, "client-secret", secret)
	key, err := env.service.decryptSAMLKey(storedSAMLKey(t, env))
	require.NoError(t, err)
	assert.Equal(t, notARealSAMLKeyPEM, key)

	confirmation, err := federationModule.RewrapSecrets(t.Context(), env.db, keyringx.SurveyOnly)
	require.NoError(t, err)
	assert.Equal(t, int64(1), confirmation.Sealed[0].Current)
	assert.Equal(t, int64(1), confirmation.Sealed[1].Current)
	assert.Equal(t, int64(0), confirmation.Pending())
}

// TestFederationRewrapRefusesToReportOnAModuleThatCannotAnswer keeps a silent
// zero out of the report. This module's ring is required wherever it is
// switched on, so a missing one is a module that never initialized, and "no
// rows depend on the retired key" would be a guess dressed as an answer.
func TestFederationRewrapRefusesToReportOnAModuleThatCannotAnswer(t *testing.T) {
	env := newFederationEnvironment(t)
	env.service.keys = nil

	_, err := (&Module{service: env.service}).RewrapSecrets(t.Context(), env.db, keyringx.SurveyOnly)
	assert.ErrorIs(t, err, ErrUnavailable)

	_, err = (&Module{}).RewrapSecrets(t.Context(), env.db, keyringx.SurveyOnly)
	assert.ErrorIs(t, err, ErrUnavailable)

	var absent *Module
	_, err = absent.RewrapSecrets(t.Context(), env.db, keyringx.SurveyOnly)
	assert.ErrorIs(t, err, ErrUnavailable)
}

func storedProviderSecret(t *testing.T, env *federationEnvironment, providerID guid.ID) string {
	t.Helper()
	row, err := env.service.repo.getProviderByID(t.Context(), providerID)
	require.NoError(t, err)
	return row.ClientSecretCiphertext
}

func storedSAMLKey(t *testing.T, env *federationEnvironment) string {
	t.Helper()
	row, err := env.service.repo.latestSAMLKey(t.Context())
	require.NoError(t, err)
	return row.KeyCiphertext
}
