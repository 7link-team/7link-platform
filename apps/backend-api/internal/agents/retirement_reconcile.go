package agents

import (
	"context"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Terminal runtime revocation is never undone by delayed source reclamation.
// Every sweep retains the initiating Human's current authority. One source
// item and its durable outcome commit together; a failure remains retryable.
func (m *Module) reconcileCompletedRetirementOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int) error {
	q := query.BindRetirement(tx, m.names)
	read, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return err
	}
	if read.State != "COMPLETED" || read.Version != version {
		return ErrConflict
	}
	if read.Precheck.Progress != nil && read.Precheck.Progress.ReconciliationStatus == "COMPLETE" {
		return nil
	}
	if m.retirementWork.PrincipalIDsOn == nil || m.retirementHandover.PrepareOn == nil || m.retirementHandover.ReconcileOn == nil || m.retirementWork.ListOn == nil || m.retirementWork.ReleaseOn == nil {
		return ErrUnavailable
	}
	ids, err := m.retirementWork.PrincipalIDsOn(ctx, tx, tenant, read.AgentPrincipalID)
	if err != nil {
		return retirementWorkError(err)
	}
	if err = m.retirementHandover.PrepareOn(ctx, tx, tenant, read.AgentPrincipalID, ids...); err != nil {
		return err
	}
	if _, err = m.retirementOn(ctx, tx, tenant, id, "agent_retire", false); err != nil {
		return err
	}
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil {
		return err
	}
	if r.State != "COMPLETED" || r.Version != version {
		return ErrConflict
	}
	if m.retirementWork.ClearLeadsOn != nil {
		if err := m.retirementWork.ClearLeadsOn(ctx, tx, tenant, r.AgentPrincipalID); err != nil {
			return retirementWorkError(err)
		}
	}
	ready, err := m.retirementHandover.ReconcileOn(ctx, tx, tenant, r.AgentPrincipalID, id)
	if err != nil {
		return err
	}
	if !ready {
		return nil
	}
	rows, err := m.retirementWork.ListOn(ctx, tx, tenant, r.AgentPrincipalID)
	if err != nil {
		return retirementWorkError(err)
	}
	if r.Precheck.Progress == nil {
		r.Precheck.Progress = &model.RetirementProgress{}
	}
	p := r.Precheck.Progress
	if len(rows) > 0 {
		item := rows[0]
		if err = m.retirementWork.ReleaseOn(ctx, tx, tenant, r.AgentPrincipalID, id, item); err != nil {
			return retirementWorkError(err)
		}
		p.ReassignResult = append(p.ReassignResult, model.RetirementTransferResult{WorkItemID: item.WorkItemID, State: "RELEASED", Code: "POST_RETIREMENT_RECLAIM"})
		p.ReconciliationStatus = "PENDING"
	} else {
		p.ReconciliationStatus = "COMPLETE"
	}
	r.Version++
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version), q.State.Eq("COMPLETED")).Select(q.Precheck, q.Version).Updates(r)
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrConflict
	}
	return m.recordRetirementOn(ctx, tx, r, "SOURCE_RECONCILED")
}
