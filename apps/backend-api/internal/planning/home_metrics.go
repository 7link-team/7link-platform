package planning

import (
	"context"
	"errors"
	"math/big"
	"slices"
	"strconv"
	"time"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// WorkbenchKPI preserves exact source numerators and denominators as decimal
// text. Value is a decimal ratio; currency remains micros and time minutes.
type WorkbenchKPI struct {
	MeanHours         *string `json:"mean_hours,omitempty"`
	P90Hours          *string `json:"p90_hours,omitempty"`
	Key               string  `json:"key" enum:"COST_PER_ACCEPTED,FIRST_PASS_RATE,ATTENTION_MIN_PER_ACCEPTED,REWORK_P50_HOURS,EVIDENCE_COVERAGE"`
	Value             *string `json:"value"`
	Numerator         *string `json:"numerator"`
	Denominator       *string `json:"denominator"`
	UnavailableReason string  `json:"unavailable_reason,omitempty"`
}

func homeRatio(key string, numerator, denominator int64) WorkbenchKPI {
	n, d := strconv.FormatInt(numerator, 10), strconv.FormatInt(denominator, 10)
	out := WorkbenchKPI{Key: key, Numerator: &n, Denominator: &d}
	if denominator == 0 {
		out.UnavailableReason = "NO_DENOMINATOR"
		return out
	}
	v := new(big.Rat).SetFrac(big.NewInt(numerator), big.NewInt(denominator)).FloatString(6)
	out.Value = &v
	return out
}

func (m *Module) homeMetricsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, projects []guid.ID, out *WorkbenchSummary) error {
	out.ProjectHealth = []delivery.HomeProjectFacts{}
	out.KPIs = []WorkbenchKPI{
		{Key: "COST_PER_ACCEPTED", UnavailableReason: "FIRST_ACCEPTANCE_HISTORY"},
		{Key: "FIRST_PASS_RATE", UnavailableReason: "QUALITY_BRANCH_HISTORY"},
		{Key: "ATTENTION_MIN_PER_ACCEPTED", UnavailableReason: "HUMAN_ATTENTION_DURATION"},
		{Key: "REWORK_P50_HOURS", UnavailableReason: "CLIENT_REJECTION_WORK_TIMELINE"},
		{Key: "EVIDENCE_COVERAGE", UnavailableReason: "CLAIM_EVIDENCE_HISTORY"},
	}
	if m.homeFacts == nil {
		return nil
	}
	// Replace mutable current acceptance summaries with actual source history.
	filtered := out.Activity[:0]
	for _, activity := range out.Activity {
		if activity.Kind != "WORK_ACCEPTED" {
			filtered = append(filtered, activity)
		}
	}
	out.Activity = filtered
	var accepted, firstPass, claimed, evidenced int64
	qualityKnown := m.qualityBranchBefore != nil
	activeLoads := map[guid.ID]int{}
	reworkKnown := m.homeReceipts != nil
	var cycles int64
	totalReworkMS := new(big.Int)
	var histogram [9]int64
	for _, project := range projects {
		facts, err := m.homeFacts(ctx, tx, tenant, project, out.From, out.To)
		if err != nil {
			return err
		}
		out.ProjectHealth = append(out.ProjectHealth, facts)
		if m.homeDelegations != nil {
			delegated, err := m.homeDelegations(ctx, tx, tenant, project, out.From, out.To)
			if err != nil {
				return err
			}
			for _, decision := range delegated {
				if slices.Contains(facts.VisibleWorkIDs, decision.WorkItemID) {
					out.Activity = append(out.Activity, WorkbenchActivity{Kind: "DELEGATED_ACCEPTANCE", ProjectID: project, ObjectID: decision.WorkItemID, PrincipalID: decision.ActorID, Title: decision.WorkItemID.String(), OccurredAt: decision.At})
				}
			}
		}
		timeline := append([]delivery.HomeWorkTransition(nil), facts.ReworkTimeline...)
		if m.homeReceipts != nil {
			receipts, err := m.homeReceipts(ctx, tx, tenant, project, out.To)
			if errors.Is(err, clientwork.ErrForbidden) || errors.Is(err, delivery.ErrWorkForbidden) {
				reworkKnown = false
			} else if err != nil {
				return err
			} else {
				reworkKnown = reworkKnown && receipts.Complete
				for _, event := range receipts.Events {
					if event.Rejected {
						if !slices.Contains(facts.VisibleWorkIDs, event.WorkItemID) {
							continue
						}
						timeline = append(timeline, delivery.HomeWorkTransition{WorkItemID: event.WorkItemID, At: event.At})
					} else if !event.At.Before(out.From) {
						out.Activity = append(out.Activity, WorkbenchActivity{Kind: "CLIENT_RECEIPT", ObjectID: event.PackageID, ProjectID: project, PrincipalID: event.ActorID, Title: event.PackageID.String(), OccurredAt: event.At})
					}
				}
			}
		}
		slices.SortStableFunc(timeline, func(a, b delivery.HomeWorkTransition) int { return a.At.Compare(b.At) })
		opened := map[guid.ID]time.Time{}
		for _, event := range timeline {
			if !event.Accepted {
				if _, exists := opened[event.WorkItemID]; !exists {
					opened[event.WorkItemID] = event.At
				}
				continue
			}
			if at, exists := opened[event.WorkItemID]; exists && !event.At.Before(out.From) {
				hours := event.At.Sub(at).Hours()
				totalReworkMS.Add(totalReworkMS, big.NewInt(event.At.Sub(at).Milliseconds()))
				bucket := 8
				for i, upper := range []float64{1, 4, 8, 24, 48, 72, 168, 336} {
					if hours <= upper {
						bucket = i
						break
					}
				}
				histogram[bucket]++
				cycles++
			}
			delete(opened, event.WorkItemID)
		}
		for _, activity := range facts.Activity {
			out.Activity = append(out.Activity, WorkbenchActivity{Kind: activity.Kind, ObjectID: activity.ObjectID, ProjectID: project, PrincipalID: activity.PrincipalID, Title: activity.Title, OccurredAt: activity.At})
		}
		for principal, active := range facts.ActiveLoads {
			activeLoads[principal] += active
		}
		claimed += int64(facts.ClaimedDays)
		evidenced += int64(facts.EvidencedDays)
		for _, receipt := range facts.FirstAcceptances {
			accepted++
			if receipt.PriorQualityRework || m.qualityBranchBefore == nil {
				continue
			}
			quality, err := m.qualityBranchBefore(ctx, tx, tenant, project, receipt.WorkItemID, receipt.At)
			if errors.Is(err, execution.ErrForbidden) {
				qualityKnown = false
				continue
			}
			if err != nil {
				return err
			}
			if !quality {
				firstPass++
			}
		}
	}
	if reworkKnown {
		out.KPIs[3] = homeRatio("REWORK_P50_HOURS", 0, cycles)
		if cycles > 0 {
			mean := new(big.Rat).SetFrac(totalReworkMS, new(big.Int).Mul(big.NewInt(cycles), big.NewInt(3600000))).FloatString(6)
			out.KPIs[3].MeanHours = &mean
			var cumulative int64
			p50Set := false
			for i, count := range histogram {
				cumulative += count
				if cumulative*2 >= cycles && !p50Set {
					p50Set = true
					v := []string{"1", "4", "8", "24", "48", "72", "168", "336", ">336"}[i]
					out.KPIs[3].Value = &v
					n := strconv.FormatInt(cumulative, 10)
					out.KPIs[3].Numerator = &n
				}
				if cumulative*10 >= cycles*9 {
					v := []string{"1", "4", "8", "24", "48", "72", "168", "336", ">336"}[i]
					out.KPIs[3].P90Hours = &v
					break
				}
			}
		}
	}
	for principal := range activeLoads {
		if !slices.ContainsFunc(out.Load, func(load WorkbenchLoad) bool { return load.PrincipalID == principal }) {
			load := WorkbenchLoad{PrincipalID: principal}
			if m.principals.ActiveRow != nil {
				member, err := m.members.IsMemberActiveOn(ctx, tx, tenant, principal)
				if err != nil {
					return err
				}
				if member {
					identity, active, err := m.principals.ActiveRow(ctx, tx, principal)
					if err != nil {
						return err
					}
					if active {
						load.DisplayName = identity.DisplayName
					}
				}
			}
			out.Load = append(out.Load, load)
		}
	}
	slices.SortFunc(out.Load, func(a, b WorkbenchLoad) int {
		if a.PrincipalID < b.PrincipalID {
			return -1
		}
		if a.PrincipalID > b.PrincipalID {
			return 1
		}
		return 0
	})
	for i := range out.Load {
		load := &out.Load[i]
		active := activeLoads[load.PrincipalID]
		load.Active = &active
		load.CapacityUnavailable = "CAPACITY_NOT_CONFIGURED_OR_NOT_VISIBLE"
		if m.homeCapacity != nil && load.PrincipalID > 0 {
			capacity, err := m.homeCapacity(ctx, tx, tenant, load.PrincipalID)
			if err != nil {
				return err
			}
			load.Capacity = capacity
			if capacity != nil {
				load.CapacityUnavailable = ""
			}
		}
	}
	if out.ExecutionCostMicros != nil {
		cost, err := strconv.ParseInt(*out.ExecutionCostMicros, 10, 64)
		if err != nil || cost < 0 {
			return ErrUnavailable
		}
		out.KPIs[0] = homeRatio("COST_PER_ACCEPTED", cost, accepted)
	} else {
		out.KPIs[0].UnavailableReason = "EXECUTION_COST"
	}
	if qualityKnown {
		out.KPIs[1] = homeRatio("FIRST_PASS_RATE", firstPass, accepted)
	}
	out.KPIs[4] = homeRatio("EVIDENCE_COVERAGE", evidenced, claimed)
	return nil
}
