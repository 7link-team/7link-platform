package collab

import (
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

// ThreadAnchor additionally verifies that a root belongs to the named channel.
type ThreadAnchor struct {
	CheckOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) error
}

func (m *Module) provideLoopAnchor(s *module.Services) error {
	return module.Provide[ThreadAnchor](s, ThreadAnchor{CheckOn: m.loopAnchorOn})
}
func (m *Module) loopAnchorOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, thread, target guid.ID) error {
	if channel <= 0 {
		return ErrInvalidArgument
	}
	if _, e := m.threadAccessOn(ctx, tx, tenant, project, thread, target); e != nil {
		return e
	}
	q := query.BindMessages(tx, m.names)
	row, e := q.WithContext(ctx).Select(q.ChannelID).Where(q.TenantID.Eq(tenant), q.ID.Eq(thread)).First()
	if e != nil {
		return e
	}
	if row.ChannelID != channel {
		return ErrNotFound
	}
	return nil
}
