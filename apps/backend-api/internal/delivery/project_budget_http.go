package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

const projectBudgetPath = "/entities/{entity_id}/project/budget"

type projectBudgetInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch  string `header:"If-Match" required:"true"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body     projectBudgetBody
}
type projectBudgetEventView struct {
	ID          guid.ID  `json:"id"`
	Kind        string   `json:"kind"`
	CapMicros   *int64   `json:"cap_micros"`
	SpentMicros int64    `json:"spent_micros"`
	NotifyPct   int      `json:"notify_pct"`
	CapVersion  int      `json:"cap_version"`
	State       string   `json:"state"`
	Reason      string   `json:"reason"`
	ActorID     *guid.ID `json:"actor_id"`
	OccurredAt  string   `json:"occurred_at"`
}

func (m *Module) registerProjectBudgetHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.budget.update", Method: http.MethodPut, Path: projectBudgetPath, Summary: "Change project budget without resetting accounted spend", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "manage"}, m.changeProjectBudgetHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.budget.events", Method: http.MethodGet, Path: projectBudgetPath + "/events", Summary: "List immutable project budget observations", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "read"}, m.projectBudgetEventsHTTP)
	return authz.RegisterRequestContract(api, projectBudgetPath)
}
func (m *Module) changeProjectBudgetHTTP(ctx context.Context, in *projectBudgetInput) (*deliveryCommandOutput[ProjectView], error) {
	project, _, version, err := deliveryEditIDs(in.EntityID, in.EntityID, in.IfMatch)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	request := struct {
		Version int
		Body    projectBudgetBody
	}{version, in.Body}
	return deliveryCommand(ctx, m, "project_budget_update", workHTTPError, "project.budget.update", http.MethodPut, projectBudgetPath, in.Key, 200, request, map[string]string{"entity_id": project.String()}, func(tx *gorm.DB) (ProjectView, guid.ID, error) {
		v, e := m.changeProjectBudgetOn(ctx, tx, authn.TenantIDFromContext(ctx), project, authn.PrincipalIDFromContext(ctx), version, in.Body)
		return v, project, e
	}, func(tx *gorm.DB) error {
		_, _, e := m.constitutionProjectOn(ctx, tx, authn.TenantIDFromContext(ctx), project, authn.PrincipalIDFromContext(ctx), "project_manage")
		return e
	})
}
func (m *Module) projectBudgetEventsHTTP(ctx context.Context, in *stageListInput) (*respx.Body[[]projectBudgetEventView], error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil || in.Offset < 0 || in.Limit < 1 || in.Limit > 200 {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	rows := []projectBudgetEventView{}
	var total int64
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if e := m.constitutionReadProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx)); e != nil {
			return e
		}
		q := query.BindBudgetEvents(tx, m.service.names)
		base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project))
		var e error
		total, e = base.Count()
		if e != nil {
			return e
		}
		found, e := base.Order(q.OccurredAt.Desc(), q.ID.Desc()).Offset(in.Offset).Limit(in.Limit).Find()
		if e != nil {
			return e
		}
		for _, r := range found {
			rows = append(rows, projectBudgetEventView{r.ID, r.Kind, r.CapMicros, r.SpentMicros, r.NotifyPct, r.CapVersion, r.State, r.Reason, r.ActorID, r.OccurredAt.Format("2006-01-02T15:04:05.000Z07:00")})
		}
		return nil
	})
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return respx.List(ctx, rows, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(rows), Total: total}), nil
}
