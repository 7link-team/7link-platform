package agents

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
)

func (m *Module) injectSourceInbox(s *module.Services) error {
	c, err := module.Resolve[delivery.AgentInboxConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(func(ctx context.Context, tx *gorm.DB, r delivery.AgentInboxReference) error {
		_, err := m.appendOn(ctx, tx, r.TenantID, r.PrincipalID, InboxReference{SourceEventID: r.SourceEventID, Kind: r.Kind, SubjectID: r.SubjectID, StreamKey: r.StreamKey, StreamSeq: r.StreamSeq})
		return err
	})
}
