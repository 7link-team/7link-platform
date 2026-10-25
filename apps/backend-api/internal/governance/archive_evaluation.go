package governance

import (
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
)

// ProjectArchiveFact is an owner-observed authority or pending approval.
type ProjectArchiveFact struct {
	Kind    string
	ID      guid.ID
	State   string
	Version int
	Ready   bool
}
type ProjectArchiveFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveFact, error)

func (m *Module) provideArchiveEvaluation(s *module.Services) error {
	if err := module.Provide[EscapeEvalConnector](s, EscapeEvalConnector{Bind: func(writer EscapeEvalWriter) error {
		if writer == nil || m.escapeEval != nil {
			return ErrInvalidArgument
		}
		m.escapeEval = writer
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[ProjectArchiveFacts](s, m.projectArchiveFactsOn)
}

// EscapeEvalFact is emitted only from the persisted source escape. The snapshot
// retains Human rejection and accepted gate provenance; it assigns no verifier label.
type EscapeEvalFact struct {
	TenantID, ProjectID, ID, WorkItemID guid.ID
	Title                               string
	Snapshot                            json.RawMessage
}
type EscapeEvalWriter func(context.Context, *gorm.DB, EscapeEvalFact) (guid.ID, bool, error)
type EscapeEvalConnector struct{ Bind func(EscapeEvalWriter) error }

func (m *Module) proposeEscapeEvalOn(ctx context.Context, tx *gorm.DB, row *model.QualityEscape) error {
	if m.escapeEval == nil {
		return nil
	}
	snapshot, err := json.Marshal(map[string]any{"escape_id": row.ID, "source_event_id": row.SourceEventID, "work_item_id": row.WorkItemID, "stage": row.Stage, "category": row.Category, "summary": row.Summary, "passed_by": json.RawMessage(row.PassedBy), "human_acceptance": "REJECTED"})
	if err != nil {
		return err
	}
	id, _, err := m.escapeEval(ctx, tx, EscapeEvalFact{TenantID: row.TenantID, ProjectID: row.ProjectID, ID: row.ID, WorkItemID: row.WorkItemID, Title: "Quality escape " + row.ID.String(), Snapshot: snapshot})
	if err != nil {
		return err
	}
	if id <= 0 {
		return nil
	}
	ref := id.String()
	if row.EvalCaseRef != nil {
		if *row.EvalCaseRef != ref {
			return ErrVersion
		}
		return nil
	}
	_, err = query.BindQualityEscapes(tx, m.seed.names).WithContext(ctx).Where(query.BindQualityEscapes(tx, m.seed.names).TenantID.Eq(row.TenantID), query.BindQualityEscapes(tx, m.seed.names).ID.Eq(row.ID)).Updates(map[string]any{"eval_case_ref": ref, "version": row.Version + 1})
	if err == nil {
		row.EvalCaseRef = &ref
		row.Version++
	}
	return err
}
func (m *Module) projectArchiveFactsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]ProjectArchiveFact, error) {
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	q := query.BindDelegations(tx, m.seed.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	out := make([]ProjectArchiveFact, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProjectArchiveFact{Kind: "DELEGATION", ID: r.ID, State: r.State, Version: r.Version, Ready: r.ActiveKey == nil && r.EndedAt != nil})
	}
	// Requests have project scope in their immutable payload, not a project column.
	requests := query.BindSpawnRequests(tx, m.seed.names)
	var after guid.ID
	for {
		rows, err := requests.WithContext(ctx).Where(requests.TenantID.Eq(tenant), requests.ID.Gt(after), requests.State.In("REQUESTED", "UNAVAILABLE")).Clauses(clause.Locking{Strength: "UPDATE"}).Order(requests.ID).Limit(200).Find()
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			var payload agentSpawnPayload
			if json.Unmarshal([]byte(row.Payload), &payload) != nil || payload.GrantID <= 0 {
				return nil, ErrUnavailable
			}
			if slices.Contains(payload.Facts.ProjectIDs, project) {
				out = append(out, ProjectArchiveFact{Kind: "AGENT_SPAWN_APPROVAL", ID: row.ID, State: row.State, Version: row.Version})
			}
			after = row.ID
		}
		if len(rows) < 200 {
			break
		}
	}
	return out, nil
}
