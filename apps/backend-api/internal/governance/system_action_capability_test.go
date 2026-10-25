package governance

import (
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSystemActionReaderRequiresSealedExactRegistration(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideSystemActions(s))
	require.NoError(t, s.Seal())
	read, err := module.Resolve[SystemActionAllowed](s)
	require.NoError(t, err)
	require.NoError(t, m.systemActions.Register("automation", "work_item_write"))
	require.False(t, read("automation", "work_item_write"))
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "work_item", Verb: "write", Scope: authz.ActionScopeTenant}}, nil))
	require.NoError(t, m.systemActions.seal(registry))
	require.True(t, read("automation", "work_item_write"))
	require.False(t, read("automation:700", "work_item_write"))
	require.False(t, read("automation", "decision_approve"))
}
