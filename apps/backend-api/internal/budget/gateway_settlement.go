package budget

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"log/slog"
)

// Actual upstream usage uses the same durable reservation as admission. Unknown
// usage never releases a reservation merely because its HTTP connection ended.
func (m *Module) bindGatewaySettlement(s *module.Services) error {
	c, err := module.Resolve[fleet.GatewaySettlementConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		slog.WarnContext(context.Background(), "gateway actual-usage connector unavailable; provider proxy remains disabled")
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(func(ctx context.Context, tx *gorm.DB, tenant, project, request guid.ID, actual int64) error {
		return m.SettleOn(ctx, tx, tenant, project, "GATEWAY", request, actual, true)
	})
}
