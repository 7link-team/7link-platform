package authn

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func personalTokenService(t *testing.T) (*Service, *authnRepositoryHarness, string, PersonalTokenRequest) {
	t.Helper()
	s, h, cookie := authenticatedLocalService(t)
	s.personalRegistry = authz.DefaultRegistry()
	s.memberTenant = boundMemberTenantResolver(t, h.runtime)
	claims, err := s.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	require.NoError(t, h.db.Exec("INSERT INTO iam_tenants (id,status) VALUES (71,'active'),(72,'active')").Error)
	require.NoError(t, h.db.Exec("INSERT INTO iam_tenant_members (tenant_id,principal_id,status) VALUES (71,?,'active'),(72,?,'active')", claims.PrincipalID, claims.PrincipalID).Error)
	s.personalPermissions = func(_ context.Context, _ guid.ID, permissions []string, _ guid.ID) (map[string]bool, error) {
		result := map[string]bool{}
		for _, p := range permissions {
			result[p] = true
		}
		return result, nil
	}
	return s, h, cookie, PersonalTokenRequest{Name: "Personal 😀 10%", TenantID: 71, Permissions: []string{"store_view"}, ExpiresAt: repositoryTestNow.Add(time.Hour)}
}

type personalTokenRouteChecker struct{ allowed bool }

func (c *personalTokenRouteChecker) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return c.allowed, nil
}
func (c *personalTokenRouteChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return c.allowed, nil
}
func (c *personalTokenRouteChecker) CheckEntity(context.Context, guid.ID, guid.ID, string, guid.ID) (bool, error) {
	return c.allowed, nil
}

func TestPersonalTokenRealRegistrarPreservesAllRouteBoundaries(t *testing.T) {
	s, h, cookie, in := personalTokenService(t)
	created, err := s.CreatePersonalToken(t.Context(), cookie, verifiedPersonalTokenProof(t, s, cookie, in), in)
	require.NoError(t, err)
	_, api := humatest.New(t)
	checker := &personalTokenRouteChecker{allowed: true}
	r := authz.NewRegistrar(s.personalRegistry, s, checker, boundMembershipChecker(t, h.runtime))
	type output struct{ Body string }
	handler := func(context.Context, *struct{}) (*output, error) { return &output{Body: "ok"}, nil }
	op := func(path string) huma.Operation {
		return huma.Operation{OperationID: strings.TrimPrefix(path, "/"), Method: http.MethodGet, Path: path}
	}
	guard := authz.Guard{Resource: "store", Verb: "view"}
	authz.Register(r, api, op("/guarded"), guard, handler)
	authz.Register(r, api, op("/write-scope"), authz.Guard{Resource: "store", Verb: "create"}, handler)
	authz.RegisterAuthenticated(r, api, op("/authenticated"), handler)
	authz.RegisterTenantMember(r, api, op("/member"), handler)
	for _, action := range s.personalRegistry.All() {
		if action.Scope == "platform" {
			authz.RegisterPlatform(r, api, op("/platform"), authz.Guard{Resource: action.Resource, Verb: action.Verb}, handler)
			break
		}
	}
	authz.RegisterEntity(r, api, op("/entity"), guard, handler)
	authz.RegisterEntityAdapter(r, api, op("/adapter"), guard, func(ctx huma.Context) { ctx.SetStatus(http.StatusOK) })
	for _, test := range []struct {
		path, tenant, entity string
		status               int
	}{
		{"/guarded", "71", "", 200}, {"/guarded", "72", "", 401}, {"/guarded", "", "", 401},
		{"/write-scope", "71", "", 401}, {"/authenticated", "71", "", 401}, {"/member", "71", "", 401}, {"/platform", "71", "", 401},
		{"/entity", "71", "5", 200}, {"/entity", "72", "5", 401}, {"/entity", "71", "", 403},
		{"/adapter?tenant_id=71&entity_id=5", "72", "", 200}, {"/adapter?tenant_id=72&entity_id=5", "71", "", 401},
	} {
		t.Run(test.path+test.tenant+test.entity, func(t *testing.T) {
			resp := api.Get(test.path, "Authorization: Bearer "+created.Token, "X-Tenant-Id: "+test.tenant, "X-Entity-Id: "+test.entity)
			assert.Equal(t, test.status, resp.Code, resp.Body.String())
		})
	}
	checker.allowed = false
	for _, path := range []string{"/guarded", "/entity", "/adapter?tenant_id=71&entity_id=5"} {
		resp := api.Get(path, "Authorization: Bearer "+created.Token, "X-Tenant-Id: 71", "X-Entity-Id: 5")
		assert.Equal(t, http.StatusForbidden, resp.Code, resp.Body.String())
	}
	checker.allowed = true
	require.NoError(t, h.db.Exec("UPDATE iam_tenant_members SET status = 'disabled' WHERE tenant_id = 71").Error)
	resp := api.Get("/guarded", "Authorization: Bearer "+created.Token, "X-Tenant-Id: 71")
	assert.Equal(t, http.StatusForbidden, resp.Code, resp.Body.String())
}

func verifiedPersonalTokenProof(t *testing.T, s *Service, cookie string, in PersonalTokenRequest) string {
	t.Helper()
	prepared, err := s.PreparePersonalToken(t.Context(), cookie, in)
	require.NoError(t, err)
	challenge, err := s.BeginReauthentication(t.Context(), cookie, ReauthenticationPersonalTokenCreate, prepared.Target, "password", "")
	require.NoError(t, err)
	require.NoError(t, s.VerifyReauthentication(t.Context(), cookie, challenge.Proof, "correct horse battery staple", "", "", nil))
	return challenge.Proof
}

func TestPersonalTokenProofBindsEveryGrantFieldAndAuditRollback(t *testing.T) {
	s, h, cookie, in := personalTokenService(t)
	proof := verifiedPersonalTokenProof(t, s, cookie, in)
	for _, change := range []struct {
		name   string
		mutate func(*PersonalTokenRequest)
	}{
		{"name", func(v *PersonalTokenRequest) { v.Name = "different" }},
		{"tenant", func(v *PersonalTokenRequest) { v.TenantID = 72 }},
		{"permission", func(v *PersonalTokenRequest) { v.Permissions = []string{"store_create"} }},
		{"expiry", func(v *PersonalTokenRequest) { v.ExpiresAt = v.ExpiresAt.Add(time.Second) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			altered := in
			change.mutate(&altered)
			_, err := s.CreatePersonalToken(t.Context(), cookie, proof, altered)
			require.Error(t, err)
		})
	}
	h.auditErr = assert.AnError
	_, err := s.CreatePersonalToken(t.Context(), cookie, proof, in)
	require.Error(t, err)
	var count int64
	require.NoError(t, h.db.Model(&model.PersonalToken{}).Count(&count).Error)
	assert.Zero(t, count)
	h.auditErr = nil
	created, err := s.CreatePersonalToken(t.Context(), cookie, proof, in)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(created.Token, "pat_"))
	_, err = s.CreatePersonalToken(t.Context(), cookie, proof, in)
	require.Error(t, err)
	rows, total, err := s.ListPersonalTokens(t.Context(), cookie, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), created.Token)
	assert.NotContains(t, string(encoded), tokenHash(created.Token))
	var stored model.PersonalToken
	require.NoError(t, h.db.First(&stored).Error)
	assert.Equal(t, tokenHash(created.Token), stored.TokenHash)
}

func TestPersonalTokenAuthenticationRequiresRouteTenantPermissionLiveCredentialAndRevocation(t *testing.T) {
	s, h, cookie, in := personalTokenService(t)
	created, err := s.CreatePersonalToken(t.Context(), cookie, verifiedPersonalTokenProof(t, s, cookie, in), in)
	require.NoError(t, err)
	for _, test := range []struct {
		name       string
		tenant     guid.ID
		permission string
		valid      bool
	}{
		{"allowed", 71, "store_view", true}, {"other tenant", 72, "store_view", false}, {"other permission", 71, "store_create", false}, {"missing tenant", 0, "store_view", false}, {"missing permission", 71, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := authnext.WithPersonalTokenScope(t.Context(), test.tenant, test.permission)
			_, err := s.Authenticate(ctx, "Bearer "+created.Token, "")
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	_, err = s.Authenticate(t.Context(), "Bearer "+created.Token, cookie)
	require.Error(t, err, "a browser cookie must not bypass unsupported bearer scope")
	ctx := authnext.WithPersonalTokenScope(t.Context(), 71, "store_view")
	s.now = func() time.Time { return in.ExpiresAt }
	_, err = s.Authenticate(ctx, "Bearer "+created.Token, "")
	require.Error(t, err)
	s.now = func() time.Time { return repositoryTestNow }
	require.NoError(t, h.db.Model(&model.Credential{}).Where("principal_id = ?", createdPrincipal(t, s, cookie)).Update("credential_version", 2).Error)
	_, err = s.Authenticate(ctx, "Bearer "+created.Token, "")
	require.Error(t, err)
	require.NoError(t, h.db.Model(&model.Credential{}).Where("credential_version = ?", 2).Update("credential_version", 1).Error)
	h.auditErr = assert.AnError
	require.Error(t, s.RevokePersonalToken(t.Context(), cookie, created.ID))
	h.auditErr = nil
	_, err = s.Authenticate(ctx, "Bearer "+created.Token, "")
	require.NoError(t, err)
	require.NoError(t, s.RevokePersonalToken(t.Context(), cookie, created.ID))
	require.NoError(t, s.RevokePersonalToken(t.Context(), cookie, created.ID))
	_, err = s.Authenticate(ctx, "Bearer "+created.Token, "")
	require.Error(t, err)
}

func createdPrincipal(t *testing.T, s *Service, cookie string) guid.ID {
	t.Helper()
	claims, err := s.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	return claims.PrincipalID
}

func TestPersonalTokenValidationAndCanonicalPreparation(t *testing.T) {
	s, _, cookie, in := personalTokenService(t)
	for _, test := range []struct {
		name   string
		mutate func(*PersonalTokenRequest)
	}{
		{"blank", func(v *PersonalTokenRequest) { v.Name = "  " }},
		{"long", func(v *PersonalTokenRequest) { v.Name = strings.Repeat("界", 129) }},
		{"nul", func(v *PersonalTokenRequest) { v.Name = "x\x00y" }},
		{"expired", func(v *PersonalTokenRequest) { v.ExpiresAt = repositoryTestNow }},
		{"year", func(v *PersonalTokenRequest) { v.ExpiresAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"no tenant", func(v *PersonalTokenRequest) { v.TenantID = 0 }},
		{"unknown tenant", func(v *PersonalTokenRequest) { v.TenantID = 99 }},
		{"no scopes", func(v *PersonalTokenRequest) { v.Permissions = nil }},
		{"wildcard", func(v *PersonalTokenRequest) { v.Permissions = []string{"*"} }},
		{"platform", func(v *PersonalTokenRequest) {
			for _, a := range s.personalRegistry.All() {
				if a.Scope == "platform" {
					v.Permissions = []string{a.Code}
					return
				}
			}
			t.Fatal("platform permission fixture absent")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := in
			test.mutate(&v)
			_, err := s.PreparePersonalToken(t.Context(), cookie, v)
			require.Error(t, err)
		})
	}
	in.Name = " " + strings.Repeat("界", 128) + " "
	in.Permissions = []string{"store_view", "store_view"}
	in.ExpiresAt = time.Date(9999, 12, 31, 23, 59, 59, 999000000, time.UTC)
	prepared, err := s.PreparePersonalToken(t.Context(), cookie, in)
	require.NoError(t, err)
	assert.Len(t, prepared.Request.Permissions, 1)
	assert.Equal(t, strings.TrimSpace(in.Name), prepared.Request.Name)
	created, err := s.CreatePersonalToken(t.Context(), cookie, verifiedPersonalTokenProof(t, s, cookie, in), in)
	require.NoError(t, err)
	assert.Equal(t, in.ExpiresAt, created.ExpiresAt)
}

func TestPersonalTokenHTTPBrowserOnlyCSRFAndSecretProjection(t *testing.T) {
	s, _, cookie, in := personalTokenService(t)
	_, api := humatest.New(t)
	registerPersonalTokenHTTP(api, s)
	list := api.Get("/authn/personal-tokens", "Cookie: "+cookie)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	proof := verifiedPersonalTokenProof(t, s, cookie, in)
	created, err := s.CreatePersonalToken(t.Context(), cookie, proof, in)
	require.NoError(t, err)
	for path, item := range api.OpenAPI().Paths {
		for _, operation := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete, item.Head, item.Options, item.Trace} {
			if operation == nil {
				continue
			}
			t.Run(operation.Method+path, func(t *testing.T) {
				assert.Equal(t, []map[string][]string{{authz.SessionScheme: {}}}, operation.Security)
				target := strings.ReplaceAll(path, "{id}", created.ID.String())
				resp := api.Do(operation.Method, target, "Authorization: Bearer "+created.Token, "Cookie: "+cookie)
				assert.Equal(t, http.StatusUnauthorized, resp.Code, resp.Body.String())
			})
		}
	}
	response := api.Delete("/authn/personal-tokens/"+created.ID.String(), "Cookie: "+cookie, "Origin: https://attacker.example")
	assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	rows, total, err := s.ListPersonalTokens(t.Context(), cookie, 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	assert.Nil(t, rows[0].RevokedAt)
}

func TestPersonalTokenCatalogUsesCurrentPermissionsAndLimitsToTenant(t *testing.T) {
	s, _, cookie, _ := personalTokenService(t)
	s.personalPermissions = func(_ context.Context, tenant guid.ID, _ []string, principal guid.ID) (map[string]bool, error) {
		assert.EqualValues(t, 71, tenant)
		assert.NotZero(t, principal)
		return map[string]bool{"store_view": true}, nil
	}
	actions, err := s.PersonalTokenPermissions(t.Context(), cookie, 71)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "store_view", actions[0].Code)
	_, err = s.PersonalTokenPermissions(t.Context(), cookie, 99)
	require.Error(t, err)
	_, err = s.PersonalTokenPermissions(t.Context(), cookie, 0)
	require.Error(t, err)
	_, err = s.PersonalTokenPermissions(t.Context(), "", 71)
	require.Error(t, err)
	s.personalPermissions = func(context.Context, guid.ID, []string, guid.ID) (map[string]bool, error) { return nil, assert.AnError }
	_, err = s.PersonalTokenPermissions(t.Context(), cookie, 71)
	require.ErrorIs(t, err, authnext.ErrUnavailable)
	s.personalRegistry = nil
	_, err = s.PersonalTokenPermissions(t.Context(), cookie, 71)
	require.ErrorIs(t, err, authnext.ErrUnavailable)
}

func TestPersonalTokenTenantRevocationRollsBackAndDoesNotAffectOtherTenant(t *testing.T) {
	s, h, cookie, in := personalTokenService(t)
	first, err := s.CreatePersonalToken(t.Context(), cookie, verifiedPersonalTokenProof(t, s, cookie, in), in)
	require.NoError(t, err)
	in.TenantID = 72
	second, err := s.CreatePersonalToken(t.Context(), cookie, verifiedPersonalTokenProof(t, s, cookie, in), in)
	require.NoError(t, err)
	principal := createdPrincipal(t, s, cookie)
	require.Error(t, s.revokeTenantPersonalTokensOn(t.Context(), nil, 71, principal))
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := s.revokeTenantPersonalTokensOn(t.Context(), tx, 71, principal); err != nil {
			return err
		}
		return assert.AnError
	})
	require.ErrorIs(t, err, assert.AnError)
	ctx := authnext.WithPersonalTokenScope(t.Context(), 71, "store_view")
	_, err = s.Authenticate(ctx, "Bearer "+first.Token, "")
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return s.revokeTenantPersonalTokensOn(t.Context(), tx, 71, principal) }))
	_, err = s.Authenticate(ctx, "Bearer "+first.Token, "")
	require.Error(t, err)
	_, err = s.Authenticate(authnext.WithPersonalTokenScope(t.Context(), 72, "store_view"), "Bearer "+second.Token, "")
	require.NoError(t, err)
	_, err = s.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
}

func TestPersonalTokenHTTPCompleteFlowAndFailures(t *testing.T) {
	s, h, cookie, in := personalTokenService(t)
	_, api := humatest.New(t)
	registerPersonalTokenHTTP(api, s)
	prepared := api.Post("/authn/personal-token-preparations", "Cookie: "+cookie, "Origin: https://app.example.test", in)
	require.Equal(t, http.StatusOK, prepared.Code, prepared.Body.String())
	proof := verifiedPersonalTokenProof(t, s, cookie, in)
	body := struct {
		PersonalTokenRequest
		Proof string `json:"proof"`
	}{PersonalTokenRequest: in, Proof: proof}
	response := api.Post("/authn/personal-tokens", "Cookie: "+cookie, "Origin: https://app.example.test", body)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var created respx.Envelope[PersonalTokenCreated]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
	require.NotEmpty(t, created.Data.Token)
	response = api.Get("/authn/personal-tokens?offset=1&limit=1", "Cookie: "+cookie)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var listed respx.Envelope[[]PersonalTokenView]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &listed))
	assert.Empty(t, listed.Data)
	require.NotNil(t, listed.Meta.Page)
	assert.EqualValues(t, 1, listed.Meta.Page.Total)
	response = api.Get("/authn/personal-token-permissions?tenant_id=71", "Cookie: "+cookie)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	s.personalPermissions = func(context.Context, guid.ID, []string, guid.ID) (map[string]bool, error) { return nil, assert.AnError }
	response = api.Get("/authn/personal-token-permissions?tenant_id=71", "Cookie: "+cookie)
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	response = api.Post("/authn/personal-token-preparations", "Cookie: "+cookie, "Origin: https://app.example.test", in)
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	in.Name = "   "
	response = api.Post("/authn/personal-token-preparations", "Cookie: "+cookie, "Origin: https://app.example.test", in)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	body.Proof = "unverified"
	response = api.Post("/authn/personal-tokens", "Cookie: "+cookie, "Origin: https://app.example.test", body)
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	h.auditErr = assert.AnError
	response = api.Delete("/authn/personal-tokens/"+created.Data.ID.String(), "Cookie: "+cookie, "Origin: https://app.example.test")
	require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	h.auditErr = nil
	response = api.Delete("/authn/personal-tokens/"+created.Data.ID.String(), "Cookie: "+cookie, "Origin: https://app.example.test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, s.RevokePersonalToken(t.Context(), cookie, 999))
	for _, bounds := range [][2]int{{-1, 1}, {0, 0}, {0, 101}} {
		_, _, err := s.ListPersonalTokens(t.Context(), cookie, bounds[0], bounds[1])
		require.Error(t, err)
	}
	// Only the credential store's test-owned table is removed to exercise
	// request-time persistence failure without invalidating the browser session.
	require.NoError(t, h.db.Migrator().DropTable(&model.PersonalToken{}))
	response = api.Get("/authn/personal-tokens", "Cookie: "+cookie)
	require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	_, err := s.Authenticate(authnext.WithPersonalTokenScope(t.Context(), 71, "store_view"), "Bearer "+created.Data.Token, "")
	require.ErrorIs(t, err, authnext.ErrUnavailable)
}

func TestPersonalTokenDependenciesFailClosedWithoutSpendingProof(t *testing.T) {
	s, h, cookie, in := personalTokenService(t)
	proof := verifiedPersonalTokenProof(t, s, cookie, in)
	for _, scenario := range []string{"membership unavailable", "permission denied", "permission service missing", "registry missing"} {
		t.Run(scenario, func(t *testing.T) {
			member, permissions, registry := s.memberTenant, s.personalPermissions, s.personalRegistry
			switch scenario {
			case "membership unavailable":
				s.memberTenant = func(context.Context, guid.ID, guid.ID) (guid.ID, error) { return 0, assert.AnError }
				_, err := s.PersonalTokenPermissions(t.Context(), cookie, in.TenantID)
				assert.ErrorIs(t, err, authnext.ErrUnavailable)
			case "permission denied":
				s.personalPermissions = func(context.Context, guid.ID, []string, guid.ID) (map[string]bool, error) {
					return map[string]bool{}, nil
				}
			case "permission service missing":
				s.personalPermissions = nil
			case "registry missing":
				s.personalRegistry = nil
			}
			_, err := s.CreatePersonalToken(t.Context(), cookie, proof, in)
			assert.Error(t, err)
			s.memberTenant, s.personalPermissions, s.personalRegistry = member, permissions, registry
			var stored model.ReauthenticationChallenge
			require.NoError(t, h.db.Take(&stored, "id_hash = ?", tokenHash(proof)).Error)
			assert.Nil(t, stored.ConsumedAt)
		})
	}
	_, err := s.PreparePersonalToken(t.Context(), "", in)
	assert.Error(t, err)
	_, err = s.CreatePersonalToken(t.Context(), "", proof, in)
	assert.Error(t, err)
	_, _, err = s.ListPersonalTokens(t.Context(), "", 0, 20)
	assert.Error(t, err)
	assert.Error(t, s.RevokePersonalToken(t.Context(), "", 1))
	_, err = s.CreatePersonalToken(t.Context(), cookie, proof, in)
	require.NoError(t, err)
}

func TestPersonalTokenCreationStorageAndIDFailuresRollBackProof(t *testing.T) {
	s, h, cookie, in := personalTokenService(t)
	proof := verifiedPersonalTokenProof(t, s, cookie, in)
	proofUpdated := false
	require.NoError(t, h.db.Callback().Update().After("gorm:update").Register("personal_token_proof_updated", func(tx *gorm.DB) {
		if tx.Statement.Table == "iam_reauth_challenges" && tx.Error == nil {
			proofUpdated = true
		}
	}))
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("personal_token_principal_failure", func(tx *gorm.DB) {
		if proofUpdated && tx.Statement.TableExpr != nil && strings.Contains(tx.Statement.TableExpr.SQL, "iam_principals") {
			tx.AddError(assert.AnError)
		}
	}))
	_, err := s.CreatePersonalToken(t.Context(), cookie, proof, in)
	assert.True(t, proofUpdated)
	assert.ErrorIs(t, err, assert.AnError)
	require.NoError(t, h.db.Callback().Update().Remove("personal_token_proof_updated"))
	require.NoError(t, h.db.Callback().Query().Remove("personal_token_principal_failure"))
	ids := s.repo.ids
	s.repo.ids = &repositoryIDFixture{err: assert.AnError}
	_, err = s.CreatePersonalToken(t.Context(), cookie, proof, in)
	assert.ErrorIs(t, err, assert.AnError)
	s.repo.ids = ids
	require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("personal_token_create_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "iam_personal_tokens" {
			tx.AddError(assert.AnError)
		}
	}))
	_, err = s.CreatePersonalToken(t.Context(), cookie, proof, in)
	assert.ErrorIs(t, err, assert.AnError)
	require.NoError(t, h.db.Callback().Create().Remove("personal_token_create_failure"))
	var count int64
	require.NoError(t, h.db.Model(&model.PersonalToken{}).Count(&count).Error)
	assert.Zero(t, count)
	var challenge model.ReauthenticationChallenge
	require.NoError(t, h.db.Take(&challenge, "id_hash = ?", tokenHash(proof)).Error)
	assert.Nil(t, challenge.ConsumedAt)
	_, err = s.CreatePersonalToken(t.Context(), cookie, proof, in)
	require.NoError(t, err)
}

func TestPersonalTokenRevocationStoreFailurePreservesTokenAndOwner(t *testing.T) {
	s, h, cookie, in := personalTokenService(t)
	created, err := s.CreatePersonalToken(t.Context(), cookie, verifiedPersonalTokenProof(t, s, cookie, in), in)
	require.NoError(t, err)
	ctx := authnext.WithPersonalTokenScope(t.Context(), in.TenantID, "store_view")
	h.seedPrincipal(t, 9001, "another-owner")
	other, err := s.CreateSession(t.Context(), 9001, s.now(), assurancePassword(s.now()))
	require.NoError(t, err)
	otherCookie := s.SessionCookie(other)
	require.NoError(t, s.RevokePersonalToken(t.Context(), otherCookie, created.ID), "foreign token IDs must behave like absent IDs")
	rows, total, err := s.ListPersonalTokens(t.Context(), otherCookie, 0, 20)
	require.NoError(t, err)
	assert.Empty(t, rows)
	assert.Zero(t, total)
	_, err = s.Authenticate(ctx, "Bearer "+created.Token, "")
	require.NoError(t, err)
	for _, table := range []string{"iam_principals", "iam_personal_tokens"} {
		require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("personal_token_revoke_failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(assert.AnError)
			}
		}))
		assert.ErrorIs(t, s.RevokePersonalToken(t.Context(), cookie, created.ID), assert.AnError)
		require.NoError(t, h.db.Callback().Update().Remove("personal_token_revoke_failure"))
		_, err := s.Authenticate(ctx, "Bearer "+created.Token, "")
		require.NoError(t, err)
	}
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("personal_token_read_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "iam_personal_tokens" {
			tx.AddError(assert.AnError)
		}
	}))
	assert.ErrorIs(t, s.RevokePersonalToken(t.Context(), cookie, created.ID), assert.AnError)
	require.NoError(t, h.db.Callback().Query().Remove("personal_token_read_failure"))
	require.NoError(t, s.RevokePersonalToken(t.Context(), cookie, created.ID))
	_, err = s.Authenticate(ctx, "Bearer "+created.Token, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken)
	_, api := humatest.New(t)
	registerPersonalTokenHTTP(api, s)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Delete("/authn/personal-tokens/not-an-id", "Cookie: "+cookie, "Origin: https://app.example.test").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/authn/personal-token-permissions?tenant_id=not-an-id", "Cookie: "+cookie).Code)
}

func TestPersonalTokenCorruptScopeAndDisabledPrincipalCannotAuthenticate(t *testing.T) {
	s, h, cookie, in := personalTokenService(t)
	created, err := s.CreatePersonalToken(t.Context(), cookie, verifiedPersonalTokenProof(t, s, cookie, in), in)
	require.NoError(t, err)
	principal := createdPrincipal(t, s, cookie)
	ctx := authnext.WithPersonalTokenScope(t.Context(), in.TenantID, "store_view")
	require.NoError(t, h.db.Model(&model.PersonalToken{}).Where("id = ?", created.ID).Update("permissions", "not-json").Error)
	_, err = s.Authenticate(ctx, "Bearer "+created.Token, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken)
	require.NoError(t, h.db.Model(&model.PersonalToken{}).Where("id = ?", created.ID).Update("permissions", `["store_view"]`).Error)
	require.NoError(t, h.db.Table("iam_principals").Where("id = ?", principal).Update("status", "disabled").Error)
	_, err = s.Authenticate(ctx, "Bearer "+created.Token, "")
	assert.Error(t, err)
	s.repo = &Repository{}
	_, err = s.authenticatePersonalToken(ctx, created.Token)
	assert.ErrorIs(t, err, authnext.ErrUnavailable)
}
