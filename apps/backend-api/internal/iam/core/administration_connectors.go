package core

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	eventsi18n "7link.com/platform/internal/infra/events/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	jobsi18n "7link.com/platform/internal/infra/jobs/i18n"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
)

func (m *Module) bindEventsAdministration(s *module.Services) error {
	c, err := module.Resolve[events.AdminConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil || c.BindAudit == nil || c.BindSecurity == nil {
		return ErrUnavailable
	}
	if err = m.systemMenus.Contribute(SystemMenuPage{Branch: SystemMenuBasicManagement, Menu: Menu{Label: "Domain events", Translations: eventsi18n.Translations("menu_events"), Route: "/system/domain-events", Icon: "radio", SortOrder: 60, PermissionCode: "system_event_view", Status: MenuActive}}); err != nil {
		return err
	}
	if err = c.Bind(m.registrar, func(ctx context.Context, tx *gorm.DB, code string, subject guid.ID) (bool, error) {
		if m == nil || m.service == nil {
			return false, ErrUnavailable
		}
		return m.service.CheckPlatformOn(ctx, tx, code, subject)
	}); err != nil {
		return err
	}
	if err = c.BindAudit(func(ctx context.Context, tx *gorm.DB, action string, tenant guid.ID, resource string, id guid.ID, detail map[string]any) error {
		if m.appender == nil {
			return ErrUnavailable
		}
		e := audit.NewDomainEvent(ctx, tenant, action, resource, id)
		for k, v := range detail {
			e.Detail[k] = v
		}
		return m.appender(ctx, tx, e)
	}); err != nil {
		return err
	}
	p, err := module.Resolve[authn.ReauthenticationConsumer](s)
	if err != nil {
		return err
	}
	i, err := module.Resolve[idempotency.Store](s)
	if err != nil {
		return err
	}
	return c.BindSecurity(p, i)
}

func (m *Module) bindJobsAdministration(s *module.Services) error {
	connector, err := module.Resolve[jobs.AdminConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrUnavailable
	}
	if err = connector.Bind(m.registrar, func(ctx context.Context, tx *gorm.DB, code string, subject guid.ID) (bool, error) {
		if m == nil || m.service == nil {
			return false, ErrUnavailable
		}
		return m.service.CheckPlatformOn(ctx, tx, code, subject)
	}); err != nil {
		return err
	}
	if connector.BindAudit == nil {
		return ErrUnavailable
	}
	if err = m.systemMenus.Contribute(
		SystemMenuPage{Branch: SystemMenuBasicManagement, Menu: Menu{Label: "Background jobs", Translations: jobsi18n.Translations("menu_jobs"), Route: "/system/jobs", Icon: "list-todo", SortOrder: 40, PermissionCode: "system_job_view", Status: MenuActive}},
		SystemMenuPage{Branch: SystemMenuBasicManagement, Menu: Menu{Label: "Scheduled jobs", Translations: jobsi18n.Translations("menu_schedules"), Route: "/system/job-schedules", Icon: "calendar-clock", SortOrder: 50, PermissionCode: "system_job_view", Status: MenuActive}},
	); err != nil {
		return err
	}
	return connector.BindAudit(func(ctx context.Context, tx *gorm.DB, action string, tenant guid.ID, resource string, id guid.ID, detail map[string]any) error {
		if m.appender == nil {
			return ErrUnavailable
		}
		event := audit.NewDomainEvent(ctx, tenant, action, resource, id)
		for key, value := range detail {
			event.Detail[key] = value
		}
		return m.appender(ctx, tx, event)
	})
}
