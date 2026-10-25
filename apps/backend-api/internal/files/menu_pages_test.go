package files

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestVersionedMenuMatchesEmbeddedFilePage(t *testing.T) {
	p := (&Module{}).MenuPages()
	require.Len(t, p, 1)
	legacy := systemFileMenu()
	require.Equal(t, legacy.Menu.Route, p[0].Route)
	require.Equal(t, legacy.Menu.PermissionCode, p[0].PermissionCode)
	require.Equal(t, legacy.Menu.Translations, p[0].Translations)
	require.EqualValues(t, 1, p[0].Version)
	for _, loc := range []string{"en-US", "zh-CN", "ms-MY"} {
		require.NotEmpty(t, p[0].Translations[loc])
	}
}
