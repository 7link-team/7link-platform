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
	_, err = resolve("test", "test_file", "", path, 100, os.Stat, func(string) (secretFile, error) {
		return statFailure, nil
	})
	assert.ErrorContains(t, err, "stat test_file")
	assert.True(t, statFailure.wasClosed)

	readFailure := &failingFile{info: info, readErr: errors.New("read failed")}
	_, err = resolve("test", "test_file", "", path, 100, os.Stat, func(string) (secretFile, error) {
		return readFailure, nil
	})
	assert.ErrorContains(t, err, "read test_file")
	assert.True(t, readFailure.wasClosed)

	closeFailure := &failingFile{info: info, data: []byte("secret"), closeErr: errors.New("close failed")}
	resolved, err := resolve("test", "test_file", "", path, 100, os.Stat, func(string) (secretFile, error) {
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
	_, err = resolve("test", "test_file", "", path, 100, func(string) (fs.FileInfo, error) {
		return nonRegular, nil
	}, func(string) (secretFile, error) {
		opened = true
		return nil, errors.New("must not open")
	})
	assert.ErrorContains(t, err, "not a regular file")
	assert.False(t, opened, "FIFO and device paths must be rejected before a blocking open")

	replaced := &failingFile{info: nonRegular}
	_, err = resolve("test", "test_file", "", path, 100, os.Stat, func(string) (secretFile, error) {
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

func TestResolveListTakesTheValuesThemselves(t *testing.T) {
	resolved, err := ResolveList("test", []string{"first", "second"}, nil, 100)
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "second"}, resolved)

	empty, err := ResolveList("test", nil, nil, 100)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestResolveListReadsEachFileInOrder(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 0, 2)
	for _, secret := range []string{"older", "oldest"} {
		path := filepath.Join(dir, secret)
		require.NoError(t, os.WriteFile(path, []byte(secret+"\n"), 0o600))
		paths = append(paths, path)
	}

	resolved, err := ResolveList("test", nil, paths, 100)
	require.NoError(t, err)
	assert.Equal(t, []string{"older", "oldest"}, resolved)
}

// TestResolveListRefusesHalfOfEachSource keeps a list from being assembled out
// of both sources, where the order it ends up in is not the order either half
// was written in.
func TestResolveListRefusesHalfOfEachSource(t *testing.T) {
	_, err := ResolveList("test", []string{"inline"}, []string{"/nowhere"}, 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test and test_files are mutually exclusive")
}

// TestResolveListSkipsAPlaceholderPath covers the entry a configuration
// template leaves behind, which asks for nothing and should not fail a start.
func TestResolveListSkipsAPlaceholderPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	require.NoError(t, os.WriteFile(path, []byte("kept"), 0o600))

	resolved, err := ResolveList("test", nil, []string{"", "   ", path}, 100)
	require.NoError(t, err)
	assert.Equal(t, []string{"kept"}, resolved)
}

// TestResolveListSaysWhichEntryItCouldNotRead reports the position rather than
// the contents, which is the part an operator can act on and the part that is
// safe to print.
func TestResolveListSaysWhichEntryItCouldNotRead(t *testing.T) {
	dir := t.TempDir()
	readable := filepath.Join(dir, "secret")
	require.NoError(t, os.WriteFile(readable, []byte("kept"), 0o600))

	_, err := ResolveList("test", nil, []string{readable, filepath.Join(dir, "absent")}, 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read test_files[2]")
	assert.NotContains(t, err.Error(), "kept")
}
