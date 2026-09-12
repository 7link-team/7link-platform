package authn

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/identity"
	identitymodel "7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/extension/passwordx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var repositoryTestNow = time.Date(2026, 9, 3, 12, 0, 0, 123_000_000, time.UTC)

func TestRepositoryExplicitAuthenticationTimestampsPostgresSQL(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sql_")
	connection, err := harness.db.DB()
	require.NoError(t, err)
	// Render through the real PostgreSQL dialect and real repository methods.
	// DryRun never executes PostgreSQL syntax on the fixture's SQLite connection.
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: connection}), &gorm.Config{
		DryRun: true, SkipDefaultTransaction: true, DisableAutomaticPing: true,
		NowFunc: func() time.Time { return repositoryTestNow.Add(time.Hour) },
		Logger:  logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	repo := harness.repository.withDB(db, true)
	var generated string
	var values []any
	var automaticTimestamp bool
	require.NoError(t, db.Callback().Update().After("gorm:update").Register("capture_authentication_timestamp_sql", func(tx *gorm.DB) {
		generated = tx.Statement.SQL.String()
		values = append([]any(nil), tx.Statement.Vars...)
		field := tx.Statement.Schema.LookUpField("UpdatedAt")
		automaticTimestamp = field != nil && field.AutoUpdateTime != 0
	}))
	for _, test := range []struct {
		name string
		run  func() error
	}{
		{"require MFA", func() error { return repo.requireMFA(t.Context(), 101, repositoryTestNow) }},
		{"enable MFA", func() error {
			_, err := repo.enableMFA(t.Context(), 101, "test-ciphertext", 1, repositoryTestNow)
			return err
		}},
		{"passkey counter", func() error {
			_, err := repo.updatePasskeyCounter(t.Context(), "test-hash", 1, 2, "old-ciphertext", "new-ciphertext", repositoryTestNow)
			return err
		}},
		{"passkey clone warning", func() error {
			_, err := repo.storePasskeyCloneWarning(t.Context(), "test-hash", "old-ciphertext", "new-ciphertext", repositoryTestNow)
			return err
		}},
		{"passkey name", func() error {
			_, _, err := repo.renamePasskey(t.Context(), 101, "test-hash", "Renamed", repositoryTestNow)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			generated, values, automaticTimestamp = "", nil, false
			require.NoError(t, test.run())
			require.True(t, automaticTimestamp, "the persisted model uses GORM's automatic UpdatedAt convention")
			require.True(t, strings.HasPrefix(generated, "UPDATE "), "must inspect an actual generated update")
			assert.Equal(t, 1, strings.Count(generated, `"updated_at"=`), "PostgreSQL rejects duplicate SET assignments")
			found := false
			for _, value := range values {
				if timestamp, ok := value.(time.Time); ok {
					found = true
					assert.True(t, timestamp.Equal(repositoryTestNow), "explicit operation time must not be replaced by the ORM clock")
				}
			}
			assert.True(t, found, "the operation must bind its explicit timestamp")
		})
	}
}

type repositoryTestIDs struct{ next atomic.Int64 }

func newRepositoryTestIDs(start int64) *repositoryTestIDs {
	ids := &repositoryTestIDs{}
	ids.next.Store(start)
	return ids
}

func (ids *repositoryTestIDs) Next() (int64, error) { return ids.next.Add(1), nil }

type authnRepositoryHarness struct {
	repository *Repository
	db         *gorm.DB
	runtime    *appenv.Runtime
	auditMu    sync.Mutex
	audits     []audit.DomainEvent
	auditDBs   []*gorm.DB
	auditErr   error
}

func newAuthnRepositoryHarness(t *testing.T, prefix string) *authnRepositoryHarness {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?_busy_timeout=10000&_journal_mode=WAL",
		filepath.ToSlash(filepath.Join(t.TempDir(), "authn-repository.sqlite")),
	)
	initializeAuthnRepositoryDatabase(t, dsn, prefix)
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: "sqlite", DSN: dsn, Writable: true, Readable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	harness := &authnRepositoryHarness{db: router.Write()}
	harness.runtime = appenv.NewRuntime(
		appenv.NewIdentity("authn-test", "1.0.0"),
		appenv.WithDatabase(router),
		appenv.WithTablePrefix(prefix),
	)
	appender := audit.Appender(func(_ context.Context, db *gorm.DB, event audit.DomainEvent) error {
		harness.auditMu.Lock()
		defer harness.auditMu.Unlock()
		harness.auditDBs = append(harness.auditDBs, db)
		harness.audits = append(harness.audits, event)
		return harness.auditErr
	})
	harness.repository = NewRepository(harness.runtime, newRepositoryTestIDs(1000), appender)
	// The repository a running deployment gets is the one its connectors bound;
	// a harness that expects an answer has to publish the owners' names too.
	bindForeignTables(t, harness.repository, prefix)
	return harness
}

func initializeAuthnRepositoryDatabase(t *testing.T, dsn, prefix string) {
	t.Helper()
	db, err := (gormx.Datasource{Type: "sqlite", DSN: dsn, Writable: true}).Open(t.Context())
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	for _, source := range []goosex.Source{
		(&identity.Module{}).MigrationSource(),
		(&Module{}).MigrationSource(),
	} {
		source.Prefix = prefix
		require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, db.Dialector.Name()))
	}
	statements := []string{
		"CREATE TABLE " + prefix + "iam_refresh_tokens (id_hash TEXT PRIMARY KEY, principal_id BIGINT NOT NULL, revoked_at DATETIME NULL)",
		"CREATE TABLE " + prefix + "iam_oauth_clients (id BIGINT PRIMARY KEY, tenant_id BIGINT NOT NULL, status TEXT NOT NULL)",
		"CREATE TABLE " + prefix + "iam_tenants (id BIGINT PRIMARY KEY, status TEXT NOT NULL)",
		"CREATE TABLE " + prefix + "iam_tenant_members (tenant_id BIGINT NOT NULL, principal_id BIGINT NOT NULL, status TEXT NOT NULL, PRIMARY KEY (tenant_id, principal_id))",
	}
	for _, statement := range statements {
		require.NoError(t, db.Exec(statement).Error)
	}
	require.NoError(t, sqlDB.Close())
}

func (h *authnRepositoryHarness) seedPrincipal(t *testing.T, principalID guid.ID, login string) {
	t.Helper()
	row := identitymodel.Principal{
		ID: principalID, LoginName: login, Email: login + "@example.test", DisplayName: login,
		Status: "active", EmailVerified: true, CreatedAt: repositoryTestNow, UpdatedAt: repositoryTestNow,
	}
	require.NoError(t, h.db.Table(h.runtime.TablePrefix()+row.TableName()).Create(&row).Error)
}

func (h *authnRepositoryHarness) seedCredential(t *testing.T, principalID guid.ID, hash string) {
	t.Helper()
	row := model.Credential{
		PrincipalID: principalID, PasswordHash: hash, PasswordChangedAt: repositoryTestNow,
		CredentialVersion: 1, UpdatedAt: repositoryTestNow,
	}
	require.NoError(t, h.db.Table(h.runtime.TablePrefix()+row.TableName()).Create(&row).Error)
}

func TestNewRepositoryRequiresDependencies(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("test", "1"))
	ids := newRepositoryTestIDs(1)
	appender := audit.Appender(func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	assert.Panics(t, func() { NewRepository(nil, ids, appender) })
	assert.Panics(t, func() { NewRepository(runtime, nil, appender) })
	assert.Panics(t, func() { NewRepository(runtime, ids, nil) })
}

func TestModuleMigrationContract(t *testing.T) {
	module := &Module{}
	assert.Equal(t, Name, module.ModuleName())
	assert.Equal(t, "goose_authn", module.MigrationSource().TableName)
	require.Len(t, module.MigrationMilestones(), 8)
	assert.Equal(t, "reauthentication", module.MigrationMilestones()[6].Name)
	assert.Equal(t, []string{"base", "constraints", "indexes", "verification-attempts", "notification-principals", "principal-erasure"}, []string{
		module.MigrationMilestones()[0].Name,
		module.MigrationMilestones()[1].Name,
		module.MigrationMilestones()[2].Name,
		module.MigrationMilestones()[3].Name,
		module.MigrationMilestones()[4].Name,
		module.MigrationMilestones()[5].Name,
	})
}

func TestRepositoryUnavailableAndIDBranches(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("test", "1"))
	repository := NewRepository(runtime, newRepositoryTestIDs(0), func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	_, _, err := repository.tables()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.ErrorIs(t, repository.runInTx(t.Context(), func(*Repository) error { return nil }), errRepositoryUnavailable)
	assert.Error(t, repository.runInTx(t.Context(), nil))
	assert.ErrorIs(t, repository.appendAudit(t.Context(), audit.DomainEvent{}), errRepositoryUnavailable)
	assert.Nil(t, (*Repository)(nil).writer())
	assert.Empty(t, (*Repository)(nil).prefix())
	assert.ErrorContains(t, (*Repository)(nil).runInTx(t.Context(), func(*Repository) error { return nil }), "invalid authentication transaction")
	assert.ErrorIs(t, (*Repository)(nil).appendAudit(t.Context(), audit.DomainEvent{}), errRepositoryUnavailable)
	_, err = (*Repository)(nil).nextID()
	assert.ErrorIs(t, err, errRepositoryUnavailable)

	zeroIDs := &repositoryIDFixture{id: 0}
	zero := NewRepository(runtime, zeroIDs, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	_, err = zero.nextID()
	assert.ErrorContains(t, err, "must be positive")
	failing := NewRepository(runtime, &repositoryIDFixture{err: errors.New("id failed")}, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	_, err = failing.nextID()
	assert.ErrorContains(t, err, "id failed")
}

func TestRepositoryMethodsFailClosedWithoutWriter(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("sl_"))
	repository := NewRepository(runtime, newRepositoryTestIDs(0), func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	now := repositoryTestNow
	principalID := guid.ID(1)
	checks := map[string]func() error{
		"pin writer":              func() error { _, err := repository.pinWriter(); return err },
		"find login principal":    func() error { _, err := repository.findLoginPrincipal(t.Context(), "alice"); return err },
		"get active principal":    func() error { _, err := repository.getActivePrincipal(t.Context(), principalID); return err },
		"find recovery principal": func() error { _, err := repository.findRecoveryPrincipal(t.Context(), "alice"); return err },
		"lock principal":          func() error { _, err := repository.lockPrincipal(t.Context(), principalID); return err },
		"lock principal by login": func() error { _, err := repository.lockPrincipalByLogin(t.Context(), "alice"); return err },
		"verify principal email": func() error {
			_, err := repository.verifyPrincipalEmail(t.Context(), principalID, "a@example.test", now)
			return err
		},
		"principal state":         func() error { _, err := repository.principalState(t.Context(), principalID); return err },
		"service account state":   func() error { _, err := repository.serviceAccountState(t.Context(), principalID); return err },
		"oauth client state":      func() error { _, err := repository.oauthClientState(t.Context(), principalID); return err },
		"active principal claims": func() error { _, _, err := repository.activePrincipalClaims(t.Context(), principalID); return err },
		"get credential":          func() error { _, err := repository.getCredential(t.Context(), principalID); return err },
		"create credential":       func() error { return repository.createCredential(t.Context(), &model.Credential{}) },
		"reset bootstrap credential": func() error {
			_, err := repository.resetBootstrapCredential(t.Context(), principalID, nil, false, now)
			return err
		},
		"get MFA credential": func() error { _, err := repository.getMFAEnabledCredential(t.Context(), principalID); return err },
		"record login failure": func() error {
			return repository.recordLoginFailure(t.Context(), principalID, 5, time.Minute, now)
		},
		"require MFA":  func() error { return repository.requireMFA(t.Context(), principalID, now) },
		"enable MFA":   func() error { _, err := repository.enableMFA(t.Context(), principalID, "cipher", 1, now); return err },
		"disable MFA":  func() error { return repository.disableMFA(t.Context(), principalID, now) },
		"advance TOTP": func() error { _, err := repository.advanceTOTPStep(t.Context(), principalID, 1); return err },
		"update password": func() error {
			_, err := repository.updatePassword(t.Context(), principalID, "old", "new", now, false, nil)
			return err
		},
		"recovery cooldown":       func() error { _, err := repository.recoveryCooldown(t.Context(), principalID); return err },
		"list password history":   func() error { _, err := repository.listPasswordHistory(t.Context(), principalID, 5); return err },
		"append password history": func() error { return repository.appendPasswordHistory(t.Context(), principalID, "hash", now) },
		"trim password history":   func() error { return repository.trimPasswordHistory(t.Context(), principalID, 5) },
		"active session":          func() error { _, err := repository.activeSession(t.Context(), "session", now); return err },
		"touch session": func() error {
			_, err := repository.touchSession(t.Context(), "session", now, now)
			return err
		},
		"create session":        func() error { return repository.createSession(t.Context(), &model.Session{}) },
		"list sessions":         func() error { _, err := repository.listActiveSessions(t.Context(), principalID, now); return err },
		"revoke session":        func() error { _, err := repository.revokeSession(t.Context(), principalID, "session", now); return err },
		"revoke sessions":       func() error { _, err := repository.revokeSessions(t.Context(), principalID, "", now); return err },
		"revoke refresh tokens": func() error { _, err := repository.revokeRefreshTokens(t.Context(), principalID, now); return err },
		"revoke authentication": func() error { return repository.revokeOtherAuthentication(t.Context(), principalID, "", now) },
		"rotate session": func() error {
			_, _, err := repository.rotateSession(t.Context(), principalID, "old", &model.Session{}, now)
			return err
		},
		"replace MFA enrollment": func() error { return repository.replaceMFAEnrollment(t.Context(), &model.MFAEnrollment{}) },
		"active MFA enrollment":  func() error { _, err := repository.activeMFAEnrollment(t.Context(), principalID, now); return err },
		"active MFA enrollment for update": func() error {
			_, err := repository.activeMFAEnrollmentForUpdate(t.Context(), principalID, now)
			return err
		},
		"delete MFA enrollment":  func() error { return repository.deleteMFAEnrollment(t.Context(), principalID) },
		"count recovery codes":   func() error { _, err := repository.countAvailableRecoveryCodes(t.Context(), principalID); return err },
		"replace recovery codes": func() error { return repository.replaceRecoveryCodes(t.Context(), principalID, nil) },
		"consume recovery code": func() error {
			_, err := repository.consumeRecoveryCode(t.Context(), principalID, []string{"code"}, now)
			return err
		},
		"create MFA challenge":  func() error { return repository.createMFAChallenge(t.Context(), &model.MFAChallenge{}, now) },
		"get MFA challenge":     func() error { _, err := repository.getMFAChallenge(t.Context(), "challenge"); return err },
		"fail MFA challenge":    func() error { _, err := repository.failMFAChallenge(t.Context(), "challenge", 0, 1, now); return err },
		"consume MFA challenge": func() error { _, err := repository.consumeMFAChallenge(t.Context(), "challenge", now); return err },
		"purge MFA":             func() error { return repository.purgeMFAState(t.Context(), principalID) },
		"list passkeys":         func() error { _, err := repository.listPasskeys(t.Context(), principalID); return err },
		"count passkeys":        func() error { _, err := repository.countPasskeys(t.Context(), principalID); return err },
		"create passkey":        func() error { _, err := repository.createPasskey(t.Context(), &model.Passkey{}, 1); return err },
		"get passkey":           func() error { _, err := repository.getPasskey(t.Context(), "passkey"); return err },
		"lock passkey": func() error {
			_, err := repository.lockPasskey(t.Context(), principalID, "passkey")
			return err
		},
		"update passkey": func() error {
			_, err := repository.updatePasskeyCounter(t.Context(), "passkey", 0, 1, "old", "cipher", now)
			return err
		},
		"store clone warning": func() error {
			_, err := repository.storePasskeyCloneWarning(t.Context(), "passkey", "old", "cipher", now)
			return err
		},
		"rename passkey": func() error {
			_, _, err := repository.renamePasskey(t.Context(), principalID, "passkey", "name", now)
			return err
		},
		"delete passkey":      func() error { _, err := repository.deletePasskey(t.Context(), principalID, "passkey"); return err },
		"ensure passkey user": func() error { _, err := repository.ensurePasskeyUser(t.Context(), &model.PasskeyUser{}); return err },
		"get passkey user":    func() error { _, err := repository.getPasskeyUser(t.Context(), principalID); return err },
		"find passkey user": func() error {
			_, err := repository.findPasskeyUserByHandle(t.Context(), principalID, "handle")
			return err
		},
		"create passkey challenge": func() error { return repository.createPasskeyChallenge(t.Context(), &model.PasskeyChallenge{}, now) },
		"get passkey challenge": func() error {
			_, err := repository.getPasskeyChallenge(t.Context(), "challenge", "login", 0)
			return err
		},
		"consume passkey challenge": func() error {
			_, err := repository.consumePasskeyChallenge(t.Context(), "challenge", "login", 0, now)
			return err
		},
		"create password recovery": func() error {
			return repository.createPasswordRecovery(t.Context(), &model.PasswordRecoveryToken{}, now)
		},
		"active password recovery": func() error {
			_, err := repository.activePasswordRecovery(t.Context(), []string{"token"}, now)
			return err
		},
		"consume password recovery": func() error { _, err := repository.consumePasswordRecovery(t.Context(), "token", now); return err },
		"consume recoveries":        func() error { return repository.consumePasswordRecoveries(t.Context(), principalID, now) },
		"enqueue notification":      func() error { return repository.enqueueNotification(t.Context(), &model.NotificationOutbox{}) },
		"reset stale notifications": func() error { return repository.resetStaleNotifications(t.Context(), now) },
		"pending notifications":     func() error { _, err := repository.pendingNotificationIDs(t.Context(), now, 20); return err },
		"claim notification": func() error {
			_, _, err := repository.claimNotification(t.Context(), principalID, now)
			return err
		},
		"mark notification sent": func() error {
			_, err := repository.markNotificationSent(t.Context(), principalID, 1, now)
			return err
		},
		"mark notification failed": func() error {
			_, err := repository.markNotificationFailed(t.Context(), principalID, 1, "failed", "error", now, now)
			return err
		},
		"replace email verification": func() error {
			return repository.replaceEmailVerification(t.Context(), &model.EmailVerificationToken{}, now)
		},
		"create email verification":   func() error { return repository.createEmailVerification(t.Context(), &model.EmailVerificationToken{}) },
		"consume email verifications": func() error { return repository.consumeEmailVerifications(t.Context(), principalID, now) },
		"email verification by HMAC": func() error {
			_, err := repository.activeEmailVerificationByHMAC(t.Context(), []string{"token"}, now)
			return err
		},
		"email verification by code": func() error {
			_, err := repository.activeEmailVerificationByEmailAndCode(t.Context(), "email", "code", 5, now)
			return err
		},
		"record email verification attempt": func() error {
			return repository.recordEmailVerificationAttempt(t.Context(), "email", 5, now)
		},
		"consume email verification": func() error {
			_, err := repository.consumeEmailVerification(t.Context(), "token", 5, now)
			return err
		},
		"latest verification": func() error { _, err := repository.latestEmailVerificationTime(t.Context(), principalID); return err },
		"create step-up":      func() error { return repository.createStepUpChallenge(t.Context(), &model.StepUpChallenge{}, now) },
		"get step-up":         func() error { _, err := repository.getStepUpChallenge(t.Context(), "challenge"); return err },
		"fail step-up": func() error {
			_, err := repository.failStepUpChallenge(t.Context(), "challenge", 0, 1, now)
			return err
		},
		"consume step-up": func() error {
			_, err := repository.consumeStepUpChallenge(t.Context(), "challenge", principalID, "session", now)
			return err
		},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, check(), errRepositoryUnavailable)
		})
	}
}

type repositoryIDFixture struct {
	id  int64
	err error
}

func (f *repositoryIDFixture) Next() (int64, error) { return f.id, f.err }

func TestRepositoryTransactionPinsDBAndRollsBackAuditFailure(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	harness.auditErr = errors.New("audit denied")

	err := harness.repository.runInTx(t.Context(), func(repository *Repository) error {
		pinned := repository.writer()
		require.NotNil(t, pinned)
		assert.Same(t, pinned, repository.writer())
		harness.seedCredentialWithRepository(t, repository, 1, "hash")
		return repository.appendAudit(t.Context(), audit.DomainEvent{PrincipalID: 1, EventType: "test"})
	})
	assert.ErrorContains(t, err, "audit denied")
	require.Len(t, harness.auditDBs, 1)
	assert.NotSame(t, harness.db, harness.auditDBs[0])
	var count int64
	require.NoError(t, harness.db.Table("sl_iam_credentials").Where("principal_id = ?", 1).Count(&count).Error)
	assert.Zero(t, count)

	harness.auditErr = nil
	require.NoError(t, harness.repository.runInTx(t.Context(), func(repository *Repository) error {
		return repository.runInTx(t.Context(), func(nested *Repository) error {
			assert.Same(t, repository, nested)
			harness.seedCredentialWithRepository(t, nested, 1, "hash")
			return nested.appendAudit(t.Context(), audit.DomainEvent{PrincipalID: 1, EventType: "committed"})
		})
	}))
	require.NoError(t, harness.db.Table("sl_iam_credentials").Where("principal_id = ?", 1).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func (h *authnRepositoryHarness) seedCredentialWithRepository(t *testing.T, repository *Repository, principalID guid.ID, hash string) {
	t.Helper()
	tables, _, err := repository.tables()
	require.NoError(t, err)
	require.NoError(t, tables.Credentials.WithContext(t.Context()).Create(&model.Credential{
		PrincipalID: principalID, PasswordHash: hash, PasswordChangedAt: repositoryTestNow,
		CredentialVersion: 1, UpdatedAt: repositoryTestNow,
	}))
}

// seedLocalPrincipal writes a principal and its password credential directly.
// A deployment creates those two rows through two capabilities in one
// transaction -- identity writes the principal, authn writes the credential,
// sequenced by the composition root -- so authn has no function that writes
// both, and a harness that needs somebody who can sign in seeds them itself.
func seedLocalPrincipal(t *testing.T, repository *Repository, login, email, password string) guid.ID {
	t.Helper()
	require.NotNil(t, repository)
	db := repository.writer()
	require.NotNil(t, db)
	principalID, err := repository.nextID()
	require.NoError(t, err)
	hash, err := passwordx.Hash(password)
	require.NoError(t, err)

	// Stored normalized, the way every path that creates a principal stores it:
	// a login is matched after normalization, so a fixture that skipped this
	// would be unreachable by its own login name.
	principal := identitymodel.Principal{
		ID:    principalID,
		Email: strings.ToLower(strings.TrimSpace(email)), DisplayName: login,
		LoginName: normalizeLogin(login),
		Status:    "active", EmailVerified: email != "",
		CreatedAt: repositoryTestNow, UpdatedAt: repositoryTestNow,
	}
	require.NoError(t, db.Table(repository.prefix()+principal.TableName()).Create(&principal).Error)
	credential := model.Credential{
		PrincipalID: principalID, PasswordHash: hash, PasswordChangedAt: repositoryTestNow,
		CredentialVersion: 1, UpdatedAt: repositoryTestNow,
	}
	require.NoError(t, db.Table(repository.prefix()+credential.TableName()).Create(&credential).Error)
	return principalID
}

// principal reads a principal row straight from storage. identity owns that
// table, so authn has no method that reads it by id; a test that wants to see
// what a flow left behind reads the row itself.
func (h *authnRepositoryHarness) principal(t *testing.T, principalID guid.ID) identitymodel.Principal {
	t.Helper()
	var row identitymodel.Principal
	require.NoError(t, h.db.Table(h.runtime.TablePrefix()+row.TableName()).
		Where("id = ?", principalID).Take(&row).Error)
	return row
}
