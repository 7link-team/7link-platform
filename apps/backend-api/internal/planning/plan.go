package planning

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

func validatePlanLimit(kind string, items []PlanningPlanItem) error {
	limit := 200
	if kind == "DAY" {
		limit = 30
	}
	if len(items) > limit {
		return ErrPlanLimit
	}
	return nil
}

func validPlanAnnotations(item PlanningPlanItem) bool {
	if item.EstimateCostMicros != nil {
		v := *item.EstimateCostMicros
		if v == "" || len(v) > 19 || strings.Trim(v, "0123456789") != "" {
			return false
		}
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			return false
		}
	}
	return utf8.ValidString(item.Notes) && utf8.RuneCountInString(item.Notes) <= 2000 &&
		(item.EstimateHours == nil || (!math.IsNaN(*item.EstimateHours) && !math.IsInf(*item.EstimateHours, 0) && *item.EstimateHours >= 0))
}

// IDs belong to persisted items. Clients retain an existing ID or omit it for
// a new item; they cannot relabel an item with an arbitrary external ID.
func (m *Module) assignPlanItemIDs(items, previous []PlanningPlanItem) error {
	known := map[guid.ID]bool{}
	for _, item := range previous {
		if item.ID > 0 {
			known[item.ID] = true
		}
	}
	seen := map[guid.ID]bool{}
	for i := range items {
		if items[i].ID != 0 {
			if !known[items[i].ID] || seen[items[i].ID] {
				return ErrInvalid
			}
		} else {
			if m.ids == nil {
				return ErrUnavailable
			}
			id, err := m.ids.Next()
			if err != nil {
				return err
			}
			if id <= 0 {
				return ErrUnavailable
			}
			items[i].ID = guid.ID(id)
		}
		seen[items[i].ID] = true
	}
	return nil
}

func planNeedsConfirmation(previous, next []PlanningPlanItem) bool {
	strip := func(items []PlanningPlanItem) []PlanningPlanItem {
		out := append([]PlanningPlanItem(nil), items...)
		for i := range out {
			out[i].Notes, out[i].EstimateHours, out[i].EstimateCostMicros = "", nil, nil
		}
		return out
	}
	before, after := strip(previous), strip(next)
	// Legacy JSON had no item ID. Assigning that missing server identity alone
	// is not a scope change and must not invalidate an existing confirmation.
	for i := range before {
		if i < len(after) && before[i].ID == 0 {
			before[i].ID = after[i].ID
		}
	}
	_, a, _ := hashJSON(before)
	_, b, _ := hashJSON(after)
	return a != b
}

func (m *Module) decidePlanOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in PlanningDecisionInput) (PlanningRecord, error) {
	r, e := m.readOn(ctx, tx, tenant, id, "plan")
	if e != nil {
		return r, e
	}
	permission := "plan_submit"
	if in.Action != "SUBMIT" {
		permission = "plan_confirm"
	}
	kind, e := m.authorize(ctx, tx, tenant, r.ProjectID, r.PrincipalID, permission, in.Action != "SUBMIT")
	if e != nil {
		return PlanningRecord{}, e
	}
	q := query.BindPlans(tx, m.names)
	row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return PlanningRecord{}, ErrNotFound
	}
	if e != nil {
		return PlanningRecord{}, e
	}
	if in.Version != row.Version || in.ExpectedContentHash != row.ContentHash {
		return PlanningRecord{}, ErrConflict
	}
	actor := authn.PrincipalIDFromContext(ctx)
	switch in.Action {
	case "SUBMIT":
		if actor != row.PrincipalID {
			return PlanningRecord{}, ErrForbidden
		}
		if row.State != "DRAFT" && row.State != "RETURNED" {
			return PlanningRecord{}, ErrConflict
		}
		var body PlanningPlanInput
		if e = json.Unmarshal([]byte(row.Content), &body); e != nil {
			return PlanningRecord{}, e
		}
		if e = validatePlanLimit(row.PeriodKind, body.Items); e != nil {
			return PlanningRecord{}, e
		}
		if e = m.validatePlanItemsOn(ctx, tx, tenant, row.ProjectID, row.PrincipalID, body.Items); e != nil {
			return PlanningRecord{}, e
		}
		start, end, err := m.periodForOn(ctx, tx, tenant, row.PeriodKind, row.PeriodStart)
		if err != nil {
			return PlanningRecord{}, err
		}
		if err = validatePlanSchedule(body.Items, start, end); err != nil {
			return PlanningRecord{}, err
		}
		if err := m.validatePlanCarryPeriodOn(ctx, tx, tenant, row.PeriodKind, row.PeriodStart, body.Items); err != nil {
			return PlanningRecord{}, err
		}
		row.State = "SUBMITTED"
	case "CONFIRM", "RETURN":
		if actor == row.PrincipalID {
			return PlanningRecord{}, ErrForbidden
		}
		if row.State != "SUBMITTED" {
			return PlanningRecord{}, ErrConflict
		}
		if in.Action == "RETURN" {
			if !validText(in.Reason, 2000) || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 4 {
				return PlanningRecord{}, ErrInvalid
			}
			row.State = "RETURNED"
		} else {
			row.State = "CONFIRMED"
		}
	default:
		return PlanningRecord{}, ErrInvalid
	}
	updates := map[string]any{"state": row.State, "version": row.Version + 1, "updated_at": m.now()}
	if in.Action == "SUBMIT" {
		updates["submitted_at"] = m.now()
		updates["unconfirmed_at"] = nil
	}
	info, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(in.Version)).Updates(updates)
	if e != nil {
		return PlanningRecord{}, e
	}
	if info.RowsAffected != 1 {
		return PlanningRecord{}, ErrConflict
	}
	r.State = row.State
	r.Unconfirmed = row.State == "SUBMITTED" && row.UnconfirmedAt != nil
	r.Version = row.Version + 1
	return r, m.record(ctx, tx, tenant, r, kind, in.Action, in.Reason)
}

func (m *Module) editPlanOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, hash string, content PlanningPlanInput) (PlanningRecord, error) {
	r, err := m.readOn(ctx, tx, tenant, id, "plan")
	if err != nil {
		return r, err
	}
	kind, err := m.authorize(ctx, tx, tenant, r.ProjectID, r.PrincipalID, "plan_submit", false)
	if err != nil {
		return PlanningRecord{}, err
	}
	if r.PrincipalID != authn.PrincipalIDFromContext(ctx) {
		return PlanningRecord{}, ErrForbidden
	}
	q := query.BindPlans(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return PlanningRecord{}, err
	}
	if row.Version != version || row.ContentHash != hash {
		return PlanningRecord{}, ErrConflict
	}
	var previous PlanningPlanInput
	if err := json.Unmarshal([]byte(row.Content), &previous); err != nil {
		return PlanningRecord{}, err
	}
	// Suggestions are scheduler output, not client assertions. Editing items
	// preserves their independent optional status.
	content.Suggestions = previous.Suggestions
	if err = validatePlanLimit(row.PeriodKind, content.Items); err != nil {
		return PlanningRecord{}, err
	}
	if err = m.assignPlanItemIDs(content.Items, previous.Items); err != nil {
		return PlanningRecord{}, err
	}
	if err = m.validatePlanItemsOn(ctx, tx, tenant, r.ProjectID, r.PrincipalID, content.Items); err != nil {
		return PlanningRecord{}, err
	}
	start, end, err := m.periodForOn(ctx, tx, tenant, row.PeriodKind, row.PeriodStart)
	if err != nil {
		return PlanningRecord{}, err
	}
	if err = validatePlanSchedule(content.Items, start, end); err != nil {
		return PlanningRecord{}, err
	}
	if err := m.validatePlanCarryPeriodOn(ctx, tx, tenant, row.PeriodKind, row.PeriodStart, content.Items); err != nil {
		return PlanningRecord{}, err
	}
	body, nextHash, err := hashJSON(content)
	if err != nil {
		return PlanningRecord{}, err
	}
	state := row.State
	if state == "CONFIRMED" && planNeedsConfirmation(previous.Items, content.Items) {
		state = "SUBMITTED"
	}
	updates := map[string]any{"content": body, "content_hash": nextHash, "state": state, "version": version + 1, "updated_at": m.now()}
	if row.State == "CONFIRMED" && state == "SUBMITTED" {
		updates["submitted_at"] = m.now()
		updates["unconfirmed_at"] = nil
	}
	info, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Updates(updates)
	if err != nil {
		return PlanningRecord{}, err
	}
	if info.RowsAffected != 1 {
		return PlanningRecord{}, ErrConflict
	}
	r.Content = json.RawMessage(body)
	r.ContentHash = nextHash
	r.Version++
	r.State = state
	r.Unconfirmed = state == "SUBMITTED" && row.State != "CONFIRMED" && row.UnconfirmedAt != nil
	return r, m.record(ctx, tx, tenant, r, kind, "EDIT", "")
}
