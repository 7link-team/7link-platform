package intake

import (
	"testing"

	intakei18n "7link.com/platform/internal/intake/i18n"
	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/require"
)

func TestModuleRegistersActivityTranslations(t *testing.T) {
	require.NoError(t, i18n.NewModule(nil, i18n.Base).Initialize(t.Context()))
	m := &Module{}
	require.NoError(t, m.Initialize(t.Context()))
	key := intakei18n.Namespace + ".activity_event_intake_received_name"
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		require.NotEqual(t, key, i18n.TContext(i18n.WithLocale(t.Context(), locale), key))
	}
	require.ErrorContains(t, m.Initialize(t.Context()), "already registered")
}
