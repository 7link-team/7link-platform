package intake

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// Submission is deliberately independent of the internal ledger/source DTOs.
// Consumers must authorize its current project before returning it publicly.
type Submission struct {
	ChangeView    *SubmissionChangeView `json:"change_view,omitempty"`
	ID            guid.ID               `json:"id"`
	ProjectID     guid.ID               `json:"project_id"`
	RefNo         int                   `json:"ref_no"`
	ExternalRef   *string               `json:"external_ref,omitempty"`
	Status        string                `json:"status"`
	Kind          string                `json:"kind"`
	ReceivedAt    time.Time             `json:"received_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
	ClientMessage *string               `json:"client_message,omitempty"`
	Version       int                   `json:"version"`
}

type SubmissionFilter struct {
	ProjectID                guid.ID
	Sort                     string
	Status, ExternalRef      string
	ReceivedFrom, ReceivedTo *time.Time
}

type SubmissionPage struct {
	Items   []Submission
	HasMore bool
}

func (m *Module) submissionActorOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) (guid.ID, error) {
	actor := authn.PrincipalIDFromContext(ctx)
	if tx == nil || tx.Statement == nil || tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant || actor <= 0 {
		return 0, ErrForbidden
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	if m.brief.principals.Lock == nil || m.brief.principals.ActiveHumanByID == nil {
		return 0, ErrUnavailable
	}
	if err := m.brief.principals.Lock(ctx, tx, actor); err != nil {
		return 0, err
	}
	human, active, err := m.brief.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return 0, err
	}
	if !active || human.ID != actor {
		return 0, ErrForbidden
	}
	return actor, nil
}

func submissionView(row *model.Record) Submission {
	return Submission{ID: row.ID, ProjectID: *row.ProjectID, RefNo: row.RefNo, ExternalRef: row.ExternalRef, Status: row.Status, Kind: row.Kind, ReceivedAt: row.ReceivedAt, UpdatedAt: row.UpdatedAt, ClientMessage: row.ClientMessage, Version: row.Version}
}

// listOwnSubmissionsOn discovers a bounded page of this Human's own sources.
// The caller holds the current tenant membership fence and must apply current
// project intake_submit authorization to each candidate before publishing it.
func (m *Module) listOwnSubmissionsOn(ctx context.Context, tx *gorm.DB, tenant, before guid.ID, limit int, filter SubmissionFilter) (SubmissionPage, error) {
	out := SubmissionPage{Items: []Submission{}}
	if filter.Sort != "" && filter.Sort != "-received_at" && filter.Sort != "received_at" {
		return out, ErrInvalidArgument
	}
	if before < 0 || filter.ProjectID < 0 || limit < 1 || limit > 100 || !utf8.ValidString(filter.ExternalRef) || utf8.RuneCountInString(filter.ExternalRef) > 128 || (filter.ReceivedFrom != nil && filter.ReceivedTo != nil && filter.ReceivedFrom.After(*filter.ReceivedTo)) {
		return out, ErrInvalidArgument
	}
	if filter.Status != "" && !strings.Contains("|RECEIVED|TRIAGED|CLARIFYING|CONVERTED|MERGED|DECLINED|", "|"+filter.Status+"|") {
		return out, ErrInvalidArgument
	}
	actor, err := m.submissionActorOn(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	q := query.BindRecords(tx, m.names)
	r := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.SubmittedByPrincipalID.Eq(actor), q.ProjectID.IsNotNull())
	if filter.ProjectID > 0 {
		r = r.Where(q.ProjectID.Eq(filter.ProjectID))
	}
	if before > 0 {
		// Receipt timestamps are immutable. Resolve only the current actor's
		// anchor, then use the unique ID to order equal timestamps strictly.
		anchor, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.SubmittedByPrincipalID.Eq(actor), q.ProjectID.IsNotNull(), q.ID.Eq(before)).Take()
		if err != nil {
			return out, err
		}
		if filter.Sort == "received_at" {
			r = r.Where(q.WithContext(ctx).Where(q.ReceivedAt.Gt(anchor.ReceivedAt)).Or(q.WithContext(ctx).Where(q.ReceivedAt.Eq(anchor.ReceivedAt), q.ID.Gt(before))))
		} else {
			r = r.Where(q.WithContext(ctx).Where(q.ReceivedAt.Lt(anchor.ReceivedAt)).Or(q.WithContext(ctx).Where(q.ReceivedAt.Eq(anchor.ReceivedAt), q.ID.Lt(before))))
		}
	}
	if filter.Status != "" {
		r = r.Where(q.Status.Eq(filter.Status))
	}
	if filter.ExternalRef != "" {
		r = r.Where(q.ExternalRef.Eq(filter.ExternalRef))
	}
	if filter.ReceivedFrom != nil {
		r = r.Where(q.ReceivedAt.Gte(*filter.ReceivedFrom))
	}
	if filter.ReceivedTo != nil {
		r = r.Where(q.ReceivedAt.Lte(*filter.ReceivedTo))
	}
	if filter.Sort == "received_at" {
		r = r.Order(q.ReceivedAt.Asc(), q.ID.Asc())
	} else {
		r = r.Order(q.ReceivedAt.Desc(), q.ID.Desc())
	}
	rows, err := r.Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	if len(rows) > limit {
		out.HasMore = true
		rows = rows[:limit]
	}
	for _, row := range rows {
		out.Items = append(out.Items, submissionView(row))
	}
	return out, nil
}

func (m *Module) ownSubmissionOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (Submission, error) {
	if id <= 0 {
		return Submission{}, ErrInvalidArgument
	}
	actor, err := m.submissionActorOn(ctx, tx, tenant)
	if err != nil {
		return Submission{}, err
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.SubmittedByPrincipalID.Eq(actor), q.ProjectID.IsNotNull(), q.ID.Eq(id)).Take()
	if err != nil {
		return Submission{}, err
	}
	out := submissionView(row)
	if row.Kind == "CHANGE" {
		out.ChangeView, err = m.submissionChangeOn(ctx, tx, row)
	}
	return out, err
}
