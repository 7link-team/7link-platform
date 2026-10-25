package delivery

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFleetRepositoryProviderCurrentAuthorityIsolationAndArchive(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	services := module.NewServices()
	require.NoError(t, m.provideFleetRepository(services))
	require.NoError(t, services.Seal())
	port, e := module.Resolve[fleet.RepositoryAuthorizer](services)
	require.NoError(t, e)
	now := time.Now().UTC().Truncate(time.Millisecond)
	repo := &model.ProjectRepository{TenantID: 1, ID: 401, ProjectID: 99, Name: "Source", RemoteURL: "https://example.test/repo.git", DefaultBranch: "main", ProtectedBranches: "[]", Version: 1, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, query.BindRepositories(db, m.service.names).WithContext(ctx).Create(repo))
	run := func(tenant, project, id guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error { return port(ctx, tx, tenant, project, id) })
	}
	require.NoError(t, run(1, 99, 401))
	require.Error(t, run(2, 99, 401))
	require.Error(t, run(1, 98, 401))
	require.ErrorIs(t, run(1, 99, 402), fleet.ErrMachineNotFound)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, run(1, 99, 401), fleet.ErrMachineForbidden)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	q := query.BindRepositories(db, m.service.names)
	_, e = q.WithContext(ctx).Where(q.ID.Eq(guid.ID(401))).Update(q.ArchivedAt, now)
	require.NoError(t, e)
	require.ErrorIs(t, run(1, 99, 401), fleet.ErrMachineNotFound)
}
