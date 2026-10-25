package webhook

import (
	"context"
	"errors"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IntakeCallbackSubscriptions validates a callback reference in its caller's
// policy transaction. A subscription never lends its management authority.
type IntakeCallbackSubscriptions interface {
	ValidateOn(context.Context, *gorm.DB, guid.ID, guid.ID) error
}

// IntakeCallbackReceiver keeps optional outbound support out of consumers'
// lifecycle dependencies. Consumers remain available when webhook is disabled.
type IntakeCallbackReceiver struct {
	Bind func(IntakeCallbackSubscriptions) error
}

func (m *Module) bindIntakeCallbacks(s *module.Services) error {
	r, err := module.Resolve[IntakeCallbackReceiver](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.Bind == nil {
		return ErrUnavailable
	}
	return r.Bind(m.service)
}

func (s *Service) ValidateOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
	if s == nil || tx == nil || tx.Statement == nil {
		return ErrUnavailable
	}
	if tenant <= 0 || id <= 0 {
		return ErrInvalidSubscription
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidSubscription
	}
	row, err := s.repo.withDB(tx.Clauses(clause.Locking{Strength: "UPDATE"})).getSubscription(ctx, tenant, id)
	if err != nil {
		return err
	}
	if row.Status != SubscriptionActive || row.PayloadMode != PayloadIntakeMetadata || row.SecretCiphertext == "" {
		return ErrInvalidSubscription
	}
	secret, err := s.openSecret(row)
	if err != nil {
		return err
	}
	if len(secret) == 0 {
		return ErrInvalidSubscription
	}
	return nil
}

var _ IntakeCallbackSubscriptions = (*Service)(nil)
