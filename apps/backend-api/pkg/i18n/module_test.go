package i18n

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleInitializesEmptyRegistry(t *testing.T) {
	module := NewModule(Base)
	assert.Equal(t, "i18n", module.ModuleName())
	require.NoError(t, module.Initialize(t.Context()))
	assert.Equal(t, Base, Locale())
	assert.Equal(t, "missing", T("missing"))
	assert.Empty(t, defaultI18n.messages)
	assert.Empty(t, defaultI18n.registeredNamespaces)
}
