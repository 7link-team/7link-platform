package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	return &authn.Claims{PrincipalID: verifier.principalID, ACR: 1, AMR: []string{"password"}}, nil
}

type httpRouteAuthorizer struct{ platformAllowed bool }

func (httpRouteAuthorizer) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (authorizer httpRouteAuthorizer) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return authorizer.platformAllowed, nil
}

type httpMembershipChecker struct{}

func (httpMembershipChecker) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

type httpAuthorizationEvaluator struct{}

func (httpAuthorizationEvaluator) CheckBulk(_ context.Context, _ guid.ID, permissions []string, _ guid.ID) (map[string]bool, error) {
	result := make(map[string]bool, len(permissions))
	for _, permission := range permissions {
		result[permission] = true
	}
	return result, nil
}

func (httpAuthorizationEvaluator) Constraint(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{AllowAll: true, OwnerIDs: []string{}, ResourceIDs: []string{}, DepartmentIDs: []string{}, Ancestors: []authz.ResourceRef{}, DeniedIDs: []string{}}, nil
}

func (httpAuthorizationEvaluator) ExplainEntity(_ context.Context, _, entityID guid.ID, _ string, _ guid.ID) (authz.Explanation, error) {
	return authz.Explanation{Allowed: true, Reason: "test_policy", Matches: []authz.DecisionMatch{{ScopeID: entityID.String()}}}, nil
}

func (httpAuthorizationEvaluator) ExplainResource(_ context.Context, _, entityID guid.ID, resourceType string, resourceID guid.ID, _ string, _ guid.ID) (authz.Explanation, error) {
	return authz.Explanation{Allowed: true, Reason: "test_policy", Matches: []authz.DecisionMatch{{ScopeID: entityID.String(), SourceType: resourceType, SourceID: resourceID.String()}}}, nil
}

type httpFailingAuthorizationEvaluator struct{ err error }

func (e httpFailingAuthorizationEvaluator) CheckBulk(context.Context, guid.ID, []string, guid.ID) (map[string]bool, error) {
	return nil, e.err
}

func (e httpFailingAuthorizationEvaluator) Constraint(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
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

	registry := authz.DefaultRegistry()
	registrar := authz.NewRegistrar(
		registry,
		httpTokenVerifier{principalID: actorID},
		httpRouteAuthorizer{platformAllowed: platformAllowed},
		httpMembershipChecker{},
	)
	appender := func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }
	module := NewModule(database.runtime, registrar, httpAuthorizationEvaluator{})
	services := modulepkg.NewServices()
	require.NoError(t, modulepkg.Provide[guid.Source](services, database.repository.ids))
	require.NoError(t, modulepkg.Provide[audit.Appender](services, appender))
	require.NoError(t, services.Seal())
	require.NoError(t, module.InjectServices(services))
	_, err := module.service.PutTenantMember(t.Context(), 1, actorID, "Operator", "operator@example.test", 0, MemberActive)
	require.NoError(t, err)

	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("iam-test", "1.0.0"))
	require.NoError(t, module.RegisterHTTP(t.Context(), api))
	return httpIAMHarness{repositoryHarness: database, module: module, api: humatest.Wrap(t, api)}
}

func tenantHeader(tenantID guid.ID) string {
	return authz.TenantHeader + ": " + tenantID.String()
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
	headerA := tenantHeader(1)

	for _, path := range []string{
		"/iam/permission-catalog", "/iam/menu-catalog", "/iam/entities", "/iam/roles",
		"/iam/members?limit=50", "/iam/menus", "/iam/relationships",
	} {
		response := harness.api.Get(path, "Authorization: Bearer test", headerA)
		assert.Equal(t, http.StatusOK, response.Code, path+": "+response.Body.String())
	}
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/scope-model", "Authorization: Bearer test").Code)

	createdRole := harness.api.Post("/iam/roles", "Authorization: Bearer test", headerA, map[string]any{
		"name": "Managers", "description": "Tenant managers",
	})
	require.Equal(t, http.StatusCreated, createdRole.Code, createdRole.Body.String())
	role := decodeEnvelope[APIRole](t, createdRole).Data
	require.NotEmpty(t, role.ID)
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/roles/"+role.ID, "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Patch("/iam/roles/"+role.ID, "Authorization: Bearer test", headerA, map[string]any{
		"name": "Operators", "description": "Updated operators",
	}).Code)
	assert.Equal(t, http.StatusOK, harness.api.Put("/iam/roles/"+role.ID+"/permissions/user_view", "Authorization: Bearer test", headerA).Code)

	setScope := harness.api.Put("/iam/roles/"+role.ID+"/data-scope", "Authorization: Bearer test", headerA, map[string]any{"scope": "self"})
	require.Equal(t, http.StatusOK, setScope.Code, setScope.Body.String())
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/roles/"+role.ID+"/data-scope", "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/roles/"+role.ID+"/permissions", "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/roles/"+role.ID+"/permission-grants", "Authorization: Bearer test", headerA).Code)
	condition := map[string]any{
		"condition": map[string]any{
			"version": 1,
			"gte":     []any{map[string]any{"context": "auth.acr"}, map[string]any{"value": 1}},
		},
	}
	assert.Equal(t, http.StatusOK, harness.api.Put("/iam/roles/"+role.ID+"/permissions/user_view/condition", "Authorization: Bearer test", headerA, condition).Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/iam/roles/"+role.ID+"/permissions/user_view/condition", "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Put("/iam/roles/"+role.ID+"/members/11", "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/roles/"+role.ID+"/members", "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/members/11/roles", "Authorization: Bearer test", headerA).Code)

	seedDirectoryFixtures(t, harness.repositoryHarness, 1)
	assert.Equal(t, http.StatusOK, harness.api.Put("/iam/roles/"+role.ID+"/directory-bindings/group/200", "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/roles/"+role.ID+"/directory-bindings", "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/iam/roles/"+role.ID+"/directory-bindings/group/200", "Authorization: Bearer test", headerA).Code)

	seedHTTPPrincipal(t, harness.repositoryHarness, 12)
	createdMember := harness.api.Post("/iam/members", "Authorization: Bearer test", headerA, map[string]any{
		"subject": "12", "display_name": "Second member", "department_id": "100", "status": "active",
	})
	require.Equal(t, http.StatusCreated, createdMember.Code, createdMember.Body.String())
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/members/12", "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Patch("/iam/members/12", "Authorization: Bearer test", headerA, map[string]any{
		"display_name": "Updated member", "email": "updated@example.test", "department_id": "100", "status": "active",
	}).Code)

	createdMenu := harness.api.Post("/iam/menus", "Authorization: Bearer test", headerA, map[string]any{
		"label": "Members", "route": "/members", "permission_code": "user_view", "status": "active",
	})
	require.Equal(t, http.StatusCreated, createdMenu.Code, createdMenu.Body.String())
	assert.NotContains(t, createdMenu.Body.String(), `"parent_id":"0"`)
	menuA := decodeEnvelope[APIMenu](t, createdMenu).Data
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/menus/"+menuA.ID, "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Patch("/iam/menus/"+menuA.ID, "Authorization: Bearer test", headerA, map[string]any{
		"parent_id": "", "label": "People", "route": "/people", "icon": "users", "sort_order": 10,
		"permission_code": "user_view", "status": "active",
	}).Code)
	effectiveMenus := harness.api.Get("/iam/me/menus", "Authorization: Bearer test", headerA)
	require.Equal(t, http.StatusOK, effectiveMenus.Code, effectiveMenus.Body.String())
	assert.Contains(t, effectiveMenus.Body.String(), "People")

	createdEntity := harness.api.Post("/iam/entities", "Authorization: Bearer test", headerA, map[string]any{
		"type": "company", "name": "Tenant A company", "metadata": map[string]any{"region": "west"},
	})
	require.Equal(t, http.StatusCreated, createdEntity.Code, createdEntity.Body.String())
	assert.NotContains(t, createdEntity.Body.String(), `"parent_id":"0"`)
	entityA := decodeEnvelope[APIEntity](t, createdEntity).Data
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/entities/"+entityA.ID, "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Patch("/iam/entities/"+entityA.ID, "Authorization: Bearer test", headerA, map[string]any{
		"type": "company", "name": "Tenant A updated", "status": "active", "metadata": map[string]any{"region": "north"},
	}).Code)
	childEntity := harness.api.Post("/iam/entities", "Authorization: Bearer test", headerA, map[string]any{
		"parent_id": entityA.ID, "type": "store", "name": "Tenant A child",
	})
	require.Equal(t, http.StatusCreated, childEntity.Code, childEntity.Body.String())
	childEntityID := decodeEnvelope[APIEntity](t, childEntity).Data.ID
	clearParent := harness.api.Patch("/iam/entities/"+childEntityID, "Authorization: Bearer test", headerA, map[string]any{"parent_id": ""})
	require.Equal(t, http.StatusOK, clearParent.Code, clearParent.Body.String())
	assert.NotContains(t, clearParent.Body.String(), `"parent_id"`)
	expiresAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	assert.Equal(t, http.StatusOK, harness.api.Put("/iam/entities/"+entityA.ID+"/role-bindings/"+role.ID+"/11", "Authorization: Bearer test", headerA, map[string]any{
		"effect": "allow", "expires_at": expiresAt,
	}).Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/entities/"+entityA.ID+"/role-bindings", "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/iam/entities/"+entityA.ID+"/role-bindings/"+role.ID+"/11", "Authorization: Bearer test", headerA).Code)

	startsAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	endsAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	createdRelationship := harness.api.Post("/iam/relationships", "Authorization: Bearer test", headerA, map[string]any{
		"subject_type": "principal", "subject_id": "12", "relation": "owner",
		"resource_type": "company", "resource_id": entityA.ID, "starts_at": startsAt, "ends_at": endsAt,
		"condition": map[string]any{"version": 1, "gte": []any{map[string]any{"context": "auth.acr"}, map[string]any{"value": 1}}},
	})
	require.Equal(t, http.StatusOK, createdRelationship.Code, createdRelationship.Body.String())
	assert.NotContains(t, createdRelationship.Body.String(), `"entity_id":"0"`)
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/relationships?resource_id="+entityA.ID, "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/relationships?entity_id="+entityA.ID+"&resource_type=company", "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Post("/iam/authorization/check", "Authorization: Bearer test", headerA, map[string]any{
		"entity_id": entityA.ID, "permission_code": "entity_view", "subject": "12",
	}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, harness.api.Post("/iam/authorization/check", "Authorization: Bearer test", headerA, map[string]any{
		"entity_id": entityA.ID, "resource_type": "merchant", "permission_code": "merchant_view", "subject": "12",
	}).Code)
	assert.Equal(t, http.StatusOK, harness.api.Post("/iam/authorization/check", "Authorization: Bearer test", headerA, map[string]any{
		"entity_id": entityA.ID, "resource_type": "merchant", "resource_id": "700", "permission_code": "merchant_view", "subject": "12",
	}).Code)
	assert.Equal(t, http.StatusOK, harness.api.Post("/iam/authorization/constraints", "Authorization: Bearer test", headerA, map[string]any{
		"permission_code": "entity_view", "subject": "12",
	}).Code)
	assert.Equal(t, http.StatusOK, harness.api.Post("/iam/authorization/explain", "Authorization: Bearer test", headerA, map[string]any{
		"entity_id": entityA.ID, "permission_code": "entity_view", "subject": "12",
	}).Code)
	assert.Equal(t, http.StatusOK, harness.api.Post("/iam/authorization/explain", "Authorization: Bearer test", headerA, map[string]any{
		"entity_id": entityA.ID, "resource_type": "merchant", "resource_id": "700", "permission_code": "merchant_view", "subject": "12",
	}).Code)
	deleteRelationship := harness.api.Delete(
		"/iam/relationships?subject_type=principal&subject_id=12&relation=owner&resource_type=company&resource_id="+entityA.ID,
		"Authorization: Bearer test", headerA,
	)
	require.Equal(t, http.StatusOK, deleteRelationship.Code, deleteRelationship.Body.String())
	assert.True(t, decodeEnvelope[MutationResult](t, deleteRelationship).Data.Changed)

	seedHTTPPrincipal(t, harness.repositoryHarness, 44)
	_, err := harness.module.service.SetPlatformAdministrator(t.Context(), 11, true, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/platform-permission-catalog", "Authorization: Bearer test").Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/iam/platform-administrators", "Authorization: Bearer test").Code)
	putPlatform := harness.api.Put("/iam/platform-administrators/44", "Authorization: Bearer test", map[string]any{
		"full_administrator": false, "permissions": []string{"tenant_view"},
	})
	require.Equal(t, http.StatusOK, putPlatform.Code, putPlatform.Body.String())
	assert.Equal(t, http.StatusOK, harness.api.Delete("/iam/platform-administrators/44", "Authorization: Bearer test").Code)

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
			return harness.api.Get("/iam/roles/"+roleB.ID.String(), "Authorization: Bearer test", headerA)
		}},
		{name: "update role", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/iam/roles/"+roleB.ID.String(), "Authorization: Bearer test", headerA, map[string]any{"name": "stolen"})
		}},
		{name: "delete role", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/roles/"+roleB.ID.String(), "Authorization: Bearer test", headerA)
		}},
		{name: "grant role", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/iam/roles/"+roleB.ID.String()+"/permissions/user_view", "Authorization: Bearer test", headerA)
		}},
		{name: "get member", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/members/22", "Authorization: Bearer test", headerA)
		}},
		{name: "update member", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/iam/members/22", "Authorization: Bearer test", headerA, map[string]any{"display_name": "stolen"})
		}},
		{name: "get menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/menus/"+menuB.ID.String(), "Authorization: Bearer test", headerA)
		}},
		{name: "update menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/iam/menus/"+menuB.ID.String(), "Authorization: Bearer test", headerA, map[string]any{"label": "stolen"})
		}},
		{name: "delete menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/menus/"+menuB.ID.String(), "Authorization: Bearer test", headerA)
		}},
		{name: "get entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/entities/"+entityB.ID.String(), "Authorization: Bearer test", headerA)
		}},
		{name: "update entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/iam/entities/"+entityB.ID.String(), "Authorization: Bearer test", headerA, map[string]any{"name": "stolen"})
		}},
		{name: "delete entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/entities/"+entityB.ID.String(), "Authorization: Bearer test", headerA)
		}},
	}
	for _, test := range crossTenantCases {
		t.Run(test.name, func(t *testing.T) {
			response := test.call()
			assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), `"message":"`+corei18n.ResourceNotFound+`"`)
		})
	}

	rolesA := harness.api.Get("/iam/roles", "Authorization: Bearer test", headerA)
	require.Equal(t, http.StatusOK, rolesA.Code, rolesA.Body.String())
	assert.NotContains(t, rolesA.Body.String(), roleB.ID.String())
	membersA := harness.api.Get("/iam/members?limit=50", "Authorization: Bearer test", headerA)
	require.Equal(t, http.StatusOK, membersA.Code, membersA.Body.String())
	assert.NotContains(t, membersA.Body.String(), "Tenant B member")
	assert.NotContains(t, membersA.Body.String(), `"department_id":"0"`)
	menusA := harness.api.Get("/iam/menus", "Authorization: Bearer test", headerA)
	require.Equal(t, http.StatusOK, menusA.Code, menusA.Body.String())
	assert.NotContains(t, menusA.Body.String(), menuB.ID.String())
	entitiesA := harness.api.Get("/iam/entities", "Authorization: Bearer test", headerA)
	require.Equal(t, http.StatusOK, entitiesA.Code, entitiesA.Body.String())
	assert.NotContains(t, entitiesA.Body.String(), entityB.ID.String())
	relationshipsA := harness.api.Get("/iam/relationships?resource_id="+entityB.ID.String(), "Authorization: Bearer test", headerA)
	require.Equal(t, http.StatusOK, relationshipsA.Code, relationshipsA.Body.String())
	assert.NotContains(t, relationshipsA.Body.String(), relationshipB.ResourceID.String())

	deleteCrossRelationship := harness.api.Delete(
		"/iam/relationships?subject_type=principal&subject_id=22&relation=owner&resource_type=company&resource_id="+entityB.ID.String(),
		"Authorization: Bearer test", headerA,
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

	assert.Equal(t, http.StatusOK, harness.api.Delete("/iam/menus/"+menuA.ID, "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/iam/entities/"+entityA.ID, "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/iam/entities/"+childEntityID, "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/iam/roles/"+role.ID+"/members/11", "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/iam/roles/"+role.ID+"/permissions/user_view", "Authorization: Bearer test", headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Delete("/iam/roles/"+role.ID, "Authorization: Bearer test", headerA).Code)
}

func TestPlatformRoutesRejectPrincipalWithoutPlatformGrant(t *testing.T) {
	harness := newHTTPIAMHarness(t, false)
	for _, response := range []*httptest.ResponseRecorder{
		harness.api.Get("/iam/platform-permission-catalog", "Authorization: Bearer test"),
		harness.api.Get("/iam/platform-administrators", "Authorization: Bearer test"),
		harness.api.Delete("/iam/platform-administrators/11", "Authorization: Bearer test"),
	} {
		assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}

	tenantRoute := harness.api.Get("/iam/roles", "Authorization: Bearer test", tenantHeader(1))
	assert.Equal(t, http.StatusOK, tenantRoute.Code, tenantRoute.Body.String())
	assert.NotContains(t, tenantRoute.Body.String(), "forbidden")
}

func TestIAMAuthorizationRoutesMapEvaluatorFailure(t *testing.T) {
	harness := newHTTPIAMHarness(t, true)
	harness.module.service.checker = httpFailingAuthorizationEvaluator{err: errors.New("authorization backend unavailable")}
	header := tenantHeader(1)

	for _, response := range []*httptest.ResponseRecorder{
		harness.api.Post("/iam/authorization/constraints", "Authorization: Bearer test", header, map[string]any{
			"permission_code": "merchant_view", "subject": "11",
		}),
		harness.api.Post("/iam/authorization/check", "Authorization: Bearer test", header, map[string]any{
			"entity_id": "1", "permission_code": "merchant_view", "subject": "11",
		}),
		harness.api.Post("/iam/authorization/explain", "Authorization: Bearer test", header, map[string]any{
			"entity_id": "1", "permission_code": "merchant_view", "subject": "11",
		}),
		harness.api.Post("/iam/authorization/explain", "Authorization: Bearer test", header, map[string]any{
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

	header := tenantHeader(1)
	condition := map[string]any{
		"condition": map[string]any{"version": 1, "eq": []any{map[string]any{"context": "auth.acr"}, map[string]any{"value": 1}}},
	}
	for _, test := range []struct {
		name string
		call func() *httptest.ResponseRecorder
	}{
		{name: "list entities", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/entities", "Authorization: Bearer test", header)
		}},
		{name: "create entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Post("/iam/entities", "Authorization: Bearer test", header, map[string]any{"type": "store", "name": "Store"})
		}},
		{name: "get entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/entities/1", "Authorization: Bearer test", header)
		}},
		{name: "update entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/iam/entities/1", "Authorization: Bearer test", header, map[string]any{"name": "Store"})
		}},
		{name: "delete entity", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/entities/1", "Authorization: Bearer test", header)
		}},
		{name: "list entity bindings", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/entities/1/role-bindings", "Authorization: Bearer test", header)
		}},
		{name: "put entity binding", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/iam/entities/1/role-bindings/2/3", "Authorization: Bearer test", header, map[string]any{"effect": "allow"})
		}},
		{name: "delete entity binding", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/entities/1/role-bindings/2/3", "Authorization: Bearer test", header)
		}},
		{name: "list roles", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/roles", "Authorization: Bearer test", header)
		}},
		{name: "create role", call: func() *httptest.ResponseRecorder {
			return harness.api.Post("/iam/roles", "Authorization: Bearer test", header, map[string]any{"name": "Role"})
		}},
		{name: "get role", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/roles/1", "Authorization: Bearer test", header)
		}},
		{name: "get role data scope", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/roles/1/data-scope", "Authorization: Bearer test", header)
		}},
		{name: "set role data scope", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/iam/roles/1/data-scope", "Authorization: Bearer test", header, map[string]any{"scope": "self"})
		}},
		{name: "update role", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/iam/roles/1", "Authorization: Bearer test", header, map[string]any{"name": "Role"})
		}},
		{name: "delete role", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/roles/1", "Authorization: Bearer test", header)
		}},
		{name: "list role permissions", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/roles/1/permissions", "Authorization: Bearer test", header)
		}},
		{name: "list role grants", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/roles/1/permission-grants", "Authorization: Bearer test", header)
		}},
		{name: "grant permission", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/iam/roles/1/permissions/user_view", "Authorization: Bearer test", header)
		}},
		{name: "revoke permission", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/roles/1/permissions/user_view", "Authorization: Bearer test", header)
		}},
		{name: "set permission condition", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/iam/roles/1/permissions/user_view/condition", "Authorization: Bearer test", header, condition)
		}},
		{name: "clear permission condition", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/roles/1/permissions/user_view/condition", "Authorization: Bearer test", header)
		}},
		{name: "list role members", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/roles/1/members", "Authorization: Bearer test", header)
		}},
		{name: "add role member", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/iam/roles/1/members/2", "Authorization: Bearer test", header)
		}},
		{name: "remove role member", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/roles/1/members/2", "Authorization: Bearer test", header)
		}},
		{name: "list directory bindings", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/roles/1/directory-bindings", "Authorization: Bearer test", header)
		}},
		{name: "add directory binding", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/iam/roles/1/directory-bindings/group/2", "Authorization: Bearer test", header)
		}},
		{name: "remove directory binding", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/roles/1/directory-bindings/group/2", "Authorization: Bearer test", header)
		}},
		{name: "list members", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/members?limit=50", "Authorization: Bearer test", header)
		}},
		{name: "create member", call: func() *httptest.ResponseRecorder {
			return harness.api.Post("/iam/members", "Authorization: Bearer test", header, map[string]any{"subject": "2", "display_name": "Member"})
		}},
		{name: "get member", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/members/2", "Authorization: Bearer test", header)
		}},
		{name: "update member", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/iam/members/2", "Authorization: Bearer test", header, map[string]any{"display_name": "Member"})
		}},
		{name: "list member roles", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/members/2/roles", "Authorization: Bearer test", header)
		}},
		{name: "list menus", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/menus", "Authorization: Bearer test", header)
		}},
		{name: "create menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Post("/iam/menus", "Authorization: Bearer test", header, map[string]any{"label": "Menu"})
		}},
		{name: "get menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/menus/1", "Authorization: Bearer test", header)
		}},
		{name: "update menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Patch("/iam/menus/1", "Authorization: Bearer test", header, map[string]any{"label": "Menu"})
		}},
		{name: "delete menu", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/menus/1", "Authorization: Bearer test", header)
		}},
		{name: "effective menus", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/me/menus", "Authorization: Bearer test", header)
		}},
		{name: "list relationships", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/relationships", "Authorization: Bearer test", header)
		}},
		{name: "put relationship", call: func() *httptest.ResponseRecorder {
			return harness.api.Post("/iam/relationships", "Authorization: Bearer test", header, map[string]any{"subject_type": "principal", "subject_id": "2", "relation": "viewer", "resource_type": "store", "resource_id": "1"})
		}},
		{name: "delete relationship", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/relationships?subject_type=principal&subject_id=2&relation=viewer&resource_type=store&resource_id=1", "Authorization: Bearer test", header)
		}},
		{name: "list platform administrators", call: func() *httptest.ResponseRecorder {
			return harness.api.Get("/iam/platform-administrators", "Authorization: Bearer test")
		}},
		{name: "put platform administrator", call: func() *httptest.ResponseRecorder {
			return harness.api.Put("/iam/platform-administrators/2", "Authorization: Bearer test", map[string]any{"full_administrator": false, "permissions": []string{"tenant_view"}})
		}},
		{name: "delete platform administrator", call: func() *httptest.ResponseRecorder {
			return harness.api.Delete("/iam/platform-administrators/2", "Authorization: Bearer test")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := test.call()
			assert.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "database is closed")
			assert.Contains(t, response.Body.String(), `"code":500`)
			assert.Contains(t, response.Body.String(), `"data":null`)
		})
	}
}
