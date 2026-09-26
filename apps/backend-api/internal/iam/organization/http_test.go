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

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	organizationi18n "7link.com/platform/internal/iam/organization/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const organizationRouteManifest = `
organization-accept-invitation|POST|/invitation-acceptances
organization-create-department|POST|/tenants/{tenant_id}/departments
organization-create-group|POST|/tenants/{tenant_id}/groups
organization-create-invitation|POST|/tenants/{tenant_id}/invitations
organization-create-position|POST|/tenants/{tenant_id}/positions
organization-create-tenant|POST|/tenants
organization-delete-department|DELETE|/departments/{department_id}
organization-delete-group|DELETE|/groups/{group_id}
organization-delete-group-member|DELETE|/groups/{group_id}/members/{principal_id}
organization-delete-position|DELETE|/positions/{position_id}
organization-delete-position-member|DELETE|/positions/{position_id}/members/{principal_id}
organization-delete-tenant|DELETE|/tenants/{tenant_id}
organization-get-department|GET|/departments/{department_id}
organization-get-group|GET|/groups/{group_id}
organization-get-position|GET|/positions/{position_id}
organization-get-tenant|GET|/tenants/{tenant_id}
organization-list-departments|GET|/tenants/{tenant_id}/departments
organization-list-group-members|GET|/groups/{group_id}/members
organization-list-groups|GET|/tenants/{tenant_id}/groups
organization-list-invitations|GET|/tenants/{tenant_id}/invitations
organization-list-position-members|GET|/positions/{position_id}/members
organization-list-positions|GET|/tenants/{tenant_id}/positions
organization-list-tenants|GET|/tenants
organization-my-tenants|GET|/principals/{principal_id}/tenants
organization-put-group-member|PUT|/groups/{group_id}/members/{principal_id}
organization-put-position-member|PUT|/positions/{position_id}/members/{principal_id}
organization-resend-invitation|POST|/invitations/{invitation_id}/deliveries
organization-revoke-invitation|DELETE|/invitations/{invitation_id}
organization-update-department|PATCH|/departments/{department_id}
organization-update-group|PATCH|/groups/{group_id}
organization-update-position|PATCH|/positions/{position_id}
organization-update-tenant|PATCH|/tenants/{tenant_id}`

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
	h := newOrganizationHarness(t, "openapi_")
	members := &organizationMemberChecker{prefix: h.prefix}
	creator := identity.InvitationPrincipalCreator(func(context.Context, *gorm.DB, guid.ID, string, string, string, string) (guid.ID, error) {
		return 900, nil
	})
	instance := NewModule(h.runtime, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, organizationTestRegistrar(members)))
	require.NoError(t, module.Provide[core.ActiveMemberChecker](services, members))
	require.NoError(t, module.Provide[core.AdministratorGuard](services, &organizationAdministratorGuard{}))
	require.NoError(t, module.Provide[authn.InvitationCredentials](services, &invitationCredentialFixture{}))
	require.NoError(t, module.Provide[identity.InvitationPrincipalCreator](services, creator))
	require.NoError(t, module.Provide[core.TenantBootstrapper](services, newTenantBootstrapStub().capability()))
	require.NoError(t, module.Provide[core.DirectoryAuthorizationSourceConnector](services, core.DirectoryAuthorizationSourceConnector{
		Bind: func(core.DirectoryAuthorizationSourceStore) error { return nil },
	}))
	require.NoError(t, module.Provide[core.DirectoryTableNamesConnector](services, core.DirectoryTableNamesConnector{
		Bind: func(core.DirectoryTableNames) error { return nil },
	}))
	require.NoError(t, module.Provide[identity.TenantOwnerLookup](services, newTenantOwnerStub().capability()))
	require.NoError(t, module.Provide[authn.DirectoryTableNamesConnector](services, authn.DirectoryTableNamesConnector{
		Bind: func(authn.DirectoryTableNames) error { return nil },
	}))
	require.NoError(t, module.Provide[identity.DirectoryTableNamesConnector](services, identity.DirectoryTableNamesConnector{
		Bind: func(identity.DirectoryTableNames) error { return nil },
	}))
	require.NoError(t, module.Provide[core.AccessTableNames](services, core.PublishedAccessTableNames(h.runtime.TablePrefix())))
	require.NoError(t, module.Provide[guid.Source](services, newOrganizationTestIDs(9000)))
	require.NoError(t, module.Provide[audit.Appender](services, h.appender))
	require.NoError(t, services.Seal())
	require.NoError(t, instance.InjectServices(services))
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, instance.Initialize(t.Context()))
	respx.Install()
	_, api := humatest.New(t)
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
			// Organization publishes the directory record, plus the invitation
			// a new member redeems before they have one.
			assert.Contains(t, []string{"directory", "authentication", "account"},
				operation.Tags[0], location)
			if guard, ok := organizationGuards[operation.OperationID]; ok {
				assert.Equal(t, guard, operation.Extensions[authz.GuardExtensionKey], location)
				assert.NotEmpty(t, operation.Security, location)
			}
			platform := strings.Contains(operation.OperationID, "tenant") && operation.OperationID != "organization-my-tenants"
			assert.Equal(t, platform, authz.IsPlatformOperation(operation), location)
		}
	}
	accept := spec.Paths["/invitation-acceptances"].Post
	require.NotNil(t, accept)
	assert.Empty(t, accept.Security)
	assert.NotContains(t, accept.Extensions, authz.GuardExtensionKey)
	assert.NotEmpty(t, spec.Paths["/principals/{principal_id}/tenants"].Get.Security)

	createGroup := spec.Paths["/tenants/{tenant_id}/groups"].Post
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
	ctx := authnext.WithClaims(t.Context(), &authnext.Claims{TenantID: 1})
	_, _, err = organizationResourceIDs(ctx, "bad")
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
	tenants, _, _ := newTenantServiceForTest(t, h, newOrganizationTestIDs(1000))
	positions := newBoundPositionService(t, h.runtime, h.appender, members, guard, newOrganizationTestIDs(300))
	groups := newBoundGroupService(t, h.runtime, h.appender, members, guard, newOrganizationTestIDs(400))
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
	invitations := newBoundInvitationService(t, h.runtime, h.appender, newOrganizationTestIDs(600), credentials, creator)

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
	tenant := "/tenants/1"

	department := h.api.Post(tenant+"/departments", auth, map[string]any{"entity_id": "10", "name": "Engineering"})
	assert.Equal(t, http.StatusCreated, department.Code, department.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get(tenant+"/departments", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/departments/101", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Patch("/departments/101", auth, map[string]any{
		"name": "Platform", "version": 1,
	}).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/departments/101?version=2", auth).Code)

	position := h.api.Post(tenant+"/positions", auth, map[string]any{"code": "engineer", "name": "Engineer"})
	assert.Equal(t, http.StatusCreated, position.Code, position.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get(tenant+"/positions", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/positions/301", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Patch("/positions/301", auth, map[string]any{
		"name": "Senior Engineer", "version": 1,
	}).Code)
	assert.Equal(t, http.StatusOK, h.api.Put("/positions/301/members/10", auth, map[string]any{}).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/positions/301/members", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/positions/301/members/10", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/positions/301?version=2", auth).Code)

	group := h.api.Post(tenant+"/groups", auth, map[string]any{"name": "Operators"})
	assert.Equal(t, http.StatusCreated, group.Code, group.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get(tenant+"/groups", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/groups/401", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Patch("/groups/401", auth, map[string]any{
		"description": "Tenant operators", "version": 1,
	}).Code)
	assert.Equal(t, http.StatusOK, h.api.Put("/groups/401/members/10", auth, map[string]any{}).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/groups/401/members", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/groups/401/members/10", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/groups/401?version=2", auth).Code)

	assert.Equal(t, http.StatusOK, h.api.Get("/principals/"+"1"+"/tenants", auth).Code)
	createdTenant := h.api.Post("/tenants", auth, map[string]any{
		"slug": "new-tenant", "name": "New Tenant",
		"owner_principal_id": organizationTestOwnerID.String(),
	})
	assert.Equal(t, http.StatusCreated, createdTenant.Code, createdTenant.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get("/tenants", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/tenants/1001", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Patch("/tenants/1001", auth, map[string]any{
		"name": "Renamed Tenant", "version": 1,
	}).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/tenants/1001?version=2", auth).Code)

	issued := h.api.Post(tenant+"/invitations", auth, map[string]any{
		"email": "invitee@example.com", "expires_in_hours": 72,
	})
	assert.Equal(t, http.StatusCreated, issued.Code, issued.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get(tenant+"/invitations", auth).Code)
	resent := h.api.Post("/invitations/601/deliveries", auth, map[string]any{"expires_in_hours": 48})
	assert.Equal(t, http.StatusOK, resent.Code, resent.Body.String())
	resentToken := fmt.Sprintf("inv1_%d.%s-%d", 601, strings.Repeat("s", 40), 2)
	accepted := h.api.Post("/invitation-acceptances", map[string]any{
		"token": resentToken, "login_name": "invitee", "password": "correct horse battery staple",
		"display_name": "Invitee",
	})
	assert.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	second := h.api.Post(tenant+"/invitations", auth, map[string]any{
		"email": "second@example.com", "expires_in_hours": 72,
	})
	assert.Equal(t, http.StatusCreated, second.Code, second.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Delete("/invitations/602", auth).Code)
}

func TestTenantAdministrationRequiresPlatformPermission(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	require.NoError(t, organizationi18n.Register())
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 2, TenantActive)
	h.seedMember(t, 2, 1, "Caller")
	service, _, _ := newTenantServiceForTest(t, h, newOrganizationTestIDs(1500))
	registrar := authz.NewRegistrar(
		authz.DefaultRegistry(), organizationHTTPTokenVerifier{},
		organizationDenyPlatformChecker{}, organizationHTTPMembershipChecker{},
	)
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("organization-platform-test", "1.0.0"))
	registerTenantHTTP(api, service, registrar)
	client := humatest.Wrap(t, api)
	auth := "Authorization: Bearer test"

	assert.Equal(t, http.StatusOK, client.Get("/principals/"+"1"+"/tenants", auth).Code)
	for _, response := range []*httptest.ResponseRecorder{
		client.Get("/tenants", auth),
		client.Post("/tenants", auth, map[string]any{"slug": "denied", "name": "Denied"}),
		client.Get("/tenants/2", auth),
		client.Patch("/tenants/2", auth, map[string]any{"name": "Denied", "version": 1}),
		client.Delete("/tenants/2?version=1", auth),
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
	tenant := "/tenants/1"

	invalidReferenceRequests := []*httptest.ResponseRecorder{
		h.api.Get("/departments/bad", auth),
		h.api.Patch("/departments/bad", auth, map[string]any{"name": "Name", "version": 1}),
		h.api.Delete("/departments/bad?version=1", auth),
		h.api.Get("/positions/bad", auth),
		h.api.Patch("/positions/bad", auth, map[string]any{"name": "Name", "version": 1}),
		h.api.Delete("/positions/bad?version=1", auth),
		h.api.Get("/positions/bad/members", auth),
		h.api.Get("/groups/bad", auth),
		h.api.Patch("/groups/bad", auth, map[string]any{"name": "Name", "version": 1}),
		h.api.Delete("/groups/bad?version=1", auth),
		h.api.Get("/groups/bad/members", auth),
		h.api.Get("/tenants/bad", auth),
		h.api.Patch("/tenants/bad", auth, map[string]any{"name": "Name", "version": 1}),
		h.api.Delete("/tenants/bad?version=1", auth),
		h.api.Post("/invitations/bad/deliveries", auth, map[string]any{"expires_in_hours": 72}),
		h.api.Delete("/invitations/bad", auth),
	}
	for _, response := range invalidReferenceRequests {
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	}
	for _, response := range []*httptest.ResponseRecorder{
		h.api.Put("/positions/1/members/bad", auth, map[string]any{}),
		h.api.Delete("/positions/1/members/bad", auth),
		h.api.Put("/groups/1/members/bad", auth, map[string]any{}),
		h.api.Delete("/groups/1/members/bad", auth),
	} {
		assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String(), "a missing parent resource takes precedence over its child selector")
	}

	sqlDB, err := h.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	failingRequests := []*httptest.ResponseRecorder{
		h.api.Get(tenant+"/departments", auth),
		h.api.Post(tenant+"/departments", auth, map[string]any{"name": "Department"}),
		h.api.Get("/departments/1", auth),
		h.api.Patch("/departments/1", auth, map[string]any{"name": "Department", "version": 1}),
		h.api.Delete("/departments/1?version=1", auth),
		h.api.Get(tenant+"/positions", auth),
		h.api.Post(tenant+"/positions", auth, map[string]any{"code": "position", "name": "Position"}),
		h.api.Get("/positions/1", auth),
		h.api.Patch("/positions/1", auth, map[string]any{"name": "Position", "version": 1}),
		h.api.Delete("/positions/1?version=1", auth),
		h.api.Get("/positions/1/members", auth),
		h.api.Put("/positions/1/members/10", auth, map[string]any{}),
		h.api.Delete("/positions/1/members/10", auth),
		h.api.Get(tenant+"/groups", auth),
		h.api.Post(tenant+"/groups", auth, map[string]any{"name": "Group"}),
		h.api.Get("/groups/1", auth),
		h.api.Patch("/groups/1", auth, map[string]any{"name": "Group", "version": 1}),
		h.api.Delete("/groups/1?version=1", auth),
		h.api.Get("/groups/1/members", auth),
		h.api.Put("/groups/1/members/10", auth, map[string]any{}),
		h.api.Delete("/groups/1/members/10", auth),
		h.api.Get("/tenants", auth),
		h.api.Post("/tenants", auth, map[string]any{"slug": "tenant-new", "name": "Tenant"}),
		h.api.Get("/tenants/1", auth),
		h.api.Patch("/tenants/1", auth, map[string]any{"name": "Tenant", "version": 1}),
		h.api.Delete("/tenants/1?version=1", auth),
		h.api.Get("/principals/"+"1"+"/tenants", auth),
		h.api.Get(tenant+"/invitations", auth),
		h.api.Post(tenant+"/invitations", auth, map[string]any{
			"email": "user@example.com", "expires_in_hours": 72,
		}),
		h.api.Post("/invitations/1/deliveries", auth, map[string]any{"expires_in_hours": 72}),
		h.api.Delete("/invitations/1", auth),
		h.api.Post("/invitation-acceptances", map[string]any{
			"token":      "inv1_1." + strings.Repeat("s", 40),
			"login_name": "user", "password": "correct horse battery staple",
		}),
	}
	for _, response := range failingRequests {
		assert.GreaterOrEqual(t, response.Code, http.StatusBadRequest, response.Body.String())
	}
}
