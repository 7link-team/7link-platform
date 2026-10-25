package execution

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestProjectStageQuietUsesActualExecutionStateAndTenant(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	for _, test := range []struct {
		tenant, project guid.ID
		want            bool
	}{{7, 9, false}, {8, 9, true}, {7, 2, true}} {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			quiet, e := f.m.projectStageQuietOn(f.human, tx, test.tenant, test.project)
			require.NoError(t, e)
			require.Equal(t, test.want, quiet)
			return nil
		}))
	}
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("state", "CANCELLED").Error) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		quiet, e := f.m.projectStageQuietOn(f.human, tx, 7, 9)
		require.NoError(t, e)
		require.True(t, quiet)
		return nil
	}))
}

func TestBindProjectStageExecutionsRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindProjectStageExecutions(module.NewServices()), module.ErrServicesProviding)
}
