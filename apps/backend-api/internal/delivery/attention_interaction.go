package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

// AttentionInteraction rechecks the current pending interaction in the caller's
// transaction. The Execution owner retains all execution and approval rules.
type AttentionInteraction func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error
type AttentionInteractionConnector struct {
	Bind func(AttentionInteraction) error
}

func (m *Module) provideAttentionInteraction(s *module.Services) error {
	return module.Provide[AttentionInteractionConnector](s, AttentionInteractionConnector{Bind: func(read AttentionInteraction) error {
		if read == nil || m.attentionInteraction != nil {
			return ErrInvalidArgument
		}
		m.attentionInteraction = read
		return nil
	}})
}
