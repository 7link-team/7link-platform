package budget

import (
	"errors"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/pkg/module"
)

func (m *Module) bindGatewayReservations(s *module.Services) error {
	c, err := module.Resolve[delivery.AcceptanceCostConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.BindSettlements == nil {
		return ErrUnavailable
	}
	return c.BindSettlements(m.executionSettlementsOn)
}
