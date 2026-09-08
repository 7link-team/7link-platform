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

func TestPrincipalErasureOnPostgres(t *testing.T) {
	_, dsn := testdb.OpenPostgresWithDSN(t)
	backendRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	command := exec.CommandContext(t.Context(), "go", "test", "7link.com/platform/internal/app",
		"-run", "^(TestComposedPrincipalErasureExternalDatabase|TestPrincipalErasureConcurrencyExternalDatabase)$", "-count=1", "-v")
	command.Dir = backendRoot
	command.Env = append(os.Environ(), lifecycleDatabaseType+"=postgres", lifecycleDatabaseDSN+"="+dsn)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	t.Log(string(output))
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
				if engine.name == "mysql" {
					// go-mysql-server/memory explicitly does not support concurrent
					// transactions. test-mysql8.sh runs the identical replay test
					// on real InnoDB instead of treating this emulator as evidence.
					t.Log("replay concurrency is covered by the required real MySQL 8 gate")
					return
				}
				replay := exec.CommandContext(t.Context(), "go", "test", "7link.com/platform/internal/iam/federation",
					"-run", "^TestReplayRegistryExternalDatabase$", "-count=1")
				replay.Dir, replay.Env = command.Dir, command.Env
				output, runErr = replay.CombinedOutput()
				require.NoError(t, runErr, string(output))
				archives := exec.CommandContext(t.Context(), "go", "test", "7link.com/platform/internal/iam/audit",
					"-run", "^TestArchiveExternalDatabase$", "-count=1")
				archives.Dir, archives.Env = command.Dir, command.Env
				output, runErr = archives.CombinedOutput()
				require.NoError(t, runErr, string(output))
			})
		}
	}
}
