package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

func (m *Module) registerToleranceHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.owner)
	errors := []int{401, 403, 404, 409, 422, 500, 503}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.tolerance.impact", Method: "POST", Path: entityBase + "/requirements/{requirement_id}/tolerance-impact", Summary: "Preview commitment impact without changing baselines or forecasts", Tags: []string{"entities"}, Errors: errors}, authz.EntityPath, owner, authz.Guard{Resource: "requirement", Verb: "read"}, m.toleranceImpactHTTP)
	for _, r := range []struct{ id, method, path, summary, verb string }{
		{"tolerance.create", "POST", "/project/tolerances", "Create a project or requirement commitment", "manage"},
		{"tolerance.edit", "PUT", "/project/tolerances/{id}", "Replace an unlocked commitment", "manage"},
		{"tolerance.forecast", "POST", "/project/tolerances/{id}/forecasts", "Record a human-confirmed forecast and raise any exception", "manage"},
		{"exception.report", "PUT", "/project/exceptions/{id}/report", "Submit the human-confirmed immutable exception report", "manage"},
	} {
		op := huma.Operation{OperationID: "clientwork." + r.id, Method: r.method, Path: entityBase + r.path, Summary: r.summary, Tags: []string{"entities"}, Errors: errors}
		if r.method == "POST" {
			op.DefaultStatus = 201
		}
		guard := authz.Guard{Resource: "project", Verb: r.verb}
		switch r.id {
		case "tolerance.create":
			authz.RegisterOwned(m.registrar, api, op, authz.EntityPath, owner, guard, m.createToleranceHTTP)
		case "tolerance.edit":
			authz.RegisterOwned(m.registrar, api, op, authz.EntityPath, owner, guard, m.editToleranceHTTP)
		case "tolerance.forecast":
			authz.RegisterOwned(m.registrar, api, op, authz.EntityPath, owner, guard, m.forecastToleranceHTTP)
		case "exception.report":
			authz.RegisterOwned(m.registrar, api, op, authz.EntityPath, owner, guard, m.reportExceptionHTTP)
		}
		if !m.registrar.IsDeclarationOnly() {
			if e := authz.RegisterRequestContract(api, op.Path); e != nil {
				return e
			}
		}
	}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.tolerance.list", Method: "GET", Path: entityBase + "/project/tolerances", Summary: "List current project and requirement commitments", Tags: []string{"entities"}, Errors: errors}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "read"}, m.listTolerancesHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.exception.list", Method: "GET", Path: entityBase + "/project/exceptions", Summary: "List exception snapshots and immutable reports", Tags: []string{"entities"}, Errors: errors}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "read"}, m.listExceptionsHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.tolerance.get", Method: "GET", Path: entityBase + "/project/tolerances/{id}", Summary: "Read one authorized project or requirement commitment", Tags: []string{"entities"}, Errors: errors}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "read"}, m.getToleranceHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.exception.get", Method: "GET", Path: entityBase + "/project/exceptions/{id}", Summary: "Read an authorized exception snapshot and report", Tags: []string{"entities"}, Errors: errors}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "read"}, m.getExceptionHTTP)

	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.exception.decide", Method: "POST", Path: entityBase + "/project/exceptions/{id}/decisions", Summary: "Decide a reported exception with current non-delegable Human authority", Tags: []string{"entities"}, DefaultStatus: 201, Errors: errors}, authz.EntityPath, owner, authz.Guard{Resource: "exception", Verb: "decide"}, m.decideExceptionHTTP)
	if !m.registrar.IsDeclarationOnly() {
		return authz.RegisterRequestContract(api, entityBase+"/project/exceptions/{id}/decisions")
	}
	return nil
}

type ToleranceImpactInput struct {
	EntityID      string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RequirementID string `path:"requirement_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body          ApprovedChangeDelta
}

func (m *Module) toleranceImpactHTTP(ctx context.Context, in *ToleranceImpactInput) (*respx.Body[ToleranceChangeSnapshot], error) {
	p, pe := guid.RequireID(in.EntityID)
	r, re := guid.RequireID(in.RequirementID)
	if pe != nil || re != nil || !validDelta(in.Body) {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out ToleranceChangeSnapshot
	e := m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.WouldBreachOn(ctx, tx, p, r, in.Body)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) createToleranceHTTP(ctx context.Context, in *CreateInput[ToleranceBody]) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.tolerance.create", "POST", entityBase+"/project/tolerances", in.Key, 201, in.Body, "project_manage", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.putToleranceOn(ctx, tx, p, 0, 0, in.Body) })
}
func (m *Module) editToleranceHTTP(ctx context.Context, in *EditInput[ToleranceBody]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.tolerance.edit", "PUT", entityBase+"/project/tolerances/"+id.String(), in.Key, 200, in, "project_manage", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.putToleranceOn(ctx, tx, p, id, v, in.Body) })
}
func (m *Module) forecastToleranceHTTP(ctx context.Context, in *EditInput[ToleranceForecast]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.tolerance.forecast", "POST", entityBase+"/project/tolerances/"+id.String()+"/forecasts", in.Key, 201, in, "project_manage", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.forecastToleranceOn(ctx, tx, p, id, v, in.Body) })
}
func (m *Module) reportExceptionHTTP(ctx context.Context, in *EditInput[ExceptionReport]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.exception.report", "PUT", entityBase+"/project/exceptions/"+id.String()+"/report", in.Key, 200, in, "project_manage", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.reportExceptionOn(ctx, tx, p, id, v, in.Body) })
}
func (m *Module) listTolerancesHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[ToleranceView]], error) {
	return readDomain(ctx, m, in, "tolerances", "project_read", false, func(tx *gorm.DB, _ bool, last guid.ID, n int) ([]ToleranceView, []guid.ID, error) {
		q := query.BindTolerance(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(authn.EntityIDFromContext(ctx)), q.ID.Gt(last)).Order(q.ID).Limit(n).Find()
		if e != nil {
			return nil, nil, e
		}
		out := make([]ToleranceView, 0, len(rows))
		ids := make([]guid.ID, 0, len(rows))
		for _, r := range rows {
			out = append(out, toleranceRecord(r))
			ids = append(ids, r.ID)
		}
		return out, ids, nil
	})
}
func (m *Module) listExceptionsHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[ExceptionView]], error) {
	return readDomain(ctx, m, in, "exceptions", "project_read", false, func(tx *gorm.DB, _ bool, last guid.ID, n int) ([]ExceptionView, []guid.ID, error) {
		q := query.BindToleranceException(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(authn.EntityIDFromContext(ctx)), q.ID.Gt(last)).Order(q.ID).Limit(n).Find()
		if e != nil {
			return nil, nil, e
		}
		out := make([]ExceptionView, 0, len(rows))
		ids := make([]guid.ID, 0, len(rows))
		for _, r := range rows {
			v, e := exceptionRecord(r)
			if e != nil {
				return nil, nil, e
			}
			out = append(out, v)
			ids = append(ids, r.ID)
		}
		return out, ids, nil
	})
}
func (m *Module) getExceptionHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[ExceptionView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out ExceptionView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.authorize(ctx, tx, p, "project_read", false); e != nil {
			return e
		}
		r, e := m.exceptionOn(ctx, tx, p, id)
		if e != nil {
			return e
		}
		out, e = exceptionRecord(r)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) decideExceptionHTTP(ctx context.Context, in *ExceptionDecisionInput) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.exception.decide", "POST", entityBase+"/project/exceptions/"+id.String()+"/decisions", in.Key, 201, in, "exception_decide", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.decideExceptionOn(ctx, tx, p, id, v, in.Cookie, in.Body) })
}

func (m *Module) getToleranceHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[ToleranceView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out ToleranceView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.authorize(ctx, tx, p, "project_read", false); e != nil {
			return e
		}
		row, e := m.toleranceOn(ctx, tx, p, id)
		if e != nil {
			return e
		}
		out = toleranceRecord(row)
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
