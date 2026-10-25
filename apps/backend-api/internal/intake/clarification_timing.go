package intake

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// ClarificationTiming is read from the source's actual channel policy. Missing
// policy stays unavailable; existing rounds never acquire invented deadlines.
type ClarificationTiming struct {
	ChannelID                                                 guid.ID
	ChannelVersion, ReminderHours, TimeoutHours, MaxReminders int
}
type ChannelTimingReader func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (ClarificationTiming, bool, error)
type ChannelTimingConnector struct {
	Bind func(ChannelTimingReader) error
}

func (m *Module) provideChannelTiming(s *module.Services) error {
	return module.Provide[ChannelTimingConnector](s, ChannelTimingConnector{Bind: func(reader ChannelTimingReader) error {
		if reader == nil || m.channelTiming != nil {
			return ErrInvalidArgument
		}
		m.channelTiming = reader
		return nil
	}})
}
func (m *Module) clarificationTimingOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (ClarificationTiming, bool, error) {
	if err := m.changeSourceAuthority(ctx, tx, tenant, project, "scope_change"); err != nil {
		return ClarificationTiming{}, false, err
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if err != nil {
		return ClarificationTiming{}, false, err
	}
	if m.channelTiming == nil {
		return ClarificationTiming{}, false, nil
	}
	return m.channelTiming(ctx, tx, tenant, project, row.ChannelID, row.ChannelKind)
}

// expireClarificationOn is a system-only capability, not a client command. The
// round owner supplies its frozen due time check and commits its audit/outbox
// with this source transition. Never treat a missing client answer as rejection.
func (m *Module) expireClarificationOn(ctx context.Context, tx *gorm.DB, tenant, project, id, round guid.ID, at time.Time) (int, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || id <= 0 || round <= 0 || at.IsZero() {
		return 0, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if err != nil {
		return 0, err
	}
	if row.Status != "CLARIFYING" || row.OpenRoundID == nil || *row.OpenRoundID != round {
		return 0, ErrConflict
	}
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(row.Version)).Updates(map[string]any{"status": "TRIAGED", "open_round_id": nil, "client_unresponsive": true, "version": row.Version + 1, "updated_at": at})
	if err != nil {
		return 0, err
	}
	if result.RowsAffected != 1 {
		return 0, ErrConflict
	}
	return row.Version + 1, nil
}
