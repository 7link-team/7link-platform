package knowledge

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// sourceProjectFrozenOn resolves source ownership before taking the project
// fence. Callers hold that fence through their source write or queued recovery.
func (m *Module) sourceProjectFrozenOn(ctx context.Context, tx *gorm.DB, tenant, source guid.ID) (bool, error) {
	q := query.BindSources(tx, m.names)
	row, err := q.WithContext(ctx).Select(q.ProjectID).Where(q.TenantID.Eq(tenant), q.ID.Eq(source)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if row.ProjectID == nil {
		return false, nil
	}
	return m.archivedProjectOn(ctx, tx, tenant, *row.ProjectID)
}

type ProjectArchiveResource struct {
	ID      guid.ID
	State   string
	Version int
}
type ProjectArchiveFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error)

type ProjectArchiveStateReader func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)
type ProjectArchiveStateConnector struct {
	Bind func(ProjectArchiveStateReader) error
}
type projectArchiveState struct{ read ProjectArchiveStateReader }

func (m *Module) archivedProjectOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (bool, error) {
	if project <= 0 {
		return false, nil
	}
	if m.archiveState.read == nil {
		return false, ErrUnavailable
	}
	return m.archiveState.read(ctx, tx, tenant, project)
}

func (m *Module) provideArchiveResources(s *module.Services) error {
	if err := module.Provide[ProjectArchiveStateConnector](s, ProjectArchiveStateConnector{Bind: func(read ProjectArchiveStateReader) error {
		if read == nil || m.archiveState.read != nil {
			return ErrInvalid
		}
		m.archiveState.read = read
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[ProjectArchiveFacts](s, m.projectArchiveFactsOn)
}
func (m *Module) projectArchiveFactsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]ProjectArchiveResource, error) {
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return nil, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalid
	}
	q := query.BindEntries(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.Eq("PENDING_REVIEW")).Clauses(clause.Locking{Strength: "UPDATE"}).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	out := []ProjectArchiveResource{}
	for _, r := range rows {
		out = append(out, ProjectArchiveResource{r.ID, r.State, r.Version})
	}
	return out, nil
}
