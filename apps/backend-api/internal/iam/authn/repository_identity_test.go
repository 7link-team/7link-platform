package authn

import (
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	identitymodel "7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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
	_, err = harness.repository.findRecoveryPrincipal(t.Context(), "WORKER@EXAMPLE.TEST")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	assert.Equal(t, "alice", harness.principal(t, 1).LoginName)
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

func TestRepositoryRecoveryIdentifierMustResolveOneHumanPrincipal(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	harness.seedPrincipal(t, 2, "bob")
	require.NoError(t, harness.db.Table("sl_iam_principals").Where("id = ?", 1).
		Update("email", "Alice@Example.Test").Error)

	principal, err := harness.repository.findRecoveryPrincipal(t.Context(), "ALICE@EXAMPLE.TEST")
	require.NoError(t, err)
	assert.Equal(t, guid.ID(1), principal.ID)

	require.NoError(t, harness.db.Table("sl_iam_principals").Where("id = ?", 2).
		Update("email", "alice@example.test").Error)

	principal, err = harness.repository.findRecoveryPrincipal(t.Context(), "ALICE@EXAMPLE.TEST")
	assert.ErrorIs(t, err, errAmbiguousRecoveryPrincipal)
	assert.Nil(t, principal)
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

// TestBootstrapCredentialResetClearsNullableSecurityState covers the one
// bootstrap mutation authn still owns: resetting the credential of a platform
// administrator, which internal/app drives through ResetBootstrapCredentialOn.
// The principal row it reads belongs to identity, so the fixture seeds it
// rather than asking authn to write it.
func TestBootstrapCredentialResetClearsNullableSecurityState(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	disabledAt := repositoryTestNow.Add(-time.Hour)
	require.NoError(t, harness.db.Table("sl_iam_principals").Create(&identitymodel.Principal{
		ID: 4, LoginName: "bootstrap", Email: "new@example.test", DisplayName: "Bootstrap",
		Status: "active", EmailVerified: true, CreatedAt: repositoryTestNow, UpdatedAt: repositoryTestNow,
	}).Error)
	require.NoError(t, harness.db.Table("sl_iam_principals").Where("id = ?", 4).
		Update("disabled_at", disabledAt).Error)
	require.NoError(t, harness.repository.createCredential(t.Context(), &model.Credential{
		PrincipalID: 4, PasswordHash: "old", PasswordChangedAt: repositoryTestNow,
		CredentialVersion: 1, UpdatedAt: repositoryTestNow,
	}))

	newHash := "new"
	changed, err := harness.repository.resetBootstrapCredential(
		t.Context(), 4, &newHash, true, repositoryTestNow.Add(time.Hour),
	)
	require.NoError(t, err)
	assert.True(t, changed)

	loaded := harness.principal(t, 4)
	assert.Equal(t, "active", loaded.Status)
	assert.Equal(t, "new@example.test", loaded.Email)
	assert.True(t, loaded.EmailVerified)
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

// serviceAccountExistsSubquery is emitted as its own statement: gorm builds a
// NOT EXISTS argument through a separate Statement, so a call that reads
// principals while excluding service accounts sends two, in this order.
const serviceAccountExistsSubquery = `SELECT 1 FROM sl_iam_service_accounts AS service_account ` +
	`WHERE service_account.principal_id = principal.id`

// frozenIdentitySQL is the exact text of every statement authn runs against a
// table identity owns, in the order one call emits them. Two of them -- the
// principal/credential joins -- are also pinned by the query-plan fingerprints
// in internal/app/testdata/iam_performance_baseline.json, and that comparison
// only runs against an external MySQL or PostgreSQL instance, which `make ci`
// does not provide. Freezing the text here is what makes a drift visible to
// `go test ./...` rather than only to the external run.
var frozenIdentitySQL = map[string][]string{
	"lockPrincipal": {
		`SELECT * FROM "sl_iam_principals" WHERE id = $1 AND status = $2 LIMIT $3 FOR UPDATE`,
	},
	"lockPrincipalByLogin": {
		`SELECT * FROM "sl_iam_principals" WHERE login_name = $1 LIMIT $2 FOR UPDATE`,
	},
	"getActivePrincipal": {
		`SELECT * FROM "sl_iam_principals" WHERE id = $1 AND status = $2 LIMIT $3`,
	},
	"verifyPrincipalEmail": {
		`UPDATE "sl_iam_principals" SET "activation_required"=$1,"email_verified"=$2,` +
			`"updated_at"=$3 WHERE id = $4 AND status = $5 AND email = $6 AND email_verified = $7`,
	},
	"findLoginPrincipal": {
		serviceAccountExistsSubquery,
		`SELECT * FROM sl_iam_principals AS principal WHERE principal.login_name = $1 ` +
			`AND NOT EXISTS (` + serviceAccountExistsSubquery + `) LIMIT $2`,
	},
	"findRecoveryPrincipal": {
		serviceAccountExistsSubquery,
		`SELECT * FROM sl_iam_principals AS principal ` +
			`WHERE (status = $1 AND email_verified = $2) ` +
			`AND (LOWER(principal.login_name) = $3 OR LOWER(principal.email) = $4) ` +
			`AND NOT EXISTS (` + serviceAccountExistsSubquery + `) LIMIT $5`,
	},
	"principalState": {
		`SELECT principal.status AS status, ` +
			`COALESCE(credential.credential_version, 0) AS credential_version ` +
			`FROM sl_iam_principals AS principal ` +
			`LEFT JOIN sl_iam_credentials AS credential ON credential.principal_id = principal.id ` +
			`WHERE principal.id = $1 LIMIT $2`,
	},
	"activePrincipalClaims": {
		`SELECT principal.id, principal.login_name, principal.email, ` +
			`principal.email_verified, principal.activation_required, principal.display_name, ` +
			`principal.status, principal.created_at, principal.updated_at, ` +
			`COALESCE(credential.credential_version, 0) AS credential_version ` +
			`FROM sl_iam_principals AS principal ` +
			`LEFT JOIN sl_iam_credentials AS credential ON credential.principal_id = principal.id ` +
			`WHERE principal.id = $1 AND principal.status = $2 AND principal.activation_required = $3 LIMIT $4`,
	},
	"serviceAccountState": {
		`SELECT account.owner_tenant_id AS tenant_id, account.status AS account_status, ` +
			`account.expires_at, account.token_version, principal.status AS principal_status, ` +
			`member.status AS member_status, tenant.status AS tenant_status ` +
			`FROM sl_iam_service_accounts AS account ` +
			`JOIN sl_iam_principals AS principal ON principal.id = account.principal_id ` +
			`JOIN sl_iam_tenant_members AS member ON member.tenant_id = account.owner_tenant_id ` +
			`AND member.principal_id = account.principal_id ` +
			`JOIN sl_iam_tenants AS tenant ON tenant.id = account.owner_tenant_id ` +
			`WHERE account.principal_id = $1 LIMIT $2`,
	},
}

// newFrozenSQLRecorder renders through the real PostgreSQL dialect with DryRun,
// so the recorded text is what a deployment sends rather than what SQLite
// accepts. It returns the repository to call and a function that yields the
// statements the last call produced, in order, and resets the recording.
func newFrozenSQLRecorder(t *testing.T, harness *authnRepositoryHarness) (*Repository, func() []string) {
	t.Helper()
	connection, err := harness.db.DB()
	require.NoError(t, err)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: connection}), &gorm.Config{
		DryRun: true, SkipDefaultTransaction: true, DisableAutomaticPing: true,
		NowFunc: func() time.Time { return repositoryTestNow },
		Logger:  logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	var recorded []string
	capture := func(tx *gorm.DB) { recorded = append(recorded, tx.Statement.SQL.String()) }
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("freeze_identity_query", capture))
	require.NoError(t, db.Callback().Update().After("gorm:update").Register("freeze_identity_update", capture))
	return harness.repository.withDB(db, true), func() []string {
		statements := recorded
		recorded = nil
		return statements
	}
}

func TestStatementsAgainstIdentityTablesAreFrozen(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	repo, recorded := newFrozenSQLRecorder(t, harness)

	for name, run := range map[string]func() error{
		"lockPrincipal":         func() error { _, e := repo.lockPrincipal(t.Context(), 101); return e },
		"lockPrincipalByLogin":  func() error { _, e := repo.lockPrincipalByLogin(t.Context(), "alice"); return e },
		"getActivePrincipal":    func() error { _, e := repo.getActivePrincipal(t.Context(), 101); return e },
		"findLoginPrincipal":    func() error { _, e := repo.findLoginPrincipal(t.Context(), "alice"); return e },
		"findRecoveryPrincipal": func() error { _, e := repo.findRecoveryPrincipal(t.Context(), "a@b.test"); return e },
		"principalState":        func() error { _, e := repo.principalState(t.Context(), 101); return e },
		"serviceAccountState":   func() error { _, e := repo.serviceAccountState(t.Context(), 101); return e },
		"activePrincipalClaims": func() error { _, _, e := repo.activePrincipalClaims(t.Context(), 101); return e },
		"verifyPrincipalEmail": func() error {
			_, e := repo.verifyPrincipalEmail(t.Context(), 101, "a@b.test", repositoryTestNow)
			return e
		},
	} {
		t.Run(name, func(t *testing.T) {
			_ = run()
			assert.Equal(t, frozenIdentitySQL[name], recorded())
		})
	}
}

// TestSecurityStateWritesRefuseADisabledPrincipal is the invariant every
// lockPrincipal caller relies on: a principal that is absent or no longer
// active must stop the operation, so a disabled account cannot be issued a
// recovery token, enrolled in MFA, sent an email verification, or given a
// passkey. Six of the seven callers discard the returned row and keep only the
// error, so this is the only place the guard is observable -- and the only
// thing standing between a refactor and a disabled account walking through
// password recovery.
func TestSecurityStateWritesRefuseADisabledPrincipal(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	require.NoError(t, harness.db.Table("sl_iam_principals").
		Where("id = ?", 1).Update("status", "disabled").Error)
	repo := harness.repository

	for name, run := range map[string]func() error{
		"password recovery": func() error {
			return repo.createPasswordRecovery(t.Context(), &model.PasswordRecoveryToken{
				PrincipalID: 1, TokenHMAC: "recovery-hash", ExpiresAt: repositoryTestNow.Add(time.Hour),
				CreatedAt: repositoryTestNow,
			}, repositoryTestNow)
		},
		"email verification": func() error {
			return repo.replaceEmailVerification(t.Context(), &model.EmailVerificationToken{
				PrincipalID: 1, Email: "alice@example.test", TokenHMAC: "verify-hash",
				ExpiresAt: repositoryTestNow.Add(time.Hour), CreatedAt: repositoryTestNow,
			}, repositoryTestNow)
		},
		"mfa enrollment": func() error {
			return repo.replaceMFAEnrollment(t.Context(), &model.MFAEnrollment{
				PrincipalID: 1, SecretCiphertext: "cipher",
				CreatedAt: repositoryTestNow, ExpiresAt: repositoryTestNow.Add(time.Hour),
			})
		},
		"mfa enrollment lookup": func() error {
			_, err := repo.activeMFAEnrollmentForUpdate(t.Context(), 1, repositoryTestNow)
			return err
		},
		"passkey": func() error {
			_, err := repo.createPasskey(t.Context(), &model.Passkey{
				IDHash: "passkey-hash", PrincipalID: 1, Name: "laptop",
				CredentialCiphertext: "cipher",
				CreatedAt:            repositoryTestNow, UpdatedAt: repositoryTestNow,
			}, 5)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, run(), gorm.ErrRecordNotFound)
		})
	}
}

// TestRecoveryIdentifierWithNoMatchReportsNotFound covers the zero-row arm of
// findRecoveryPrincipal. The one-row and two-row arms are covered by
// TestRepositoryRecoveryIdentifierMustResolveOneHumanPrincipal; without this
// one, a refactor could collapse "nobody" into "ambiguous" and the suite would
// stay green.
func TestRecoveryIdentifierWithNoMatchReportsNotFound(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")

	_, err := harness.repository.findRecoveryPrincipal(t.Context(), "nobody@example.test")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.NotErrorIs(t, err, errAmbiguousRecoveryPrincipal)
}
