package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"gorm.io/gorm"
	"time"
)

// ClarificationReminderFact contains routing metadata only. Questions, source
// bytes and internal drafts never enter the notification transport.
type ClarificationReminderFact struct {
	ProjectID, RecipientID guid.ID
	Ordinal                int
	DueAt                  time.Time
}
type ClarificationReminderFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) (ClarificationReminderFact, bool, error)

func (m *Module) clarificationReminderFactsOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (ClarificationReminderFact, bool, error) {
	var out ClarificationReminderFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalid
	}
	q := query.BindClarificationRound(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if row.State != "OPEN" || row.DueAt == nil || !now().Before(*row.DueAt) || row.RemindersSent < 1 {
		return out, false, nil
	}
	if m.clients == nil {
		return out, false, ErrUnavailable
	}
	active, err := m.clients.IsClientOn(ctx, tx, tenant, row.ProjectID, row.RecipientID)
	if err != nil || !active {
		return out, false, err
	}
	return ClarificationReminderFact{ProjectID: row.ProjectID, RecipientID: row.RecipientID, Ordinal: row.RemindersSent, DueAt: *row.DueAt}, true, nil
}
