package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
)

func (m *Module) bindMachineHistory(s *module.Services) error {
	connector, err := module.Resolve[fleet.MachineExecutionHistoryConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return connector.Bind(m.machineHistoryOn)
}

// Scan pages are bounded even when the actor has no access to most projects.
// A continuation may contain no visible items; it never discloses their data.
func (m *Module) machineHistoryOn(ctx context.Context, tx *gorm.DB, tenant, machine, after guid.ID, limit int) (fleet.MachineExecutionHistoryPage, error) {
	out := fleet.MachineExecutionHistoryPage{Items: []fleet.MachineExecutionHistoryEntry{}}
	if transaction(tx, tenant, machine) != nil || after < 0 || limit < 1 || limit > 100 {
		return out, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Gt(after)).Order(q.ID).Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	if len(rows) > limit {
		out.HasMore = true
		rows = rows[:limit]
		out.NextCursor = rows[len(rows)-1].ID.String()
	}
	authorized := map[guid.ID]bool{}
	for _, row := range rows {
		allowed, seen := authorized[row.ProjectID]
		if !seen {
			e := m.humanAuthority(ctx, tx, tenant, row.ProjectID, "execution_read")
			if e != nil && !errors.Is(e, ErrForbidden) {
				return out, e
			}
			allowed = e == nil
			authorized[row.ProjectID] = allowed
		}
		if !allowed {
			continue
		}
		v, e := view(row)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, fleet.MachineExecutionHistoryEntry{ID: row.ID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, State: row.State, ResultState: row.ResultState, Runtime: v.Plan.Runtime, Model: v.Plan.Model, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}
	return out, nil
}
