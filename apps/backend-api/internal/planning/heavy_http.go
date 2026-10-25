package planning

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type HeavyCreateInput struct {
	PlanningTenantInput
	Key  string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body HeavyCreate
}
type HeavyRefInput struct {
	PlanningTenantInput
	RunID     string `path:"run_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ProjectID string `query:"project_id" required:"true" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type HeavyChangeInput struct {
	HeavyRefInput
	Key  string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body struct {
		Version int    `json:"version" minimum:"1"`
		Action  string `json:"action" enum:"LAUNCH,CANCEL"`
	}
}
type HeavyOutput struct {
	Status   int
	Replayed string `header:"Idempotency-Replayed"`
	Body     respx.Envelope[HeavyView]
}

func (m *Module) registerHeavyHTTP(api huma.API) {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, op("planning.heavy.create", http.MethodPost, "/heavy-runs", "Plan budgeted parallel execution branches", 201), m.createHeavyHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("planning.heavy.get", http.MethodGet, "/heavy-runs/{run_id}", "Read a parallel execution request", 200), m.getHeavyHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("planning.heavy.change", http.MethodPut, "/heavy-runs/{run_id}/state", "Launch or cancel parallel execution branches", 200), m.changeHeavyHTTP)
}
func (m *Module) heavyCommand(ctx context.Context, tenant, project guid.ID, key, operation string, body any, permission string, run func(*gorm.DB) (HeavyView, error)) (*HeavyOutput, error) {
	db, err := m.database()
	if err != nil {
		return nil, planningError(ctx, err)
	}
	if m.replay == nil {
		return nil, planningError(ctx, ErrUnavailable)
	}
	hash, err := idempotency.HashRequest(http.MethodPost, operation, map[string]string{"tenant_id": tenant.String(), "project_id": project.String()}, body)
	if err != nil {
		return nil, planningError(ctx, err)
	}
	k := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: key, RequestHash: hash}
	if err = k.Validate(); err != nil {
		return nil, planningError(ctx, err)
	}
	var result idempotency.Result
	var replay bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Recheck current scope before returning any persisted idempotency response.
		if _, err := m.authorize(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), permission, true); err != nil {
			return err
		}
		var err error
		result, replay, err = m.replay.Do(ctx, tx, k, func(work *gorm.DB) (idempotency.Result, error) {
			v, err := run(work)
			if err != nil {
				return idempotency.Result{}, err
			}
			data, err := json.Marshal(v)
			return idempotency.Result{StatusCode: 200, Body: data, ResourceKind: "heavy_run", ResourceID: v.ID}, err
		})
		return err
	})
	if errors.Is(err, budget.ErrExceeded) {
		err = ErrConflict
	}
	if err != nil {
		return nil, planningError(ctx, err)
	}
	var v HeavyView
	if err = json.Unmarshal(result.Body, &v); err != nil {
		return nil, planningError(ctx, ErrUnavailable)
	}
	out := &HeavyOutput{Status: 200, Body: respx.OK(ctx, v).Body}
	if replay {
		out.Replayed = "true"
	}
	return out, nil
}
func (m *Module) createHeavyHTTP(ctx context.Context, in *HeavyCreateInput) (*HeavyOutput, error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	out, err := m.heavyCommand(ctx, tenant, in.Body.ProjectID, in.Key, "planning.heavy.create", in.Body, "work_item_assign", func(tx *gorm.DB) (HeavyView, error) { return m.createHeavyOn(ctx, tx, tenant, in.Body) })
	if out != nil {
		out.Status = 201
	}
	return out, err
}
func (m *Module) getHeavyHTTP(ctx context.Context, in *HeavyRefInput) (*respx.Body[HeavyView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	project, p := guid.RequireID(in.ProjectID)
	id, r := guid.RequireID(in.RunID)
	if e != nil || p != nil || r != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	db, e := m.database()
	if e != nil {
		return nil, planningError(ctx, e)
	}
	var v HeavyView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, e := m.heavyOn(ctx, tx, tenant, project, id, "work_item_read")
		if e != nil {
			return e
		}
		v, e = heavyView(row)
		return e
	})
	if e != nil {
		return nil, planningError(ctx, e)
	}
	return respx.OK(ctx, v), nil
}
func (m *Module) changeHeavyHTTP(ctx context.Context, in *HeavyChangeInput) (*HeavyOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	project, p := guid.RequireID(in.ProjectID)
	id, r := guid.RequireID(in.RunID)
	if e != nil || p != nil || r != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	permission := "execution_start"
	if in.Body.Action == "CANCEL" {
		permission = "execution_stop"
	}
	return m.heavyCommand(ctx, tenant, project, in.Key, "planning.heavy.change:"+id.String(), in.Body, permission, func(tx *gorm.DB) (HeavyView, error) {
		return m.changeHeavyOn(ctx, tx, tenant, project, id, in.Body.Version, in.Body.Action)
	})
}
