package clientwork

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) bindAgentPerformance(s *module.Services) error {
	c, err := module.Resolve[agents.AgentPerformanceConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.BindHistory(m.agentPerformanceHistoryOn)
}

// Reuse the dispatch quality/cost definitions; claims and rework use their own
// event timestamps, independently of the work's first decision or acceptance.
func (m *Module) agentPerformanceHistoryOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, days int, kind string, at time.Time) ([]agents.AgentPerformanceMeasures, error) {
	if days != 30 && days != 90 || agent <= 0 || at.IsZero() || kind != "" && !slices.Contains(agents.PerformanceWorkTypes, kind) {
		return nil, agents.ErrInvalid
	}
	if m.dispatchDecisions.ReadOn == nil || m.dispatchExecutions.ForWorkOn == nil {
		return nil, agents.ErrUnavailable
	}
	kinds := agents.PerformanceWorkTypes
	if kind != "" {
		kinds = []string{kind}
	}
	out := make([]agents.AgentPerformanceMeasures, 0, len(kinds))
	since := at.Add(-time.Duration(days) * 24 * time.Hour)
	for _, kind := range kinds {
		stats, _, _, err := m.measuredDispatchWindowOn(ctx, tx, tenant, kind, at, time.Duration(days)*24*time.Hour)
		if err != nil {
			return nil, err
		}
		v := agents.AgentPerformanceMeasures{WorkItemType: kind, CostOnAcceptedMicros: new(int64)}
		if s := stats[agent]; s != nil {
			v.FirstDecisions, v.FirstPassAccepts, v.AcceptedItems = s.FirstDecisions, s.FirstPassAccepts, s.AcceptedItems
		}
		works, err := m.dispatchDecisions.ReadOn(ctx, tx, tenant, kind, at)
		if err != nil {
			return nil, err
		}
		byProject := map[guid.ID][]guid.ID{}
		for _, work := range works {
			if len(work.Claims) > 0 {
				byProject[work.ProjectID] = append(byProject[work.ProjectID], work.WorkItemID)
			}
		}
		executions := map[guid.ID]execution.DispatchExecutionFact{}
		projects := make([]guid.ID, 0, len(byProject))
		for id := range byProject {
			projects = append(projects, id)
		}
		slices.Sort(projects)
		for _, project := range projects {
			ids := byProject[project]
			for start := 0; start < len(ids); start += 500 {
				rows, err := m.dispatchExecutions.ForWorkOn(ctx, tx, tenant, project, ids[start:min(start+500, len(ids))], at)
				if err != nil {
					return nil, err
				}
				for _, row := range rows {
					executions[row.ID] = row
				}
			}
		}
		for _, work := range works {
			owned := map[guid.ID]bool{}
			for _, claim := range work.Claims {
				if claim.ExecutionID == nil {
					continue
				}
				e, ok := executions[*claim.ExecutionID]
				if !ok || e.WorkItemID != work.WorkItemID || e.ExecutorPrincipalID <= 0 {
					return nil, agents.ErrUnavailable
				}
				if e.ExecutorPrincipalID != agent {
					continue
				}
				owned[claim.ID] = true
				if !claim.CreatedAt.Before(since) && !claim.CreatedAt.After(at) {
					v.Claims++
				}
			}
			for _, d := range work.Decisions {
				if owned[d.ClaimID] && (d.Action == "REJECT" || d.Action == "REQUEST_CHANGES") && !d.CreatedAt.Before(since) && !d.CreatedAt.After(at) {
					v.ReworkDecisions++
				}
			}
			if work.AcceptedAt == nil || work.AcceptedClaimID == nil || !owned[*work.AcceptedClaimID] || work.AcceptedAt.Before(since) || work.AcceptedAt.After(at) {
				continue
			}
			// Assignment-free purposes (planning, skill distillation) are not
			// delivery execution costs. Keep every assigned retry/ensemble branch.
			ids := []guid.ID{}
			for _, e := range executions {
				if e.WorkItemID == work.WorkItemID && e.ExecutorPrincipalID == agent && e.AssignmentID > 0 {
					ids = append(ids, e.ID)
				}
			}
			slices.Sort(ids)
			if len(ids) == 0 || len(ids) > 500 || m.forecastCosts.ExecutionsOn == nil {
				v.CostOnAcceptedMicros = nil
				continue
			}
			cost, available, err := m.forecastCosts.ExecutionsOn(ctx, tx, tenant, work.ProjectID, ids)
			if err != nil {
				return nil, err
			}
			if !available || cost < 0 {
				v.CostOnAcceptedMicros = nil
				continue
			}
			if v.CostOnAcceptedMicros != nil {
				if *v.CostOnAcceptedMicros > math.MaxInt64-cost {
					return nil, agents.ErrUnavailable
				}
				*v.CostOnAcceptedMicros += cost
			}
		}
		out = append(out, v)
	}
	return out, nil
}
