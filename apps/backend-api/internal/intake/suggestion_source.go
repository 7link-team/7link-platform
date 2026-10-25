package intake

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"gorm.io/gorm"
)

// SuggestionSource contains the actual immutable source, without contact
// identities, reply tokens or approval facts. Like IntakeReader, its caller
// must hold current authorized project/principal fences before using it.
type SuggestionSource struct {
	ID          guid.ID `json:"id"`
	ProjectID   guid.ID `json:"project_id"`
	Version     int     `json:"version"`
	Kind        string  `json:"kind"`
	State       string  `json:"state"`
	Title       string  `json:"title"`
	Content     *string `json:"content,omitempty"`
	ContentHash string  `json:"content_hash"`
}

type SuggestionSourcePage struct {
	Items   []SuggestionSource
	HasMore bool
}

// Discovery returns eligible metadata only. The authorized caller reads the
// actual source separately before drafting an assessment.
func (m *Module) suggestionSourcesOn(ctx context.Context, tx *gorm.DB, tenant, project, before guid.ID, limit int) (SuggestionSourcePage, error) {
	out := SuggestionSourcePage{Items: []SuggestionSource{}}
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || before < 0 || limit < 1 || limit > 100 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return out, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	q := query.BindRecords(tx, m.names)
	r := q.WithContext(ctx).Select(q.ID, q.Version, q.Kind, q.Status, q.Title, q.RawContentHash).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Kind.In("NEW", "REWORK"), q.Status.In("RECEIVED", "TRIAGED"))
	if before > 0 {
		r = r.Where(q.ID.Lt(before))
	}
	rows, err := r.Order(q.ID.Desc()).Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	if len(rows) > limit {
		out.HasMore = true
		rows = rows[:limit]
	}
	for _, row := range rows {
		out.Items = append(out.Items, SuggestionSource{ID: row.ID, ProjectID: project, Version: row.Version, Kind: row.Kind, State: row.Status, Title: row.Title, ContentHash: row.RawContentHash})
	}
	return out, nil
}

func (m *Module) suggestionSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (SuggestionSource, error) {
	var out SuggestionSource
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || id <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return out, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.Kind.In("NEW", "REWORK"), q.Status.In("RECEIVED", "TRIAGED")).Take()
	if err != nil {
		return out, err
	}
	return SuggestionSource{ID: row.ID, ProjectID: project, Version: row.Version, Kind: row.Kind, State: row.Status, Title: row.Title, Content: row.RawContent, ContentHash: row.RawContentHash}, nil
}

// Resolves only the project fence; the caller must authorize it before returning
// source data. This capability is never a public source-read permission.
func (m *Module) suggestionProjectOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (guid.ID, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return 0, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return 0, err
	}
	if row.ProjectID == nil {
		return 0, ErrUnavailable
	}
	return *row.ProjectID, nil
}
