package intake

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

// DispositionRoundConnector lets the round owner close exactly the source's
// current round within the disposition transaction, without cross-table writes.
type DispositionRoundCloser func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, string, string) error
type DispositionRoundConnector struct {
	Bind             func(DispositionRoundCloser) error
	BindNotification func(DispositionNotification) error
}

// DispositionNotification returns only an actual transport result. The callback
// runs in the disposition transaction; a linked transport failure rolls it back.
type DispositionDelivery struct {
	State     string   `json:"state" enum:"NOT_REQUESTED,UNAVAILABLE,POSTED,READ"`
	MessageID *guid.ID `json:"message_id,omitempty"`
}

type DispositionNotification func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, *int64) (DispositionDelivery, error)

func (m *Module) provideDispositionRound(s *module.Services) error {
	return module.Provide[DispositionRoundConnector](s, DispositionRoundConnector{Bind: func(closer DispositionRoundCloser) error {
		if closer == nil || m.dispositionRoundCloser != nil {
			return ErrInvalidArgument
		}
		m.dispositionRoundCloser = closer
		return nil
	}, BindNotification: func(send DispositionNotification) error {
		if send == nil || m.dispositionNotification != nil {
			return ErrInvalidArgument
		}
		m.dispositionNotification = send
		return nil
	}})
}
