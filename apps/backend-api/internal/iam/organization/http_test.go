package organization

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	organizationi18n "7link.com/platform/internal/iam/organization/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const organizationRouteManifest = `
organization-accept-invitation|POST|/iam/invitations/accept
organization-create-department|POST|/iam/departments
organization-create-group|POST|/iam/groups
organization-create-invitation|POST|/iam/invitations
organization-create-position|POST|/iam/positions
organization-create-tenant|POST|/iam/tenants
organization-delete-department|DELETE|/iam/departments/{id}
organization-delete-group|DELETE|/iam/groups/{id}
organization-delete-group-member|DELETE|/iam/groups/{id}/members/{principal_id}
organization-delete-position|DELETE|/iam/positions/{id}
organization-delete-position-member|DELETE|/iam/positions/{id}/members/{principal_id}
organization-delete-tenant|DELETE|/iam/tenants/{tenant_id}
organization-get-department|GET|/iam/departments/{id}
organization-get-group|GET|/iam/groups/{id}
organization-get-position|GET|/iam/positions/{id}
organization-get-tenant|GET|/iam/tenants/{tenant_id}
organization-list-departments|GET|/iam/departments
organization-list-group-members|GET|/iam/groups/{id}/members
organization-list-groups|GET|/iam/groups
organization-list-invitations|GET|/iam/invitations
organization-list-position-members|GET|/iam/positions/{id}/members
organization-list-positions|GET|/iam/positions
organization-list-tenants|GET|/iam/tenants
organization-my-tenants|GET|/iam/me/tenants
organization-put-group-member|PUT|/iam/groups/{id}/members/{principal_id}
organization-put-position-member|PUT|/iam/positions/{id}/members/{principal_id}
organization-resend-invitation|POST|/iam/invitations/{id}/resend
organization-revoke-invitation|DELETE|/iam/invitations/{id}
organization-update-department|PATCH|/iam/departments/{id}
organization-update-group|PATCH|/iam/groups/{id}
organization-update-position|PATCH|/iam/positions/{id}
organization-update-tenant|PATCH|/iam/tenants/{tenant_id}`

var organizationGuards = map[string]string{
	"organization-list-departments":       "dept_view",
	"organization-create-department":      "dept_create",
	"organization-get-department":         "dept_view",
	"organization-update-department":      "dept_update",
	"organization-delete-department":      "dept_delete",
	"organization-list-positions":         "position_view",
	"organization-create-position":        "position_create",
	"organization-get-position":           "position_view",
	"organization-update-position":        "position_update",
	"organization-delete-position":        "position_delete",
	"organization-list-position-members":  "position_view",
	"organization-put-position-member":    "position_manage_member",
	"organization-delete-position-member": "position_manage_member",
	"organization-list-groups":            "group_view",
	"organization-create-group":           "group_create",
	"organization-get-group":              "group_view",
	"organization-update-group":           "group_update",
	"organization-delete-group":           "group_delete",
	"organization-list-group-members":     "group_view",
	"organization-put-group-member":       "group_manage_member",
	"organization-delete-group-member":    "group_manage_member",
	"organization-list-tenants":           "tenant_view",
	"organization-create-tenant":          "tenant_create",
	"organization-get-tenant":             "tenant_view",
	"organization-update-tenant":          "tenant_update",
	"organization-delete-tenant":          "tenant_delete",
	"organization-list-invitations":       "invitation_view",
	"organization-create-invitation":      "invitation_create",
	"organization-revoke-invitation":      "invitation_revoke",
	"organization-resend-invitation":      "invitation_resend",
}

func organizationDeclarationAPI(t *testing.T) huma.API {
	t.Helper()
	respx.Install()
	_, api := humatest.New(t)
	instance := NewDeclarationOnlyModule(authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	require.NoError(t, instance.RegisterHTTP(t.Context(), api))
	return api
}

func organizationPathOperations(item *huma.PathItem) map[string]*huma.Operation {
	if item == nil {
		return nil
	}
	return map[string]*huma.Operation{
		http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
		http.MethodPatch: item.Patch, http.MethodDelete: item.Delete, http.MethodHead: item.Head,
		http.MethodOptions: item.Options, http.MethodTrace: item.Trace,
	}
}

func organizationOperationManifest(spec *huma.OpenAPI) []string {
	manifest := make([]string, 0, 32)
	for path, item := range spec.Paths {
		for method, operation := range organizationPathOperations(item) {
			if operation != nil {
				manifest = append(manifest, operation.OperationID+"|"+method+"|"+path)
			}
		}
	}
	return manifest
}

func TestOrganizationRouteManifestMatchesGeneratedOpenAPI(t *testing.T) {
	api := organizationDeclarationAPI(t)
	want, got := strings.Fields(organizationRouteManifest), organizationOperationManifest(api.OpenAPI())
	sort.Strings(want)
	sort.Strings(got)
	require.Equal(t, want, got)
	require.Len(t, got, 32)
	require.NoError(t, authz.ValidateOperations(api, authz.DefaultRegistry()))
}

func TestOrganizationOpenAPIGuardsScopesAndSchemas(t *testing.T) {
	spec := organizationDeclarationAPI(t).OpenAPI()
	for path, item := range spec.Paths {
		for method, operation := range organizationPathOperations(item) {
			if operation == nil {
				continue
			}
			location := method + " " + path
			assert.Equal(t, []string{"organization"}, operation.Tags, location)
			if guard, ok := organizationGuards[operation.OperationID]; ok {
				assert.Equal(t, guard, operation.Extensions[authz.GuardExtensionKey], location)
				assert.NotEmpty(t, operation.Security, location)
			}
			platform := strings.Contains(operation.OperationID, "tenant") && operation.OperationID != "organization-my-tenants"
			assert.Equal(t, platform, authz.IsPlatformOperation(operation), location)
		}
	}
	accept := spec.Paths["/iam/invitations/accept"].Post
	require.NotNil(t, accept)
	assert.Empty(t, accept.Security)
	assert.NotContains(t, accept.Extensions, authz.GuardExtensionKey)
	assert.NotEmpty(t, spec.Paths["/iam/me/tenants"].Get.Security)

	createGroup := spec.Paths["/iam/groups"].Post
	schema := createGroup.RequestBody.Content["application/json"].Schema
	if schema.Ref != "" {
		schema = spec.Components.Schemas.SchemaFromRef(schema.Ref)
	}
	require.NotNil(t, schema)
	rule := schema.Properties["membership_rule"]
	if rule.Ref != "" {
		rule = spec.Components.Schemas.SchemaFromRef(rule.Ref)
	}
	require.NotNil(t, rule)
	assert.Equal(t, huma.TypeObject, rule.Type)
	assert.Equal(t, []string{"version", "match", "conditions"}, rule.Required)
}

func TestOrganizationErrorMappings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mapErr func(error) error
		err    error
		status int
		key    string
	}{
		{"department missing", departmentError, ErrNotFound, 404, organizationi18n.DepartmentNotFound},
		{"department duplicate", departmentError, ErrNameConflict, 409, organizationi18n.DepartmentNameExists},
		{"department cycle", departmentError, ErrHierarchyCycle, 409, organizationi18n.DepartmentHierarchyCycle},
		{"department children", departmentError, ErrHasChildren, 409, organizationi18n.DepartmentHasChildren},
		{"department used", departmentError, ErrInUse, 409, organizationi18n.DepartmentInUse},
		{"department version", departmentError, ErrVersionConflict, 409, organizationi18n.DepartmentVersionConflict},
		{"department invalid", departmentError, ErrInvalid, 422, organizationi18n.InvalidDepartment},
		{"position missing", positionError, ErrPositionNotFound, 404, organizationi18n.PositionNotFound},
		{"position duplicate", positionError, ErrPositionCodeConflict, 409, organizationi18n.PositionCodeExists},
		{"position version", positionError, ErrPositionVersionConflict, 409, organizationi18n.PositionVersionConflict},
		{"position members", positionError, ErrPositionHasMembers, 409, organizationi18n.PositionHasMembers},
		{"position role", positionError, ErrPositionRoleBound, 409, organizationi18n.PositionRoleBound},
		{"position relationship", positionError, ErrPositionRelationshipBound, 409, organizationi18n.PositionRelationshipBound},
		{"position inactive", positionError, ErrPositionMemberInactive, 409, organizationi18n.PositionMemberInactive},
		{"position last admin", positionError, core.ErrLastTenantAdministrator, 409, organizationi18n.LastTenantAdministrator},
		{"position invalid", positionError, ErrPositionInvalid, 422, organizationi18n.InvalidPosition},
		{"group missing", groupError, ErrGroupNotFound, 404, organizationi18n.GroupNotFound},
		{"group duplicate", groupError, ErrGroupNameConflict, 409, organizationi18n.GroupNameExists},
		{"group version", groupError, ErrGroupVersionConflict, 409, organizationi18n.GroupVersionConflict},
		{"group members", groupError, ErrGroupHasMembers, 409, organizationi18n.GroupHasMembers},
		{"group role", groupError, ErrGroupRoleBound, 409, organizationi18n.GroupRoleBound},
		{"group relationship", groupError, ErrGroupRelationshipBound, 409, organizationi18n.GroupRelationshipBound},
		{"group inactive", groupError, ErrGroupMemberInactive, 409, organizationi18n.GroupMemberInactive},
		{"group dynamic", groupError, ErrDynamicGroupMembers, 409, organizationi18n.DynamicGroupMembersComputed},
		{"group rule type", groupError, ErrGroupRuleType, 422, organizationi18n.GroupRuleRequiresDynamic},
		{"group rule invalid", groupError, ErrGroupRuleInvalid, 422, organizationi18n.InvalidGroupRule},
		{"group last admin", groupError, core.ErrLastTenantAdministrator, 409, organizationi18n.LastTenantAdministrator},
		{"group invalid", groupError, ErrGroupInvalid, 422, organizationi18n.InvalidGroup},
		{"tenant missing", tenantError, ErrTenantNotFound, 404, organizationi18n.TenantNotFound},
		{"tenant duplicate", tenantError, ErrTenantSlugConflict, 409, organizationi18n.TenantSlugExists},
		{"tenant version", tenantError, ErrTenantVersionConflict, 409, organizationi18n.TenantVersionConflict},
		{"tenant invalid", tenantError, ErrInvalidTenant, 422, organizationi18n.InvalidTenant},
		{"invitation missing", invitationError, ErrInvitationNotFound, 404, organizationi18n.InvitationNotFound},
		{"invitation credential", invitationError, ErrInvitationCredential, 400, organizationi18n.InvalidInvitationCredential},
		{"invitation expired", invitationError, ErrInvitationExpired, 410, organizationi18n.InvitationExpired},
		{"invitation state", invitationError, ErrInvitationState, 409, organizationi18n.InvitationStateConflict},
		{"invitation login", invitationError, ErrInvitationLoginConflict, 409, organizationi18n.InvitationLoginNameExists},
		{"invitation binding", invitationError, ErrInvitationBindingMissing, 404, organizationi18n.InvitationBindingNotFound},
		{"invitation inactive", invitationError, ErrInvitationBindingInactive, 409, organizationi18n.InvitationBindingInactive},
		{"invitation invalid", invitationError, ErrInvitationInvalid, 422, organizationi18n.InvalidInvitation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var status huma.StatusError
			require.ErrorAs(t, test.mapErr(fmt.Errorf("wrapped: %w", test.err)), &status)
			assert.Equal(t, test.status, status.GetStatus())
			assert.Contains(t, status.Error(), test.key)
		})
	}
	for _, mapper := range []func(error) error{departmentError, positionError, groupError, tenantError, invitationError} {
		var status huma.StatusError
		require.ErrorAs(t, mapper(errors.New("storage unavailable")), &status)
		assert.Equal(t, http.StatusInternalServerError, status.GetStatus())
		assert.Contains(t, status.Error(), organizationi18n.Unavailable)
	}
}

func TestParseOrganizationIDs(t *testing.T) {
	t.Parallel()
	id, err := parseOrganizationID(" 42 ")
	require.NoError(t, err)
	assert.EqualValues(t, 42, id)
	optional, err := parseOptionalOrganizationID(" ")
	require.NoError(t, err)
	assert.Zero(t, optional)
	_, err = parseOrganizationID("invalid")
	assert.Error(t, err)
	_, _, err = organizationResourceIDs("1", "bad")
	assert.Error(t, err)
}

type organizationHTTPMembershipChecker struct{}

func (organizationHTTPMembershipChecker) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

type organizationHTTPHarness struct {
	*organizationHarness
	api humatest.TestAPI
}

func newOrganizationHTTPHarness(t *testing.T) organizationHTTPHarness {
	t.Helper()
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	require.NoError(t, organizationi18n.Register())
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	h.seedTenant(t, 2, TenantActive)
	h.seedMember(t, 1, 1, "Administrator")
	h.seedMember(t, 1, 10, "Alice")
	members := &organizationMemberChecker{prefix: h.prefix}
	guard := &organizationAdministratorGuard{}
	departments := h.service
	tenants := NewTenantService(h.runtime, h.appender, newOrganizationTestIDs(1000))
	positions := NewPositionService(h.runtime, h.appender, members, guard, newOrganizationTestIDs(300))
	groups := NewGroupService(h.runtime, h.appender, members, guard, newOrganizationTestIDs(400))
	credentials := &invitationCredentialFixture{}
	creator := identity.InvitationPrincipalCreator(func(
		ctx context.Context, db *gorm.DB, tenantID guid.ID, _, _, displayName, email string,
	) (guid.ID, error) {
		const principalID guid.ID = 900
		err := db.WithContext(ctx).Table(h.table("iam_tenant_members")).Create(map[string]any{
			"tenant_id": tenantID, "principal_id": principalID, "display_name": displayName,
			"email": email, "status": StatusActive,
			"created_at": organizationTestNow, "updated_at": organizationTestNow,
		}).Error
		return principalID, err
	})
	invitations := NewInvitationService(h.runtime, h.appender, newOrganizationTestIDs(600), credentials, creator)

	registrar := authz.NewRegistrar(
		authz.DefaultRegistry(),
		organizationHTTPTokenVerifier{},
		organizationPermissionChecker{},
		organizationHTTPMembershipChecker{},
	)
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("organization-test", "1.0.0"))
	RegisterHTTP(api, departments, tenants, positions, groups, invitations, registrar)
	return organizationHTTPHarness{organizationHarness: h, api: humatest.Wrap(t, api)}
}

type organizationHTTPTokenVerifier struct{}

func (organizationHTTPTokenVerifier) Authenticate(context.Context, string, string) (*authnext.Claims, error) {
	return &authnext.Claims{
		Subject: "1", SubjectType: authnext.SubjectTypePrincipal, PrincipalID: 1,
		ACR: 1, AMR: []string{"password"},
	}, nil
}

type organizationDenyPlatformChecker struct{ organizationPermissionChecker }

func (organizationDenyPlatformChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return false, nil
}

func TestOrganizationHTTPLifecycleExercisesEveryOperation(t *testing.T) {
	h := newOrganizationHTTPHarness(t)
	auth := "Authorization: Bearer test"
	tenant := authz.TenantHeader + ": 1"

	department := h.api.Post("/iam/departments", auth, tenant, map[string]any{"name": "Engineering"})
	assert.Equal(t, http.StatusCreated, department.Code, department.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get("/iam/departments", auth, tenant).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/iam/departments/101", auth, tenant).Code)
	assert.Equal(t, http.StatusOK, h.api.Patch("/iam/departments/101", auth, tenant, map[string]any{
		"name": "Platform", "version": 1,
	}).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/iam/departments/101?version=2", auth, tenant).Code)

	position := h.api.Post("/iam/positions", auth, tenant, map[string]any{"code": "engineer", "name": "Engineer"})
	assert.Equal(t, http.StatusCreated, position.Code, position.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get("/iam/positions", auth, tenant).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/iam/positions/301", auth, tenant).Code)
	assert.Equal(t, http.StatusOK, h.api.Patch("/iam/positions/301", auth, tenant, map[string]any{
		"name": "Senior Engineer", "version": 1,
	}).Code)
	assert.Equal(t, http.StatusOK, h.api.Put("/iam/positions/301/members/10", auth, tenant, map[string]any{}).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/iam/positions/301/members", auth, tenant).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/iam/positions/301/members/10", auth, tenant).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/iam/positions/301?version=2", auth, tenant).Code)

	group := h.api.Post("/iam/groups", auth, tenant, map[string]any{"name": "Operators"})
	assert.Equal(t, http.StatusCreated, group.Code, group.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get("/iam/groups", auth, tenant).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/iam/groups/401", auth, tenant).Code)
	assert.Equal(t, http.StatusOK, h.api.Patch("/iam/groups/401", auth, tenant, map[string]any{
		"description": "Tenant operators", "version": 1,
	}).Code)
	assert.Equal(t, http.StatusOK, h.api.Put("/iam/groups/401/members/10", auth, tenant, map[string]any{}).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/iam/groups/401/members", auth, tenant).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/iam/groups/401/members/10", auth, tenant).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/iam/groups/401?version=2", auth, tenant).Code)

	assert.Equal(t, http.StatusOK, h.api.Get("/iam/me/tenants", auth).Code)
	createdTenant := h.api.Post("/iam/tenants", auth, map[string]any{"slug": "new-tenant", "name": "New Tenant"})
	assert.Equal(t, http.StatusCreated, createdTenant.Code, createdTenant.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get("/iam/tenants", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/iam/tenants/1001", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Patch("/iam/tenants/1001", auth, map[string]any{
		"name": "Renamed Tenant", "version": 1,
	}).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/iam/tenants/1001?version=2", auth).Code)

	issued := h.api.Post("/iam/invitations", auth, tenant, map[string]any{
		"email": "invitee@example.com", "expires_in_hours": 72,
	})
	assert.Equal(t, http.StatusCreated, issued.Code, issued.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get("/iam/invitations", auth, tenant).Code)
	resent := h.api.Post("/iam/invitations/601/resend", auth, tenant, map[string]any{"expires_in_hours": 48})
	assert.Equal(t, http.StatusOK, resent.Code, resent.Body.String())
	resentToken := fmt.Sprintf("inv1_%d.%s-%d", 601, strings.Repeat("s", 40), 2)
	accepted := h.api.Post("/iam/invitations/accept", map[string]any{
		"token": resentToken, "login_name": "invitee", "password": "correct horse battery staple",
		"display_name": "Invitee",
	})
	assert.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	second := h.api.Post("/iam/invitations", auth, tenant, map[string]any{
		"email": "second@example.com", "expires_in_hours": 72,
	})
	assert.Equal(t, http.StatusCreated, second.Code, second.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Delete("/iam/invitations/602", auth, tenant).Code)
}

func TestTenantAdministrationRequiresPlatformPermission(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	require.NoError(t, organizationi18n.Register())
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 2, TenantActive)
	h.seedMember(t, 2, 1, "Caller")
	service := NewTenantService(h.runtime, h.appender, newOrganizationTestIDs(1500))
	registrar := authz.NewRegistrar(
		authz.DefaultRegistry(), organizationHTTPTokenVerifier{},
		organizationDenyPlatformChecker{}, organizationHTTPMembershipChecker{},
	)
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("organization-platform-test", "1.0.0"))
	registerTenantHTTP(api, service, registrar)
	client := humatest.Wrap(t, api)
	auth := "Authorization: Bearer test"

	assert.Equal(t, http.StatusOK, client.Get("/iam/me/tenants", auth).Code)
	for _, response := range []*httptest.ResponseRecorder{
		client.Get("/iam/tenants", auth),
		client.Post("/iam/tenants", auth, map[string]any{"slug": "denied", "name": "Denied"}),
		client.Get("/iam/tenants/2", auth),
		client.Patch("/iam/tenants/2", auth, map[string]any{"name": "Denied", "version": 1}),
		client.Delete("/iam/tenants/2?version=1", auth),
	} {
		assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
	stored, err := service.Get(t.Context(), 2)
	require.NoError(t, err)
	assert.Equal(t, "Tenant 2", stored.Name)
	var count int64
	require.NoError(t, h.db.Table(h.table("iam_tenants")).Where("slug = ?", "denied").Count(&count).Error)
	assert.Zero(t, count)
}

func TestOrganizationHTTPRejectsInvalidReferencesAndStorageFailures(t *testing.T) {
	h := newOrganizationHTTPHarness(t)
	auth := "Authorization: Bearer test"
	tenant := authz.TenantHeader + ": 1"

	invalidReferenceRequests := []*httptest.ResponseRecorder{
		h.api.Get("/iam/departments/bad", auth, tenant),
		h.api.Patch("/iam/departments/bad", auth, tenant, map[string]any{"name": "Name", "version": 1}),
		h.api.Delete("/iam/departments/bad?version=1", auth, tenant),
		h.api.Get("/iam/positions/bad", auth, tenant),
		h.api.Patch("/iam/positions/bad", auth, tenant, map[string]any{"name": "Name", "version": 1}),
		h.api.Delete("/iam/positions/bad?version=1", auth, tenant),
		h.api.Get("/iam/positions/bad/members", auth, tenant),
		h.api.Put("/iam/positions/1/members/bad", auth, tenant, map[string]any{}),
		h.api.Delete("/iam/positions/1/members/bad", auth, tenant),
		h.api.Get("/iam/groups/bad", auth, tenant),
		h.api.Patch("/iam/groups/bad", auth, tenant, map[string]any{"name": "Name", "version": 1}),
		h.api.Delete("/iam/groups/bad?version=1", auth, tenant),
		h.api.Get("/iam/groups/bad/members", auth, tenant),
		h.api.Put("/iam/groups/1/members/bad", auth, tenant, map[string]any{}),
		h.api.Delete("/iam/groups/1/members/bad", auth, tenant),
		h.api.Get("/iam/tenants/bad", auth),
		h.api.Patch("/iam/tenants/bad", auth, map[string]any{"name": "Name", "version": 1}),
		h.api.Delete("/iam/tenants/bad?version=1", auth),
		h.api.Post("/iam/invitations/bad/resend", auth, tenant, map[string]any{"expires_in_hours": 72}),
		h.api.Delete("/iam/invitations/bad", auth, tenant),
	}
	for _, response := range invalidReferenceRequests {
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	}

	sqlDB, err := h.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	failingRequests := []*httptest.ResponseRecorder{
		h.api.Get("/iam/departments", auth, tenant),
		h.api.Post("/iam/departments", auth, tenant, map[string]any{"name": "Department"}),
		h.api.Get("/iam/departments/1", auth, tenant),
		h.api.Patch("/iam/departments/1", auth, tenant, map[string]any{"name": "Department", "version": 1}),
		h.api.Delete("/iam/departments/1?version=1", auth, tenant),
		h.api.Get("/iam/positions", auth, tenant),
		h.api.Post("/iam/positions", auth, tenant, map[string]any{"code": "position", "name": "Position"}),
		h.api.Get("/iam/positions/1", auth, tenant),
		h.api.Patch("/iam/positions/1", auth, tenant, map[string]any{"name": "Position", "version": 1}),
		h.api.Delete("/iam/positions/1?version=1", auth, tenant),
		h.api.Get("/iam/positions/1/members", auth, tenant),
		h.api.Put("/iam/positions/1/members/10", auth, tenant, map[string]any{}),
		h.api.Delete("/iam/positions/1/members/10", auth, tenant),
		h.api.Get("/iam/groups", auth, tenant),
		h.api.Post("/iam/groups", auth, tenant, map[string]any{"name": "Group"}),
		h.api.Get("/iam/groups/1", auth, tenant),
		h.api.Patch("/iam/groups/1", auth, tenant, map[string]any{"name": "Group", "version": 1}),
		h.api.Delete("/iam/groups/1?version=1", auth, tenant),
		h.api.Get("/iam/groups/1/members", auth, tenant),
		h.api.Put("/iam/groups/1/members/10", auth, tenant, map[string]any{}),
		h.api.Delete("/iam/groups/1/members/10", auth, tenant),
		h.api.Get("/iam/tenants", auth),
		h.api.Post("/iam/tenants", auth, map[string]any{"slug": "tenant-new", "name": "Tenant"}),
		h.api.Get("/iam/tenants/1", auth),
		h.api.Patch("/iam/tenants/1", auth, map[string]any{"name": "Tenant", "version": 1}),
		h.api.Delete("/iam/tenants/1?version=1", auth),
		h.api.Get("/iam/me/tenants", auth),
		h.api.Get("/iam/invitations", auth, tenant),
		h.api.Post("/iam/invitations", auth, tenant, map[string]any{
			"email": "user@example.com", "expires_in_hours": 72,
		}),
		h.api.Post("/iam/invitations/1/resend", auth, tenant, map[string]any{"expires_in_hours": 72}),
		h.api.Delete("/iam/invitations/1", auth, tenant),
		h.api.Post("/iam/invitations/accept", map[string]any{
			"token":      "inv1_1." + strings.Repeat("s", 40),
			"login_name": "user", "password": "correct horse battery staple",
		}),
	}
	for _, response := range failingRequests {
		assert.GreaterOrEqual(t, response.Code, http.StatusBadRequest, response.Body.String())
	}
}
