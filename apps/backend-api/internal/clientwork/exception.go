package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"slices"
)

type ExceptionDecisionBody struct {
	Decision        string                `json:"decision" enum:"CONTINUE,RESHAPE,HOLD,KILL"`
	Reason          string                `json:"reason" minLength:"5" maxLength:"2000"`
	Budget          *ToleranceBudget      `json:"budget,omitempty"`
	Change          *ClientChangeProposal `json:"change,omitempty"`
	ResumeCondition string                `json:"resume_condition,omitempty" maxLength:"2000"`
	ReauthProof     string                `json:"reauth_proof,omitempty" maxLength:"512" writeOnly:"true"`
}
type ExceptionDecisionInput struct {
	EditInput[ExceptionDecisionBody]
	Cookie string `header:"Cookie"`
}

func (m *Module) decideExceptionOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, cookie string, in ExceptionDecisionBody) (Receipt, error) {
	var commandDecisionID *guid.ID
	if in.Decision == "KILL" {
		c, ok := m.projects.(delivery.ProjectCancellationAccess)
		if !ok {
			return Receipt{}, ErrUnavailable
		}
		if e := c.LockCancellationOn(ctx, tx, authn.TenantIDFromContext(ctx), p); e != nil {
			return Receipt{}, e
		}
	}
	if _, e := m.authorize(ctx, tx, p, "exception_decide", false); e != nil {
		return Receipt{}, e
	}
	if !slices.Contains([]string{"CONTINUE", "RESHAPE", "HOLD", "KILL"}, in.Decision) || !validText(in.Reason, 2000) || len([]rune(in.Reason)) < 5 {
		return Receipt{}, ErrInvalid
	}
	if (in.Decision == "CONTINUE" || in.Decision == "RESHAPE") && (in.Budget == nil || !validToleranceBudget(*in.Budget)) {
		return Receipt{}, ErrInvalid
	}
	if in.Decision == "HOLD" && !validText(in.ResumeCondition, 2000) {
		return Receipt{}, ErrInvalid
	}
	if in.Decision == "RESHAPE" && in.Change == nil {
		return Receipt{}, ErrInvalid
	}
	if in.Decision != "RESHAPE" && in.Change != nil {
		return Receipt{}, ErrInvalid
	}
	if (in.Decision == "HOLD" || in.Decision == "KILL") && in.Budget != nil {
		return Receipt{}, ErrInvalid
	}
	ex, e := m.exceptionOn(ctx, tx, p, id)
	if e != nil {
		return Receipt{}, e
	}
	if ex.Version != v || ex.State != "REPORTED" || ex.Report == nil {
		return Receipt{}, ErrConflict
	}
	r, e := m.toleranceOn(ctx, tx, p, ex.ToleranceID)
	if e != nil {
		return Receipt{}, e
	}
	if r.OpenExceptionID == nil || *r.OpenExceptionID != id {
		return Receipt{}, ErrConflict
	}
	// AuthorityChecker enforces non-delegable Human authority even for CONTINUE.
	if in.Decision == "KILL" {
		value, e := m.nextID()
		if e != nil {
			return Receipt{}, e
		}
		decision := &model.CommandDecision{TenantID: r.TenantID, ID: value, ProjectID: p, TargetKind: "PROJECT", TargetID: p, Action: "CANCEL", SourceID: id, SourceVersion: v, ActorID: authn.PrincipalIDFromContext(ctx), Reason: in.Reason, CreatedAt: now()}
		cancellation, ok := m.projects.(delivery.ProjectCancellationAccess)
		if !ok {
			return Receipt{}, ErrUnavailable
		}
		authority, e := cancellation.CancelProjectOn(ctx, tx, r.TenantID, p, value, in.Reason, cookie, in.ReauthProof)
		if e != nil {
			return Receipt{}, e
		}
		decision.AuthoritySource = authority
		if e = m.archiveProjectDraftPackagesOn(ctx, tx, r.TenantID, p); e != nil {
			return Receipt{}, e
		}
		if e = query.BindCommandDecision(tx, m.names).WithContext(ctx).Create(decision); e != nil {
			return Receipt{}, e
		}
		commandDecisionID = &value
	} else if e = m.projects.ExceptionProjectOn(ctx, tx, r.TenantID, p, in.Decision, in.Reason, cookie, in.ReauthProof); e != nil {
		return Receipt{}, e
	}
	var changeID *guid.ID
	if in.Decision == "RESHAPE" {
		change := in.Change
		if r.SubjectKind != "REQUIREMENT" || change.RequirementID != r.SubjectID {
			return Receipt{}, ErrInvalid
		}
		d := change.Change
		canonical := delivery.ClientScopeChange{Criteria: d.Criteria, BaseVersionNo: d.BaseVersionNo, Summary: d.Summary, Title: d.Title, Description: d.Description, Priority: d.Priority, StaleWorkIDs: d.StaleWorkIDs, ReauthProof: in.ReauthProof}
		applied, e := m.projects.ApplyClientChangeOn(ctx, tx, r.TenantID, p, r.SubjectID, change.RequirementVersion, cookie, canonical)
		if e != nil {
			return Receipt{}, e
		}
		changeID = &applied
	}
	if in.Budget != nil {
		b := in.Budget
		r.AppetiteEndAt = b.AppetiteEndAt
		r.AppetiteCostMicros = b.AppetiteCostMicros
		r.AppetiteScopeCriteria = b.AppetiteScopeCriteria
		r.ToleranceTimePct = b.ToleranceTimePct
		r.ToleranceCostPct = b.ToleranceCostPct
		r.ToleranceScopePct = b.ToleranceScopePct
		state, _ := toleranceState(r)
		if state != "OK" {
			return Receipt{}, ErrInvalid
		}
		r.State = state
		r.BaselineNo++
	}
	r.OpenExceptionID = nil
	r.Version++
	q := query.BindTolerance(tx, m.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(r.ID)).Select(q.AppetiteEndAt, q.AppetiteCostMicros, q.AppetiteScopeCriteria, q.ToleranceTimePct, q.ToleranceCostPct, q.ToleranceScopePct, q.State, q.OpenExceptionID, q.Version, q.BaselineNo).Updates(r); e != nil {
		return Receipt{}, e
	}
	// Store only public decision facts: never retain cookies, proofs or an
	// editable canonical scope request in immutable decision history.
	payload, e := json.Marshal(struct {
		DecisionID      *guid.ID         `json:"decision_id,omitempty"`
		Budget          *ToleranceBudget `json:"budget,omitempty"`
		ChangeID        *guid.ID         `json:"change_record_id,omitempty"`
		ResumeCondition string           `json:"resume_condition,omitempty"`
	}{commandDecisionID, in.Budget, changeID, in.ResumeCondition})
	if e != nil {
		return Receipt{}, e
	}
	stored := string(payload)
	at := now()
	actor := authn.PrincipalIDFromContext(ctx)
	ex.State = "DECIDED"
	ex.Decision = &in.Decision
	ex.DecisionPayload = &stored
	ex.DecidedAt = &at
	ex.DecidedBy = &actor
	ex.DecisionReason = &in.Reason
	ex.Version++
	x := query.BindToleranceException(tx, m.names)
	if _, e = x.WithContext(ctx).Where(x.TenantID.Eq(ex.TenantID), x.ID.Eq(id)).Select(x.State, x.Decision, x.DecisionPayload, x.DecidedAt, x.DecidedBy, x.DecisionReason, x.Version).Updates(ex); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "exception_decided", "exception_decide", ex.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: ex.Version, State: ex.State}, nil
}

func (m *Module) archiveProjectDraftPackagesOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	q := query.BindDeliveryPackage(tx, m.names)
	rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.Eq("DRAFT"), q.ArchivedAt.IsNull()).Order(q.ID).Find()
	if e != nil {
		return e
	}
	at := now()
	for _, row := range rows {
		row.ArchivedAt, row.UpdatedAt = &at, at
		row.Version++
		if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID)).Select(q.ArchivedAt, q.UpdatedAt, q.Version).Updates(row); e != nil {
			return e
		}
		if e = m.event(ctx, tx, project, row.ID, "package_archived", "exception_decide", row.Version); e != nil {
			return e
		}
	}
	return nil
}
