package authn

import (
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/identity"
	identitymodel "7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRepositoryIdentityReadsRespectIdentityOwnershipAndPrefix(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	harness.seedPrincipal(t, 2, "worker")
	harness.seedPrincipal(t, 3, "pending")
	harness.seedCredential(t, 1, "alice-hash")

	require.NoError(t, harness.db.Table("sl_iam_service_accounts").Create(&identitymodel.ServiceAccount{
		PrincipalID: 2, OwnerTenantID: 10, Status: "active", TokenVersion: 7, Version: 1,
		CreatedAt: repositoryTestNow, UpdatedAt: repositoryTestNow,
	}).Error)
	require.NoError(t, harness.db.Table("sl_iam_tenants").Create(map[string]any{"id": 10, "status": "active"}).Error)
	require.NoError(t, harness.db.Table("sl_iam_tenant_members").Create(map[string]any{
		"tenant_id": 10, "principal_id": 2, "status": "active",
	}).Error)
	require.NoError(t, harness.db.Table("sl_iam_oauth_clients").Create(map[string]any{
		"id": 20, "tenant_id": 10, "status": "active",
	}).Error)

	principal, err := harness.repository.findLoginPrincipal(t.Context(), "alice")
	require.NoError(t, err)
	assert.Equal(t, guid.ID(1), principal.ID)
	_, err = harness.repository.findLoginPrincipal(t.Context(), "worker")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	principal, err = harness.repository.getPrincipal(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, "alice", principal.LoginName)
	principal, err = harness.repository.getActivePrincipal(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(1), principal.ID)
	principal, err = harness.repository.findRecoveryPrincipal(t.Context(), "ALICE@EXAMPLE.TEST")
	require.NoError(t, err)
	assert.Equal(t, guid.ID(1), principal.ID)

	locked, err := harness.repository.lockPrincipal(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(1), locked.ID)
	locked, err = harness.repository.lockPrincipalByLogin(t.Context(), "alice")
	require.NoError(t, err)
	assert.Equal(t, guid.ID(1), locked.ID)

	state, err := harness.repository.principalState(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, principalCredentialState{Status: "active", CredentialVersion: 1}, state)
	account, err := harness.repository.serviceAccountState(t.Context(), 2)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(10), account.TenantID)
	assert.Equal(t, "active", account.AccountStatus)
	assert.Equal(t, "active", account.PrincipalStatus)
	assert.Equal(t, "active", account.MemberStatus)
	assert.Equal(t, "active", account.TenantStatus)
	assert.Nil(t, account.ExpiresAt)
	assert.Equal(t, int64(7), account.TokenVersion)
	client, err := harness.repository.oauthClientState(t.Context(), 20)
	require.NoError(t, err)
	assert.Equal(t, oauthClientState{TenantID: 10, Status: "active"}, client)

	claimsPrincipal, version, err := harness.repository.activePrincipalClaims(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(1), claimsPrincipal.ID)
	assert.Equal(t, int64(1), version)
	claimsPrincipal, version, err = harness.repository.activePrincipalClaims(t.Context(), 2)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(2), claimsPrincipal.ID)
	assert.Zero(t, version)

	require.NoError(t, harness.db.Table("sl_iam_principals").Where("id = ?", 3).
		Updates(map[string]any{"activation_required": true}).Error)
	_, _, err = harness.repository.activePrincipalClaims(t.Context(), 3)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestRepositoryConcurrentLoginFailuresDoNotLoseAttempts(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	harness.seedCredential(t, 1, "hash")
	const attempts = 20
	start := make(chan struct{})
	errs := make(chan error, attempts)
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errs <- harness.repository.recordLoginFailure(t.Context(), 1, 100, time.Minute, repositoryTestNow)
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	credential, err := harness.repository.getCredential(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, attempts, credential.FailedAttempts)
	assert.Nil(t, credential.LockedUntil)
}

func TestRepositoryVerifiesEmailOnlyAgainstBoundSnapshot(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	require.NoError(t, harness.db.Table("sl_iam_principals").Where("id = ?", 1).
		Updates(map[string]any{"email_verified": false, "activation_required": true}).Error)

	changed, err := harness.repository.verifyPrincipalEmail(t.Context(), 1, "other@example.test", repositoryTestNow.Add(time.Minute))
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = harness.repository.verifyPrincipalEmail(t.Context(), 1, "alice@example.test", repositoryTestNow.Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = harness.repository.verifyPrincipalEmail(t.Context(), 1, "alice@example.test", repositoryTestNow.Add(2*time.Minute))
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestRepositoryBootstrapMutationsUseIdentityModelsAndNullableState(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	disabledAt := repositoryTestNow.Add(-time.Hour)
	principal := &identity.Principal{
		ID: 4, LoginName: "bootstrap", Email: "old@example.test", DisplayName: "Old",
		Status: "disabled", CreatedAt: repositoryTestNow, UpdatedAt: repositoryTestNow,
	}
	require.NoError(t, harness.repository.createPrincipal(t.Context(), principal))
	require.NoError(t, harness.db.Table("sl_iam_principals").Where("id = ?", 4).Update("disabled_at", disabledAt).Error)
	require.NoError(t, harness.repository.createCredential(t.Context(), &model.Credential{
		PrincipalID: 4, PasswordHash: "old", PasswordChangedAt: repositoryTestNow,
		CredentialVersion: 1, UpdatedAt: repositoryTestNow,
	}))
	now := repositoryTestNow.Add(time.Hour)
	require.NoError(t, harness.repository.updateBootstrapPrincipal(
		t.Context(), 4, "new@example.test", "Bootstrap", true, now,
	))
	newHash := "new"
	changed, err := harness.repository.resetBootstrapCredential(t.Context(), 4, &newHash, true, now)
	require.NoError(t, err)
	assert.True(t, changed)

	loaded, err := harness.repository.getPrincipal(t.Context(), 4)
	require.NoError(t, err)
	assert.Equal(t, "active", loaded.Status)
	assert.Equal(t, "new@example.test", loaded.Email)
	assert.True(t, loaded.EmailVerified)
	var storedState struct {
		DisabledAt *time.Time
	}
	require.NoError(t, harness.db.Table("sl_iam_principals").Select("disabled_at").Where("id = ?", 4).
		Take(&storedState).Error)
	assert.Nil(t, storedState.DisabledAt)
	credential, err := harness.repository.getCredential(t.Context(), 4)
	require.NoError(t, err)
	assert.Equal(t, "new", credential.PasswordHash)
	assert.Equal(t, int64(2), credential.CredentialVersion)
	changed, err = harness.repository.resetBootstrapCredential(t.Context(), 99, nil, false, now)
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestRepositoryCredentialCASAndPasswordHistory(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	harness.seedCredential(t, 1, "old-hash")
	now := repositoryTestNow.Add(time.Hour)

	credential, err := harness.repository.getCredential(t.Context(), 1)
	require.NoError(t, err)
	assert.Nil(t, credential.LockedUntil)
	_, err = harness.repository.getMFAEnabledCredential(t.Context(), 1)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	for range 4 {
		require.NoError(t, harness.repository.recordLoginFailure(t.Context(), 1, 5, time.Minute, now))
	}
	credential, err = harness.repository.getCredential(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, 4, credential.FailedAttempts)
	assert.Nil(t, credential.LockedUntil)
	require.NoError(t, harness.repository.recordLoginFailure(t.Context(), 1, 5, time.Minute, now))
	credential, err = harness.repository.getCredential(t.Context(), 1)
	require.NoError(t, err)
	assert.Zero(t, credential.FailedAttempts)
	require.NotNil(t, credential.LockedUntil)
	assert.Equal(t, now.Add(time.Minute), *credential.LockedUntil)
	require.NoError(t, harness.repository.resetLoginFailures(t.Context(), 1, now.Add(time.Minute)))
	credential, err = harness.repository.getCredential(t.Context(), 1)
	require.NoError(t, err)
	assert.Zero(t, credential.FailedAttempts)
	assert.Nil(t, credential.LockedUntil)

	require.NoError(t, harness.repository.requireMFA(t.Context(), 1, now))
	credential, err = harness.repository.getMFAEnabledCredential(t.Context(), 1)
	require.NoError(t, err)
	assert.True(t, credential.MFARequired)
	require.NoError(t, harness.repository.disableMFA(t.Context(), 1, now))
	enabled, err := harness.repository.enableMFA(t.Context(), 1, "ciphertext", 10, now)
	require.NoError(t, err)
	assert.True(t, enabled)
	enabled, err = harness.repository.enableMFA(t.Context(), 1, "other", 11, now)
	require.NoError(t, err)
	assert.False(t, enabled)
	advanced, err := harness.repository.advanceTOTPStep(t.Context(), 1, 11)
	require.NoError(t, err)
	assert.True(t, advanced)
	advanced, err = harness.repository.advanceTOTPStep(t.Context(), 1, 11)
	require.NoError(t, err)
	assert.False(t, advanced)

	cooldown := now.Add(24 * time.Hour)
	changed, err := harness.repository.updatePassword(t.Context(), 1, "wrong", "new", now, true, &cooldown)
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = harness.repository.updatePassword(t.Context(), 1, "old-hash", "new-hash", now, true, &cooldown)
	require.NoError(t, err)
	assert.True(t, changed)
	credential, err = harness.repository.getCredential(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, "new-hash", credential.PasswordHash)
	assert.Equal(t, int64(2), credential.CredentialVersion)
	until, err := harness.repository.recoveryCooldown(t.Context(), 1)
	require.NoError(t, err)
	require.NotNil(t, until)
	assert.Equal(t, cooldown, *until)

	for index := range 7 {
		require.NoError(t, harness.repository.appendPasswordHistory(t.Context(), 1, "hash", now.Add(time.Duration(index)*time.Minute)))
	}
	history, err := harness.repository.listPasswordHistory(t.Context(), 1, 5)
	require.NoError(t, err)
	require.Len(t, history, 5)
	assert.True(t, history[0].CreatedAt.After(history[1].CreatedAt))
	require.NoError(t, harness.repository.trimPasswordHistory(t.Context(), 1, 5))
	history, err = harness.repository.listPasswordHistory(t.Context(), 1, 0)
	require.NoError(t, err)
	assert.Len(t, history, 5)
}
