package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	modulepkg "7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type httpTokenVerifier struct{ principalID guid.ID }

func (verifier httpTokenVerifier) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{PrincipalID: verifier.principalID, SubjectType: authn.SubjectTypePrincipal, ACR: 1, AMR: []string{"password"}}, nil
}

type httpRouteAuthorizer struct{ platformAllowed bool }

func (httpRouteAuthorizer) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (httpRouteAuthorizer) EntityConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{AllowAll: true}, nil
}

func (httpRouteAuthorizer) CheckEntity(context.Context, guid.ID, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (authorizer httpRouteAuthorizer) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return authorizer.platformAllowed, nil
}

func (authorizer httpRouteAuthorizer) CheckOperatingPlatform(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return authorizer.platformAllowed, nil
}

type httpMembershipChecker struct {
	tenantID    guid.ID
	principalID guid.ID
}

func (checker httpMembershipChecker) IsMemberActive(_ context.Context, tenantID, principalID guid.ID) (bool, error) {
	return tenantID == checker.tenantID && principalID == checker.principalID, nil
}

func (checker httpMembershipChecker) IsOperatingPlatformMemberActive(ctx context.Context, platformID, principalID guid.ID) (bool, error) {
	return checker.IsMemberActive(ctx, platformID, principalID)
}

type httpAuthorizationEvaluator struct{}

func (httpAuthorizationEvaluator) FieldConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.FieldConstraint, error) {
	fields := map[string]authz.FieldDecision{}
	for _, field := range authz.ProfileFields() {
		fields[field.Name] = authz.DecideField(field, nil, nil)
	}
	return authz.FieldConstraint{Fields: fields}, nil
}

func (httpAuthorizationEvaluator) CheckBulk(_ context.Context, _ guid.ID, permissions []string, _ guid.ID) (map[string]bool, error) {
	result := make(map[string]bool, len(permissions))
	for _, permission := range permissions {
		result[permission] = true
	}
	return result, nil
}

func (httpAuthorizationEvaluator) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (httpAuthorizationEvaluator) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return true, nil
}

func (httpAuthorizationEvaluator) CheckOperatingPlatform(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (httpAuthorizationEvaluator) Constraint(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{AllowAll: true, OwnerIDs: []string{}, ResourceIDs: []string{}, DepartmentIDs: []string{}, Ancestors: []authz.ResourceRef{}, DeniedIDs: []string{}}, nil
}

func (e httpAuthorizationEvaluator) EntityConstraint(ctx context.Context, tenantID, _ guid.ID, permission string, subject guid.ID) (authz.DataConstraint, error) {
	return e.Constraint(ctx, tenantID, permission, subject)
}

func (httpAuthorizationEvaluator) ExplainEntity(_ context.Context, _, entityID guid.ID, _ string, _ guid.ID) (authz.Explanation, error) {
	return authz.Explanation{Allowed: true, Reason: "test_policy", Matches: []authz.DecisionMatch{{ScopeID: entityID.String()}}}, nil
}

func (httpAuthorizationEvaluator) ExplainResource(_ context.Context, _, entityID guid.ID, resourceType string, resourceID guid.ID, _ string, _ guid.ID) (authz.Explanation, error) {
	return authz.Explanation{Allowed: true, Reason: "test_policy", Matches: []authz.DecisionMatch{{ScopeID: entityID.String(), SourceType: resourceType, SourceID: resourceID.String()}}}, nil
}

type httpFailingAuthorizationEvaluator struct{ err error }

func (e httpFailingAuthorizationEvaluator) FieldConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.FieldConstraint, error) {
	return authz.FieldConstraint{}, e.err
}

func (e httpFailingAuthorizationEvaluator) CheckBulk(context.Context, guid.ID, []string, guid.ID) (map[string]bool, error) {
	return nil, e.err
}

func (e httpFailingAuthorizationEvaluator) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return false, e.err
}

func (e httpFailingAuthorizationEvaluator) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return false, e.err
}

func (e httpFailingAuthorizationEvaluator) CheckOperatingPlatform(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return false, e.err
}

func (e httpFailingAuthorizationEvaluator) Constraint(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{}, e.err
}

func (e httpFailingAuthorizationEvaluator) EntityConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{}, e.err
}

func (e httpFailingAuthorizationEvaluator) ExplainEntity(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.Explanation, error) {
	return authz.Explanation{}, e.err
}

func (e httpFailingAuthorizationEvaluator) ExplainResource(context.Context, guid.ID, guid.ID, string, guid.ID, string, guid.ID) (authz.Explanation, error) {
	return authz.Explanation{}, e.err
}

var (
	httpLocaleOnce sync.Once
	httpLocaleErr  error
)

func initializeHTTPLocales() error {
	httpLocaleOnce.Do(func() {
		if err := platformi18n.NewModule(nil, platformi18n.Base).Initialize(context.Background()); err != nil {
			httpLocaleErr = err
			return
		}
		if err := respx.NewModule(nil).Initialize(context.Background()); err != nil {
			httpLocaleErr = err
			return
		}
		httpLocaleErr = corei18n.Register()
	})
	return httpLocaleErr
}

type httpIAMHarness struct {
	repositoryHarness
	module *Module
	api    humatest.TestAPI
}

func newHTTPIAMHarness(t *testing.T, platformAllowed bool) httpIAMHarness {
	t.Helper()
	require.NoError(t, initializeHTTPLocales())
	database := newRepositoryHarness(t, "sl_")
	actorID := guid.ID(11)
	seedTenantAndPrincipal(t, database.db, "sl_", 1, actorID)
	seedSystemAccessFixture(t, database.db, "sl_")

	registry := authz.DefaultRegistry()
	registrar := authz.NewRegistrar(
		registry,
		httpTokenVerifier{principalID: actorID},
		httpRouteAuthorizer{platformAllowed: platformAllowed},
		httpMembershipChecker{tenantID: 1, principalID: actorID},
	)
	appender := func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }
	module := NewModule(database.runtime, enabledModuleConfig())
	bindModuleTables(t, module, "sl_")
	services := modulepkg.NewServices()
	require.NoError(t, modulepkg.Provide[guid.Source](services, database.repository.ids))
	require.NoError(t, modulepkg.Provide[audit.Appender](services, appender))
	require.NoError(t, modulepkg.Provide[authz.TokenVerifier](services, httpTokenVerifier{principalID: actorID}))
	require.NoError(t, services.Seal())
	require.NoError(t, module.InjectServices(services))
	module.lockPrincipal = func(ctx context.Context, tx *gorm.DB, id guid.ID) error {
		return tx.WithContext(ctx).Table("sl_iam_principals").Where("id = ?", id).Update("updated_at", gorm.Expr("updated_at")).Error
	}
	module.service.revokePersonalTokens = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }
	module.service.revokeOAuth = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }
	module.registrar = registrar
	module.service.checker = httpAuthorizationEvaluator{}
	_, err := module.service.PutTenantMember(t.Context(), 1, actorID, "Operator", "operator@example.test", 0, MemberActive)
	require.NoError(t, err)

	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("iam-test", "1.0.0"))
	require.NoError(t, module.RegisterHTTP(t.Context(), api))
	return httpIAMHarness{repositoryHarness: database, module: module, api: humatest.Wrap(t, api)}
}

func tenantPathPrefix(tenantID guid.ID) string {
	return "/tenants/" + tenantID.String()
}

func seedHTTPPrincipal(t *testing.T, harness repositoryHarness, principalID guid.ID) {
	t.Helper()
	require.NoError(t, harness.db.Exec(
		"INSERT INTO sl_iam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'active', ?, ?)",
		principalID, "principal-"+principalID.String(), "Principal "+principalID.String(), repositoryTestTime, repositoryTestTime,
	).Error)
}

func decodeEnvelope[T any](t *testing.T, response *httptest.ResponseRecorder) respx.Envelope[T] {
	t.Helper()
	var envelope respx.Envelope[T]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope), response.Body.String())
	return envelope
}

func TestIAMManagementHTTPFlowAndTenantIsolation(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	require.NoError(t, harness.module.CheckHealth(t.Context()))
	headerA := tenantPathPrefix(1)

	for _, path := range []string{
		"/permission-catalog", "/menu-catalog", "/entities", "/roles",
		"/members?limit=50", "/menus", "/authorization-relationships",
	} {
		response := harness.api.Get(headerA+path, "Authorization: Bearer test")
		assert.Equal(t, http.StatusOK, response.Code, path+": "+response.Body.String())
	}
	assert.Equal(t, http.StatusOK, harness.api.Get("/authorization-scope-model", "Authorization: Bearer test").Code)

	createdRole := harness.api.Post(headerA+"/roles", "Authorization: Bearer test", map[string]any{
		"name": "Managers", "description": "Tenant managers",
	})
	require.Equal(t, http.StatusCreated, createdRole.Code, createdRole.Body.String())
	role := decodeEnvelope[APIRole](t, createdRole).Data
	require.NotEmpty(t, role.ID)
	assert.Equal(t, http.StatusOK, harness.api.Get("/roles/"+role.ID.String(), "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Patch("/roles/"+role.ID.String(), "Authorization: Bearer test", map[string]any{
		"name": "Operators", "description": "Updated operators",
	}).Code)
	assert.Equal(t, http.StatusOK, harness.api.Put("/roles/"+role.ID.String()+"/permissions/user_view", "Authorization: Bearer test").Code)

	setScope := harness.api.Put("/roles/"+role.ID.String()+"/data-scope", "Authorization: Bearer test", map[string]any{"scope": "self"})
	require.Equal(t, http.StatusOK, setScope.Code, setScope.Body.String())
	assert.Equal(t, http.StatusOK, harness.api.Get("/roles/"+role.ID.String()+"/data-scope", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Get("/roles/"+role.ID.String()+"/permissions", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/roles/"+role.ID.String()+"/permission-grants", "Authorization: Bearer test").Code)
	condition := map[string]any{
		"condition": map[string]any{
			"version": 1,
			"gte":     []any{map[string]any{"context": "auth.acr"}, map[string]any{"value": 1}},
		},
	}
	assert.Equal(t, http.StatusOK, harness.api.Put("/roles/"+role.ID.String()+"/permissions/user_view/condition", "Authorization: Bearer test", condition).Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/roles/"+role.ID.String()+"/permissions/user_view/condition", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Put("/roles/"+role.ID.String()+"/members/11", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/roles/"+role.ID.String()+"/members", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Get(headerA+"/members/11/roles", "Authorization: Bearer test").Code)

	seedDirectoryFixtures(t, harness.repositoryHarness, 1)
	assert.Equal(t, http.StatusOK, harness.api.Put("/roles/"+role.ID.String()+"/directory-bindings/group/200", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/roles/"+role.ID.String()+"/directory-bindings", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/roles/"+role.ID.String()+"/directory-bindings/group/200", "Authorization: Bearer test").Code)

	seedHTTPPrincipal(t, harness.repositoryHarness, 12)
	createdMember := harness.api.Post(headerA+"/members", "Authorization: Bearer test", map[string]any{
		"subject": "12", "display_name": "Second member", "department_id": "100", "status": "active",
	})
	require.Equal(t, http.StatusCreated, createdMember.Code, createdMember.Body.String())
	assert.Equal(t, http.StatusOK, harness.api.Get(headerA+"/members/12", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Patch(headerA+"/members/12", "Authorization: Bearer test", map[string]any{
		"display_name": "Updated member", "email": "updated@example.test", "department_id": "100", "status": "active",
	}).Code)

	createdMenu := harness.api.Post(headerA+"/menus", "Authorization: Bearer test", map[string]any{
		"label": "Members", "route": "/members", "permission_code": "user_view", "status": "active",
	})
	require.Equal(t, http.StatusCreated, createdMenu.Code, createdMenu.Body.String())
	assert.NotContains(t, createdMenu.Body.String(), `"parent_id":"0"`)
	menuA := decodeEnvelope[APIMenu](t, createdMenu).Data
	assert.Equal(t, http.StatusOK, harness.api.Get("/menus/"+menuA.ID.String(), "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Patch("/menus/"+menuA.ID.String(), "Authorization: Bearer test", map[string]any{
		"parent_id": "", "label": "People", "route": "/people", "icon": "users", "sort_order": 10,
		"permission_code": "user_view", "status": "active",
	}).Code)
	effectiveMenus := harness.api.Get(headerA+"/principals/"+"11"+"/menus", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, effectiveMenus.Code, effectiveMenus.Body.String())
	assert.Contains(t, effectiveMenus.Body.String(), "People")

	createdEntity := harness.api.Post(headerA+"/entities", "Authorization: Bearer test", map[string]any{
		"type": "company", "name": "Tenant A company", "metadata": map[string]any{"region": "west"},
	})
	require.Equal(t, http.StatusCreated, createdEntity.Code, createdEntity.Body.String())
	assert.NotContains(t, createdEntity.Body.String(), `"parent_id":"0"`)
	entityA := decodeEnvelope[APIEntity](t, createdEntity).Data
	assert.Equal(t, http.StatusOK, harness.api.Get("/entities/"+entityA.ID.String(), "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Patch("/entities/"+entityA.ID.String(), "Authorization: Bearer test", map[string]any{
		"type": "company", "name": "Tenant A updated", "status": "active", "metadata": map[string]any{"region": "north"},
	}).Code)
	childEntity := harness.api.Post(headerA+"/entities", "Authorization: Bearer test", map[string]any{
		"parent_id": entityA.ID, "type": "store", "name": "Tenant A child",
	})
	require.Equal(t, http.StatusCreated, childEntity.Code, childEntity.Body.String())
	childEntityID := decodeEnvelope[APIEntity](t, childEntity).Data.ID
	clearParent := harness.api.Patch("/entities/"+childEntityID.String(), "Authorization: Bearer test", map[string]any{"parent_id": ""})
	require.Equal(t, http.StatusOK, clearParent.Code, clearParent.Body.String())
	assert.NotContains(t, clearParent.Body.String(), `"parent_id"`)
	expiresAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	assert.Equal(t, http.StatusOK, harness.api.Put("/entities/"+entityA.ID.String()+"/role-bindings/"+role.ID.String()+"/11", "Authorization: Bearer test", map[string]any{
		"effect": "allow", "expires_at": expiresAt,
	}).Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/entities/"+entityA.ID.String()+"/role-bindings", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/entities/"+entityA.ID.String()+"/role-bindings/"+role.ID.String()+"/11", "Authorization: Bearer test").Code)

	startsAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	endsAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	createdRelationship := harness.api.Post(headerA+"/authorization-relationships", "Authorization: Bearer test", map[string]any{
		"subject_type": "principal", "subject_id": "12", "relation": "owner",
		"resource_type": "company", "resource_id": entityA.ID, "starts_at": startsAt, "ends_at": endsAt,
		"condition": map[string]any{"version": 1, "gte": []any{map[string]any{"context": "auth.acr"}, map[string]any{"value": 1}}},
	})
	require.Equal(t, http.StatusOK, createdRelationship.Code, createdRelationship.Body.String())
	assert.NotContains(t, createdRelationship.Body.String(), `"entity_id":"0"`)
	assert.Equal(t, http.StatusOK, harness.api.Get(headerA+"/authorization-relationships?resource_id="+entityA.ID.String(), "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Get(headerA+"/authorization-relationships?entity_id="+entityA.ID.String()+"&resource_type=company", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Post(headerA+"/authorization-checks", "Authorization: Bearer test", map[string]any{
		"entity_id": entityA.ID, "permission_code": "entity_view", "subject": "12",
	}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, harness.api.Post(headerA+"/authorization-checks", "Authorization: Bearer test", map[string]any{
		"entity_id": entityA.ID, "resource_type": "merchant", "permission_code": "merchant_view", "subject": "12",
	}).Code)
	assert.Equal(t, http.StatusOK, harness.api.Post(headerA+"/authorization-checks", "Authorization: Bearer test", map[string]any{
		"entity_id": entityA.ID, "resource_type": "merchant", "resource_id": "700", "permission_code": "merchant_view", "subject": "12",
	}).Code)
	assert.Equal(t, http.StatusOK, harness.api.Post(headerA+"/authorization-constraint-evaluations", "Authorization: Bearer test", map[string]any{
		"permission_code": "entity_view", "subject": "12",
	}).Code)
	assert.Equal(t, http.StatusOK, harness.api.Post(headerA+"/authorization-explanations", "Authorization: Bearer test", map[string]any{
		"entity_id": entityA.ID, "permission_code": "entity_view", "subject": "12",
	}).Code)
	assert.Equal(t, http.StatusOK, harness.api.Post(headerA+"/authorization-explanations", "Authorization: Bearer test", map[string]any{
		"entity_id": entityA.ID, "resource_type": "merchant", "resource_id": "700", "permission_code": "merchant_view", "subject": "12",
	}).Code)
	deleteRelationship := harness.api.Delete(
		headerA+"/authorization-relationships?subject_type=principal&subject_id=12&relation=owner&resource_type=company&resource_id="+entityA.ID.String(),
		"Authorization: Bearer test",
	)
	require.Equal(t, http.StatusOK, deleteRelationship.Code, deleteRelationship.Body.String())
	assert.True(t, decodeEnvelope[MutationResult](t, deleteRelationship).Data.Changed)

	seedHTTPPrincipal(t, harness.repositoryHarness, 44)
	_, err := harness.module.service.SetPlatformAdministrator(t.Context(), 11, true, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, harness.api.Get("/system/permission-catalog", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/system/administrators", "Authorization: Bearer test").Code)
	putPlatform := harness.api.Put("/system/administrators/44", "Authorization: Bearer test", map[string]any{
		"full_administrator": false, "permissions": []string{"tenant_view"},
	})
	require.Equal(t, http.StatusOK, putPlatform.Code, putPlatform.Body.String())
	assert.Equal(t, http.StatusOK, harness.api.Delete("/system/administrators/44", "Authorization: Bearer test").Code)

	seedTenantAndPrincipal(t, harness.db, "sl_", 2, 22)
	_, err = harness.module.service.PutTenantMember(t.Context(), 2, 22, "Tenant B member", "b@example.test", 0, MemberActive)
	require.NoError(t, err)
	roleB, err := harness.module.service.CreateRole(t.Context(), 2, "Tenant B role", "must remain isolated")
	require.NoError(t, err)
	menuB, err := harness.module.service.CreateMenu(t.Context(), Menu{TenantID: 2, Label: "Tenant B", Route: "/b", Status: MenuActive})
	require.NoError(t, err)
	entityB, err := harness.module.service.CreateEntity(t.Context(), Entity{TenantID: 2, Type: "company", Name: "Tenant B", Status: EntityActive})
	require.NoError(t, err)
	relationshipB, _, err := harness.module.service.PutRelationship(t.Context(), Relationship{
		TenantID: 2, SubjectType: "principal", SubjectID: 22, Relation: "owner",
		ResourceType: "company", ResourceID: entityB.ID,
	})
	require.NoError(t, err)

	crossTenantCases := []struct {
		name string
		call func() *httptest.ResponseRecorder
	}{
		{name: "get role", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/roles/"+roleB.ID.String(), "Authorization: Bearer test")
		}},
		{name: "update role", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/roles/"+roleB.ID.String(), "Authorization: Bearer test", map[string]any{"name": "stolen"})
		}},
		{name: "delete role", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/"+roleB.ID.String(), "Authorization: Bearer test")
		}},
		{name: "grant role", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+roleB.ID.String()+"/permissions/user_view", "Authorization: Bearer test")
		}},
		{name: "get member", call: func() *httptest.ResponseRecorder {
			return harness.api.Get(headerA+"/members/22", "Authorization: Bearer test")
		}},
		{name: "update member", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch(headerA+"/members/22", "Authorization: Bearer test", map[string]any{"display_name": "stolen"})
		}},
		{name: "get menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/menus/"+menuB.ID.String(), "Authorization: Bearer test")
		}},
		{name: "update menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/menus/"+menuB.ID.String(), "Authorization: Bearer test", map[string]any{"label": "stolen"})
		}},
		{name: "delete menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/menus/"+menuB.ID.String(), "Authorization: Bearer test")
		}},
		{name: "get entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/entities/"+entityB.ID.String(), "Authorization: Bearer test")
		}},
		{name: "update entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/entities/"+entityB.ID.String(), "Authorization: Bearer test", map[string]any{"name": "stolen"})
		}},
		{name: "delete entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/entities/"+entityB.ID.String(), "Authorization: Bearer test")
		}},
	}
	for _, test := range crossTenantCases {
		t.Run(test.name, func(t *testing.T) {
			response := test.call()
			assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), `"message":"`+corei18n.ResourceNotFound+`"`)
		})
	}

	rolesA := harness.api.Get(headerA+"/roles", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, rolesA.Code, rolesA.Body.String())
	assert.NotContains(t, rolesA.Body.String(), roleB.ID.String())
	membersA := harness.api.Get(headerA+"/members?limit=50", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, membersA.Code, membersA.Body.String())
	assert.NotContains(t, membersA.Body.String(), "Tenant B member")
	assert.NotContains(t, membersA.Body.String(), `"department_id":"0"`)
	menusA := harness.api.Get(headerA+"/menus", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, menusA.Code, menusA.Body.String())
	assert.NotContains(t, menusA.Body.String(), menuB.ID.String())
	entitiesA := harness.api.Get(headerA+"/entities", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, entitiesA.Code, entitiesA.Body.String())
	assert.NotContains(t, entitiesA.Body.String(), entityB.ID.String())
	relationshipsA := harness.api.Get(headerA+"/authorization-relationships?resource_id="+entityB.ID.String(), "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, relationshipsA.Code, relationshipsA.Body.String())
	assert.NotContains(t, relationshipsA.Body.String(), relationshipB.ResourceID.String())

	deleteCrossRelationship := harness.api.Delete(
		headerA+"/authorization-relationships?subject_type=principal&subject_id=22&relation=owner&resource_type=company&resource_id="+entityB.ID.String(),
		"Authorization: Bearer test",
	)
	require.Equal(t, http.StatusOK, deleteCrossRelationship.Code, deleteCrossRelationship.Body.String())
	assert.False(t, decodeEnvelope[MutationResult](t, deleteCrossRelationship).Data.Changed)

	unchangedRole, err := harness.module.service.GetRole(t.Context(), 2, roleB.ID)
	require.NoError(t, err)
	assert.Equal(t, "Tenant B role", unchangedRole.Name)
	unchangedMember, err := harness.module.service.GetTenantMember(t.Context(), 2, 22)
	require.NoError(t, err)
	assert.Equal(t, "Tenant B member", unchangedMember.DisplayName)
	unchangedMenu, err := harness.module.service.GetMenu(t.Context(), 2, menuB.ID)
	require.NoError(t, err)
	assert.Equal(t, "Tenant B", unchangedMenu.Label)
	unchangedEntity, err := harness.module.service.GetEntity(t.Context(), 2, entityB.ID)
	require.NoError(t, err)
	assert.Equal(t, "Tenant B", unchangedEntity.Name)
	unchangedRelationships, err := harness.module.service.ListRelationships(t.Context(), 2, RelationshipFilter{ResourceID: entityB.ID})
	require.NoError(t, err)
	assert.Len(t, unchangedRelationships, 1)

	assert.Equal(t, http.StatusOK, harness.api.Delete("/menus/"+menuA.ID.String(), "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/entities/"+entityA.ID.String(), "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/entities/"+childEntityID.String(), "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/roles/"+role.ID.String()+"/members/11", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/roles/"+role.ID.String()+"/permissions/user_view", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/roles/"+role.ID.String(), "Authorization: Bearer test").Code)
}

func TestMemberAuthorizationSourcesHTTPReturnsSnapshotAndNotFound(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	authorizer := boundAuthorizer(t, harness.runtime, "sl_")
	authorizer.now = harness.repository.now
	harness.module.service.checker = authorizer
	header := tenantPathPrefix(1)

	role, err := harness.module.service.CreateRole(t.Context(), 1, "Auditor", "")
	require.NoError(t, err)
	_, err = harness.module.service.GrantPermission(t.Context(), 1, role.ID, "audit_event_view")
	require.NoError(t, err)
	_, err = harness.module.service.AddMember(t.Context(), 1, role.ID, 11)
	require.NoError(t, err)

	response := harness.api.Get(
		header+"/members/11/authorization-sources", "Authorization: Bearer test",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	snapshot := decodeEnvelope[APIMemberAuthorizationSnapshot](t, response).Data
	assert.Equal(t, "11", snapshot.Subject)
	assert.NotZero(t, snapshot.Revision)
	require.Len(t, snapshot.Sources, 1)
	assert.Equal(t, AuthorizationSourcePermanentRole, snapshot.Sources[0].Type)
	assert.Equal(t, role.ID, snapshot.Sources[0].RoleID)
	assert.Equal(t, []string{"audit_event_view"}, snapshot.Sources[0].PermissionCodes)
	assert.Equal(t, BindingAllow, snapshot.Sources[0].Effect)
	assert.NotNil(t, snapshot.Sources[0].DepartmentIDs)
	assert.NotNil(t, snapshot.Sources[0].RelationshipPath)
	assert.Empty(t, snapshot.Sources[0].RelationshipPath)

	notFound := harness.api.Get(
		header+"/members/999/authorization-sources", "Authorization: Bearer test",
	)
	assert.Equal(t, http.StatusNotFound, notFound.Code, notFound.Body.String())
}

// TestCreatingARootAcceptsTheClearedFormFieldTheUpdateAccepts covers the shape a
// client actually sends. A form offering "no parent" as a cleared select posts
// an empty string, and the update already reads that as the root, so a create
// that rejects it makes the first node of a tree the one node the console
// cannot produce.
func TestCreatingARootAcceptsTheClearedFormFieldTheUpdateAccepts(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	header := tenantPathPrefix(1)
	seedHTTPPrincipal(t, harness.repositoryHarness, 13)

	for name, test := range map[string]struct {
		path string
		body map[string]any
	}{
		"a root menu": {path: "/menus", body: map[string]any{
			"parent_id": "", "label": "Members", "route": "/members",
		}},
		"a root entity": {path: "/entities", body: map[string]any{
			"parent_id": "", "type": "company", "name": "Root company",
		}},
		"a member in no department": {path: "/members", body: map[string]any{
			"department_id": "", "subject": "13", "display_name": "Third member",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			response := harness.api.Post(header+test.path, "Authorization: Bearer test", test.body)
			require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
			// The cleared field means the same as an omitted one, so nothing in
			// the response may claim a parent the caller did not name.
			assert.NotContains(t, response.Body.String(), `"parent_id":"0"`)
			assert.NotContains(t, response.Body.String(), `"department_id":"0"`)
		})
	}
}

func TestPlatformRoutesRejectPrincipalWithoutPlatformGrant(t *testing.T) {
	harness := newHTTPIAMHarness(t, false)
	for _, response := range []*httptest.ResponseRecorder{
		harness.api.Get("/system/permission-catalog", "Authorization: Bearer test"),
		harness.api.Get("/system/administrators", "Authorization: Bearer test"),
		harness.api.Delete("/system/administrators/11", "Authorization: Bearer test"),
	} {
		assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}

	tenantRoute := harness.api.Get(tenantPathPrefix(1)+"/roles", "Authorization: Bearer test")
	assert.Equal(t, http.StatusOK, tenantRoute.Code, tenantRoute.Body.String())
	assert.NotContains(t, tenantRoute.Body.String(), "forbidden")
}

func TestIAMAuthorizationRoutesMapEvaluatorFailure(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	harness.module.service.checker = httpFailingAuthorizationEvaluator{err: errors.New("authorization backend unavailable")}
	header := tenantPathPrefix(1)

	for _, response := range []*httptest.ResponseRecorder{
		harness.api.Post(header+"/authorization-constraint-evaluations", "Authorization: Bearer test", map[string]any{
			"permission_code": "merchant_view", "subject": "11",
		}),
		harness.api.Post(header+"/authorization-checks", "Authorization: Bearer test", map[string]any{
			"entity_id": "1", "permission_code": "merchant_view", "subject": "11",
		}),
		harness.api.Post(header+"/authorization-explanations", "Authorization: Bearer test", map[string]any{
			"entity_id": "1", "permission_code": "merchant_view", "subject": "11",
		}),
		harness.api.Post(header+"/authorization-explanations", "Authorization: Bearer test", map[string]any{
			"entity_id": "1", "resource_type": "merchant", "resource_id": "2", "permission_code": "merchant_view", "subject": "11",
		}),
	} {
		assert.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "authorization backend unavailable")
	}
}

func TestIAMHTTPInternalFailuresUseUniformEnvelope(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	database, err := harness.db.DB()
	require.NoError(t, err)
	require.NoError(t, database.Close())

	header := tenantPathPrefix(1)
	condition := map[string]any{
		"condition": map[string]any{"version": 1, "eq": []any{map[string]any{"context": "auth.acr"}, map[string]any{"value": 1}}},
	}
	for _, test := range []struct {
		name string
		call func() *httptest.ResponseRecorder
	}{
		{name: "list entities", call: func() *httptest.ResponseRecorder {
			return harness.api.Get(header+"/entities", "Authorization: Bearer test")
		}},
		{name: "create entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Post(header+"/entities", "Authorization: Bearer test", map[string]any{"type": "store", "name": "Store"})
		}},
		{name: "get entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/entities/1", "Authorization: Bearer test")
		}},
		{name: "update entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/entities/1", "Authorization: Bearer test", map[string]any{"name": "Store"})
		}},
		{name: "delete entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/entities/1", "Authorization: Bearer test")
		}},
		{name: "list entity bindings", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/entities/1/role-bindings", "Authorization: Bearer test")
		}},
		{name: "put entity binding", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/entities/1/role-bindings/2/3", "Authorization: Bearer test", map[string]any{"effect": "allow"})
		}},
		{name: "delete entity binding", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/entities/1/role-bindings/2/3", "Authorization: Bearer test")
		}},
		{name: "list roles", call: func() *httptest.ResponseRecorder {
			return harness.api.Get(header+"/roles", "Authorization: Bearer test")
		}},
		{name: "create role", call: func() *httptest.ResponseRecorder {
			return harness.api.Post(header+"/roles", "Authorization: Bearer test", map[string]any{"name": "Role"})
		}},
		{name: "get role", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/roles/1", "Authorization: Bearer test")
		}},
		{name: "get role data scope", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/roles/1/data-scope", "Authorization: Bearer test")
		}},
		{name: "set role data scope", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/1/data-scope", "Authorization: Bearer test", map[string]any{"scope": "self"})
		}},
		{name: "update role", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/roles/1", "Authorization: Bearer test", map[string]any{"name": "Role"})
		}},
		{name: "delete role", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/1", "Authorization: Bearer test")
		}},
		{name: "list role grants", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/roles/1/permission-grants", "Authorization: Bearer test")
		}},
		{name: "grant permission", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/1/permissions/user_view", "Authorization: Bearer test")
		}},
		{name: "revoke permission", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/1/permissions/user_view", "Authorization: Bearer test")
		}},
		{name: "set permission condition", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/1/permissions/user_view/condition", "Authorization: Bearer test", condition)
		}},
		{name: "clear permission condition", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/1/permissions/user_view/condition", "Authorization: Bearer test")
		}},
		{name: "list role members", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/roles/1/members", "Authorization: Bearer test")
		}},
		{name: "add role member", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/1/members/2", "Authorization: Bearer test")
		}},
		{name: "remove role member", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/1/members/2", "Authorization: Bearer test")
		}},
		{name: "list directory bindings", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/roles/1/directory-bindings", "Authorization: Bearer test")
		}},
		{name: "add directory binding", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/1/directory-bindings/group/2", "Authorization: Bearer test")
		}},
		{name: "remove directory binding", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/1/directory-bindings/group/2", "Authorization: Bearer test")
		}},
		{name: "list members", call: func() *httptest.ResponseRecorder {
			return harness.api.Get(header+"/members?limit=50", "Authorization: Bearer test")
		}},
		{name: "create member", call: func() *httptest.ResponseRecorder {
			return harness.api.Post(header+"/members", "Authorization: Bearer test", map[string]any{"subject": "2", "display_name": "Member"})
		}},
		{name: "get member", call: func() *httptest.ResponseRecorder {
			return harness.api.Get(header+"/members/2", "Authorization: Bearer test")
		}},
		{name: "update member", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch(header+"/members/2", "Authorization: Bearer test", map[string]any{"display_name": "Member"})
		}},
		{name: "list member roles", call: func() *httptest.ResponseRecorder {
			return harness.api.Get(header+"/members/2/roles", "Authorization: Bearer test")
		}},
		{name: "list menus", call: func() *httptest.ResponseRecorder {
			return harness.api.Get(header+"/menus", "Authorization: Bearer test")
		}},
		{name: "create menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Post(header+"/menus", "Authorization: Bearer test", map[string]any{"label": "Menu"})
		}},
		{name: "get menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/menus/1", "Authorization: Bearer test")
		}},
		{name: "update menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/menus/1", "Authorization: Bearer test", map[string]any{"label": "Menu"})
		}},
		{name: "delete menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/menus/1", "Authorization: Bearer test")
		}},
		{name: "effective menus", call: func() *httptest.ResponseRecorder {
			return harness.api.Get(header+"/principals/"+"11"+"/menus", "Authorization: Bearer test")
		}},
		{name: "list relationships", call: func() *httptest.ResponseRecorder {
			return harness.api.Get(header+"/authorization-relationships", "Authorization: Bearer test")
		}},
		{name: "put relationship", call: func() *httptest.ResponseRecorder {
			return harness.api.Post(header+"/authorization-relationships", "Authorization: Bearer test", map[string]any{"subject_type": "principal", "subject_id": "2", "relation": "viewer", "resource_type": "store", "resource_id": "1"})
		}},
		{name: "delete relationship", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete(header+"/authorization-relationships?subject_type=principal&subject_id=2&relation=viewer&resource_type=store&resource_id=1", "Authorization: Bearer test")
		}},
		{name: "list platform administrators", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/system/administrators", "Authorization: Bearer test")
		}},
		{name: "put platform administrator", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/system/administrators/2", "Authorization: Bearer test", map[string]any{"full_administrator": false, "permissions": []string{"tenant_view"}})
		}},
		{name: "delete platform administrator", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/system/administrators/2", "Authorization: Bearer test")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := test.call()
			assert.Contains(t, []int{http.StatusInternalServerError, http.StatusServiceUnavailable}, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "database is closed")
			assert.Contains(t, response.Body.String(), `"code":`+strconv.Itoa(response.Code))
			assert.Contains(t, response.Body.String(), `"data":null`)
		})
	}
}

// TestIAMHTTPFailsClosedWhenStorageIsGone drives every route the module
// registers while the database is unreachable. A route that answered 200 with
// an empty list, or reported a write as done, would be indistinguishable from a
// tenant that really holds nothing -- and an administration console would act
// on that. The static catalogs are the deliberate exception: they are compiled
// in, so they must keep answering while storage is down.
func TestIAMHTTPFailsClosedWhenStorageIsGone(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	auth, tenant := "Authorization: Bearer test", tenantPathPrefix(1)
	seedHTTPPrincipal(t, harness.repositoryHarness, 12)
	seedDirectoryFixtures(t, harness.repositoryHarness, 1)
	_, err := harness.module.service.SetPlatformAdministrator(t.Context(), 11, true, nil)
	require.NoError(t, err)
	role, err := harness.module.service.CreateRole(t.Context(), 1, "Managers", "")
	require.NoError(t, err)
	entity, err := harness.module.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "company", Name: "Company"})
	require.NoError(t, err)
	menu, err := harness.module.service.CreateMenu(t.Context(), Menu{TenantID: 1, Label: "People", Route: "/people", Status: MenuActive})
	require.NoError(t, err)
	roleID, entityID, menuID := role.ID.String(), entity.ID.String(), menu.ID.String()

	connection, err := harness.db.DB()
	require.NoError(t, err)
	require.NoError(t, connection.Close())

	// The compiled-in catalogs and the routes answered by the authorization
	// evaluator describe the software rather than the rows of a deployment, so
	// they keep answering while storage is gone.
	for _, path := range []string{
		"/authorization-scope-model", tenant + "/permission-catalog", tenant + "/menu-catalog",
		"/system/permission-catalog", tenant + "/field-catalog",
	} {
		response := harness.api.Get(path, auth)
		require.Equal(t, http.StatusOK, response.Code, "%s: %s", path, response.Body.String())
	}

	condition := map[string]any{"condition": map[string]any{
		"version": 1, "gte": []any{map[string]any{"context": "auth.acr"}, map[string]any{"value": 1}},
	}}
	for name, call := range map[string]func() *httptest.ResponseRecorder{
		"my memberships": func() *httptest.ResponseRecorder { return harness.api.Get("/principals/"+"11"+"/memberships", auth) },
		"leave membership": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/principals/"+"11"+"/memberships/1", auth)
		},
		"list field policies": func() *httptest.ResponseRecorder {
			return harness.api.Get(tenant+"/field-policies", auth)
		},
		"set field policy": func() *httptest.ResponseRecorder {
			return harness.api.Put(tenant+"/field-policies/profile/display_name", auth, map[string]any{
				"read": map[string]any{"mode": "all"}, "write": map[string]any{"mode": "all"}, "mask": "omit",
			})
		},
		"delete field policy": func() *httptest.ResponseRecorder {
			return harness.api.Delete(tenant+"/field-policies/profile/display_name", auth)
		},
		"list relationships": func() *httptest.ResponseRecorder {
			return harness.api.Get(tenant+"/authorization-relationships?resource_id="+entityID, auth)
		},
		"put relationship": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/authorization-relationships", auth, map[string]any{
				"subject_type": "principal", "subject_id": "12", "relation": "owner",
				"resource_type": "company", "resource_id": entityID,
			})
		},
		"delete relationship": func() *httptest.ResponseRecorder {
			return harness.api.Delete(tenant+
				"/authorization-relationships?subject_type=principal&subject_id=12&relation=owner&resource_type=company&resource_id="+entityID, auth)
		},
		"list platform administrators": func() *httptest.ResponseRecorder {
			return harness.api.Get("/system/administrators", auth)
		},
		"put platform administrator": func() *httptest.ResponseRecorder {
			return harness.api.Put("/system/administrators/12", auth, map[string]any{
				"full_administrator": false, "permissions": []string{"tenant_view"},
			})
		},
		"delete platform administrator": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/system/administrators/12", auth)
		},
		"list entities": func() *httptest.ResponseRecorder { return harness.api.Get(tenant+"/entities", auth) },
		"create entity": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/entities", auth, map[string]any{"type": "company", "name": "New"})
		},
		"get entity":    func() *httptest.ResponseRecorder { return harness.api.Get("/entities/"+entityID, auth) },
		"delete entity": func() *httptest.ResponseRecorder { return harness.api.Delete("/entities/"+entityID, auth) },
		"update entity": func() *httptest.ResponseRecorder {
			return harness.api.Patch("/entities/"+entityID, auth, map[string]any{"name": "Renamed"})
		},
		"list entity role bindings": func() *httptest.ResponseRecorder {
			return harness.api.Get("/entities/"+entityID+"/role-bindings", auth)
		},
		"put entity role binding": func() *httptest.ResponseRecorder {
			return harness.api.Put("/entities/"+entityID+"/role-bindings/"+roleID+"/11", auth, map[string]any{"effect": "allow"})
		},
		"delete entity role binding": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/entities/"+entityID+"/role-bindings/"+roleID+"/11", auth)
		},
		"list roles": func() *httptest.ResponseRecorder { return harness.api.Get(tenant+"/roles", auth) },
		"create role": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/roles", auth, map[string]any{"name": "New role"})
		},
		"get role":    func() *httptest.ResponseRecorder { return harness.api.Get("/roles/"+roleID, auth) },
		"delete role": func() *httptest.ResponseRecorder { return harness.api.Delete("/roles/"+roleID, auth) },
		"update role": func() *httptest.ResponseRecorder {
			return harness.api.Patch("/roles/"+roleID, auth, map[string]any{"name": "Renamed"})
		},
		"get data scope": func() *httptest.ResponseRecorder { return harness.api.Get("/roles/"+roleID+"/data-scope", auth) },
		"set data scope": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+roleID+"/data-scope", auth, map[string]any{"scope": "self"})
		},
		"list role permission grants": func() *httptest.ResponseRecorder {
			return harness.api.Get("/roles/"+roleID+"/permission-grants", auth)
		},
		"grant role permission": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+roleID+"/permissions/user_view", auth)
		},
		"revoke role permission": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/"+roleID+"/permissions/user_view", auth)
		},
		"set permission condition": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+roleID+"/permissions/user_view/condition", auth, condition)
		},
		"clear permission condition": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/"+roleID+"/permissions/user_view/condition", auth)
		},
		"list role members": func() *httptest.ResponseRecorder {
			return harness.api.Get("/roles/"+roleID+"/members", auth)
		},
		"add role member": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+roleID+"/members/11", auth)
		},
		"remove role member": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/"+roleID+"/members/11", auth)
		},
		"list directory bindings": func() *httptest.ResponseRecorder {
			return harness.api.Get("/roles/"+roleID+"/directory-bindings", auth)
		},
		"add directory binding": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+roleID+"/directory-bindings/group/200", auth)
		},
		"remove directory binding": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/"+roleID+"/directory-bindings/group/200", auth)
		},
		"list tenant members": func() *httptest.ResponseRecorder {
			return harness.api.Get(tenant+"/members?limit=50", auth)
		},
		"create tenant member": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/members", auth, map[string]any{
				"subject": "12", "display_name": "Second member", "status": "active",
			})
		},
		"get tenant member": func() *httptest.ResponseRecorder { return harness.api.Get(tenant+"/members/11", auth) },
		"update tenant member": func() *httptest.ResponseRecorder {
			return harness.api.Patch(tenant+"/members/11", auth, map[string]any{"display_name": "Renamed"})
		},
		"list tenant member roles": func() *httptest.ResponseRecorder {
			return harness.api.Get(tenant+"/members/11/roles", auth)
		},
		"member authorization sources": func() *httptest.ResponseRecorder {
			return harness.api.Get(tenant+"/members/11/authorization-sources", auth)
		},
		"list menus": func() *httptest.ResponseRecorder { return harness.api.Get(tenant+"/menus", auth) },
		"create menu": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/menus", auth, map[string]any{
				"label": "Members", "route": "/members", "permission_code": "user_view", "status": "active",
			})
		},
		"get menu":    func() *httptest.ResponseRecorder { return harness.api.Get("/menus/"+menuID, auth) },
		"delete menu": func() *httptest.ResponseRecorder { return harness.api.Delete("/menus/"+menuID, auth) },
		"update menu": func() *httptest.ResponseRecorder {
			return harness.api.Patch("/menus/"+menuID, auth, map[string]any{"label": "Renamed"})
		},
		"effective menus": func() *httptest.ResponseRecorder { return harness.api.Get(tenant+"/principals/"+"11"+"/menus", auth) },
	} {
		t.Run(name, func(t *testing.T) {
			response := call()
			require.GreaterOrEqual(t, response.Code, http.StatusBadRequest,
				"%s answered %d while storage was gone: %s", name, response.Code, response.Body.String())
		})
	}
}

// unrepresentableID is nineteen digits: it satisfies every path and body
// pattern the contract declares and still does not fit the platform identifier
// type. It is the value that separates "rejected as malformed input" from
// "silently truncated into some other record".
const unrepresentableID = "9999999999999999999"

// TestIAMHTTPRejectsUnrepresentableIdentifiers drives every route that accepts
// an identifier with one that passes schema validation but cannot be parsed.
// Each must answer 422: a 404 would claim the record is missing, and a 200
// would mean the request was applied to whatever the value truncated into.
func TestIAMHTTPRejectsUnrepresentableIdentifiers(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	auth, tenant, big := "Authorization: Bearer test", tenantPathPrefix(1), unrepresentableID
	condition := map[string]any{"condition": map[string]any{
		"version": 1, "gte": []any{map[string]any{"context": "auth.acr"}, map[string]any{"value": 1}},
	}}

	for name, call := range map[string]func() *httptest.ResponseRecorder{
		"get entity":    func() *httptest.ResponseRecorder { return harness.api.Get("/entities/" + big) },
		"delete entity": func() *httptest.ResponseRecorder { return harness.api.Delete("/entities/"+big, auth) },
		"update entity": func() *httptest.ResponseRecorder {
			return harness.api.Patch("/entities/"+big, auth, map[string]any{"name": "Renamed"})
		},
		"create entity parent": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/entities", auth, map[string]any{
				"parent_id": big, "type": "store", "name": "Store",
			})
		},
		"update entity parent": func() *httptest.ResponseRecorder {
			return harness.api.Patch("/entities/"+big, auth, map[string]any{"parent_id": big})
		},
		"list entity role bindings": func() *httptest.ResponseRecorder {
			return harness.api.Get("/entities/"+big+"/role-bindings", auth)
		},
		"put entity role binding": func() *httptest.ResponseRecorder {
			return harness.api.Put("/entities/"+big+"/role-bindings/"+big+"/"+big, auth, map[string]any{"effect": "allow"})
		},
		"delete entity role binding": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/entities/"+big+"/role-bindings/"+big+"/"+big, auth)
		},
		"get role":    func() *httptest.ResponseRecorder { return harness.api.Get("/roles/"+big, auth) },
		"delete role": func() *httptest.ResponseRecorder { return harness.api.Delete("/roles/"+big, auth) },
		"update role": func() *httptest.ResponseRecorder {
			return harness.api.Patch("/roles/"+big, auth, map[string]any{"name": "Renamed"})
		},
		"get data scope": func() *httptest.ResponseRecorder { return harness.api.Get("/roles/"+big+"/data-scope", auth) },
		"set data scope": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+big+"/data-scope", auth, map[string]any{"scope": "self"})
		},
		"list role permission grants": func() *httptest.ResponseRecorder {
			return harness.api.Get("/roles/"+big+"/permission-grants", auth)
		},
		"grant role permission": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+big+"/permissions/user_view", auth)
		},
		"revoke role permission": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/"+big+"/permissions/user_view", auth)
		},
		"set permission condition": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+big+"/permissions/user_view/condition", auth, condition)
		},
		"clear permission condition": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/"+big+"/permissions/user_view/condition", auth)
		},
		"list role members": func() *httptest.ResponseRecorder {
			return harness.api.Get("/roles/"+big+"/members", auth)
		},
		"add role member": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+big+"/members/"+big, auth)
		},
		"remove role member": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/"+big+"/members/"+big, auth)
		},
		"list directory bindings": func() *httptest.ResponseRecorder {
			return harness.api.Get("/roles/"+big+"/directory-bindings", auth)
		},
		"add directory binding": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+big+"/directory-bindings/group/"+big, auth)
		},
		"remove directory binding": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/"+big+"/directory-bindings/group/"+big, auth)
		},
		"get tenant member": func() *httptest.ResponseRecorder { return harness.api.Get(tenant+"/members/"+big, auth) },
		"update tenant member": func() *httptest.ResponseRecorder {
			return harness.api.Patch(tenant+"/members/"+big, auth, map[string]any{"display_name": "Renamed"})
		},
		"create tenant member": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/members", auth, map[string]any{
				"subject": big, "display_name": "Member", "status": "active",
			})
		},
		"create tenant member department": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/members", auth, map[string]any{
				"subject": "11", "display_name": "Member", "department_id": big, "status": "active",
			})
		},
		"list tenant member roles": func() *httptest.ResponseRecorder {
			return harness.api.Get(tenant+"/members/"+big+"/roles", auth)
		},
		"member authorization sources": func() *httptest.ResponseRecorder {
			return harness.api.Get(tenant+"/members/"+big+"/authorization-sources", auth)
		},
		"get menu":    func() *httptest.ResponseRecorder { return harness.api.Get("/menus/"+big, auth) },
		"delete menu": func() *httptest.ResponseRecorder { return harness.api.Delete("/menus/"+big, auth) },
		"update menu": func() *httptest.ResponseRecorder {
			return harness.api.Patch("/menus/"+big, auth, map[string]any{"label": "Renamed"})
		},
		"create menu parent": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/menus", auth, map[string]any{
				"label": "Menu", "route": "/menu", "parent_id": big, "status": "active",
			})
		},
		"put platform administrator": func() *httptest.ResponseRecorder {
			return harness.api.Put("/system/administrators/"+big, auth, map[string]any{
				"full_administrator": false, "permissions": []string{"tenant_view"},
			})
		},
		"delete platform administrator": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/system/administrators/"+big, auth)
		},
		"effective entity permissions": func() *httptest.ResponseRecorder {
			return harness.api.Get(tenant+"/entities/"+big+"/principals/"+"11"+"/permissions", auth)
		},
		"put relationship subject": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/authorization-relationships", auth, map[string]any{
				"subject_type": "principal", "subject_id": big, "relation": "owner",
				"resource_type": "company", "resource_id": "1",
			})
		},
		"put relationship resource": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/authorization-relationships", auth, map[string]any{
				"subject_type": "principal", "subject_id": "11", "relation": "owner",
				"resource_type": "company", "resource_id": big,
			})
		},
		"delete relationship": func() *httptest.ResponseRecorder {
			return harness.api.Delete(tenant+
				"/authorization-relationships?subject_type=principal&subject_id="+big+
				"&relation=owner&resource_type=company&resource_id=1", auth)
		},
		"list relationships": func() *httptest.ResponseRecorder {
			return harness.api.Get(tenant+"/authorization-relationships?resource_id="+big, auth)
		},
		"authorization check": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/authorization-checks", auth, map[string]any{
				"entity_id": big, "permission_code": "entity_view", "subject": "11",
			})
		},
		"authorization check subject": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/authorization-checks", auth, map[string]any{
				"entity_id": "1", "permission_code": "entity_view", "subject": big,
			})
		},
		"authorization constraint": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/authorization-constraint-evaluations", auth, map[string]any{
				"permission_code": "entity_view", "subject": big,
			})
		},
		"authorization explanation": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/authorization-explanations", auth, map[string]any{
				"entity_id": big, "permission_code": "entity_view", "subject": "11",
			})
		},
		"authorization explanation subject": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/authorization-explanations", auth, map[string]any{
				"entity_id": "1", "permission_code": "entity_view", "subject": big,
			})
		},
		"authorization explanation resource": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/authorization-explanations", auth, map[string]any{
				"entity_id": "1", "resource_type": "merchant", "resource_id": big,
				"permission_code": "merchant_view", "subject": "11",
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			response := call()
			require.Equal(t, http.StatusUnprocessableEntity, response.Code,
				"%s: %s", name, response.Body.String())
		})
	}
}

// TestIAMHTTPMutationsFailWhenThePolicyRevisionLedgerIsBroken removes the table
// every policy mutation fences itself against. Reads are unaffected, so each
// failure comes from the write path rather than from authorization or routing.
// A mutation that reported success here would have changed a policy no
// evaluator could ever observe, because the revision that invalidates cached
// decisions was never recorded.
func TestIAMHTTPMutationsFailWhenThePolicyRevisionLedgerIsBroken(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	auth, tenant := "Authorization: Bearer test", tenantPathPrefix(1)
	seedHTTPPrincipal(t, harness.repositoryHarness, 12)
	seedDirectoryFixtures(t, harness.repositoryHarness, 1)
	role, err := harness.module.service.CreateRole(t.Context(), 1, "Managers", "")
	require.NoError(t, err)
	entity, err := harness.module.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "company", Name: "Company"})
	require.NoError(t, err)
	menu, err := harness.module.service.CreateMenu(t.Context(), Menu{
		TenantID: 1, Label: "People", Route: "/people", Status: MenuActive,
	})
	require.NoError(t, err)
	_, _, err = harness.module.service.PutRelationship(t.Context(), Relationship{
		TenantID: 1, SubjectType: "principal", SubjectID: 11, Relation: "owner",
		ResourceType: "company", ResourceID: entity.ID,
	})
	require.NoError(t, err)
	roleID, entityID, menuID := role.ID.String(), entity.ID.String(), menu.ID.String()

	require.NoError(t, harness.db.Exec("DROP TABLE sl_iam_policy_revisions").Error)

	// Reads still answer, so a failure below is the write path and not the route.
	for _, path := range []string{
		tenant + "/roles", tenant + "/entities", tenant + "/menus",
		"/roles/" + roleID, "/entities/" + entityID, "/menus/" + menuID,
	} {
		response := harness.api.Get(path, auth)
		require.Equal(t, http.StatusOK, response.Code, "%s: %s", path, response.Body.String())
	}

	for name, call := range map[string]func() *httptest.ResponseRecorder{
		"create role": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/roles", auth, map[string]any{"name": "Second"})
		},
		"update role": func() *httptest.ResponseRecorder {
			return harness.api.Patch("/roles/"+roleID, auth, map[string]any{"name": "Renamed"})
		},
		"delete role": func() *httptest.ResponseRecorder { return harness.api.Delete("/roles/"+roleID, auth) },
		"grant role permission": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+roleID+"/permissions/user_view", auth)
		},
		"revoke role permission": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/"+roleID+"/permissions/user_view", auth)
		},
		"set data scope": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+roleID+"/data-scope", auth, map[string]any{"scope": "self"})
		},
		"add role member": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+roleID+"/members/11", auth)
		},
		"remove role member": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/"+roleID+"/members/11", auth)
		},
		"add directory binding": func() *httptest.ResponseRecorder {
			return harness.api.Put("/roles/"+roleID+"/directory-bindings/group/200", auth)
		},
		"remove directory binding": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/roles/"+roleID+"/directory-bindings/group/200", auth)
		},
		"create entity": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/entities", auth, map[string]any{"type": "store", "name": "Store"})
		},
		"update entity": func() *httptest.ResponseRecorder {
			return harness.api.Patch("/entities/"+entityID, auth, map[string]any{"name": "Renamed"})
		},
		"delete entity": func() *httptest.ResponseRecorder { return harness.api.Delete("/entities/"+entityID, auth) },
		"put entity role binding": func() *httptest.ResponseRecorder {
			return harness.api.Put("/entities/"+entityID+"/role-bindings/"+roleID+"/12", auth,
				map[string]any{"effect": "allow"})
		},
		"delete entity role binding": func() *httptest.ResponseRecorder {
			return harness.api.Delete("/entities/"+entityID+"/role-bindings/"+roleID+"/12", auth)
		},
		"create menu": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/menus", auth, map[string]any{
				"label": "Members", "route": "/members", "permission_code": "user_view", "status": "active",
			})
		},
		"update menu": func() *httptest.ResponseRecorder {
			return harness.api.Patch("/menus/"+menuID, auth, map[string]any{"label": "Renamed"})
		},
		"delete menu": func() *httptest.ResponseRecorder { return harness.api.Delete("/menus/"+menuID, auth) },
		"create tenant member": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/members", auth, map[string]any{
				"subject": "12", "display_name": "Second member", "status": "active",
			})
		},
		"update tenant member": func() *httptest.ResponseRecorder {
			return harness.api.Patch(tenant+"/members/11", auth, map[string]any{"display_name": "Renamed"})
		},
		"put relationship": func() *httptest.ResponseRecorder {
			return harness.api.Post(tenant+"/authorization-relationships", auth, map[string]any{
				"subject_type": "principal", "subject_id": "12", "relation": "owner",
				"resource_type": "company", "resource_id": entityID,
			})
		},
		"delete relationship": func() *httptest.ResponseRecorder {
			return harness.api.Delete(tenant+
				"/authorization-relationships?subject_type=principal&subject_id=11&relation=owner&resource_type=company&resource_id="+
				entityID, auth)
		},
	} {
		t.Run(name, func(t *testing.T) {
			response := call()
			require.Equal(t, http.StatusInternalServerError, response.Code, "%s: %s", name, response.Body.String())
		})
	}

	// Nothing the refused mutations touched was left changed.
	stored, err := harness.module.service.GetRole(t.Context(), 1, role.ID)
	require.NoError(t, err)
	require.Equal(t, "Managers", stored.Name)
	permissions, err := harness.module.service.ListPermissions(t.Context(), 1, role.ID)
	require.NoError(t, err)
	require.Empty(t, permissions)
	storedEntity, err := harness.module.service.GetEntity(t.Context(), 1, entity.ID)
	require.NoError(t, err)
	require.Equal(t, "Company", storedEntity.Name)
	storedMenu, err := harness.module.service.GetMenu(t.Context(), 1, menu.ID)
	require.NoError(t, err)
	require.Equal(t, "People", storedMenu.Label)
}
