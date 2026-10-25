package yunxiao

import (
	"context"
	"errors"

	"7link.com/platform/internal/iam/webhook"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) provideIntakeCallbacks(s *module.Services) error {
	return module.Provide[webhook.IntakeCallbackReceiver](s, webhook.IntakeCallbackReceiver{Bind: func(provider webhook.IntakeCallbackSubscriptions) error {
		if provider == nil || m.intakeCallbacks != nil {
			return ErrInvalid
		}
		m.intakeCallbacks = provider
		return nil
	}})
}

func (m *Module) validateIntakeCallbackOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind string, id *guid.ID) error {
	if id == nil {
		return nil
	}
	if kind != "API" || *id <= 0 {
		return ErrInvalid
	}
	if m.intakeCallbacks == nil {
		return ErrUnavailable
	}
	if err := m.intakeCallbacks.ValidateOn(ctx, tx, tenant, *id); err != nil {
		if errors.Is(err, webhook.ErrUnavailable) {
			return ErrUnavailable
		}
		if errors.Is(err, webhook.ErrInvalidSubscription) || errors.Is(err, webhook.ErrSubscriptionNotFound) {
			return ErrInvalid
		}
		return err
	}
	return nil
}
