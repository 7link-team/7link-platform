package execution

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) provideExecutionHolds(s *module.Services) error {
	c, err := module.Resolve[delivery.ExecutionHoldConnector](s)
	if err != nil {
		if errors.Is(err, module.ErrServiceMissing) {
			return nil
		}
		return err
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(delivery.ExecutionHolds{PrincipalsOn: m.holdPrincipalsOn, ChangeOn: m.changeHoldOn})
}

func (m *Module) holdExecutionsOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, lock bool) ([]*model.Execution, error) {
	if transaction(tx, tenant, project) != nil || work < 0 {
		return nil, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.In("CREATED", "RUNNING", "PAUSED"))
	if work > 0 {
		base = base.Where(q.WorkItemID.Eq(work))
	}
	if lock {
		base = base.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	return base.Order(q.ID).Find()
}

func (m *Module) holdPrincipalsOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) ([]guid.ID, error) {
	rows, err := m.holdExecutionsOn(ctx, tx, tenant, project, work, false)
	if err != nil {
		return nil, err
	}
	ids := []guid.ID{}
	for _, row := range rows {
		machine, err := m.machines.ByIDOn(ctx, tx, tenant, row.MachineID)
		if err != nil {
			return nil, err
		}
		ids = append(ids, machine.PrincipalID)
		if row.InitiatorID > 0 {
			ids = append(ids, row.InitiatorID)
		}
		if m.work.PrincipalsOn != nil {
			extra, err := m.work.PrincipalsOn(ctx, tx, link(row, row.InitiatorID))
			if err != nil {
				return nil, err
			}
			ids = append(ids, extra...)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// The trusted delivery command holds current principal/project/work locks and
// has evaluated all overlapping holds. No new runtime state is asserted here.
func (m *Module) changeHoldOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, release bool) ([]delivery.ExecutionHoldFact, error) {
	rows, err := m.holdExecutionsOn(ctx, tx, tenant, project, work, true)
	if err != nil {
		return nil, err
	}
	q := query.BindExecutions(tx, m.names)
	out := []delivery.ExecutionHoldFact{}
	for _, row := range rows {
		control := "NOT_RUNNING"
		reason := row.StateReason
		if row.CancelRequestedAt != nil {
			continue
		}
		if !release && row.State == "RUNNING" {
			reason, control = "PAUSE_REQUESTED", "PAUSE_REQUESTED"
		}
		if !release && row.State == "PAUSED" {
			control = "PAUSED"
			if row.StateReason == "RESUME_REQUESTED" {
				reason = "HOLD_ACKNOWLEDGED"
			}
		}
		if release && row.State == "PAUSED" && row.StateReason == "HOLD_ACKNOWLEDGED" {
			reason, control = "RESUME_REQUESTED", "RESUME_REQUESTED"
		}
		// Release racing with an in-flight pause waits for PAUSED then requests
		// resume on the next hold disposition; it never invents a running ACK.
		if release && row.State == "RUNNING" && row.StateReason == "PAUSE_REQUESTED" {
			return nil, ErrConflict
		}
		var resumedProgress *string
		if release && reason == "RESUME_REQUESTED" && row.ProgressJSON != nil {
			var p ProgressState
			if json.Unmarshal([]byte(*row.ProgressJSON), &p) != nil {
				return nil, ErrUnavailable
			}
			if p.StopReason != "" {
				p.LastStopReason = p.StopReason
				p.StopReason = ""
				p.NoProgressCount = 0
				p.DependencyMissCount = 0
				p.HumanResumeCount++
				raw, e := json.Marshal(p)
				if e != nil {
					return nil, e
				}
				v := string(raw)
				resumedProgress = &v
			}
		}
		if reason != row.StateReason {
			now := time.Now().UTC().Truncate(time.Millisecond)
			updates := map[string]any{"state_reason": reason, "version": row.Version + 1, "updated_at": now}
			if resumedProgress != nil {
				updates["progress_json"] = *resumedProgress
			}
			result, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(row.Version)).Updates(updates)
			if e != nil {
				return nil, e
			}
			if result.RowsAffected != 1 {
				return nil, ErrConflict
			}
			row.StateReason, row.Version = reason, row.Version+1
			if e = m.eventOn(ctx, tx, row, "execution.state_changed", "SYSTEM", "delivery:authorized_hold", map[string]any{"control": control, "runner_acknowledged": false}); e != nil {
				return nil, e
			}
		}
		out = append(out, delivery.ExecutionHoldFact{ExecutionID: row.ID, State: row.State, Control: control})
	}
	return out, nil
}
