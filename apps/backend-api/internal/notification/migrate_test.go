package notification

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrationStreamsRemainSymmetric(t *testing.T) {
	var expected []string
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		entries, err := fs.ReadDir(MigrationSource().FS, "sql/"+dialect)
		require.NoError(t, err)
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		if expected == nil {
			expected = names
		} else {
			require.Equal(t, expected, names)
		}
	}
	require.Len(t, expected, 8)
}
