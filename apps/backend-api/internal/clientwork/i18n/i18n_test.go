package clienti18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestEveryClientErrorHasLocalizedTextInAllSupportedLanguages(t *testing.T) {
	var baseline map[string]string
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		data, e := locales.ReadFile("locales/" + locale + ".json")
		require.NoError(t, e)
		var messages map[string]string
		require.NoError(t, json.Unmarshal(data, &messages))
		for _, key := range []string{Invalid, NotFound, Conflict, Forbidden, Unavailable, Internal} {
			require.NotEmpty(t, messages[strings.TrimPrefix(key, Namespace+".")])
		}
		if baseline == nil {
			baseline = messages
		} else {
			require.Len(t, messages, len(baseline))
			for key := range baseline {
				require.Contains(t, messages, key)
			}
		}
	}
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, Register())
}
