package i18n

import (
	"testing"

	"7link.com/platform/pkg/appenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleInitializesEmptyRegistry(t *testing.T) {
	module := NewModule(newTestRuntime(), Base)
	assert.Equal(t, "i18n", module.ModuleName())
	require.NoError(t, module.Initialize(t.Context()))
	assert.Equal(t, Base, Locale())
	assert.Equal(t, "missing", T("missing"))
	assert.Empty(t, defaultI18n.messages)
	assert.Empty(t, defaultI18n.registeredNamespaces)
}

func newTestRuntime() *appenv.Runtime {
	return appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0"))
}
