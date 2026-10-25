package intake

import (
	"context"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TriageRequest struct {
	RequestedAt time.Time `json:"requested_at"`
	Version     int       `json:"version"`
	Coalesced   bool      `json:"coalesced"`
}

// Current policy and channel configuration are checked even for idempotent
// replay. The project owner fences principal/project before the source lock.
func (m *Module) triageRequestSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (*model.Record, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || id <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.EntityIDFromContext(ctx) != project || authn.PrincipalIDFromContext(ctx) <= 0 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	if m.triageRequestAuthority == nil || m.triageRequestCoordinator == nil {
		return nil, ErrUnavailable
	}
	q := query.BindRecords(tx, m.names)
	hint, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if err != nil {
		return nil, err
	}
	if err = m.triageRequestCoordinator(ctx, tx, tenant, project, hint.ChannelID, hint.ChannelKind, m.triageRequestAuthority); err != nil {
		return nil, err
	}
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if err != nil {
		return nil, err
	}
	if row.ChannelID != hint.ChannelID || row.ChannelKind != hint.ChannelKind {
		return nil, ErrConflict
	}
	return row, nil
}

func (m *Module) authorizeTriageRequestOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) error {
	_, err := m.triageRequestSourceOn(ctx, tx, tenant, project, id)
	return err
}

func (m *Module) requestTriageOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, note string) (TriageRequest, error) {
	var out TriageRequest
	if !utf8.ValidString(note) || utf8.RuneCountInString(note) > 2000 {
		return out, ErrInvalidArgument
	}
	row, err := m.triageRequestSourceOn(ctx, tx, tenant, project, id)
	if err != nil {
		return out, err
	}
	if row.Status != "RECEIVED" && row.Status != "TRIAGED" && row.Status != "CLARIFYING" {
		return out, ErrConflict
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	if row.LastTriageRequestedAt != nil && at.Before(row.LastTriageRequestedAt.Add(10*time.Minute)) {
		return TriageRequest{RequestedAt: *row.LastTriageRequestedAt, Version: row.Version, Coalesced: true}, nil
	}
	if m.brief.audit == nil || m.brief.events == nil {
		return out, ErrUnavailable
	}
	q := query.BindRecords(tx, m.names)
	actor := authn.PrincipalIDFromContext(ctx)
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.Version.Eq(row.Version)).Updates(map[string]any{"last_triage_requested_at": at, "version": row.Version + 1, "updated_at": at, "updated_by": actor})
	if err != nil {
		return out, err
	}
	if result.RowsAffected != 1 {
		return out, ErrConflict
	}
	kind := "HUMAN"
	if m.brief.principals.ActiveHumanByID == nil {
		return out, ErrUnavailable
	}
	human, active, err := m.brief.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return out, err
	}
	if !active || human.ID != actor {
		kind = "AGENT"
	}
	if err = m.sourceEvent(ctx, tx, tenant, project, id, "intake.triage_requested", row.Version+1, map[string]any{"requested_at": at, "note": note, "actor_kind": kind}, "IAM:intake_triage"); err != nil {
		return out, err
	}
	return TriageRequest{RequestedAt: at, Version: row.Version + 1}, nil
}
