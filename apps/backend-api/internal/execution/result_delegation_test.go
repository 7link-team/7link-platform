package execution

import (
	"7link.com/platform/pkg/module"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAcceptanceExecutionIDsAreCurrentAndTenantBound(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		ids, err := f.m.acceptanceExecutionIDsOn(t.Context(), tx, 7, 9, v.WorkItemID)
		require.NoError(t, err)
		require.Equal(t, []guid.ID{v.ID}, ids)
		for _, scope := range [][3]guid.ID{{8, 9, v.WorkItemID}, {7, 10, v.WorkItemID}, {7, 9, v.WorkItemID + 1}} {
			ids, err = f.m.acceptanceExecutionIDsOn(t.Context(), tx, scope[0], scope[1], scope[2])
			require.NoError(t, err)
			require.Empty(t, ids)
		}
		return nil
	}))
	_, err := f.m.acceptanceExecutionIDsOn(t.Context(), f.db, 7, 9, v.WorkItemID)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestBindAcceptanceExecutionsRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindAcceptanceExecutions(module.NewServices()), module.ErrServicesProviding)
}
