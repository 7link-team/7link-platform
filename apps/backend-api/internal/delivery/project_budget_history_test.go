package delivery

import (
	"context"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectBudgetHistoryConnectorRequiresRealReader(t *testing.T) {
	m := &Module{service: &projectService{}}
	s := module.NewServices()
	require.NoError(t, m.provideProjectBudgetHistory(s))
	require.NoError(t, s.Seal())
	c, e := module.Resolve[ProjectBudgetHistoryConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, c.Bind(nil), ErrUnavailable)
	read := ProjectBudgetHistory(func(context.Context, *gorm.DB, guid.ID, guid.ID, int64) (ProjectBudgetTrend, error) {
		return ProjectBudgetTrend{Available: true, Points: []ProjectBudgetPoint{}}, nil
	})
	require.NoError(t, c.Bind(read))
	require.NotNil(t, m.service.budgetHistory)
}
