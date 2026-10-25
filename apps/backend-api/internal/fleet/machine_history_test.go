package fleet

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestMachineHistoryConnectorAndScopedContinuation(t *testing.T) {
	f := inspectionHarness(t)
	_, err := f.m.machineHistoryHTTP(f.ctx, &machineHistoryInput{MachineInput: MachineInput{MachineID: "1"}, Limit: 1})
	require.Error(t, err)
	services := module.NewServices()
	require.NoError(t, f.m.provideMachineHistory(services))
	services.Seal()
	connector, err := module.Resolve[MachineExecutionHistoryConnector](services)
	require.NoError(t, err)
	calls := 0
	reader := func(_ context.Context, _ *gorm.DB, tenant, machine, after guid.ID, limit int) (MachineExecutionHistoryPage, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(1), machine)
		require.Equal(t, 1, limit)
		calls++
		if after == 0 {
			return MachineExecutionHistoryPage{Items: []MachineExecutionHistoryEntry{{ID: 9007199254740993, ProjectID: 9}}, HasMore: true, NextCursor: "9007199254740993"}, nil
		}
		require.Equal(t, guid.ID(9007199254740993), after)
		return MachineExecutionHistoryPage{Items: []MachineExecutionHistoryEntry{}}, nil
	}
	require.Error(t, connector.Bind(nil))
	require.NoError(t, connector.Bind(reader))
	require.Error(t, connector.Bind(reader))
	first, err := f.m.machineHistoryHTTP(f.ctx, &machineHistoryInput{MachineInput: MachineInput{MachineID: "1"}, Limit: 1})
	require.NoError(t, err)
	require.True(t, first.Body.Data.HasMore)
	last, err := f.m.machineHistoryHTTP(f.ctx, &machineHistoryInput{MachineInput: MachineInput{MachineID: "1"}, Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, err)
	require.Empty(t, last.Body.Data.Items)
	_, err = f.m.machineHistoryHTTP(f.ctx, &machineHistoryInput{MachineInput: MachineInput{MachineID: "2"}, Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.Error(t, err)
	_, err = f.m.machineHistoryHTTP(f.ctx, &machineHistoryInput{MachineInput: MachineInput{MachineID: "1"}, Limit: 101})
	require.Error(t, err)
	require.Equal(t, 2, calls)
}
