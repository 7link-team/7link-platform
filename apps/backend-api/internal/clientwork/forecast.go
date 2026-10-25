package clientwork

import (
	"context"
	"encoding/json"
	"math"
	"math/big"
	"slices"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ForecastMeasurement struct {
	Source                string   `json:"source"`
	LowConfidence         bool     `json:"low_confidence"`
	UnavailableDimensions []string `json:"unavailable_dimensions"`
	CycleSamples          int      `json:"cycle_samples"`
	CostSamples           int      `json:"cost_samples"`
	Remaining             int64    `json:"remaining_work_items"`
	EffectiveParallelism  float64  `json:"effective_parallelism"`
}

func medianForecast(values []int64) *big.Rat {
	if len(values) < 5 {
		return nil
	}
	v := slices.Clone(values)
	slices.Sort(v)
	mid := len(v) / 2
	if len(v)%2 == 1 {
		return new(big.Rat).SetInt64(v[mid])
	}
	sum := new(big.Int).Add(big.NewInt(v[mid-1]), big.NewInt(v[mid]))
	return new(big.Rat).SetFrac(sum, big.NewInt(2))
}
func ceilForecast(v *big.Rat) (int64, bool) {
	if v.Sign() < 0 {
		return 0, false
	}
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(v.Num(), v.Denom(), r)
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q.Int64(), q.IsInt64()
}

// measureForecast uses only observed facts. Template forecast defaults are not
// available in the template capability; sparse dimensions remain explicitly nil.
func measureForecast(at time.Time, work delivery.ForecastWorkMeasurement, runs []execution.ForecastExecution, costs []int64) (*time.Time, *int64, *int64, ForecastMeasurement) {
	meta := ForecastMeasurement{Source: "MEASURED_30D", Remaining: work.Remaining, CostSamples: len(costs), UnavailableDimensions: []string{}}
	cycles := make([]int64, 0, len(work.Samples))
	for _, s := range work.Samples {
		if s.CycleMillis >= 0 {
			cycles = append(cycles, s.CycleMillis)
		}
	}
	meta.CycleSamples = len(cycles)
	active := int64(0)
	for day := 0; day < 7; day++ {
		start := at.Add(time.Duration(day-7) * 24 * time.Hour)
		end := start.Add(24 * time.Hour)
		for _, run := range runs {
			if run.StartedAt == nil || !run.StartedAt.Before(end) || (run.Terminal && run.EndedAt == nil) {
				continue
			}
			if run.EndedAt == nil || run.EndedAt.After(start) {
				active++
			}
		}
	}
	active = max(active, 7)
	meta.EffectiveParallelism = float64(active) / 7
	var end *time.Time
	var cost *int64
	if work.Remaining == 0 {
		v := at
		end = &v
	} else if median := medianForecast(cycles); median != nil {
		span := new(big.Rat).Mul(median, new(big.Rat).SetInt64(work.Remaining))
		span.Mul(span, new(big.Rat).SetFrac64(7, active))
		ms, ok := ceilForecast(span)
		maxMS := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC).UnixMilli() - at.UnixMilli()
		if ok && ms <= maxMS {
			v := time.UnixMilli(at.UnixMilli() + ms).UTC()
			end = &v
		}
	}
	if work.SpentMicros != nil && *work.SpentMicros >= 0 {
		if work.Remaining == 0 {
			v := *work.SpentMicros
			cost = &v
		} else if median := medianForecast(costs); median != nil {
			total := new(big.Rat).Mul(median, new(big.Rat).SetInt64(work.Remaining))
			total.Add(total, new(big.Rat).SetInt64(*work.SpentMicros))
			if n, ok := ceilForecast(total); ok {
				cost = &n
			}
		}
	}
	if end == nil {
		meta.UnavailableDimensions = append(meta.UnavailableDimensions, "TIME")
	}
	if cost == nil {
		meta.UnavailableDimensions = append(meta.UnavailableDimensions, "COST")
	}
	meta.LowConfidence = len(meta.UnavailableDimensions) > 0 || len(cycles) < 5 || len(costs) < 5
	scope := work.ScopeCriteria
	return end, cost, &scope, meta
}

func (m *Module) forecastOneOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, source *model.Tolerance, at time.Time) error {
	var requirement *guid.ID
	if source.SubjectKind == "REQUIREMENT" {
		id := source.SubjectID
		requirement = &id
	}
	// Delivery locks the project before this module locks the tolerance row.
	work, e := m.forecastWork.ReadOn(ctx, tx, tenant, source.ProjectID, requirement, at)
	if e != nil {
		return e
	}
	q := query.BindTolerance(tx, m.names)
	row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(source.ID), q.ProjectID.Eq(source.ProjectID)).First()
	if e != nil {
		return e
	}
	if row.ForecastAt != nil && !row.ForecastAt.Before(at.Add(-time.Minute)) {
		return nil
	}
	runs, e := m.forecastExecutions.ReadOn(ctx, tx, tenant, row.ProjectID, at)
	if e != nil {
		return e
	}
	byWork := map[guid.ID][]guid.ID{}
	for _, run := range runs {
		byWork[run.WorkItemID] = append(byWork[run.WorkItemID], run.ID)
	}
	var costs []int64
	for _, id := range work.AcceptedWorkIDs {
		ids := byWork[id]
		if len(ids) == 0 {
			continue
		}
		cost, known, e := m.forecastCosts.ExecutionsOn(ctx, tx, tenant, row.ProjectID, ids)
		if e != nil {
			return e
		}
		if known {
			costs = append(costs, cost)
		}
	}
	if requirement != nil {
		var spent int64
		known := true
		for _, id := range work.WorkIDs {
			ids := byWork[id]
			if len(ids) == 0 {
				known = false
				continue
			}
			cost, ok, e := m.forecastCosts.ExecutionsOn(ctx, tx, tenant, row.ProjectID, ids)
			if e != nil {
				return e
			}
			if !ok || cost > math.MaxInt64-spent {
				known = false
				continue
			}
			spent += cost
		}
		if known {
			work.SpentMicros = &spent
		}
	}
	end, cost, scope, meta := measureForecast(at, work, runs, costs)
	encoded, e := json.Marshal(meta)
	if e != nil {
		return e
	}
	measurement := string(encoded)
	row.ForecastEndAt = end
	row.ForecastCostMicros = cost
	row.ForecastScopeCriteria = scope
	row.ForecastAt = &at
	row.ForecastMeasurement = &measurement
	row.Version++
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID)).Select(q.ForecastEndAt, q.ForecastCostMicros, q.ForecastScopeCriteria, q.ForecastAt, q.ForecastMeasurement, q.Version).Updates(row); e != nil {
		return e
	}
	emit := func(id guid.ID, kind string, version int) error {
		detail := map[string]any{"source": "MEASURED_FORECAST", "authority_source": "system:tolerance_forecast", "version": version, "measurement": meta}
		if e := m.audit(ctx, tx, audit.DomainEvent{TenantID: tenant, EventType: "clientwork." + kind, TargetType: Name, TargetID: id, Detail: detail}); e != nil {
			return e
		}
		_, e := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: row.ProjectID, Type: "clientwork." + kind, Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "system:tolerance_forecast"}, Subject: events.Subject{Kind: Name, ID: id}, SubjectVersion: int64(version), Data: detail})
		return e
	}
	if e = m.recomputeToleranceWithEventOn(ctx, tx, row, emit); e != nil {
		return e
	}
	return emit(row.ID, "tolerance_forecasted", row.Version)
}
