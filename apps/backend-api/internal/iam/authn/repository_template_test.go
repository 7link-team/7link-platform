package authn

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"7link.com/platform/pkg/extension/passwordx"
	"github.com/stretchr/testify/require"
)

// Building the schema is the same work every time, and goose running identity's
// migrations and this module's twenty took 55 ms of every test in the package.
// It is done once per prefix and copied after that: a fresh file somebody else
// migrated is indistinguishable from one migrated here, and costs about a
// millisecond. Hashing a fixture's password is the same kind of waste --
// argon2id costs 31 ms by design, and a fixture needs a hash that verifies, not
// a fresh salt.
//
// TestMain owns the directory so a template outlives the test that built it and
// nothing is left in /tmp afterwards.

var (
	templateDirectory string
	templates         sync.Map
	fixtureHashes     sync.Map
)

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "authn-schema-templates")
	if err != nil {
		panic(err)
	}
	templateDirectory = directory
	code := m.Run()
	_ = os.RemoveAll(directory)
	os.Exit(code)
}

func authnSQLiteDSN(path string) string {
	return fmt.Sprintf("file:%s?_busy_timeout=10000&_journal_mode=WAL", filepath.ToSlash(path))
}

// templateDatabase returns the path of a migrated database for one prefix,
// building it on first use. The connection is closed before any copy is taken,
// which checkpoints the write-ahead log into the file being copied.
func templateDatabase(t *testing.T, prefix string) string {
	t.Helper()
	if cached, ok := templates.Load(prefix); ok {
		return cached.(string)
	}
	name := prefix
	if name == "" {
		name = "plain"
	}
	path := filepath.Join(templateDirectory, name+".sqlite")
	initializeAuthnRepositoryDatabase(t, authnSQLiteDSN(path), prefix)
	cached, _ := templates.LoadOrStore(prefix, path)
	return cached.(string)
}

func cloneTemplateDatabase(t *testing.T, prefix, destination string) {
	t.Helper()
	schema, err := os.ReadFile(templateDatabase(t, prefix))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(destination, schema, 0o600))
}

func hashFixturePassword(t *testing.T, password string) string {
	t.Helper()
	if cached, ok := fixtureHashes.Load(password); ok {
		return cached.(string)
	}
	hash, err := passwordx.Hash(password)
	require.NoError(t, err)
	cached, _ := fixtureHashes.LoadOrStore(password, hash)
	return cached.(string)
}
