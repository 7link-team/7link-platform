package delivery

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WorkActivityInput struct {
	Kind, ActorKind, RefKind, Title string
	ActorID, RefID                  guid.ID
}

// Cross-module callers retain their own authorization and call in the same
// transaction as the source mutation. Tenant and work scope are explicit.
type WorkItemActivityRecorder struct {
	RecordOn func(context.Context, *gorm.DB, guid.ID, guid.ID, WorkActivityInput) (int64, error)
}
type WorkActivityActor struct {
	Kind string  `json:"kind"`
	ID   guid.ID `json:"id"`
}
type WorkActivityRef struct {
	Kind string  `json:"kind"`
	ID   guid.ID `json:"id"`
}
type WorkActivityView struct {
	Seq        int64             `json:"seq"`
	Kind       string            `json:"kind"`
	Actor      WorkActivityActor `json:"actor"`
	Ref        WorkActivityRef   `json:"ref"`
	OccurredAt time.Time         `json:"occurred_at"`
	Title      string            `json:"title"`
}

func workActivityView(row model.WorkItemActivity) WorkActivityView {
	return WorkActivityView{Seq: row.Seq, Kind: row.Kind, Actor: WorkActivityActor{Kind: row.ActorKind, ID: row.ActorID}, Ref: WorkActivityRef{Kind: row.RefKind, ID: row.RefID}, OccurredAt: row.OccurredAt, Title: row.Title}
}

type WorkItemActivityReader struct {
	CheckOn func(context.Context, *gorm.DB, guid.ID, guid.ID, *int64, bool) error
	HeadOn  func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error)
	Since   func(context.Context, *gorm.DB, guid.ID, guid.ID, int64, int) ([]WorkActivityView, error)
}

func (m *Module) provideWorkActivity(s *module.Services) error {
	if err := module.Provide[WorkItemActivityRecorder](s, WorkItemActivityRecorder{RecordOn: m.recordWorkActivityOn}); err != nil {
		return err
	}
	return module.Provide[WorkItemActivityReader](s, WorkItemActivityReader{HeadOn: m.workActivityHeadOn, Since: m.workActivitySinceOn, CheckOn: m.checkWorkContextOn})
}
func (m *Module) workActivityRowOn(ctx context.Context, tx *gorm.DB, tenant, work guid.ID) (*model.WorkItem, error) {
	if !isWorkActivityTransaction(tx) || tenant <= 0 || work <= 0 {
		return nil, ErrInvalidArgument
	}
	if m == nil || m.service == nil {
		return nil, ErrUnavailable
	}
	w := query.BindWorkItems(tx, m.service.names)
	return w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ID.Eq(work), w.ArchivedAt.IsNull()).First()
}
func isWorkActivityTransaction(tx *gorm.DB) bool {
	if tx == nil || tx.Statement == nil {
		return false
	}
	_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
	return ok
}
func (m *Module) recordWorkActivityOn(ctx context.Context, tx *gorm.DB, tenant, work guid.ID, in WorkActivityInput) (int64, error) {
	if !slices.Contains([]string{"STATE_CHANGED", "ASSIGNED", "ASSIGNMENT_RELEASED", "CLAIM_SUBMITTED", "DECISION_MADE", "PLAN_DECIDED", "ANDON", "EVIDENCE_ADDED", "COMMENT_ADDED", "EXECUTION_ENDED", "TRANSITIONED"}, in.Kind) || !slices.Contains([]string{"HUMAN", "AGENT", "SYSTEM"}, in.ActorKind) || in.ActorID < 0 || (in.ActorKind != "SYSTEM" && in.ActorID == 0) || in.RefID <= 0 || strings.TrimSpace(in.RefKind) == "" || len(in.RefKind) > 32 || utf8.RuneCountInString(in.Title) > 200 {
		return 0, ErrInvalidArgument
	}
	row, err := m.workActivityRowOn(ctx, tx, tenant, work)
	if err != nil {
		return 0, err
	}
	if m.ids == nil {
		return 0, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, ErrUnavailable
	}
	seq := row.ActivitySeq + 1
	receipt := model.WorkItemActivity{TenantID: tenant, ID: guid.ID(id), ProjectID: row.ProjectID, WorkItemID: work, Seq: seq, Kind: in.Kind, ActorKind: in.ActorKind, ActorID: in.ActorID, RefKind: in.RefKind, RefID: in.RefID, Title: in.Title, OccurredAt: time.Now().UTC().Truncate(time.Millisecond)}
	if err = query.BindWorkActivities(tx.WithContext(ctx), m.service.names).Create(&receipt).Error; err != nil {
		return 0, err
	}
	w := query.BindWorkItems(tx, m.service.names)
	changed, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(work), w.ActivitySeq.Eq(row.ActivitySeq)).Updates(map[string]any{"activity_seq": seq})
	if err != nil {
		return 0, err
	}
	if changed.RowsAffected != 1 {
		return 0, ErrWorkVersion
	}
	return seq, nil
}
func (m *Module) workActivityHeadOn(ctx context.Context, tx *gorm.DB, tenant, work guid.ID) (int64, error) {
	row, err := m.workActivityRowOn(ctx, tx, tenant, work)
	if err != nil {
		return 0, err
	}
	return row.ActivitySeq, nil
}
func (m *Module) workActivitySinceOn(ctx context.Context, tx *gorm.DB, tenant, work guid.ID, after int64, limit int) ([]WorkActivityView, error) {
	return m.workActivityPageOn(ctx, tx, tenant, work, after, limit, 0)
}
func (m *Module) workActivityPageOn(ctx context.Context, tx *gorm.DB, tenant, work guid.ID, after int64, limit, offset int) ([]WorkActivityView, error) {
	if !isWorkActivityTransaction(tx) || tenant <= 0 || work <= 0 || after < 0 || limit < 1 || limit > 200 || offset < 0 {
		return nil, ErrInvalidArgument
	}
	if m == nil || m.service == nil {
		return nil, ErrUnavailable
	}
	rows := []model.WorkItemActivity{}
	err := query.BindWorkActivities(tx.WithContext(ctx), m.service.names).Where(map[string]any{"tenant_id": tenant, "work_item_id": work}).Where(clause.Gt{Column: clause.Column{Name: "seq"}, Value: after}).Order(clause.OrderByColumn{Column: clause.Column{Name: "seq"}}).Limit(limit).Offset(offset).Find(&rows).Error
	out := []WorkActivityView{}
	for _, row := range rows {
		out = append(out, workActivityView(row))
	}
	return out, err
}

var ErrWorkSeenSeqRequired = errors.New("delivery: seen_seq required")

type WorkWriteInterrupted struct {
	State       string             `json:"state"`
	Stream      string             `json:"stream"`
	SeenSeq     int64              `json:"seen_seq"`
	CurrentSeq  int64              `json:"current_seq"`
	NewerEvents []WorkActivityView `json:"newer_events"`
	Truncated   bool               `json:"truncated"`
}

func (*WorkWriteInterrupted) Error() string { return "delivery: work context interrupted" }
func (m *Module) checkWorkSeenOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, seen *int64) error {
	if seen == nil {
		return nil
	}
	if *seen < 0 || *seen > work.ActivitySeq {
		return ErrInvalidArgument
	}
	if *seen == work.ActivitySeq {
		return nil
	}
	rows := []model.WorkItemActivity{}
	err := query.BindWorkActivities(tx.WithContext(ctx), m.service.names).Where(map[string]any{"tenant_id": work.TenantID, "work_item_id": work.ID}).Where(clause.Gt{Column: clause.Column{Name: "seq"}, Value: *seen}, clause.Neq{Column: clause.Column{Name: "actor_id"}, Value: authn.PrincipalIDFromContext(ctx)}).Order(clause.OrderByColumn{Column: clause.Column{Name: "seq"}}).Limit(51).Find(&rows).Error
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	truncated := len(rows) > 50
	if truncated {
		rows = rows[:50]
	}
	newer := []WorkActivityView{}
	for _, row := range rows {
		newer = append(newer, workActivityView(row))
	}
	return &WorkWriteInterrupted{State: "interrupted", Stream: "work_item:" + work.ID.String(), SeenSeq: *seen, CurrentSeq: work.ActivitySeq, NewerEvents: newer, Truncated: truncated}
}

// checkWorkContextOn keeps the source row lock and stale-context rules owned by
// delivery. Callers authorize their operation before using this capability.
func (m *Module) checkWorkContextOn(ctx context.Context, tx *gorm.DB, tenant, work guid.ID, seen *int64, required bool) error {
	row, err := m.workActivityRowOn(ctx, tx, tenant, work)
	if err != nil {
		return err
	}
	if required && seen == nil {
		return ErrWorkSeenSeqRequired
	}
	return m.checkWorkSeenOn(ctx, tx, row, seen)
}
