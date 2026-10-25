package fleet

import (
	fleeti18n "7link.com/platform/internal/fleet/i18n"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

type EnsembleProjectInput struct {
	EntityID guid.ID `path:"entity_id" maxLength:"19"`
}
type EnsembleRefInput struct {
	EnsembleProjectInput
	PlanID guid.ID `path:"plan_id" maxLength:"19"`
}
type ensembleCreateInput struct {
	EnsembleProjectInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           EnsembleCreate
}
type ensembleChangeInput struct {
	EnsembleRefInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           EnsembleDecision
}
type ensembleListInput struct {
	EnsembleProjectInput
	WorkItemID guid.ID `query:"work_item_id" required:"true"`
}

func (m *Module) registerEnsembleHTTP(api huma.API) error {
	if e := m.registerEnsembleLaunchHTTP(api); e != nil {
		return e
	}
	authz.RegisterEntity(m.registrar, api, huma.Operation{OperationID: "ensemble.plan.list", Method: "GET", Path: "/ensemble-plans", Summary: "List execution comparison plans for one authorized work item", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 503}}, authz.Guard{Resource: "work_item", Verb: "read", FilterData: true}, m.listEnsemblesWireHTTP)
	authz.RegisterEntity(m.registrar, api, huma.Operation{OperationID: "ensemble.plan.create", Method: "POST", Path: "/ensemble-plans", Summary: "Record real execution branches for comparison", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 503}}, authz.Guard{Resource: "work_item", Verb: "assign", FilterData: true}, m.createEnsembleWireHTTP)
	authz.RegisterEntity(m.registrar, api, huma.Operation{OperationID: "ensemble.plan.get", Method: "GET", Path: "/ensemble-plans/{plan_id}", Summary: "Read an authorized ensemble and frozen comparison", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 503}}, authz.Guard{Resource: "work_item", Verb: "read", FilterData: true}, m.getEnsembleWireHTTP)
	authz.RegisterEntity(m.registrar, api, huma.Operation{OperationID: "ensemble.plan.change", Method: "POST", Path: "/ensemble-plans/{plan_id}/decisions", Summary: "Compare select synthesize or abandon actual execution alternatives", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 503}}, authz.Guard{Resource: "work_item", Verb: "assign", FilterData: true}, m.changeEnsembleWireHTTP)
	for _, p := range []string{"/entities/{entity_id}/ensemble-plans", "/entities/{entity_id}/ensemble-plans/{plan_id}", "/entities/{entity_id}/ensemble-plans/{plan_id}/decisions"} {
		if e := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+p); e != nil {
			return e
		}
	}
	return nil
}
func ensembleCommand[T any](ctx context.Context, m *Module, project, work guid.ID, key, operation string, body any, run func(*gorm.DB) (T, error)) (*respx.Body[T], error) {
	var out T
	db, e := m.writer()
	if e != nil {
		return nil, ensembleHTTPError(ctx, e)
	}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if lock, ok := body.(ensembleCommandLock); ok {
			if e := lock.lockEnsembleCommand(ctx, tx, m, tenant, project, work); e != nil {
				return e
			}
		}
		// Reauthorization precedes both first use and receipt replay.
		command, runtimeCommand := body.(ensembleLaunchCommand)
		if !runtimeCommand || !command.Cancel {
			if e := m.ensembleAuthority(ctx, tx, tenant, project, work, "work_item_assign"); e != nil {
				return e
			}
		}
		if m.idempotency == nil {
			return ErrUnavailable
		}
		hash, e := idempotency.HashRequest(http.MethodPost, operation, map[string]string{"project": project.String()}, body)
		if e != nil {
			return e
		}
		receipt, _, e := m.idempotency.Do(ctx, tx, idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: key, RequestHash: hash}, func(tx *gorm.DB) (idempotency.Result, error) {
			v, e := run(tx)
			if e != nil {
				return idempotency.Result{}, e
			}
			b, e := json.Marshal(v)
			return idempotency.Result{StatusCode: 201, Body: b}, e
		})
		if e != nil {
			return e
		}
		return json.Unmarshal(receipt.Body, &out)
	})
	if errors.Is(e, idempotency.ErrKeyReused) || errors.Is(e, idempotency.ErrInProgress) {
		e = ErrVersionConflict
	}
	if errors.Is(e, idempotency.ErrInvalidKey) {
		e = ErrInvalidArgument
	}
	if e != nil {
		return nil, ensembleHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) createEnsembleHTTP(ctx context.Context, in *ensembleCreateInput) (*respx.Body[EnsembleView], error) {
	return ensembleCommand(ctx, m, in.EntityID, in.Body.WorkItemID, in.IdempotencyKey, "ensemble.plan.create", in.Body, func(tx *gorm.DB) (EnsembleView, error) {
		return m.CreateEnsembleOn(ctx, tx, authn.TenantIDFromContext(ctx), in.EntityID, in.Body)
	})
}
func (m *Module) changeEnsembleHTTP(ctx context.Context, in *ensembleChangeInput) (*respx.Body[EnsembleView], error) {
	db, e := m.writer()
	if e != nil {
		return nil, ensembleHTTPError(ctx, e)
	}
	q := query.BindEnsemblePlans(db, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(in.EntityID), q.ID.Eq(in.PlanID)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		e = ErrMachineNotFound
	}
	if e != nil {
		return nil, ensembleHTTPError(ctx, e)
	}
	return ensembleCommand(ctx, m, in.EntityID, r.WorkItemID, in.IdempotencyKey, "ensemble.plan.change:"+in.PlanID.String(), ensembleDecisionCommand{PlanID: in.PlanID, EnsembleDecision: in.Body}, func(tx *gorm.DB) (EnsembleView, error) {
		return m.ChangeEnsembleOn(ctx, tx, authn.TenantIDFromContext(ctx), in.EntityID, in.PlanID, in.Body)
	})
}
func (m *Module) getEnsembleHTTP(ctx context.Context, in *EnsembleRefInput) (*respx.Body[EnsembleView], error) {
	db, e := m.writer()
	if e != nil {
		return nil, ensembleHTTPError(ctx, e)
	}
	var out EnsembleView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		q := query.BindEnsemblePlans(tx, m.names)
		r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(in.EntityID), q.ID.Eq(in.PlanID)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrMachineNotFound
		}
		if e != nil {
			return e
		}
		if e = m.ensembleAuthority(ctx, tx, tenant, in.EntityID, r.WorkItemID, "work_item_read"); e != nil {
			return e
		}
		r, e = m.ensembleRowOn(ctx, tx, tenant, in.EntityID, in.PlanID)
		if e != nil {
			return e
		}
		out, e = m.ensembleViewOn(ctx, tx, r)
		return e
	})
	if e != nil {
		return nil, ensembleHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) listEnsemblesHTTP(ctx context.Context, in *ensembleListInput) (*respx.Body[[]EnsembleView], error) {
	db, e := m.writer()
	if e != nil {
		return nil, ensembleHTTPError(ctx, e)
	}
	out := []EnsembleView{}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if e := m.ensembleAuthority(ctx, tx, tenant, in.EntityID, in.WorkItemID, "work_item_read"); e != nil {
			return e
		}
		q := query.BindEnsemblePlans(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(in.EntityID), q.WorkItemID.Eq(in.WorkItemID)).Order(q.ID.Desc()).Limit(100).Find()
		if e != nil {
			return e
		}
		for _, r := range rows {
			v, e := m.ensembleViewOn(ctx, tx, r)
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	if e != nil {
		return nil, ensembleHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

func ensembleHTTPError(ctx context.Context, e error) error {
	switch {
	case errors.Is(e, ErrInvalidArgument):
		return respx.ErrStatus(ctx, 422, 422, fleeti18n.Namespace+".ensemble_invalid", nil)
	case errors.Is(e, ErrMachineNotFound), errors.Is(e, gorm.ErrRecordNotFound):
		return respx.ErrStatus(ctx, 404, 404, fleeti18n.Namespace+".ensemble_not_found", nil)
	case errors.Is(e, ErrMachineForbidden):
		return respx.ErrStatus(ctx, 403, 403, fleeti18n.Forbidden, nil)
	case errors.Is(e, ErrVersionConflict):
		return respx.ErrStatus(ctx, 409, 409, fleeti18n.Namespace+".ensemble_conflict", nil)
	default:
		return respx.ErrStatus(ctx, 503, 503, fleeti18n.Namespace+".ensemble_unavailable", nil)
	}
}

// String path selectors follow Huma's wire contract; domain inputs retain IDs.
type EnsembleWireProject struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type EnsembleWireRef struct {
	EnsembleWireProject
	PlanID string `path:"plan_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type ensembleWireList struct {
	EnsembleWireProject
	WorkItemID string `query:"work_item_id" required:"true" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type ensembleWireCreate struct {
	EnsembleWireProject
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           EnsembleCreate
}
type ensembleWireChange struct {
	EnsembleWireRef
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           EnsembleDecision
}

func (m *Module) createEnsembleWireHTTP(ctx context.Context, in *ensembleWireCreate) (*respx.Body[EnsembleView], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, ensembleHTTPError(ctx, ErrInvalidArgument)
	}
	return m.createEnsembleHTTP(ctx, &ensembleCreateInput{EnsembleProjectInput: EnsembleProjectInput{EntityID: project}, IdempotencyKey: in.IdempotencyKey, Body: in.Body})
}
func (m *Module) changeEnsembleWireHTTP(ctx context.Context, in *ensembleWireChange) (*respx.Body[EnsembleView], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, ensembleHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.PlanID)
	if e != nil {
		return nil, ensembleHTTPError(ctx, ErrInvalidArgument)
	}
	return m.changeEnsembleHTTP(ctx, &ensembleChangeInput{EnsembleRefInput: EnsembleRefInput{EnsembleProjectInput: EnsembleProjectInput{EntityID: project}, PlanID: id}, IdempotencyKey: in.IdempotencyKey, Body: in.Body})
}
func (m *Module) getEnsembleWireHTTP(ctx context.Context, in *EnsembleWireRef) (*respx.Body[EnsembleView], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, ensembleHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.PlanID)
	if e != nil {
		return nil, ensembleHTTPError(ctx, ErrInvalidArgument)
	}
	return m.getEnsembleHTTP(ctx, &EnsembleRefInput{EnsembleProjectInput: EnsembleProjectInput{EntityID: project}, PlanID: id})
}
func (m *Module) listEnsemblesWireHTTP(ctx context.Context, in *ensembleWireList) (*respx.Body[[]EnsembleView], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, ensembleHTTPError(ctx, ErrInvalidArgument)
	}
	work, e := guid.RequireID(in.WorkItemID)
	if e != nil {
		return nil, ensembleHTTPError(ctx, ErrInvalidArgument)
	}
	return m.listEnsemblesHTTP(ctx, &ensembleListInput{EnsembleProjectInput: EnsembleProjectInput{EntityID: project}, WorkItemID: work})
}
