package organization

import (
	"context"
	"encoding/json"
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
	corei18n "7link.com/platform/internal/iam/core/i18n"
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
organization-system-departments|GET|/system/departments
organization-system-department-create|POST|/system/departments
organization-system-department|GET|/system/departments/{department_id}
organization-system-department-update|PATCH|/system/departments/{department_id}
organization-system-department-delete|DELETE|/system/departments/{department_id}
organization-system-tenant-departments|GET|/system/tenants/{owner_id}/departments
organization-system-platform-departments|GET|/system/operating-platforms/{owner_id}/departments
organization-platform-departments|GET|/operating-platforms/{platform_id}/departments
organization-platform-department-create|POST|/operating-platforms/{platform_id}/departments
organization-platform-groups|GET|/operating-platforms/{platform_id}/principal-groups
organization-platform-group-create|POST|/operating-platforms/{platform_id}/principal-groups
organization-accept-invitation|POST|/membership-invitations/acceptances
organization-join-invitation|POST|/membership-invitations/join
organization-create-department|POST|/tenants/{tenant_id}/departments
organization-create-group|POST|/tenants/{tenant_id}/principal-groups
organization-create-invitation|POST|/tenants/{tenant_id}/membership-invitations
organization-create-tenant|POST|/tenants
organization-create-operating-platform|POST|/operating-platforms
organization-delete-department|DELETE|/departments/{department_id}
organization-delete-group|DELETE|/principal-groups/{group_id}
organization-delete-group-member|DELETE|/principal-groups/{group_id}/members/{principal_id}
organization-delete-tenant|DELETE|/tenants/{tenant_id}
organization-delete-operating-platform|DELETE|/operating-platforms/{platform_id}
organization-get-department|GET|/departments/{department_id}
organization-get-group|GET|/principal-groups/{group_id}
organization-get-tenant|GET|/tenants/{tenant_id}
organization-get-operating-platform|GET|/operating-platforms/{platform_id}
organization-list-departments|GET|/tenants/{tenant_id}/departments
organization-list-group-members|GET|/principal-groups/{group_id}/members
organization-list-groups|GET|/tenants/{tenant_id}/principal-groups
organization-list-invitations|GET|/tenants/{tenant_id}/membership-invitations
organization-list-tenants|GET|/tenants
organization-list-operating-platforms|GET|/operating-platforms
organization-my-tenants|GET|/principals/{principal_id}/tenants
organization-tenant-registration-policy|GET|/tenant-registrations/options
organization-register-tenant|POST|/tenant-registrations
organization-my-operating-platforms|GET|/principals/{principal_id}/operating-platforms
organization-put-group-member|PUT|/principal-groups/{group_id}/members/{principal_id}
organization-resend-invitation|POST|/membership-invitations/{invitation_id}/deliveries
organization-revoke-invitation|DELETE|/membership-invitations/{invitation_id}
organization-update-department|PATCH|/departments/{department_id}
organization-update-group|PATCH|/principal-groups/{group_id}
organization-update-tenant|PATCH|/tenants/{tenant_id}
organization-update-operating-platform|PATCH|/operating-platforms/{platform_id}`

var organizationGuards = map[string]string{
	"organization-platform-departments":        "dept_view",
	"organization-system-departments":          "system_department_view",
	"organization-system-department-create":    "system_department_create",
	"organization-system-department":           "system_department_view",
	"organization-system-department-update":    "system_department_update",
	"organization-system-department-delete":    "system_department_delete",
	"organization-system-tenant-departments":   "system_account_view",
	"organization-system-platform-departments": "system_account_view",
	"organization-platform-department-create":  "dept_create",
	"organization-platform-groups":             "group_view",
	"organization-platform-group-create":       "group_create",
	"organization-list-departments":            "dept_view",
	"organization-create-department":           "dept_create",
	"organization-get-department":              "dept_view",
	"organization-update-department":           "dept_update",
	"organization-delete-department":           "dept_delete",

	"organization-list-groups":               "group_view",
	"organization-create-group":              "group_create",
	"organization-get-group":                 "group_view",
	"organization-update-group":              "group_update",
	"organization-delete-group":              "group_delete",
	"organization-list-group-members":        "group_view",
	"organization-put-group-member":          "group_manage_member",
	"organization-delete-group-member":       "group_manage_member",
	"organization-list-tenants":              "tenant_view",
	"organization-create-tenant":             "tenant_create",
	"organization-get-tenant":                "tenant_view",
	"organization-update-tenant":             "tenant_update",
	"organization-delete-tenant":             "tenant_delete",
	"organization-list-operating-platforms":  "operating_platform_view",
	"organization-create-operating-platform": "operating_platform_create",
	"organization-get-operating-platform":    "operating_platform_view",
	"organization-update-operating-platform": "operating_platform_update",
	"organization-delete-operating-platform": "operating_platform_delete",
	"organization-list-invitations":          "invitation_view",
	"organization-create-invitation":         "invitation_create",
	"organization-revoke-invitation":         "invitation_revoke",
	"organization-resend-invitation":         "invitation_resend",
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
	require.NoError(t, module.Provide[core.TenantConstraint](services, allDepartmentsConstraint))
	require.NoError(t, module.Provide[authn.InvitationCredentials](services, &invitationCredentialFixture{}))
	require.NoError(t, module.Provide[identity.InvitationPrincipalCreator](services, creator))
	require.NoError(t, module.Provide[identity.InvitationMemberAcceptor](services, func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }))
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
	require.Len(t, got, 44)
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
			assert.Contains(t, []string{"directory", "authentication", "account", "operating-platforms", "system", "tenant-registrations"},
				operation.Tags[0], location)
			if guard, ok := organizationGuards[operation.OperationID]; ok {
				assert.Equal(t, guard, operation.Extensions[authz.GuardExtensionKey], location)
				assert.NotEmpty(t, operation.Security, location)
			}
			platform := strings.HasPrefix(operation.OperationID, "organization-system-") || (strings.Contains(operation.OperationID, "tenant") && operation.OperationID != "organization-my-tenants" && operation.OperationID != "organization-tenant-registration-policy" && operation.OperationID != "organization-register-tenant") || (strings.Contains(operation.OperationID, "operating-platform") && operation.OperationID != "organization-my-operating-platforms")
			assert.Equal(t, platform, authz.IsPlatformOperation(operation), location)
			assert.Equal(t, strings.HasPrefix(operation.OperationID, "organization-platform-"), authz.IsOperatingPlatformOperation(operation), location)
		}
	}
	accept := spec.Paths["/membership-invitations/acceptances"].Post
	require.NotNil(t, accept)
	assert.Empty(t, accept.Security)
	assert.NotContains(t, accept.Extensions, authz.GuardExtensionKey)
	assert.NotEmpty(t, spec.Paths["/principals/{principal_id}/tenants"].Get.Security)

	createGroup := spec.Paths["/tenants/{tenant_id}/principal-groups"].Post
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
		{"group last admin", groupError, core.ErrLastTenantAdministrator, 409, corei18n.LastTenantAdministrator},
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
		{"invitation escalation", invitationError, core.ErrPrivilegeEscalation, 403, corei18n.PrivilegeEscalationDenied},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var status huma.StatusError
			require.ErrorAs(t, test.mapErr(fmt.Errorf("wrapped: %w", test.err)), &status)
			assert.Equal(t, test.status, status.GetStatus())
			assert.Contains(t, status.Error(), test.key)
		})
	}
	for _, mapper := range []func(error) error{departmentError, groupError, tenantError, invitationError} {
		var status huma.StatusError
		require.ErrorAs(t, mapper(errors.New("storage unavailable")), &status)
		assert.Equal(t, http.StatusInternalServerError, status.GetStatus())
		assert.Contains(t, status.Error(), organizationi18n.Unavailable)
	}
}

func TestParseOrganizationIDs(t *testing.T) {
	t.Parallel()
	id, err := guid.RequireID(" 42 ")
	require.NoError(t, err)
	assert.EqualValues(t, 42, id)
	optional, err := guid.OptionalID(" ")
	require.NoError(t, err)
	assert.Zero(t, optional)
	_, err = guid.RequireID("invalid")
	assert.Error(t, err)
	ctx := authnext.WithClaims(t.Context(), &authnext.Claims{TenantID: 1})
	_, _, err = organizationResourceIDs(ctx, "bad")
	assert.Error(t, err)
}

type organizationHTTPMembershipChecker struct{}

func (organizationHTTPMembershipChecker) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

func (organizationHTTPMembershipChecker) IsOperatingPlatformMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
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
	RegisterHTTP(api, departments, tenants, groups, invitations, registrar)
	return organizationHTTPHarness{organizationHarness: h, api: humatest.Wrap(t, api)}
}

type organizationHTTPTokenVerifier struct{}

func TestTenantSelfRegistrationIsDisabledByDefault(t *testing.T) {
	h := newOrganizationHTTPHarness(t)
	options := h.api.Get("/tenant-registrations/options", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, options.Code)
	assert.Contains(t, options.Body.String(), `"enabled":false`)
	created := h.api.Post("/tenant-registrations", "Authorization: Bearer test", map[string]any{"slug": "disabled-registration", "name": "Disabled"})
	require.Equal(t, http.StatusForbidden, created.Code)
}

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
	for _, path := range []string{"/positions/1", "/job-positions/1", tenant + "/job-positions"} {
		assert.Equal(t, http.StatusNotFound, h.api.Get(path, auth).Code, path)
		assert.Equal(t, http.StatusNotFound, h.api.Post(path, auth, map[string]any{}).Code, path)
	}

	department := h.api.Post(tenant+"/departments", auth, map[string]any{"entity_id": "10", "name": "Engineering"})
	assert.Equal(t, http.StatusCreated, department.Code, department.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get(tenant+"/departments", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/departments/101", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Patch("/departments/101", auth, map[string]any{
		"name": "Platform", "version": 1,
	}).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/departments/101?version=2", auth).Code)

	group := h.api.Post(tenant+"/principal-groups", auth, map[string]any{"name": "Operators"})
	assert.Equal(t, http.StatusCreated, group.Code, group.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get(tenant+"/principal-groups", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/principal-groups/401", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Patch("/principal-groups/401", auth, map[string]any{
		"description": "Tenant operators", "version": 1,
	}).Code)
	assert.Equal(t, http.StatusOK, h.api.Put("/principal-groups/401/members/10", auth, map[string]any{}).Code)
	assert.Equal(t, http.StatusOK, h.api.Get("/principal-groups/401/members", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/principal-groups/401/members/10", auth).Code)
	assert.Equal(t, http.StatusOK, h.api.Delete("/principal-groups/401?version=2", auth).Code)

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

	issued := h.api.Post(tenant+"/membership-invitations", auth, map[string]any{
		"email": "invitee@example.com", "expires_in_hours": 72,
	})
	assert.Equal(t, http.StatusCreated, issued.Code, issued.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Get(tenant+"/membership-invitations", auth).Code)
	resent := h.api.Post("/membership-invitations/601/deliveries", auth, map[string]any{"expires_in_hours": 48})
	assert.Equal(t, http.StatusOK, resent.Code, resent.Body.String())
	resentToken := fmt.Sprintf("inv1_%d.%s-%d", 601, strings.Repeat("s", 40), 2)
	accepted := h.api.Post("/membership-invitations/acceptances", map[string]any{
		"token": resentToken, "login_name": "invitee", "password": "correct horse battery staple",
		"display_name": "Invitee",
	})
	assert.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	second := h.api.Post(tenant+"/membership-invitations", auth, map[string]any{
		"email": "second@example.com", "expires_in_hours": 72,
	})
	assert.Equal(t, http.StatusCreated, second.Code, second.Body.String())
	assert.Equal(t, http.StatusOK, h.api.Delete("/membership-invitations/602", auth).Code)
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
		h.api.Get("/principal-groups/bad", auth),
		h.api.Patch("/principal-groups/bad", auth, map[string]any{"name": "Name", "version": 1}),
		h.api.Delete("/principal-groups/bad?version=1", auth),
		h.api.Get("/principal-groups/bad/members", auth),
		h.api.Get("/tenants/bad", auth),
		h.api.Patch("/tenants/bad", auth, map[string]any{"name": "Name", "version": 1}),
		h.api.Delete("/tenants/bad?version=1", auth),
		h.api.Post("/membership-invitations/bad/deliveries", auth, map[string]any{"expires_in_hours": 72}),
		h.api.Delete("/membership-invitations/bad", auth),
	}
	for _, response := range invalidReferenceRequests {
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	}
	for _, response := range []*httptest.ResponseRecorder{
		h.api.Put("/principal-groups/1/members/bad", auth, map[string]any{}),
		h.api.Delete("/principal-groups/1/members/bad", auth),
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
		h.api.Get(tenant+"/principal-groups", auth),
		h.api.Post(tenant+"/principal-groups", auth, map[string]any{"name": "Group"}),
		h.api.Get("/principal-groups/1", auth),
		h.api.Patch("/principal-groups/1", auth, map[string]any{"name": "Group", "version": 1}),
		h.api.Delete("/principal-groups/1?version=1", auth),
		h.api.Get("/principal-groups/1/members", auth),
		h.api.Put("/principal-groups/1/members/10", auth, map[string]any{}),
		h.api.Delete("/principal-groups/1/members/10", auth),
		h.api.Get("/tenants", auth),
		h.api.Post("/tenants", auth, map[string]any{"slug": "tenant-new", "name": "Tenant"}),
		h.api.Get("/tenants/1", auth),
		h.api.Patch("/tenants/1", auth, map[string]any{"name": "Tenant", "version": 1}),
		h.api.Delete("/tenants/1?version=1", auth),
		h.api.Get("/principals/"+"1"+"/tenants", auth),
		h.api.Get(tenant+"/membership-invitations", auth),
		h.api.Post(tenant+"/membership-invitations", auth, map[string]any{
			"email": "user@example.com", "expires_in_hours": 72,
		}),
		h.api.Post("/membership-invitations/1/deliveries", auth, map[string]any{"expires_in_hours": 72}),
		h.api.Delete("/membership-invitations/1", auth),
		h.api.Post("/membership-invitations/acceptances", map[string]any{
			"token":      "inv1_1." + strings.Repeat("s", 40),
			"login_name": "user", "password": "correct horse battery staple",
		}),
	}
	for _, response := range failingRequests {
		assert.GreaterOrEqual(t, response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestOrganizationHTTPMapsDirectoryDomainErrorsWithoutWriting(t *testing.T) {
	h := newOrganizationHTTPHarness(t)
	auth := "Authorization: Bearer test"
	departments := func() []Department {
		items, err := h.service.List(t.Context(), 1)
		require.NoError(t, err)
		return items
	}

	for _, body := range []map[string]any{
		{"entity_id": "bad", "name": "Engineering"},
		{"entity_id": "10", "parent_id": "bad", "name": "Engineering"},
	} {
		response := h.api.Post("/tenants/1/departments", auth, body)
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	}
	assert.Equal(t, http.StatusNotFound, h.api.Post("/tenants/1/departments", auth, map[string]any{"entity_id": "20", "name": "Foreign entity"}).Code, "an entity of another tenant is not found")
	assert.Empty(t, departments())

	created := func(body map[string]any) Department {
		response := h.api.Post("/tenants/1/departments", auth, body)
		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		var envelope struct {
			Data Department `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		return envelope.Data
	}
	root := created(map[string]any{"entity_id": "10", "name": "Engineering"})
	child := created(map[string]any{"entity_id": "10", "parent_id": root.ID.String(), "name": "Platform"})
	rootPath := "/departments/" + root.ID.String()
	assert.Equal(t, http.StatusConflict, h.api.Post("/tenants/1/departments", auth, map[string]any{"entity_id": "10", "name": "engineering"}).Code, "sibling names are unique case-insensitively")
	assert.Equal(t, http.StatusUnprocessableEntity, h.api.Patch(rootPath, auth, map[string]any{"parent_id": "bad", "version": 1}).Code)
	assert.Equal(t, http.StatusConflict, h.api.Patch(rootPath, auth, map[string]any{"parent_id": child.ID.String(), "version": 1}).Code, "a department cannot move under its descendant")
	assert.Equal(t, http.StatusConflict, h.api.Patch(rootPath, auth, map[string]any{"name": "Stale", "version": 9}).Code)
	assert.Equal(t, http.StatusConflict, h.api.Delete(rootPath+"?version=1", auth).Code, "a department with children cannot be deleted")
	stored := departments()
	require.Len(t, stored, 2)
	for _, item := range stored {
		assert.Equal(t, int64(1), item.Version, "rejected writes must leave %s untouched", item.Name)
		if item.ID == root.ID {
			assert.Equal(t, "Engineering", item.Name)
			assert.Zero(t, item.ParentID)
		}
	}

	require.Equal(t, http.StatusCreated, h.api.Post("/tenants/1/principal-groups", auth, map[string]any{"name": "Operators"}).Code)
	assert.Equal(t, http.StatusConflict, h.api.Post("/tenants/1/principal-groups", auth, map[string]any{"name": "OPERATORS"}).Code)
	assert.Equal(t, http.StatusConflict, h.api.Patch("/principal-groups/401", auth, map[string]any{"description": "Stale", "version": 9}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, h.api.Put("/principal-groups/401/members/bad", auth, map[string]any{}).Code)
	assert.Equal(t, http.StatusConflict, h.api.Put("/principal-groups/401/members/2", auth, map[string]any{}).Code, "only active tenant members can join a group")
	require.Equal(t, http.StatusOK, h.api.Put("/principal-groups/401/members/10", auth, map[string]any{}).Code)
	assert.Equal(t, http.StatusConflict, h.api.Delete("/principal-groups/401?version=1", auth).Code, "a group with members cannot be deleted")
	assert.Equal(t, http.StatusUnprocessableEntity, h.api.Delete("/principal-groups/401/members/bad", auth).Code)

	var groups []groupRow
	require.NoError(t, h.db.Table(h.table("iam_groups")).Where("tenant_id = 1").Find(&groups).Error)
	require.Len(t, groups, 1)
	assert.Equal(t, "Operators", groups[0].Name)
	assert.Empty(t, groups[0].Description)
	assert.Equal(t, int64(1), groups[0].Version)
	var members int64
	require.NoError(t, h.db.Table(h.table("iam_group_members")).Where("tenant_id = 1 AND group_id = 401").Count(&members).Error)
	assert.Equal(t, int64(1), members)
}

// TestErrorMappersKeepChosenStatus is this package's half of the set-wide rule in
// tests/architecture (domainErrorMappers): an error that already chose its
// status, headers included, leaves every mapper unchanged instead of becoming 500.
func TestErrorMappersKeepChosenStatus(t *testing.T) {
	chosen := huma.ErrorWithHeaders(huma.Error422UnprocessableEntity(respx.InvalidID), http.Header{"Retry-After": {"1"}})
	for name, mapper := range map[string]func(error) error{
		"departmentError": departmentError,
		"groupError":      groupError,
		"invitationError": invitationError,
		"tenantError":     tenantError,
	} {
		mapped := mapper(chosen)
		var status huma.StatusError
		require.ErrorAs(t, mapped, &status, name)
		assert.Equal(t, http.StatusUnprocessableEntity, status.GetStatus(), name)
		var headers huma.HeadersError
		require.ErrorAs(t, mapped, &headers, name)
		assert.Equal(t, "1", headers.GetHeaders().Get("Retry-After"), name)
	}
}

// allDepartmentsConstraint stands in for the authorizer where a test is about
// something other than narrowing. AllowAll is what a tenant administrator's
// dept_view compiles to, so it keeps every other listing assertion unchanged.
func allDepartmentsConstraint(
	context.Context, guid.ID, string, guid.ID,
) (authz.DataConstraint, error) {
	return authz.DataConstraint{AllowAll: true}, nil
}
