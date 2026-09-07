package federation

import (
	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/extension/keyringx"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/federation/model"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var federationTestNow = time.Now().UTC().Truncate(time.Millisecond)

type federationTestIDs struct{ next atomic.Int64 }

func newFederationTestIDs(start int64) *federationTestIDs {
	ids := &federationTestIDs{}
	ids.next.Store(start)
	return ids
}

func (ids *federationTestIDs) Next() (int64, error) { return ids.next.Add(1), nil }

func federationID(value string) guid.ID {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	id, err := guid.Parse(strconv.FormatUint(hash.Sum64()>>1, 10))
	if err != nil {
		panic(err)
	}
	return id
}

type federationPrincipal struct {
	ID          guid.ID `gorm:"primaryKey"`
	LoginName   string
	Email       string
	DisplayName string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type federationMember struct {
	TenantID    guid.ID `gorm:"primaryKey"`
	PrincipalID guid.ID `gorm:"primaryKey"`
	DisplayName string
	Email       string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type federationRole struct {
	TenantID    guid.ID `gorm:"primaryKey"`
	ID          guid.ID `gorm:"primaryKey"`
	Name        string
	NameKey     string
	Description string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type federationRoleMember struct {
	TenantID    guid.ID `gorm:"primaryKey"`
	RoleID      guid.ID `gorm:"primaryKey"`
	PrincipalID guid.ID `gorm:"primaryKey"`
	CreatedAt   time.Time
}

type federationRevision struct {
	TenantID  guid.ID `gorm:"primaryKey"`
	Revision  int64
	UpdatedAt time.Time
}

type federationIdentity struct {
	ids    guid.Source
	prefix string
	web    *federationWeb
}

func (f *federationIdentity) EnsureExternalPrincipal(ctx context.Context, db *gorm.DB, tenantID guid.ID, email, displayName string, now time.Time) (guid.ID, bool, error) {
	var existing federationPrincipal
	err := db.WithContext(ctx).Table(f.prefix+"iam_principals").Where("email = ?", email).Take(&existing).Error
	if err == nil {
		var count int64
		if err := db.WithContext(ctx).Table(f.prefix+"iam_tenant_members").Where("tenant_id = ? AND principal_id = ?", tenantID, existing.ID).Count(&count).Error; err != nil {
			return 0, false, err
		}
		if count == 0 {
			member := federationMember{TenantID: tenantID, PrincipalID: existing.ID, DisplayName: displayName, Email: email, Status: "active", CreatedAt: now, UpdatedAt: now}
			if err := db.WithContext(ctx).Table(f.prefix + "iam_tenant_members").Create(&member).Error; err != nil {
				return 0, false, err
			}
		}
		f.web.setProfile(existing.ID, existing.LoginName, existing.Email)
		return existing.ID, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, err
	}
	next, err := f.ids.Next()
	if err != nil {
		return 0, false, err
	}
	id := guid.ID(next)
	principal := federationPrincipal{ID: id, LoginName: strings.ToLower(strings.Split(email, "@")[0]), Email: email, DisplayName: displayName, Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := db.WithContext(ctx).Table(f.prefix + "iam_principals").Create(&principal).Error; err != nil {
		return 0, false, err
	}
	member := federationMember{TenantID: tenantID, PrincipalID: id, DisplayName: displayName, Email: email, Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := db.WithContext(ctx).Table(f.prefix + "iam_tenant_members").Create(&member).Error; err != nil {
		return 0, false, err
	}
	f.web.setProfile(id, principal.LoginName, principal.Email)
	return id, true, nil
}

func (*federationIdentity) CreateProvisionedTo(context.Context, *gorm.DB, guid.ID, identity.ProvisionedPrincipalInput) (identity.Principal, error) {
	return identity.Principal{}, errors.New("not used")
}

func (*federationIdentity) ReplaceProvisionedTo(context.Context, *gorm.DB, guid.ID, guid.ID, identity.ProvisionedPrincipalInput) (identity.Principal, error) {
	return identity.Principal{}, errors.New("not used")
}

func (*federationIdentity) Get(context.Context, guid.ID, guid.ID) (identity.Principal, error) {
	return identity.Principal{}, errors.New("not used")
}

type federationWeb struct {
	mu       sync.Mutex
	next     int
	sessions map[string]*authnext.Claims
	profiles map[guid.ID]federationProfile
}

type federationProfile struct {
	subject  string
	username string
	email    string
}

func newFederationWeb() *federationWeb {
	return &federationWeb{sessions: map[string]*authnext.Claims{}, profiles: map[guid.ID]federationProfile{}}
}

func (w *federationWeb) setProfile(principalID guid.ID, username, email string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.profiles[principalID] = federationProfile{subject: principalID.String(), username: username, email: email}
}

func (w *federationWeb) ResolveReturnURL(value string) (string, error) {
	if value == "" {
		return "https://app.example/", nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host != "app.example" {
		return "", errors.New("invalid return URL")
	}
	return value, nil
}

func (w *federationWeb) CreateSession(_ context.Context, principalID guid.ID, now time.Time, assurance authnext.Assurance) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.next++
	token := fmt.Sprintf("session-%d", w.next)
	profile := federationProfile{subject: principalID.String(), username: "federated", email: "user@example.com"}
	if stored, ok := w.profiles[principalID]; ok {
		profile = stored
	}
	w.sessions[token] = &authnext.Claims{Subject: profile.subject, PrincipalID: principalID, PreferredUsername: profile.username, Email: profile.email, EmailVerified: true, AuthTime: assurance.AuthTime, ACR: assurance.Level, AMR: assurance.Methods, IssuedAt: now}
	return token, nil
}

func (w *federationWeb) Authenticate(_ context.Context, authorization, cookieHeader string) (*authnext.Claims, error) {
	token := strings.TrimPrefix(authorization, "Bearer ")
	if token == authorization {
		request := &http.Request{Header: http.Header{"Cookie": []string{cookieHeader}}}
		cookie, err := request.Cookie(w.SessionCookieName())
		if err != nil {
			return nil, err
		}
		token = cookie.Value
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	claims, ok := w.sessions[token]
	if !ok {
		return nil, errors.New("invalid session")
	}
	result := *claims
	return &result, nil
}

func (w *federationWeb) Logout(_ context.Context, cookieHeader string) string {
	request := &http.Request{Header: http.Header{"Cookie": []string{cookieHeader}}}
	if cookie, err := request.Cookie(w.SessionCookieName()); err == nil {
		w.mu.Lock()
		delete(w.sessions, cookie.Value)
		w.mu.Unlock()
	}
	return "https://app.example/login"
}

func (*federationWeb) CookieSecure() bool        { return true }
func (*federationWeb) SessionCookieName() string { return "custom_session" }
func (w *federationWeb) SessionCookie(token string) string {
	return (&http.Cookie{Name: w.SessionCookieName(), Value: token, Path: "/", HttpOnly: true, Secure: true}).String()
}
func (w *federationWeb) ClearCookie() string {
	return (&http.Cookie{Name: w.SessionCookieName(), Path: "/", MaxAge: -1}).String()
}
func (*federationWeb) Issuer() string { return "https://iam.example" }
func (*federationWeb) IssueTenantAccessTokenWithAssurance(context.Context, guid.ID, string, string, string, authnext.Assurance) (string, int64, error) {
	return "access", 60, nil
}
func (*federationWeb) IssueTenantIDTokenWithAssurance(context.Context, guid.ID, string, string, string, string, authnext.Assurance) (string, error) {
	return "id", nil
}

func (*federationWeb) ProjectProfile(context.Context, *authnext.Claims, string) (authn.ProfileClaims, error) {
	return authn.ProfileClaims{}, nil
}
func (*federationWeb) IssueTenantOAuthClientToken(context.Context, string, string, string, string, string) (string, int64, error) {
	return "client", 60, nil
}
func (*federationWeb) IssueTenantServiceAccountToken(context.Context, string, string, string, string, string, int64) (string, int64, error) {
	return "service", 60, nil
}
func (*federationWeb) JWKS() map[string]any                              { return map[string]any{} }
func (*federationWeb) ValidateCSRF(string, string, string, string) error { return nil }

type federationEnvironment struct {
	db       *gorm.DB
	runtime  *appenv.Runtime
	service  *Service
	web      *federationWeb
	identity *federationIdentity
	ids      *federationTestIDs
	key      []byte
	keys     *keyringx.Keyring
	prefix   string
	idp      *testIDP
	auditErr error
	events   []audit.DomainEvent
}

func newFederationEnvironment(t *testing.T, prefixes ...string) *federationEnvironment {
	t.Helper()
	prefix := ""
	if len(prefixes) > 0 {
		prefix = prefixes[0]
	}
	dsn := fmt.Sprintf("file:%s?_busy_timeout=10000&_journal_mode=WAL&_foreign_keys=on", filepath.ToSlash(filepath.Join(t.TempDir(), "federation.sqlite")))
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: "sqlite", DSN: dsn, Writable: true, Readable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	runtime := appenv.NewRuntime(appenv.NewIdentity("federation-test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(prefix))
	env := &federationEnvironment{db: router.Write(), runtime: runtime, prefix: prefix, ids: newFederationTestIDs(1000), key: []byte("0123456789abcdef0123456789abcdef"), web: newFederationWeb()}
	env.migrate(t)
	env.keys = newFederationTestKeyring(t, env.key)
	env.identity = &federationIdentity{ids: env.ids, prefix: prefix, web: env.web}
	appender := audit.Appender(func(_ context.Context, _ *gorm.DB, event audit.DomainEvent) error {
		if env.auditErr != nil {
			return env.auditErr
		}
		env.events = append(env.events, event)
		return nil
	})
	env.service = NewService(runtime, appender, env.identity, env.web, Config{HTTPTimeout: 5 * time.Second, ClockSkew: 30 * time.Second, StateTTL: 10 * time.Minute}, env.keys, env.ids, testOutboundPolicy())
	env.service.now = func() time.Time { return federationTestNow }
	env.idp = newTestIDP(t)
	return env
}

func insertRole(t *testing.T, db *gorm.DB, tenant, role string) {
	t.Helper()
	now := federationTestNow
	require.NoError(t, db.Table("iam_roles").Create(&federationRole{
		TenantID: testID(tenant), ID: testID(role), Name: role, NameKey: role,
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error)
}

func (e *federationEnvironment) migrate(t *testing.T) {
	t.Helper()
	for _, item := range []struct {
		name string
		row  any
	}{
		{model.Provider{}.TableName(), &model.Provider{}}, {model.IdentityLink{}.TableName(), &model.IdentityLink{}},
		{model.SAMLServiceProvider{}.TableName(), &model.SAMLServiceProvider{}}, {model.SAMLIDPKey{}.TableName(), &model.SAMLIDPKey{}},
		{model.ReplayClaim{}.TableName(), &model.ReplayClaim{}},
		{"iam_principals", &federationPrincipal{}}, {"iam_tenant_members", &federationMember{}},
		{"iam_roles", &federationRole{}}, {"iam_role_members", &federationRoleMember{}}, {"iam_policy_revisions", &federationRevision{}},
	} {
		require.NoError(t, e.db.Table(e.prefix+item.name).AutoMigrate(item.row), item.name)
	}
	require.NoError(t, e.db.Exec("CREATE UNIQUE INDEX "+e.prefix+"test_provider_issuer ON "+e.prefix+"iam_identity_providers (tenant_id, issuer)").Error)
	require.NoError(t, e.db.Exec("CREATE UNIQUE INDEX "+e.prefix+"test_saml_sp_entity ON "+e.prefix+"iam_saml_service_providers (tenant_id, entity_id)").Error)
	require.NoError(t, e.db.Exec("CREATE UNIQUE INDEX "+e.prefix+"test_identity_email ON "+e.prefix+"iam_principals (email)").Error)
}

func (e *federationEnvironment) providerInput(name, issuer string) ProviderInput {
	return ProviderInput{Name: name, ProviderType: ProviderOIDC, Issuer: issuer, ClientID: e.idp.clientID, ClientSecret: "client-secret", AutoProvision: true, Status: ProviderActive}
}

func (e *federationEnvironment) createProvider(t *testing.T, tenant guid.ID, input ProviderInput) Provider {
	t.Helper()
	provider, err := e.service.CreateProvider(t.Context(), tenant, input)
	require.NoError(t, err)
	return provider
}

func (e *federationEnvironment) startOIDC(t *testing.T, provider Provider) (LoginStart, loginState, string) {
	t.Helper()
	callback := e.idp.issuer + callbackURL(provider.ID)
	start, err := e.service.StartLogin(t.Context(), provider.ID, "https://app.example/", callback)
	require.NoError(t, err)
	stateValue, err := cookieValue(start.StateCookie, stateCookieName)
	require.NoError(t, err)
	state, err := openState(e.keys, stateValue)
	require.NoError(t, err)
	return start, state, callback
}

func (e *federationEnvironment) completeOIDC(t *testing.T, provider Provider) (LoginComplete, loginState, string, string) {
	t.Helper()
	start, state, callback := e.startOIDC(t, provider)
	stateValue, err := cookieValue(start.StateCookie, stateCookieName)
	require.NoError(t, err)
	code, returned := e.idp.authorize(t, e.idp.server.Client(), provider.Issuer, state.CodeVerifier, state.Nonce)
	require.Equal(t, state.Nonce, returned)
	result, err := e.service.CompleteLogin(t.Context(), provider.ID, code, state.Nonce, stateCookieName+"="+stateValue, callback)
	require.NoError(t, err)
	return result, state, stateValue, callback
}

func TestFederationProviderManagementIsTenantAndPrefixSafe(t *testing.T) {
	env := newFederationEnvironment(t, "sl_")
	tenantA, tenantB := federationID("tenant-a"), federationID("tenant-b")
	roleID := federationID("role-a")
	now := federationTestNow
	require.NoError(t, env.db.Table(env.prefix+"iam_roles").Create(&federationRole{TenantID: tenantA, ID: roleID, Name: "Member", NameKey: "member", Status: "active", CreatedAt: now, UpdatedAt: now}).Error)

	input := env.providerInput("Primary", "https://issuer.example/")
	input.DefaultRoleID = roleID
	provider := env.createProvider(t, tenantA, input)
	assert.Equal(t, "https://issuer.example", provider.Issuer)
	assert.Equal(t, defaultScopes, provider.Scopes)
	assert.True(t, provider.ClientSecretSet)
	assert.Equal(t, roleID, provider.DefaultRoleID)

	items, err := env.service.ListProviders(t.Context(), tenantA)
	require.NoError(t, err)
	require.Len(t, items, 1)
	other, err := env.service.ListProviders(t.Context(), tenantB)
	require.NoError(t, err)
	assert.Empty(t, other)
	_, err = env.service.UpdateProvider(t.Context(), tenantB, provider.ID, input)
	assert.ErrorIs(t, err, ErrProviderNotFound)
	assert.ErrorIs(t, env.service.DeleteProvider(t.Context(), tenantB, provider.ID), ErrProviderNotFound)

	input.Name, input.ClientSecret = "Renamed", ""
	updated, err := env.service.UpdateProvider(t.Context(), tenantA, provider.ID, input)
	require.NoError(t, err)
	assert.Equal(t, "Renamed", updated.Name)
	row, err := env.service.getProvider(t.Context(), tenantA, provider.ID)
	require.NoError(t, err)
	secret, err := env.service.decryptSecret(row.ID, row.ClientSecretCiphertext)
	require.NoError(t, err)
	assert.Equal(t, "client-secret", secret)

	_, err = env.service.CreateProvider(t.Context(), tenantA, env.providerInput("Duplicate", "https://issuer.example"))
	assert.ErrorIs(t, err, ErrProviderIssuerExists)
	require.NoError(t, env.service.DeleteProvider(t.Context(), tenantA, provider.ID))
	assert.ErrorIs(t, env.service.DeleteProvider(t.Context(), tenantA, provider.ID), ErrProviderNotFound)
}

func TestFederationOIDCLoginLinksIdentityAndRejectsStateReplay(t *testing.T) {
	env := newFederationEnvironment(t, "")
	tenant := federationID("tenant-a")
	env.idp.secret = "client-secret"
	provider := env.createProvider(t, tenant, env.providerInput("OIDC", env.idp.issuer))
	result, state, cookie, callback := env.completeOIDC(t, provider)
	assert.NotEmpty(t, result.SessionToken)
	assert.Equal(t, "https://app.example/", result.ReturnURL)
	claims, err := env.web.Authenticate(t.Context(), "", env.web.SessionCookie(result.SessionToken))
	require.NoError(t, err)
	assert.Equal(t, []string{"oidc"}, claims.AMR)

	link, err := env.service.repo.identityLink(t.Context(), provider.ID, tenant, "external-user-1")
	require.NoError(t, err)
	assert.Equal(t, claims.PrincipalID, link.PrincipalID)
	require.Len(t, env.events, 1)
	assert.Equal(t, tenant, env.events[0].TenantID)

	_, err = env.service.CompleteLogin(t.Context(), provider.ID, "replayed-code", state.Nonce, stateCookieName+"="+cookie, callback)
	assert.ErrorIs(t, err, ErrOIDCState)
	assert.Contains(t, env.service.StateClearCookie(), stateCookieName+"=")
}

func TestFederationOIDCFailureRollsBackIdentityAndTenantBinding(t *testing.T) {
	env := newFederationEnvironment(t, "sl_")
	tenantA, tenantB := federationID("tenant-a"), federationID("tenant-b")
	env.idp.secret = "client-secret"
	providerA := env.createProvider(t, tenantA, env.providerInput("A", env.idp.issuer))
	providerB := env.createProvider(t, tenantB, env.providerInput("B", env.idp.issuer+"/b"))

	start, state, callback := env.startOIDC(t, providerA)
	cookie, err := cookieValue(start.StateCookie, stateCookieName)
	require.NoError(t, err)
	state.ProviderID = providerB.ID.String()
	tamperedBinding, err := sealState(env.keys, state)
	require.NoError(t, err)
	_, err = env.service.CompleteLogin(t.Context(), providerB.ID, "code", state.Nonce, stateCookieName+"="+tamperedBinding, env.idp.issuer+callbackURL(providerB.ID))
	assert.ErrorIs(t, err, ErrOIDCState)

	code, _ := env.idp.authorize(t, env.idp.server.Client(), providerA.Issuer, state.CodeVerifier, state.Nonce)
	env.auditErr = errors.New("audit unavailable")
	_, err = env.service.CompleteLogin(t.Context(), providerA.ID, code, state.Nonce, stateCookieName+"="+cookie, callback)
	assert.ErrorContains(t, err, "audit unavailable")
	var principals, links int64
	require.NoError(t, env.db.Table(env.prefix+"iam_principals").Count(&principals).Error)
	require.NoError(t, env.db.Table(env.prefix+"iam_identity_links").Count(&links).Error)
	assert.Zero(t, principals)
	assert.Zero(t, links)
}

func TestFederationProviderValidationAndSecretFailures(t *testing.T) {
	env := newFederationEnvironment(t, "")
	tenant := federationID("tenant-a")
	bad := []ProviderInput{
		{},
		{Name: "x", ProviderType: "ldap", Issuer: "https://issuer.example", ClientID: "client", Status: ProviderActive},
		{Name: "x", ProviderType: ProviderOIDC, Issuer: "javascript:bad", ClientID: "client", Status: ProviderActive},
		{Name: "x", ProviderType: ProviderOIDC, Issuer: "https://issuer.example", ClientID: "client", Status: "pending"},
	}
	for _, input := range bad {
		_, err := env.service.CreateProvider(t.Context(), tenant, input)
		assert.ErrorIs(t, err, ErrInvalidProvider)
	}
	missingRole := env.providerInput("missing-role", "https://issuer.example")
	missingRole.DefaultRoleID = federationID("missing")
	_, err := env.service.CreateProvider(t.Context(), tenant, missingRole)
	assert.ErrorIs(t, err, ErrProviderRoleMissing)

	provider := env.createProvider(t, tenant, env.providerInput("secret", "https://secret.example"))
	row, err := env.service.getProvider(t.Context(), tenant, provider.ID)
	require.NoError(t, err)
	for _, ciphertext := range []string{"v9.bad", "v1.!!bad!!", "v1.AA", row.ClientSecretCiphertext[:len(row.ClientSecretCiphertext)-2] + "AA"} {
		_, err := env.service.decryptSecret(row.ID, ciphertext)
		assert.Error(t, err)
	}
	_, err = env.service.StartLogin(t.Context(), provider.ID, "javascript:bad", "https://issuer.example"+callbackURL(provider.ID))
	assert.Error(t, err)
}

// newFederationTestKeyring wraps raw key material the way the module does, so a
// test that seals through the ring and a test that reads a stored value are
// talking about the same key.
func newFederationTestKeyring(t *testing.T, active []byte, previous ...[]byte) *keyringx.Keyring {
	t.Helper()
	encoded := make([]string, 0, len(previous))
	for _, key := range previous {
		encoded = append(encoded, base64.RawStdEncoding.EncodeToString(key))
	}
	ring, err := keyringx.New(federationKeyDomain, base64.RawStdEncoding.EncodeToString(active), encoded...)
	require.NoError(t, err)
	return ring
}

func TestFederationEncryptionKeyResolution(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	expected := newFederationTestKeyring(t, key).ActiveID()

	_, err := ResolveKeyring(Config{})
	assert.Error(t, err)
	_, err = ResolveKeyring(Config{EncryptionKey: "bad"})
	assert.Error(t, err)

	file := filepath.Join(t.TempDir(), "federation.key")
	require.NoError(t, osWriteFile(file, []byte(base64.RawStdEncoding.EncodeToString(key))))
	ring, err := ResolveKeyring(Config{EncryptionKeyFile: file})
	require.NoError(t, err)
	assert.Equal(t, expected, ring.ActiveID())

	ring, err = ResolveKeyring(Config{EncryptionKey: base64.URLEncoding.EncodeToString(key)})
	require.NoError(t, err)
	assert.Equal(t, expected, ring.ActiveID())
}

// TestFederationResolvesTheKeysARotationLeavesBehind covers the configuration a
// deployment is in for as long as a rotation takes: one key sealing, one or
// more still opening.
func TestFederationResolvesTheKeysARotationLeavesBehind(t *testing.T) {
	active := []byte("0123456789abcdef0123456789abcdef")
	retired := []byte("fedcba9876543210fedcba9876543210")

	dir := t.TempDir()
	retiredFile := filepath.Join(dir, "federation.previous.key")
	require.NoError(t, osWriteFile(retiredFile, []byte(base64.RawStdEncoding.EncodeToString(retired))))

	ring, err := ResolveKeyring(Config{
		EncryptionKey:              base64.RawStdEncoding.EncodeToString(active),
		PreviousEncryptionKeyFiles: []string{retiredFile},
	})
	require.NoError(t, err)
	assert.Equal(t, newFederationTestKeyring(t, active, retired).IDs(), ring.IDs())

	// Naming the same key twice is how an operator convinces themselves they
	// have rotated when they have not, so it is refused rather than deduplicated.
	_, err = ResolveKeyring(Config{
		EncryptionKey:          base64.RawStdEncoding.EncodeToString(active),
		PreviousEncryptionKeys: []string{base64.RawStdEncoding.EncodeToString(active)},
	})
	require.Error(t, err)

	_, err = ResolveKeyring(Config{
		EncryptionKey:              base64.RawStdEncoding.EncodeToString(active),
		PreviousEncryptionKeys:     []string{base64.RawStdEncoding.EncodeToString(retired)},
		PreviousEncryptionKeyFiles: []string{retiredFile},
	})
	require.Error(t, err)
}

// TestFederationOpensWhatARetiredKeySealed is the whole point of the ring: a
// deployment that replaces its federation key must not lose the client secrets
// the replaced key protected.
func TestFederationOpensWhatARetiredKeySealed(t *testing.T) {
	env := newFederationEnvironment(t, "")
	provider := env.createProvider(t, federationID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))

	row, err := env.service.repo.getProviderByID(t.Context(), provider.ID)
	require.NoError(t, err)
	sealedByRetired := row.ClientSecretCiphertext
	require.NotEmpty(t, sealedByRetired)

	replacement := []byte("fedcba9876543210fedcba9876543210")
	env.service.keys = newFederationTestKeyring(t, replacement, env.key)

	secret, err := env.service.decryptSecret(provider.ID, sealedByRetired)
	require.NoError(t, err)
	assert.Equal(t, "client-secret", secret)

	// What the new ring seals records the new key, so rewrapping is a read and
	// a write rather than a migration somebody has to hand-write.
	assert.True(t, env.service.keys.NeedsRewrap(sealedByRetired))
	resealed, err := env.service.encryptSecret(provider.ID, secret)
	require.NoError(t, err)
	assert.False(t, env.service.keys.NeedsRewrap(resealed))
}

// TestFederationSaysWhenAKeyIsSimplyMissing keeps the one recoverable failure
// from being reported as data loss: the fix is to put the retired key back.
func TestFederationSaysWhenAKeyIsSimplyMissing(t *testing.T) {
	env := newFederationEnvironment(t, "")
	provider := env.createProvider(t, federationID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	sealed, err := env.service.encryptSecret(provider.ID, "client-secret")
	require.NoError(t, err)

	env.service.keys = newFederationTestKeyring(t, []byte("fedcba9876543210fedcba9876543210"))
	_, err = env.service.decryptSecret(provider.ID, sealed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no longer holds")
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func TestFederationServicePropagatesProviderDatabaseFailures(t *testing.T) {
	env := newFederationEnvironment(t)
	require.NoError(t, env.db.Exec("DROP TABLE iam_identity_providers").Error)
	input := env.providerInput("Broken", "https://idp.example")

	_, err := env.service.ListProviders(t.Context(), testID("tenant-a"))
	assert.Error(t, err)
	_, err = env.service.CreateProvider(t.Context(), testID("tenant-a"), input)
	assert.Error(t, err)
	_, err = env.service.UpdateProvider(t.Context(), testID("tenant-a"), testID("missing"), input)
	assert.Error(t, err)
	assert.Error(t, env.service.DeleteProvider(t.Context(), testID("tenant-a"), testID("missing")))
	_, err = env.service.StartLogin(t.Context(), testID("missing"), "https://app.example/", "https://app.example/callback")
	assert.Error(t, err)
	_, err = env.service.CompleteLogin(t.Context(), testID("missing"), "code", "state", "", "https://app.example/callback")
	assert.Error(t, err)
}

func TestFederationProviderAdditionalManagementAndValidation(t *testing.T) {
	env := newFederationEnvironment(t)
	tenant := testID("tenant-a")

	_, err := env.service.ListProviders(t.Context(), 0)
	assert.ErrorIs(t, err, ErrInvalidProvider)
	_, err = env.service.CreateProvider(t.Context(), tenant, env.providerInput(strings.Repeat("a", 129), "https://issuer.example"))
	assert.ErrorIs(t, err, ErrInvalidProvider)
	invalid := env.providerInput("Invalid", "https://issuer.example")
	invalid.Status = "pending"
	_, err = env.service.CreateProvider(t.Context(), tenant, invalid)
	assert.ErrorIs(t, err, ErrInvalidProvider)
	invalid = env.providerInput("Invalid", "https://issuer.example")
	invalid.Scopes = "openid " + strings.Repeat("x", 65)
	_, err = env.service.CreateProvider(t.Context(), tenant, invalid)
	assert.ErrorIs(t, err, ErrInvalidProvider)

	first := env.createProvider(t, tenant, env.providerInput("First", "https://first.example"))
	second := env.createProvider(t, tenant, env.providerInput("Second", "https://second.example"))
	update := env.providerInput("Second", first.Issuer)
	_, err = env.service.UpdateProvider(t.Context(), tenant, second.ID, update)
	assert.ErrorIs(t, err, ErrProviderIssuerExists)
	update = env.providerInput("First Rotated", first.Issuer)
	update.ClientSecret = "new-secret"
	_, err = env.service.UpdateProvider(t.Context(), tenant, first.ID, update)
	require.NoError(t, err)
	row, err := env.service.getProvider(t.Context(), tenant, first.ID)
	require.NoError(t, err)
	plain, err := env.service.decryptSecret(first.ID, row.ClientSecretCiphertext)
	require.NoError(t, err)
	assert.Equal(t, "new-secret", plain)
}

func TestFederationLoginReusesLinkAndEnforcesProvisioningPolicy(t *testing.T) {
	env := newFederationEnvironment(t)
	env.idp.secret = "client-secret"
	tenant := testID("tenant-a")
	provider := env.createProvider(t, tenant, env.providerInput("OIDC", env.idp.issuer))

	first, _, _, _ := env.completeOIDC(t, provider)
	second, _, _, _ := env.completeOIDC(t, provider)
	firstClaims, err := env.web.Authenticate(t.Context(), "", env.web.SessionCookie(first.SessionToken))
	require.NoError(t, err)
	secondClaims, err := env.web.Authenticate(t.Context(), "", env.web.SessionCookie(second.SessionToken))
	require.NoError(t, err)
	assert.Equal(t, firstClaims.PrincipalID, secondClaims.PrincipalID)
	var count int64
	require.NoError(t, env.db.Table("iam_principals").Where("email = ?", "user@example.com").Count(&count).Error)
	assert.EqualValues(t, 1, count)

	noProvision := env.createProvider(t, tenant, ProviderInput{
		Name: "No Provision", ProviderType: ProviderOIDC, Issuer: env.idp.issuer + "/no-provision",
		ClientID: env.idp.clientID, ClientSecret: "client-secret", AutoProvision: false, Status: ProviderActive,
	})
	start, state, callback := env.startOIDC(t, noProvision)
	stateValue, err := cookieValue(start.StateCookie, stateCookieName)
	require.NoError(t, err)
	code, _ := env.idp.authorize(t, env.idp.server.Client(), noProvision.Issuer, state.CodeVerifier, state.Nonce)
	_, err = env.service.CompleteLogin(t.Context(), noProvision.ID, code, state.Nonce, stateCookieName+"="+stateValue, callback)
	assert.ErrorIs(t, err, ErrProvisioningDisabled)

	unverified := env.createProvider(t, tenant, env.providerInput("Unverified", env.idp.issuer+"/unverified"))
	env.idp.setClaim("email_verified", false)
	start, state, callback = env.startOIDC(t, unverified)
	stateValue, err = cookieValue(start.StateCookie, stateCookieName)
	require.NoError(t, err)
	code, _ = env.idp.authorize(t, env.idp.server.Client(), unverified.Issuer, state.CodeVerifier, state.Nonce)
	_, err = env.service.CompleteLogin(t.Context(), unverified.ID, code, state.Nonce, stateCookieName+"="+stateValue, callback)
	assert.ErrorIs(t, err, ErrProvisioningUnverified)
}

func TestFederationDefaultRoleAndInactiveMember(t *testing.T) {
	env := newFederationEnvironment(t)
	env.idp.secret = "client-secret"
	tenant, roleID := testID("tenant-a"), testID("role-a")
	insertRole(t, env.db, "tenant-a", "role-a")
	input := env.providerInput("Role Grant", env.idp.issuer)
	input.DefaultRoleID = roleID
	provider := env.createProvider(t, tenant, input)
	result, _, _, _ := env.completeOIDC(t, provider)
	claims, err := env.web.Authenticate(t.Context(), "", env.web.SessionCookie(result.SessionToken))
	require.NoError(t, err)

	var count int64
	require.NoError(t, env.db.Table("iam_role_members").Where(
		"tenant_id = ? AND role_id = ? AND principal_id = ?", tenant, roleID, claims.PrincipalID,
	).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	var revision federationRevision
	require.NoError(t, env.db.Table("iam_policy_revisions").Where("tenant_id = ?", tenant).Take(&revision).Error)
	assert.EqualValues(t, 1, revision.Revision)

	require.NoError(t, env.db.Table("iam_tenant_members").Where(
		"tenant_id = ? AND principal_id = ?", tenant, claims.PrincipalID,
	).Update("status", "disabled").Error)
	start, state, callback := env.startOIDC(t, provider)
	stateValue, err := cookieValue(start.StateCookie, stateCookieName)
	require.NoError(t, err)
	code, _ := env.idp.authorize(t, env.idp.server.Client(), provider.Issuer, state.CodeVerifier, state.Nonce)
	_, err = env.service.CompleteLogin(t.Context(), provider.ID, code, state.Nonce, stateCookieName+"="+stateValue, callback)
	assert.ErrorIs(t, err, ErrPrincipalInactive)
}

func TestFederationMissingDefaultRoleAtProvisioningIsAudited(t *testing.T) {
	env := newFederationEnvironment(t)
	env.idp.secret = "client-secret"
	tenant, roleID := testID("tenant-a"), testID("role-a")
	insertRole(t, env.db, "tenant-a", "role-a")
	input := env.providerInput("Role Gone", env.idp.issuer)
	input.DefaultRoleID = roleID
	provider := env.createProvider(t, tenant, input)
	require.NoError(t, env.db.Table("iam_roles").Where("tenant_id = ? AND id = ?", tenant, roleID).Delete(&federationRole{}).Error)

	result, _, _, _ := env.completeOIDC(t, provider)
	assert.NotEmpty(t, result.SessionToken)
	require.NotEmpty(t, env.events)
	assert.Equal(t, true, env.events[len(env.events)-1].Detail["default_role_missing"])
}

func TestFederationLoginStateProviderAndSecretDenials(t *testing.T) {
	env := newFederationEnvironment(t)
	tenant := testID("tenant-a")
	_, err := env.service.StartLogin(t.Context(), 0, "https://app.example/", "https://app.example/callback")
	assert.ErrorIs(t, err, ErrProviderNotFound)

	disabled := env.createProvider(t, tenant, env.providerInput("Disabled", env.idp.issuer+"/disabled"))
	input := env.providerInput("Disabled", disabled.Issuer)
	input.Status = ProviderDisabled
	_, err = env.service.UpdateProvider(t.Context(), tenant, disabled.ID, input)
	require.NoError(t, err)
	_, err = env.service.CompleteLogin(t.Context(), disabled.ID, "code", "state", "", env.idp.issuer+callbackURL(disabled.ID))
	assert.ErrorIs(t, err, ErrProviderDisabled)

	provider := env.createProvider(t, tenant, env.providerInput("State", env.idp.issuer+"/state"))
	_, state, callback := env.startOIDC(t, provider)
	state.ExpiresAt = env.service.now().Add(-time.Minute).Unix()
	stale, err := sealState(env.keys, state)
	require.NoError(t, err)
	_, err = env.service.CompleteLogin(t.Context(), provider.ID, "code", state.Nonce, stateCookieName+"="+stale, callback)
	assert.ErrorIs(t, err, ErrOIDCState)

	state.ExpiresAt = env.service.now().Add(time.Minute).Unix()
	state.ProviderID = testID("other").String()
	wrongProvider, err := sealState(env.keys, state)
	require.NoError(t, err)
	_, err = env.service.CompleteLogin(t.Context(), provider.ID, "code", state.Nonce, stateCookieName+"="+wrongProvider, callback)
	assert.ErrorIs(t, err, ErrOIDCState)
	state.ProviderID = provider.ID.String()
	state.TenantID = testID("tenant-b").String()
	wrongTenant, err := sealState(env.keys, state)
	require.NoError(t, err)
	_, err = env.service.CompleteLogin(t.Context(), provider.ID, "code", state.Nonce, stateCookieName+"="+wrongTenant, callback)
	assert.ErrorIs(t, err, ErrOIDCState)

	corrupted := env.createProvider(t, tenant, env.providerInput("Corrupted", env.idp.issuer+"/corrupted"))
	require.NoError(t, env.db.Table("iam_identity_providers").Where("id = ?", corrupted.ID).Update("client_secret_ciphertext", "garbage").Error)
	start, state, callback := env.startOIDC(t, corrupted)
	stateValue, err := cookieValue(start.StateCookie, stateCookieName)
	require.NoError(t, err)
	_, err = env.service.CompleteLogin(t.Context(), corrupted.ID, "code", state.Nonce, stateCookieName+"="+stateValue, callback)
	assert.Error(t, err)
}

func TestFederationHelpersAndConstructorGuards(t *testing.T) {
	assert.Equal(t, defaultScopes, normalizeScopes(""))
	assert.Equal(t, "openid profile", normalizeScopes(" openid profile openid "))
	assert.Empty(t, normalizeScopes("openid "+strings.Repeat("x", 65)))
	assert.Equal(t, defaultScopes, normalizeScopes("   "))

	env := newFederationEnvironment(t)
	_, err := env.service.getProviderByID(t.Context(), 0)
	assert.ErrorIs(t, err, ErrProviderNotFound)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("Secret", "https://secret.example"))
	row, err := env.service.getProvider(t.Context(), testID("tenant-a"), provider.ID)
	require.NoError(t, err)
	_, err = env.service.decryptSecret(row.ID, "v9."+strings.TrimPrefix(row.ClientSecretCiphertext, "v1."))
	assert.Error(t, err)
	_, err = env.service.decryptSecret(row.ID, "v1.!!bad-base64!!")
	assert.Error(t, err)
	_, err = env.service.decryptSecret(row.ID, "v1.AA")
	assert.Error(t, err)
	tampered := row.ClientSecretCiphertext[:len(row.ClientSecretCiphertext)-3] + "AAA"
	_, err = env.service.decryptSecret(row.ID, tampered)
	assert.Error(t, err)

	config := Config{}
	assert.Panics(t, func() {
		NewService(nil, env.service.audit, env.identity, env.web, config, env.keys, env.ids, testOutboundPolicy())
	})
	assert.Panics(t, func() {
		NewService(env.runtime, nil, env.identity, env.web, config, env.keys, env.ids, testOutboundPolicy())
	})
	assert.Panics(t, func() {
		NewService(env.runtime, env.service.audit, nil, env.web, config, env.keys, env.ids, testOutboundPolicy())
	})
	assert.Panics(t, func() {
		NewService(env.runtime, env.service.audit, env.identity, nil, config, env.keys, env.ids, testOutboundPolicy())
	})
	assert.Panics(t, func() {
		NewService(env.runtime, env.service.audit, env.identity, env.web, config, env.keys, nil, testOutboundPolicy())
	})
}

func TestFederationServiceRejectsInvalidIDsAndGeneratorFailures(t *testing.T) {
	env := newFederationEnvironment(t)
	tenant := testID("tenant-a")
	providerInput := env.providerInput("Provider", "https://provider.example")
	originalNextID := env.service.nextID
	env.service.nextID = func() (guid.ID, error) { return 0, errors.New("id generator unavailable") }
	_, err := env.service.CreateProvider(t.Context(), tenant, providerInput)
	assert.ErrorContains(t, err, "id generator unavailable")
	env.service.nextID = func() (guid.ID, error) { return 0, nil }
	_, err = env.service.CreateProvider(t.Context(), tenant, providerInput)
	assert.ErrorIs(t, err, ErrInvalidProvider)
	env.service.nextID = originalNextID

	provider := env.createProvider(t, tenant, providerInput)
	_, err = env.service.UpdateProvider(t.Context(), 0, provider.ID, providerInput)
	assert.ErrorIs(t, err, ErrInvalidProvider)
	_, err = env.service.UpdateProvider(t.Context(), tenant, provider.ID, ProviderInput{})
	assert.ErrorIs(t, err, ErrInvalidProvider)
	missingRole := providerInput
	missingRole.DefaultRoleID = testID("missing-role")
	_, err = env.service.UpdateProvider(t.Context(), tenant, provider.ID, missingRole)
	assert.ErrorIs(t, err, ErrProviderRoleMissing)
	assert.ErrorIs(t, env.service.DeleteProvider(t.Context(), tenant, 0), ErrInvalidProvider)

	tsp := newSAMLSPServer(t)
	samlInput := SAMLServiceProviderInput{Name: "portal", EntityID: tsp.entityID, MetadataXML: tsp.metadataXML(), Status: samlSPStatusActive}
	_, err = env.service.ListSAMLServiceProviders(t.Context(), 0)
	assert.ErrorIs(t, err, ErrInvalidSAMLSP)
	env.service.nextID = func() (guid.ID, error) { return 0, errors.New("id generator unavailable") }
	_, err = env.service.CreateSAMLServiceProvider(t.Context(), tenant, samlInput)
	assert.ErrorContains(t, err, "id generator unavailable")
	env.service.nextID = func() (guid.ID, error) { return 0, nil }
	_, err = env.service.CreateSAMLServiceProvider(t.Context(), tenant, samlInput)
	assert.ErrorIs(t, err, ErrInvalidSAMLSP)
	env.service.nextID = originalNextID
	_, err = env.service.UpdateSAMLServiceProvider(t.Context(), tenant, 0, samlInput)
	assert.ErrorIs(t, err, ErrInvalidSAMLSP)
	_, err = env.service.UpdateSAMLServiceProvider(t.Context(), tenant, provider.ID, SAMLServiceProviderInput{})
	assert.ErrorIs(t, err, ErrInvalidSAMLSP)
	assert.ErrorIs(t, env.service.DeleteSAMLServiceProvider(t.Context(), tenant, 0), ErrInvalidSAMLSP)
}

func TestFederationServicePropagatesSAMLTableFailures(t *testing.T) {
	env := newFederationEnvironment(t)
	tenant := testID("tenant-a")
	tsp := newSAMLSPServer(t)
	input := SAMLServiceProviderInput{Name: "portal", EntityID: tsp.entityID, MetadataXML: tsp.metadataXML(), Status: samlSPStatusActive}
	require.NoError(t, env.db.Exec("DROP TABLE iam_saml_service_providers").Error)

	_, err := env.service.ListSAMLServiceProviders(t.Context(), tenant)
	assert.Error(t, err)
	_, err = env.service.CreateSAMLServiceProvider(t.Context(), tenant, input)
	assert.Error(t, err)
	_, err = env.service.UpdateSAMLServiceProvider(t.Context(), tenant, testID("sp"), input)
	assert.Error(t, err)
	assert.Error(t, env.service.DeleteSAMLServiceProvider(t.Context(), tenant, testID("sp")))
}

// testOutboundPolicy permits the loopback address the fake upstream in these
// tests listens on. The shipped default refuses it, which is the point of the
// guard; allowing it here is the same thing a deployment does for a provider on
// its own network, and the guard itself is covered in pkg/extension/httpx.
func testOutboundPolicy() httpx.Policy {
	return httpx.Policy{
		AllowPlaintext: true,
		Allow:          []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")},
	}
}

// TestFederationOutboundPolicyRefusesMalformedNetworks covers the startup path:
// a typo in the allow list has to stop the process rather than quietly widen or
// narrow what federation can reach.
func TestFederationOutboundPolicyRefusesMalformedNetworks(t *testing.T) {
	_, err := ResolveOutboundPolicy(Config{OutboundAllowNetworks: []string{"10.0.0.0/8", "not-a-cidr"}})
	assert.ErrorContains(t, err, "federation outbound policy")

	policy, err := ResolveOutboundPolicy(Config{OutboundAllowNetworks: []string{"10.0.0.0/8"}})
	require.NoError(t, err)
	assert.Len(t, policy.Allow, 1)

	strict, err := ResolveOutboundPolicy(Config{})
	require.NoError(t, err)
	assert.Equal(t, httpx.Policy{}, strict, "an unconfigured deployment gets the strict policy")
}

// TestFederationIssuerRefusesInternalAddresses is the negative half of the
// guard: a tenant naming an issuer inside the network must be refused when the
// provider is saved, not merely when the discovery request is attempted.
func TestFederationIssuerRefusesInternalAddresses(t *testing.T) {
	strict := httpx.Policy{}
	for name, issuer := range map[string]string{
		"cloud metadata": "https://169.254.169.254/",
		"loopback":       "https://127.0.0.1:8443/realms/main",
		"ipv6 loopback":  "https://[::1]/realms/main",
		"private ten":    "https://10.0.0.5/realms/main",
		"private 192":    "https://192.168.1.10/realms/main",
		"plaintext":      "http://idp.example.test/realms/main",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeIssuer(issuer, strict)
			assert.ErrorContains(t, err, "invalid issuer", "%s must not be a usable issuer", issuer)
		})
	}

	normalized, err := normalizeIssuer("https://idp.example.test/realms/main/", strict)
	require.NoError(t, err)
	assert.Equal(t, "https://idp.example.test/realms/main", normalized)

	internal, err := normalizeIssuer("https://10.0.0.5/realms/main", httpx.Policy{Allow: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}})
	require.NoError(t, err)
	assert.Equal(t, "https://10.0.0.5/realms/main", internal)
}
