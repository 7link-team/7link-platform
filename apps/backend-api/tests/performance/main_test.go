package performance_test

import (
	"fmt"
	"os"
	"testing"

	"7link.com/platform/tests/internal/testdb"
)

// TestMain stops the PostgreSQL server the tests in this package share. The
// server outlives any one test by design -- starting one runs initdb, which
// costs far more than every migration these tests apply -- so nothing but the
// end of the binary can stop it. Exiting without this leaves a postgres
// process running and its data directory on disk.
func TestMain(m *testing.M) {
	code := m.Run()
	if err := testdb.ShutdownPostgres(); err != nil {
		fmt.Fprintln(os.Stderr, "shutdown postgres:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
