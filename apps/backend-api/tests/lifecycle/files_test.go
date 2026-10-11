package lifecycle_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

// TestFileSpaceInitializationOnPostgres runs the files module's concurrent
// first-use test against a real PostgreSQL server, where concurrent
// transactions really interleave.
func TestFileSpaceInitializationOnPostgres(t *testing.T) {
	_, dsn := testdb.OpenPostgresWithDSN(t)
	backendRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	command := exec.CommandContext(t.Context(), "go", "test", "7link.com/platform/internal/files",
		"-run", "^TestSpaceInitializationConcurrencyExternalDatabase$", "-count=1", "-v")
	command.Dir = backendRoot
	command.Env = append(os.Environ(), lifecycleDatabaseType+"=postgres", lifecycleDatabaseDSN+"="+dsn)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "--- PASS: TestSpaceInitializationConcurrencyExternalDatabase")
	t.Log(string(output))
}
