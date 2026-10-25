package fleet

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestRuntimeFactsStayTenantScoped(t *testing.T) {
	m, db := directoryHarness(t)
	_, _, err := m.agentRuntimeOn(t.Context(), db, 7, 101)
	require.Error(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		f, found, e := m.agentRuntimeOn(t.Context(), tx, 7, 101)
		require.NoError(t, e)
		require.True(t, found)
		require.Equal(t, "CODEX", f.RuntimeKind)
		require.Equal(t, "UNASSIGNED", f.MachineState)
		require.Nil(t, f.MachineID)
		for _, tenant := range []guid.ID{8, 9} {
			f, found, e = m.agentRuntimeOn(t.Context(), tx, tenant, 101)
			require.NoError(t, e)
			require.False(t, found)
			require.Empty(t, f)
		}
		return nil
	}))
	s := module.NewServices()
	require.NoError(t, m.provideAgentRuntime(s))
	require.NoError(t, s.Seal())
	facts, e := module.Resolve[AgentRuntimeFacts](s)
	require.NoError(t, e)
	require.NotNil(t, facts.ByPrincipalOn)
}

func TestRetirementRuntimeFactsDoNotRequireCredentialDelivery(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	require.NoError(t, db.Table(m.names.Agents.String()).Where("tenant_id=? AND principal_id=?", 7, 101).Updates(map[string]any{"machine_id": 1, "credential_id": 333, "credential_delivery": "PENDING"}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.agentRuntimeOn(ctx, tx, 7, 101)
		require.ErrorIs(t, err, ErrVersionConflict)
		fact, found, err := m.agentRuntimeFactOn(ctx, tx, 7, 101, false)
		require.NoError(t, err)
		require.True(t, found)
		require.EqualValues(t, 1, *fact.MachineID)
		require.Equal(t, "OFFLINE", fact.MachineState)
		return nil
	}))
}

// Runtime connectivity must come from the assigned machine in the same tenant.
func TestRuntimeFactsFollowAssignedMachineAndStorageFailures(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	require.NoError(t, db.Table(m.names.Agents.String()).Where("tenant_id = ? AND principal_id = ?", 7, 101).Update("machine_id", 1).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		facts, found, err := m.agentRuntimeOn(ctx, tx, 7, 101)
		require.NoError(t, err)
		require.True(t, found)
		require.NotNil(t, facts.MachineID)
		require.EqualValues(t, 1, *facts.MachineID)
		require.Equal(t, "OFFLINE", facts.MachineState)
		return nil
	}))
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Update("tenant_id", 8).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		facts, found, err := m.agentRuntimeOn(ctx, tx, 7, 101)
		require.ErrorIs(t, err, ErrMachineNotFound)
		require.False(t, found)
		require.Empty(t, facts)
		return nil
	}))
	failure := errors.New("runtime query failed")
	reads := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("runtime_query_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Agents.String() {
			reads++
			// The directory read succeeds; the runtime-specific read must also fail closed.
			if reads == 2 {
				_ = tx.AddError(failure)
			}
		}
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		facts, found, err := m.agentRuntimeOn(ctx, tx, 7, 101)
		require.ErrorIs(t, err, failure)
		require.False(t, found)
		require.Empty(t, facts)
		return nil
	}))
	require.Equal(t, 2, reads)
}
