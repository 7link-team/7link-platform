package core

import (
	"context"
	"net/http"
	"strings"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type systemMenuCreateInput struct {
	Body struct {
		Translations   map[string]string `json:"translations,omitempty"`
		ParentID       string            `json:"parent_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19"`
		Label          string            `json:"label" minLength:"1" maxLength:"128"`
		Route          string            `json:"route,omitempty" maxLength:"512"`
		Icon           string            `json:"icon,omitempty" maxLength:"64"`
		SortOrder      int               `json:"sort_order,omitempty" minimum:"-100000" maximum:"100000"`
		PermissionCode string            `json:"permission_code,omitempty" maxLength:"128"`
		Status         MenuStatus        `json:"status,omitempty" enum:"active,disabled" default:"active"`
	}
}

func (s *Service) requireSystemMenuBinding(ctx context.Context) error {
	allowed, err := s.checker.CheckPlatform(ctx, "system_menu_bind_permission", authnext.PrincipalIDFromContext(ctx))
	if err != nil {
		return apiError("check system menu permission binding", err)
	}
	if !allowed {
		return huma.Error403Forbidden(respx.Forbidden)
	}
	return nil
}

func registerSystemMenuHTTP(api huma.API, svc *Service, registrar *authz.Registrar) {
	op := func(id, method, route, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: route, Summary: summary, Tags: []string{"system-menus"}}
	}
	authz.RegisterPlatform(registrar, api, op("iam-system-menus", http.MethodGet, "/system/menus", "List system menus"), authz.Guard{Resource: "system_menu", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]APIMenu], error) {
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system menus", err)
		}
		menus, err := s.ListMenus(ctx, owner)
		if err != nil {
			return nil, apiError("list system menus", err)
		}
		return respx.OK(ctx, menusFromDomain(menus)), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-menu-catalog", http.MethodGet, "/system/menu-catalog", "List system menu declarations"), authz.Guard{Resource: "system_menu", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]MenuDeclaration], error) {
		return respx.OK(ctx, svc.SystemMenuCatalog(ctx)), nil
	})
	authz.RegisterAuthenticated(registrar, api, op("iam-system-navigation", http.MethodGet, "/system/navigation", "Return my system menu tree"), func(ctx context.Context, _ *struct{}) (*respx.Body[[]MenuItem], error) {
		claims, _ := authnext.FromContext(ctx)
		if claims != nil && claims.AccountView != nil && (claims.AccountView.ScopeType != "system" || claims.AccountView.ScopeID != 1) {
			return nil, huma.Error403Forbidden(respx.Forbidden)
		}
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system navigation", err)
		}
		menus, err := s.EffectiveMenus(ctx, owner, authnext.PrincipalIDFromContext(ctx))
		if err != nil {
			return nil, apiError("system navigation", err)
		}
		return respx.OK(ctx, menus), nil
	})
	authz.RegisterPlatform(registrar, api, created(op("iam-system-menu-create", http.MethodPost, "/system/menus", "Create a system menu")), authz.Guard{Resource: "system_menu", Verb: "create"}, func(ctx context.Context, in *systemMenuCreateInput) (*respx.Body[APIMenu], error) {
		if strings.TrimSpace(in.Body.PermissionCode) != "" {
			if err := svc.requireSystemMenuBinding(ctx); err != nil {
				return nil, err
			}
		}
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system menus", err)
		}
		parent, err := guid.OptionalID(in.Body.ParentID)
		if err != nil {
			return nil, err
		}
		menu, err := s.CreateMenu(ctx, Menu{TenantID: owner, ParentID: parent, Label: in.Body.Label, Route: in.Body.Route, Icon: in.Body.Icon, SortOrder: in.Body.SortOrder, PermissionCode: in.Body.PermissionCode, Status: in.Body.Status, Translations: in.Body.Translations})
		if err != nil {
			return nil, apiError("create system menu", err)
		}
		return respx.OK(ctx, menuFromDomain(menu)), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-menu", http.MethodGet, "/system/menus/{menu_id}", "Get a system menu"), authz.Guard{Resource: "system_menu", Verb: "view"}, func(ctx context.Context, in *menuInput) (*respx.Body[APIMenu], error) {
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system menus", err)
		}
		id, err := guid.RequireID(in.MenuID)
		if err != nil {
			return nil, err
		}
		menu, err := s.GetMenu(ctx, owner, id)
		if err != nil {
			return nil, apiError("get system menu", err)
		}
		return respx.OK(ctx, menuFromDomain(menu)), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-menu-update", http.MethodPatch, "/system/menus/{menu_id}", "Update a system menu"), authz.Guard{Resource: "system_menu", Verb: "update"}, func(ctx context.Context, in *updateMenuInput) (*respx.Body[APIMenu], error) {
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system menus", err)
		}
		id, err := guid.RequireID(in.MenuID)
		if err != nil {
			return nil, err
		}
		if in.Body.Placement != nil {
			if in.Body.Label != nil || in.Body.Translations != nil || in.Body.Route != nil || in.Body.Icon != nil || in.Body.SortOrder != nil || in.Body.PermissionCode != nil || in.Body.Status != nil {
				return nil, huma.Error422UnprocessableEntity(respx.InvalidArgument)
			}
			var parent *guid.ID
			if in.Body.ParentID != nil {
				parsed, err := guid.OptionalID(*in.Body.ParentID)
				if err != nil {
					return nil, err
				}
				parent = &parsed
			}
			relative, err := guid.OptionalID(in.Body.Placement.RelativeTo)
			if err != nil {
				return nil, err
			}
			menu, err := s.MoveMenu(ctx, owner, id, parent, in.Body.Placement.Kind, relative)
			if err != nil {
				return nil, apiError("move system menu", err)
			}
			return respx.OK(ctx, menuFromDomain(menu)), nil
		}
		menu, err := s.GetMenu(ctx, owner, id)
		if err != nil {
			return nil, apiError("get system menu", err)
		}
		if in.Body.PermissionCode != nil && strings.TrimSpace(*in.Body.PermissionCode) != menu.PermissionCode {
			if err := svc.requireSystemMenuBinding(ctx); err != nil {
				return nil, err
			}
		}
		if in.Body.ParentID != nil {
			parent, err := guid.OptionalID(*in.Body.ParentID)
			if err != nil {
				return nil, err
			}
			menu.ParentID = parent
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
		menu, err = s.UpdateMenu(ctx, menu)
		if err != nil {
			return nil, apiError("update system menu", err)
		}
		return respx.OK(ctx, menuFromDomain(menu)), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-menu-delete", http.MethodDelete, "/system/menus/{menu_id}", "Delete a system menu"), authz.Guard{Resource: "system_menu", Verb: "delete"}, func(ctx context.Context, in *menuInput) (*respx.Body[MutationResult], error) {
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system menus", err)
		}
		id, err := guid.RequireID(in.MenuID)
		if err != nil {
			return nil, err
		}
		if err := s.DeleteMenu(ctx, owner, id); err != nil {
			return nil, apiError("delete system menu", err)
		}
		return respx.OK(ctx, MutationResult{Changed: true, SyncStatus: "not_applicable"}), nil
	})
}
