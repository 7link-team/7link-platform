package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"slices"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrOpenException = errors.New("clientwork: unresolved tolerance exception")

type ToleranceBudget struct {
	AppetiteEndAt         time.Time `json:"appetite_end_at"`
	AppetiteCostMicros    *int64    `json:"appetite_cost_micros" minimum:"0"`
	AppetiteScopeCriteria *int64    `json:"appetite_scope_criteria" minimum:"0"`
	ToleranceTimePct      int       `json:"tolerance_time_pct" minimum:"0" maximum:"100"`
	ToleranceCostPct      int       `json:"tolerance_cost_pct" minimum:"0" maximum:"100"`
	ToleranceScopePct     int       `json:"tolerance_scope_pct" minimum:"0" maximum:"100"`
}
type ToleranceBody struct {
	ToleranceBudget
	SubjectKind string  `json:"subject_kind" enum:"PROJECT,REQUIREMENT"`
	SubjectID   guid.ID `json:"subject_id"`
}
type ToleranceForecast struct {
	EndAt         time.Time `json:"end_at"`
	CostMicros    *int64    `json:"cost_micros" minimum:"0"`
	ScopeCriteria *int64    `json:"scope_criteria" minimum:"0"`
}
type ToleranceView struct {
	Measurement *ForecastMeasurement `json:"forecast_measurement,omitempty"`
	ToleranceBody
	ID              guid.ID           `json:"id"`
	ProjectID       guid.ID           `json:"project_id"`
	Forecast        ToleranceForecast `json:"forecast"`
	ForecastAt      *time.Time        `json:"forecast_at"`
	State           string            `json:"state"`
	OpenExceptionID *guid.ID          `json:"open_exception_id"`
	BaselineNo      int               `json:"baseline_no"`
	Version         int               `json:"version"`
	CreatedAt       time.Time         `json:"created_at"`
}
type ExceptionOption struct {
	Decision  string `json:"decision" enum:"CONTINUE,RESHAPE,HOLD,KILL"`
	Impact    string `json:"impact" minLength:"1" maxLength:"2000"`
	CostDelta int64  `json:"cost_delta"`
	TimeDelta int    `json:"time_delta"`
}
type ExceptionReport struct {
	Situation      string            `json:"situation" minLength:"1" maxLength:"20000"`
	Options        []ExceptionOption `json:"options" minItems:"1" maxItems:"4"`
	Recommendation string            `json:"recommendation" enum:"CONTINUE,RESHAPE,HOLD,KILL"`
}
type ExceptionView struct {
	ID                 guid.ID          `json:"id"`
	ProjectID          guid.ID          `json:"project_id"`
	ToleranceID        guid.ID          `json:"tolerance_id"`
	BreachedDimensions []string         `json:"breached_dimensions"`
	Snapshot           ToleranceView    `json:"forecast_snapshot"`
	Report             *ExceptionReport `json:"report"`
	State              string           `json:"state"`
	Decision           *string          `json:"decision"`
	DecisionPayload    json.RawMessage  `json:"decision_payload"`
	DecidedBy          *guid.ID         `json:"decided_by"`
	DecidedAt          *time.Time       `json:"decided_at"`
	Reason             *string          `json:"decision_reason"`
	Version            int              `json:"version"`
	CreatedAt          time.Time        `json:"created_at"`
}

func toleranceRecord(r *model.Tolerance) ToleranceView {
	f := ToleranceForecast{CostMicros: r.ForecastCostMicros, ScopeCriteria: r.ForecastScopeCriteria}
	if r.ForecastEndAt != nil {
		f.EndAt = *r.ForecastEndAt
	}
	out := ToleranceView{ToleranceBody: ToleranceBody{ToleranceBudget: ToleranceBudget{r.AppetiteEndAt, r.AppetiteCostMicros, r.AppetiteScopeCriteria, r.ToleranceTimePct, r.ToleranceCostPct, r.ToleranceScopePct}, SubjectKind: r.SubjectKind, SubjectID: r.SubjectID}, ID: r.ID, ProjectID: r.ProjectID, Forecast: f, ForecastAt: r.ForecastAt, State: r.State, OpenExceptionID: r.OpenExceptionID, BaselineNo: r.BaselineNo, Version: r.Version, CreatedAt: r.CreatedAt}
	if r.ForecastMeasurement != nil {
		var meta ForecastMeasurement
		if json.Unmarshal([]byte(*r.ForecastMeasurement), &meta) == nil {
			out.Measurement = &meta
		}
	}
	return out
}
func exceptionRecord(r *model.ToleranceException) (ExceptionView, error) {
	v := ExceptionView{ID: r.ID, ProjectID: r.ProjectID, ToleranceID: r.ToleranceID, State: r.State, Decision: r.Decision, DecidedBy: r.DecidedBy, DecidedAt: r.DecidedAt, Reason: r.DecisionReason, Version: r.Version, CreatedAt: r.CreatedAt}
	if e := json.Unmarshal([]byte(r.BreachedDimensions), &v.BreachedDimensions); e != nil {
		return v, e
	}
	if e := json.Unmarshal([]byte(r.ForecastSnapshot), &v.Snapshot); e != nil {
		return v, e
	}
	if r.Report != nil {
		if e := json.Unmarshal([]byte(*r.Report), &v.Report); e != nil {
			return v, e
		}
	}
	if r.DecisionPayload != nil {
		v.DecisionPayload = json.RawMessage(*r.DecisionPayload)
	}
	return v, nil
}
func validToleranceBudget(in ToleranceBudget) bool {
	return !in.AppetiteEndAt.IsZero() && in.AppetiteEndAt.Year() >= 1970 && in.AppetiteEndAt.Year() <= 9999 && (in.AppetiteCostMicros == nil || *in.AppetiteCostMicros >= 0) && (in.AppetiteScopeCriteria == nil || *in.AppetiteScopeCriteria >= 0) && in.ToleranceTimePct >= 0 && in.ToleranceTimePct <= 100 && in.ToleranceCostPct >= 0 && in.ToleranceCostPct <= 100 && in.ToleranceScopePct >= 0 && in.ToleranceScopePct <= 100
}
func (m *Module) toleranceOn(ctx context.Context, tx *gorm.DB, p, id guid.ID) (*model.Tolerance, error) {
	q := query.BindTolerance(tx, m.names)
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id)).First()
}
func (m *Module) exceptionOn(ctx context.Context, tx *gorm.DB, p, id guid.ID) (*model.ToleranceException, error) {
	q := query.BindToleranceException(tx, m.names)
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(p), q.ID.Eq(id)).First()
}
func (m *Module) putToleranceOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, expected int, in ToleranceBody) (Receipt, error) {
	if _, e := m.authorize(ctx, tx, p, "project_manage", false); e != nil {
		return Receipt{}, e
	}
	if !validToleranceBudget(in.ToleranceBudget) || in.SubjectID <= 0 || !slices.Contains([]string{"PROJECT", "REQUIREMENT"}, in.SubjectKind) || (in.SubjectKind == "PROJECT" && in.SubjectID != p) {
		return Receipt{}, ErrInvalid
	}
	if in.SubjectKind == "REQUIREMENT" {
		if _, e := m.projects.RequirementsOn(ctx, tx, authn.TenantIDFromContext(ctx), p, []guid.ID{in.SubjectID}); e != nil {
			return Receipt{}, e
		}
	}
	q := query.BindTolerance(tx, m.names)
	var r *model.Tolerance
	var e error
	if id == 0 {
		exists, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.SubjectKind.Eq(in.SubjectKind), q.SubjectID.Eq(in.SubjectID)).Count()
		if e != nil {
			return Receipt{}, e
		}
		if exists != 0 {
			return Receipt{}, ErrConflict
		}
		id, e = m.nextID()
		if e != nil {
			return Receipt{}, e
		}
		r = &model.Tolerance{TenantID: authn.TenantIDFromContext(ctx), ID: id, ProjectID: p, SubjectKind: in.SubjectKind, SubjectID: in.SubjectID, State: "OK", BaselineNo: 1, Version: 1, CreatedAt: now()}
	} else {
		r, e = m.toleranceOn(ctx, tx, p, id)
		if e != nil {
			return Receipt{}, e
		}
		if r.Version != expected || r.SubjectKind != in.SubjectKind || r.SubjectID != in.SubjectID {
			return Receipt{}, ErrConflict
		}
		if r.OpenExceptionID != nil {
			return Receipt{}, ErrOpenException
		}
		r.Version++
		r.BaselineNo++
	}
	r.AppetiteEndAt = in.AppetiteEndAt.UTC().Truncate(time.Millisecond)
	r.AppetiteCostMicros = in.AppetiteCostMicros
	r.AppetiteScopeCriteria = in.AppetiteScopeCriteria
	r.ToleranceTimePct = in.ToleranceTimePct
	r.ToleranceCostPct = in.ToleranceCostPct
	r.ToleranceScopePct = in.ToleranceScopePct
	if expected == 0 {
		e = q.WithContext(ctx).Create(r)
	} else {
		_, e = q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(id)).Select(q.AppetiteEndAt, q.AppetiteCostMicros, q.AppetiteScopeCriteria, q.ToleranceTimePct, q.ToleranceCostPct, q.ToleranceScopePct, q.Version, q.BaselineNo).Updates(r)
	}
	if e != nil {
		return Receipt{}, e
	}
	if e = m.recomputeToleranceOn(ctx, tx, r); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "tolerance_changed", "project_manage", r.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: r.Version, State: r.State}, nil
}

// Compare products as integers: large micro-costs must not overflow or round.
func exceeds(value, base int64, pct int) bool {
	l := new(big.Int).Mul(big.NewInt(value), big.NewInt(100))
	r := new(big.Int).Mul(big.NewInt(base), big.NewInt(int64(100+pct)))
	return l.Cmp(r) > 0
}
func toleranceState(r *model.Tolerance) (string, []string) {
	dims := []string{}
	risk := false
	// Time budget is the duration from the immutable commitment creation time.
	if r.ForecastEndAt != nil {
		base := r.AppetiteEndAt.UnixMilli() - r.CreatedAt.UnixMilli()
		if base < 0 {
			base = 0
		}
		value := r.ForecastEndAt.UnixMilli() - r.CreatedAt.UnixMilli()
		if exceeds(value, base, r.ToleranceTimePct) {
			dims = append(dims, "TIME")
		}
		risk = risk || r.ForecastEndAt.After(r.AppetiteEndAt)
	}
	for _, v := range []struct {
		name               string
		forecast, appetite *int64
		pct                int
	}{{"COST", r.ForecastCostMicros, r.AppetiteCostMicros, r.ToleranceCostPct}, {"SCOPE", r.ForecastScopeCriteria, r.AppetiteScopeCriteria, r.ToleranceScopePct}} {
		if v.forecast != nil && v.appetite != nil {
			if exceeds(*v.forecast, *v.appetite, v.pct) {
				dims = append(dims, v.name)
			}
			risk = risk || *v.forecast > *v.appetite
		}
	}
	if len(dims) > 0 {
		return "EXCEPTION", dims
	}
	if risk {
		return "AT_RISK", dims
	}
	return "OK", dims
}
func (m *Module) recomputeToleranceOn(ctx context.Context, tx *gorm.DB, r *model.Tolerance) error {
	return m.recomputeToleranceWithEventOn(ctx, tx, r, func(id guid.ID, kind string, version int) error {
		return m.event(ctx, tx, r.ProjectID, id, kind, "project_manage", version)
	})
}
func (m *Module) recomputeToleranceWithEventOn(ctx context.Context, tx *gorm.DB, r *model.Tolerance, emit func(guid.ID, string, int) error) error {
	state, dims := toleranceState(r)
	if r.OpenExceptionID != nil {
		state = "EXCEPTION"
	}
	if state == "EXCEPTION" && r.OpenExceptionID == nil {
		id, e := m.nextID()
		if e != nil {
			return e
		}
		snapshot, e := json.Marshal(toleranceRecord(r))
		if e != nil {
			return e
		}
		dimensions, e := json.Marshal(dims)
		if e != nil {
			return e
		}
		ex := &model.ToleranceException{TenantID: r.TenantID, ID: id, ProjectID: r.ProjectID, ToleranceID: r.ID, BreachedDimensions: string(dimensions), ForecastSnapshot: string(snapshot), State: "RAISED", Version: 1, CreatedAt: now()}
		if e = query.BindToleranceException(tx, m.names).WithContext(ctx).Create(ex); e != nil {
			return e
		}
		r.OpenExceptionID = &id
		if e = emit(id, "exception_raised", 1); e != nil {
			return e
		}
	}
	r.State = state
	q := query.BindTolerance(tx, m.names)
	_, e := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(r.ID)).Select(q.State, q.OpenExceptionID).Updates(r)
	return e
}
func (m *Module) forecastToleranceOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, in ToleranceForecast) (Receipt, error) {
	if _, e := m.authorize(ctx, tx, p, "project_manage", false); e != nil {
		return Receipt{}, e
	}
	if in.EndAt.IsZero() || in.EndAt.Year() < 1970 || in.EndAt.Year() > 9999 || (in.CostMicros != nil && *in.CostMicros < 0) || (in.ScopeCriteria != nil && *in.ScopeCriteria < 0) {
		return Receipt{}, ErrInvalid
	}
	r, e := m.toleranceOn(ctx, tx, p, id)
	if e != nil {
		return Receipt{}, e
	}
	if r.Version != v {
		return Receipt{}, ErrConflict
	}
	at := now()
	end := in.EndAt.UTC().Truncate(time.Millisecond)
	r.ForecastEndAt = &end
	r.ForecastCostMicros = in.CostMicros
	r.ForecastScopeCriteria = in.ScopeCriteria
	r.ForecastAt = &at
	r.ForecastMeasurement = nil
	r.Version++
	q := query.BindTolerance(tx, m.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(id)).Select(q.ForecastEndAt, q.ForecastCostMicros, q.ForecastScopeCriteria, q.ForecastAt, q.ForecastMeasurement, q.Version).Updates(r); e != nil {
		return Receipt{}, e
	}
	if e = m.recomputeToleranceOn(ctx, tx, r); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "tolerance_forecasted", "project_manage", r.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: r.Version, State: r.State}, nil
}
func (m *Module) reportExceptionOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, in ExceptionReport) (Receipt, error) {
	if _, e := m.authorize(ctx, tx, p, "project_manage", false); e != nil {
		return Receipt{}, e
	}
	if !validText(in.Situation, 20000) || len(in.Options) < 1 || len(in.Options) > 4 {
		return Receipt{}, ErrInvalid
	}
	found := false
	seen := map[string]bool{}
	for _, o := range in.Options {
		if !validText(o.Impact, 2000) || !slices.Contains([]string{"CONTINUE", "RESHAPE", "HOLD", "KILL"}, o.Decision) || seen[o.Decision] {
			return Receipt{}, ErrInvalid
		}
		seen[o.Decision] = true
		found = found || o.Decision == in.Recommendation
	}
	if !found {
		return Receipt{}, ErrInvalid
	}
	r, e := m.exceptionOn(ctx, tx, p, id)
	if e != nil {
		return Receipt{}, e
	}
	if r.State != "RAISED" || r.Version != v {
		return Receipt{}, ErrConflict
	}
	data, e := json.Marshal(in)
	if e != nil {
		return Receipt{}, e
	}
	s := string(data)
	r.Report = &s
	r.State = "REPORTED"
	r.Version++
	q := query.BindToleranceException(tx, m.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(id)).Select(q.Report, q.State, q.Version).Updates(r); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "exception_reported", "project_manage", r.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: r.Version, State: r.State}, nil
}

type ApprovedChangeDelta struct {
	ScheduleDeltaDays  int   `json:"schedule_delta_days" minimum:"-36500" maximum:"36500"`
	CostDeltaMicros    int64 `json:"cost_delta_micros"`
	ScopeDeltaCriteria int64 `json:"scope_delta_criteria"`
}

func addNonnegative(base *int64, delta int64) (*int64, error) {
	if base == nil {
		return nil, nil
	}
	if (delta > 0 && *base > math.MaxInt64-delta) || delta < -*base {
		return nil, ErrInvalid
	}
	v := *base + delta
	return &v, nil
}
