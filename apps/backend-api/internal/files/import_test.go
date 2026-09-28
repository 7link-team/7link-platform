package files

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyImportStreamingRepeatableKeepsSource(t *testing.T) {
	legacyRoot, destinationRoot := t.TempDir(), t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(legacyRoot, "1"), 0700))
	content := bytes.Repeat([]byte("legacy"), 20000)
	require.NoError(t, os.WriteFile(filepath.Join(legacyRoot, "1", "image.png"), content, 0600))
	driver, err := NewLocal(destinationRoot)
	require.NoError(t, err)
	roots := map[string]string{"avatars": legacyRoot}
	require.NoError(t, importLegacy(t.Context(), driver, roots, "avatars/1/image.png"))
	require.NoError(t, importLegacy(t.Context(), driver, roots, "avatars/1/image.png"))
	actual, err := os.ReadFile(filepath.Join(destinationRoot, "avatars", "1", "image.png"))
	require.NoError(t, err)
	require.Equal(t, content, actual)
	require.NoError(t, os.WriteFile(filepath.Join(destinationRoot, "avatars", "1", "image.png"), []byte("corrupt"), 0600))
	require.ErrorIs(t, importLegacy(t.Context(), driver, roots, "avatars/1/image.png"), ErrLength)
	_, err = os.Stat(filepath.Join(legacyRoot, "1", "image.png"))
	require.NoError(t, err)
	require.ErrorIs(t, importLegacy(t.Context(), driver, roots, "avatars/1/missing.png"), os.ErrNotExist)
	require.ErrorIs(t, importLegacy(t.Context(), driver, roots, "other/1/image.png"), os.ErrNotExist)
	require.ErrorIs(t, importLegacy(t.Context(), driver, roots, "../escape"), ErrInvalidKey)
}
