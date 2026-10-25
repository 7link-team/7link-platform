package execution

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestMachineHistoryUsesActualRecordsAndCurrentProjectAuthority(t *testing.T) {
	f := executionHarness(t)
	execution := f.create(t)
	read := func(tenant, machine, after guid.ID, limit int) error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			page, e := f.m.machineHistoryOn(f.human, tx, tenant, machine, after, limit)
			if e != nil {
				return e
			}
			if tenant == 7 && machine == execution.MachineID && after == 0 {
				require.Len(t, page.Items, 1)
				require.Equal(t, execution.ID, page.Items[0].ID)
				require.Equal(t, execution.State, page.Items[0].State)
				require.Equal(t, execution.Plan.Model, page.Items[0].Model)
			} else {
				require.Empty(t, page.Items)
			}
			return nil
		})
	}
	require.NoError(t, read(7, execution.MachineID, 0, 1))
	require.NoError(t, read(7, execution.MachineID, execution.ID, 1))
	require.NoError(t, read(8, execution.MachineID, 0, 1))
	require.NoError(t, read(7, execution.MachineID+1, 0, 1))
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		page, e := f.m.machineHistoryOn(f.human, tx, 7, execution.MachineID, 0, 1)
		require.Empty(t, page.Items)
		return e
	}))
	_, err := f.m.machineHistoryOn(f.human, f.db, 7, execution.MachineID, 0, 1)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestBindMachineHistoryRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindMachineHistory(module.NewServices()), module.ErrServicesProviding)
}
