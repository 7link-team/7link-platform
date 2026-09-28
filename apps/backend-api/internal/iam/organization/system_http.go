package organization

import (
	"context"
	"errors"
	"net/http"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type systemDepartmentPageInput struct {
	Offset int `query:"offset" minimum:"0" default:"0"`
	Limit  int `query:"limit" minimum:"1" maximum:"200" default:"50"`
}
type systemDepartmentCreateInput struct {
	Body struct {
		ParentID  string `json:"parent_id,omitempty" maxLength:"19"`
		Name      string `json:"name" minLength:"1" maxLength:"128"`
		Status    string `json:"status,omitempty" enum:"active,disabled" default:"active"`
		SortOrder int    `json:"sort_order,omitempty" minimum:"0" maximum:"1000000"`
	}
}

// System routes name the selected root owner_id, as core's system scope routes
// do: a tenant_id path parameter would read as tenant-gated admission.
type systemOwnerDepartmentsInput struct {
	OwnerID string `path:"owner_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"organization root the route acts in: a tenant or an operating platform"`
	Offset  int    `query:"offset" minimum:"0" default:"0"`
	Limit   int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

func registerSystemOwnerDepartmentsHTTP(api huma.API, svc *Service, registrar *authz.Registrar) {
	list := func(ctx context.Context, value, kind string, offset, limit int) (*respx.Body[[]Department], error) {
		id, err := guid.RequireID(value)
		if err != nil {
			return nil, err
		}
		repo, err := svc.repo.pinWriter()
		if err != nil {
			return nil, departmentError(err)
		}
		var root model.Tenant
		if err := repo.writer().WithContext(ctx).Table(repo.names.Tenants.String()).Where("id = ? AND scope_kind = ?", id, kind).Take(&root).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, departmentError(ErrNotFound)
			}
			return nil, departmentError(err)
		}
		items, total, err := svc.ListPage(ctx, id, offset, limit)
		if err != nil {
			return nil, departmentError(err)
		}
		return respx.List(ctx, items, respx.Page{Offset: offset, Limit: limit, Total: total}), nil
	}
	authz.RegisterPlatform(registrar, api, huma.Operation{OperationID: "organization-system-tenant-departments", Method: http.MethodGet, Path: "/system/tenants/{owner_id}/departments", Summary: "List departments of an explicitly selected tenant", Tags: []string{"system"}}, authz.Guard{Resource: "system_account", Verb: "view"}, func(ctx context.Context, in *systemOwnerDepartmentsInput) (*respx.Body[[]Department], error) {
		return list(ctx, in.OwnerID, ScopeTenant, in.Offset, in.Limit)
	})
	authz.RegisterPlatform(registrar, api, huma.Operation{OperationID: "organization-system-platform-departments", Method: http.MethodGet, Path: "/system/operating-platforms/{owner_id}/departments", Summary: "List departments of an explicitly selected operating platform", Tags: []string{"system"}}, authz.Guard{Resource: "system_account", Verb: "view"}, func(ctx context.Context, in *systemOwnerDepartmentsInput) (*respx.Body[[]Department], error) {
		return list(ctx, in.OwnerID, ScopePlatform, in.Offset, in.Limit)
	})
}

func (s *Service) systemDepartmentService(ctx context.Context) (*Service, guid.ID, error) {
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	var root model.Tenant
	err = repo.writer().WithContext(ctx).Table(repo.names.Tenants.String()).Where("slug = ? AND scope_kind = ? AND status = ?", core.SystemRootSlug, ScopeSystem, TenantActive).Take(&root).Error
	if err != nil {
		return nil, 0, err
	}
	clone := *s
	clonedRepo := *s.repo
	clonedRepo.appender = func(ctx context.Context, db *gorm.DB, event audit.DomainEvent) error {
		event.TenantID = 0
		if event.Detail == nil {
			event.Detail = map[string]any{}
		}
		event.Detail["scope_kind"], event.Detail["scope_id"] = ScopeSystem, root.ID.String()
		return s.repo.appender(ctx, db, event)
	}
	clone.repo = &clonedRepo
	return &clone, root.ID, nil
}

func (s *Service) systemDepartmentMutation(ctx context.Context, permission string, mutate func(*Service, guid.ID) error) error {
	system, owner, err := s.systemDepartmentService(ctx)
	if err != nil {
		return err
	}
	return system.repo.runInTx(ctx, func(repo *Repository) error {
		if system.systemPermissions == nil {
			return ErrUnavailable
		}
		allowed, err := system.systemPermissions(ctx, repo.writer(), permission, authn.PrincipalIDFromContext(ctx))
		if err != nil {
			return err
		}
		if !allowed {
			return core.ErrPrivilegeEscalation
		}
		clone := *system
		clone.repo = repo
		return mutate(&clone, owner)
	})
}

func registerSystemDepartmentHTTP(api huma.API, svc *Service, registrar *authz.Registrar) {
	registerSystemOwnerDepartmentsHTTP(api, svc, registrar)
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"system"}}
	}
	authz.RegisterPlatform(registrar, api, op("organization-system-departments", http.MethodGet, "/system/departments", "List system departments"), authz.Guard{Resource: "system_department", Verb: "view"}, func(ctx context.Context, in *systemDepartmentPageInput) (*respx.Body[[]Department], error) {
		s, owner, err := svc.systemDepartmentService(ctx)
		if err != nil {
			return nil, departmentError(err)
		}
		items, total, err := s.ListPage(ctx, owner, in.Offset, in.Limit)
		if err != nil {
			return nil, departmentError(err)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})
	authz.RegisterPlatform(registrar, api, created(op("organization-system-department-create", http.MethodPost, "/system/departments", "Create a system department")), authz.Guard{Resource: "system_department", Verb: "create"}, func(ctx context.Context, in *systemDepartmentCreateInput) (*respx.Body[Department], error) {
		parent, err := guid.OptionalID(in.Body.ParentID)
		if err != nil {
			return nil, err
		}
		var department Department
		err = svc.systemDepartmentMutation(ctx, "system_department_create", func(tx *Service, owner guid.ID) error {
			var err error
			department, err = tx.Create(ctx, owner, CreateDepartment{ParentID: parent, Name: in.Body.Name, Status: in.Body.Status, SortOrder: in.Body.SortOrder})
			return err
		})
		if err != nil {
			return nil, departmentError(err)
		}
		return respx.OK(ctx, department), nil
	})
	authz.RegisterPlatform(registrar, api, op("organization-system-department", http.MethodGet, "/system/departments/{department_id}", "Get a system department"), authz.Guard{Resource: "system_department", Verb: "view"}, func(ctx context.Context, in *departmentResourceInput) (*respx.Body[Department], error) {
		id, err := guid.RequireID(in.ID)
		if err != nil {
			return nil, err
		}
		s, owner, err := svc.systemDepartmentService(ctx)
		if err != nil {
			return nil, departmentError(err)
		}
		department, err := s.Get(ctx, owner, id)
		if err != nil {
			return nil, departmentError(err)
		}
		return respx.OK(ctx, department), nil
	})
	authz.RegisterPlatform(registrar, api, op("organization-system-department-update", http.MethodPatch, "/system/departments/{department_id}", "Update a system department"), authz.Guard{Resource: "system_department", Verb: "update"}, func(ctx context.Context, in *updateDepartmentInput) (*respx.Body[Department], error) {
		id, err := guid.RequireID(in.ID)
		if err != nil {
			return nil, err
		}
		input := UpdateDepartment{Name: in.Body.Name, Status: in.Body.Status, SortOrder: in.Body.SortOrder, Version: in.Body.Version}
		if in.Body.ParentID != nil {
			parent, err := guid.OptionalID(*in.Body.ParentID)
			if err != nil {
				return nil, err
			}
			input.ParentID = &parent
		}
		var department Department
		err = svc.systemDepartmentMutation(ctx, "system_department_update", func(tx *Service, owner guid.ID) error {
			var err error
			department, err = tx.Update(ctx, owner, id, input)
			return err
		})
		if err != nil {
			return nil, departmentError(err)
		}
		return respx.OK(ctx, department), nil
	})
	authz.RegisterPlatform(registrar, api, op("organization-system-department-delete", http.MethodDelete, "/system/departments/{department_id}", "Delete an empty system department"), authz.Guard{Resource: "system_department", Verb: "delete"}, func(ctx context.Context, in *deleteDepartmentInput) (*respx.Body[deletedResource], error) {
		id, err := guid.RequireID(in.ID)
		if err != nil {
			return nil, err
		}
		err = svc.systemDepartmentMutation(ctx, "system_department_delete", func(tx *Service, owner guid.ID) error { return tx.Delete(ctx, owner, id, in.Version) })
		if err != nil {
			return nil, departmentError(err)
		}
		return respx.OK(ctx, deletedResource{Deleted: true}), nil
	})
}
