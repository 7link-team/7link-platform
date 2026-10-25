package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
	"time"
)

// ClarificationReplySource is supplied by the owner of the client reply.
// Version changes only when a new reply is written, not on reminder/expiry.
// lock is true only for a source mutation, after principal/project locks.
// Notification reads must not acquire source locks before recipient locks.
type ClarificationReplySource func(context.Context, *gorm.DB, guid.ID, guid.ID, bool) (ClarificationReplyFact, bool, error)
type ClarificationReplyFact struct {
	ProjectID, RequesterID guid.ID
	ReplyVersion           int
}
type ClarificationHandled func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (bool, error)

type clarificationReceiptView struct {
	ID              guid.ID   `json:"id"`
	ClarificationID guid.ID   `json:"clarification_id"`
	ReplyVersion    int       `json:"reply_version"`
	HandledBy       guid.ID   `json:"handled_by"`
	HandledAt       time.Time `json:"handled_at"`
}

func (m *Module) provideClarificationReceipts(s *module.Services) error {
	return module.Provide[ClarificationHandled](s, m.clarificationHandledOn)
}

func (m *Module) clarificationHandledOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version int) (bool, error) {
	if tx == nil || tenant <= 0 || project <= 0 || id <= 0 || version < 1 {
		return false, ErrInvalidArgument
	}
	q := query.BindClarificationReceipts(tx, m.service.names)
	count, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ClarificationID.Eq(id), q.ReplyVersion.Eq(version)).Count()
	return count > 0, err
}

func (m *Module) handleClarificationOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor guid.ID, version int) (clarificationReceiptView, error) {
	var out clarificationReceiptView
	if id <= 0 || version < 1 {
		return out, ErrInvalidArgument
	}
	_, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "intake_read")
	if err != nil {
		return out, err
	}
	if kind != "HUMAN" {
		return out, ErrWorkForbidden
	}
	if m.clarificationReplySource == nil {
		return out, ErrUnavailable
	}
	fact, found, err := m.clarificationReplySource(ctx, tx, tenant, id, true)
	if err != nil {
		return out, err
	}
	if !found || fact.ProjectID != project {
		return out, ErrProjectNotFound
	}
	if fact.RequesterID != actor {
		return out, ErrWorkForbidden
	}
	if fact.ReplyVersion != version {
		return out, ErrWorkVersion
	}
	q := query.BindClarificationReceipts(tx, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ClarificationID.Eq(id), q.ReplyVersion.Eq(version)).Find()
	if err != nil {
		return out, err
	}
	if len(rows) > 0 {
		r := rows[0]
		return clarificationReceiptView{r.ID, r.ClarificationID, r.ReplyVersion, r.HandledBy, r.HandledAt}, nil
	}
	rawID, err := m.ids.Next()
	if err != nil {
		return out, err
	}
	receiptID := guid.ID(rawID)
	at := time.Now().UTC().Truncate(time.Millisecond)
	row := &model.ClarificationReceipt{ID: receiptID, TenantID: tenant, ProjectID: project, ClarificationID: id, ReplyVersion: version, HandledBy: actor, HandledAt: at}
	if err = q.WithContext(ctx).Create(row); err != nil {
		return out, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.clarification_handled", "clarification", id)
	event.Detail["reply_version"], event.Detail["receipt_id"] = version, receiptID.String()
	if err = m.service.audit(ctx, tx, event); err != nil {
		return out, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "clarification.handled", Subject: events.Subject{Kind: "clarification", ID: id}, SubjectVersion: int64(version), Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: "permission:intake_read"}, Data: map[string]any{"reply_version": version, "receipt_id": receiptID.String()}})
	if err != nil {
		return out, err
	}
	return clarificationReceiptView{receiptID, id, version, actor, at}, nil
}
