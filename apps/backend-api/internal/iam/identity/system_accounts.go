package identity

import (
	"context"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type AccountWorkspace struct {
	ScopeType string  `json:"scope_type"`
	ScopeID   guid.ID `json:"scope_id"`
	TenantID  guid.ID `json:"tenant_id,omitempty"`
	Name      string  `json:"name"`
}

func (s *Service) AccountWorkspaces(ctx context.Context, target guid.ID) ([]AccountWorkspace, error) {
	principal, err := s.GetGlobal(ctx, target)
	if err != nil {
		return nil, err
	}
	items := []AccountWorkspace{{ScopeType: "personal", ScopeID: target, Name: principal.DisplayName}}
	if principal.Status != "active" {
		return items, nil
	}
	if s.avatarPermissions == nil {
		return nil, ErrUnavailable
	}
	for _, action := range s.permissionRegistry.All() {
		if action.Scope != authz.ActionScopeSystem {
			continue
		}
		allowed, err := s.avatarPermissions.CheckPlatform(ctx, action.Code, target)
		if err != nil {
			return nil, err
		}
		if allowed {
			items = append(items, AccountWorkspace{ScopeType: "system", ScopeID: 1, Name: "System"})
			break
		}
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	if repo.foreign == nil || !repo.foreign.directory.Bound() || !repo.foreign.access.Bound() {
		return nil, ErrUnavailable
	}
	roots, members := repo.foreign.directory.Tenants.String(), repo.foreign.access.TenantMembers.String()
	var owned []struct {
		ID              guid.ID
		ScopeKind, Name string
	}
	err = repo.writer().WithContext(ctx).Table(roots+" AS root").Select("root.id, root.scope_kind, root.name").
		Joins("JOIN "+members+" AS member ON member.tenant_id = root.id AND member.principal_id = ? AND member.status = 'active'", target).
		Where("root.status = 'active' AND root.scope_kind IN ?", []string{"tenant", "platform"}).Order("root.name ASC, root.id ASC").Scan(&owned).Error
	if err != nil {
		return nil, err
	}
	var tenants []guid.ID
	for _, owner := range owned {
		items = append(items, AccountWorkspace{ScopeType: owner.ScopeKind, ScopeID: owner.ID, Name: owner.Name})
		if owner.ScopeKind == "tenant" {
			tenants = append(tenants, owner.ID)
		}
	}
	if len(tenants) != 0 {
		var entities []struct {
			ID, TenantID guid.ID
			Name         string
		}
		err := repo.writer().WithContext(ctx).Table(repo.foreign.access.Entities.String()).Select("id, tenant_id, name").Where("tenant_id IN ? AND status = 'active'", tenants).Order("name ASC, id ASC").Scan(&entities).Error
		if err != nil {
			return nil, err
		}
		for _, entity := range entities {
			items = append(items, AccountWorkspace{ScopeType: "entity", ScopeID: entity.ID, TenantID: entity.TenantID, Name: entity.Name})
		}
	}
	return items, nil
}

func (s *Service) ListGlobal(ctx context.Context, search string, limit, offset int) ([]Principal, int64, error) {
	return s.ListGlobalCategory(ctx, search, limit, offset, "user", 0, "")
}

func (s *Service) ListGlobalCategory(ctx context.Context, search string, limit, offset int, category string, ownerID guid.ID, status string) ([]Principal, int64, error) {
	if limit < 1 || limit > 200 || offset < 0 || len(search) > 200 {
		return nil, 0, ErrInvalid
	}
	if category == "" {
		category = "user"
	}
	if category != "user" && category != "system" && category != "platform" && category != "tenant" {
		return nil, 0, ErrInvalid
	}
	if ownerID < 0 || (ownerID > 0 && category != "platform" && category != "tenant") {
		return nil, 0, ErrInvalid
	}
	if status != "" && status != "active" && status != "disabled" {
		return nil, 0, ErrInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	rows, total, err := repo.listPrincipalsFiltered(ctx, 0, search, limit, offset, category, ownerID, status)
	if err != nil {
		return nil, 0, err
	}
	items := make([]Principal, len(rows))
	for i, row := range rows {
		items[i] = principalFromRow(row)
	}
	return items, total, nil
}

func (s *Service) GetGlobal(ctx context.Context, id guid.ID) (Principal, error) {
	if id.Zero() {
		return Principal{}, ErrInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return Principal{}, err
	}
	row, err := repo.getPrincipal(ctx, 0, id)
	if err != nil {
		return Principal{}, err
	}
	return principalFromRow(row), nil
}

// System account profiles are global identities; tenant field policies have no
// owner here. Their endpoint authority controls access to the default projection.
func systemPrincipalView(principal Principal) PrincipalView {
	fields := authz.FieldConstraint{Fields: map[string]authz.FieldDecision{}}
	for _, field := range authz.ProfileFields() {
		fields.Fields[field.Name] = authz.DecideField(field, nil, nil)
	}
	return profileView(principal, fields)
}

type systemPrincipalListInput struct {
	Category string `query:"category" enum:"system,platform,tenant,user" default:"user" doc:"overlapping account relationship category; user lists all human identities"`
	OwnerID  string `query:"owner_id" doc:"optional explicit operating-platform or tenant owner for category filtering"`
	Status   string `query:"status" enum:"active,disabled" doc:"global account status; omitted includes disabled accounts"`
	Search   string `query:"search" maxLength:"200"`
	Limit    int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset   int    `query:"offset" default:"0" minimum:"0"`
}

type systemPrincipalIDInput struct {
	ID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type systemPrincipalCreateInput struct {
	Body struct {
		LoginName   string `json:"login_name" minLength:"1" maxLength:"200"`
		Password    string `json:"password" minLength:"12" maxLength:"1024"`
		DisplayName string `json:"display_name,omitempty" maxLength:"128"`
		Email       string `json:"email,omitempty" maxLength:"320"`
	}
}

type systemPrincipalUpdateInput struct {
	ID   string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body struct {
		DisplayName *string `json:"display_name,omitempty" minLength:"1" maxLength:"128"`
		Email       *string `json:"email,omitempty" maxLength:"320"`
	}
}

func registerSystemPrincipalHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "identity-system-principal-workspaces", Method: http.MethodGet, Path: "/system/principals/{principal_id}/workspaces",
		Summary: "List an account's admitted workspaces", Tags: []string{"system"}, Errors: []int{http.StatusNotFound},
	}, authz.Guard{Resource: "system_account", Verb: "view"}, func(ctx context.Context, in *systemPrincipalIDInput) (*respx.Body[[]AccountWorkspace], error) {
		id, err := guid.RequireID(in.ID)
		if err != nil {
			return nil, err
		}
		items, err := service.AccountWorkspaces(ctx, id)
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, items), nil
	})
	// The system_account resource spans every global account. The "System
	// accounts" menu (/system/accounts/system) is one category of this list,
	// selected by the category parameter, not a separate resource.
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "identity-system-list-principals", Method: http.MethodGet, Path: "/system/principals",
		Summary: "List global accounts", Tags: []string{"system"},
		Description: "Lists global accounts of one category (user when omitted). The system_account permission covers every category; the System accounts menu shows only the system category.",
	}, authz.Guard{Resource: "system_account", Verb: "view"}, func(ctx context.Context, in *systemPrincipalListInput) (*respx.Body[principalListData], error) {
		var ownerID guid.ID
		if in.OwnerID != "" {
			var err error
			ownerID, err = guid.RequireID(in.OwnerID)
			if err != nil {
				return nil, err
			}
		}
		items, total, err := service.ListGlobalCategory(ctx, in.Search, in.Limit, in.Offset, in.Category, ownerID, in.Status)
		if err != nil {
			return nil, identityError(err)
		}
		views := make([]PrincipalView, len(items))
		for i, item := range items {
			views[i] = systemPrincipalView(item)
		}
		return respx.OK(ctx, principalListData{Items: views, Total: total}), nil
	})
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "identity-system-create-principal", Method: http.MethodPost, Path: "/system/principals",
		Summary: "Create a global account", Tags: []string{"system"}, DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "system_account", Verb: "create"}, func(ctx context.Context, in *systemPrincipalCreateInput) (*respx.Body[PrincipalView], error) {
		item, err := service.CreateGlobal(ctx, in.Body.LoginName, in.Body.Password, in.Body.DisplayName, in.Body.Email)
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, systemPrincipalView(item)), nil
	})
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "identity-system-get-principal", Method: http.MethodGet, Path: "/system/principals/{principal_id}",
		Summary: "Get a global account", Tags: []string{"system"}, Errors: []int{http.StatusNotFound},
	}, authz.Guard{Resource: "system_account", Verb: "view"}, func(ctx context.Context, in *systemPrincipalIDInput) (*respx.Body[PrincipalView], error) {
		id, err := guid.RequireID(in.ID)
		if err != nil {
			return nil, err
		}
		item, err := service.GetGlobal(ctx, id)
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, systemPrincipalView(item)), nil
	})
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "identity-system-update-principal", Method: http.MethodPatch, Path: "/system/principals/{principal_id}",
		Summary: "Update a global account profile", Tags: []string{"system"}, Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "system_account", Verb: "update"}, func(ctx context.Context, in *systemPrincipalUpdateInput) (*respx.Body[PrincipalView], error) {
		id, err := guid.RequireID(in.ID)
		if err != nil {
			return nil, err
		}
		item, err := service.UpdateGlobal(ctx, id, in.Body.DisplayName, in.Body.Email)
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, systemPrincipalView(item)), nil
	})
}
