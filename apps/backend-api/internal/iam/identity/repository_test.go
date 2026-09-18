package identity

import (
	"7link.com/platform/internal/files"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var identityTestNow = time.Date(2026, 9, 3, 12, 0, 0, 123_000_000, time.UTC)

type identityTestIDs struct{ next atomic.Int64 }

func newIdentityTestIDs(start int64) *identityTestIDs {
	ids := &identityTestIDs{}
	ids.next.Store(start)
	return ids
}

func (ids *identityTestIDs) Next() (int64, error) { return ids.next.Add(1), nil }

// failingIDSource proves an ID generator failure aborts the workflow instead of
// writing a row with a zero identifier.
type failingIDSource struct{}

func (failingIDSource) Next() (int64, error) { return 0, errors.New("id source down") }

var _ guid.Source = failingIDSource{}

// guardCall records one administrator-guard invocation so tests can assert both
// the protected tenants and the order of the deferred verifications.
type guardCall struct {
	tenantID guid.ID
	verified bool
}

type stubAdministratorGuard struct {
	mu         sync.Mutex
	calls      []*guardCall
	protectErr error
	verifyErr  error
}

func (guard *stubAdministratorGuard) Protect(
	_ context.Context,
	db *gorm.DB,
	tenantID guid.ID,
) (func() error, error) {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if db == nil {
		return nil, errors.New("administrator guard requires a database handle")
	}
	if guard.protectErr != nil {
		return nil, guard.protectErr
	}
	call := &guardCall{tenantID: tenantID}
	guard.calls = append(guard.calls, call)
	return func() error {
		guard.mu.Lock()
		defer guard.mu.Unlock()
		call.verified = true
		return guard.verifyErr
	}, nil
}

func (guard *stubAdministratorGuard) protectedTenants() []guid.ID {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	tenants := make([]guid.ID, 0, len(guard.calls))
	for _, call := range guard.calls {
		tenants = append(tenants, call.tenantID)
	}
	return tenants
}

func (guard *stubAdministratorGuard) verifiedCount() int {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	count := 0
	for _, call := range guard.calls {
		if call.verified {
			count++
		}
	}
	return count
}

var _ core.AdministratorGuard = (*stubAdministratorGuard)(nil)

type identityHarness struct {
	service    *Service
	repository *Repository
	guard      *stubAdministratorGuard
	db         *gorm.DB
	runtime    *appenv.Runtime
	prefix     string
	auditMu    sync.Mutex
	audits     []audit.DomainEvent
	auditDBs   []*gorm.DB
	auditErr   error
}

func newIdentityHarness(t *testing.T, prefix string) *identityHarness {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?_busy_timeout=10000&_journal_mode=WAL",
		filepath.ToSlash(filepath.Join(t.TempDir(), "identity.sqlite")),
	)
	initializeIdentityDatabase(t, dsn, prefix)
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: "sqlite", DSN: dsn, Writable: true, Readable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	harness := &identityHarness{db: router.Write(), prefix: prefix, guard: &stubAdministratorGuard{}}
	harness.runtime = appenv.NewRuntime(
		appenv.NewIdentity("identity-test", "1.0.0"),
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
	harness.service = NewService(harness.runtime, appender, harness.guard, newIdentityTestIDs(1000))
	storage, err := files.NewLocal(t.TempDir())
	require.NoError(t, err)
	harness.service.repo.storage = storage
	harness.service.repo.accountFiles, err = files.NewAccountObjects(storage, "local", prefix)
	require.NoError(t, err)
	harness.service.now = func() time.Time { return identityTestNow }
	harness.service.erasureRecorder = func(context.Context, audit.ErasureObligation) error { return nil }
	harness.repository = harness.service.repo
	// The owners' connectors publish their table names in a running deployment;
	// a harness that runs identity alone publishes what they would, so a reader
	// here reaches a table instead of reporting the table names as missing.
	bindForeignTables(t, harness.repository, prefix)
	// The credential tables belong to authn, which runs those writes in
	// identity's transaction. identity's tests cannot reach the real capability,
	// so they supply a fixture -- see repository_credentialstore_test.go.
	bindCredentialStore(t, harness.repository, prefix)
	return harness
}

// initializeIdentityDatabase applies the identity module's own migrations and
// creates the cross-module tables identity writes through raw table names.
func initializeIdentityDatabase(t *testing.T, dsn, prefix string) {
	t.Helper()
	db, err := (gormx.Datasource{Type: "sqlite", DSN: dsn, Writable: true}).Open(t.Context())
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = prefix
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, db.Dialector.Name()))
	fileSource := (&files.Module{}).MigrationSource()
	fileSource.Prefix = prefix
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, fileSource, db.Dialector.Name()))
	statements := []string{
		"CREATE TABLE " + prefix + "iam_tenants (id BIGINT PRIMARY KEY, status TEXT NOT NULL)",
		"CREATE TABLE " + prefix + "iam_tenant_members (" +
			"tenant_id BIGINT NOT NULL, principal_id BIGINT NOT NULL, display_name TEXT NOT NULL, " +
			"email TEXT NOT NULL, status TEXT NOT NULL, created_at DATETIME NOT NULL, " +
			"updated_at DATETIME NOT NULL, disabled_at DATETIME NULL, " +
			"PRIMARY KEY (tenant_id, principal_id))",
		"CREATE TABLE " + prefix + "iam_policy_revisions (" +
			"tenant_id BIGINT PRIMARY KEY, revision BIGINT NOT NULL, updated_at DATETIME NOT NULL)",
		"CREATE TABLE " + prefix + "iam_credentials (" +
			"principal_id BIGINT PRIMARY KEY, password_hash TEXT NOT NULL, " +
			"password_changed_at DATETIME NOT NULL, credential_version BIGINT NOT NULL, " +
			"updated_at DATETIME NOT NULL)",
		"CREATE TABLE " + prefix + "iam_sessions (" +
			"id BIGINT PRIMARY KEY, principal_id BIGINT NOT NULL, revoked_at DATETIME NULL)",
		"CREATE TABLE " + prefix + "iam_refresh_tokens (" +
			"id_hash TEXT PRIMARY KEY, principal_id BIGINT NOT NULL, revoked_at DATETIME NULL)",
		"CREATE TABLE " + prefix + "iam_password_recovery_tokens (" +
			"id BIGINT PRIMARY KEY, principal_id BIGINT NOT NULL, consumed_at DATETIME NULL)",
		"CREATE TABLE " + prefix + "iam_email_verification_tokens (" +
			"id BIGINT PRIMARY KEY, principal_id BIGINT NOT NULL, consumed_at DATETIME NULL)",
	}
	for _, statement := range statements {
		require.NoError(t, db.Exec(statement).Error)
	}
	require.NoError(t, sqlDB.Close())
}

func (h *identityHarness) table(name string) string { return h.prefix + name }

func (h *identityHarness) seedTenant(t *testing.T, tenantID guid.ID, status string) {
	t.Helper()
	require.NoError(t, h.db.Table(h.table("iam_tenants")).
		Create(map[string]any{"id": tenantID, "status": status}).Error)
}

func (h *identityHarness) seedSession(t *testing.T, sessionID, principalID guid.ID) {
	t.Helper()
	require.NoError(t, h.db.Table(h.table("iam_sessions")).
		Create(map[string]any{"id": sessionID, "principal_id": principalID, "revoked_at": nil}).Error)
}

func (h *identityHarness) seedRefreshToken(t *testing.T, hash string, principalID guid.ID) {
	t.Helper()
	require.NoError(t, h.db.Table(h.table("iam_refresh_tokens")).
		Create(map[string]any{"id_hash": hash, "principal_id": principalID, "revoked_at": nil}).Error)
}

func (h *identityHarness) seedRecoveryToken(t *testing.T, id, principalID guid.ID) {
	t.Helper()
	require.NoError(t, h.db.Table(h.table("iam_password_recovery_tokens")).
		Create(map[string]any{"id": id, "principal_id": principalID, "consumed_at": nil}).Error)
}

func (h *identityHarness) seedVerificationToken(t *testing.T, id, principalID guid.ID) {
	t.Helper()
	require.NoError(t, h.db.Table(h.table("iam_email_verification_tokens")).
		Create(map[string]any{"id": id, "principal_id": principalID, "consumed_at": nil}).Error)
}

func (h *identityHarness) principal(t *testing.T, id guid.ID) *model.Principal {
	t.Helper()
	var row model.Principal
	require.NoError(t, h.db.Table(h.table(row.TableName())).Where("id = ?", id).Take(&row).Error)
	return &row
}

func (h *identityHarness) principalCount(t *testing.T) int64 {
	t.Helper()
	var count int64
	require.NoError(t, h.db.Table(h.table(model.Principal{}.TableName())).Count(&count).Error)
	return count
}

func (h *identityHarness) member(t *testing.T, tenantID, principalID guid.ID) *tenantMemberRecord {
	t.Helper()
	var member tenantMemberRecord
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).
		Where("tenant_id = ? AND principal_id = ?", tenantID, principalID).Take(&member).Error)
	return &member
}

func (h *identityHarness) memberCount(t *testing.T, tenantID, principalID guid.ID) int64 {
	t.Helper()
	var count int64
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).
		Where("tenant_id = ? AND principal_id = ?", tenantID, principalID).Count(&count).Error)
	return count
}

func (h *identityHarness) credentialVersion(t *testing.T, principalID guid.ID) int64 {
	t.Helper()
	var version int64
	require.NoError(t, h.db.Table(h.table("iam_credentials")).Select("credential_version").
		Where("principal_id = ?", principalID).Scan(&version).Error)
	return version
}

func (h *identityHarness) revision(t *testing.T, tenantID guid.ID) int64 {
	t.Helper()
	var revision int64
	require.NoError(t, h.db.Table(h.table("iam_policy_revisions")).Select("revision").
		Where("tenant_id = ?", tenantID).Scan(&revision).Error)
	return revision
}

func (h *identityHarness) nullTimeCount(t *testing.T, table, column string, principalID guid.ID) int64 {
	t.Helper()
	var count int64
	require.NoError(t, h.db.Table(h.table(table)).
		Where("principal_id = ? AND "+column+" IS NULL", principalID).Count(&count).Error)
	return count
}

func (h *identityHarness) events() []audit.DomainEvent {
	h.auditMu.Lock()
	defer h.auditMu.Unlock()
	return append([]audit.DomainEvent(nil), h.audits...)
}

func (h *identityHarness) eventTypes() []string {
	types := make([]string, 0)
	for _, event := range h.events() {
		types = append(types, event.EventType)
	}
	return types
}

func TestNewRepositoryRequiresDependencies(t *testing.T) {
	t.Parallel()

	runtime := appenv.NewRuntime(appenv.NewIdentity("identity-test", "1"))
	ids := newIdentityTestIDs(1)
	appender := audit.Appender(func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	assert.Panics(t, func() { NewRepository(nil, ids, appender) })
	assert.Panics(t, func() { NewRepository(runtime, nil, appender) })
	assert.Panics(t, func() { NewRepository(runtime, ids, nil) })
	assert.Panics(t, func() { NewService(nil, appender, &stubAdministratorGuard{}, ids) })
	assert.Panics(t, func() { NewService(runtime, nil, &stubAdministratorGuard{}, ids) })
	assert.Panics(t, func() { NewService(runtime, appender, nil, ids) })
	assert.Panics(t, func() { NewService(runtime, appender, &stubAdministratorGuard{}, nil) })
}

func TestRepositoryUnavailableWithoutDatabase(t *testing.T) {
	t.Parallel()

	runtime := appenv.NewRuntime(appenv.NewIdentity("identity-test", "1"))
	repository := NewRepository(runtime, newIdentityTestIDs(0), func(context.Context, *gorm.DB, audit.DomainEvent) error {
		return nil
	})
	_, _, err := repository.tables()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.ErrorIs(t, repository.runInTx(t.Context(), func(*Repository) error { return nil }), errRepositoryUnavailable)
	assert.Error(t, repository.runInTx(t.Context(), nil))
	assert.ErrorIs(t, repository.appendAudit(t.Context(), audit.DomainEvent{}), errRepositoryUnavailable)
	assert.ErrorIs(t, repository.bumpPolicyRevision(t.Context(), 1, identityTestNow), errRepositoryUnavailable)
	_, err = repository.pinWriter()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repository.on(nil)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Nil(t, (*Repository)(nil).writer())
	_, _, err = (*Repository)(nil).tables()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = (*Repository)(nil).pinWriter()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.ErrorIs(t, (*Repository)(nil).appendAudit(t.Context(), audit.DomainEvent{}), errRepositoryUnavailable)
	assert.ErrorIs(t, (*Repository)(nil).bumpPolicyRevision(t.Context(), 1, identityTestNow), errRepositoryUnavailable)
	assert.Error(t, (*Repository)(nil).runInTx(t.Context(), func(*Repository) error { return nil }))
}

func TestNextIdentityIDRejectsInvalidSources(t *testing.T) {
	t.Parallel()

	_, err := nextIdentityID(nil)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = nextIdentityID(newIdentityTestIDs(-1))
	assert.Error(t, err)
	_, err = nextIdentityID(failingIDSource{})
	assert.EqualError(t, err, "id source down")
	id, err := nextIdentityID(newIdentityTestIDs(41))
	require.NoError(t, err)
	assert.Equal(t, guid.ID(42), id)
}

func TestRepositoryPrefixIsAppliedToEveryTable(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	assert.Equal(t, "sl_iam_principals", harness.repository.names.Principals.String())
	tables, db, err := harness.repository.tables()
	require.NoError(t, err)
	require.NotNil(t, db)
	assert.Equal(t, "sl_iam_principals", tables.Principals.TableName())
	assert.Equal(t, "sl_iam_service_accounts", tables.ServiceAccounts.TableName())
	assert.Equal(t, "sl_iam_service_account_credentials", tables.ServiceAccountCredentials.TableName())
}

func TestRunInTxReusesActiveTransaction(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	var outer, inner *Repository
	require.NoError(t, harness.repository.runInTx(t.Context(), func(repo *Repository) error {
		outer = repo
		return repo.runInTx(t.Context(), func(nested *Repository) error {
			inner = nested
			return nil
		})
	}))
	assert.True(t, outer.inTx)
	assert.Same(t, outer, inner)
}

func TestRunInTxRollsBackOnError(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	failure := errors.New("transaction failure")
	err := harness.repository.runInTx(t.Context(), func(repo *Repository) error {
		if err := repo.insertPrincipal(t.Context(), &model.Principal{
			ID: 5000, LoginName: "rollback", DisplayName: "rollback", Status: "active",
			CreatedAt: identityTestNow, UpdatedAt: identityTestNow,
		}); err != nil {
			return err
		}
		return failure
	})
	assert.ErrorIs(t, err, failure)
	assert.Zero(t, harness.principalCount(t))
}

func TestPinWriterKeepsOneHandleOutsideTransaction(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	pinned, err := harness.repository.pinWriter()
	require.NoError(t, err)
	assert.False(t, pinned.inTx)
	assert.Same(t, pinned.writer(), harness.db)
	again, err := pinned.pinWriter()
	require.NoError(t, err)
	assert.Same(t, pinned, again)
}

func TestAppendAuditWrapsAppenderFailure(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	harness.auditErr = errors.New("audit sink down")
	err := harness.repository.appendAudit(t.Context(), audit.DomainEvent{EventType: "probe"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "append identity audit event")
	assert.Contains(t, err.Error(), "audit sink down")
	require.Len(t, harness.auditDBs, 1)
	assert.Same(t, harness.db, harness.auditDBs[0])
}

func TestBumpPolicyRevisionAdvancesTenantRevision(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	require.NoError(t, harness.repository.bumpPolicyRevision(t.Context(), 7, identityTestNow))
	assert.Equal(t, int64(1), harness.revision(t, 7))
	require.NoError(t, harness.repository.bumpPolicyRevision(t.Context(), 7, identityTestNow))
	assert.Equal(t, int64(2), harness.revision(t, 7))
	assert.Zero(t, harness.revision(t, 8))
}

func TestCredentialLifecycleThroughAuthnOwnedTable(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	require.NoError(t, harness.repository.createCredential(t.Context(), 900, "hash", identityTestNow))
	assert.Equal(t, int64(1), harness.credentialVersion(t, 900))
	require.NoError(t, harness.repository.incrementCredentialVersion(t.Context(), 900, identityTestNow))
	assert.Equal(t, int64(2), harness.credentialVersion(t, 900))
	assert.ErrorIs(
		t,
		harness.repository.incrementCredentialVersion(t.Context(), 901, identityTestNow),
		errPrincipalCredentialNotFound,
	)
	err := harness.repository.createCredential(t.Context(), 900, "hash", identityTestNow)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insert principal credential")
}

func TestRevokeSecurityStateOnlyTouchesOpenRows(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	harness.seedSession(t, 11, 900)
	harness.seedSession(t, 12, 901)
	harness.seedRefreshToken(t, "hash-900", 900)
	harness.seedRefreshToken(t, "hash-901", 901)
	harness.seedRecoveryToken(t, 21, 900)
	harness.seedRecoveryToken(t, 22, 901)
	harness.seedVerificationToken(t, 31, 900)
	harness.seedVerificationToken(t, 32, 901)

	require.NoError(t, harness.repository.revokeAllSecurityState(t.Context(), 900, identityTestNow))

	assert.Zero(t, harness.nullTimeCount(t, "iam_sessions", "revoked_at", 900))
	assert.Zero(t, harness.nullTimeCount(t, "iam_refresh_tokens", "revoked_at", 900))
	assert.Zero(t, harness.nullTimeCount(t, "iam_password_recovery_tokens", "consumed_at", 900))
	assert.Zero(t, harness.nullTimeCount(t, "iam_email_verification_tokens", "consumed_at", 900))
	assert.Equal(t, int64(1), harness.nullTimeCount(t, "iam_sessions", "revoked_at", 901))
	assert.Equal(t, int64(1), harness.nullTimeCount(t, "iam_refresh_tokens", "revoked_at", 901))
	assert.Equal(t, int64(1), harness.nullTimeCount(t, "iam_password_recovery_tokens", "consumed_at", 901))
	assert.Equal(t, int64(1), harness.nullTimeCount(t, "iam_email_verification_tokens", "consumed_at", 901))
}

func TestRepositoryReportsMissingCrossModuleTables(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	require.NoError(t, harness.db.Exec("DROP TABLE iam_sessions").Error)
	err := harness.repository.revokeSessionsAndRefreshTokens(t.Context(), 900, identityTestNow)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "revoke principal sessions")
	require.NoError(t, harness.db.Exec("DROP TABLE iam_password_recovery_tokens").Error)
	require.NoError(t, harness.db.Exec(
		"CREATE TABLE iam_sessions (id BIGINT PRIMARY KEY, principal_id BIGINT NOT NULL, revoked_at DATETIME NULL)",
	).Error)
	err = harness.repository.revokeAllSecurityState(t.Context(), 900, identityTestNow)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consume principal credential tokens")
}

// newDetachedRepository builds a repository whose runtime carries no database,
// which is how every persistence method behaves when the datasource router is
// unavailable at runtime.
func newDetachedRepository(t *testing.T) *Repository {
	t.Helper()
	return NewRepository(
		appenv.NewRuntime(appenv.NewIdentity("identity-test", "1"), appenv.WithTablePrefix("sl_")),
		newIdentityTestIDs(1),
		func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil },
	)
}

func TestEveryPersistenceMethodFailsWithoutDatabase(t *testing.T) {
	t.Parallel()

	repository := newDetachedRepository(t)
	ctx := t.Context()
	principal := &model.Principal{ID: 1, LoginName: "alice", DisplayName: "Alice", Status: "active"}
	member := tenantMemberRecord{TenantID: 7, PrincipalID: 1, DisplayName: "Alice", Status: "active"}

	calls := map[string]func() error{
		"createCredential": func() error {
			return repository.createCredential(ctx, 1, "hash", identityTestNow)
		},
		"incrementCredentialVersion": func() error {
			return repository.incrementCredentialVersion(ctx, 1, identityTestNow)
		},
		"revokeSessionsAndRefreshTokens": func() error {
			return repository.revokeSessionsAndRefreshTokens(ctx, 1, identityTestNow)
		},
		"revokeAllSecurityState": func() error {
			return repository.revokeAllSecurityState(ctx, 1, identityTestNow)
		},
		"insertPrincipal":    func() error { return repository.insertPrincipal(ctx, principal) },
		"insertTenantMember": func() error { return repository.insertTenantMember(ctx, member) },
		"insertExternalPrincipal": func() error {
			_, err := repository.insertExternalPrincipal(ctx, principal)
			return err
		},
		"getPrincipal": func() error {
			_, err := repository.getPrincipal(ctx, 7, 1)
			return err
		},
		"listPrincipals": func() error {
			_, _, err := repository.listPrincipals(ctx, 7, "", 10, 0)
			return err
		},
		"updatePrincipalRow": func() error {
			return repository.updatePrincipalRow(ctx, 1, map[string]any{"status": "active"})
		},
		"updateTenantMember": func() error {
			return repository.updateTenantMember(ctx, 7, 1, map[string]any{"status": "active"})
		},
		"activeTenantIDs": func() error {
			_, err := repository.activeTenantIDs(ctx, 1)
			return err
		},
		"principalByLoginOrEmail": func() error {
			_, err := repository.principalByLoginOrEmail(ctx, "alice@example.test")
			return err
		},
		"ensureTenantMember": func() error {
			_, err := repository.ensureTenantMember(ctx, 7, principal, identityTestNow)
			return err
		},
		"getTenantMember": func() error {
			_, err := repository.getTenantMember(ctx, 7, 1)
			return err
		},
		"tenantActive": func() error {
			_, err := repository.tenantActive(ctx, 7)
			return err
		},
		"insertServiceAccount": func() error {
			return repository.insertServiceAccount(ctx, &model.ServiceAccount{PrincipalID: 1, OwnerTenantID: 7})
		},
		"serviceAccountQuery": func() error {
			_, err := repository.serviceAccountQuery(ctx)
			return err
		},
		"getServiceAccount": func() error {
			_, err := repository.getServiceAccount(ctx, 7, 1, false)
			return err
		},
		"listServiceAccounts": func() error {
			_, _, err := repository.listServiceAccounts(ctx, 7, "", 10, 0)
			return err
		},
		"replaceServiceAccount": func() error {
			return repository.replaceServiceAccount(ctx, 7, 1, "", "active", nil, 1, false, identityTestNow)
		},
		"deleteServiceAccount": func() error {
			return repository.deleteServiceAccount(ctx, 7, 1, 1, identityTestNow)
		},
		"revokeServiceAccountCredentials": func() error {
			return repository.revokeServiceAccountCredentials(ctx, 1, identityTestNow)
		},
		"countActiveServiceAccountCredentials": func() error {
			_, err := repository.countActiveServiceAccountCredentials(ctx, 1, identityTestNow)
			return err
		},
		"insertServiceAccountCredential": func() error {
			return repository.insertServiceAccountCredential(ctx, &model.ServiceAccountCredential{ID: 1, PrincipalID: 1})
		},
		"listServiceAccountCredentials": func() error {
			_, err := repository.listServiceAccountCredentials(ctx, 1)
			return err
		},
		"revokeServiceAccountCredential": func() error {
			_, err := repository.revokeServiceAccountCredential(ctx, 1, 2, identityTestNow)
			return err
		},
		"incrementServiceAccountTokenVersion": func() error {
			return repository.incrementServiceAccountTokenVersion(ctx, 7, 1, identityTestNow)
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, call(), errRepositoryUnavailable)
		})
	}
}

func TestEveryServiceEntryPointFailsWithoutDatabase(t *testing.T) {
	t.Parallel()

	runtime := appenv.NewRuntime(appenv.NewIdentity("identity-test", "1"), appenv.WithTablePrefix("sl_"))
	service := NewService(
		runtime,
		func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil },
		&stubAdministratorGuard{},
		newIdentityTestIDs(1),
	)
	ctx := t.Context()
	display := "Alice"

	calls := map[string]func() error{
		"Create": func() error {
			_, err := service.Create(ctx, 7, "alice", testPassword, "Alice", "alice@example.test")
			return err
		},
		"List": func() error {
			_, _, err := service.List(ctx, 7, "", 10, 0)
			return err
		},
		"Get": func() error {
			_, err := service.Get(ctx, 7, 1)
			return err
		},
		"Update": func() error {
			_, err := service.Update(ctx, 7, 1, &display, nil)
			return err
		},
		"SetStatus": func() error {
			_, err := service.SetStatus(ctx, 7, 1, "disabled")
			return err
		},
		"CreateServiceAccount": func() error {
			_, err := service.CreateServiceAccount(ctx, 7, "robot", "Robot", "", nil)
			return err
		},
		"ListServiceAccounts": func() error {
			_, _, err := service.ListServiceAccounts(ctx, 7, "", 10, 0)
			return err
		},
		"GetServiceAccount": func() error {
			_, err := service.GetServiceAccount(ctx, 7, 1)
			return err
		},
		"ReplaceServiceAccount": func() error {
			_, err := service.ReplaceServiceAccount(ctx, 7, 1, "Robot", "", "active", nil, 1)
			return err
		},
		"DeleteServiceAccount": func() error { return service.DeleteServiceAccount(ctx, 7, 1, 1) },
		"CreateServiceAccountCredential": func() error {
			_, err := service.CreateServiceAccountCredential(ctx, 7, 1, "ci", []string{"read"}, nil)
			return err
		},
		"ListServiceAccountCredentials": func() error {
			_, err := service.ListServiceAccountCredentials(ctx, 7, 1)
			return err
		},
		"RevokeServiceAccountCredential": func() error {
			return service.RevokeServiceAccountCredential(ctx, 7, 1, 2)
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, call(), errRepositoryUnavailable)
		})
	}
}

func TestPersistenceMethodsWrapSchemaFailures(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	ctx := t.Context()
	for _, table := range []string{
		"iam_principals", "iam_service_accounts", "iam_service_account_credentials",
		"iam_tenant_members", "iam_tenants",
	} {
		require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table(table)).Error)
	}
	repository := harness.repository
	principal := &model.Principal{ID: 1, LoginName: "alice", DisplayName: "Alice", Status: "active"}

	for name, call := range map[string]func() error{
		"insertPrincipal": func() error { return repository.insertPrincipal(ctx, principal) },
		"insertExternalPrincipal": func() error {
			_, err := repository.insertExternalPrincipal(ctx, principal)
			return err
		},
		"insertTenantMember": func() error {
			return repository.insertTenantMember(ctx, tenantMemberRecord{TenantID: 7, PrincipalID: 1})
		},
		"getPrincipal": func() error {
			_, err := repository.getPrincipal(ctx, 7, 1)
			return err
		},
		"listPrincipals": func() error {
			_, _, err := repository.listPrincipals(ctx, 7, "", 10, 0)
			return err
		},
		"updatePrincipalRow": func() error {
			return repository.updatePrincipalRow(ctx, 1, map[string]any{"status": "active"})
		},
		"updateTenantMember": func() error {
			return repository.updateTenantMember(ctx, 7, 1, map[string]any{"status": "active"})
		},
		"activeTenantIDs": func() error {
			_, err := repository.activeTenantIDs(ctx, 1)
			return err
		},
		"principalByLoginOrEmail": func() error {
			_, err := repository.principalByLoginOrEmail(ctx, "alice@example.test")
			return err
		},
		"ensureTenantMember": func() error {
			_, err := repository.ensureTenantMember(ctx, 7, principal, identityTestNow)
			return err
		},
		"getTenantMember": func() error {
			_, err := repository.getTenantMember(ctx, 7, 1)
			return err
		},
		"tenantActive": func() error {
			_, err := repository.tenantActive(ctx, 7)
			return err
		},
		"insertServiceAccount": func() error {
			return repository.insertServiceAccount(ctx, &model.ServiceAccount{PrincipalID: 1, OwnerTenantID: 7})
		},
		"getServiceAccount": func() error {
			_, err := repository.getServiceAccount(ctx, 7, 1, false)
			return err
		},
		"listServiceAccounts": func() error {
			_, _, err := repository.listServiceAccounts(ctx, 7, "", 10, 0)
			return err
		},
		"replaceServiceAccount": func() error {
			return repository.replaceServiceAccount(ctx, 7, 1, "", "active", nil, 1, true, identityTestNow)
		},
		"deleteServiceAccount": func() error {
			return repository.deleteServiceAccount(ctx, 7, 1, 1, identityTestNow)
		},
		"revokeServiceAccountCredentials": func() error {
			return repository.revokeServiceAccountCredentials(ctx, 1, identityTestNow)
		},
		"countActiveServiceAccountCredentials": func() error {
			_, err := repository.countActiveServiceAccountCredentials(ctx, 1, identityTestNow)
			return err
		},
		"insertServiceAccountCredential": func() error {
			return repository.insertServiceAccountCredential(ctx, &model.ServiceAccountCredential{ID: 1, PrincipalID: 1})
		},
		"listServiceAccountCredentials": func() error {
			_, err := repository.listServiceAccountCredentials(ctx, 1)
			return err
		},
		"revokeServiceAccountCredential": func() error {
			_, err := repository.revokeServiceAccountCredential(ctx, 1, 2, identityTestNow)
			return err
		},
		"incrementServiceAccountTokenVersion": func() error {
			return repository.incrementServiceAccountTokenVersion(ctx, 7, 1, identityTestNow)
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			require.Error(t, err)
			assert.NotErrorIs(t, err, errRepositoryUnavailable, "a missing table is a query failure, not a missing writer")
		})
	}
}
