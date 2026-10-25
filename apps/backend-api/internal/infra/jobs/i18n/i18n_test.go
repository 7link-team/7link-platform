package jobsi18n

import (
	"7link.com/platform/pkg/i18n"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAdministrativeMessagesCoverEveryLocale(t *testing.T) {
	require.NoError(t, i18n.NewModule(nil, "en-US").Initialize(t.Context()))
	require.NoError(t, Register())
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		raw, err := locales.ReadFile("locales/" + locale + ".json")
		require.NoError(t, err)
		var messages map[string]string
		require.NoError(t, json.Unmarshal(raw, &messages))
		for _, key := range []string{"version_required", "version_conflict", "invalid_transition", "schedule_active"} {
			require.NotEmpty(t, messages[key], "%s: %s", locale, key)
		}
	}
}
