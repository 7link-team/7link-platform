package intake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TriageDraft is an internal assessment, never a signed requirement or a client
// approval. Its project is the source's existing authorized project.
type TriageDraft struct {
	Summary          string   `json:"summary" minLength:"1" maxLength:"2000"`
	RequirementDraft string   `json:"requirement_draft" minLength:"1" maxLength:"8000"`
	Criteria         []string `json:"criteria" minItems:"1" maxItems:"100"`
	Risks            []string `json:"risks" maxItems:"50"`
	Questions        []string `json:"questions" maxItems:"50"`
	Size             string   `json:"size" enum:"XS,S,M,L,XL,UNKNOWN"`
}

type TriageSource struct {
	ID        guid.ID     `json:"id"`
	IntakeID  guid.ID     `json:"intake_id"`
	ProjectID guid.ID     `json:"project_id"`
	Revision  int         `json:"revision"`
	Version   int         `json:"version"`
	Hash      string      `json:"hash"`
	ActorID   guid.ID     `json:"actor_id"`
	CreatedAt time.Time   `json:"created_at"`
	Draft     TriageDraft `json:"draft"`
}

func validTriageText(value string, limit int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= limit
}

func (m *Module) readTriageSourceOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, row *model.Record) (TriageSource, error) {
	var out TriageSource
	c := query.BindTriageCards(tx, m.names)
	card, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.IntakeID.Eq(row.ID), c.Revision.Eq(row.LatestCardRevision), c.ChangeID.IsNull()).Take()
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(card.ProposalJSON), &out.Draft); err != nil {
		return out, err
	}
	hash := sha256.Sum256([]byte(card.ProposalJSON))
	if hex.EncodeToString(hash[:]) != card.ProposalHash || row.ProjectID == nil {
		return out, ErrConflict
	}
	out.ID, out.IntakeID, out.ProjectID = card.ID, row.ID, *row.ProjectID
	out.Revision, out.Version, out.Hash, out.ActorID, out.CreatedAt = card.Revision, row.Version, card.ProposalHash, card.ActorID, card.CreatedAt
	return out, nil
}

func (m *Module) triageSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, expected int, in TriageDraft) (TriageSource, error) {
	var out TriageSource
	if id <= 0 || expected < 1 || !validTriageText(in.Summary, 2000) || !validTriageText(in.RequirementDraft, 8000) || len(in.Criteria) < 1 || len(in.Criteria) > 100 || len(in.Risks) > 50 || len(in.Questions) > 50 {
		return out, ErrInvalidArgument
	}
	switch in.Size {
	case "XS", "S", "M", "L", "XL", "UNKNOWN":
	default:
		return out, ErrInvalidArgument
	}
	for _, list := range [][]string{in.Criteria, in.Risks, in.Questions} {
		for _, value := range list {
			if !validTriageText(value, 2000) {
				return out, ErrInvalidArgument
			}
		}
	}
	if err := m.changeSourceAuthority(ctx, tx, tenant, project, "scope_change"); err != nil {
		return out, err
	}
	if m.brief.ids == nil || m.brief.audit == nil || m.brief.events == nil {
		return out, ErrUnavailable
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if err != nil {
		return out, err
	}
	if (row.Kind != "NEW" && row.Kind != "REWORK") || (row.Status != "RECEIVED" && row.Status != "TRIAGED") || row.Version != expected {
		return out, ErrConflict
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	if len(raw) > 65536 {
		return out, ErrInvalidArgument
	}
	cardID, err := m.brief.ids.Next()
	if err != nil {
		return out, err
	}
	if cardID <= 0 {
		return out, ErrUnavailable
	}
	hash := sha256.Sum256(raw)
	at := time.Now().UTC().Truncate(time.Millisecond)
	actor := authn.PrincipalIDFromContext(ctx)
	// NULL denotes an assessment without a canonical change object;
	// only the separate CHANGE provider can write a real change association.
	card := &model.TriageCard{ID: guid.ID(cardID), TenantID: tenant, IntakeID: id, Revision: row.LatestCardRevision + 1, ProposalHash: hex.EncodeToString(hash[:]), ProposalJSON: string(raw), ImpactJSON: "{}", ActorID: actor, CreatedAt: at}
	c := query.BindTriageCards(tx, m.names)
	if err = c.WithContext(ctx).Create(card); err != nil {
		return out, err
	}
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(expected)).Updates(map[string]any{"status": "TRIAGED", "latest_card_revision": card.Revision, "version": expected + 1, "updated_at": at, "updated_by": actor})
	if err != nil {
		return out, err
	}
	if result.RowsAffected != 1 {
		return out, ErrConflict
	}
	if err = m.sourceEvent(ctx, tx, tenant, project, id, "intake.triaged", expected+1, map[string]any{"card_revision": card.Revision, "proposal_hash": card.ProposalHash, "kind": row.Kind}); err != nil {
		return out, err
	}
	row.LatestCardRevision, row.Version = card.Revision, expected+1
	return m.readTriageSourceOn(ctx, tx, tenant, row)
}
