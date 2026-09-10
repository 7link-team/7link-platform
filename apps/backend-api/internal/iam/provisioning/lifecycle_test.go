package provisioning

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var provisioningErasureTime = time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)

func TestProvisioningErasePrincipalDataRefusesUnusableArguments(t *testing.T) {
	env := newProvisioningEnvironment(t)

	assert.ErrorContains(t, ErasePrincipalData(t.Context(), nil, "", 1, provisioningErasureTime), "invalid principal erasure transaction")
	assert.ErrorContains(t, ErasePrincipalData(t.Context(), env.db, "", 0, provisioningErasureTime), "invalid principal erasure transaction")
	assert.Error(t, ErasePrincipalData(t.Context(), env.db, "bad prefix; DROP TABLE x;--", 1, provisioningErasureTime))
}

func TestProvisioningErasePrincipalDataRefusesWhileARemoteAccountLives(t *testing.T) {
	env := newProvisioningEnvironment(t)
	tenant := testID("tenant-a")
	_, server := newSCIMProviderEndpoint(t, 200, `{"id":"remote-user"}`)
	target, err := env.service.CreateTarget(t.Context(), tenant, "Directory", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("erase", "erase", "erase@example.test"))
	require.NoError(t, err)
	_, err = env.service.PushResource(t.Context(), tenant, parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	require.NoError(t, err)

	// The remote account exists and is still reachable, so anonymizing the local
	// record would strip the only thing that says whose data is sitting over
	// there under a name nobody can resolve any more.
	assert.ErrorIs(t,
		ErasePrincipalData(t.Context(), env.db, env.prefix, parseGUID(user.ID), provisioningErasureTime),
		ErrPrincipalRemoteProvisioned)
}

// TestDeletingATargetCannotBypassRemoteErasureAccountability is the regression
// this guard exists for. Erasure decides by counting live mappings; deleting a
// target used to cascade those rows away, so the same principal that was
// refused a moment earlier would be approved with the remote account still in
// place and the bearer token needed to reach it destroyed.
func TestDeletingATargetCannotBypassRemoteErasureAccountability(t *testing.T) {
	env := newProvisioningEnvironment(t)
	tenant := testID("tenant-a")
	provider, server := newSCIMProviderEndpoint(t, 200, `{"id":"remote-user"}`)
	target, err := env.service.CreateTarget(t.Context(), tenant, "Directory", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)
	targetID := parseGUID(target.Target.ID)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("bypass", "bypass", "bypass@example.test"))
	require.NoError(t, err)
	principalID := parseGUID(user.ID)
	_, err = env.service.PushResource(t.Context(), tenant, targetID, ResourceUser, principalID)
	require.NoError(t, err)

	require.ErrorIs(t, ErasePrincipalData(t.Context(), env.db, env.prefix, principalID, provisioningErasureTime),
		ErrPrincipalRemoteProvisioned, "the guard must refuse before the target is touched")

	assert.ErrorIs(t, env.service.DeleteTarget(t.Context(), tenant, targetID), ErrTargetHasLiveMappings,
		"deleting the target is the bypass, so it is refused while the mapping is live")

	// Nothing was destroyed by the refusal, so erasure still refuses too.
	assert.ErrorIs(t, ErasePrincipalData(t.Context(), env.db, env.prefix, principalID, provisioningErasureTime),
		ErrPrincipalRemoteProvisioned)

	// The way through is the one that ends with the remote account gone.
	provider.configure(204, "", true)
	require.NoError(t, env.service.DeprovisionResource(t.Context(), tenant, targetID, ResourceUser, principalID))
	require.NoError(t, env.service.DeleteTarget(t.Context(), tenant, targetID),
		"a target with nothing live left is safe to delete")
	require.NoError(t, ErasePrincipalData(t.Context(), env.db, env.prefix, principalID, provisioningErasureTime),
		"with the remote account deprovisioned there is nothing left to be accountable for")
}

func TestProvisioningDeleteTargetAllowsATargetThatNeverProvisionedAnything(t *testing.T) {
	env := newProvisioningEnvironment(t)
	tenant := testID("tenant-a")
	_, server := newSCIMProviderEndpoint(t, 200, `{"id":"remote-user"}`)
	target, err := env.service.CreateTarget(t.Context(), tenant, "Empty", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)

	// The guard has to stay out of the way of the ordinary case, or operators
	// learn to reach for whatever bypasses it.
	assert.NoError(t, env.service.DeleteTarget(t.Context(), tenant, parseGUID(target.Target.ID)))
}

func TestProvisioningErasePrincipalDataTombstonesMappingsAndSparesEveryoneElse(t *testing.T) {
	env := newProvisioningEnvironment(t)
	tenant := testID("tenant-a")
	provider, server := newSCIMProviderEndpoint(t, 200, `{"id":"remote-user"}`)
	target, err := env.service.CreateTarget(t.Context(), tenant, "Directory", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)
	targetID := parseGUID(target.Target.ID)

	erased, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("gone", "gone", "gone@example.test"))
	require.NoError(t, err)
	kept, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("kept", "kept", "kept@example.test"))
	require.NoError(t, err)
	erasedID, keptID := parseGUID(erased.ID), parseGUID(kept.ID)
	for _, id := range []guid.ID{erasedID, keptID} {
		_, err = env.service.PushResource(t.Context(), tenant, targetID, ResourceUser, id)
		require.NoError(t, err)
	}
	provider.configure(204, "", true)
	require.NoError(t, env.service.DeprovisionResource(t.Context(), tenant, targetID, ResourceUser, erasedID))

	require.NoError(t, ErasePrincipalData(t.Context(), env.db, env.prefix, erasedID, provisioningErasureTime))

	assert.Empty(t, readTargetExternalID(t, env, targetID, erasedID),
		"the remote identifier is the last thing tying the record to a person")
	assert.NotEmpty(t, readTargetExternalID(t, env, targetID, keptID),
		"an unrelated principal keeps the mapping its own remote account needs")
}

func readTargetExternalID(t *testing.T, env *provisioningEnvironment, targetID, resourceID guid.ID) string {
	t.Helper()
	var externalID string
	require.NoError(t, env.db.Table(env.prefix+"iam_scim_target_resources").
		Select("external_id").
		Where("target_id = ? AND resource_type = 'User' AND resource_id = ?", targetID, resourceID).
		Scan(&externalID).Error)
	return externalID
}

func TestRetireTargetRemovesRemoteAccountsBeforeTheMeansToReachThem(t *testing.T) {
	env := newProvisioningEnvironment(t)
	tenant := testID("tenant-a")
	provider, server := newSCIMProviderEndpoint(t, 200, `{"id":"remote-user"}`)
	target, err := env.service.CreateTarget(t.Context(), tenant, "Directory", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)
	targetID := parseGUID(target.Target.ID)

	principals := make([]guid.ID, 0, 3)
	for _, name := range []string{"one", "two", "three"} {
		user, createErr := env.service.CreateUser(t.Context(), env.auth, activeUserInput(name, name, name+"@example.test"))
		require.NoError(t, createErr)
		id := parseGUID(user.ID)
		principals = append(principals, id)
		_, err = env.service.PushResource(t.Context(), tenant, targetID, ResourceUser, id)
		require.NoError(t, err)
	}

	provider.configure(204, "", true)
	count, err := env.service.RetireTarget(t.Context(), tenant, targetID)
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	// Every principal is now free to be erased, which is the whole point: the
	// accountability the delete guard protects is discharged rather than lost.
	for _, id := range principals {
		assert.NoError(t, ErasePrincipalData(t.Context(), env.db, env.prefix, id, provisioningErasureTime))
	}
	assert.ErrorIs(t, env.service.DeleteTarget(t.Context(), tenant, targetID), ErrTargetMissing,
		"a retired target is gone")
}

func TestRetireTargetKeepsEverythingWhenTheRemoteRefuses(t *testing.T) {
	env := newProvisioningEnvironment(t)
	tenant := testID("tenant-a")
	provider, server := newSCIMProviderEndpoint(t, 200, `{"id":"remote-user"}`)
	target, err := env.service.CreateTarget(t.Context(), tenant, "Directory", server.URL, "token", &env.auth.PrincipalID)
	require.NoError(t, err)
	targetID := parseGUID(target.Target.ID)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("stuck", "stuck", "stuck@example.test"))
	require.NoError(t, err)
	principalID := parseGUID(user.ID)
	_, err = env.service.PushResource(t.Context(), tenant, targetID, ResourceUser, principalID)
	require.NoError(t, err)

	provider.configure(429, "remote rejected this request", true)
	count, err := env.service.RetireTarget(t.Context(), tenant, targetID)
	require.Error(t, err)
	assert.Zero(t, count)

	// A remote that would not delete the account is exactly when the local
	// record must survive: it is the only remaining evidence of what is over
	// there, and the token is the only way back to remove it.
	assert.ErrorIs(t, ErasePrincipalData(t.Context(), env.db, env.prefix, principalID, provisioningErasureTime),
		ErrPrincipalRemoteProvisioned)
	_, err = env.service.repo.getTarget(t.Context(), tenant, targetID)
	assert.NoError(t, err, "the target and its credentials outlive a failed retirement")

	// An explicit rejection permits retry once the provider accepts requests.
	provider.configure(204, "", true)
	count, err = env.service.RetireTarget(t.Context(), tenant, targetID)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}
