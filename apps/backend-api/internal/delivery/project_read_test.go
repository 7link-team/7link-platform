package delivery

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectReadUsesTenantAndTransactionalEntityFacts(t *testing.T) {
	s, db, draft := projectHarness(t)
	p := draft.project
	p.ProjectID = 99
	clientID := guid.ID(98)
	p.ClientEntityID = &clientID
	require.NoError(t, query.BindProjects(db, s.names).WithContext(t.Context()).Create(&p))
	calls := 0
	s.entities.GetOn = func(_ context.Context, tx *gorm.DB, tenant, id guid.ID) (core.Entity, error) {
		calls++
		require.Equal(t, guid.ID(1), tenant)
		_, transactional := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, transactional)
		if id == 99 {
			return core.Entity{ID: 99, TenantID: 1, Type: "project", Name: "Current project name"}, nil
		}
		require.Equal(t, clientID, id)
		return core.Entity{ID: 98, TenantID: 1, Type: "client", Name: "Current client name"}, nil
	}
	view, err := s.read(t.Context(), db, 1, 99)
	require.NoError(t, err)
	require.Equal(t, "Current project name", view.Name)
	require.Equal(t, &projectClientView{ID: 98, Name: "Current client name"}, view.Client)
	require.Equal(t, p.Version, view.Version)
	require.Equal(t, 2, calls)
	for _, scope := range [][2]guid.ID{{2, 99}, {1, 100}} {
		_, err = s.read(t.Context(), db, scope[0], scope[1])
		require.ErrorIs(t, err, ErrProjectNotFound)
	}
	require.Equal(t, 2, calls, "missing tenant-scoped project must not query IAM")
	s.entities.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (core.Entity, error) {
		return core.Entity{ID: 99, TenantID: 1, Type: "ordinary"}, nil
	}
	_, err = s.read(t.Context(), db, 1, 99)
	require.ErrorIs(t, err, ErrProjectNotFound)
	s.entities.GetOn = nil
	_, err = s.read(t.Context(), db, 1, 99)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestProjectReadBudgetTrendUsesSameTransactionAndCannotFabricateUnavailableHistory(t *testing.T) {
	s, db, draft := projectHarness(t)
	draft.project.ProjectID = 99
	draft.project.BudgetSpentMicros = 101
	require.NoError(t, query.BindProjects(db, s.names).WithContext(t.Context()).Create(&draft.project))
	s.entities.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (core.Entity, error) {
		return core.Entity{ID: 99, TenantID: 1, Type: "project", Name: "Current name"}, nil
	}
	noSource, err := s.read(t.Context(), db, 1, 99)
	require.NoError(t, err)
	require.False(t, noSource.BudgetTrend.Available)
	require.Empty(t, noSource.BudgetTrend.Points)
	failure := errors.New("ledger unavailable")
	s.budgetHistory = func(_ context.Context, tx *gorm.DB, tenant, project guid.ID, spent int64) (ProjectBudgetTrend, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), project)
		require.Equal(t, int64(101), spent)
		_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, ok)
		return ProjectBudgetTrend{}, failure
	}
	_, err = s.read(t.Context(), db, 1, 99)
	require.ErrorIs(t, err, failure)
	s.budgetHistory = func(context.Context, *gorm.DB, guid.ID, guid.ID, int64) (ProjectBudgetTrend, error) {
		return ProjectBudgetTrend{Available: true, Points: []ProjectBudgetPoint{}}, nil
	}
	view, err := s.read(t.Context(), db, 1, 99)
	require.NoError(t, err)
	require.True(t, view.BudgetTrend.Available)
	require.Empty(t, view.BudgetTrend.Points)
}
