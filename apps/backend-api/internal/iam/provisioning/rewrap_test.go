package provisioning

import (
	"testing"

	"7link.com/platform/internal/iam/provisioning/model"
	"7link.com/platform/pkg/extension/keyringx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProvisioningRewrapMovesStoredTokensOntoTheActiveKey walks the rotation
// end to end. The survey first, because that is the order an operator works in:
// find out what the retired key still holds, then decide to convert it.
func TestProvisioningRewrapMovesStoredTokensOntoTheActiveKey(t *testing.T) {
	env := newProvisioningEnvironment(t)
	target, err := env.service.CreateTarget(
		t.Context(), testID("tenant-a"), "Rewrapping", "https://example.test/scim", "not-a-real-bearer-token",
	)
	require.NoError(t, err)
	targetID := parseGUID(target.Target.ID)

	replacement := []byte("fedcba9876543210fedcba9876543210")
	env.service.keys = newProvisioningTestKeyring(t, replacement, testProvisioningKey())
	provisioningModule := &Module{service: env.service}

	survey, err := provisioningModule.RewrapSecrets(t.Context(), env.db, keyringx.SurveyOnly)
	require.NoError(t, err)
	require.Len(t, survey.Sealed, 1)
	assert.Equal(t, "iam_scim_targets.bearer_token_ciphertext", survey.Sealed[0].Store)
	assert.Equal(t, int64(1), survey.Sealed[0].Retired)
	assert.Equal(t, int64(1), survey.Pending())
	assert.Empty(t, survey.Derived, "nothing this module holds is a one-way digest")
	assert.Equal(t, int64(0), survey.Sealed[0].Rewrapped)

	sealedBefore := storedTargetToken(t, env, target.Target.ID)
	assert.True(t, env.service.keys.NeedsRewrap(sealedBefore),
		"the survey must not have written anything")

	result, err := provisioningModule.RewrapSecrets(t.Context(), env.db, keyringx.RewrapStale)
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.Sealed[0].Rewrapped)
	assert.Equal(t, int64(0), result.Sealed[0].Skipped)

	// The token has to still reach the provider afterwards, or the rewrap has
	// replaced an outage that had not happened yet with one that has.
	sealedAfter := storedTargetToken(t, env, target.Target.ID)
	assert.False(t, env.service.keys.NeedsRewrap(sealedAfter))
	token, err := env.service.decryptToken(targetID, sealedAfter)
	require.NoError(t, err)
	assert.Equal(t, "not-a-real-bearer-token", token)

	// A second pass is what an operator runs to confirm the first one finished,
	// and zero pending is the sentence they act on.
	confirmation, err := provisioningModule.RewrapSecrets(t.Context(), env.db, keyringx.SurveyOnly)
	require.NoError(t, err)
	assert.Equal(t, int64(1), confirmation.Sealed[0].Current)
	assert.Equal(t, int64(0), confirmation.Pending())
}

// TestProvisioningRewrapSeparatesNoTargetsFromNoService keeps two very
// different states from producing the same report. A deployment that pushes
// nowhere has nothing to reseal; a module that never initialized cannot say
// whether it does, and reporting zero for it would be a guess dressed as an
// answer.
func TestProvisioningRewrapSeparatesNoTargetsFromNoService(t *testing.T) {
	env := newProvisioningEnvironment(t)
	env.service.keys = nil
	withoutKeys := &Module{service: env.service}

	result, err := withoutKeys.RewrapSecrets(t.Context(), env.db, keyringx.RewrapStale)
	require.NoError(t, err)
	assert.Empty(t, result.Sealed)
	assert.Equal(t, int64(0), result.Pending())

	_, err = (&Module{}).RewrapSecrets(t.Context(), env.db, keyringx.SurveyOnly)
	assert.ErrorIs(t, err, ErrUnavailable)

	var absent *Module
	_, err = absent.RewrapSecrets(t.Context(), env.db, keyringx.SurveyOnly)
	assert.ErrorIs(t, err, ErrUnavailable)
}

func storedTargetToken(t *testing.T, env *provisioningEnvironment, targetID string) string {
	t.Helper()
	var row model.Target
	require.NoError(t, env.db.Table(env.prefix+"iam_scim_targets").
		Where("id = ?", targetID).First(&row).Error)
	return row.BearerTokenCiphertext
}
