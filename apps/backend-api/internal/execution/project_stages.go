package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) bindProjectStageExecutions(s *module.Services) error {
	c, e := module.Resolve[delivery.ProjectStageExecutionsConnector](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	if c.Bind == nil {
		return ErrCapabilityUnavailable
	}
	return c.Bind(m.projectStageQuietOn)
}
func (m *Module) projectStageQuietOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (bool, error) {
	if transaction(tx, tenant, project) != nil {
		return false, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	rows, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.In("CREATED", "RUNNING", "PAUSED")).Order(q.ID).Limit(1).Find()
	return len(rows) == 0, e
}
