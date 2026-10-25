package execution

import (
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"slices"
)

// Terminal facts derive only from the frozen execution plan, not a requested path.
func (m *Module) bindTerminalExecutions(s *module.Services) error {
	recordings, err := module.Resolve[*files.TerminalRecordings](s)
	if err != nil {
		return err
	}
	if err = recordings.ConfigureRetention(m.config.Terminal.RecordingRetentionDays); err != nil {
		return err
	}
	c, err := module.Resolve[fleet.TerminalExecutionConnector](s)
	if err != nil {
		return err
	}
	return c.Bind(m.terminalExecutionOn)
}
func (m *Module) terminalExecutionOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, permission string) (fleet.TerminalExecution, error) {
	r, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return fleet.TerminalExecution{}, terminalBoundaryError(err)
	}
	actualPermission := permission
	if permission == "terminal_metadata" {
		actualPermission = "terminal_interact"
	}
	if err = m.humanAuthority(ctx, tx, tenant, r.ProjectID, actualPermission); err != nil {
		return fleet.TerminalExecution{}, terminalBoundaryError(err)
	}
	r, err = m.rowOn(ctx, tx, tenant, id, true)
	if err != nil {
		return fleet.TerminalExecution{}, terminalBoundaryError(err)
	}
	if permission == "terminal_interact" && !slices.Contains([]string{"CREATED", "RUNNING", "PAUSED"}, r.State) {
		return fleet.TerminalExecution{}, fleet.ErrTerminalClosed
	}
	var p ExecutionPlan
	if err = json.Unmarshal([]byte(r.PlanJSON), &p); err != nil {
		return fleet.TerminalExecution{}, terminalBoundaryError(err)
	}
	if p.Workdir == "" || p.WorkdirID <= 0 {
		return fleet.TerminalExecution{}, fleet.ErrUnavailable
	}
	return fleet.TerminalExecution{ID: r.ID, ProjectID: r.ProjectID, MachineID: r.MachineID, Workspace: p.Workdir, DispatchEpoch: r.DispatchEpoch}, nil
}

func terminalBoundaryError(err error) error {
	switch {
	case errors.Is(err, ErrForbidden):
		return fleet.ErrMachineForbidden
	case errors.Is(err, ErrNotFound):
		return fleet.ErrMachineNotFound
	case errors.Is(err, ErrConflict), errors.Is(err, ErrFenced):
		return fleet.ErrTerminalClosed
	default:
		return err
	}
}
