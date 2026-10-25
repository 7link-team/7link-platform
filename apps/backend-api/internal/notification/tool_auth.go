package notification

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"time"
)

func (m *Module) injectToolAuthNotifications(s *module.Services) error {
	source, err := module.Resolve[agents.ToolAuthNotifications](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if source.Bind == nil {
		return ErrUnavailable
	}
	return source.Bind(m.toolAuthFailureOn)
}
func (m *Module) toolAuthFailureOn(ctx context.Context, tx *gorm.DB, f agents.ToolAuthFailure) error {
	if !validTransaction(tx) || f.TenantID <= 0 || f.ToolID <= 0 {
		return ErrInvalid
	}
	var after guid.ID
	for {
		ids, err := m.candidates(ctx, tx, after, 100)
		if err != nil {
			return err
		}
		for _, actor := range ids {
			if actor <= after {
				return ErrInvalid
			}
			after = actor
			row := &model.Notification{TenantID: f.TenantID, RecipientPrincipalID: actor, Type: "agents.tool_auth_failed", Permission: "tool_catalog_manage"}
			allowed, err := m.visibleOn(ctx, tx, row, actor)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			_, err = m.Notify(ctx, tx, Notification{TenantID: f.TenantID, Recipients: []guid.ID{actor}, Type: row.Type, Permission: row.Permission, Severity: Action, Channels: []ChannelName{Email}, TemplateKey: "notification.tool_auth_failed", Params: map[string]string{"title": f.Title, "subject_id": f.ToolID.String()}, LinkPath: "/admin/" + f.TenantID.String() + "/tool-catalog", DedupeKey: "tool-auth:" + f.ToolID.String() + ":" + m.now().Format(time.RFC3339Nano)})
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
