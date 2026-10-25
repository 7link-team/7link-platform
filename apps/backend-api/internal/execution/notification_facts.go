package execution

import (
	"context"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// NotificationFacts contains persisted routing and state facts, without plan,
// runner secrets, output, or an inferred retry-exhaustion verdict.
type NotificationFacts struct {
	ProjectID, WorkItemID, InitiatorID, MachineID guid.ID
	State, StateReason                            string
	DispatchEpoch                                 int64
}

// NotificationFactsOn reads in a trusted notification consumer's transaction.
// It does not authorize an end-user read; consumers recheck recipient access.
type NotificationFactsOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (NotificationFacts, error)

func (m *Module) provideNotificationFacts(s *module.Services) error {
	return module.Provide[NotificationFactsOn](s, m.notificationFactsOn)
}

func (m *Module) notificationFactsOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (NotificationFacts, error) {
	if m == nil {
		return NotificationFacts{}, ErrUnavailable
	}
	row, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return NotificationFacts{}, err
	}
	return NotificationFacts{ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, InitiatorID: row.InitiatorID, MachineID: row.MachineID, State: row.State, StateReason: row.StateReason, DispatchEpoch: row.DispatchEpoch}, nil
}
