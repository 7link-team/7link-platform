package respx

import (
	"testing"

	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleRegistersOnlyNamespacedMessages(t *testing.T) {
	rt := newTestRuntime()
	require.NoError(t, i18n.NewModule(rt, i18n.Base).Initialize(t.Context()))

	module := NewModule(rt)
	assert.Equal(t, Name, module.ModuleName())
	assert.Equal(t, []string{"i18n"}, module.ModuleDependencies())
	require.NoError(t, module.Initialize(t.Context()))

	assert.Equal(t, "Success", i18n.T(messageKey("success")))
	assert.Equal(t, "success", i18n.T("success"))
	assert.ErrorContains(t, module.Initialize(t.Context()), `namespace "respx" is already registered`)
}

func newTestRuntime() *appenv.Runtime {
	return appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0"))
}
