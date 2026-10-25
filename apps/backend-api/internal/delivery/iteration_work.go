package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

type PlanningWorkIdentity struct {
	State, Type string
	PrincipalID guid.ID
}
type PlanningWorkIdentityOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (PlanningWorkIdentity, error)

func (m *Module) provideIterationWork(s *module.Services) error {
	if err := m.provideAutomationWorkOwner(s); err != nil {
		return err
	}
	return module.Provide[PlanningWorkIdentityOn](s, m.iterationWorkOn)
}
func (m *Module) iterationWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (PlanningWorkIdentity, error) {
	state, err := m.templateWorkStatusOn(ctx, tx, tenant, project, id)
	if err != nil {
		return PlanningWorkIdentity{}, err
	}
	q := query.BindWorkItems(tx, m.service.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if err != nil {
		return PlanningWorkIdentity{}, err
	}
	return PlanningWorkIdentity{State: state, Type: r.Type, PrincipalID: r.AccountablePrincipalID}, nil
}
