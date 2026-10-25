package planning

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/infra/guid"
	"context"
	"gorm.io/gorm"
)

// loopReminderInputOn keeps calendar ownership in planning and delivers each
// occurrence through the existing transactional Agent reminder/inbox mechanism.
func (m *Module) loopReminderInputOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, view LoopContractView, reminder guid.ID) (agents.LoopReminderInput, error) {
	in := agents.LoopReminderInput{AgentID: view.AgentID, ProjectID: view.ProjectID, ThreadID: view.Anchor.ThreadRootMessageID, ReminderID: reminder, Name: view.Name}
	if view.ScheduleKind == "INTERVAL" {
		if view.IntervalMinutes == nil {
			return in, ErrInvalid
		}
		in.IntervalMinutes = *view.IntervalMinutes
		return in, nil
	}
	if view.ScheduleKind != "PERIOD" || view.PeriodKind == nil {
		return in, ErrInvalid
	}
	settings, e := m.readSettingsOn(ctx, tx, tenant)
	if e != nil {
		return in, e
	}
	due, ok := nextScheduledReportAt(*view.PeriodKind, settings, m.now())
	if !ok {
		return in, ErrInvalid
	}
	in.DueAt = &due
	return in, nil
}
