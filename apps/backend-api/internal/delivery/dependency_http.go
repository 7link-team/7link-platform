package delivery

import (
	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
)

const dependenciesPath = workPath + "/{work_item_id}/dependencies"

type dependencyListInput struct {
	EntityID   string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Direction  string `query:"direction" default:"upstream" enum:"upstream,downstream"`
	Offset     int    `query:"offset" default:"0" minimum:"0"`
	Limit      int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}
type dependencyCreateInput struct {
	EntityID    string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID  string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	DependsOnID string `path:"depends_on_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type dependencyDeleteInput = dependencyCreateInput

type dependencyReceipt struct {
	Dependency dependencyView `json:"dependency"`
	WorkItem   WorkView       `json:"work_item"`
}

func dependencyHTTPError(ctx context.Context, err error) error {
	if errors.Is(err, ErrDependencyCycle) {
		var cycle *dependencyCycleError
		if errors.As(err, &cycle) {
			return respx.ErrStatus(ctx, 422, 120422, deliveryi18n.DependencyCycle, map[string]any{"cycle": cycle.Cycle})
		}
		return respx.ErrStatus(ctx, 422, 120422, deliveryi18n.DependencyCycle, nil)
	}
	if errors.Is(err, ErrDependencyLimit) {
		return respx.ErrStatus(ctx, 422, 120423, deliveryi18n.DependencyLimit, map[string]int{"limit": 50})
	}
	if errors.Is(err, ErrDependencyProject) {
		return respx.ErrStatus(ctx, 422, 120451, deliveryi18n.DependencyProject, nil)
	}
	return workHTTPError(ctx, err)
}
func (m *Module) registerDependencyHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.dependency-list", Method: http.MethodGet, Path: dependenciesPath, Summary: "List upstream or downstream work dependencies", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.listDependenciesHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.dependency-create", Method: http.MethodPut, Path: dependenciesPath + "/{depends_on_id}", Summary: "Add a work item predecessor", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "write"}, m.createDependencyHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.dependency-delete", Method: http.MethodDelete, Path: dependenciesPath + "/{depends_on_id}", Summary: "Remove a work item predecessor", Tags: []string{"entities"}, DefaultStatus: 204, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "write"}, m.deleteDependencyHTTP)
	// A naturally idempotent PUT returns the existing relation with 200.
	op := api.OpenAPI().Paths[dependenciesPath+"/{depends_on_id}"].Put
	existing := *op.Responses["201"]
	existing.Description = "Dependency already exists"
	op.Responses["200"] = &existing
	return nil
}
func (m *Module) listDependenciesHTTP(ctx context.Context, in *dependencyListInput) (*respx.Body[[]dependencyView], error) {
	project, e := guid.RequireID(in.EntityID)
	id, err := guid.RequireID(in.WorkItemID)
	if e != nil || err != nil || in.Offset < 0 || in.Limit < 1 || in.Limit > 200 || (in.Direction != "" && in.Direction != "upstream" && in.Direction != "downstream") {
		return nil, dependencyHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, dependencyHTTPError(ctx, ErrUnavailable)
	}
	var items []dependencyView
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		items, e = m.dependenciesOn(ctx, tx, authn.TenantIDFromContext(ctx), project, id, authn.PrincipalIDFromContext(ctx), in.Direction == "downstream")
		return e
	})
	if err != nil {
		return nil, dependencyHTTPError(ctx, err)
	}
	total := len(items)
	start := min(in.Offset, total)
	end := start + min(in.Limit, total-start)
	page := items[start:end]
	return respx.List(ctx, page, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(page), Total: int64(total)}), nil
}
func (m *Module) createDependencyHTTP(ctx context.Context, in *dependencyCreateInput) (*deliveryCommandOutput[dependencyReceipt], error) {
	view, predecessor, created, err := m.dependencyRelationHTTP(ctx, in, false)
	if err != nil {
		return nil, err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return &deliveryCommandOutput[dependencyReceipt]{Status: status, Body: respx.OK(ctx, dependencyReceipt{Dependency: predecessor, WorkItem: view}).Body}, nil
}
func (m *Module) deleteDependencyHTTP(ctx context.Context, in *dependencyDeleteInput) (*struct{}, error) {
	_, _, _, err := m.dependencyRelationHTTP(ctx, in, true)
	if err != nil {
		return nil, err
	}
	return nil, nil
}

// Relations are naturally idempotent. The current project/work locks serialize
// graph edits and blocker recomputation; no caller version or receipt is needed.
func (m *Module) dependencyRelationHTTP(ctx context.Context, in *dependencyCreateInput, remove bool) (WorkView, dependencyView, bool, error) {
	var view WorkView
	var predecessor dependencyView
	project, e := guid.RequireID(in.EntityID)
	id, err := guid.RequireID(in.WorkItemID)
	dependsOn, parseErr := guid.RequireID(in.DependsOnID)
	if e != nil || err != nil || parseErr != nil {
		return view, predecessor, false, dependencyHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return view, predecessor, false, dependencyHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	db := m.runtime.Database().Write()
	owner, err := m.preparationOwner(ctx, db, tenant, project, id)
	if err != nil {
		return view, predecessor, false, dependencyHTTPError(ctx, err)
	}
	created := false
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, _, _, e := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "work_item_write"); e != nil {
			return e
		}
		q := query.BindDependencies(tx, m.service.names)
		existing, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(id), q.DependsOnID.Eq(dependsOn)).Find()
		if e != nil {
			return e
		}
		view, e = m.changeDependencyOn(ctx, tx, tenant, project, id, actor, owner, dependsOn, 0, remove)
		if e != nil {
			return e
		}
		created = !remove && len(existing) == 0
		if !remove {
			rows, e := m.dependenciesOn(ctx, tx, tenant, project, id, actor)
			if e != nil {
				return e
			}
			for _, row := range rows {
				if row.ID == dependsOn {
					predecessor = row
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return view, predecessor, created, dependencyHTTPError(ctx, err)
	}
	return view, predecessor, created, nil
}
