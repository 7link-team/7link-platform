package lifecycle_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

const (
	lifecycleDatabaseType = "IAM_LIFECYCLE_DATABASE_TYPE"
	lifecycleDatabaseDSN  = "IAM_LIFECYCLE_DATABASE_DSN"
	lifecycleTablePrefix  = "IAM_LIFECYCLE_TABLE_PREFIX"
)

type lifecycleEngine struct {
	name string
	open func(*testing.T) string
}

func TestIAMLifecycleAcrossEnginesAndPrefixes(t *testing.T) {
	engines := []lifecycleEngine{
		{name: "postgres", open: func(t *testing.T) string {
			_, dsn := testdb.OpenPostgresWithDSN(t)
			return dsn
		}},
		{name: "mysql", open: func(t *testing.T) string {
			_, dsn := testdb.OpenMySQLWithDSN(t)
			return dsn
		}},
		{name: "sqlite", open: func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "iam-lifecycle.sqlite")
		}},
	}
	backendRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	for _, engine := range engines {
		for _, prefix := range []string{"", "sl_"} {
			name := "plain"
			if prefix != "" {
				name = "prefixed"
			}
			t.Run(engine.name+"/"+name, func(t *testing.T) {
				dsn := engine.open(t)
				command := exec.CommandContext(
					t.Context(), "go", "test", "7link.com/platform/internal/app",
					"-run", "^TestIAMLifecycleExternalDatabase$", "-count=1",
				)
				command.Dir = backendRoot
				command.Env = append(os.Environ(),
					lifecycleDatabaseType+"="+engine.name,
					lifecycleDatabaseDSN+"="+dsn,
					lifecycleTablePrefix+"="+prefix,
				)
				output, runErr := command.CombinedOutput()
				require.NoError(t, runErr, string(output))
			})
		}
	}
}
