package performance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestIAMHotPathPerformanceOnPostgres(t *testing.T) {
	_, dsn := testdb.OpenPostgresWithDSN(t)
	backendRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	command := exec.CommandContext(
		t.Context(), "go", "test", "7link.com/platform/internal/app",
		"-run", "^TestIAMPerformanceExternalDatabase$", "-count=1", "-v",
	)
	command.Dir = backendRoot
	command.Env = append(os.Environ(),
		"IAM_PERFORMANCE_DATABASE_TYPE=postgres",
		"IAM_PERFORMANCE_DATABASE_DSN="+dsn,
	)
	output, runErr := command.CombinedOutput()
	require.NoError(t, runErr, string(output))
	t.Log(string(output))
}
