package secretx

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingFile struct {
	info      fs.FileInfo
	statErr   error
	readErr   error
	closeErr  error
	data      []byte
	wasClosed bool
}

func (f *failingFile) Read(buffer []byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	if len(f.data) == 0 {
		return 0, io.EOF
	}
	n := copy(buffer, f.data)
	f.data = f.data[n:]
	return n, nil
}
func (f *failingFile) Close() error {
	f.wasClosed = true
	return f.closeErr
}
func (f *failingFile) Stat() (fs.FileInfo, error) { return f.info, f.statErr }

type modeFileInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

func (i modeFileInfo) Mode() fs.FileMode { return i.mode }

func TestResolve(t *testing.T) {
	assert.Equal(t, "inline", mustResolve(t, "inline", ""))
	assert.Empty(t, mustResolve(t, "", ""))

	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	require.NoError(t, os.WriteFile(path, []byte("from-file\r\n"), 0o600))
	assert.Equal(t, "from-file", mustResolve(t, "", path))
	resolved, err := Resolve("test", "", path, 0)
	require.NoError(t, err)
	assert.Equal(t, "from-file", resolved)

	_, err = Resolve("test", "inline", path, 100)
	assert.ErrorContains(t, err, "mutually exclusive")
	_, err = Resolve("test", "", dir, 100)
	assert.ErrorContains(t, err, "not a regular file")
	_, err = Resolve("test", "", filepath.Join(dir, "missing"), 100)
	assert.ErrorContains(t, err, "read test_file")

	require.NoError(t, os.WriteFile(path, nil, 0o600))
	_, err = Resolve("test", "", path, 100)
	assert.ErrorContains(t, err, "empty")
	require.NoError(t, os.WriteFile(path, []byte("too-long"), 0o600))
	_, err = Resolve("test", "", path, 3)
	assert.ErrorContains(t, err, "exceeds")
}

func TestResolvePropagatesOpenedFileFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(path, []byte("secret"), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	statFailure := &failingFile{statErr: errors.New("stat failed")}
	_, err = resolve("test", "", path, 100, os.Stat, func(string) (secretFile, error) {
		return statFailure, nil
	})
	assert.ErrorContains(t, err, "stat test_file")
	assert.True(t, statFailure.wasClosed)

	readFailure := &failingFile{info: info, readErr: errors.New("read failed")}
	_, err = resolve("test", "", path, 100, os.Stat, func(string) (secretFile, error) {
		return readFailure, nil
	})
	assert.ErrorContains(t, err, "read test_file")
	assert.True(t, readFailure.wasClosed)

	closeFailure := &failingFile{info: info, data: []byte("secret"), closeErr: errors.New("close failed")}
	resolved, err := resolve("test", "", path, 100, os.Stat, func(string) (secretFile, error) {
		return closeFailure, nil
	})
	assert.Empty(t, resolved)
	assert.ErrorContains(t, err, "close test_file")
	assert.True(t, closeFailure.wasClosed)
}

func TestResolveRejectsNonRegularFileBeforeOpenAndAfterReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(path, []byte("secret"), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)
	nonRegular := modeFileInfo{FileInfo: info, mode: fs.ModeNamedPipe}

	opened := false
	_, err = resolve("test", "", path, 100, func(string) (fs.FileInfo, error) {
		return nonRegular, nil
	}, func(string) (secretFile, error) {
		opened = true
		return nil, errors.New("must not open")
	})
	assert.ErrorContains(t, err, "not a regular file")
	assert.False(t, opened, "FIFO and device paths must be rejected before a blocking open")

	replaced := &failingFile{info: nonRegular}
	_, err = resolve("test", "", path, 100, os.Stat, func(string) (secretFile, error) {
		return replaced, nil
	})
	assert.ErrorContains(t, err, "not a regular file")
	assert.True(t, replaced.wasClosed)
}

func mustResolve(t *testing.T, value, path string) string {
	t.Helper()
	resolved, err := Resolve("test", value, path, 100)
	require.NoError(t, err)
	return resolved
}
