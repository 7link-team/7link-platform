package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type DispatchCandidatesInput struct {
	EntityID   string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type DispatchListInput struct {
	ListInput
	WorkItemID  string `query:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	PrincipalID string `query:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"Include proposals recommending or applied to this principal; not every losing candidate."`
	State       string `query:"state" enum:"PROPOSED,APPLIED,DISMISSED,EXPIRED"`
}

type DispatchStopBody struct {
	ExecutionID              guid.ID `json:"execution_id"`
	ExpectedExecutionVersion int     `json:"expected_execution_version" minimum:"1"`
	Reason                   string  `json:"reason" minLength:"1" maxLength:"2000"`
}
type DispatchStopInput EditInput[DispatchStopBody]

func (m *Module) stopDispatchExecutionHTTP(ctx context.Context, in *DispatchStopInput) (*CommandOutput, error) {
	p, id, version, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if in.Body.ExecutionID <= 0 || in.Body.ExpectedExecutionVersion < 1 || !validText(in.Body.Reason, 2000) {
		return nil, httpError(ctx, ErrInvalid)
	}
	check := func(tx *gorm.DB) error { _, err := m.dispatchOn(ctx, tx, p, id); return err }
	return m.command(ctx, p, "clientwork.dispatch.execution_stop", "POST", entityBase+"/dispatch-proposals/"+id.String()+"/execution-stop", in.Key, 201, in, "execution_stop", false, check, func(tx *gorm.DB) (Receipt, error) {
		row, err := m.dispatchOn(ctx, tx, p, id)
		if err != nil {
			return Receipt{}, err
		}
		if row.Version != version || row.State != "PROPOSED" || row.Trigger != "REDISPATCH" {
			return Receipt{}, ErrConflict
		}
		if !now().Before(row.ExpiresAt) {
			return Receipt{}, ErrDispatchExpired
		}
		if m.dispatch.StopOn == nil || m.dispatch.WorkOn == nil {
			return Receipt{}, ErrUnavailable
		}
		work, err := m.dispatch.WorkOn(ctx, tx, row.TenantID, p, row.WorkItemID)
		if err != nil {
			return Receipt{}, err
		}
		if work.WorkVersion != row.WorkVersion {
			return Receipt{}, ErrConflict
		}
		state, err := m.dispatch.StopOn(ctx, tx, row.TenantID, p, row.WorkItemID, in.Body.ExecutionID, in.Body.ExpectedExecutionVersion)
		if err != nil {
			return Receipt{}, err
		}
		status := "EXECUTION_STOP_PENDING"
		if state.State == "CANCELLED" { //nolint:misspell // Canonical protocol spelling.
			status = "EXECUTION_STOPPED"
		}
		event := audit.NewDomainEvent(ctx, row.TenantID, "clientwork.dispatch_execution_stop_requested", "clientwork", id)
		event.Detail["execution_id"], event.Detail["reason"], event.Detail["proposal_version"] = in.Body.ExecutionID.String(), in.Body.Reason, row.Version
		if err = m.audit(ctx, tx, event); err != nil {
			return Receipt{}, err
		}
		return Receipt{ID: id, Version: row.Version, State: status}, nil
	})
}

func (m *Module) registerDispatchHTTP(api huma.API) error {
	path := entityBase + "/dispatch-proposals"
	owner := authz.ResourceOwnerResolver(m.owner)
	guard := authz.Guard{Resource: "work_item", Verb: "assign"}
	errs := []int{401, 403, 404, 409, 422, 500, 503}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.dispatch.candidates", Method: "GET", Path: entityBase + "/work-items/{work_item_id}/dispatch-candidates", Summary: "Preview current dispatch candidates without creating a proposal", Tags: []string{"entities"}, Errors: errs}, authz.EntityPath, owner, guard, m.dispatchCandidatesHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.dispatch.list", Method: "GET", Path: path, Summary: "List authorized frozen dispatch proposals and filtered recommendation history", Tags: []string{"entities"}, Errors: errs}, authz.EntityPath, owner, guard, m.listDispatchFilteredHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.dispatch.get", Method: "GET", Path: path + "/{id}", Summary: "Read a dispatch proposal and its reproducible candidate inputs", Tags: []string{"entities"}, Errors: errs}, authz.EntityPath, owner, guard, m.getDispatchHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.dispatch.create", Method: "POST", Path: path, Summary: "Freeze a dispatch recommendation from current domain facts", Tags: []string{"entities"}, DefaultStatus: 201, Errors: errs}, authz.EntityPath, owner, guard, m.createDispatchHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.dispatch.auto", Method: "POST", Path: path + "/auto", Summary: "Let the current lead Agent propose and apply guarded AUTO dispatch", Tags: []string{"entities"}, DefaultStatus: 201, Errors: errs}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.autoDispatchHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.dispatch.decide", Method: "POST", Path: path + "/{id}/decisions", Summary: "Confirm override dismiss or expire an exact dispatch proposal", Tags: []string{"entities"}, DefaultStatus: 201, Errors: errs}, authz.EntityPath, owner, guard, m.decideDispatchHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.dispatch.execution_stop", Method: "POST", Path: path + "/{id}/execution-stop", Summary: "Request canonical execution cancellation before a fresh reassignment proposal", Tags: []string{"entities"}, DefaultStatus: 201, Errors: errs}, authz.EntityPath, owner, guard, m.stopDispatchExecutionHTTP)
	if !m.registrar.IsDeclarationOnly() {
		if e := authz.RegisterRequestContract(api, path); e != nil {
			return e
		}
		if e := authz.RegisterRequestContract(api, path+"/auto"); e != nil {
			return e
		}
		if e := authz.RegisterRequestContract(api, path+"/{id}/execution-stop"); e != nil {
			return e
		}
		return authz.RegisterRequestContract(api, path+"/{id}/decisions")
	}
	return nil
}

func (m *Module) dispatchCandidatesHTTP(ctx context.Context, in *DispatchCandidatesInput) (*respx.Body[DispatchCandidatesView], error) {
	p, e := guid.RequireID(in.EntityID)
	w, we := guid.RequireID(in.WorkItemID)
	if e != nil || we != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out DispatchCandidatesView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.authorize(ctx, tx, p, "work_item_assign", false); e != nil {
			return e
		}
		var e error
		out.Snapshot, e = m.dispatchSnapshotOn(ctx, tx, p, w)
		if e != nil {
			return e
		}
		_, out.InputsDigest, e = encodeDispatchSnapshot(out.Snapshot)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) createDispatchHTTP(ctx context.Context, in *CreateInput[DispatchBody]) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	check := func(tx *gorm.DB) error {
		if m.dispatch.WorkOn == nil {
			return ErrUnavailable
		}
		_, e := m.dispatch.WorkOn(ctx, tx, authn.TenantIDFromContext(ctx), p, in.Body.WorkItemID)
		return e
	}
	return m.command(ctx, p, "clientwork.dispatch.create", "POST", entityBase+"/dispatch-proposals", in.Key, 201, in.Body, "work_item_assign", false, check, func(tx *gorm.DB) (Receipt, error) { return m.createDispatchOn(ctx, tx, p, in.Body) })
}
func (m *Module) decideDispatchHTTP(ctx context.Context, in *EditInput[DispatchDecision]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	check := func(tx *gorm.DB) error {
		row, err := m.dispatchOn(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if m.dispatch.AuthorityOn == nil {
			return ErrUnavailable
		}
		target := guid.ID(0)
		if in.Body.Decision == "CONFIRM" && row.RecommendedID != nil {
			target = *row.RecommendedID
		}
		if in.Body.Decision == "OVERRIDE" && in.Body.AssigneePrincipalID != nil {
			target = *in.Body.AssigneePrincipalID
		}
		source, err := m.dispatch.AuthorityOn(ctx, tx, row.TenantID, p, row.WorkItemID, target)
		if err != nil {
			return err
		}
		if source == "" {
			return ErrForbidden
		}
		return nil
	}
	return m.command(ctx, p, "clientwork.dispatch.decide", "POST", entityBase+"/dispatch-proposals/"+id.String()+"/decisions", in.Key, 201, in, "work_item_assign", false, check, func(tx *gorm.DB) (Receipt, error) { return m.decideDispatchOn(ctx, tx, p, id, v, in.Body) })
}
func (m *Module) getDispatchHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[DispatchView], error) {
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
	var out DispatchView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.authorize(ctx, tx, p, "work_item_assign", false); e != nil {
			return e
		}
		row, e := m.dispatchOn(ctx, tx, p, id)
		if e != nil {
			return e
		}
		out, e = dispatchView(row)
		if e == nil {
			out.Decision, e = m.dispatchActionOn(ctx, tx, row)
		}
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) listDispatchHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[DispatchView]], error) {
	return m.listDispatchFilteredHTTP(ctx, &DispatchListInput{ListInput: *in})
}

func (m *Module) listDispatchFilteredHTTP(ctx context.Context, in *DispatchListInput) (*respx.Body[Page[DispatchView]], error) {
	var work, principal guid.ID
	var e error
	if in.WorkItemID != "" {
		work, e = guid.RequireID(in.WorkItemID)
		if e != nil {
			return nil, httpError(ctx, ErrInvalid)
		}
	}
	if in.PrincipalID != "" {
		principal, e = guid.RequireID(in.PrincipalID)
		if e != nil {
			return nil, httpError(ctx, ErrInvalid)
		}
	}
	if in.State != "" && in.State != "PROPOSED" && in.State != "APPLIED" && in.State != "DISMISSED" && in.State != "EXPIRED" {
		return nil, httpError(ctx, ErrInvalid)
	}
	kind := "dispatch-proposals"
	if work > 0 || principal > 0 || in.State != "" {
		kind = fmt.Sprintf("dispatch-proposals:%s:%s:%s", work, principal, in.State)
	}
	return readDomain(ctx, m, &in.ListInput, kind, "work_item_assign", false, func(tx *gorm.DB, _ bool, last guid.ID, n int) ([]DispatchView, []guid.ID, error) {
		q := query.BindDispatchProposal(tx, m.names)
		if m.dispatch.WorkOn == nil {
			return nil, nil, ErrUnavailable
		}
		out := make([]DispatchView, 0, n)
		ids := make([]guid.ID, 0, n)
		// Scan until the authorized page (including its lookahead) is full.
		// Limiting before filtering would hide later accessible proposals and
		// misreport has_more when early proposals belong to other work owners.
		for len(out) < n {
			r := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(authn.EntityIDFromContext(ctx)), q.ID.Gt(last))
			if work > 0 {
				r = r.Where(q.WorkItemID.Eq(work))
			}
			if principal > 0 {
				r = r.Where(q.WithContext(ctx).Where(q.RecommendedID.Eq(principal)).Or(q.AppliedID.Eq(principal)))
			}
			if in.State != "" {
				r = r.Where(q.State.Eq(in.State))
			}
			rows, e := r.Order(q.ID).Limit(n).Find()
			if e != nil {
				return nil, nil, e
			}
			for _, row := range rows {
				last = row.ID
				if _, e = m.dispatch.WorkOn(ctx, tx, row.TenantID, row.ProjectID, row.WorkItemID); e != nil {
					if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) || errors.Is(e, delivery.ErrWorkForbidden) || errors.Is(e, delivery.ErrProjectNotFound) || errors.Is(e, gorm.ErrRecordNotFound) {
						continue
					}
					return nil, nil, e
				}
				v, e := dispatchView(row)
				if e != nil {
					return nil, nil, e
				}
				v.Decision, e = m.dispatchActionOn(ctx, tx, row)
				if e != nil {
					return nil, nil, e
				}
				out = append(out, v)
				ids = append(ids, row.ID)
				if len(out) == n {
					break
				}
			}
			if len(rows) < n {
				break
			}
		}
		return out, ids, nil
	})
}
