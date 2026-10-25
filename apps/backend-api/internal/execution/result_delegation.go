package execution

import (
	"context"
	"errors"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) bindAcceptanceExecutions(s *module.Services) error {
	c, err := module.Resolve[delivery.AcceptanceCostConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.BindExecutions == nil {
		return ErrUnavailable
	}
	return c.BindExecutions(m.acceptanceExecutionIDsOn)
}

func (m *Module) acceptanceExecutionIDsOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) ([]guid.ID, error) {
	if transaction(tx, tenant, project) != nil || work <= 0 {
		return nil, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(work)).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	ids := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}
