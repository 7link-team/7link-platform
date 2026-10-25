package planning

import (
	"context"
	"math"
	"slices"
	"time"

	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// teamLoopCostOn aggregates only the exact Agent's accepted execution starts.
// Actual project identities scope historical assignment and settlement readers;
// a team contract never substitutes a project or an estimated zero cost.
func (m *Module) teamLoopCostOn(ctx context.Context, tx *gorm.DB, contract *model.LoopContract, start, end time.Time) (int64, error) {
	if m.loopRuntime.executions.AgentOn == nil || m.loopRuntime.assignments.AgentsOn == nil || m.loopRuntime.settlements.ExecutionsOn == nil {
		return 0, ErrUnavailable
	}
	facts, err := m.loopRuntime.executions.AgentOn(ctx, tx, contract.TenantID, contract.AgentID, start, end)
	if err != nil {
		return 0, err
	}
	grouped := map[guid.ID][]execution.LoopExecutionFact{}
	seen := map[guid.ID]bool{}
	for _, f := range facts {
		if f.ID <= 0 || f.ProjectID <= 0 || f.AssignmentID <= 0 || f.StartedAt == nil || f.StartedAt.Before(start) || !f.StartedAt.Before(end) || seen[f.ID] {
			return 0, ErrUnavailable
		}
		seen[f.ID] = true
		grouped[f.ProjectID] = append(grouped[f.ProjectID], f)
	}
	projects := make([]guid.ID, 0, len(grouped))
	for project := range grouped {
		projects = append(projects, project)
	}
	slices.Sort(projects)
	total := int64(0)
	for _, project := range projects {
		rows := grouped[project]
		for len(rows) > 0 {
			count := min(1000, len(rows))
			batch := rows[:count]
			rows = rows[count:]
			assignments := make([]guid.ID, 0, count)
			executions := make([]guid.ID, 0, count)
			for _, f := range batch {
				assignments = append(assignments, f.AssignmentID)
				executions = append(executions, f.ID)
			}
			holders, err := m.loopRuntime.assignments.AgentsOn(ctx, tx, contract.TenantID, project, assignments)
			if err != nil {
				return 0, err
			}
			for _, f := range batch {
				if holders[f.AssignmentID] != contract.AgentID {
					return 0, ErrUnavailable
				}
			}
			cost, settled, err := m.loopRuntime.settlements.ExecutionsOn(ctx, tx, contract.TenantID, project, executions)
			if err != nil {
				return 0, err
			}
			if !settled || cost < 0 || total > math.MaxInt64-cost {
				return 0, ErrUnavailable
			}
			total += cost
		}
	}
	return total, nil
}

func loopProject(contract *model.LoopContract) guid.ID {
	if contract.ProjectID != nil {
		return *contract.ProjectID
	}
	return 0
}

// Team anchors lock both the acting principal and target Agent. Acquire every
// participant before IAM and the contract so a concurrent owner or lifecycle
// change cannot reverse that order through the thread authorization callback.
func (m *Module) lockTeamLoopParticipantsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, contract *model.LoopContract) error {
	if contract.ProjectID != nil {
		return nil
	}
	if m.principals.LockActiveRow == nil || m.agents.ByPrincipalOn == nil {
		return ErrUnavailable
	}
	observed, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, contract.AgentID)
	if err != nil {
		return err
	}
	if !found || observed.PrincipalID != contract.AgentID || observed.TenantID != tenant {
		return ErrInvalid
	}
	ids := []guid.ID{contract.AgentID, contract.OwnerPrincipalID, observed.OwnerPrincipalID, authn.PrincipalIDFromContext(ctx)}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		if id > 0 {
			if _, _, err = m.principals.LockActiveRow(ctx, tx, id); err != nil {
				return err
			}
		}
	}
	current, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, contract.AgentID)
	if err != nil {
		return err
	}
	if !found || current.PrincipalID != observed.PrincipalID || current.TenantID != observed.TenantID || current.OwnerPrincipalID != observed.OwnerPrincipalID || current.State != observed.State {
		return ErrConflict
	}
	return nil
}

func (m *Module) commandLoopOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, write ...bool) (*model.LoopContract, error) {
	q := query.BindLoopContracts(tx, m.names)
	observed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return m.loopOn(ctx, tx, tenant, id)
	}
	if err = m.lockTeamLoopParticipantsOn(ctx, tx, tenant, observed); err != nil {
		return nil, err
	}
	mutation := len(write) == 0 || write[0]
	if err = m.loopAuthority(ctx, tx, tenant, observed, mutation); err != nil {
		return nil, err
	}
	if mutation {
		if err = m.loopProjectWriteOn(ctx, tx, tenant, observed.ProjectID); err != nil {
			return nil, err
		}
	}
	current, err := m.loopOn(ctx, tx, tenant, id)
	if err != nil {
		return nil, err
	}
	if current.AgentID != observed.AgentID || current.OwnerPrincipalID != observed.OwnerPrincipalID || (current.ProjectID == nil) != (observed.ProjectID == nil) || loopProject(current) != loopProject(observed) {
		return nil, ErrConflict
	}
	return current, nil
}
