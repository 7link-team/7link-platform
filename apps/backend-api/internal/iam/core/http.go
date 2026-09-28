package core

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
)

func optionalIDString(id guid.ID) string {
	if id.Zero() {
		return ""
	}
	return id.String()
}

func requireIDs(values []string) ([]guid.ID, error) {
	ids := make([]guid.ID, 0, len(values))
	for _, value := range values {
		id, err := guid.RequireID(value)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func roleRef(ctx context.Context, in *roleInput) (guid.ID, guid.ID, error) {
	return roleRefIDs(ctx, in.RoleID)
}

func roleRefIDs(ctx context.Context, roleIDValue string) (guid.ID, guid.ID, error) {
	tenantID := authnext.TenantIDFromContext(ctx)
	roleID, err := guid.RequireID(roleIDValue)
	if err != nil {
		return 0, 0, err
	}
	return tenantID, roleID, nil
}

func memberRef(ctx context.Context, in *memberInput) (guid.ID, guid.ID, guid.ID, error) {
	tenantID := authnext.TenantIDFromContext(ctx)
	roleID, err := guid.RequireID(in.RoleID)
	if err != nil {
		return 0, 0, 0, err
	}
	subject, err := guid.RequireID(in.Subject)
	if err != nil {
		return 0, 0, 0, err
	}
	return tenantID, roleID, subject, nil
}

func tenantMemberRef(in *tenantMemberInput) (guid.ID, guid.ID, error) {
	return tenantMemberRefIDs(in.TenantID, in.Subject)
}

func tenantMemberRefIDs(tenantIDValue, subjectValue string) (guid.ID, guid.ID, error) {
	tenantID, err := guid.RequireID(tenantIDValue)
	if err != nil {
		return 0, 0, err
	}
	subject, err := guid.RequireID(subjectValue)
	if err != nil {
		return 0, 0, err
	}
	return tenantID, subject, nil
}

func menuRef(ctx context.Context, in *menuInput) (guid.ID, guid.ID, error) {
	tenantID := authnext.TenantIDFromContext(ctx)
	menuID, err := guid.RequireID(in.MenuID)
	if err != nil {
		return 0, 0, err
	}
	return tenantID, menuID, nil
}

type APITenantMember struct {
	TenantID     guid.ID      `json:"tenant_id"`
	Subject      guid.ID      `json:"subject"`
	DisplayName  string       `json:"display_name,omitempty"`
	Email        string       `json:"email,omitempty"`
	DepartmentID guid.ID      `json:"department_id,omitempty"`
	Status       MemberStatus `json:"status"`
	RoleIDs      []guid.ID    `json:"role_ids,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
	DisabledAt   *time.Time   `json:"disabled_at,omitempty"`
}

type APIMenu struct {
	Translations   map[string]string `json:"translations,omitempty"`
	ID             guid.ID           `json:"id"`
	TenantID       guid.ID           `json:"tenant_id"`
	ParentID       guid.ID           `json:"parent_id,omitempty"`
	Label          string            `json:"label"`
	Route          string            `json:"route,omitempty"`
	Icon           string            `json:"icon,omitempty"`
	SortOrder      int               `json:"sort_order"`
	PermissionCode string            `json:"permission_code,omitempty"`
	Status         MenuStatus        `json:"status"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type APIRole struct {
	ID          guid.ID   `json:"id"`
	TenantID    guid.ID   `json:"tenant_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type APIRolePermissionGrant struct {
	PermissionCode string          `json:"permission_code"`
	Condition      policyCondition `json:"condition,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

type APIRoleDirectoryBinding struct {
	RoleID       guid.ID               `json:"role_id"`
	AssigneeType DirectoryAssigneeType `json:"assignee_type" enum:"group"`
	AssigneeID   guid.ID               `json:"assignee_id"`
	CreatedAt    time.Time             `json:"created_at"`
}

type APIRoleDataScope struct {
	RoleID        guid.ID    `json:"role_id"`
	Scope         DataScope  `json:"scope" enum:"all,self,department,department_and_descendants,selected_departments"`
	DepartmentIDs []guid.ID  `json:"department_ids"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

type MutationResult struct {
	Changed    bool   `json:"changed" doc:"true when the local desired binding changed"`
	SyncStatus string `json:"sync_status" doc:"local transaction state; applied means immediately effective"`
}

type APIEntity struct {
	ID        guid.ID        `json:"id"`
	TenantID  guid.ID        `json:"tenant_id"`
	ParentID  guid.ID        `json:"parent_id,omitempty"`
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Status    EntityStatus   `json:"status"`
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type APIEntityRoleBinding struct {
	EntityID    guid.ID       `json:"entity_id"`
	RoleID      guid.ID       `json:"role_id"`
	PrincipalID guid.ID       `json:"principal_id"`
	Effect      BindingEffect `json:"effect" enum:"allow,deny"`
	ExpiresAt   *time.Time    `json:"expires_at,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
}

type APIMemberAuthorizationSource struct {
	Type             MemberAuthorizationSourceType `json:"type" enum:"permanent_role,temporary_role,static_group,dynamic_group,entity_binding,relationship"`
	SourceID         guid.ID                       `json:"source_id"`
	SourceName       string                        `json:"source_name,omitempty"`
	RoleID           guid.ID                       `json:"role_id,omitempty"`
	RoleName         string                        `json:"role_name,omitempty"`
	PermissionCodes  []string                      `json:"permission_codes"`
	Effect           BindingEffect                 `json:"effect" enum:"allow,deny"`
	ScopeType        string                        `json:"scope_type"`
	ScopeID          guid.ID                       `json:"scope_id"`
	ScopeName        string                        `json:"scope_name,omitempty"`
	DataScope        DataScope                     `json:"data_scope,omitempty" enum:"all,self,department,department_and_descendants,selected_departments"`
	DepartmentIDs    []guid.ID                     `json:"department_ids"`
	ExpiresAt        *time.Time                    `json:"expires_at,omitempty"`
	Inherited        bool                          `json:"inherited"`
	Relation         string                        `json:"relation,omitempty" enum:"owner,editor,viewer"`
	RelationshipPath []authz.RelationshipStep      `json:"relationship_path"`
}

type APIMemberAuthorizationSnapshot struct {
	Subject     string                         `json:"subject" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Revision    int64                          `json:"revision"`
	EvaluatedAt time.Time                      `json:"evaluated_at"`
	Sources     []APIMemberAuthorizationSource `json:"sources"`
}

type tenantInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"tenant authorization boundary"`
	// ponytail: pagination added to prevent unbounded list responses.
	// Offset and Limit are optional; 0/0 returns all records.
	Offset int `query:"offset" minimum:"0" default:"0"`
	Limit  int `query:"limit" minimum:"0" maximum:"200" default:"0"`
}

// boundedTenantInput is tenantInput's bounded counterpart, for the tenant-wide
// listings that actually page: entities and roles both grow with the tenant, so
// omitting limit must not mean the whole table. tenantInput keeps the opt-in
// shape for the handlers that do not page at all.
type boundedTenantInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"tenant authorization boundary"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

// myPermissionsInput carries the tenant selector alone: the reply is a bounded
// catalog of the caller's own codes, so there is nothing to page through.
type myPermissionsInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"tenant authorization boundary"`
}

type myEntityPermissionsInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"tenant authorization boundary"`
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"entity authorization boundary"`
}

type roleInput struct {
	RoleID string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

// listRoleMembersInput bounds the listing by default. A role's membership grows
// with the tenant, so the page size follows listTenantMembersInput rather than
// the opt-in shape, where omitting limit returns every row.
type listRoleMembersInput struct {
	RoleID string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset int    `query:"offset" minimum:"0" default:"0"`
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

// listRoleDirectoryBindingsInput bounds the listing by default: a role can be
// granted to any number of the tenant's groups.
type listRoleDirectoryBindingsInput struct {
	RoleID string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset int    `query:"offset" minimum:"0" default:"0"`
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type createRoleInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		Name        string `json:"name" minLength:"1" maxLength:"128"`
		Description string `json:"description,omitempty" maxLength:"4096"`
	}
}

type updateRoleInput struct {
	RoleID string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body   struct {
		Name        *string `json:"name,omitempty" minLength:"1" maxLength:"128"`
		Description *string `json:"description,omitempty" maxLength:"4096"`
	}
}

type setRoleDataScopeInput struct {
	RoleID string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body   struct {
		Scope         DataScope `json:"scope" enum:"all,self,department,department_and_descendants,selected_departments"`
		DepartmentIDs []string  `json:"department_ids,omitempty" maxItems:"200"`
	}
}

type permissionInput struct {
	RoleID         string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	PermissionCode string `path:"permission_code" maxLength:"128"`
}

type setPermissionConditionInput struct {
	RoleID         string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	PermissionCode string `path:"permission_code" maxLength:"128"`
	Body           struct {
		Condition policyCondition `json:"condition"`
	}
}

type memberInput struct {
	RoleID  string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Subject string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"immutable local principal ID"`
}

type directoryBindingInput struct {
	RoleID       string                `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	AssigneeType DirectoryAssigneeType `path:"assignee_type" enum:"group"`
	AssigneeID   string                `path:"assignee_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type listTenantMembersInput struct {
	TenantID string       `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Search   string       `query:"search" maxLength:"128"`
	Status   MemberStatus `query:"status" enum:"active,disabled"`
	Offset   int          `query:"offset" minimum:"0" default:"0"`
	Limit    int          `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type tenantMemberInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Subject  string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

// listTenantMemberRolesInput bounds the listing by default: a member holds as
// many roles as the tenant grants them, permanent and temporary together.
type listTenantMemberRolesInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Subject  string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type createTenantMemberInput struct {
	RawBody  []byte
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		Subject      string       `json:"subject" pattern:"^[1-9][0-9]*$" maxLength:"19"`
		DisplayName  string       `json:"display_name" minLength:"1" maxLength:"128"`
		Email        string       `json:"email,omitempty" maxLength:"320"`
		DepartmentID string       `json:"department_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19" doc:"positive department ID; omitted or empty leaves the member unassigned"`
		Status       MemberStatus `json:"status,omitempty" enum:"active,disabled" default:"active"`
	}
}

type updateTenantMemberInput struct {
	RawBody  []byte
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Subject  string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		DisplayName  *string       `json:"display_name,omitempty" minLength:"1" maxLength:"128"`
		Email        *string       `json:"email,omitempty" maxLength:"320"`
		DepartmentID *string       `json:"department_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19" doc:"positive department ID, or an empty string to clear the assignment; omitted leaves it unchanged"`
		Status       *MemberStatus `json:"status,omitempty" enum:"active,disabled"`
	}
}

type menuInput struct {
	MenuID string `path:"menu_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type createMenuInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		Translations   map[string]string `json:"translations,omitempty" doc:"BCP 47 language tags mapped to menu labels; default label is the fallback"`
		ParentID       string            `json:"parent_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19" doc:"positive parent menu ID; omitted or empty makes this a root menu"`
		Label          string            `json:"label" minLength:"1" maxLength:"128"`
		Route          string            `json:"route,omitempty" maxLength:"512"`
		Icon           string            `json:"icon,omitempty" maxLength:"64"`
		SortOrder      int               `json:"sort_order,omitempty" minimum:"-100000" maximum:"100000" default:"0"`
		PermissionCode string            `json:"permission_code,omitempty" maxLength:"128"`
		Status         MenuStatus        `json:"status,omitempty" enum:"active,disabled" default:"active"`
	}
}

type updateMenuInput struct {
	MenuID string `path:"menu_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body   struct {
		Placement      *menuPlacementInput `json:"placement,omitempty" doc:"atomically reparent and order this subtree; cannot be combined with metadata edits"`
		Translations   *map[string]string  `json:"translations,omitempty" doc:"replace translations; an empty object clears them; omitted preserves existing translations"`
		ParentID       *string             `json:"parent_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19" doc:"positive parent menu ID, or an empty string to move this menu to the root; omitted leaves it where it is"`
		Label          *string             `json:"label,omitempty" minLength:"1" maxLength:"128"`
		Route          *string             `json:"route,omitempty" maxLength:"512"`
		Icon           *string             `json:"icon,omitempty" maxLength:"64"`
		SortOrder      *int                `json:"sort_order,omitempty" minimum:"-100000" maximum:"100000"`
		PermissionCode *string             `json:"permission_code,omitempty" maxLength:"128"`
		Status         *MenuStatus         `json:"status,omitempty" enum:"active,disabled"`
	}
}

type menuPlacementInput struct {
	Kind       string `json:"kind" enum:"first,last,before,after"`
	RelativeTo string `json:"relative_to,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19" doc:"required sibling menu ID for before/after; omitted for first/last"`
}

type entityInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

// listEntityRoleBindingsInput bounds the listing by default: an entity carries
// one binding per principal and role granted at its scope.
type listEntityRoleBindingsInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type createEntityInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		ParentID string         `json:"parent_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19" doc:"positive parent entity ID; omitted or empty makes this a root entity"`
		Type     string         `json:"type" minLength:"1" maxLength:"64"`
		Name     string         `json:"name" minLength:"1" maxLength:"200"`
		Status   EntityStatus   `json:"status,omitempty" enum:"active,disabled" default:"active"`
		Metadata map[string]any `json:"metadata,omitempty"`
	}
}

type updateEntityInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		ParentID *string         `json:"parent_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19" doc:"positive parent entity ID, or an empty string to move this entity to the root; omitted leaves it where it is"`
		Type     *string         `json:"type,omitempty" minLength:"1" maxLength:"64"`
		Name     *string         `json:"name,omitempty" minLength:"1" maxLength:"200"`
		Status   *EntityStatus   `json:"status,omitempty" enum:"active,disabled"`
		Metadata *map[string]any `json:"metadata,omitempty"`
	}
}

type entityRoleBindingRef struct {
	EntityID    string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RoleID      string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type putEntityRoleBindingInput struct {
	EntityID    string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RoleID      string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body        struct {
		Effect    BindingEffect `json:"effect" enum:"allow,deny"`
		ExpiresAt *time.Time    `json:"expires_at,omitempty"`
	}
}

type authorizationConstraintInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		PermissionCode string `json:"permission_code" maxLength:"128"`
		Subject        string `json:"subject" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	}
}

type authorizationExplanationInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		EntityID       string `json:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
		ResourceType   string `json:"resource_type,omitempty" maxLength:"64"`
		ResourceID     string `json:"resource_id,omitempty" pattern:"^[1-9][0-9]*$" maxLength:"19"`
		PermissionCode string `json:"permission_code" maxLength:"128"`
		Subject        string `json:"subject" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	}
}

// RegisterHTTP registers all core IAM HTTP operations.
func RegisterHTTP(a huma.API, svc *Service, registrar *authz.Registrar) {
	registerAccountViewWorkspaceHTTP(a, svc, registrar)
	registerMemberExitHTTP(a, svc, registrar)
	registerFieldPolicyHTTP(a, svc, registrar)
	registerRelationshipHTTP(a, svc, registrar)
	registerPlatformHTTP(a, svc, registrar)
	registerOperatingPlatformHTTP(a, svc, registrar)
	registerSystemMenuHTTP(a, svc, registrar)
	registerSystemAccessHTTP(a, svc, registrar)
	registerSystemScopeHTTP(a, svc, registrar)
	authz.Register(registrar, a, huma.Operation{
		OperationID: "iam-permission-catalog",
		Method:      http.MethodGet,
		Path:        "/permission-catalog",
		Summary:     "List declared permissions",
		Tags:        []string{"access-control"},
	}, authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]authz.Action], error) {
		return respx.OK(ctx, svc.PermissionCatalog(ctx)), nil
	})

	authz.RegisterPlatform(registrar, a, huma.Operation{
		OperationID: "iam-scope-model",
		Method:      http.MethodGet,
		Path:        "/authorization-scope-model",
		Summary:     "List platform tenant merchant store scope model",
		Tags:        []string{"access-control"},
	}, authz.Guard{Resource: "tenant", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]ScopeNode], error) {
		return respx.OK(ctx, svc.ScopeModel(ctx)), nil
	})

	authz.Register(registrar, a, huma.Operation{
		OperationID: "iam-menu-catalog",
		Method:      http.MethodGet,
		Path:        "/menu-catalog",
		Summary:     "List menu metadata bound to permission codes",
		Tags:        []string{"access-control"},
	}, authz.Guard{Resource: "menu", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]MenuDeclaration], error) {
		return respx.OK(ctx, svc.MenuCatalog(ctx)), nil
	})

	authz.Register(registrar, a, huma.Operation{
		OperationID: "iam-authorization-constraint", Method: http.MethodPost, Path: "/authorization-constraint-evaluations", Summary: "Compute an entity data constraint for a tenant subject", Tags: []string{"access-control"}, Errors: []int{http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, in *authorizationConstraintInput) (*respx.Body[authz.DataConstraint], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		subject, err := guid.RequireID(in.Body.Subject)
		if err != nil {
			return nil, err
		}
		constraint, err := svc.AuthorizationConstraint(ctx, tenantID, in.Body.PermissionCode, subject)
		if err != nil {
			return nil, apiError("compute authorization constraint", err)
		}
		return respx.OK(ctx, constraint), nil
	})

	authz.Register(registrar, a, huma.Operation{
		OperationID: "iam-explain-authorization", Method: http.MethodPost, Path: "/authorization-explanations", Summary: "Explain an entity or business-resource authorization decision", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, in *authorizationExplanationInput) (*respx.Body[authz.Explanation], error) {
		var explanation authz.Explanation
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		entityID, err := guid.RequireID(in.Body.EntityID)
		if err != nil {
			return nil, err
		}
		subject, err := guid.RequireID(in.Body.Subject)
		if err != nil {
			return nil, err
		}
		if in.Body.ResourceType != "" || in.Body.ResourceID != "" {
			resourceID, err := guid.RequireID(in.Body.ResourceID)
			if err != nil {
				return nil, err
			}
			explanation, err = svc.ExplainResourceAuthorization(ctx, tenantID, entityID, in.Body.ResourceType, resourceID, in.Body.PermissionCode, subject)
			if err != nil {
				return nil, apiError("explain resource authorization", err)
			}
		} else {
			explanation, err = svc.ExplainEntityAuthorization(ctx, tenantID, entityID, in.Body.PermissionCode, subject)
			if err != nil {
				return nil, apiError("explain entity authorization", err)
			}
		}
		return respx.OK(ctx, explanation), nil
	})

	authz.Register(registrar, a, huma.Operation{
		OperationID: "iam-list-entities", Method: http.MethodGet, Path: "/entities", Summary: "List tenant entities", Tags: []string{"directory"},
	}, authz.Guard{Resource: "entity", Verb: "view"}, func(ctx context.Context, in *boundedTenantInput) (*respx.Body[[]APIEntity], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		entities, total, err := svc.ListEntitiesPage(ctx, tenantID, in.Offset, in.Limit)
		if err != nil {
			return nil, apiError("list entities", err)
		}
		return respx.List(ctx, entitiesFromDomain(entities), respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})

	authz.Register(registrar, a, huma.Operation{
		OperationID: "iam-create-entity", Method: http.MethodPost, Path: "/entities", Summary: "Create a tenant entity", Tags: []string{"directory"}, DefaultStatus: http.StatusCreated, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "entity", Verb: "create"}, func(ctx context.Context, in *createEntityInput) (*respx.Body[APIEntity], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		parentID, err := guid.OptionalID(in.Body.ParentID)
		if err != nil {
			return nil, err
		}
		entity, err := svc.CreateEntity(ctx, Entity{
			TenantID: tenantID, ParentID: parentID, Type: in.Body.Type, Name: in.Body.Name,
			Status: in.Body.Status, Metadata: in.Body.Metadata,
		})
		if err != nil {
			return nil, apiError("create entity", err)
		}
		return respx.OK(ctx, entityFromDomain(entity)), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-get-entity", Method: http.MethodGet, Path: "/entities/{entity_id}", Summary: "Get a tenant entity", Tags: []string{"directory"}, Errors: []int{http.StatusNotFound},
	}, "entity_id", svc.resolveEntityOwner, authz.Guard{Resource: "entity", Verb: "view"}, func(ctx context.Context, in *entityInput) (*respx.Body[APIEntity], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		entityID, err := guid.RequireID(in.EntityID)
		if err != nil {
			return nil, err
		}
		entity, err := svc.GetEntity(ctx, tenantID, entityID)
		if err != nil {
			return nil, apiError("get entity", err)
		}
		return respx.OK(ctx, entityFromDomain(entity)), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-update-entity", Method: http.MethodPatch, Path: "/entities/{entity_id}", Summary: "Update a tenant entity", Tags: []string{"directory"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "entity_id", svc.resolveEntityOwner, authz.Guard{Resource: "entity", Verb: "update"}, func(ctx context.Context, in *updateEntityInput) (*respx.Body[APIEntity], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		entityID, err := guid.RequireID(in.EntityID)
		if err != nil {
			return nil, err
		}
		patch := EntityPatch{Type: in.Body.Type, Name: in.Body.Name, Status: in.Body.Status, Metadata: in.Body.Metadata}
		if in.Body.ParentID != nil {
			parentID, err := guid.OptionalID(*in.Body.ParentID)
			if err != nil {
				return nil, err
			}
			patch.ParentID = &parentID
		}
		entity, err := svc.UpdateEntity(ctx, tenantID, entityID, patch)
		if err != nil {
			return nil, apiError("update entity", err)
		}
		return respx.OK(ctx, entityFromDomain(entity)), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-delete-entity", Method: http.MethodDelete, Path: "/entities/{entity_id}", Summary: "Delete an empty tenant entity", Tags: []string{"directory"}, Errors: []int{http.StatusNotFound, http.StatusConflict},
	}, "entity_id", svc.resolveEntityOwner, authz.Guard{Resource: "entity", Verb: "delete"}, func(ctx context.Context, in *entityInput) (*respx.Body[MutationResult], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		entityID, err := guid.RequireID(in.EntityID)
		if err != nil {
			return nil, err
		}
		if err := svc.DeleteEntity(ctx, tenantID, entityID); err != nil {
			return nil, apiError("delete entity", err)
		}
		return respx.OK(ctx, MutationResult{Changed: true, SyncStatus: "applied"}), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-list-entity-role-bindings", Method: http.MethodGet, Path: "/entities/{entity_id}/role-bindings", Summary: "List direct principal role bindings for an entity", Tags: []string{"directory"}, Errors: []int{http.StatusNotFound},
	}, "entity_id", svc.resolveEntityOwner, authz.Guard{Resource: "entity", Verb: "view"}, func(ctx context.Context, in *listEntityRoleBindingsInput) (*respx.Body[[]APIEntityRoleBinding], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		entityID, err := guid.RequireID(in.EntityID)
		if err != nil {
			return nil, err
		}
		bindings, total, err := svc.ListEntityRoleBindingsPage(ctx, tenantID, entityID, in.Offset, in.Limit)
		if err != nil {
			return nil, apiError("list entity role bindings", err)
		}
		return respx.List(ctx, entityBindingsFromDomain(bindings), respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-put-entity-role-binding", Method: http.MethodPut, Path: "/entities/{entity_id}/role-bindings/{role_id}/{principal_id}", Summary: "Assign a direct principal role at an entity scope", Tags: []string{"directory"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "entity_id", svc.resolveEntityOwner, authz.Guard{Resource: "entity", Verb: "manage_binding"}, func(ctx context.Context, in *putEntityRoleBindingInput) (*respx.Body[APIEntityRoleBinding], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		entityID, err := guid.RequireID(in.EntityID)
		if err != nil {
			return nil, err
		}
		roleID, err := guid.RequireID(in.RoleID)
		if err != nil {
			return nil, err
		}
		principalID, err := guid.RequireID(in.PrincipalID)
		if err != nil {
			return nil, err
		}
		expiresAt := time.Time{}
		if in.Body.ExpiresAt != nil {
			expiresAt = *in.Body.ExpiresAt
		}
		binding, _, err := svc.PutEntityRoleBinding(ctx, tenantID, entityID, roleID, principalID, in.Body.Effect, expiresAt)
		if err != nil {
			return nil, apiError("put entity role binding", err)
		}
		return respx.OK(ctx, entityBindingFromDomain(binding)), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-delete-entity-role-binding", Method: http.MethodDelete, Path: "/entities/{entity_id}/role-bindings/{role_id}/{principal_id}", Summary: "Remove a direct principal role from an entity scope", Tags: []string{"directory"}, Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, "entity_id", svc.resolveEntityOwner, authz.Guard{Resource: "entity", Verb: "manage_binding"}, func(ctx context.Context, in *entityRoleBindingRef) (*respx.Body[MutationResult], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		entityID, err := guid.RequireID(in.EntityID)
		if err != nil {
			return nil, err
		}
		roleID, err := guid.RequireID(in.RoleID)
		if err != nil {
			return nil, err
		}
		principalID, err := guid.RequireID(in.PrincipalID)
		if err != nil {
			return nil, err
		}
		changed, err := svc.DeleteEntityRoleBinding(ctx, tenantID, entityID, roleID, principalID)
		if err != nil {
			return nil, apiError("delete entity role binding", err)
		}
		return respx.OK(ctx, MutationResult{Changed: changed, SyncStatus: "applied"}), nil
	})

	authz.Register(registrar, a, huma.Operation{
		OperationID: "iam-list-roles", Method: http.MethodGet, Path: "/roles", Summary: "List tenant roles", Tags: []string{"access-control"},
	}, authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, in *boundedTenantInput) (*respx.Body[[]APIRole], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		roles, total, err := svc.ListRolesPage(ctx, tenantID, in.Offset, in.Limit)
		if err != nil {
			return nil, apiError("list roles", err)
		}
		return respx.List(ctx, rolesFromDomain(roles), respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})

	authz.Register(registrar, a, huma.Operation{
		OperationID: "iam-create-role", Method: http.MethodPost, Path: "/roles", Summary: "Create a tenant role", Tags: []string{"access-control"}, DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict},
	}, authz.Guard{Resource: "role", Verb: "create"}, func(ctx context.Context, in *createRoleInput) (*respx.Body[APIRole], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		role, err := svc.CreateRole(ctx, tenantID, in.Body.Name, in.Body.Description)
		if err != nil {
			return nil, apiError("create role", err)
		}
		return respx.OK(ctx, roleFromDomain(role)), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-get-role", Method: http.MethodGet, Path: "/roles/{role_id}", Summary: "Get a tenant role", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, in *roleInput) (*respx.Body[APIRole], error) {
		tenantID, roleID, err := roleRef(ctx, in)
		if err != nil {
			return nil, err
		}
		role, err := svc.GetRole(ctx, tenantID, roleID)
		if err != nil {
			return nil, apiError("get role", err)
		}
		return respx.OK(ctx, roleFromDomain(role)), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-get-role-data-scope", Method: http.MethodGet, Path: "/roles/{role_id}/data-scope", Summary: "Get a role data scope", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, in *roleInput) (*respx.Body[APIRoleDataScope], error) {
		tenantID, roleID, err := roleRef(ctx, in)
		if err != nil {
			return nil, err
		}
		scope, err := svc.GetRoleDataScope(ctx, tenantID, roleID)
		if err != nil {
			return nil, apiError("get role data scope", err)
		}
		return respx.OK(ctx, roleDataScopeFromDomain(scope)), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-set-role-data-scope", Method: http.MethodPut, Path: "/roles/{role_id}/data-scope", Summary: "Set a role data scope", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "update"}, func(ctx context.Context, in *setRoleDataScopeInput) (*respx.Body[APIRoleDataScope], error) {
		tenantID, roleID, err := roleRefIDs(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		departmentIDs, err := requireIDs(in.Body.DepartmentIDs)
		if err != nil {
			return nil, err
		}
		scope, _, err := svc.SetRoleDataScope(ctx, tenantID, roleID, in.Body.Scope, departmentIDs)
		if err != nil {
			return nil, apiError("set role data scope", err)
		}
		return respx.OK(ctx, roleDataScopeFromDomain(scope)), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-update-role", Method: http.MethodPatch, Path: "/roles/{role_id}", Summary: "Update a tenant role", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusConflict},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "update"}, func(ctx context.Context, in *updateRoleInput) (*respx.Body[APIRole], error) {
		tenantID, roleID, err := roleRefIDs(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		role, err := svc.UpdateRole(ctx, tenantID, roleID, in.Body.Name, in.Body.Description)
		if err != nil {
			return nil, apiError("update role", err)
		}
		return respx.OK(ctx, roleFromDomain(role)), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-delete-role", Method: http.MethodDelete, Path: "/roles/{role_id}", Summary: "Delete a tenant role", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusConflict},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "delete"}, func(ctx context.Context, in *roleInput) (*respx.Body[MutationResult], error) {
		tenantID, roleID, err := roleRef(ctx, in)
		if err != nil {
			return nil, err
		}
		if err := svc.DeleteRole(ctx, tenantID, roleID); err != nil {
			return nil, apiError("delete role", err)
		}
		return respx.OK(ctx, MutationResult{Changed: true, SyncStatus: "applied"}), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-list-role-permission-grants", Method: http.MethodGet, Path: "/roles/{role_id}/permission-grants", Summary: "List role permission grants with conditions", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, in *roleInput) (*respx.Body[[]APIRolePermissionGrant], error) {
		tenantID, roleID, err := roleRef(ctx, in)
		if err != nil {
			return nil, err
		}
		grants, err := svc.ListPermissionGrants(ctx, tenantID, roleID)
		if err != nil {
			return nil, apiError("list role permission grants", err)
		}
		return respx.OK(ctx, rolePermissionGrantsFromDomain(grants)), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-grant-role-permission", Method: http.MethodPut, Path: "/roles/{role_id}/permissions/{permission_code}", Summary: "Grant a permission to a role", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "grant_permission"}, func(ctx context.Context, in *permissionInput) (*respx.Body[MutationResult], error) {
		tenantID, roleID, err := roleRefIDs(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		changed, err := svc.GrantPermission(ctx, tenantID, roleID, in.PermissionCode)
		if err != nil {
			return nil, apiError("grant role permission", err)
		}
		return respx.OK(ctx, MutationResult{Changed: changed, SyncStatus: "applied"}), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-revoke-role-permission", Method: http.MethodDelete, Path: "/roles/{role_id}/permissions/{permission_code}", Summary: "Revoke a permission from a role", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusConflict},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "grant_permission"}, func(ctx context.Context, in *permissionInput) (*respx.Body[MutationResult], error) {
		tenantID, roleID, err := roleRefIDs(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		changed, err := svc.RevokePermission(ctx, tenantID, roleID, in.PermissionCode)
		if err != nil {
			return nil, apiError("revoke role permission", err)
		}
		return respx.OK(ctx, MutationResult{Changed: changed, SyncStatus: "applied"}), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-set-role-permission-condition", Method: http.MethodPut, Path: "/roles/{role_id}/permissions/{permission_code}/condition", Summary: "Set a trusted-context condition on a role permission", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "grant_permission"}, func(ctx context.Context, in *setPermissionConditionInput) (*respx.Body[APIRolePermissionGrant], error) {
		tenantID, roleID, err := roleRefIDs(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		grant, _, err := svc.SetPermissionCondition(ctx, tenantID, roleID, in.PermissionCode, json.RawMessage(in.Body.Condition))
		if err != nil {
			return nil, apiError("set role permission condition", err)
		}
		return respx.OK(ctx, rolePermissionGrantFromDomain(grant)), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-clear-role-permission-condition", Method: http.MethodDelete, Path: "/roles/{role_id}/permissions/{permission_code}/condition", Summary: "Clear a role permission condition", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "grant_permission"}, func(ctx context.Context, in *permissionInput) (*respx.Body[MutationResult], error) {
		tenantID, roleID, err := roleRefIDs(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		_, changed, err := svc.SetPermissionCondition(ctx, tenantID, roleID, in.PermissionCode, nil)
		if err != nil {
			return nil, apiError("clear role permission condition", err)
		}
		return respx.OK(ctx, MutationResult{Changed: changed, SyncStatus: "applied"}), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-list-role-members", Method: http.MethodGet, Path: "/roles/{role_id}/members", Summary: "List local principals in a role", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, in *listRoleMembersInput) (*respx.Body[[]string], error) {
		tenantID, roleID, err := roleRefIDs(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		subjects, total, err := svc.ListRoleMembersPage(ctx, tenantID, roleID, in.Offset, in.Limit)
		if err != nil {
			return nil, apiError("list role members", err)
		}
		return respx.List(ctx, guidStrings(subjects), respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-add-role-member", Method: http.MethodPut, Path: "/roles/{role_id}/members/{principal_id}", Summary: "Add a local principal to a role", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusConflict},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "manage_member"}, func(ctx context.Context, in *memberInput) (*respx.Body[MutationResult], error) {
		tenantID, roleID, subject, err := memberRef(ctx, in)
		if err != nil {
			return nil, err
		}
		changed, err := svc.AddMember(ctx, tenantID, roleID, subject)
		if err != nil {
			return nil, apiError("add role member", err)
		}
		return respx.OK(ctx, MutationResult{Changed: changed, SyncStatus: "applied"}), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-remove-role-member", Method: http.MethodDelete, Path: "/roles/{role_id}/members/{principal_id}", Summary: "Remove a local principal from a role", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusConflict},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "manage_member"}, func(ctx context.Context, in *memberInput) (*respx.Body[MutationResult], error) {
		tenantID, roleID, subject, err := memberRef(ctx, in)
		if err != nil {
			return nil, err
		}
		changed, err := svc.RemoveMember(ctx, tenantID, roleID, subject)
		if err != nil {
			return nil, apiError("remove role member", err)
		}
		return respx.OK(ctx, MutationResult{Changed: changed, SyncStatus: "applied"}), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-list-role-directory-bindings", Method: http.MethodGet, Path: "/roles/{role_id}/directory-bindings", Summary: "List group role assignments", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, in *listRoleDirectoryBindingsInput) (*respx.Body[[]APIRoleDirectoryBinding], error) {
		tenantID, roleID, err := roleRefIDs(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		bindings, total, err := svc.ListDirectoryBindingsPage(ctx, tenantID, roleID, in.Offset, in.Limit)
		if err != nil {
			return nil, apiError("list role directory bindings", err)
		}
		return respx.List(ctx, directoryBindingsFromDomain(bindings), respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-add-role-directory-binding", Method: http.MethodPut, Path: "/roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}", Summary: "Assign a group to a role", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "manage_assignee"}, func(ctx context.Context, in *directoryBindingInput) (*respx.Body[MutationResult], error) {
		tenantID, roleID, err := roleRefIDs(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		assigneeID, err := guid.RequireID(in.AssigneeID)
		if err != nil {
			return nil, err
		}
		changed, err := svc.AddDirectoryBinding(ctx, tenantID, roleID, in.AssigneeType, assigneeID)
		if err != nil {
			return nil, apiError("add role directory binding", err)
		}
		return respx.OK(ctx, MutationResult{Changed: changed, SyncStatus: "applied"}), nil
	})

	authz.RegisterOwned(registrar, a, huma.Operation{
		OperationID: "iam-remove-role-directory-binding", Method: http.MethodDelete, Path: "/roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}", Summary: "Remove a group from a role", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "role_id", svc.resolveRoleOwner, authz.Guard{Resource: "role", Verb: "manage_assignee"}, func(ctx context.Context, in *directoryBindingInput) (*respx.Body[MutationResult], error) {
		tenantID, roleID, err := roleRefIDs(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		assigneeID, err := guid.RequireID(in.AssigneeID)
		if err != nil {
			return nil, err
		}
		changed, err := svc.RemoveDirectoryBinding(ctx, tenantID, roleID, in.AssigneeType, assigneeID)
		if err != nil {
			return nil, apiError("remove role directory binding", err)
		}
		return respx.OK(ctx, MutationResult{Changed: changed, SyncStatus: "applied"}), nil
	})

	authz.Register(registrar, a, huma.Operation{OperationID: "iam-list-tenant-members", Method: http.MethodGet, Path: "/members", Summary: "List tenant memberships", Tags: []string{"directory"}}, authz.Guard{Resource: "user", Verb: "view"}, func(ctx context.Context, in *listTenantMembersInput) (*respx.Body[[]APITenantMember], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		fields, err := svc.profileConstraint(ctx, tenantID)
		if err == nil {
			err = fields.RequireRead("display_name") // The repository orders by display name.
		}
		if err == nil && strings.TrimSpace(in.Search) != "" {
			err = fields.RequireRead("display_name", "email")
		}
		if err != nil {
			return nil, apiError("read member fields", err)
		}
		members, total, err := svc.ListTenantMembers(ctx, tenantID, MemberFilter{Search: in.Search, Status: in.Status, Offset: in.Offset, Limit: in.Limit})
		if err != nil {
			return nil, apiError("list tenant members", err)
		}
		views := make([]APITenantMember, len(members))
		for i, member := range members {
			views[i] = memberProfileView(member, fields)
		}
		return respx.List(ctx, views, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(members), Total: total}), nil
	})
	authz.Register(registrar, a, huma.Operation{OperationID: "iam-create-tenant-member", Method: http.MethodPost, Path: "/members", Summary: "Bind an existing principal to a tenant", Tags: []string{"directory"}, DefaultStatus: http.StatusCreated, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity}}, authz.Guard{Resource: "user", Verb: "create"}, func(ctx context.Context, in *createTenantMemberInput) (*respx.Body[APITenantMember], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		subject, err := guid.RequireID(in.Body.Subject)
		if err != nil {
			return nil, err
		}
		departmentID, err := guid.OptionalID(in.Body.DepartmentID)
		if err != nil {
			return nil, err
		}
		fields, err := svc.profileConstraint(ctx, tenantID)
		if err == nil {
			err = requireMemberProfileWrites(in.RawBody, fields, true)
		}
		if err != nil {
			return nil, apiError("write member fields", err)
		}
		member, err := svc.PutTenantMember(ctx, tenantID, subject, in.Body.DisplayName, in.Body.Email, departmentID, in.Body.Status)
		if err != nil {
			return nil, apiError("create tenant member", err)
		}
		return respx.OK(ctx, memberProfileView(member, fields)), nil
	})
	authz.Register(registrar, a, huma.Operation{OperationID: "iam-get-tenant-member", Method: http.MethodGet, Path: "/members/{principal_id}", Summary: "Get a tenant membership", Tags: []string{"directory"}, Errors: []int{http.StatusNotFound}}, authz.Guard{Resource: "user", Verb: "view"}, func(ctx context.Context, in *tenantMemberInput) (*respx.Body[APITenantMember], error) {
		tenantID, subject, err := tenantMemberRef(in)
		if err != nil {
			return nil, err
		}
		fields, err := svc.profileConstraint(ctx, tenantID)
		if err != nil {
			return nil, apiError("read member fields", err)
		}
		member, err := svc.GetTenantMember(ctx, tenantID, subject)
		if err != nil {
			return nil, apiError("get tenant member", err)
		}
		roles, err := svc.ListTenantMemberRoles(ctx, tenantID, subject)
		if err != nil {
			return nil, apiError("list tenant member roles", err)
		}
		out := memberProfileView(member, fields)
		out.RoleIDs = append([]guid.ID{}, roles...)
		return respx.OK(ctx, out), nil
	})
	authz.Register(registrar, a, huma.Operation{OperationID: "iam-update-tenant-member", Method: http.MethodPatch, Path: "/members/{principal_id}", Summary: "Update or disable a tenant membership", Tags: []string{"directory"}, Errors: []int{http.StatusNotFound, http.StatusConflict}}, authz.Guard{Resource: "user", Verb: "update"}, func(ctx context.Context, in *updateTenantMemberInput) (*respx.Body[APITenantMember], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		subject, err := guid.RequireID(in.Subject)
		if err != nil {
			return nil, err
		}
		fields, err := svc.profileConstraint(ctx, tenantID)
		if err == nil {
			err = requireMemberProfileWrites(in.RawBody, fields, false)
		}
		if err != nil {
			return nil, apiError("write member fields", err)
		}
		member, err := svc.GetTenantMember(ctx, tenantID, subject)
		if err != nil {
			return nil, apiError("get tenant member", err)
		}
		if in.Body.DisplayName != nil {
			member.DisplayName = *in.Body.DisplayName
		}
		if in.Body.Email != nil {
			member.Email = *in.Body.Email
		}
		if in.Body.DepartmentID != nil {
			departmentID, err := guid.OptionalID(*in.Body.DepartmentID)
			if err != nil {
				return nil, err
			}
			member.DepartmentID = departmentID
		}
		if in.Body.Status != nil {
			member.Status = *in.Body.Status
		}
		member, err = svc.PutTenantMember(ctx, tenantID, subject, member.DisplayName, member.Email, member.DepartmentID, member.Status)
		if err != nil {
			return nil, apiError("update tenant member", err)
		}
		return respx.OK(ctx, memberProfileView(member, fields)), nil
	})
	authz.Register(registrar, a, huma.Operation{OperationID: "iam-list-tenant-member-roles", Method: http.MethodGet, Path: "/members/{principal_id}/roles", Summary: "List role assignments for a tenant member", Tags: []string{"directory"}, Errors: []int{http.StatusNotFound}}, authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, in *listTenantMemberRolesInput) (*respx.Body[[]string], error) {
		tenantID, subject, err := tenantMemberRefIDs(in.TenantID, in.Subject)
		if err != nil {
			return nil, err
		}
		roles, total, err := svc.ListTenantMemberRolesPage(ctx, tenantID, subject, in.Offset, in.Limit)
		if err != nil {
			return nil, apiError("list tenant member roles", err)
		}
		return respx.List(ctx, guidStrings(roles), respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})
	authz.Register(registrar, a, huma.Operation{
		OperationID: "iam-list-member-authorization-sources", Method: http.MethodGet,
		Path:    "/members/{principal_id}/authorization-sources",
		Summary: "List effective authorization sources for a tenant member", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusConflict},
	}, authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, in *tenantMemberInput) (*respx.Body[APIMemberAuthorizationSnapshot], error) {
		tenantID, subject, err := tenantMemberRef(in)
		if err != nil {
			return nil, err
		}
		snapshot, err := svc.ListMemberAuthorizationSources(ctx, tenantID, subject)
		if err != nil {
			return nil, apiError("list member authorization sources", err)
		}
		return respx.OK(ctx, memberAuthorizationSnapshotFromDomain(snapshot)), nil
	})

	authz.Register(registrar, a, huma.Operation{OperationID: "iam-list-menus", Method: http.MethodGet, Path: "/menus", Summary: "List tenant menu metadata", Tags: []string{"access-control"}}, authz.Guard{Resource: "menu", Verb: "view"}, func(ctx context.Context, in *tenantInput) (*respx.Body[[]APIMenu], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		menus, err := svc.ListMenus(ctx, tenantID)
		if err != nil {
			return nil, apiError("list menus", err)
		}
		return respx.OK(ctx, menusFromDomain(menus)), nil
	})
	authz.Register(registrar, a, huma.Operation{OperationID: "iam-create-menu", Method: http.MethodPost, Path: "/menus", Summary: "Create a tenant menu", Tags: []string{"access-control"}, DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity}}, authz.Guard{Resource: "menu", Verb: "create"}, func(ctx context.Context, in *createMenuInput) (*respx.Body[APIMenu], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		parentID, err := guid.OptionalID(in.Body.ParentID)
		if err != nil {
			return nil, err
		}
		menu, err := svc.CreateMenu(ctx, Menu{TenantID: tenantID, ParentID: parentID, Label: in.Body.Label, Translations: in.Body.Translations, Route: in.Body.Route, Icon: in.Body.Icon, SortOrder: in.Body.SortOrder, PermissionCode: in.Body.PermissionCode, Status: in.Body.Status})
		if err != nil {
			return nil, apiError("create menu", err)
		}
		return respx.OK(ctx, menuFromDomain(menu)), nil
	})
	authz.RegisterOwned(registrar, a, huma.Operation{OperationID: "iam-get-menu", Method: http.MethodGet, Path: "/menus/{menu_id}", Summary: "Get a tenant menu", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound}}, "menu_id", svc.resolveMenuOwner, authz.Guard{Resource: "menu", Verb: "view"}, func(ctx context.Context, in *menuInput) (*respx.Body[APIMenu], error) {
		tenantID, menuID, err := menuRef(ctx, in)
		if err != nil {
			return nil, err
		}
		menu, err := svc.GetMenu(ctx, tenantID, menuID)
		if err != nil {
			return nil, apiError("get menu", err)
		}
		return respx.OK(ctx, menuFromDomain(menu)), nil
	})
	authz.RegisterOwned(registrar, a, huma.Operation{OperationID: "iam-update-menu", Method: http.MethodPatch, Path: "/menus/{menu_id}", Summary: "Update a tenant menu", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity}}, "menu_id", svc.resolveMenuOwner, authz.Guard{Resource: "menu", Verb: "update"}, func(ctx context.Context, in *updateMenuInput) (*respx.Body[APIMenu], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		menuID, err := guid.RequireID(in.MenuID)
		if err != nil {
			return nil, err
		}
		if in.Body.Placement != nil {
			if in.Body.Label != nil || in.Body.Translations != nil || in.Body.Route != nil || in.Body.Icon != nil || in.Body.SortOrder != nil || in.Body.PermissionCode != nil || in.Body.Status != nil {
				return nil, huma.Error422UnprocessableEntity(respx.InvalidArgument)
			}
			var parentID *guid.ID
			if in.Body.ParentID != nil {
				id, err := guid.OptionalID(*in.Body.ParentID)
				if err != nil {
					return nil, err
				}
				parentID = &id
			}
			relativeTo, err := guid.OptionalID(in.Body.Placement.RelativeTo)
			if err != nil {
				return nil, err
			}
			menu, err := svc.MoveMenu(ctx, tenantID, menuID, parentID, in.Body.Placement.Kind, relativeTo)
			if err != nil {
				return nil, apiError("move menu", err)
			}
			return respx.OK(ctx, menuFromDomain(menu)), nil
		}
		menu, err := svc.GetMenu(ctx, tenantID, menuID)
		if err != nil {
			return nil, apiError("get menu", err)
		}
		if in.Body.ParentID != nil {
			parentID, err := guid.OptionalID(*in.Body.ParentID)
			if err != nil {
				return nil, err
			}
			menu.ParentID = parentID
		}
		if in.Body.Label != nil {
			menu.Label = *in.Body.Label
		}
		if in.Body.Translations != nil {
			menu.Translations = *in.Body.Translations
		}
		if in.Body.Route != nil {
			menu.Route = *in.Body.Route
		}
		if in.Body.Icon != nil {
			menu.Icon = *in.Body.Icon
		}
		if in.Body.SortOrder != nil {
			menu.SortOrder = *in.Body.SortOrder
		}
		if in.Body.PermissionCode != nil {
			menu.PermissionCode = *in.Body.PermissionCode
		}
		if in.Body.Status != nil {
			menu.Status = *in.Body.Status
		}
		menu, err = svc.UpdateMenu(ctx, menu)
		if err != nil {
			return nil, apiError("update menu", err)
		}
		return respx.OK(ctx, menuFromDomain(menu)), nil
	})
	authz.RegisterOwned(registrar, a, huma.Operation{OperationID: "iam-delete-menu", Method: http.MethodDelete, Path: "/menus/{menu_id}", Summary: "Delete a tenant menu", Tags: []string{"access-control"}, Errors: []int{http.StatusNotFound, http.StatusConflict}}, "menu_id", svc.resolveMenuOwner, authz.Guard{Resource: "menu", Verb: "delete"}, func(ctx context.Context, in *menuInput) (*respx.Body[MutationResult], error) {
		tenantID, menuID, err := menuRef(ctx, in)
		if err != nil {
			return nil, err
		}
		if err := svc.DeleteMenu(ctx, tenantID, menuID); err != nil {
			return nil, apiError("delete menu", err)
		}
		return respx.OK(ctx, MutationResult{Changed: true, SyncStatus: "not_applicable"}), nil
	})
	authz.RegisterPrincipalTenantMember(registrar, a, huma.Operation{OperationID: "iam-effective-menus", Method: http.MethodGet, Path: "/principals/{principal_id}/menus", Summary: "Return the current member's authorized menu tree", Tags: []string{"access-control"}}, func(ctx context.Context, in *tenantInput) (*respx.Body[[]MenuItem], error) {
		claims, ok := authnext.FromContext(ctx)
		if !ok || claims == nil || claims.PrincipalID.Zero() {
			return nil, huma.Error401Unauthorized(respx.Unauthorized)
		}
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		menus, err := svc.EffectiveMenus(ctx, tenantID, claims.PrincipalID)
		if err != nil {
			return nil, apiError("effective menus", err)
		}
		return paginateList(ctx, menus, in.Offset, in.Limit), nil
	})
	// A platform administrator must discover their own platform permissions
	// before the first tenant exists. This self-service route deliberately has no
	// tenant context; tenant permissions use the tenant-scoped route below.
	authz.RegisterPrincipalAuthenticated(registrar, a, huma.Operation{
		OperationID: "iam-effective-platform-permissions", Method: http.MethodGet,
		Path:    "/principals/{principal_id}/permissions",
		Summary: "Return my effective platform permission codes", Tags: []string{"account"},
	}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]string], error) {
		claims, ok := authnext.FromContext(ctx)
		if !ok || claims == nil || claims.PrincipalID.Zero() {
			return nil, huma.Error401Unauthorized(respx.Unauthorized)
		}
		codes, err := svc.EffectivePermissions(ctx, 0, claims.PrincipalID)
		if err != nil {
			return nil, apiError("effective permissions", err)
		}
		return respx.OK(ctx, codes), nil
	})
	authz.RegisterPrincipalTenantMember(registrar, a, huma.Operation{
		OperationID: "iam-effective-tenant-permissions", Method: http.MethodGet,
		Path:    "/principals/{principal_id}/permissions",
		Summary: "Return my effective tenant permission codes", Tags: []string{"access-control"},
	}, func(ctx context.Context, in *myPermissionsInput) (*respx.Body[[]string], error) {
		claims, ok := authnext.FromContext(ctx)
		if !ok || claims == nil || claims.PrincipalID.Zero() {
			return nil, huma.Error401Unauthorized(respx.Unauthorized)
		}
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		codes, err := svc.EffectivePermissions(ctx, tenantID, claims.PrincipalID)
		if err != nil {
			return nil, apiError("effective permissions", err)
		}
		return respx.OK(ctx, codes), nil
	})
	authz.RegisterPrincipalAuthenticated(registrar, a, huma.Operation{
		OperationID: "iam-effective-entity-permissions", Method: http.MethodGet,
		Path:    "/tenants/{tenant_id}/entities/{entity_id}/principals/{principal_id}/permissions",
		Summary: "Return my effective entity permission codes", Tags: []string{"access-control"},
	}, func(ctx context.Context, in *myEntityPermissionsInput) (*respx.Body[[]string], error) {
		claims, ok := authnext.FromContext(ctx)
		if !ok || claims == nil || claims.PrincipalID.Zero() {
			return nil, huma.Error401Unauthorized(respx.Unauthorized)
		}
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		entityID, err := guid.RequireID(in.EntityID)
		if err != nil {
			return nil, err
		}
		codes, err := svc.EffectiveEntityPermissions(ctx, tenantID, entityID, claims.PrincipalID)
		if err != nil {
			return nil, apiError("effective entity permissions", err)
		}
		return respx.OK(ctx, codes), nil
	})
}

func roleFromDomain(role Role) APIRole {
	return APIRole(role)
}

func rolesFromDomain(roles []Role) []APIRole {
	out := make([]APIRole, 0, len(roles))
	for _, role := range roles {
		out = append(out, roleFromDomain(role))
	}
	return out
}

func rolePermissionGrantFromDomain(grant RolePermissionGrant) APIRolePermissionGrant {
	return APIRolePermissionGrant{PermissionCode: grant.PermissionCode, Condition: policyCondition(grant.Condition), CreatedAt: grant.CreatedAt}
}

func rolePermissionGrantsFromDomain(grants []RolePermissionGrant) []APIRolePermissionGrant {
	out := make([]APIRolePermissionGrant, 0, len(grants))
	for _, grant := range grants {
		out = append(out, rolePermissionGrantFromDomain(grant))
	}
	return out
}

func entityFromDomain(entity Entity) APIEntity {
	return APIEntity(entity)
}

func entitiesFromDomain(entities []Entity) []APIEntity {
	out := make([]APIEntity, 0, len(entities))
	for _, entity := range entities {
		out = append(out, entityFromDomain(entity))
	}
	return out
}

func entityBindingFromDomain(binding EntityRoleBinding) APIEntityRoleBinding {
	out := APIEntityRoleBinding{
		EntityID: binding.EntityID, RoleID: binding.RoleID, PrincipalID: binding.PrincipalID,
		Effect: binding.Effect, CreatedAt: binding.CreatedAt,
	}
	if !binding.ExpiresAt.IsZero() {
		expiresAt := binding.ExpiresAt
		out.ExpiresAt = &expiresAt
	}
	return out
}

func entityBindingsFromDomain(bindings []EntityRoleBinding) []APIEntityRoleBinding {
	out := make([]APIEntityRoleBinding, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, entityBindingFromDomain(binding))
	}
	return out
}

func memberAuthorizationSnapshotFromDomain(snapshot MemberAuthorizationSnapshot) APIMemberAuthorizationSnapshot {
	out := APIMemberAuthorizationSnapshot{
		Subject: snapshot.Subject.String(), Revision: snapshot.Revision, EvaluatedAt: snapshot.EvaluatedAt,
		Sources: make([]APIMemberAuthorizationSource, 0, len(snapshot.Sources)),
	}
	for _, source := range snapshot.Sources {
		expiresAt := cloneOptionalTime(source.ExpiresAt)
		out.Sources = append(out.Sources, APIMemberAuthorizationSource{
			Type: source.Type, SourceID: source.SourceID, SourceName: source.SourceName,
			RoleID: source.RoleID, RoleName: source.RoleName,
			PermissionCodes: append([]string(nil), source.PermissionCodes...), Effect: source.Effect,
			ScopeType: source.ScopeType, ScopeID: source.ScopeID, ScopeName: source.ScopeName,
			DataScope: source.DataScope, DepartmentIDs: append([]guid.ID{}, source.DepartmentIDs...), ExpiresAt: expiresAt,
			Inherited: source.Inherited, Relation: source.Relation,
			RelationshipPath: append([]authz.RelationshipStep{}, source.RelationshipPath...),
		})
	}
	return out
}

func directoryBindingsFromDomain(bindings []RoleDirectoryBinding) []APIRoleDirectoryBinding {
	out := make([]APIRoleDirectoryBinding, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, APIRoleDirectoryBinding(binding))
	}
	return out
}

func memberFromDomain(member TenantMember) APITenantMember {
	out := APITenantMember{TenantID: member.TenantID, Subject: member.PrincipalID, DisplayName: member.DisplayName, Email: member.Email, DepartmentID: member.DepartmentID, Status: member.Status, CreatedAt: member.CreatedAt, UpdatedAt: member.UpdatedAt}
	if !member.DisabledAt.IsZero() {
		value := member.DisabledAt
		out.DisabledAt = &value
	}
	return out
}

func roleDataScopeFromDomain(scope RoleDataScope) APIRoleDataScope {
	out := APIRoleDataScope{RoleID: scope.RoleID, Scope: scope.Scope, DepartmentIDs: append([]guid.ID{}, scope.DepartmentIDs...)}
	if !scope.UpdatedAt.IsZero() {
		updatedAt := scope.UpdatedAt
		out.UpdatedAt = &updatedAt
	}
	return out
}

func menuFromDomain(menu Menu) APIMenu {
	return APIMenu{ID: menu.ID, TenantID: menu.TenantID, ParentID: menu.ParentID, Label: menu.Label, Translations: menu.Translations, Route: menu.Route, Icon: menu.Icon, SortOrder: menu.SortOrder, PermissionCode: menu.PermissionCode, Status: menu.Status, CreatedAt: menu.CreatedAt, UpdatedAt: menu.UpdatedAt}
}
func menusFromDomain(menus []Menu) []APIMenu {
	out := make([]APIMenu, 0, len(menus))
	for _, menu := range menus {
		out = append(out, menuFromDomain(menu))
	}
	return out
}

func apiError(operation string, err error) error {
	if respx.IsStatusError(err) {
		return err
	}
	if errors.Is(err, ErrMemberConflict) {
		return huma.Error409Conflict(corei18n.MembershipAlreadyExists)
	}
	switch {
	case errors.Is(err, ErrFieldPolicyConflict):
		return huma.Error409Conflict(corei18n.ResourceConflict)
	case errors.Is(err, ErrRoleNotFound):
		return huma.Error404NotFound(corei18n.ResourceNotFound)
	case errors.Is(err, ErrRoleNameConflict):
		return huma.Error409Conflict(corei18n.RoleNameConflict)
	case errors.Is(err, ErrMemberNotFound), errors.Is(err, ErrMenuNotFound):
		return huma.Error404NotFound(corei18n.ResourceNotFound)
	case errors.Is(err, ErrMenuConflict), errors.Is(err, ErrMenuHasChildren):
		return huma.Error409Conflict(corei18n.ResourceConflict)
	case errors.Is(err, ErrMemberInactive):
		return huma.Error409Conflict(corei18n.TenantMemberInactive)
	case errors.Is(err, ErrDirectoryAssigneeNotFound):
		return huma.Error404NotFound(corei18n.DirectoryAssigneeNotFound)
	case errors.Is(err, ErrDirectoryAssigneeInactive):
		return huma.Error409Conflict(corei18n.DirectoryAssigneeInactive)
	case errors.Is(err, ErrLastTenantAdministrator):
		return huma.Error409Conflict(corei18n.LastTenantAdministrator)
	case errors.Is(err, ErrEntityNotFound):
		return huma.Error404NotFound(corei18n.EntityNotFound)
	case errors.Is(err, ErrEntityConflict):
		return huma.Error409Conflict(corei18n.EntityConflict)
	case errors.Is(err, ErrEntityHasChildren):
		return huma.Error409Conflict(corei18n.EntityHasChildren)
	case errors.Is(err, ErrEntityHasBindings):
		return huma.Error409Conflict(corei18n.EntityHasRoleBindings)
	case errors.Is(err, ErrEntityHasRelationships):
		return huma.Error409Conflict(corei18n.EntityHasRelationships)
	case errors.Is(err, ErrEntityHasFiles):
		return huma.Error409Conflict(corei18n.EntityHasFiles)
	case errors.Is(err, ErrEntityHierarchy):
		return huma.Error409Conflict(corei18n.EntityHierarchyConflict)
	case errors.Is(err, ErrInvalidRelationship):
		return huma.Error422UnprocessableEntity(corei18n.InvalidRelationship)
	case errors.Is(err, ErrRelationshipSubjectMissing):
		return huma.Error404NotFound(corei18n.RelationshipSubjectNotFound)
	case errors.Is(err, ErrRelationshipSubjectInactive):
		return huma.Error409Conflict(corei18n.RelationshipSubjectInactive)
	case errors.Is(err, ErrRelationshipResourceInactive):
		return huma.Error409Conflict(corei18n.RelationshipResourceInactive)
	case errors.Is(err, ErrRelationshipHierarchy):
		return huma.Error409Conflict(corei18n.RelationshipHierarchyConflict)
	case errors.Is(err, ErrInvalidRelationshipWindow):
		return huma.Error422UnprocessableEntity(corei18n.InvalidRelationshipWindow)
	case errors.Is(err, ErrInvalidRelationshipCondition):
		return huma.Error422UnprocessableEntity(corei18n.InvalidRelationshipCondition)
	case errors.Is(err, ErrInvalidResourceAuthorization):
		return huma.Error422UnprocessableEntity(corei18n.InvalidResourceAuthorization)
	case errors.Is(err, ErrAuthorizationChanged):
		return huma.Error503ServiceUnavailable(corei18n.AuthorizationPolicyChanged)
	case errors.Is(err, ErrInvalidRoleDataScope):
		return huma.Error422UnprocessableEntity(corei18n.InvalidRoleDataScope)
	case errors.Is(err, ErrRoleScopeDepartmentNeeded):
		return huma.Error422UnprocessableEntity(corei18n.RoleDataScopeDepartmentRequired)
	case errors.Is(err, ErrRoleScopeDepartmentsExtra):
		return huma.Error422UnprocessableEntity(corei18n.RoleDataScopeDepartmentsNotAllowed)
	case errors.Is(err, ErrRoleScopeDepartmentMissing):
		return huma.Error404NotFound(corei18n.RoleScopeDepartmentNotFound)
	case errors.Is(err, ErrRoleScopeDepartmentInactive):
		return huma.Error409Conflict(corei18n.RoleScopeDepartmentInactive)
	case errors.Is(err, ErrMemberDepartmentMissing):
		return huma.Error404NotFound(corei18n.MemberDepartmentNotFound)
	case errors.Is(err, ErrMemberDepartmentInactive):
		return huma.Error409Conflict(corei18n.MemberDepartmentInactive)
	case errors.Is(err, ErrPermissionNotFound):
		return huma.Error422UnprocessableEntity(corei18n.PermissionNotFound)
	case errors.Is(err, ErrRolePermissionNotGranted):
		return huma.Error404NotFound(corei18n.RolePermissionNotGranted)
	case errors.Is(err, ErrInvalidRolePermissionCondition):
		return huma.Error422UnprocessableEntity(corei18n.InvalidRolePermissionCondition)
	case errors.Is(err, authz.ErrFieldsUnavailable):
		return huma.Error503ServiceUnavailable(respx.ServiceUnavailable)
	case errors.Is(err, ErrPrivilegeEscalation), errors.Is(err, authz.ErrFieldDenied):
		return huma.Error403Forbidden(corei18n.PrivilegeEscalationDenied)
	case errors.Is(err, ErrInvalidArgument):
		return huma.Error422UnprocessableEntity(corei18n.InvalidRequest)
	case errors.Is(err, ErrUnavailable):
		return huma.Error503ServiceUnavailable(corei18n.Unavailable)
	default:
		slog.Error("iam request failed", "operation", operation, "err", err)
		return huma.Error500InternalServerError(respx.InternalServerError)
	}
}

// paginateList applies offset/limit to a list result, defaulting to all
// records when both are zero. This prevents unbounded response sizes
// on collections that can grow large (roles, entities, menus).
func paginateList[T any](ctx context.Context, items []T, offset, limit int) *respx.Body[[]T] {
	if offset < 0 {
		offset = 0
	}
	if limit < 0 || limit > 200 {
		limit = 0
	}
	total := len(items)
	if offset >= total {
		return respx.List(ctx, []T{}, respx.Page{Offset: offset, Limit: limit, Total: int64(total)})
	}
	if limit > 0 {
		end := offset + limit
		if end > total {
			end = total
		}
		items = items[offset:end]
	} else if offset > 0 {
		items = items[offset:]
	}
	return respx.List(ctx, items, respx.Page{Offset: offset, Limit: limit, Total: int64(total)})
}
