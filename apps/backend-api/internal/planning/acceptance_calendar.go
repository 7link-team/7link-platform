package planning

import (
	"context"
	"errors"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) bindAcceptanceCalendar(s *module.Services) error {
	c, e := module.Resolve[clientwork.AcceptanceCalendarConnector](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(func(ctx context.Context, tx *gorm.DB, tenant guid.ID) (string, int, error) {
		v, e := m.readSettingsOn(ctx, tx, tenant)
		return v.Timezone, v.Version, e
	})
}
