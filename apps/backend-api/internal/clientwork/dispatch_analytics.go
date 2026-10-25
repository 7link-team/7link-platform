package clientwork

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"log/slog"
	"math"
	"math/big"
	"slices"
	"time"
)

type DispatchAnalytics struct {
	FirstDecisions       int64  `json:"first_decisions"`
	FirstPassAccepts     int64  `json:"first_pass_accepts"`
	AcceptedItems        int64  `json:"accepted_items"`
	CostOnAcceptedMicros *int64 `json:"cost_on_accepted_micros"`
	CPAMicros            *int64 `json:"cpa_micros"`
	CostAvailable        bool   `json:"cost_available"`
}

func (m *Module) injectDispatchAnalytics(s *module.Services) error {
	var err error
	if m.reworkDisposition, err = module.Resolve[delivery.RejectedWorkDisposition](s); err != nil {
		return err
	}
	if m.questionDrafts, err = module.Resolve[delivery.ClarificationDraftAccess](s); err != nil {
		return err
	}
	if m.clientSigners, err = module.Resolve[delivery.ClientSignerFacts](s); err != nil {
		return err
	}
	if m.intakeProjects, err = module.Resolve[delivery.IntakeProjectCreation](s); err != nil {
		return err
	}
	if m.dispatchCatalog, err = module.Resolve[agents.SkillCatalog](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.dispatchExecutions, err = module.Resolve[execution.DispatchHistory](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.dispatchDecisions, err = module.Resolve[delivery.DispatchDecisionHistory](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.dispatchCatalog.ForAgentOn == nil || m.dispatchExecutions.ForWorkOn == nil || m.dispatchDecisions.ReadOn == nil {
		slog.Warn("dispatch analytics providers unavailable; measured quality/cost will remain unavailable")
	}
	return nil
}

func roundedRatio(n, d int64) int64 {
	if d <= 0 {
		return 0
	}
	return (n + d/2) / d
}
func dispatchMeasuredFPA(a, n, teamA, teamN int64) int {
	if teamN == 0 {
		return int(roundedRatio(10000*(a+2), n+4))
	}
	return int(roundedRatio(10000*(a*teamN+4*teamA), (n+4)*teamN))
}
func dispatchCostBP(value, low, high int64) int {
	value = max(low, min(high, value))
	if high == low {
		return 10000
	}
	// Costs can approach int64's limit; normalization must not overflow.
	numerator := new(big.Int).Mul(big.NewInt(high-value), big.NewInt(10000))
	denominator := big.NewInt(high - low)
	numerator.Add(numerator, new(big.Int).Div(new(big.Int).Set(denominator), big.NewInt(2)))
	return int(numerator.Div(numerator, denominator).Int64())
}

// measuredDispatchOn uses all authorized same-type history, not only today's
// winning claims. The frozen scope is explicit: unauthorized projects are never
// exposed or silently represented as a complete tenant-wide dataset.
func (m *Module) measuredDispatchOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind string, at time.Time) (map[guid.ID]*DispatchAnalytics, int64, int64, error) {
	return m.measuredDispatchWindowOn(ctx, tx, tenant, kind, at, 90*24*time.Hour)
}

func (m *Module) measuredDispatchWindowOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind string, at time.Time, window time.Duration) (map[guid.ID]*DispatchAnalytics, int64, int64, error) {
	if m.dispatchDecisions.ReadOn == nil || m.dispatchExecutions.ForWorkOn == nil {
		return nil, 0, 0, ErrUnavailable
	}
	works, err := m.dispatchDecisions.ReadOn(ctx, tx, tenant, kind, at)
	if err != nil {
		return nil, 0, 0, err
	}
	byProject := map[guid.ID][]guid.ID{}
	for _, work := range works {
		if len(work.Claims) > 0 {
			byProject[work.ProjectID] = append(byProject[work.ProjectID], work.WorkItemID)
		}
	}
	executions := map[guid.ID]execution.DispatchExecutionFact{}
	projects := make([]guid.ID, 0, len(byProject))
	for project := range byProject {
		projects = append(projects, project)
	}
	slices.Sort(projects)
	for _, project := range projects {
		ids := byProject[project]
		for start := 0; start < len(ids); start += 500 {
			rows, err := m.dispatchExecutions.ForWorkOn(ctx, tx, tenant, project, ids[start:min(start+500, len(ids))], at)
			if err != nil {
				return nil, 0, 0, err
			}
			for _, row := range rows {
				executions[row.ID] = row
			}
		}
	}
	stats := map[guid.ID]*DispatchAnalytics{}
	get := func(id guid.ID) *DispatchAnalytics {
		if stats[id] == nil {
			stats[id] = &DispatchAnalytics{CostAvailable: true, CostOnAcceptedMicros: new(int64)}
		}
		return stats[id]
	}
	since := at.Add(-window)
	teamA, teamN := int64(0), int64(0)
	for _, work := range works {
		owner := func(claim delivery.DispatchClaimFact) (guid.ID, error) {
			if claim.ExecutionID == nil {
				return 0, nil
			} // Manual Human claims are not Agent performance.
			e, ok := executions[*claim.ExecutionID]
			if !ok || e.WorkItemID != work.WorkItemID || e.ExecutorPrincipalID <= 0 {
				return 0, ErrUnavailable
			}
			return e.ExecutorPrincipalID, nil
		}
		if len(work.Decisions) > 0 {
			first := work.Decisions[0]
			if !first.CreatedAt.Before(since) && !first.CreatedAt.After(at) {
				counted := map[guid.ID]bool{}
				for _, claim := range work.Claims {
					if claim.CreatedAt.After(first.CreatedAt) {
						continue
					}
					id, err := owner(claim)
					if err != nil {
						return nil, 0, 0, err
					}
					if id > 0 && !counted[id] {
						counted[id] = true
						s := get(id)
						s.FirstDecisions++
						if first.Action == "APPROVE" {
							s.FirstPassAccepts++
						}
					}
				}
				if len(work.Claims) > 0 {
					teamN++
					if first.Action == "APPROVE" {
						teamA++
					}
				}
			}
		}
		if work.AcceptedAt == nil || work.AcceptedClaimID == nil || work.AcceptedAt.Before(since) || work.AcceptedAt.After(at) {
			continue
		}
		var acceptedOwner guid.ID
		for _, claim := range work.Claims {
			if claim.ID == *work.AcceptedClaimID {
				id, err := owner(claim)
				if err != nil {
					return nil, 0, 0, err
				}
				acceptedOwner = id
			}
		}
		if acceptedOwner == 0 {
			continue
		}
		s := get(acceptedOwner)
		s.AcceptedItems++
		ids := []guid.ID{}
		for _, e := range executions {
			if e.WorkItemID == work.WorkItemID && e.ExecutorPrincipalID == acceptedOwner {
				ids = append(ids, e.ID)
			}
		}
		slices.Sort(ids)
		if m.forecastCosts.ExecutionsOn == nil || len(ids) == 0 || len(ids) > 500 {
			s.CostAvailable = false
			s.CostOnAcceptedMicros = nil
			continue
		}
		cost, available, err := m.forecastCosts.ExecutionsOn(ctx, tx, tenant, work.ProjectID, ids)
		if err != nil {
			return nil, 0, 0, err
		}
		if !available || cost < 0 {
			s.CostAvailable = false
			s.CostOnAcceptedMicros = nil
			continue
		}
		if s.CostOnAcceptedMicros != nil {
			if *s.CostOnAcceptedMicros > math.MaxInt64-cost {
				return nil, 0, 0, ErrUnavailable
			}
			*s.CostOnAcceptedMicros += cost
		}
	}
	for _, s := range stats {
		if s.AcceptedItems > 0 && s.CostAvailable && s.CostOnAcceptedMicros != nil {
			v := *s.CostOnAcceptedMicros / s.AcceptedItems
			s.CPAMicros = &v
		}
	}
	return stats, teamA, teamN, nil
}

func (m *Module) enrichDispatchAnalyticsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, out *DispatchSnapshot) error {
	if m.dispatchCatalog.ForAgentOn != nil {
		complete := true
		for i := range out.Candidates {
			c := &out.Candidates[i]
			catalog, err := m.dispatchCatalog.ForAgentOn(ctx, tx, tenant, c.PrincipalID)
			if err != nil {
				if errors.Is(err, agents.ErrForbidden) || errors.Is(err, agents.ErrUnavailable) {
					complete = false
					continue
				}
				return err
			}
			c.Catalog = &catalog
			if !catalog.TagsAvailable && len(out.Work.RequiredTags) > 0 && isDispatchAuto(ctx) {
				out.GuardReasons = append(out.GuardReasons, "SKILL_CATALOG_TAGS_UNAVAILABLE")
			}
		}
		if complete {
			out.Limitations = slices.DeleteFunc(out.Limitations, func(code string) bool { return code == "SKILL_CATALOG_UNAVAILABLE" })
			out.Limitations = append(out.Limitations, "SKILL_TAGS_AND_MOUNTS_UNAVAILABLE")
		}
	}
	if m.dispatchDecisions.ReadOn == nil || m.dispatchExecutions.ForWorkOn == nil {
		return nil
	}
	stats, teamA, teamN, err := m.measuredDispatchOn(ctx, tx, tenant, out.Work.Type, now())
	if err != nil {
		if errors.Is(err, ErrUnavailable) || errors.Is(err, execution.ErrCapabilityUnavailable) || errors.Is(err, execution.ErrForbidden) || errors.Is(err, delivery.ErrWorkForbidden) {
			return nil
		}
		return err
	}
	if isDispatchAuto(ctx) {
		out.GuardReasons = append(out.GuardReasons, "AUTHORIZED_HISTORY_SCOPE_REQUIRES_HUMAN")
	}
	out.AnalyticsAvailable = true
	out.AnalyticsScope = "AUTHORIZED_WORK_ITEMS"
	out.ScoringVersion = "s1-measured"
	out.HistorySamples90D = &teamN
	out.Limitations = slices.DeleteFunc(out.Limitations, func(code string) bool { return code == "QUALITY_COST_HISTORY_UNAVAILABLE" })
	costs := []int64{}
	for _, s := range stats {
		if s.CPAMicros != nil {
			costs = append(costs, *s.CPAMicros)
		}
	}
	slices.Sort(costs)
	var median *int64
	if len(costs) > 0 {
		v := costs[len(costs)/2]
		if len(costs)%2 == 0 {
			a, b := costs[len(costs)/2-1], v
			v = a + (b-a)/2
		}
		median = &v
	}
	low, high := int64(math.MaxInt64), int64(0)
	for i := range out.Candidates {
		c := &out.Candidates[i]
		s := stats[c.PrincipalID]
		if s == nil {
			s = &DispatchAnalytics{CostAvailable: true}
		}
		c.Analytics = s
		c.HistoryAvailable = true
		c.Components.FPABP = dispatchMeasuredFPA(s.FirstPassAccepts, s.FirstDecisions, teamA, teamN)
		c.EstimatedCostMicros = s.CPAMicros
		if c.EstimatedCostMicros == nil {
			c.EstimatedCostMicros = median
		}
		if len(c.IneligibleCodes) == 0 && c.EstimatedCostMicros != nil {
			low = min(low, *c.EstimatedCostMicros)
			high = max(high, *c.EstimatedCostMicros)
		}
	}
	for i := range out.Candidates {
		c := &out.Candidates[i]
		if c.EstimatedCostMicros != nil && low != math.MaxInt64 {
			c.Components.CostBP = dispatchCostBP(*c.EstimatedCostMicros, low, high)
		}
		c.Score = 35*c.Components.SkillBP + 30*c.Components.FPABP + 15*c.Components.CostBP + 15*c.Components.LoadBP + 5*c.Components.DiversityBP
	}
	if median == nil {
		out.Limitations = append(out.Limitations, "COST_UNMEASURED_EQUAL_SCORES")
	}
	return nil
}
