package gen

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueryDirUsesModuleSourceInsteadOfWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	for _, modulePath := range []string{"governance", filepath.Join("iam", "core"), "todo"} {
		source := filepath.Join(root, "internal", modulePath, "dbgen", "main.go")
		want := filepath.Join(root, "internal", modulePath, "query")
		for _, cwd := range []string{root, t.TempDir()} {
			t.Chdir(cwd)
			got, err := queryDir(source)
			require.NoError(t, err)
			require.Equal(t, want, got, "source=%s cwd=%s", source, cwd)
		}
	}
}

func TestQueryDirRejectsUnanchoredSources(t *testing.T) {
	for _, source := range []string{"", filepath.Join("dbgen", "main.go"), filepath.Join(t.TempDir(), "main.go")} {
		got, err := queryDir(source)
		require.Error(t, err, "source=%s", source)
		require.Empty(t, got)
	}
}
