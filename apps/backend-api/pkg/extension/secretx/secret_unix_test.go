//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package secretx

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestResolveDoesNotBlockWhenRegularPathIsReplacedByFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(path, []byte("secret"), 0o600))

	result := make(chan error, 1)
	go func() {
		_, err := resolve("test", "test_file", "", path, 100, func(path string) (fs.FileInfo, error) {
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			if err := os.Remove(path); err != nil {
				return nil, err
			}
			if err := unix.Mkfifo(path, 0o600); err != nil {
				return nil, err
			}
			return info, nil
		}, openFile)
		result <- err
	}()

	select {
	case err := <-result:
		assert.ErrorContains(t, err, "not a regular file")
	case <-time.After(2 * time.Second):
		t.Fatal("Resolve blocked while opening a FIFO that replaced the checked regular file")
	}
}

func TestResolveFollowsSymlinkToRegularSecret(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "secret")
	require.NoError(t, os.WriteFile(target, []byte("from-symlink\n"), 0o600))
	require.NoError(t, os.Symlink(target, link))

	resolved, err := Resolve("test", "", link, 100)
	require.NoError(t, err)
	assert.Equal(t, "from-symlink", resolved)
}
