package notification

import (
	"context"
	"errors"
	"strconv"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) injectWake(s *module.Services) error {
	connector, err := module.Resolve[agents.WakeNotifications](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrUnavailable
	}
	return connector.Bind(m.wakeFallbackOn)
}
func (m *Module) wakeFallbackOn(ctx context.Context, tx *gorm.DB, f agents.WakeFailure) error {
	if !validTransaction(tx) || f.TenantID <= 0 || f.AgentPrincipalID <= 0 || f.OwnerPrincipalID <= 0 || f.Failures < 20 {
		return ErrInvalid
	}
	_, err := m.Notify(ctx, tx, Notification{TenantID: f.TenantID, Recipients: []guid.ID{f.OwnerPrincipalID}, Type: "agents.wake_failed", Severity: Action, TemplateKey: "agents.wake_failed", Params: map[string]string{"subject_id": f.AgentPrincipalID.String(), "failures": strconv.Itoa(f.Failures)}, LinkPath: "/admin/" + f.TenantID.String() + "/agents?agent=" + f.AgentPrincipalID.String(), DedupeKey: "wake-fallback:" + f.AgentPrincipalID.String()})
	return err
}
