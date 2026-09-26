package oauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/oauth/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var oauthTestNow = time.Date(2026, 9, 3, 18, 0, 0, 123_000_000, time.UTC)

type oauthTestIDs struct{ next atomic.Int64 }

func newOAuthTestIDs(start int64) *oauthTestIDs {
	ids := &oauthTestIDs{}
	ids.next.Store(start)
	return ids
}

func (ids *oauthTestIDs) Next() (int64, error) { return ids.next.Add(1), nil }

type oauthFailingIDs struct{}

func (oauthFailingIDs) Next() (int64, error) { return 0, errors.New("ID source down") }

type oauthWebFixture struct {
	mu                sync.Mutex
	next              int
	session           *authnext.Claims
	tokens            map[string]*authnext.Claims
	issue             error
	csrf              error
	profileError      error
	authenticateError error
	profileScope      string
}

func newOAuthWebFixture() *oauthWebFixture {
	return &oauthWebFixture{
		session: &authnext.Claims{
			Issuer: "https://iam.example", Subject: "101", PrincipalID: 101,
			AuthTime: oauthTestNow.Add(-time.Minute), ACR: 1, AMR: []string{"pwd"},
		},
		tokens: make(map[string]*authnext.Claims),
	}
}

func (web *oauthWebFixture) Authenticate(_ context.Context, authorization, cookie string) (*authnext.Claims, error) {
	web.mu.Lock()
	defer web.mu.Unlock()
	if web.authenticateError != nil {
		return nil, web.authenticateError
	}
	if cookie == "session" {
		claimsCopy := *web.session
		return &claimsCopy, nil
	}
	const bearer = "Bearer "
	if len(authorization) > len(bearer) && authorization[:len(bearer)] == bearer {
		if claims := web.tokens[authorization[len(bearer):]]; claims != nil {
			claimsCopy := *claims
			return &claimsCopy, nil
		}
	}
	return nil, authnext.ErrInvalidCredentials
}

func (web *oauthWebFixture) issueToken(claims *authnext.Claims) (string, int64, error) {
	web.mu.Lock()
	defer web.mu.Unlock()
	if web.issue != nil {
		return "", 0, web.issue
	}
	web.next++
	token := fmt.Sprintf("token-%d", web.next)
	claims.Issuer = "https://iam.example"
	claims.Audience = []string{"api"}
	claims.ExpiresAt = oauthTestNow.Add(time.Hour)
	web.tokens[token] = claims
	return token, 3600, nil
}

func (web *oauthWebFixture) IssueTenantAccessTokenWithAssurance(
	_ context.Context,
	principalID guid.ID,
	tenantID, _, scope string,
	assurance authnext.Assurance,
) (string, int64, error) {
	return web.issueToken(&authnext.Claims{
		Subject: principalID.String(), PrincipalID: principalID, OrganizationID: tenantID,
		ClientID: assurance.ClientID, AuthTime: assurance.AuthTime, ACR: assurance.Level,
		OAuthGrantID: assurance.OAuthGrantID,
		AMR:          assurance.Methods, SubjectType: authnext.SubjectTypePrincipal,
		Raw: map[string]any{"scope": scope},
	})
}

func (web *oauthWebFixture) IssueTenantIDTokenWithAssurance(
	_ context.Context,
	principalID guid.ID,
	tenantID, clientID, _, scope string,
	assurance authnext.Assurance,
) (string, error) {
	token, _, err := web.issueToken(&authnext.Claims{
		Subject: principalID.String(), PrincipalID: principalID, OrganizationID: tenantID,
		ClientID: clientID, AuthTime: assurance.AuthTime, ACR: assurance.Level,
		AMR: assurance.Methods, SubjectType: authnext.SubjectTypePrincipal,
		Raw: map[string]any{"scope": scope},
	})
	return token, err
}

func (web *oauthWebFixture) ProjectProfile(_ context.Context, claims *authnext.Claims, scope string) (authn.ProfileClaims, error) {
	web.mu.Lock()
	defer web.mu.Unlock()
	web.profileScope = scope
	view := authn.ProfileClaims{}
	if containsWord(scope, "profile") {
		view.PreferredUsername = &claims.PreferredUsername
	}
	if containsWord(scope, "email") {
		view.Email, view.EmailVerified = &claims.Email, &claims.EmailVerified
	}
	return view, web.profileError
}

func (web *oauthWebFixture) IssueTenantOAuthClientToken(
	_ context.Context,
	clientID, tenantID, _, _, name string,
) (string, int64, error) {
	return web.issueToken(&authnext.Claims{
		Subject: "client:" + clientID, SubjectType: authnext.SubjectTypeOAuthClient,
		OrganizationID: tenantID, ClientID: clientID, PreferredUsername: name,
	})
}

func (web *oauthWebFixture) IssueTenantServiceAccountToken(
	_ context.Context,
	subject, tenantID, _, _, username string,
	credentialVersion int64,
) (string, int64, error) {
	principalID, _ := guid.Parse(subject)
	return web.issueToken(&authnext.Claims{
		Subject: subject, SubjectType: authnext.SubjectTypeServiceAccount,
		OrganizationID: tenantID, PrincipalID: principalID,
		PreferredUsername: username, CredentialVersion: credentialVersion,
	})
}

func (*oauthWebFixture) ClearCookie() string                            { return "" }
func (*oauthWebFixture) CookieSecure() bool                             { return false }
func (*oauthWebFixture) Issuer() string                                 { return "https://iam.example" }
func (*oauthWebFixture) JWKS() map[string]any                           { return map[string]any{"keys": []any{}} }
func (*oauthWebFixture) Logout(context.Context, string) (string, error) { return "", nil }
func (*oauthWebFixture) ResolveReturnURL(value string) (string, error)  { return value, nil }
func (*oauthWebFixture) SessionCookie(value string) string              { return value }
func (*oauthWebFixture) SessionCookieName() string                      { return "session" }
func (web *oauthWebFixture) setCSRF(err error) {
	web.mu.Lock()
	defer web.mu.Unlock()
	web.csrf = err
}

func (web *oauthWebFixture) setPrincipal(id guid.ID) {
	web.mu.Lock()
	defer web.mu.Unlock()
	web.session.PrincipalID = id
	web.session.Subject = id.String()
}

func (web *oauthWebFixture) ValidateCSRF(string, string, string, string) error {
	web.mu.Lock()
	defer web.mu.Unlock()
	return web.csrf
}
func (*oauthWebFixture) CreateSession(context.Context, guid.ID, time.Time, authnext.Assurance) (string, error) {
	return "session", nil
}
func (*oauthWebFixture) CreateSessionOn(context.Context, *gorm.DB, guid.ID, time.Time, authnext.Assurance) (string, error) {
	return "session", nil
}

type oauthHarness struct {
	service  *Service
	repo     *Repository
	web      *oauthWebFixture
	db       *gorm.DB
	runtime  *appenv.Runtime
	prefix   string
	auditErr error
}

func newOAuthHarness(t *testing.T, prefix string) *oauthHarness {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?_busy_timeout=10000&_journal_mode=WAL",
		filepath.ToSlash(filepath.Join(t.TempDir(), "oauth.sqlite")),
	)
	initializeOAuthDatabase(t, dsn, prefix)
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: "sqlite", DSN: dsn, Writable: true, Readable: true, MaxOpenConns: 1},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	harness := &oauthHarness{db: router.Write(), prefix: prefix, web: newOAuthWebFixture()}
	harness.runtime = appenv.NewRuntime(
		appenv.NewIdentity("oauth-test", "1.0.0"),
		appenv.WithDatabase(router),
		appenv.WithTablePrefix(prefix),
	)
	appender := audit.Appender(func(ctx context.Context, db *gorm.DB, event audit.DomainEvent) error {
		if harness.auditErr != nil {
			return harness.auditErr
		}
		return db.WithContext(ctx).Table(prefix + "oauth_test_audits").Create(map[string]any{
			"tenant_id": event.TenantID, "principal_id": event.PrincipalID,
			"event_type": event.EventType, "target_id": event.TargetID,
		}).Error
	})
	harness.service = NewService(
		harness.runtime, harness.web, newOAuthTestIDs(1000), appender,
		resolveIdentityPrincipalState(t, harness.runtime), ConsentConfig{},
	)
	harness.service.now = func() time.Time { return oauthTestNow }
	harness.repo = harness.service.repo
	bindForeignTables(t, harness.repo, prefix)
	return harness
}

func initializeOAuthDatabase(t *testing.T, dsn, prefix string) {
	t.Helper()
	db, err := (gormx.Datasource{Type: "sqlite", DSN: dsn, Writable: true}).Open(t.Context())
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	identitySource := goosex.Source{FS: os.DirFS(filepath.Join(filepath.Dir(filename), "..", "identity")), TableName: "goose_identity", Prefix: prefix}
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, identitySource, db.Dialector.Name()))
	statements := []string{
		"CREATE TABLE " + prefix + "iam_credentials (principal_id BIGINT PRIMARY KEY, credential_version BIGINT NOT NULL)",
		"CREATE TABLE " + prefix + "iam_tenants (id BIGINT PRIMARY KEY, status TEXT NOT NULL)",
		"CREATE TABLE " + prefix + "iam_tenant_members (tenant_id BIGINT NOT NULL, principal_id BIGINT NOT NULL, status TEXT NOT NULL, PRIMARY KEY (tenant_id, principal_id))",
		"CREATE TABLE " + prefix + "oauth_test_audits (id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id BIGINT NOT NULL, principal_id BIGINT NOT NULL, event_type TEXT NOT NULL, target_id BIGINT NOT NULL)",
	}
	for _, statement := range statements {
		require.NoError(t, db.Exec(statement).Error)
	}
	source := (&Module{}).MigrationSource()
	source.Prefix = prefix
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, db.Dialector.Name()))
	require.NoError(t, db.Table(prefix+"iam_principals").Create(map[string]any{
		"id": 101, "login_name": "alice", "status": "active",
		"display_name": "Alice", "created_at": oauthTestNow, "updated_at": oauthTestNow,
	}).Error)
	require.NoError(t, db.Table(prefix+"iam_tenants").Create(map[string]any{
		"id": 7, "status": "active",
	}).Error)
	require.NoError(t, db.Table(prefix+"iam_tenant_members").Create(map[string]any{
		"tenant_id": 7, "principal_id": 101, "status": "active",
	}).Error)
}

func (h *oauthHarness) table(name string) string { return h.prefix + name }

func (h *oauthHarness) count(t *testing.T, table string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, h.db.Table(h.table(table)).Count(&count).Error)
	return count
}

func (h *oauthHarness) eventTypes(t *testing.T) []string {
	t.Helper()
	var types []string
	require.NoError(t, h.db.Table(h.table("oauth_test_audits")).Order("id ASC").Pluck("event_type", &types).Error)
	return types
}

func TestRepositoryInfrastructureContracts(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("test", "1"))
	ids := newOAuthTestIDs(-1)
	appender := audit.Appender(func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	assert.Panics(t, func() { NewRepository(nil, ids, appender) })
	assert.Panics(t, func() { NewRepository(runtime, nil, appender) })
	assert.Panics(t, func() { NewRepository(runtime, ids, nil) })
	repo := NewRepository(runtime, ids, appender)
	assert.Nil(t, (*Repository)(nil).writer())
	_, _, err := (*Repository)(nil).tables()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, _, err = repo.tables()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = repo.pinWriter()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.ErrorIs(t, repo.runInTx(t.Context(), func(*Repository) error { return nil }), errRepositoryUnavailable)
	assert.Error(t, repo.runInTx(t.Context(), nil))
	assert.Error(t, (*Repository)(nil).runInTx(t.Context(), func(*Repository) error { return nil }))
	assert.ErrorIs(t, repo.appendAudit(t.Context(), audit.DomainEvent{}), errRepositoryUnavailable)

	zero := NewRepository(runtime, ids, appender)
	_, err = zero.nextID()
	assert.ErrorContains(t, err, "must be positive")
	failing := NewRepository(runtime, oauthFailingIDs{}, appender)
	_, err = failing.nextID()
	assert.EqualError(t, err, "ID source down")
}

func TestRepositoryMethodsFailClosedWithoutWriter(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("sl_"))
	repo := NewRepository(
		runtime,
		newOAuthTestIDs(0),
		func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil },
	)
	now := oauthTestNow
	client := &model.Client{}
	code := &model.AuthorizationCode{}
	consent := &model.Consent{}
	refresh := &model.RefreshToken{}
	checks := map[string]func() error{
		"insert client": func() error { return repo.insertClient(t.Context(), client) },
		"list clients":  func() error { _, err := repo.listClients(t.Context(), 1); return err },
		"update client": func() error {
			_, err := repo.updateClient(t.Context(), 1, 2, map[string]any{"name": "x"})
			return err
		},
		"revoke client refresh": func() error { return repo.revokeClientRefreshTokens(t.Context(), 2, now) },
		"delete client":         func() error { return repo.deleteClient(t.Context(), 1, 2) },
		"active client":         func() error { _, err := repo.activeClient(t.Context(), 2); return err },
		"active member":         func() error { _, err := repo.activeMember(t.Context(), 1, 2); return err },
		"consent":               func() error { _, err := repo.consent(t.Context(), 2, 3); return err },
		"upsert consent":        func() error { return repo.upsertConsent(t.Context(), consent) },
		"list consents": func() error {
			_, err := repo.consentsForPrincipal(t.Context(), 2)
			return err
		},
		"delete consent": func() error { return repo.deleteConsent(t.Context(), 2, 3) },
		"client owner":   func() error { _, _, err := repo.clientOwner(t.Context(), 2); return err },
		"lock consent":   func() error { _, err := repo.lockConsent(t.Context(), 2, 3); return err },
		"require grant":  func() error { return repo.requireGrant(t.Context(), 2, 3, "grant") },
		"insert consent request": func() error {
			return repo.insertConsentRequest(t.Context(), &model.ConsentRequest{})
		},
		"read consent request": func() error {
			_, err := repo.consentRequest(t.Context(), "challenge")
			return err
		},
		"resolve consent request": func() error {
			return repo.resolveConsentRequest(t.Context(), "challenge", now)
		},
		"revoke granted": func() error { return repo.revokeGrantedTokens(t.Context(), 2, 3, now) },
		"insert code":    func() error { return repo.insertAuthorizationCode(t.Context(), code) },
		"read code": func() error {
			_, err := repo.authorizationCode(t.Context(), "hash", 2)
			return err
		},
		"consume code": func() error {
			return repo.consumeAuthorizationCode(t.Context(), "hash", now, refresh)
		},
		"read refresh": func() error {
			_, err := repo.refreshToken(t.Context(), "hash", 2)
			return err
		},
		"rotate refresh": func() error {
			return repo.rotateRefreshToken(t.Context(), "hash", "family", now, refresh)
		},
		"revoke refresh": func() error { return repo.revokeRefreshFamily(t.Context(), "family", now) },
		"service account": func() error {
			_, err := repo.serviceAccountClient(t.Context(), 2)
			return err
		},
		"touch service account": func() error {
			return repo.touchServiceAccountCredential(t.Context(), 2, now)
		},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, check(), errRepositoryUnavailable)
		})
	}
}

func TestRepositoryPrefixAndTransactionReuse(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	assert.Equal(t, "sl_iam_oauth_clients", harness.repo.names.Clients.String())
	tables, db, err := harness.repo.tables()
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_oauth_clients", tables.Clients.TableName())
	assert.Same(t, harness.db, db)
	require.NoError(t, harness.repo.runInTx(t.Context(), func(tx *Repository) error {
		assert.True(t, tx.inTx)
		return tx.runInTx(t.Context(), func(nested *Repository) error {
			assert.Same(t, tx, nested)
			return nil
		})
	}))
}

func TestOAuthModelRowsRoundTripNullableTimes(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	client := model.Client{
		ID: 22, TenantID: 7, Name: "Browser", RedirectURIs: "[]", GrantTypes: "refresh_token",
		Scopes: "openid", PublicClient: true, ConsentMode: string(ConsentImplicit),
		Status: "active", CreatedAt: oauthTestNow, UpdatedAt: oauthTestNow,
	}
	require.NoError(t, harness.db.Table(harness.table(client.TableName())).Create(&client).Error)
	row := model.RefreshToken{
		IDHash: hashToken("refresh"), FamilyID: "family", PrincipalID: 101, ClientID: 22,
		Scope: "openid", AuthTime: oauthTestNow, CreatedAt: oauthTestNow,
		ExpiresAt: oauthTestNow.Add(time.Hour),
	}
	require.NoError(t, harness.db.Table(harness.table(row.TableName())).Create(&row).Error)
	var loaded model.RefreshToken
	require.NoError(t, harness.db.Table(harness.table(row.TableName())).Take(&loaded).Error)
	assert.Nil(t, loaded.UsedAt)
	assert.Nil(t, loaded.RevokedAt)
	assert.Equal(t, oauthTestNow, loaded.AuthTime)
}

func TestRefreshCASLoserRevokesCompleteFamily(t *testing.T) {
	harness := newOAuthHarness(t, "")
	client, _ := grantClient(t, harness, "Refresh CAS")
	clientID, err := guid.Parse(client.ID)
	require.NoError(t, err)
	require.NoError(t, harness.db.Create(&model.Consent{
		PrincipalID: 101, ClientID: clientID, TenantID: 7, GrantID: "current-grant",
		Scope: "openid", CreatedAt: oauthTestNow, LastUsedAt: oauthTestNow,
	}).Error)
	initial := model.RefreshToken{
		GrantID: "current-grant",
		IDHash:  hashToken("initial"), FamilyID: "family", PrincipalID: 101, ClientID: clientID,
		Scope: "openid", AuthTime: oauthTestNow, CreatedAt: oauthTestNow,
		ExpiresAt: oauthTestNow.Add(time.Hour),
	}
	require.NoError(t, harness.db.Create(&initial).Error)
	var sequence atomic.Int64
	errs := runOAuthConcurrently(8, func() error {
		index := sequence.Add(1)
		next := model.RefreshToken{
			GrantID: initial.GrantID,
			IDHash:  hashToken(fmt.Sprintf("next-%d", index)), FamilyID: initial.FamilyID,
			PrincipalID: initial.PrincipalID, ClientID: initial.ClientID, Scope: initial.Scope,
			AuthTime: oauthTestNow, CreatedAt: oauthTestNow, ExpiresAt: oauthTestNow.Add(time.Hour),
		}
		return harness.repo.rotateRefreshToken(
			t.Context(), initial.IDHash, initial.FamilyID, oauthTestNow, &next,
		)
	})
	assertSingleOAuthWinner(t, errs)
	assert.Equal(t, int64(2), harness.count(t, "iam_refresh_tokens"))
	var unrevoked int64
	require.NoError(t, harness.db.Model(&model.RefreshToken{}).
		Where("family_id = ? AND revoked_at IS NULL", initial.FamilyID).Count(&unrevoked).Error)
	assert.Zero(t, unrevoked, "a CAS loser must revoke the original and the winner's successor")
}

func TestRepositoryPropagatesStorageFailures(t *testing.T) {
	t.Run("client table", func(t *testing.T) {
		harness := newOAuthHarness(t, "")
		require.NoError(t, harness.db.Exec("DROP TABLE iam_oauth_clients").Error)
		assert.Error(t, harness.repo.insertClient(t.Context(), &model.Client{}))
		_, err := harness.repo.listClients(t.Context(), 7)
		assert.Error(t, err)
		_, err = harness.repo.updateClient(t.Context(), 7, 8, map[string]any{"name": "x"})
		assert.Error(t, err)
		assert.Error(t, harness.repo.deleteClient(t.Context(), 7, 8))
		_, err = harness.repo.activeClient(t.Context(), 8)
		assert.Error(t, err)
	})

	t.Run("refresh table", func(t *testing.T) {
		harness := newOAuthHarness(t, "")
		require.NoError(t, harness.db.Exec("DROP TABLE iam_refresh_tokens").Error)
		assert.Error(t, harness.repo.revokeClientRefreshTokens(t.Context(), 8, oauthTestNow))
		_, err := harness.repo.refreshToken(t.Context(), "hash", 8)
		assert.Error(t, err)
		assert.Error(t, harness.repo.rotateRefreshToken(
			t.Context(), "hash", "family", oauthTestNow, &model.RefreshToken{},
		))
		assert.Error(t, harness.repo.revokeRefreshFamily(t.Context(), "family", oauthTestNow))
		assert.Error(t, harness.repo.revokeGrantedTokens(t.Context(), 9, 8, oauthTestNow))
	})

	t.Run("consent table", func(t *testing.T) {
		harness := newOAuthHarness(t, "")
		require.NoError(t, harness.db.Exec("DROP TABLE iam_oauth_consents").Error)
		_, err := harness.repo.consent(t.Context(), 8, 9)
		assert.Error(t, err)
		assert.Error(t, harness.repo.upsertConsent(t.Context(), &model.Consent{}))
		_, err = harness.repo.consentsForPrincipal(t.Context(), 9)
		assert.Error(t, err)
		assert.Error(t, harness.repo.deleteConsent(t.Context(), 9, 8))
	})

	t.Run("authorization code table", func(t *testing.T) {
		harness := newOAuthHarness(t, "")
		require.NoError(t, harness.db.Exec("DROP TABLE iam_oauth_codes").Error)
		assert.Error(t, harness.repo.insertAuthorizationCode(t.Context(), &model.AuthorizationCode{}))
		_, err := harness.repo.authorizationCode(t.Context(), "hash", 8)
		assert.Error(t, err)
		assert.Error(t, harness.repo.consumeAuthorizationCode(t.Context(), "hash", oauthTestNow, nil))
		// The refresh half succeeds and the code half fails, which is the only
		// way to reach the second error return in a revocation.
		assert.Error(t, harness.repo.revokeGrantedTokens(t.Context(), 9, 8, oauthTestNow))
	})

	t.Run("service account credential table", func(t *testing.T) {
		harness := newOAuthHarness(t, "")
		require.NoError(t, harness.db.Exec("DROP TABLE iam_service_account_credentials").Error)
		_, err := harness.repo.serviceAccountClient(t.Context(), 8)
		assert.Error(t, err)
		assert.Error(t, harness.repo.touchServiceAccountCredential(t.Context(), 8, oauthTestNow))
	})
}

// TestOAuthRepositoryWritesThatMatchNoRowAreRefused pins the row-count check on
// the three writes whose predicate is the whole point. Each one is single-use or
// self-service, so "nothing matched" is a caller mistake and has to reach the
// caller as one: reporting success would tell somebody their consent had been
// withdrawn, or let a consent challenge be answered twice.
func TestOAuthRepositoryWritesThatMatchNoRowAreRefused(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")

	assert.ErrorIs(t, harness.repo.deleteConsent(t.Context(), 5201, 6201), ErrInvalidRequest,
		"withdrawing a consent nobody granted is not a withdrawal")
	assert.ErrorIs(t, harness.repo.resolveConsentRequest(t.Context(), "no-such-challenge", oauthTestNow),
		ErrInvalidRequest, "an unknown challenge cannot be resolved")
	assert.ErrorIs(t, harness.repo.touchServiceAccountCredential(t.Context(), 6202, oauthTestNow),
		ErrInvalidRequest, "a credential that is gone or revoked cannot record a use")
}
