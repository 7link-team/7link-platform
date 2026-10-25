package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestTerminalExecutionUsesFrozenWorkspaceAndCurrentHumanAuthority(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		fact, e := f.m.terminalExecutionOn(f.human, tx, 7, v.ID, "terminal_interact")
		require.NoError(t, e)
		require.Equal(t, "/registered/workspace", fact.Workspace)
		require.Equal(t, guid.ID(9), fact.ProjectID)
		return nil
	}))
	f.humanActive = false
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.terminalExecutionOn(f.human, tx, 7, v.ID, "terminal_interact")
		return e
	}))
	f.humanActive = true
	q := query.BindExecutions(f.db, f.m.names)
	_, err := q.WithContext(context.Background()).Where(q.ID.Eq(v.ID)).Update(q.State, "SUCCEEDED")
	require.NoError(t, err)
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.terminalExecutionOn(f.human, tx, 7, v.ID, "terminal_interact")
		return e
	}))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.terminalExecutionOn(f.human, tx, 7, v.ID, "audit_read"); return e }))
}

func TestBindTerminalExecutionsRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindTerminalExecutions(module.NewServices()), module.ErrServicesProviding)
}
