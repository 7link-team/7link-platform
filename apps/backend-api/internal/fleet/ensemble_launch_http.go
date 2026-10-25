package fleet

import (
	"context"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type ensembleCommandLock interface {
	lockEnsembleCommand(context.Context, *gorm.DB, *Module, guid.ID, guid.ID, guid.ID) error
}
type ensemblePrepareCommand struct{ EnsemblePrepare }

type ensembleDecisionCommand struct {
	PlanID guid.ID `json:"plan_id"`
	EnsembleDecision
}

func (in ensembleDecisionCommand) lockEnsembleCommand(ctx context.Context, tx *gorm.DB, m *Module, tenant, project, _ guid.ID) error {
	q := query.BindEnsemblePlans(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(in.PlanID)).First()
	if err != nil {
		return err
	}
	return m.lockEnsembleSnapshotOn(ctx, tx, tenant, project, r)
}

func (in ensemblePrepareCommand) lockEnsembleCommand(ctx context.Context, tx *gorm.DB, m *Module, tenant, project, work guid.ID) error {
	if m.ensemble.Runtime.LockOn == nil {
		return ErrUnavailable
	}
	return m.ensemble.Runtime.LockOn(ctx, tx, tenant, project, work, in.Branches)
}

type ensembleLaunchCommand struct {
	PlanID  guid.ID `json:"plan_id"`
	Version int     `json:"version" minimum:"1"`
	Cancel  bool    `json:"cancel"`
}

func (in ensembleLaunchCommand) lockEnsembleCommand(ctx context.Context, tx *gorm.DB, m *Module, tenant, project, _ guid.ID) error {
	if m.ensemble.Runtime.LockOn == nil {
		return ErrUnavailable
	}
	r, branches, e := m.ensembleLaunchSeed(ctx, tx, tenant, project, in.PlanID)
	if e != nil {
		return e
	}
	if e = m.ensemble.Runtime.LockOn(ctx, tx, tenant, project, r.WorkItemID, branches); e != nil {
		return e
	}
	if m.ensemble.Runtime.AuthorizeOn == nil {
		return ErrUnavailable
	}
	return m.ensemble.Runtime.AuthorizeOn(ctx, tx, tenant, project, r.WorkItemID, in.PlanID, branches, in.Cancel)
}

type ensemblePrepareWire struct {
	EnsembleWireProject
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           EnsemblePrepare
}
type ensembleLaunchWire struct {
	EnsembleWireRef
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           struct {
		Version int `json:"version" minimum:"1"`
	}
}

func (m *Module) registerEnsembleLaunchHTTP(api huma.API) error {
	authz.RegisterEntity(m.registrar, api, huma.Operation{OperationID: "ensemble.plan.prepare", Method: "POST", Path: "/ensemble-plans/prepare", Summary: "Freeze proposed parallel branches for explicit Human approval", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 503}}, authz.Guard{Resource: "work_item", Verb: "assign", FilterData: true}, m.prepareEnsembleWireHTTP)
	authz.RegisterEntity(m.registrar, api, huma.Operation{OperationID: "ensemble.plan.launch", Method: "POST", Path: "/ensemble-plans/{plan_id}/launch", Summary: "Launch approved parallel branches and reserve each branch budget atomically", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 503}}, authz.Guard{Resource: "work_item", Verb: "assign", FilterData: true}, m.launchEnsembleWireHTTP)
	authz.RegisterEntity(m.registrar, api, huma.Operation{OperationID: "ensemble.plan.cancel", Method: "POST", Path: "/ensemble-plans/{plan_id}/cancel", Summary: "Request cancellation of the actual parallel branch executions", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 503}}, authz.Guard{Resource: "execution", Verb: "stop", FilterData: true}, m.cancelEnsembleWireHTTP)
	for _, p := range []string{"/ensemble-plans/prepare", "/ensemble-plans/{plan_id}/launch", "/ensemble-plans/{plan_id}/cancel"} {
		if e := authz.RegisterRequestContract(api, "/tenants/{tenant_id}/entities/{entity_id}"+p); e != nil {
			return e
		}
	}
	return nil
}
func (m *Module) prepareEnsembleWireHTTP(ctx context.Context, in *ensemblePrepareWire) (*respx.Body[EnsembleView], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, ensembleHTTPError(ctx, ErrInvalidArgument)
	}
	body := ensemblePrepareCommand{in.Body}
	return ensembleCommand(ctx, m, project, in.Body.WorkItemID, in.IdempotencyKey, "ensemble.plan.prepare", body, func(tx *gorm.DB) (EnsembleView, error) {
		return m.PrepareEnsembleOn(ctx, tx, authn.TenantIDFromContext(ctx), project, in.Body)
	})
}
func (m *Module) launchEnsembleWireHTTP(ctx context.Context, in *ensembleLaunchWire) (*respx.Body[EnsembleView], error) {
	return m.ensembleRuntimeWireHTTP(ctx, in, false)
}
func (m *Module) cancelEnsembleWireHTTP(ctx context.Context, in *ensembleLaunchWire) (*respx.Body[EnsembleView], error) {
	return m.ensembleRuntimeWireHTTP(ctx, in, true)
}
func (m *Module) ensembleRuntimeWireHTTP(ctx context.Context, in *ensembleLaunchWire, cancel bool) (*respx.Body[EnsembleView], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, ensembleHTTPError(ctx, ErrInvalidArgument)
	}
	plan, e := guid.RequireID(in.PlanID)
	if e != nil {
		return nil, ensembleHTTPError(ctx, ErrInvalidArgument)
	}
	db, e := m.writer()
	if e != nil {
		return nil, ensembleHTTPError(ctx, e)
	}
	var work guid.ID
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, _, e := m.ensembleLaunchSeed(ctx, tx, authn.TenantIDFromContext(ctx), project, plan)
		if e == nil {
			work = r.WorkItemID
		}
		return e
	})
	if e != nil {
		return nil, ensembleHTTPError(ctx, e)
	}
	operation := "ensemble.plan.launch:" + plan.String()
	if cancel {
		operation = "ensemble.plan.cancel:" + plan.String()
	}
	body := ensembleLaunchCommand{PlanID: plan, Version: in.Body.Version, Cancel: cancel}
	return ensembleCommand(ctx, m, project, work, in.IdempotencyKey, operation, body, func(tx *gorm.DB) (EnsembleView, error) {
		return m.launchEnsembleOn(ctx, tx, authn.TenantIDFromContext(ctx), project, plan, in.Body.Version, cancel)
	})
}
