package files

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestPortableNameAndCollisionKey(t *testing.T) {
	for _, name := range []string{"报告.mp4", "Budget.XLSX", "photo (1).png", "🙂.txt"} {
		actual, key, err := PortableName(name)
		require.NoError(t, err)
		require.Equal(t, name, actual)
		require.NotEmpty(t, key)
	}
	a, ka, err := PortableName("Cafe\u0301.txt")
	require.NoError(t, err)
	_, kb, err := PortableName("CAFÉ.TXT")
	require.NoError(t, err)
	require.Equal(t, "Café.txt", a)
	require.Equal(t, ka, kb)
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a:", "a?", "a\n", "a.", "a ", "CON", "con.txt", "NUL.png", "COM1", "LPT².txt", "CON .txt", "CONOUT$", strings.Repeat("a", 256), strings.Repeat("中", 86), string([]byte{0xff})} {
		_, _, err := PortableName(name)
		require.ErrorIs(t, err, ErrName, name)
	}
	require.Equal(t, "mp3", Extension("renamed.MP3"))
	require.Equal(t, "", Extension("README"))
}
