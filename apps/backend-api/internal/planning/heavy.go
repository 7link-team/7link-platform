package planning

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type HeavyBranch struct {
	Execution        execution.ExecutionCreate `json:"execution"`
	CapMicros        string                    `json:"cap_micros" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"Planned branch ceiling in exact decimal micros; not spent or reserved money."`
	ExecutionID      guid.ID                   `json:"execution_id,omitempty" readOnly:"true"`
	ExecutionVersion int                       `json:"execution_version,omitempty" readOnly:"true"`
	State            string                    `json:"state,omitempty" readOnly:"true"`
}
type HeavyCreate struct {
	ProjectID  guid.ID       `json:"project_id"`
	WorkItemID guid.ID       `json:"work_item_id"`
	Reason     string        `json:"reason" minLength:"10" maxLength:"2000"`
	Branches   []HeavyBranch `json:"branches" minItems:"2" maxItems:"4"`
}
type HeavyView struct {
	model.HeavyRun
	Branches []HeavyBranch `json:"branches"`
}

func heavyView(row *model.HeavyRun) (HeavyView, error) {
	v := HeavyView{HeavyRun: *row}
	if json.Unmarshal([]byte(row.Branches), &v.Branches) != nil {
		return v, ErrUnavailable
	}
	return v, nil
}
func (m *Module) heavyEvent(ctx context.Context, tx *gorm.DB, row *model.HeavyRun, action string) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	ev := audit.NewDomainEvent(ctx, row.TenantID, "planning.heavy_changed", "heavy_run", row.ID)
	ev.Detail["authority_source"] = "IAM"
	ev.Detail["action"] = action
	ev.Detail["state"] = row.State
	ev.Detail["work_item_id"] = row.WorkItemID.String()
	if err := m.audit(ctx, tx, ev); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: "planning.heavy_changed", Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM"}, Subject: events.Subject{Kind: "heavy_run", ID: row.ID}, SubjectVersion: int64(row.Version), Data: map[string]any{"state": row.State, "action": action, "work_item_id": row.WorkItemID}})
	return err
}
func (m *Module) createHeavyOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in HeavyCreate) (HeavyView, error) {
	var empty HeavyView
	if in.ProjectID <= 0 || in.WorkItemID <= 0 || !validText(in.Reason, 2000) || len([]rune(in.Reason)) < 10 || len(in.Branches) < 2 || len(in.Branches) > 4 {
		return empty, ErrInvalid
	}
	if _, err := m.authorize(ctx, tx, tenant, in.ProjectID, authn.PrincipalIDFromContext(ctx), "work_item_assign", true); err != nil {
		return empty, err
	}
	if m.heavyBudget.FactOn == nil || m.heavyReference == nil {
		return empty, ErrUnavailable
	}
	seen := map[guid.ID]bool{}
	in.Branches = slices.Clone(in.Branches)
	var total int64
	for i := range in.Branches {
		b := &in.Branches[i]
		ceiling, err := strconv.ParseInt(b.CapMicros, 10, 64)
		if err != nil || ceiling <= 0 || strconv.FormatInt(ceiling, 10) != b.CapMicros || ceiling > 9223372036854775807-total || b.Execution.ProjectID != in.ProjectID || b.Execution.WorkItemID != in.WorkItemID || b.Execution.AssignmentID <= 0 || seen[b.Execution.AssignmentID] || b.ExecutionID != 0 || b.ExecutionVersion != 0 || b.State != "" {
			return empty, ErrInvalid
		}
		seen[b.Execution.AssignmentID] = true
		if err := m.heavyReference(ctx, tx, tenant, in.ProjectID, in.WorkItemID, b.Execution.AssignmentID); err != nil {
			return empty, err
		}
		total += ceiling
		b.State = "PLANNED"
	}
	fact, err := m.heavyBudget.FactOn(ctx, tx, tenant, in.ProjectID)
	if err != nil {
		return empty, err
	}
	if fact.SpentMicros < 0 || fact.ReservedMicros < 0 {
		return empty, ErrUnavailable
	}
	if fact.CapMicros != nil {
		ceiling := *fact.CapMicros
		if fact.UnknownReserved || fact.SpentMicros > ceiling || fact.ReservedMicros > ceiling-fact.SpentMicros || total > ceiling-fact.SpentMicros-fact.ReservedMicros {
			return empty, budget.ErrExceeded
		}
	}
	id, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	if id <= 0 {
		return empty, ErrUnavailable
	}
	data, err := json.Marshal(in.Branches)
	if err != nil {
		return empty, err
	}
	row := model.HeavyRun{ID: guid.ID(id), TenantID: tenant, ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, State: "DRAFT", Reason: in.Reason, Branches: string(data), CreatedBy: authn.PrincipalIDFromContext(ctx), Version: 1, CreatedAt: m.now()}
	if err = query.BindHeavyRuns(tx, m.names).WithContext(ctx).Create(&row); err != nil {
		return empty, err
	}
	if err = m.heavyEvent(ctx, tx, &row, "CREATED"); err != nil {
		return empty, err
	}
	return heavyView(&row)
}
func (m *Module) heavyOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, permission string) (*model.HeavyRun, error) {
	if _, err := m.authorize(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), permission, true); err != nil {
		return nil, err
	}
	q := query.BindHeavyRuns(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return row, err
}
func (m *Module) changeHeavyOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version int, action string) (HeavyView, error) {
	var empty HeavyView
	permission := "execution_start"
	if action == "CANCEL" {
		permission = "execution_stop"
	} else if action != "LAUNCH" {
		return empty, ErrInvalid
	}
	row, err := m.heavyOn(ctx, tx, tenant, project, id, permission)
	if err != nil {
		return empty, err
	}
	if row.Version != version {
		return empty, ErrConflict
	}
	v, err := heavyView(row)
	if err != nil {
		return empty, err
	}
	switch action {
	case "LAUNCH":
		if row.State != "DRAFT" && row.State != "UNAVAILABLE" {
			return empty, ErrConflict
		}
		if m.heavyCommands.ParallelOn == nil {
			row.State = "UNAVAILABLE"
			break
		}
		inputs := make([]execution.ExecutionCreate, len(v.Branches))
		for i, b := range v.Branches {
			inputs[i] = b.Execution
		}
		launched, err := m.heavyCommands.ParallelOn(ctx, tx, tenant, inputs)
		if err != nil {
			return empty, err
		}
		if len(launched) != len(inputs) {
			return empty, ErrUnavailable
		}
		ids := map[guid.ID]bool{}
		for i, e := range launched {
			if e.ID <= 0 || ids[e.ID] || e.ProjectID != project || e.WorkItemID != row.WorkItemID || e.AssignmentID != inputs[i].AssignmentID || e.Plan.PriceSnapshot == nil {
				return empty, ErrUnavailable
			}
			ids[e.ID] = true
			price := e.Plan.PriceSnapshot
			cost, known, err := fleet.EstimateTokenCost(e.Plan.MaxInputTokens, e.Plan.MaxOutputTokens, &fleet.TokenPrice{InputMicros: price.InputMicros, OutputMicros: price.OutputMicros})
			if err != nil {
				return empty, err
			}
			ceiling, err := strconv.ParseInt(v.Branches[i].CapMicros, 10, 64)
			if err != nil || !known || cost > ceiling {
				return empty, budget.ErrExceeded
			}
			v.Branches[i].ExecutionID = e.ID
			v.Branches[i].ExecutionVersion = e.Version
			v.Branches[i].State = e.State
		}
		row.State = "LAUNCHED"
	case "CANCEL":
		if row.State == "CANCEL_REQUESTED" {
			return v, nil
		}
		if row.State != "DRAFT" && row.State != "UNAVAILABLE" && row.State != "LAUNCHED" {
			return empty, ErrConflict
		}
		for i, b := range v.Branches {
			if b.ExecutionID == 0 {
				continue
			}
			if m.heavyCommands.CancelOn == nil {
				return empty, ErrUnavailable
			}
			e, err := m.heavyCommands.CancelOn(ctx, tx, tenant, b.ExecutionID)
			if err != nil {
				return empty, err
			}
			v.Branches[i].State = e.State
			v.Branches[i].ExecutionVersion = e.Version
		}
		row.State = "CANCEL_REQUESTED" // Cancellation intent is not a runner acknowledgement.
	}
	data, err := json.Marshal(v.Branches)
	if err != nil {
		return empty, err
	}
	row.Branches = string(data)
	row.Version++
	q := query.BindHeavyRuns(tx, m.names)
	info, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Updates(map[string]any{"state": row.State, "branches": row.Branches, "version": row.Version})
	if err != nil {
		return empty, err
	}
	if info.RowsAffected != 1 {
		return empty, ErrConflict
	}
	if err = m.heavyEvent(ctx, tx, row, action); err != nil {
		return empty, err
	}
	return heavyView(row)
}
