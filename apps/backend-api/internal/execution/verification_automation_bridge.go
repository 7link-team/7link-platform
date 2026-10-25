package execution

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/delivery"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// verificationAutomationAdapter consumes committed facts, outside runner locks.
// Delivery establishes current authority and its domain lock order itself.
type verificationAutomationAdapter struct{ enabled bool }

func (*verificationAutomationAdapter) ModuleName() string { return "verification_automation_bridge" }
func (m *verificationAutomationAdapter) Enabled() bool    { return m.enabled }
func (*verificationAutomationAdapter) ModuleDependencies() []string {
	return []string{delivery.Name, Name, events.Name}
}
func (*verificationAutomationAdapter) InjectServices(s *module.Services) error {
	facts, err := module.Resolve[VerificationSourceFacts](s)
	if err != nil {
		return err
	}
	automation, err := module.Resolve[delivery.VerificationAutomation](s)
	if err != nil {
		return err
	}
	catalog, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	if facts.ReadOn == nil || automation.QueueOn == nil || automation.CollectOn == nil {
		return delivery.ErrUnavailable
	}
	return catalog.AddSubscribers(delivery.Name, events.Subscriber{
		Name:       "delivery.verification_automation",
		EventTypes: []string{"execution.state_changed", "execution.verification_completed", "execution.merged"},
		Mode:       events.Transactional, StartFrom: events.Earliest,
		Handle: func(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
			if e.Subject.Kind != "execution" || e.TenantID <= 0 || e.ProjectID <= 0 || e.Subject.ID <= 0 {
				return ErrInvalidArgument
			}
			switch e.EventType {
			case "execution.state_changed":
				var data struct {
					State string `json:"state"`
				}
				if err := json.Unmarshal(e.Data, &data); err != nil {
					return err
				}
				if data.State != "SUCCEEDED" {
					return nil
				}
			case "execution.verification_completed", "execution.merged":
			default:
				return nil
			}
			fact, err := facts.ReadOn(ctx, tx, e.TenantID, e.ProjectID, e.Subject.ID)
			if err != nil {
				return err
			}
			if fact.State != "SUCCEEDED" || fact.WorkItemID <= 0 {
				return ErrConflict
			}
			if e.EventType == "execution.state_changed" {
				return automation.QueueOn(ctx, tx, e.TenantID, e.ProjectID, fact.WorkItemID, e.Subject.ID)
			}
			return automation.CollectOn(ctx, tx, e.TenantID, e.ProjectID, fact.WorkItemID, e.Subject.ID)
		},
	})
}

// NewVerificationAutomationBridge registers the existing capability adapter.
func NewVerificationAutomationBridge(enabled bool) module.Module {
	return &verificationAutomationAdapter{enabled: enabled}
}

var _ module.Enabler = (*verificationAutomationAdapter)(nil)

var _ module.Depender = (*verificationAutomationAdapter)(nil)

var _ module.ServiceInjector = (*verificationAutomationAdapter)(nil)
