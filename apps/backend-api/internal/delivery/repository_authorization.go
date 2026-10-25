package delivery

import (
	"context"
	"errors"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// provideFleetRepository exposes only the existing project-manage authorization
// and repository membership check; the consumer never receives storage names.
func (m *Module) provideFleetRepository(s *module.Services) error {
	return module.Provide[fleet.RepositoryAuthorizer](s, m.fleetRepositoryOn)
}
func (m *Module) fleetRepositoryOn(ctx context.Context, tx *gorm.DB, tenant, project, repository guid.ID) error {
	_, _, e := m.constitutionProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), "project_manage")
	if e != nil {
		switch {
		case errors.Is(e, ErrWorkForbidden), errors.Is(e, ErrInvalidMember), errors.Is(e, ErrConstitutionHumanOnly):
			return fleet.ErrMachineForbidden
		case errors.Is(e, ErrProjectNotFound):
			return fleet.ErrMachineNotFound
		default:
			return e
		}
	}
	q := query.BindRepositories(tx, m.service.names)
	_, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(repository), q.ArchivedAt.IsNull()).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return fleet.ErrMachineNotFound
	}
	return e
}
