package jobs

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/query"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProjectArchiveJobs reads project-scoped queued/running jobs. The originating
// domain owns pausing its recurring contract; queue receipts never substitute
// for that contract's state.
type ProjectArchiveJob struct {
	ID      guid.ID
	State   string
	Version int
}
type ProjectArchiveJobs func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveJob, error)

func (m *Module) provideArchiveJobs(s *module.Services) error {
	return module.Provide[ProjectArchiveJobs](s, m.projectArchiveJobsOn)
}
func (m *Module) projectArchiveJobsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]ProjectArchiveJob, error) {
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return nil, ErrInvalidRequest
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidRequest
	}
	q := query.BindJobs(tx, m.names)
	out := []ProjectArchiveJob{}
	var cursor guid.ID
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(cursor), q.State.In("PENDING", "RUNNING", "RETRY")).Clauses(clause.Locking{Strength: "UPDATE"}).Order(q.ID).Limit(200).Find()
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			var payload struct {
				ProjectID guid.ID `json:"project_id"`
			}
			if err = json.Unmarshal([]byte(r.Payload), &payload); err != nil {
				return nil, err
			}
			if payload.ProjectID == project {
				out = append(out, ProjectArchiveJob{ID: r.ID, State: r.State, Version: r.Version})
			}
			cursor = r.ID
		}
		if len(rows) < 200 {
			return out, nil
		}
	}
}
