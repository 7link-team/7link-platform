package organization

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"7link.com/platform/internal/iam/core"
	corei18n "7link.com/platform/internal/iam/core/i18n"
	organizationi18n "7link.com/platform/internal/iam/organization/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type departmentResourceInput struct {
	ID string `path:"department_id" maxLength:"128"`
}

type groupResourceInput struct {
	ID string `path:"group_id" maxLength:"128"`
}

// tenantPageSelector selects a tenant for a listing that has to be bounded by
// default. These collections grow with the tenant's data, so the page follows
// the bounded shape core's listTenantMembersInput already publishes -- minimum
// one, maximum two hundred, default fifty -- rather than the opt-in shape,
// where omitting limit returns every row.
type tenantPageSelector struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type groupResourcePageInput struct {
	ID     string `path:"group_id" maxLength:"128"`
	Offset int    `query:"offset" minimum:"0" default:"0"`
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type groupMemberResourceInput struct {
	ID          string `path:"group_id" maxLength:"128"`
	PrincipalID string `path:"principal_id" maxLength:"128"`
}

type deleteDepartmentInput struct {
	ID      string `path:"department_id" maxLength:"128"`
	Version int64  `query:"version" minimum:"1"`
}

type deleteGroupInput struct {
	ID      string `path:"group_id" maxLength:"128"`
	Version int64  `query:"version" minimum:"1"`
}

type createDepartmentInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Body     struct {
		EntityID  string `json:"entity_id" minLength:"1" maxLength:"128"`
		ParentID  string `json:"parent_id,omitempty" maxLength:"128"`
		Name      string `json:"name" minLength:"1" maxLength:"128"`
		Status    string `json:"status,omitempty" enum:"active,disabled" default:"active"`
		SortOrder int    `json:"sort_order,omitempty" minimum:"0" maximum:"1000000"`
	}
}

type updateDepartmentInput struct {
	ID   string `path:"department_id" maxLength:"128"`
	Body struct {
		ParentID  *string `json:"parent_id,omitempty" maxLength:"128"`
		Name      *string `json:"name,omitempty" minLength:"1" maxLength:"128"`
		Status    *string `json:"status,omitempty" enum:"active,disabled"`
		SortOrder *int    `json:"sort_order,omitempty" minimum:"0" maximum:"1000000"`
		Version   int64   `json:"version" minimum:"1"`
	}
}

type groupMembershipInput struct {
	ID          string `path:"group_id" maxLength:"128"`
	PrincipalID string `path:"principal_id" maxLength:"128"`
	Body        struct {
		StartsAt *time.Time `json:"starts_at,omitempty"`
		EndsAt   *time.Time `json:"ends_at,omitempty"`
	}
}

type createGroupInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Body     struct {
		Name        string         `json:"name" minLength:"1" maxLength:"128"`
		Type        string         `json:"type,omitempty" enum:"static,dynamic" default:"static"`
		Rule        MembershipRule `json:"membership_rule,omitempty"`
		Description string         `json:"description,omitempty" maxLength:"1024"`
		Status      string         `json:"status,omitempty" enum:"active,disabled" default:"active"`
		SortOrder   int            `json:"sort_order,omitempty" minimum:"0" maximum:"1000000"`
	}
}

type updateGroupInput struct {
	ID   string `path:"group_id" maxLength:"128"`
	Body struct {
		Name        *string         `json:"name,omitempty" minLength:"1" maxLength:"128"`
		Description *string         `json:"description,omitempty" maxLength:"1024"`
		Status      *string         `json:"status,omitempty" enum:"active,disabled"`
		SortOrder   *int            `json:"sort_order,omitempty" minimum:"0" maximum:"1000000"`
		Rule        *MembershipRule `json:"membership_rule,omitempty"`
		Version     int64           `json:"version" minimum:"1"`
	}
}

// listTenantsInput bounds the platform tenant listing by default, for the same
// reason tenantPageSelector bounds the tenant-scoped ones: the collection grows
// with the deployment, and this one is not even narrowed by a tenant.
type listTenantsInput struct {
	IncludeDeleted bool `query:"include_deleted"`
	Offset         int  `query:"offset" minimum:"0" default:"0"`
	Limit          int  `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type tenantIDInput struct {
	ID string `path:"tenant_id" maxLength:"128"`
}

type createTenantInput struct {
	Body struct {
		Slug string `json:"slug" minLength:"3" maxLength:"63" pattern:"^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$"`
		Name string `json:"name" minLength:"1" maxLength:"128"`
		// OwnerPrincipalID is required: a tenant is created together with the
		// first principal able to administer it, or not at all.
		OwnerPrincipalID string `json:"owner_principal_id" minLength:"1" maxLength:"128"`
	}
}

type updateTenantInput struct {
	ID   string `path:"tenant_id" maxLength:"128"`
	Body struct {
		Name    *string `json:"name,omitempty" minLength:"1" maxLength:"128"`
		Status  *string `json:"status,omitempty" enum:"active,suspended"`
		Version int64   `json:"version" minimum:"1"`
	}
}

type deleteTenantInput struct {
	ID      string `path:"tenant_id" maxLength:"128"`
	Version int64  `query:"version" minimum:"1"`
}

type createInvitationInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Body     struct {
		Email          string   `json:"email" format:"email" maxLength:"320"`
		DepartmentID   string   `json:"department_id,omitempty" maxLength:"128"`
		RoleIDs        []string `json:"role_ids,omitempty" maxItems:"50"`
		ExpiresInHours int      `json:"expires_in_hours,omitempty" minimum:"1" maximum:"720" default:"72"`
	}
}

type resendInvitationInput struct {
	ID   string `path:"invitation_id" maxLength:"128"`
	Body struct {
		ExpiresInHours int `json:"expires_in_hours,omitempty" minimum:"1" maximum:"720" default:"72"`
	}
}

type invitationResourceInput struct {
	ID string `path:"invitation_id" maxLength:"128"`
}

func tenantOwner(tenantID guid.ID, found bool, err error) (authz.ResourceOwner, bool, error) {
	return authz.ResourceOwner{TenantID: tenantID}, found, err
}

func (s *Service) resolveDepartmentOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	ownerID, found, err := s.repo.resolveDepartmentTenant(ctx, id)
	return s.repo.resolveRootOwner(ctx, ownerID, found, err)
}

func (s *GroupService) resolveGroupOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	ownerID, found, err := s.repo.resolveGroupTenant(ctx, id)
	return s.repo.resolveRootOwner(ctx, ownerID, found, err)
}

func (s *InvitationService) resolveInvitationOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	return tenantOwner(s.repo.resolveInvitationTenant(ctx, id))
}

type acceptInvitationInput struct {
	Body struct {
		Token       string `json:"token" minLength:"40" maxLength:"256"`
		LoginName   string `json:"login_name" minLength:"1" maxLength:"200"`
		Password    string `json:"password" minLength:"12" maxLength:"1024"`
		DisplayName string `json:"display_name,omitempty" maxLength:"128"`
	}
}

type deletedResource struct {
	Deleted bool `json:"deleted"`
}

type issuedInvitation struct {
	Invitation Invitation `json:"invitation"`
	Token      string     `json:"token" doc:"single-display bearer credential; never stored in plaintext"`
}

type revokedInvitation struct {
	Revoked bool `json:"revoked"`
}

func (MembershipRule) Schema(huma.Registry) *huma.Schema {
	minItems, maxConditions, maxValues, maxLength := 1, 16, 16, 320
	version := float64(1)
	condition := &huma.Schema{
		Type: huma.TypeObject, AdditionalProperties: false,
		Required: []string{"field", "operator", "values"},
		Properties: map[string]*huma.Schema{
			"field": {Type: huma.TypeString, Enum: []any{
				"member.subject", "member.email", "member.email_domain", "member.department_id", "member.status",
			}},
			"operator": {Type: huma.TypeString, Enum: []any{"in", "not_in"}},
			"values": {
				Type: huma.TypeArray, Items: &huma.Schema{Type: huma.TypeString, MaxLength: &maxLength},
				MinItems: &minItems, MaxItems: &maxValues,
			},
		},
	}
	return &huma.Schema{
		Type:                 huma.TypeObject,
		Description:          "Version 1 dynamic membership rule evaluated against stable tenant-member attributes",
		AdditionalProperties: false, Required: []string{"version", "match", "conditions"},
		Properties: map[string]*huma.Schema{
			"version":    {Type: huma.TypeInteger, Minimum: &version, Maximum: &version},
			"match":      {Type: huma.TypeString, Enum: []any{"all", "any"}},
			"conditions": {Type: huma.TypeArray, Items: condition, MinItems: &minItems, MaxItems: &maxConditions},
		},
	}
}

// RegisterHTTP publishes the frozen organization tenant, directory, and
// invitation operations.
func RegisterHTTP(
	api huma.API,
	departments *Service,
	tenants *TenantService,
	groups *GroupService,
	invitations *InvitationService,
	registrar *authz.Registrar,
) {
	registerDepartmentHTTP(api, departments, registrar)
	registerGroupHTTP(api, groups, registrar)
	registerTenantHTTP(api, tenants, registrar)
	registerInvitationHTTP(api, invitations, registrar)
}

func parseOrganizationID(value string) (guid.ID, error) {
	id, err := guid.Parse(strings.TrimSpace(value))
	if err != nil || id.Zero() {
		return 0, huma.Error422UnprocessableEntity(respx.InvalidID)
	}
	return id, nil
}

func parseOptionalOrganizationID(value string) (guid.ID, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	return parseOrganizationID(value)
}

func organizationResourceIDs(ctx context.Context, idValue string) (guid.ID, guid.ID, error) {
	tenantID := authnext.TenantIDFromContext(ctx)
	id, err := parseOrganizationID(idValue)
	if err != nil {
		return 0, 0, err
	}
	return tenantID, id, nil
}

func registerDepartmentHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.Register(registrar, api, huma.Operation{
		OperationID: "organization-list-departments", Method: http.MethodGet, Path: "/departments",
		Summary: "List the tenant department hierarchy", Tags: []string{"directory"},
		Errors: []int{http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "dept", Verb: "view"},
		func(ctx context.Context, in *tenantPageSelector) (*respx.Body[[]Department], error) {
			tenantID, err := parseOrganizationID(in.TenantID)
			if err != nil {
				return nil, err
			}
			items, total, err := service.ListPage(ctx, tenantID, in.Offset, in.Limit)
			if err != nil {
				return nil, departmentError(err)
			}
			return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "organization-create-department", Method: http.MethodPost, Path: "/departments",
		Summary: "Create a tenant department", Tags: []string{"directory"}, DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "dept", Verb: "create"},
		func(ctx context.Context, in *createDepartmentInput) (*respx.Body[Department], error) {
			tenantID, err := parseOrganizationID(in.TenantID)
			if err != nil {
				return nil, err
			}
			entityID, err := parseOrganizationID(in.Body.EntityID)
			if err != nil {
				return nil, err
			}
			parentID, err := parseOptionalOrganizationID(in.Body.ParentID)
			if err != nil {
				return nil, err
			}
			item, err := service.Create(ctx, tenantID, CreateDepartment{
				EntityID: entityID, ParentID: parentID, Name: in.Body.Name, Status: in.Body.Status, SortOrder: in.Body.SortOrder,
			})
			if err != nil {
				return nil, departmentError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "organization-get-department", Method: http.MethodGet, Path: "/departments/{department_id}",
		Summary: "Get a tenant department", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, "department_id", service.resolveDepartmentOwner, authz.Guard{Resource: "dept", Verb: "view"},
		func(ctx context.Context, in *departmentResourceInput) (*respx.Body[Department], error) {
			tenantID, id, err := organizationResourceIDs(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			item, err := service.Get(ctx, tenantID, id)
			if err != nil {
				return nil, departmentError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "organization-update-department", Method: http.MethodPatch, Path: "/departments/{department_id}",
		Summary: "Update or move a tenant department", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "department_id", service.resolveDepartmentOwner, authz.Guard{Resource: "dept", Verb: "update"},
		func(ctx context.Context, in *updateDepartmentInput) (*respx.Body[Department], error) {
			tenantID, id, err := organizationResourceIDs(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			update := UpdateDepartment{
				Name: in.Body.Name, Status: in.Body.Status, SortOrder: in.Body.SortOrder, Version: in.Body.Version,
			}
			if in.Body.ParentID != nil {
				parentID, err := parseOptionalOrganizationID(*in.Body.ParentID)
				if err != nil {
					return nil, err
				}
				update.ParentID = &parentID
			}
			item, err := service.Update(ctx, tenantID, id, update)
			if err != nil {
				return nil, departmentError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "organization-delete-department", Method: http.MethodDelete, Path: "/departments/{department_id}",
		Summary: "Delete an empty tenant department", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "department_id", service.resolveDepartmentOwner, authz.Guard{Resource: "dept", Verb: "delete"},
		func(ctx context.Context, in *deleteDepartmentInput) (*respx.Body[deletedResource], error) {
			tenantID, id, err := organizationResourceIDs(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if err := service.Delete(ctx, tenantID, id, in.Version); err != nil {
				return nil, departmentError(err)
			}
			return respx.OK(ctx, deletedResource{Deleted: true}), nil
		})
}

func registerGroupHTTP(api huma.API, service *GroupService, registrar *authz.Registrar) {
	authz.Register(registrar, api, huma.Operation{
		OperationID: "organization-list-groups", Method: http.MethodGet, Path: "/principal-groups",
		Summary: "List tenant groups", Tags: []string{"directory"}, Errors: []int{http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "group", Verb: "view"},
		func(ctx context.Context, in *tenantPageSelector) (*respx.Body[[]Group], error) {
			tenantID, err := parseOrganizationID(in.TenantID)
			if err != nil {
				return nil, err
			}
			items, total, err := service.ListPage(ctx, tenantID, in.Offset, in.Limit)
			if err != nil {
				return nil, groupError(err)
			}
			return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "organization-create-group", Method: http.MethodPost, Path: "/principal-groups",
		Summary: "Create a tenant group", Tags: []string{"directory"}, DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "group", Verb: "create"},
		func(ctx context.Context, in *createGroupInput) (*respx.Body[Group], error) {
			tenantID, err := parseOrganizationID(in.TenantID)
			if err != nil {
				return nil, err
			}
			item, err := service.Create(ctx, tenantID, CreateGroup{
				Name: in.Body.Name, Type: in.Body.Type, Rule: in.Body.Rule,
				Description: in.Body.Description, Status: in.Body.Status, SortOrder: in.Body.SortOrder,
			})
			if err != nil {
				return nil, groupError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "organization-get-group", Method: http.MethodGet, Path: "/principal-groups/{group_id}",
		Summary: "Get a tenant group", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, "group_id", service.resolveGroupOwner, authz.Guard{Resource: "group", Verb: "view"},
		func(ctx context.Context, in *groupResourceInput) (*respx.Body[Group], error) {
			tenantID, id, err := organizationResourceIDs(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			item, err := service.Get(ctx, tenantID, id)
			if err != nil {
				return nil, groupError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "organization-update-group", Method: http.MethodPatch, Path: "/principal-groups/{group_id}",
		Summary: "Update a tenant group", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "group_id", service.resolveGroupOwner, authz.Guard{Resource: "group", Verb: "update"},
		func(ctx context.Context, in *updateGroupInput) (*respx.Body[Group], error) {
			tenantID, id, err := organizationResourceIDs(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			item, err := service.Update(ctx, tenantID, id, UpdateGroup{
				Name: in.Body.Name, Description: in.Body.Description, Status: in.Body.Status,
				SortOrder: in.Body.SortOrder, Rule: in.Body.Rule, Version: in.Body.Version,
			})
			if err != nil {
				return nil, groupError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "organization-delete-group", Method: http.MethodDelete, Path: "/principal-groups/{group_id}",
		Summary: "Delete an empty tenant group", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "group_id", service.resolveGroupOwner, authz.Guard{Resource: "group", Verb: "delete"},
		func(ctx context.Context, in *deleteGroupInput) (*respx.Body[deletedResource], error) {
			tenantID, id, err := organizationResourceIDs(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if err := service.Delete(ctx, tenantID, id, in.Version); err != nil {
				return nil, groupError(err)
			}
			return respx.OK(ctx, deletedResource{Deleted: true}), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "organization-list-group-members", Method: http.MethodGet, Path: "/principal-groups/{group_id}/members",
		Summary: "List group members", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, "group_id", service.resolveGroupOwner, authz.Guard{Resource: "group", Verb: "view"},
		func(ctx context.Context, in *groupResourcePageInput) (*respx.Body[[]GroupMember], error) {
			tenantID, id, err := organizationResourceIDs(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			items, total, err := service.ListMembersPage(ctx, tenantID, id, in.Offset, in.Limit)
			if err != nil {
				return nil, groupError(err)
			}
			return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "organization-put-group-member", Method: http.MethodPut,
		Path: "/principal-groups/{group_id}/members/{principal_id}", Summary: "Assign or update a group member",
		Tags: []string{"directory"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "group_id", service.resolveGroupOwner, authz.Guard{Resource: "group", Verb: "manage_member"},
		func(ctx context.Context, in *groupMembershipInput) (*respx.Body[GroupMember], error) {
			tenantID, id, err := organizationResourceIDs(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			principalID, err := parseOrganizationID(in.PrincipalID)
			if err != nil {
				return nil, err
			}
			item, err := service.PutMember(ctx, tenantID, id, principalID, GroupMemberWindow{
				StartsAt: in.Body.StartsAt, EndsAt: in.Body.EndsAt,
			})
			if err != nil {
				return nil, groupError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "organization-delete-group-member", Method: http.MethodDelete,
		Path: "/principal-groups/{group_id}/members/{principal_id}", Summary: "Remove a group member",
		Tags: []string{"directory"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "group_id", service.resolveGroupOwner, authz.Guard{Resource: "group", Verb: "manage_member"},
		func(ctx context.Context, in *groupMemberResourceInput) (*respx.Body[deletedResource], error) {
			tenantID, id, err := organizationResourceIDs(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			principalID, err := parseOrganizationID(in.PrincipalID)
			if err != nil {
				return nil, err
			}
			deleted, err := service.DeleteMember(ctx, tenantID, id, principalID)
			if err != nil {
				return nil, groupError(err)
			}
			return respx.OK(ctx, deletedResource{Deleted: deleted}), nil
		})
}

func registerTenantHTTP(api huma.API, service *TenantService, registrar *authz.Registrar) {
	registerTenantRegistrationHTTP(api, service, registrar)
	authz.RegisterPrincipalAuthenticated(registrar, api, huma.Operation{
		OperationID: "organization-my-tenants", Method: http.MethodGet, Path: "/principals/{principal_id}/tenants",
		Summary: "List the caller's tenants", Tags: []string{"account"},
	}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]Tenant], error) {
		claims, ok := authnext.FromContext(ctx)
		if !ok || claims == nil || claims.PrincipalID.Zero() {
			return nil, huma.Error401Unauthorized(respx.Unauthorized)
		}
		items, err := service.ListByMember(ctx, claims.PrincipalID)
		if err != nil {
			return nil, tenantError(err)
		}
		return respx.OK(ctx, items), nil
	})

	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "organization-list-tenants", Method: http.MethodGet, Path: "/tenants",
		Summary: "List platform tenants", Tags: []string{"directory"},
	}, authz.Guard{Resource: "tenant", Verb: "view"},
		func(ctx context.Context, in *listTenantsInput) (*respx.Body[[]Tenant], error) {
			items, total, err := service.ListPage(ctx, in.IncludeDeleted, in.Offset, in.Limit)
			if err != nil {
				return nil, tenantError(err)
			}
			return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
		})

	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "organization-create-tenant", Method: http.MethodPost, Path: "/tenants",
		Summary: "Create a platform tenant", Tags: []string{"directory"}, DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "tenant", Verb: "create"},
		func(ctx context.Context, in *createTenantInput) (*respx.Body[Tenant], error) {
			owner, err := parseOrganizationID(in.Body.OwnerPrincipalID)
			if err != nil {
				return nil, err
			}
			item, err := service.Create(ctx, CreateTenant{
				Slug: in.Body.Slug, Name: in.Body.Name, OwnerPrincipalID: owner,
			})
			if err != nil {
				return nil, tenantError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "organization-get-tenant", Method: http.MethodGet, Path: "/tenants/{tenant_id}",
		Summary: "Get a platform tenant", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "tenant", Verb: "view"},
		func(ctx context.Context, in *tenantIDInput) (*respx.Body[Tenant], error) {
			id, err := parseOrganizationID(in.ID)
			if err != nil {
				return nil, err
			}
			item, err := service.Get(ctx, id)
			if err != nil {
				return nil, tenantError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "organization-update-tenant", Method: http.MethodPatch, Path: "/tenants/{tenant_id}",
		Summary: "Update or suspend a platform tenant", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "tenant", Verb: "update"},
		func(ctx context.Context, in *updateTenantInput) (*respx.Body[Tenant], error) {
			id, err := parseOrganizationID(in.ID)
			if err != nil {
				return nil, err
			}
			item, err := service.Update(ctx, id, UpdateTenant{
				Name: in.Body.Name, Status: in.Body.Status, Version: in.Body.Version,
			})
			if err != nil {
				return nil, tenantError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "organization-delete-tenant", Method: http.MethodDelete, Path: "/tenants/{tenant_id}",
		Summary: "Soft-delete a platform tenant", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "tenant", Verb: "delete"},
		func(ctx context.Context, in *deleteTenantInput) (*respx.Body[deletedResource], error) {
			id, err := parseOrganizationID(in.ID)
			if err != nil {
				return nil, err
			}
			if err := service.Delete(ctx, id, in.Version); err != nil {
				return nil, tenantError(err)
			}
			return respx.OK(ctx, deletedResource{Deleted: true}), nil
		})
}

func registerInvitationHTTP(api huma.API, service *InvitationService, registrar *authz.Registrar) {
	authz.Register(registrar, api, huma.Operation{
		OperationID: "organization-list-invitations", Method: http.MethodGet, Path: "/membership-invitations",
		Summary: "List tenant invitations", Tags: []string{"directory"},
		Errors: []int{http.StatusUnprocessableEntity, http.StatusInternalServerError},
	}, authz.Guard{Resource: "invitation", Verb: "view"},
		func(ctx context.Context, in *tenantPageSelector) (*respx.Body[[]Invitation], error) {
			tenantID, err := parseOrganizationID(in.TenantID)
			if err != nil {
				return nil, err
			}
			items, total, err := service.ListPage(ctx, tenantID, in.Offset, in.Limit)
			if err != nil {
				return nil, invitationError(err)
			}
			return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "organization-create-invitation", Method: http.MethodPost, Path: "/membership-invitations",
		Summary: "Create a tenant invitation and return its credential once", Tags: []string{"directory"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError},
	}, authz.Guard{Resource: "invitation", Verb: "create"},
		func(ctx context.Context, in *createInvitationInput) (*respx.Body[issuedInvitation], error) {
			tenantID, err := parseOrganizationID(in.TenantID)
			if err != nil {
				return nil, err
			}
			departmentID, err := parseOptionalOrganizationID(in.Body.DepartmentID)
			if err != nil {
				return nil, err
			}
			roleIDs := make([]guid.ID, 0, len(in.Body.RoleIDs))
			for _, value := range in.Body.RoleIDs {
				roleID, err := parseOrganizationID(value)
				if err != nil {
					return nil, err
				}
				roleIDs = append(roleIDs, roleID)
			}
			item, token, err := service.Create(ctx, tenantID, CreateInvitation{
				Email: in.Body.Email, DepartmentID: departmentID, RoleIDs: roleIDs,
				TTL: time.Duration(in.Body.ExpiresInHours) * time.Hour,
			})
			if err != nil {
				return nil, invitationError(err)
			}
			return respx.OK(ctx, issuedInvitation{Invitation: item, Token: token}), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "organization-revoke-invitation", Method: http.MethodDelete, Path: "/membership-invitations/{invitation_id}",
		Summary: "Revoke a pending tenant invitation", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusGone, http.StatusUnprocessableEntity, http.StatusInternalServerError},
	}, "invitation_id", service.resolveInvitationOwner, authz.Guard{Resource: "invitation", Verb: "revoke"},
		func(ctx context.Context, in *invitationResourceInput) (*respx.Body[revokedInvitation], error) {
			tenantID, id, err := organizationResourceIDs(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if err := service.Revoke(ctx, tenantID, id); err != nil {
				return nil, invitationError(err)
			}
			return respx.OK(ctx, revokedInvitation{Revoked: true}), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "organization-resend-invitation", Method: http.MethodPost, Path: "/membership-invitations/{invitation_id}/deliveries",
		Summary: "Create a new invitation delivery and return its rotated credential once", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError},
	}, "invitation_id", service.resolveInvitationOwner, authz.Guard{Resource: "invitation", Verb: "resend"},
		func(ctx context.Context, in *resendInvitationInput) (*respx.Body[issuedInvitation], error) {
			tenantID, id, err := organizationResourceIDs(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			item, token, err := service.Resend(ctx, tenantID, id, time.Duration(in.Body.ExpiresInHours)*time.Hour)
			if err != nil {
				return nil, invitationError(err)
			}
			return respx.OK(ctx, issuedInvitation{Invitation: item, Token: token}), nil
		})

	authz.RegisterPublic(api, huma.Operation{
		OperationID: "organization-accept-invitation", Method: http.MethodPost, Path: "/membership-invitations/acceptances",
		Summary: "Accept a tenant invitation and create the local principal", Tags: []string{"authentication"},
		Security: []map[string][]string{},
		Errors:   []int{http.StatusBadRequest, http.StatusConflict, http.StatusGone, http.StatusInternalServerError},
	}, func(ctx context.Context, in *acceptInvitationInput) (*respx.Body[InvitationAcceptance], error) {
		accepted, err := service.Accept(ctx, AcceptInvitation{
			Token: in.Body.Token, LoginName: in.Body.LoginName,
			Password: in.Body.Password, DisplayName: in.Body.DisplayName,
		})
		if err != nil {
			return nil, invitationError(err)
		}
		return respx.OK(ctx, accepted), nil
	})
}

func departmentError(err error) error {
	switch {
	case errors.Is(err, core.ErrPrivilegeEscalation):
		return huma.Error403Forbidden(corei18n.PrivilegeEscalationDenied)
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound(organizationi18n.DepartmentNotFound)
	case errors.Is(err, ErrNameConflict):
		return huma.Error409Conflict(organizationi18n.DepartmentNameExists)
	case errors.Is(err, ErrHierarchyCycle):
		return huma.Error409Conflict(organizationi18n.DepartmentHierarchyCycle)
	case errors.Is(err, ErrEntityMismatch):
		return huma.Error422UnprocessableEntity(organizationi18n.InvalidDepartment)
	case errors.Is(err, ErrHasChildren):
		return huma.Error409Conflict(organizationi18n.DepartmentHasChildren)
	case errors.Is(err, ErrInUse):
		return huma.Error409Conflict(organizationi18n.DepartmentInUse)
	case errors.Is(err, ErrVersionConflict):
		return huma.Error409Conflict(organizationi18n.DepartmentVersionConflict)
	case errors.Is(err, ErrInvalid):
		return huma.Error422UnprocessableEntity(organizationi18n.InvalidDepartment)
	default:
		return huma.Error500InternalServerError(organizationi18n.Unavailable)
	}
}

func groupError(err error) error {
	switch {
	case errors.Is(err, core.ErrPrivilegeEscalation):
		return huma.Error403Forbidden(corei18n.PrivilegeEscalationDenied)
	case errors.Is(err, ErrGroupNotFound):
		return huma.Error404NotFound(organizationi18n.GroupNotFound)
	case errors.Is(err, ErrGroupNameConflict):
		return huma.Error409Conflict(organizationi18n.GroupNameExists)
	case errors.Is(err, ErrGroupVersionConflict):
		return huma.Error409Conflict(organizationi18n.GroupVersionConflict)
	case errors.Is(err, ErrGroupHasMembers):
		return huma.Error409Conflict(organizationi18n.GroupHasMembers)
	case errors.Is(err, ErrGroupRoleBound):
		return huma.Error409Conflict(organizationi18n.GroupRoleBound)
	case errors.Is(err, ErrGroupRelationshipBound):
		return huma.Error409Conflict(organizationi18n.GroupRelationshipBound)
	case errors.Is(err, ErrGroupMemberInactive):
		return huma.Error409Conflict(organizationi18n.GroupMemberInactive)
	case errors.Is(err, ErrDynamicGroupMembers):
		return huma.Error409Conflict(organizationi18n.DynamicGroupMembersComputed)
	case errors.Is(err, ErrGroupRuleType):
		return huma.Error422UnprocessableEntity(organizationi18n.GroupRuleRequiresDynamic)
	case errors.Is(err, ErrGroupRuleInvalid):
		return huma.Error422UnprocessableEntity(organizationi18n.InvalidGroupRule)
	case errors.Is(err, core.ErrLastTenantAdministrator):
		return huma.Error409Conflict(organizationi18n.LastTenantAdministrator)
	case errors.Is(err, ErrGroupInvalid):
		return huma.Error422UnprocessableEntity(organizationi18n.InvalidGroup)
	default:
		return huma.Error500InternalServerError(organizationi18n.Unavailable)
	}
}

func tenantError(err error) error {
	switch {
	case errors.Is(err, ErrTenantNotFound):
		return huma.Error404NotFound(organizationi18n.TenantNotFound)
	case errors.Is(err, ErrTenantSlugConflict):
		return huma.Error409Conflict(organizationi18n.TenantSlugExists)
	case errors.Is(err, ErrTenantVersionConflict):
		return huma.Error409Conflict(organizationi18n.TenantVersionConflict)
	case errors.Is(err, ErrInvalidTenant):
		return huma.Error422UnprocessableEntity(organizationi18n.InvalidTenant)
	case errors.Is(err, ErrTenantOwnerNotFound):
		return huma.Error422UnprocessableEntity(organizationi18n.TenantOwnerNotFound)
	case errors.Is(err, ErrTenantOwnerInactive):
		return huma.Error422UnprocessableEntity(organizationi18n.TenantOwnerInactive)
	default:
		return huma.Error500InternalServerError(organizationi18n.Unavailable)
	}
}

func invitationError(err error) error {
	switch {
	case errors.Is(err, core.ErrPrivilegeEscalation):
		return huma.Error403Forbidden(corei18n.PrivilegeEscalationDenied)
	case errors.Is(err, ErrInvitationNotFound):
		return huma.Error404NotFound(organizationi18n.InvitationNotFound)
	case errors.Is(err, ErrInvitationCredential):
		return huma.Error400BadRequest(organizationi18n.InvalidInvitationCredential)
	case errors.Is(err, ErrInvitationExpired):
		return huma.NewError(http.StatusGone, organizationi18n.InvitationExpired)
	case errors.Is(err, ErrInvitationState):
		return huma.Error409Conflict(organizationi18n.InvitationStateConflict)
	case errors.Is(err, ErrInvitationLoginConflict):
		return huma.Error409Conflict(organizationi18n.InvitationLoginNameExists)
	case errors.Is(err, ErrInvitationBindingMissing):
		return huma.Error404NotFound(organizationi18n.InvitationBindingNotFound)
	case errors.Is(err, ErrInvitationBindingInactive):
		return huma.Error409Conflict(organizationi18n.InvitationBindingInactive)
	case errors.Is(err, ErrInvitationInvalid):
		return huma.Error422UnprocessableEntity(organizationi18n.InvalidInvitation)
	default:
		return huma.Error500InternalServerError(organizationi18n.Unavailable)
	}
}
