package agents

import (
	"context"
	"errors"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

func (m *Module) prepareRetirementAdvanceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
	q := query.BindRetirement(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if r.State != "IN_PROGRESS" {
		return nil
	}
	if r.Step == "REASSIGN" {
		return m.prepareRetirementTransferOn(ctx, tx, tenant, id, 0)
	}
	if r.Step == "CLEANUP" {
		return m.prepareRetirementCleanupOn(ctx, tx, tenant, id)
	}
	return m.prepareRetirementHandoverOn(ctx, tx, tenant, id)
}

func (m *Module) advanceRetirementOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version, agentVersion int) (model.Retirement, error) {
	q := query.BindRetirement(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Retirement{}, ErrNotFound
	}
	if err != nil {
		return model.Retirement{}, err
	}
	if r.Step == "REASSIGN" {
		return m.advanceRetirementTransferOn(ctx, tx, tenant, id, version)
	}
	if r.Step == "CLEANUP" {
		return m.advanceRetirementCleanupOn(ctx, tx, tenant, id, version, agentVersion)
	}
	return m.advanceRetirementHandoverOn(ctx, tx, tenant, id, version, false)
}

func (m *Module) prepareRetirementTransferOn(ctx context.Context, tx *gorm.DB, tenant, id, successor guid.ID) error {
	q := query.BindRetirement(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if r.State != "IN_PROGRESS" {
		return nil
	}
	if m.retirementWork.PrincipalIDsOn == nil || m.retirementHandover.PrepareOn == nil {
		return ErrUnavailable
	}
	ids, err := m.retirementWork.PrincipalIDsOn(ctx, tx, tenant, r.AgentPrincipalID)
	if err != nil {
		return retirementWorkError(err)
	}
	if successor <= 0 && r.Precheck.Progress != nil && r.Precheck.Progress.SuccessorPrincipalID != nil {
		successor = *r.Precheck.Progress.SuccessorPrincipalID
	}
	if successor > 0 {
		ids = append(ids, successor)
	}
	return m.retirementHandover.PrepareOn(ctx, tx, tenant, r.AgentPrincipalID, ids...)
}

func (m *Module) selectRetirementSuccessorOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, kind string, successor *guid.ID) (model.Retirement, error) {
	var empty model.Retirement
	if (kind != "HUMAN" && kind != "AGENT" && kind != "LATER") || (kind == "LATER" && successor != nil) || (kind != "LATER" && (successor == nil || *successor <= 0)) {
		return empty, ErrInvalid
	}
	var target guid.ID
	if successor != nil {
		target = *successor
	}
	if err := m.prepareRetirementTransferOn(ctx, tx, tenant, id, target); err != nil {
		return empty, err
	}
	r, err := m.retirementOn(ctx, tx, tenant, id, "agent_retire", true)
	if err != nil {
		return empty, err
	}
	p := r.Precheck.Progress
	if version < 1 || r.Version != version || r.State != "IN_PROGRESS" || r.Step != "SUCCESSOR" || p == nil || p.HandoverArtifactID == nil {
		return empty, ErrConflict
	}
	if target == r.AgentPrincipalID {
		return empty, ErrInvalid
	}
	if target > 0 {
		principal, active, err := m.principals.LockActiveRow(ctx, tx, target)
		if err != nil {
			return empty, err
		}
		if !active {
			return empty, ErrInvalid
		}
		member, err := m.members.IsMemberActiveOn(ctx, tx, tenant, target)
		if err != nil {
			return empty, err
		}
		if !member {
			return empty, ErrInvalid
		}
		if kind == "HUMAN" {
			human, found, err := m.principals.HumanByLogin(ctx, tx, principal.LoginName)
			if err != nil {
				return empty, err
			}
			if !found || human.ID != target {
				return empty, ErrInvalid
			}
		} else {
			agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, target)
			if err != nil {
				return empty, err
			}
			if !found || agent.State != "ACTIVE" {
				return empty, ErrInvalid
			}
		}
	}
	if m.retirementHandover.ReadOn == nil {
		return empty, ErrUnavailable
	}
	artifact, err := m.retirementHandover.ReadOn(ctx, tx, tenant, r.AgentPrincipalID, id)
	if err != nil {
		return empty, err
	}
	if artifact.ID != *p.HandoverArtifactID || artifact.Source != p.HandoverSource {
		return empty, ErrConflict
	}
	p.SuccessorKind, p.SuccessorPrincipalID = kind, successor
	r.Step, r.StepStartedAt = "REASSIGN", m.now()
	if err = m.saveRetirementProgressOn(ctx, tx, r, version, "SUCCESSOR_SELECTED"); err != nil {
		return empty, err
	}
	return *r, nil
}

// One source mutation and its durable result commit together. A domain failure
// rolls back that item's savepoint and is recorded; infrastructure failures
// abort the entire attempt so a later job can retry it.
func (m *Module) advanceRetirementTransferOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int) (model.Retirement, error) {
	var empty model.Retirement
	if err := m.prepareRetirementTransferOn(ctx, tx, tenant, id, 0); err != nil {
		return empty, err
	}
	r, err := m.retirementOn(ctx, tx, tenant, id, "agent_retire", true)
	if err != nil {
		return empty, err
	}
	p := r.Precheck.Progress
	if version < 1 || r.Version != version || r.State != "IN_PROGRESS" || r.Step != "REASSIGN" || p == nil || p.HandoverArtifactID == nil || p.SuccessorKind == "" {
		return empty, ErrConflict
	}
	if m.retirementWork.ListOn == nil || m.retirementWork.ReleaseOn == nil || m.retirementWork.TransferOn == nil {
		return empty, ErrUnavailable
	}
	if m.retirementHandover.ReconcileOn != nil {
		ready, err := m.retirementHandover.ReconcileOn(ctx, tx, tenant, r.AgentPrincipalID, id)
		if err != nil {
			return empty, err
		}
		if !ready {
			return *r, nil
		}
	} else if len(r.Precheck.Executions) != 0 {
		return empty, ErrUnavailable
	}
	completed := make(map[guid.ID]bool, len(p.ReassignResult))
	for _, result := range p.ReassignResult {
		completed[result.WorkItemID] = true
	}
	var next *model.RetirementWorkItem
	for i := range r.Precheck.WorkItems {
		if !completed[r.Precheck.WorkItems[i].WorkItemID] {
			next = &r.Precheck.WorkItems[i]
			break
		}
	}
	if next == nil {
		r.Step, r.StepStartedAt = "CLEANUP", m.now()
	} else {
		live, err := m.retirementWork.ListOn(ctx, tx, tenant, r.AgentPrincipalID)
		if err != nil {
			return empty, retirementWorkError(err)
		}
		var current *delivery.RetirementAssignment
		for i := range live {
			if live[i].AssignmentID == next.AssignmentID && live[i].WorkItemID == next.WorkItemID && live[i].ProjectID == next.ProjectID {
				current = &live[i]
				break
			}
		}
		result := model.RetirementTransferResult{WorkItemID: next.WorkItemID, State: "SOURCE_CHANGED", Code: "ASSIGNMENT_NO_LONGER_ACTIVE"}
		if current != nil {
			var assignment guid.ID
			err = tx.Transaction(func(itemTx *gorm.DB) error {
				if p.SuccessorKind == "LATER" {
					return m.retirementWork.ReleaseOn(ctx, itemTx, tenant, r.AgentPrincipalID, id, *current)
				}
				if p.SuccessorPrincipalID == nil {
					return ErrInvalid
				}
				var err error
				assignment, err = m.retirementWork.TransferOn(ctx, itemTx, tenant, r.AgentPrincipalID, *current, delivery.RetirementTransferInput{RetirementID: id, ArtifactID: *p.HandoverArtifactID, AssigneeKind: p.SuccessorKind, AssigneePrincipalID: *p.SuccessorPrincipalID})
				if err == nil && assignment <= 0 {
					return ErrUnavailable
				}
				return err
			})
			if err != nil {
				switch {
				case errors.Is(err, delivery.ErrWorkVersion):
					result.State, result.Code = "FAILED", "SOURCE_VERSION_CHANGED"
				case errors.Is(err, delivery.ErrAssignmentState), errors.Is(err, delivery.ErrInvalidMember):
					result.State, result.Code = "FAILED", "ASSIGNEE_OR_WORK_NOT_ELIGIBLE"
				case errors.Is(err, delivery.ErrWorkForbidden), errors.Is(err, delivery.ErrProjectForbidden):
					result.State, result.Code = "FAILED", "CURRENT_WORK_AUTHORITY_DENIED"
				default:
					return empty, err
				}
			} else if p.SuccessorKind == "LATER" {
				result.State, result.Code = "RELEASED", ""
			} else {
				result.State, result.Code, result.AssignmentID = "REASSIGNED", "", &assignment
			}
		}
		p.ReassignResult = append(p.ReassignResult, result)
	}
	if err = m.saveRetirementProgressOn(ctx, tx, r, version, "REASSIGN_PROGRESS"); err != nil {
		return empty, err
	}
	return *r, nil
}
