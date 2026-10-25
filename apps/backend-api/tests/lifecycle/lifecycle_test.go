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
	lifecycleMemoryMySQL  = "IAM_LIFECYCLE_MEMORY_MYSQL"
)

type lifecycleEngine struct {
	name string
	open func(*testing.T) string
}

func TestAccountViewOnPostgres(t *testing.T) {
	_, dsn := testdb.OpenPostgresWithDSN(t)
	backendRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	for _, prefix := range []string{"", "av_"} {
		name := "plain"
		if prefix != "" {
			name = "prefixed"
		}
		t.Run(name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), "go", "test", "7link.com/platform/internal/integration/iam",
				"-run", "^TestAccountViewExternalDatabase$", "-count=1", "-v")
			command.Dir = backendRoot
			command.Env = append(os.Environ(), lifecycleDatabaseType+"=postgres", lifecycleDatabaseDSN+"="+dsn, lifecycleTablePrefix+"="+prefix)
			output, err := command.CombinedOutput()
			require.NoError(t, err, string(output))
			require.Contains(t, string(output), "--- PASS: TestAccountViewExternalDatabase")
			t.Log(string(output))
		})
	}
}

func TestGrantabilityOnPostgres(t *testing.T) {
	_, dsn := testdb.OpenPostgresWithDSN(t)
	backendRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	for _, prefix := range []string{"", "grant_"} {
		name := "plain"
		if prefix != "" {
			name = "prefixed"
		}
		t.Run(name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), "go", "test", "7link.com/platform/internal/integration/iam", "-run", "^TestGrantabilityExternalDatabase$", "-count=1", "-v")
			command.Dir = backendRoot
			command.Env = append(os.Environ(), lifecycleDatabaseType+"=postgres", lifecycleDatabaseDSN+"="+dsn, lifecycleTablePrefix+"="+prefix)
			output, err := command.CombinedOutput()
			require.NoError(t, err, string(output))
			require.Contains(t, string(output), "--- PASS: TestGrantabilityExternalDatabase")
			t.Log(string(output))
		})
	}
}

func TestPrincipalErasureOnPostgres(t *testing.T) {
	_, dsn := testdb.OpenPostgresWithDSN(t)
	backendRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	command := exec.CommandContext(t.Context(), "go", "test", "7link.com/platform/internal/integration/iam",
		"-run", "^(TestComposedPrincipalErasureExternalDatabase|TestPrincipalErasureConcurrencyExternalDatabase)$", "-count=1", "-v")
	command.Dir = backendRoot
	command.Env = append(os.Environ(), lifecycleDatabaseType+"=postgres", lifecycleDatabaseDSN+"="+dsn)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "--- PASS: TestComposedPrincipalErasureExternalDatabase")
	require.Contains(t, string(output), "--- PASS: TestPrincipalErasureConcurrencyExternalDatabase")
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
					t.Context(), "go", "test", "7link.com/platform/internal/integration/iam",
					"-run", "^TestIAMLifecycleExternalDatabase$", "-count=1", "-v",
				)
				command.Dir = backendRoot
				command.Env = append(os.Environ(),
					lifecycleDatabaseType+"="+engine.name,
					lifecycleDatabaseDSN+"="+dsn,
					lifecycleTablePrefix+"="+prefix,
				)
				if engine.name == "mysql" {
					// This marker applies only to the memory emulator. The real
					// MySQL gate uses the default concurrent connection pool.
					command.Env = append(command.Env, lifecycleMemoryMySQL+"=true")
				}
				output, runErr := command.CombinedOutput()
				require.NoError(t, runErr, string(output))
				require.Contains(t, string(output), "--- PASS: TestIAMLifecycleExternalDatabase")
				if engine.name == "mysql" {
					// go-mysql-server/memory explicitly does not support concurrent
					// transactions. test-mysql8.sh runs the identical replay test
					// on real InnoDB instead of treating this emulator as evidence.
					t.Log("replay concurrency is covered by the required real MySQL 8 gate")
					return
				}
				replay := exec.CommandContext(t.Context(), "go", "test", "7link.com/platform/internal/iam/federation",
					"-run", "^TestReplayRegistryExternalDatabase$", "-count=1", "-v")
				replay.Dir, replay.Env = command.Dir, command.Env
				output, runErr = replay.CombinedOutput()
				require.NoError(t, runErr, string(output))
				require.Contains(t, string(output), "--- PASS: TestReplayRegistryExternalDatabase")
				archives := exec.CommandContext(t.Context(), "go", "test", "7link.com/platform/internal/iam/audit",
					"-run", "^TestArchiveExternalDatabase$", "-count=1", "-v")
				archives.Dir, archives.Env = command.Dir, command.Env
				output, runErr = archives.CombinedOutput()
				require.NoError(t, runErr, string(output))
				require.Contains(t, string(output), "--- PASS: TestArchiveExternalDatabase")
			})
		}
	}
}
