package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type ToleranceBreach struct {
	Dimension   string  `json:"dimension"`
	SubjectKind string  `json:"subject_kind"`
	SubjectID   guid.ID `json:"subject_id"`
}
type ToleranceChangeSnapshot struct {
	Subjects      []ToleranceView   `json:"subjects"`
	Before        []ToleranceView   `json:"before"`
	Breached      []ToleranceBreach `json:"breached"`
	LowConfidence bool              `json:"low_confidence"`
}

func validDelta(d ApprovedChangeDelta) bool {
	return d.ScheduleDeltaDays >= -36500 && d.ScheduleDeltaDays <= 36500
}
func shiftedTime(t time.Time, days int) (time.Time, error) {
	n := t.AddDate(0, 0, days)
	if n.Year() < 1970 || n.Year() > 9999 {
		return time.Time{}, ErrInvalid
	}
	return n, nil
}

// toleranceChangeRowsOn always locks both subjects, in stable ID order. The
// enclosing project lock also serializes initial creation of either subject.
func (m *Module) toleranceChangeRowsOn(ctx context.Context, tx *gorm.DB, p, requirement guid.ID) ([]*model.Tolerance, error) {
	q := query.BindTolerance(tx, m.names)
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p)).Where(q.WithContext(ctx).Where(q.SubjectKind.Eq("PROJECT"), q.SubjectID.Eq(p)).Or(q.WithContext(ctx).Where(q.SubjectKind.Eq("REQUIREMENT"), q.SubjectID.Eq(requirement)))).Order(q.ID).Find()
}
func projectedTolerance(r *model.Tolerance, d ApprovedChangeDelta) (*model.Tolerance, error) {
	after := *r
	end := r.AppetiteEndAt
	if r.ForecastEndAt != nil {
		end = *r.ForecastEndAt
	}
	next, e := shiftedTime(end, d.ScheduleDeltaDays)
	if e != nil {
		return nil, e
	}
	after.ForecastEndAt = &next
	cost := r.ForecastCostMicros
	if cost == nil {
		cost = r.AppetiteCostMicros
	}
	after.ForecastCostMicros, e = addNonnegative(cost, d.CostDeltaMicros)
	if e != nil {
		return nil, e
	}
	scope := r.ForecastScopeCriteria
	if scope == nil {
		scope = r.AppetiteScopeCriteria
	}
	after.ForecastScopeCriteria, e = addNonnegative(scope, d.ScopeDeltaCriteria)
	return &after, e
}
func incrementalBreaches(r *model.Tolerance, d ApprovedChangeDelta) ([]string, error) {
	after, e := projectedTolerance(r, d)
	if e != nil {
		return nil, e
	}
	_, dims := toleranceState(after)
	out := []string{}
	for _, dim := range dims {
		if (dim == "TIME" && d.ScheduleDeltaDays > 0) || (dim == "COST" && d.CostDeltaMicros > 0) || (dim == "SCOPE" && d.ScopeDeltaCriteria > 0) {
			out = append(out, dim)
		}
	}
	return out, nil
}

// WouldBreachOn is read-only. It does not alter forecasts or create exceptions.
func (m *Module) WouldBreachOn(ctx context.Context, tx *gorm.DB, p, requirement guid.ID, d ApprovedChangeDelta) (ToleranceChangeSnapshot, error) {
	out := ToleranceChangeSnapshot{Subjects: []ToleranceView{}, Before: []ToleranceView{}, Breached: []ToleranceBreach{}}
	if !validDelta(d) || p <= 0 || requirement <= 0 {
		return out, ErrInvalid
	}
	if _, e := m.authorizeActorOn(ctx, tx, p, false); e != nil {
		return out, e
	}
	if e := m.projects.LockRequirementReadOn(ctx, tx, authn.TenantIDFromContext(ctx), p, requirement); e != nil {
		return out, e
	}
	rows, e := m.toleranceChangeRowsOn(ctx, tx, p, requirement)
	if e != nil {
		return out, e
	}
	for _, r := range rows {
		if measured := toleranceRecord(r).Measurement; measured != nil {
			out.LowConfidence = out.LowConfidence || measured.LowConfidence
		}
		out.Before = append(out.Before, toleranceRecord(r))
		// An absent measured dimension falls back to its appetite for this
		// calculation. Make that uncertainty explicit rather than inventing facts.
		out.LowConfidence = out.LowConfidence || r.ForecastAt == nil || r.ForecastEndAt == nil || (r.AppetiteCostMicros != nil && r.ForecastCostMicros == nil) || (r.AppetiteScopeCriteria != nil && r.ForecastScopeCriteria == nil)
		projected, e := projectedTolerance(r, d)
		if e != nil {
			return out, e
		}
		projected.State, _ = toleranceState(projected)
		out.Subjects = append(out.Subjects, toleranceRecord(projected))
		dims, e := incrementalBreaches(r, d)
		if e != nil {
			return out, e
		}
		for _, dim := range dims {
			out.Breached = append(out.Breached, ToleranceBreach{dim, r.SubjectKind, r.SubjectID})
		}
	}
	return out, nil
}

// applyApprovedToleranceChangeOn is called exclusively after checking the exact
// client-confirmed frozen proposal. The caller owns replay and the transaction.
func (m *Module) applyApprovedToleranceChangeOn(ctx context.Context, tx *gorm.DB, p, requirement, source guid.ID, d ApprovedChangeDelta, sourceFields ...string) error {
	if !validDelta(d) || source <= 0 {
		return ErrInvalid
	}
	rows, e := m.toleranceChangeRowsOn(ctx, tx, p, requirement)
	if e != nil {
		return e
	}
	// Check every affected subject before any update, including subjects whose
	// positive delta would not currently breach. An exception cannot be bypassed.
	for _, r := range rows {
		if r.OpenExceptionID != nil {
			return ErrOpenException
		}
	}
	for _, r := range rows {
		dims, e := incrementalBreaches(r, d)
		if e != nil {
			return e
		}
		timeDelta, costDelta, scopeDelta := d.ScheduleDeltaDays, d.CostDeltaMicros, d.ScopeDeltaCriteria
		if r.SubjectKind == "PROJECT" {
			timeDelta = 0
			costDelta = 0
			scopeDelta = 0
			for _, dim := range dims {
				switch dim {
				case "TIME":
					timeDelta = d.ScheduleDeltaDays
				case "COST":
					costDelta = d.CostDeltaMicros
				case "SCOPE":
					scopeDelta = d.ScopeDeltaCriteria
				}
			}
		}
		if timeDelta == 0 && costDelta == 0 && scopeDelta == 0 {
			continue
		}
		r.AppetiteEndAt, e = shiftedTime(r.AppetiteEndAt, timeDelta)
		if e != nil {
			return e
		}
		r.AppetiteCostMicros, e = addNonnegative(r.AppetiteCostMicros, costDelta)
		if e != nil {
			return e
		}
		r.AppetiteScopeCriteria, e = addNonnegative(r.AppetiteScopeCriteria, scopeDelta)
		if e != nil {
			return e
		}
		previous := r.BaselineNo
		r.BaselineNo++
		r.Version++
		q := query.BindTolerance(tx, m.names)
		if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(r.ID)).Select(q.AppetiteEndAt, q.AppetiteCostMicros, q.AppetiteScopeCriteria, q.BaselineNo, q.Version).Updates(r); e != nil {
			return e
		}
		if e = m.recomputeToleranceOn(ctx, tx, r); e != nil {
			return e
		}
		event := audit.NewDomainEvent(ctx, r.TenantID, "delivery.tolerance_changed", "tolerance", r.ID)
		event.Detail["source"] = "CLIENT_APPROVED_CHANGE"
		sourceField := "source_change_id"
		if len(sourceFields) > 0 && sourceFields[0] == "source_intake_id" {
			sourceField = sourceFields[0]
		}
		event.Detail[sourceField] = source.String()
		event.Detail["old_baseline_no"] = previous
		event.Detail["new_baseline_no"] = r.BaselineNo
		encoded, e := json.Marshal(d)
		if e != nil {
			return e
		}
		event.Detail["delta"] = json.RawMessage(encoded)
		if e = m.audit(ctx, tx, event); e != nil {
			return e
		}
		if e = m.event(ctx, tx, p, r.ID, "tolerance_changed", "scope_change", r.Version); e != nil {
			return e
		}
	}
	return nil
}
