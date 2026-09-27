package federation

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn"
	authnmodel "7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation/model"
	"7link.com/platform/internal/iam/identity"
	identitymodel "7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type rejectIdentityJIT struct{ identity.IdentityProvisioner }

func (*rejectIdentityJIT) EnsureExternalPrincipal(context.Context, *gorm.DB, guid.ID, string, string, time.Time) (guid.ID, bool, error) {
	return 0, false, assert.AnError
}

func newIdentityLinkEnvironment(t *testing.T) (*federationEnvironment, *authn.Service, guid.ID, string) {
	t.Helper()
	env := newFederationEnvironment(t)
	require.NoError(t, env.db.AutoMigrate(&identitymodel.Principal{}, &authnmodel.Credential{}, &authnmodel.Session{}, &authnmodel.PasswordHistory{}, &authnmodel.ReauthenticationChallenge{}, &model.IdentityLinkTransaction{}))
	tenant := testID("tenant-a")
	require.NoError(t, env.db.Exec("UPDATE iam_tenants SET status = ? WHERE id = ?", "active", tenant).Error)
	principalID := guid.ID(101)
	require.NoError(t, env.db.Create(&identitymodel.Principal{ID: principalID, LoginName: "owner", Email: "owner@example.test", DisplayName: "Owner", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error)
	require.NoError(t, env.db.Table("iam_tenant_members").Create(&federationMember{TenantID: tenant, PrincipalID: principalID, Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error)
	repo := authn.NewRepository(env.runtime, env.ids, env.service.audit)
	require.NoError(t, repo.CreateBootstrapCredentialOn(t.Context(), env.db, principalID, "correct horse battery staple", time.Now().UTC()))
	authenticator, err := authn.NewWebService(authnext.Config{Enabled: true, Issuer: "https://iam.example", SigningKey: base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))),
		MFA: authnext.MFAConfig{EncryptionKey: base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("m", 32)))},
		Web: authnext.WebConfig{Enabled: true, PostLoginURL: "https://app.example/", AllowedReturnURLs: []string{"https://app.example/"}, AllowedOrigins: []string{"https://app.example"}}}, repo)
	require.NoError(t, err)
	instance := authn.NewModule(env.runtime, authn.Config{})
	instance.Service = authenticator
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, services.Seal())
	// A deployment binds these through the connectors authn publishes and the
	// receiver identity pushes into, and the module hands the holder to the
	// repository in Initialize. This harness builds the repository itself, so it
	// fills the same four sets itself -- without them authn refuses the
	// JOINs rather than naming another module's table.
	require.NoError(t, repo.BindPrincipalState(
		identityPrincipalStateForFederation(t, env.runtime.TablePrefix()),
	))
	grants := oauth.PublishedGrantTableNames(env.runtime.TablePrefix())
	require.NoError(t, repo.BindForeignTables(
		authn.DirectoryTableNames{
			Tenants: organization.PublishedDirectoryTableNames(env.runtime.TablePrefix()).Tenants,
		},
		authn.OAuthTableNames{Clients: grants.Clients, RefreshTokens: grants.RefreshTokens},
		core.PublishedAccessTableNames(env.runtime.TablePrefix()),
		identity.PublishedPrincipalTableNames(env.runtime.TablePrefix()),
	))
	env.service.accountProofs, err = module.Resolve[authn.ReauthenticationConsumer](services)
	require.NoError(t, err)
	env.service.accountReauthentication, err = module.Resolve[authn.FederatedReauthentication](services)
	require.NoError(t, err)
	env.service.authn = authenticator
	proofTrust, err := module.Resolve[authn.FederatedProofTrustConnector](services)
	require.NoError(t, err)
	require.NoError(t, proofTrust.Bind(env.service.proofSourceTrusted))
	env.service.identities = &rejectIdentityJIT{}
	session, err := authenticator.CreateSession(t.Context(), principalID, time.Now().UTC(), authnext.Assurance{AuthTime: time.Now().UTC(), Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	return env, authenticator, principalID, authenticator.SessionCookie(session)
}

func identityPasswordProof(t *testing.T, service *authn.Service, cookie, purpose, target string) string {
	t.Helper()
	options, err := service.BeginReauthentication(t.Context(), cookie, purpose, target, "password", "")
	require.NoError(t, err)
	require.NoError(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil))
	return options.Proof
}

func TestAccountViewPersonalIdentityReadsUseTargetWithoutTouchingSessions(t *testing.T) {
	env, _, operatorID, cookie := newIdentityLinkEnvironment(t)
	operatorProvider := env.createProvider(t, testID("tenant-a"), env.providerInput("Operator provider", env.idp.issuer))
	targetProvider := env.createProvider(t, testID("tenant-b"), env.providerInput("Target provider", env.idp.issuer))
	targetID := guid.ID(202)
	now := time.Now().UTC()
	require.NoError(t, env.db.Create(&identitymodel.Principal{ID: targetID, LoginName: "target", Status: "active", CreatedAt: now, UpdatedAt: now}).Error)
	require.NoError(t, env.db.Exec("UPDATE iam_tenants SET status = 'active' WHERE id = ?", targetProvider.TenantID).Error)
	require.NoError(t, env.db.Table("iam_tenant_members").Create(&federationMember{TenantID: targetProvider.TenantID, PrincipalID: targetID, Status: "active", CreatedAt: now, UpdatedAt: now}).Error)
	for _, link := range []model.IdentityLink{
		{ProviderID: operatorProvider.ID, TenantID: operatorProvider.TenantID, ExternalSubject: "operator-subject", PrincipalID: operatorID, CreatedAt: now, UpdatedAt: now},
		{ProviderID: targetProvider.ID, TenantID: targetProvider.TenantID, ExternalSubject: "target-subject", PrincipalID: targetID, CreatedAt: now, UpdatedAt: now},
	} {
		require.NoError(t, env.db.Create(&link).Error)
	}
	var before, after []authnmodel.Session
	require.NoError(t, env.db.Order("id_hash").Find(&before).Error)
	ctx := authnext.WithClaims(t.Context(), &authnext.Claims{
		PrincipalID: targetID,
		AccountView: &authnext.AccountViewIdentity{ID: 50, ActorID: operatorID, ReadOnly: true},
	})
	for _, rawCookie := range []string{cookie, "invalid", ""} {
		links, err := env.service.ListPersonalIdentityLinks(ctx, rawCookie)
		require.NoError(t, err)
		require.Len(t, links, 1)
		assert.Equal(t, "target-subject", links[0].ExternalSubject)
		providers, err := env.service.ListLinkableProviders(ctx, rawCookie)
		require.NoError(t, err)
		require.Len(t, providers, 1)
		assert.Equal(t, targetProvider.ID, providers[0].ID)
	}
	require.NoError(t, env.db.Order("id_hash").Find(&after).Error)
	assert.Equal(t, before, after)
	for _, view := range []*authnext.AccountViewIdentity{{ID: 50}, {ActorID: operatorID}} {
		invalid := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: targetID, AccountView: view})
		_, err := env.service.ListPersonalIdentityLinks(invalid, cookie)
		assert.ErrorIs(t, err, authnext.ErrInvalidSession)
		_, err = env.service.ListLinkableProviders(invalid, cookie)
		assert.ErrorIs(t, err, authnext.ErrInvalidSession)
	}
}

func verifyIdentityOIDC(t *testing.T, env *federationEnvironment, provider Provider, start LoginStart) string {
	t.Helper()
	state, code := issueIdentityOIDC(t, env, start)
	require.NoError(t, env.service.VerifyIdentityOperation(t.Context(), provider.ID, state, code, "", "https://iam.example"+callbackURL(provider.ID)))
	return state
}

func issueIdentityOIDC(t *testing.T, env *federationEnvironment, start LoginStart) (string, string) {
	t.Helper()
	endpoint, err := url.Parse(start.AuthorizationURL)
	require.NoError(t, err)
	state := endpoint.Query().Get("state")
	assert.Equal(t, "login", endpoint.Query().Get("prompt"))
	assert.Equal(t, "0", endpoint.Query().Get("max_age"))
	assert.Equal(t, state, endpoint.Query().Get("nonce"))
	env.idp.setClaim("nonce", state)
	env.idp.setClaim("auth_time", time.Now().Unix())
	response, err := env.idp.server.Client().Get(start.AuthorizationURL)
	require.NoError(t, err)
	defer response.Body.Close()
	var issued struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&issued))
	return state, issued.Code
}

func TestIdentityLinkHTTPStorageFailuresPreserveCallbackAndContinuation(t *testing.T) {
	env, authenticator, _, cookie := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	router, api := humatest.New(t)
	registerBrowserHTTP(api, env.service)
	registerPersonalIdentityHTTP(api, env.service)
	request := func(method, path, body, cookies string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "https://iam.example"+path, strings.NewReader(body))
		req.Header.Set("Cookie", cookies)
		req.Header.Set("Origin", "https://app.example")
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}
	fault := func(operation, table string) func() {
		callback := func(tx *gorm.DB) {
			if tx.Statement.Table == table || tx.Statement.TableExpr != nil && strings.Contains(tx.Statement.TableExpr.SQL, table) {
				tx.AddError(assert.AnError)
			}
		}
		switch operation {
		case "row":
			require.NoError(t, env.db.Callback().Row().Before("gorm:row").Register("identity_store_failure", callback))
			return func() { require.NoError(t, env.db.Callback().Row().Remove("identity_store_failure")) }
		case "query":
			require.NoError(t, env.db.Callback().Query().Before("gorm:query").Register("identity_store_failure", callback))
			return func() { require.NoError(t, env.db.Callback().Query().Remove("identity_store_failure")) }
		case "create":
			require.NoError(t, env.db.Callback().Create().Before("gorm:create").Register("identity_store_failure", callback))
			return func() { require.NoError(t, env.db.Callback().Create().Remove("identity_store_failure")) }
		case "delete":
			require.NoError(t, env.db.Callback().Delete().Before("gorm:delete").Register("identity_store_failure", callback))
			return func() { require.NoError(t, env.db.Callback().Delete().Remove("identity_store_failure")) }
		default:
			require.NoError(t, env.db.Callback().Update().Before("gorm:update").Register("identity_store_failure", callback))
			return func() { require.NoError(t, env.db.Callback().Update().Remove("identity_store_failure")) }
		}
	}
	for _, route := range []struct{ path, table string }{{"/principals/" + "101" + "/identity-links", "iam_identity_links"}, {"/principals/" + "101" + "/identity-link-providers", "iam_identity_providers"}} {
		restore := fault("row", route.table)
		assert.Equal(t, http.StatusInternalServerError, request(http.MethodGet, route.path, "", cookie).Code)
		restore()
	}
	proof := identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityLink, provider.ID.String())
	body, err := json.Marshal(map[string]any{"provider_id": provider.ID, "proof": proof})
	require.NoError(t, err)
	restore := fault("create", "iam_identity_link_transactions")
	assert.Equal(t, http.StatusInternalServerError, request(http.MethodPost, "/principals/"+"101"+"/identity-link-transactions", string(body), cookie).Code)
	restore()
	start, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "", "https://iam.example"+callbackURL(provider.ID))
	require.NoError(t, err, "failed transaction creation must leave its proof reusable")
	state, code := issueIdentityOIDC(t, env, start)
	callback := func(code string) *httptest.ResponseRecorder {
		return request(http.MethodGet, callbackURL(provider.ID)+"?"+url.Values{"state": {state}, "code": {code}}.Encode(), "", "")
	}
	restore = fault("update", "iam_identity_link_transactions")
	assert.Equal(t, http.StatusInternalServerError, callback(code).Code)
	restore()
	_, code = issueIdentityOIDC(t, env, start)
	require.Equal(t, http.StatusSeeOther, callback(code).Code)
	bound := cookie + "; " + start.StateCookie
	for _, stage := range []struct{ operation, table string }{{"update", "iam_principals"}, {"query", "iam_identity_links"}, {"create", "iam_identity_links"}, {"update", "iam_identity_link_transactions"}} {
		restore = fault(stage.operation, stage.table)
		failed := request(http.MethodGet, identityContinuationURL(provider.ID, state), "", bound)
		assert.Equal(t, http.StatusInternalServerError, failed.Code, stage.table)
		assert.Empty(t, failed.Header().Get("Set-Cookie"))
		restore()
		var links int64
		require.NoError(t, env.db.Model(&model.IdentityLink{}).Count(&links).Error)
		assert.Zero(t, links, "failed continuation must roll back identity creation")
		var transaction model.IdentityLinkTransaction
		require.NoError(t, env.db.Take(&transaction, "id_hash = ?", identityOperationHash(state)).Error)
		assert.Nil(t, transaction.ConsumedAt)
	}
	require.Equal(t, http.StatusSeeOther, request(http.MethodGet, identityContinuationURL(provider.ID, state), "", bound).Code)
	links, err := env.service.ListPersonalIdentityLinks(t.Context(), cookie)
	require.NoError(t, err)
	require.Len(t, links, 1)
	proof = identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityUnlink, links[0].ID)
	restore = fault("delete", "iam_identity_links")
	assert.ErrorIs(t, env.service.UnlinkPersonalIdentity(t.Context(), cookie, links[0].ID, proof), assert.AnError)
	restore()
	require.NoError(t, env.service.UnlinkPersonalIdentity(t.Context(), cookie, links[0].ID, proof))
}

func TestIdentityLinkContinuationRejectsExpiredUnderlyingReauthentication(t *testing.T) {
	env, authenticator, principalID, cookie := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	env.idp.setClaim("sub", "existing-subject")
	require.NoError(t, env.db.Create(&model.IdentityLink{BindingProvenance: BindingPrincipalVerified, SecurityFingerprint: securityProviderFingerprint(providerRow{ProviderType: provider.ProviderType, Issuer: provider.Issuer, ClientID: provider.ClientID}), ProviderID: provider.ID, TenantID: provider.TenantID, PrincipalID: principalID, ExternalSubject: "existing-subject", CreatedAt: time.Now(), UpdatedAt: time.Now(), LastLoginAt: time.Now()}).Error)
	options, err := authenticator.BeginReauthentication(t.Context(), cookie, authn.ReauthenticationPrincipalClose, "self", "federation", "")
	require.NoError(t, err)
	start, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, options.Proof, identityLinkID(provider.ID, "existing-subject"), "", "https://iam.example"+callbackURL(provider.ID))
	require.NoError(t, err)
	state := verifyIdentityOIDC(t, env, provider, start)
	require.NoError(t, env.db.Model(&authnmodel.ReauthenticationChallenge{}).Where("principal_id = ?", principalID).Update("expires_at", time.Now().Add(-time.Minute)).Error)
	router, api := humatest.New(t)
	registerPersonalIdentityHTTP(api, env.service)
	req := httptest.NewRequest(http.MethodGet, "https://iam.example"+identityContinuationURL(provider.ID, state), nil)
	req.Header.Set("Cookie", cookie+"; "+start.StateCookie)
	result := httptest.NewRecorder()
	router.ServeHTTP(result, req)
	assert.Equal(t, http.StatusUnauthorized, result.Code)
	var row model.IdentityLinkTransaction
	require.NoError(t, env.db.Take(&row, "id_hash = ?", identityOperationHash(state)).Error)
	assert.Nil(t, row.ConsumedAt)
}

func TestIdentityLinkOIDCRejectsUnboundAndStaleClaims(t *testing.T) {
	env, authenticator, principalID, cookie := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	require.NoError(t, env.db.Create(&model.IdentityLink{BindingProvenance: BindingPrincipalVerified, SecurityFingerprint: securityProviderFingerprint(providerRow{ProviderType: provider.ProviderType, Issuer: provider.Issuer, ClientID: provider.ClientID}), ProviderID: provider.ID, TenantID: provider.TenantID, PrincipalID: principalID, ExternalSubject: "expected", CreatedAt: time.Now(), UpdatedAt: time.Now(), LastLoginAt: time.Now()}).Error)
	for _, scenario := range []string{"nonce", "old", "future", "subject", "oversize"} {
		t.Run(scenario, func(t *testing.T) {
			options, err := authenticator.BeginReauthentication(t.Context(), cookie, authn.ReauthenticationPrincipalClose, "self", "federation", "")
			require.NoError(t, err)
			callback := "https://iam.example" + callbackURL(provider.ID)
			start, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, options.Proof, identityLinkID(provider.ID, "expected"), "", callback)
			require.NoError(t, err)
			endpoint, err := url.Parse(start.AuthorizationURL)
			require.NoError(t, err)
			state := endpoint.Query().Get("state")
			env.idp.setClaim("nonce", state)
			env.idp.setClaim("auth_time", time.Now().Unix())
			env.idp.setClaim("sub", "expected")
			switch scenario {
			case "nonce":
				env.idp.setClaim("nonce", "different-browser")
			case "old":
				env.idp.setClaim("auth_time", time.Now().Add(-time.Hour).Unix())
			case "future":
				env.idp.setClaim("auth_time", time.Now().Add(time.Hour).Unix())
			case "subject":
				env.idp.setClaim("sub", "someone-else")
			case "oversize":
				env.idp.setClaim("sub", strings.Repeat("x", 129))
			}
			response, err := env.idp.server.Client().Get(start.AuthorizationURL)
			require.NoError(t, err)
			defer response.Body.Close()
			var issued struct {
				Code string `json:"code"`
			}
			require.NoError(t, json.NewDecoder(response.Body).Decode(&issued))
			assert.Error(t, env.service.VerifyIdentityOperation(t.Context(), provider.ID, state, issued.Code, "", callback))
			_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, cookie+"; "+start.StateCookie)
			assert.Error(t, err)
			var row model.IdentityLinkTransaction
			require.NoError(t, env.db.Where("id_hash = ?", identityOperationHash(state)).Take(&row).Error)
			assert.Nil(t, row.VerifiedAt)
			assert.Nil(t, row.ConsumedAt)
		})
	}
}

func TestIdentityLinkContinuationRechecksSessionProviderMembershipAndRollback(t *testing.T) {
	env, authenticator, principalID, cookie := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	proof := identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityLink, provider.ID.String())
	callback := "https://iam.example" + callbackURL(provider.ID)
	start, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "", callback)
	require.NoError(t, err)
	state := verifyIdentityOIDC(t, env, provider, start)
	bound := cookie + "; " + start.StateCookie
	assert.ErrorIs(t, env.service.VerifyIdentityOperation(t.Context(), provider.ID, state, "code", "", callback), ErrOIDCState)
	other, err := authenticator.CreateSession(t.Context(), principalID, time.Now(), authnext.Assurance{AuthTime: time.Now(), Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, authenticator.SessionCookie(other)+"; "+start.StateCookie)
	assert.ErrorIs(t, err, ErrOIDCState)
	for _, change := range []struct{ table, column, bad, good string }{
		{"iam_identity_providers", "client_id", "changed", provider.ClientID},
		{"iam_identity_providers", "status", "disabled", "active"},
		{"iam_tenants", "status", "disabled", "active"},
		{"iam_tenant_members", "status", "ended", "active"},
	} {
		require.NoError(t, env.db.Table(change.table).Where("1 = 1").Update(change.column, change.bad).Error)
		_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, bound)
		assert.Error(t, err, change.table)
		require.NoError(t, env.db.Table(change.table).Where("1 = 1").Update(change.column, change.good).Error)
	}
	env.auditErr = assert.AnError
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, bound)
	assert.Error(t, err)
	var count int64
	require.NoError(t, env.db.Model(&model.IdentityLink{}).Count(&count).Error)
	assert.Zero(t, count)
	env.auditErr = nil
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, bound)
	require.NoError(t, err)
	links, err := env.service.ListPersonalIdentityLinks(t.Context(), cookie)
	require.NoError(t, err)
	require.Len(t, links, 1)
	unlinkProof := identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityUnlink, links[0].ID)
	env.auditErr = assert.AnError
	assert.Error(t, env.service.UnlinkPersonalIdentity(t.Context(), cookie, links[0].ID, unlinkProof))
	env.auditErr = nil
	require.NoError(t, env.service.UnlinkPersonalIdentity(t.Context(), cookie, links[0].ID, unlinkProof))
}

func TestIdentityLinkHTTPUsesSessionCSRFAndCompletesPersonalLifecycle(t *testing.T) {
	env, authenticator, _, cookie := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	router, api := humatest.New(t)
	registerPersonalIdentityHTTP(api, env.service)
	request := func(method, path, body, origin, authorization, cookies string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "https://iam.example"+path, strings.NewReader(body))
		req.Header.Set("Cookie", cookies)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("Authorization", authorization)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	for _, path := range []string{"/principals/" + "101" + "/identity-links", "/principals/" + "101" + "/identity-link-providers"} {
		assert.Equal(t, http.StatusOK, request(http.MethodGet, path, "", "", "", cookie).Code)
		assert.Equal(t, http.StatusUnauthorized, request(http.MethodGet, path, "", "", "Bearer invalid", cookie).Code)
	}
	providers, err := env.service.ListLinkableProviders(t.Context(), cookie)
	require.NoError(t, err)
	require.Len(t, providers, 1)
	assert.Equal(t, provider.ID, providers[0].ID)
	proof := identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityLink, provider.ID.String())
	body, err := json.Marshal(map[string]any{"provider_id": provider.ID, "proof": proof, "return_url": "https://app.example/security"})
	require.NoError(t, err)
	path := "/principals/" + "101" + "/identity-link-transactions"
	assert.Equal(t, http.StatusForbidden, request(http.MethodPost, path, string(body), "https://evil.example", "", cookie).Code)
	assert.Equal(t, http.StatusUnauthorized, request(http.MethodPost, path, string(body), "https://app.example", "Bearer invalid", cookie).Code)
	started := request(http.MethodPost, path, string(body), "https://app.example", "", cookie)
	require.Equal(t, http.StatusOK, started.Code)
	var envelope struct {
		Data identityOperationLocation `json:"data"`
	}
	require.NoError(t, json.Unmarshal(started.Body.Bytes(), &envelope))
	start := LoginStart{AuthorizationURL: envelope.Data.AuthorizationURL, StateCookie: started.Header().Get("Set-Cookie")}
	state := verifyIdentityOIDC(t, env, provider, start)
	continuation := identityContinuationURL(provider.ID, state)
	assert.Equal(t, http.StatusUnauthorized, request(http.MethodGet, continuation, "", "", "Bearer invalid", cookie).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, request(http.MethodGet, "/identity-providers/invalid/identity-continuation?state=x", "", "", "", cookie).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, request(http.MethodGet, "/identity-providers/9223372036854775808/identity-continuation?state=x", "", "", "", cookie).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, request(http.MethodGet, "/identity-providers/"+provider.ID.String()+"/identity-continuation", "", "", "", cookie).Code)
	assert.Equal(t, http.StatusBadRequest, request(http.MethodGet, continuation, "", "", "", cookie).Code)
	completed := request(http.MethodGet, continuation, "", "", "", cookie+"; "+start.StateCookie)
	require.Equal(t, http.StatusSeeOther, completed.Code)
	links, err := env.service.ListPersonalIdentityLinks(t.Context(), cookie)
	require.NoError(t, err)
	require.Len(t, links, 1)
	proof = identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityUnlink, links[0].ID)
	body, err = json.Marshal(map[string]string{"proof": proof})
	require.NoError(t, err)
	path = "/principals/" + "101" + "/identity-links/" + links[0].ID
	assert.Equal(t, http.StatusForbidden, request(http.MethodDelete, path, string(body), "https://evil.example", "", cookie).Code)
	assert.Equal(t, http.StatusUnauthorized, request(http.MethodDelete, path, string(body), "https://app.example", "Bearer invalid", cookie).Code)
	require.Equal(t, http.StatusOK, request(http.MethodDelete, path, string(body), "https://app.example", "", cookie).Code)
	assert.Equal(t, http.StatusUnauthorized, request(http.MethodDelete, path, string(body), "https://app.example", "", cookie).Code)
}

func TestIdentityLinkRejectsInvalidOperationsAndUnavailableDependencies(t *testing.T) {
	env, authenticator, _, cookie := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	callback := "https://iam.example" + callbackURL(provider.ID)
	for _, id := range []string{"invalid", "invalid:abc", provider.ID.String() + ":%%%"} {
		assert.Error(t, env.service.UnlinkPersonalIdentity(t.Context(), cookie, id, "proof"))
		_, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, "proof", id, "", callback)
		assert.Error(t, err)
	}
	_, err := env.service.ListPersonalIdentityLinks(t.Context(), "")
	assert.Error(t, err)
	_, err = env.service.ListLinkableProviders(t.Context(), "")
	assert.Error(t, err)
	_, err = env.service.StartIdentityOperation(t.Context(), "", provider.ID, "proof", "", "", callback)
	assert.Error(t, err)
	_, err = env.service.StartIdentityOperation(t.Context(), cookie, 999, "proof", "", "", callback)
	assert.Error(t, err)
	_, err = env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, "proof", "", "", "invalid")
	assert.ErrorIs(t, err, ErrInvalidProvider)
	_, err = env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, "proof", "", "https://evil.example", callback)
	assert.Error(t, err)
	_, err = env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, "proof", identityLinkID(provider.ID, "missing"), "", callback)
	assert.Error(t, err)
	assert.Error(t, env.service.UnlinkPersonalIdentity(t.Context(), "", identityLinkID(provider.ID, "missing"), "proof"))
	assert.ErrorIs(t, env.service.VerifyIdentityOperation(t.Context(), provider.ID, "ordinary-state", "", "", callback), ErrOIDCState)
	assert.ErrorIs(t, env.service.VerifyIdentityOperation(t.Context(), provider.ID, "account.missing", "", "", callback), ErrOIDCState)
	proof := identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityLink, provider.ID.String())
	env.auditErr = assert.AnError
	_, err = env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "", callback)
	assert.Error(t, err)
	env.auditErr = nil
	start, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "", callback)
	require.NoError(t, err, "failed audit must roll back proof consumption")
	endpoint, err := url.Parse(start.AuthorizationURL)
	require.NoError(t, err)
	state := endpoint.Query().Get("state")
	assert.ErrorIs(t, env.service.VerifyIdentityOperation(t.Context(), provider.ID, state, "", "", "https://evil.example"), ErrOIDCState)
	assert.Error(t, env.service.VerifyIdentityOperation(t.Context(), provider.ID, state, "invalid-code", "", callback))
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, start.StateCookie)
	assert.Error(t, err)
	require.NoError(t, env.db.Model(&model.Provider{}).Where("id = ?", provider.ID).Update("status", "disabled").Error)
	_, err = env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "", callback)
	assert.ErrorIs(t, err, ErrProviderDisabled)
	require.NoError(t, env.db.Model(&model.IdentityLinkTransaction{}).Where("id_hash = ?", identityOperationHash(state)).Update("expires_at", time.Now().Add(-time.Minute)).Error)
	assert.ErrorIs(t, env.service.VerifyIdentityOperation(t.Context(), provider.ID, state, "", "", callback), ErrOIDCState)
	savedRepo := env.service.repo
	env.service.repo = &Repository{}
	_, err = env.service.ListPersonalIdentityLinks(t.Context(), cookie)
	assert.Error(t, err)
	_, err = env.service.ListLinkableProviders(t.Context(), cookie)
	assert.Error(t, err)
	assert.Error(t, env.service.VerifyIdentityOperation(t.Context(), provider.ID, state, "", "", callback))
	env.service.repo = savedRepo
	env.service.accountProofs = nil
	env.service.accountReauthentication.ConsumeIndependentOn = nil
	_, err = env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "", callback)
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.ErrorIs(t, env.service.UnlinkPersonalIdentity(t.Context(), cookie, identityLinkID(provider.ID, "subject"), proof), ErrUnavailable)
}

func TestIdentityLinkNeverTransfersAnotherPrincipalsIdentity(t *testing.T) {
	env, authenticator, _, cookie := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	proof := identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityLink, provider.ID.String())
	start, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "", "https://iam.example"+callbackURL(provider.ID))
	require.NoError(t, err)
	state := verifyIdentityOIDC(t, env, provider, start)
	var transaction model.IdentityLinkTransaction
	require.NoError(t, env.db.Where("id_hash = ?", identityOperationHash(state)).Take(&transaction).Error)
	require.NoError(t, env.db.Create(&identitymodel.Principal{ID: 202, LoginName: "other", Email: "other@example.test", DisplayName: "Other", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error)
	require.NoError(t, env.db.Create(&model.IdentityLink{ProviderID: provider.ID, TenantID: provider.TenantID, PrincipalID: 202, ExternalSubject: transaction.ExternalSubject, CreatedAt: time.Now(), UpdatedAt: time.Now(), LastLoginAt: time.Now()}).Error)
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, cookie+"; "+start.StateCookie)
	assert.ErrorIs(t, err, ErrIdentityLinkConflict)
	id := identityLinkID(provider.ID, transaction.ExternalSubject)
	proof = identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityUnlink, id)
	assert.ErrorIs(t, env.service.UnlinkPersonalIdentity(t.Context(), cookie, id, proof), ErrPrincipalInactive)
	options, err := authenticator.BeginReauthentication(t.Context(), cookie, authn.ReauthenticationPrincipalClose, "self", "federation", "")
	require.NoError(t, err)
	_, err = env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, options.Proof, id, "", "https://iam.example"+callbackURL(provider.ID))
	assert.ErrorIs(t, err, ErrPrincipalInactive)
	var link model.IdentityLink
	require.NoError(t, env.db.Take(&link).Error)
	assert.Equal(t, guid.ID(202), link.PrincipalID)
	assert.Equal(t, http.StatusConflict, federationError(ErrIdentityLinkConflict).(interface{ GetStatus() int }).GetStatus())
	assert.Equal(t, http.StatusConflict, federationError(ErrLastLoginMethod).(interface{ GetStatus() int }).GetStatus())
	assert.Equal(t, http.StatusServiceUnavailable, federationError(authnext.ErrUnavailable).(interface{ GetStatus() int }).GetStatus())
}

func TestIdentityLinkReauthenticationRejectsLinkRemovedDuringRedirect(t *testing.T) {
	env, authenticator, principalID, cookie := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	env.idp.setClaim("sub", "owner-subject")
	require.NoError(t, env.db.Create(&model.IdentityLink{BindingProvenance: BindingPrincipalVerified, SecurityFingerprint: securityProviderFingerprint(providerRow{ProviderType: provider.ProviderType, Issuer: provider.Issuer, ClientID: provider.ClientID}), ProviderID: provider.ID, TenantID: provider.TenantID, PrincipalID: principalID, ExternalSubject: "owner-subject", CreatedAt: time.Now(), UpdatedAt: time.Now(), LastLoginAt: time.Now()}).Error)
	id := identityLinkID(provider.ID, "owner-subject")
	options, err := authenticator.BeginReauthentication(t.Context(), cookie, authn.ReauthenticationPrincipalClose, "self", "federation", "")
	require.NoError(t, err)
	start, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, options.Proof, id, "", "https://iam.example"+callbackURL(provider.ID))
	require.NoError(t, err)
	state := verifyIdentityOIDC(t, env, provider, start)
	proof := identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityUnlink, id)
	local := env.service.accountReauthentication.LocalMethodsOn
	env.service.accountReauthentication.LocalMethodsOn = func(context.Context, *gorm.DB, guid.ID) (int, error) { return 0, assert.AnError }
	assert.ErrorIs(t, env.service.UnlinkPersonalIdentity(t.Context(), cookie, id, proof), assert.AnError)
	env.service.accountReauthentication.LocalMethodsOn = local
	require.NoError(t, env.service.UnlinkPersonalIdentity(t.Context(), cookie, id, proof))
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, cookie+"; "+start.StateCookie)
	assert.ErrorIs(t, err, ErrPrincipalInactive)
	env.service.accountReauthentication.VerifyOn = nil
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, cookie+"; "+start.StateCookie)
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestIdentityLinkUsesExistingPrincipalWithoutJITOrSessionSwitch(t *testing.T) {
	env, authenticator, principalID, cookie := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	proof := identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityLink, provider.ID.String())
	start, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "https://app.example/security", "https://iam.example"+callbackURL(provider.ID))
	require.NoError(t, err)
	state := verifyIdentityOIDC(t, env, provider, start)
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, cookie)
	assert.ErrorIs(t, err, ErrOIDCState)
	boundCookie := cookie + "; " + start.StateCookie
	location, err := env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, boundCookie)
	require.NoError(t, err)
	assert.Equal(t, "https://app.example/security", location)
	links, err := env.service.ListPersonalIdentityLinks(t.Context(), cookie)
	require.NoError(t, err)
	require.Len(t, links, 1)
	var stored model.IdentityLink
	require.NoError(t, env.db.Take(&stored).Error)
	assert.Equal(t, principalID, stored.PrincipalID)
	for table, want := range map[string]int64{"iam_principals": 1, "iam_sessions": 1, "iam_role_members": 0} {
		var count int64
		require.NoError(t, env.db.Table(table).Count(&count).Error)
		assert.Equal(t, want, count, table)
	}
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, boundCookie)
	assert.ErrorIs(t, err, ErrOIDCState)
	_, err = env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "", "https://iam.example"+callbackURL(provider.ID))
	assert.Error(t, err)
}

func TestFederationOnlyPrincipalCanVerifyOperationProof(t *testing.T) {
	env, authenticator, principalID, cookie := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	proof := identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityLink, provider.ID.String())
	start, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "", "https://iam.example"+callbackURL(provider.ID))
	require.NoError(t, err)
	state := verifyIdentityOIDC(t, env, provider, start)
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, cookie+"; "+start.StateCookie)
	require.NoError(t, err)
	links, err := env.service.ListPersonalIdentityLinks(t.Context(), cookie)
	require.NoError(t, err)
	require.NoError(t, env.db.Delete(&authnmodel.Credential{}, "principal_id = ?", principalID).Error)
	options, err := authenticator.BeginReauthentication(t.Context(), cookie, authn.ReauthenticationPersonalTokenCreate, "token-request", "federation", "")
	require.NoError(t, err)
	start, err = env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, options.Proof, links[0].ID, "", "https://iam.example"+callbackURL(provider.ID))
	require.NoError(t, err)
	state = verifyIdentityOIDC(t, env, provider, start)
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, cookie+"; "+start.StateCookie)
	require.NoError(t, err)
	require.NoError(t, env.db.Transaction(func(tx *gorm.DB) error {
		return authenticator.ConsumeReauthenticationOn(t.Context(), tx, principalID, cookie, options.Proof, authn.ReauthenticationPersonalTokenCreate, "token-request")
	}))
}

func TestConcurrentIdentityUnlinkRetainsLastUsableLogin(t *testing.T) {
	env, authenticator, principalID, cookie := newIdentityLinkEnvironment(t)
	first := env.createProvider(t, testID("tenant-a"), env.providerInput("First", env.idp.issuer))
	second := env.createProvider(t, testID("tenant-a"), env.providerInput("Second", env.idp.issuer+"/second"))
	ids := []string{}
	proofs := []string{}
	for _, provider := range []Provider{first, second} {
		id := identityLinkID(provider.ID, "subject")
		require.NoError(t, env.db.Create(&model.IdentityLink{BindingProvenance: BindingPrincipalVerified, SecurityFingerprint: securityProviderFingerprint(providerRow{ProviderType: provider.ProviderType, Issuer: provider.Issuer, ClientID: provider.ClientID}), ProviderID: provider.ID, TenantID: provider.TenantID, PrincipalID: principalID, ExternalSubject: "subject", CreatedAt: time.Now(), UpdatedAt: time.Now(), LastLoginAt: time.Now()}).Error)
		ids = append(ids, id)
		proofs = append(proofs, identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityUnlink, id))
	}
	// Remove the password, then re-verify new federation proofs at the trusted
	// protocol boundary; the complete OIDC path is exercised above.
	require.NoError(t, env.db.Delete(&authnmodel.Credential{}, "principal_id = ?", principalID).Error)
	for i, id := range ids {
		options, err := authenticator.BeginReauthentication(t.Context(), cookie, authn.ReauthenticationIdentityUnlink, id, "federation", "")
		require.NoError(t, err)
		binding, err := env.service.accountReauthentication.Inspect(t.Context(), cookie, options.Proof)
		require.NoError(t, err)
		providerID, _, err := parseIdentityLinkID(id)
		require.NoError(t, err)
		provider, err := env.service.repo.getProviderByID(t.Context(), providerID)
		require.NoError(t, err)
		require.NoError(t, env.db.Transaction(func(tx *gorm.DB) error {
			return env.service.accountReauthentication.VerifyOn(t.Context(), tx, principalID, cookie, binding.ChallengeHash, id, securityProviderFingerprint(provider))
		}))
		proofs[i] = options.Proof
	}
	db, err := env.db.DB()
	require.NoError(t, err)
	db.SetMaxOpenConns(4)
	var wait sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for i := range ids {
		wait.Go(func() { <-start; results <- env.service.UnlinkPersonalIdentity(t.Context(), cookie, ids[i], proofs[i]) })
	}
	close(start)
	wait.Wait()
	close(results)
	wins, blocked := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else {
			assert.ErrorIs(t, err, ErrLastLoginMethod)
			blocked++
		}
	}
	assert.Equal(t, 1, wins)
	assert.Equal(t, 1, blocked)
	var remaining int64
	require.NoError(t, env.db.Model(&model.IdentityLink{}).Where("principal_id = ?", principalID).Count(&remaining).Error)
	assert.Equal(t, int64(1), remaining)
}

func TestSAMLIdentityLinkUsesFreshSignedResponseAndSameSiteContinuation(t *testing.T) {
	env, authenticator, principalID, cookie := newIdentityLinkEnvironment(t)
	upstream := newFederationEnvironment(t)
	startSAML(t, upstream, SAMLConfig{Enabled: true})
	server := newSAMLServer(t, upstream)
	entityID := "https://iam.example/account-sp"
	provider := env.createProvider(t, testID("tenant-a"), ProviderInput{Name: "SAML", ProviderType: ProviderSAML, Issuer: server.URL + samlMetadataPath(testID("tenant-a")), ClientID: entityID, Status: ProviderActive})
	callback := "https://iam.example" + callbackURL(provider.ID)
	sp := newSAMLSPServer(t)
	_, err := upstream.service.CreateSAMLServiceProvider(t.Context(), testID("tenant-a"), SAMLServiceProviderInput{Name: "account-sp", EntityID: entityID, MetadataXML: spLoginMetadata(t, sp, entityID, callback), Status: samlSPStatusActive})
	require.NoError(t, err)
	proof := identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityLink, provider.ID.String())
	start, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "https://app.example/security", callback)
	require.NoError(t, err)
	location, err := url.Parse(start.AuthorizationURL)
	require.NoError(t, err)
	compressed, err := base64.StdEncoding.DecodeString(location.Query().Get("SAMLRequest"))
	require.NoError(t, err)
	reader := flate.NewReader(bytes.NewReader(compressed))
	requestXML, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Contains(t, string(requestXML), `ForceAuthn="true"`)
	_, idpCookie := samlSessionCookie(t, upstream)
	response := samlBrowserRequest(t, server, http.MethodGet, start.AuthorizationURL, idpCookie)
	require.Equal(t, http.StatusOK, response.StatusCode)
	assertion := extractSAMLResponse(t, samlReadBody(t, response))
	router, api := humatest.New(t)
	registerBrowserHTTP(api, env.service)
	registerPersonalIdentityHTTP(api, env.service)
	form := url.Values{"SAMLResponse": {assertion}, "RelayState": {location.Query().Get("RelayState")}}
	request := httptest.NewRequest(http.MethodPost, callback, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	result := httptest.NewRecorder()
	router.ServeHTTP(result, request)
	require.Equal(t, http.StatusSeeOther, result.Code)
	assert.Empty(t, result.Header().Get("Set-Cookie"), "cross-site POST must not create/switch a session")
	resume := httptest.NewRequest(http.MethodGet, "https://iam.example"+result.Header().Get("Location"), nil)
	resume.Header.Set("Cookie", cookie+"; "+start.StateCookie)
	completed := httptest.NewRecorder()
	router.ServeHTTP(completed, resume)
	require.Equal(t, http.StatusSeeOther, completed.Code)
	assert.Equal(t, "https://app.example/security", completed.Header().Get("Location"))
	var link model.IdentityLink
	require.NoError(t, env.db.Take(&link).Error)
	assert.Equal(t, principalID, link.PrincipalID)
}

// identityPrincipalStateForFederation resolves identity's principal-state
// capability from identity itself. authn reads and locks identity's principal
// table through it, so a harness that builds authn by hand has to push the real
// one rather than a stand-in.
func identityPrincipalStateForFederation(t *testing.T, prefix string) identity.PrincipalStateStore {
	t.Helper()
	runtime := appenv.NewRuntime(appenv.NewIdentity("federation-test", "1"), appenv.WithTablePrefix(prefix))
	services := module.NewServices()
	require.NoError(t, identity.NewModule(runtime, identity.Config{}).ProvideServices(services))
	require.NoError(t, services.Seal())
	store, err := module.Resolve[identity.PrincipalStateStore](services)
	require.NoError(t, err)
	return store
}
