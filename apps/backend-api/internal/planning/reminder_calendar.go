package planning

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"time"
)

func (m *Module) injectReminderCalendar(s *module.Services) error {
	connector, err := module.Resolve[agents.ReminderCalendarConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrUnavailable
	}
	return connector.Bind(m.reminderCalendarOn)
}
func (m *Module) reminderCalendarOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind string, after time.Time) (time.Time, string, error) {
	settings, err := m.readSettingsOn(ctx, tx, tenant)
	if err != nil {
		return time.Time{}, "", err
	}
	due, ok := nextScheduledReportAt(kind, settings, after)
	if !ok {
		return time.Time{}, "", ErrInvalid
	}
	return due.UTC(), settings.Timezone, nil
}
