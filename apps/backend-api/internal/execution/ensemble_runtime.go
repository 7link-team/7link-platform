package execution

import (
	"context"
	"encoding/json"
	"slices"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

type ensembleCandidate struct {
	HeadCommit string         `json:"head_commit"`
	Evidence   []EvidenceFact `json:"evidence"`
}

// Completion proves a branch outcome, not selection or a submitted Claim.
// Preserve the exact runner-observed head and evidence for the owning selection
// command; this write shares the receipt, cost settlement and outbox transaction.
func (m *Module) saveEnsembleCandidateOn(ctx context.Context, tx *gorm.DB, row *model.Execution, head string, evidence []EvidenceFact) error {
	if row.BranchReason != "ENSEMBLE" || row.PendingClaimJSON != nil || !commitHash(head) || len(evidence) == 0 {
		return ErrInvalidArgument
	}
	raw, err := json.Marshal(ensembleCandidate{HeadCommit: head, Evidence: evidence})
	if err != nil {
		return err
	}
	q := query.BindExecutions(tx, m.names)
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(row.ID), q.Version.Eq(row.Version)).Updates(map[string]any{"pending_claim_json": string(raw)})
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}

func (m *Module) bindEnsembleRuntime(c fleet.EnsembleConnector) error {
	if c.CheckBranchOn == nil {
		return ErrCapabilityUnavailable
	}
	return c.BindRuntime(fleet.EnsembleRuntime{
		AuthorizeOn: func(ctx context.Context, tx *gorm.DB, tenant, project, work, plan guid.ID, branches []fleet.EnsembleLaunchBranch, cancel bool) error {
			permission := "execution_start"
			if cancel {
				permission = "execution_stop"
			}
			if e := m.humanAuthority(ctx, tx, tenant, project, permission); e != nil {
				return e
			}
			if cancel {
				for _, b := range branches {
					r, e := m.rowOn(ctx, tx, tenant, b.ExecutionID, false)
					if e != nil {
						return e
					}
					if r.ProjectID != project || r.WorkItemID != work || r.AssignmentID != b.AssignmentID {
						return ErrForbidden
					}
					if m.work.AuthorizeOn == nil {
						return ErrCapabilityUnavailable
					}
					if e = m.work.AuthorizeOn(ctx, tx, link(r, authn.PrincipalIDFromContext(ctx)), "CANCEL"); e != nil {
						return e
					}
				}
			}
			return nil
		},
		LaunchOn: func(ctx context.Context, tx *gorm.DB, tenant, project, work, plan guid.ID, branches []fleet.EnsembleLaunchBranch) error {
			inputs := make([]ExecutionCreate, 0, len(branches))
			for _, b := range branches {
				if e := c.CheckBranchOn(ctx, tx, tenant, project, work, plan, b.AssignmentID, b.ExecutionID); e != nil {
					return e
				}
				var p ExecutionPlan
				if json.Unmarshal(b.Plan, &p) != nil {
					return ErrInvalidArgument
				}
				inputs = append(inputs, ExecutionCreate{ProjectID: project, WorkItemID: work, AssignmentID: b.AssignmentID, MachineID: b.MachineID, Plan: p, ensemblePlanID: plan, ensembleExecutionID: b.ExecutionID})
			}
			_, e := m.parallelEnsembleOn(ctx, tx, tenant, inputs)
			return e
		}, CancelOn: func(ctx context.Context, tx *gorm.DB, tenant, project, work, plan guid.ID, branches []fleet.EnsembleLaunchBranch) error {
			for _, b := range branches {
				if e := c.CheckBranchOn(ctx, tx, tenant, project, work, plan, b.AssignmentID, b.ExecutionID); e != nil {
					return e
				}
				r, e := m.rowOn(ctx, tx, tenant, b.ExecutionID, false)
				if e != nil {
					return e
				}
				if r.ProjectID != project || r.WorkItemID != work || r.AssignmentID != b.AssignmentID || r.BranchReason != "ENSEMBLE" {
					return ErrForbidden
				}
				if slices.Contains([]string{"SUCCEEDED", "FAILED", "CANCELLED"}, r.State) { //nolint:misspell // Frozen protocol state.
					continue
				}
				if _, e = m.CancelOn(ctx, tx, tenant, r.ID, r.Version); e != nil {
					return e
				}
			}
			return nil
		}})
}

// This capability accepts only source-constructed inputs. No posted field can
// manufacture an approved branch, preallocated identity, or budget reservation.
func (m *Module) parallelEnsembleOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, inputs []ExecutionCreate) ([]ExecutionView, error) {
	out := []ExecutionView{}
	if len(inputs) < 2 || len(inputs) > 5 {
		return nil, ErrInvalidArgument
	}
	plan, project, work := inputs[0].ensemblePlanID, inputs[0].ProjectID, inputs[0].WorkItemID
	if plan <= 0 || transaction(tx, tenant, project) != nil {
		return nil, ErrForbidden
	}
	seen := map[guid.ID]bool{}
	for _, in := range inputs {
		if in.ensemblePlanID != plan || in.ProjectID != project || in.WorkItemID != work || in.ensembleExecutionID <= 0 || seen[in.AssignmentID] {
			return nil, ErrForbidden
		}
		seen[in.AssignmentID] = true
	}
	for _, in := range inputs {
		v, e := m.createOn(ctx, tx, tenant, in)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
