package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalImmutableStreamAndFailures(t *testing.T) {
	root := t.TempDir()
	driver, err := NewLocal(root)
	require.NoError(t, err)
	uri, err := driver.Put(t.Context(), "objects/1", bytes.NewBufferString("abc"), 3)
	require.NoError(t, err)
	require.Contains(t, uri, "file:///")
	location, err := url.Parse(uri)
	require.NoError(t, err)
	require.Empty(t, location.Host, "a Windows drive letter must not become a URI host")
	require.True(t, strings.HasSuffix(location.Path, "/objects/1"))
	reader, err := driver.Open(t.Context(), "objects/1")
	require.NoError(t, err)
	content, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "abc", string(content))
	rangeReader, err := driver.OpenRange(t.Context(), "objects/1", 1, 2)
	require.NoError(t, err)
	part, err := io.ReadAll(rangeReader)
	require.NoError(t, err)
	require.NoError(t, rangeReader.Close())
	require.Equal(t, "bc", string(part))
	_, err = driver.OpenRange(t.Context(), "objects/1", 2, 2)
	require.ErrorIs(t, err, ErrRange)
	_, err = driver.Put(t.Context(), "objects/1", bytes.NewBufferString("new"), 3)
	require.ErrorIs(t, err, os.ErrExist)
	require.ErrorIs(t, err, ErrExists)
	for _, item := range []struct {
		key, body string
		size      int64
	}{
		{"objects/short", "a", 2}, {"objects/long", "abc", 2},
	} {
		_, err := driver.Put(t.Context(), item.key, bytes.NewBufferString(item.body), item.size)
		require.ErrorIs(t, err, ErrLength)
		_, err = driver.Open(t.Context(), item.key)
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorIs(t, err, ErrNotFound)
	}
	_, err = driver.Put(t.Context(), "objects/empty", bytes.NewReader(nil), 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = driver.Put(ctx, "objects/canceled", bytes.NewReader(nil), 0)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, driver.Delete(t.Context(), "objects/1"))
	require.NoError(t, driver.Delete(t.Context(), "objects/1"))
}

func TestLocalRejectsTraversalAndEscapingSymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	driver, err := NewLocal(root)
	require.NoError(t, err)
	for _, key := range []string{"", "../evil", "/evil", "a//b", "a/../b", `a\b`, "a:b"} {
		_, err := driver.Put(t.Context(), key, bytes.NewReader(nil), 0)
		require.ErrorIs(t, err, ErrInvalidKey)
		require.ErrorIs(t, driver.Delete(t.Context(), key), ErrInvalidKey)
	}
	link := filepath.Join(root, "escape")
	if runtime.GOOS == "windows" {
		// Junctions exercise the same root-escape boundary without requiring the
		// Windows symbolic-link privilege or skipping the security assertion.
		output, err := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", link, outside).CombinedOutput()
		require.NoError(t, err, string(output))
	} else {
		require.NoError(t, os.Symlink(outside, link))
	}
	_, err = driver.Put(t.Context(), "escape/evil", bytes.NewBufferString("abc"), 3)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(outside, "evil"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRegistryExtensionAndDuplicateProtection(t *testing.T) {
	r := NewRegistry()
	factory := func(c DriverConfig) (Driver, error) { return NewLocal(c["root"]) }
	require.NoError(t, r.Register("local", factory))
	require.Error(t, r.Register("local", factory))
	require.Error(t, r.Register("BAD", factory))
	require.Error(t, r.Register("nil", nil))
	_, err := r.Build("oss-native", nil)
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = r.Build("local", nil)
	require.Error(t, err)
	driver, err := r.Build("local", DriverConfig{"root": t.TempDir()})
	require.NoError(t, err)
	require.False(t, driver.Capabilities().Multipart)
	require.NoError(t, r.Register("broken", func(DriverConfig) (Driver, error) { return nil, nil }))
	_, err = r.Build("broken", nil)
	require.Error(t, err)
	require.NoError(t, r.Register("failure", func(DriverConfig) (Driver, error) { return nil, errors.New("factory failed") }))
	_, err = r.Build("failure", nil)
	require.ErrorContains(t, err, "factory failed")
}

type capabilityClaim struct {
	Driver
	claimed Capabilities
}

func (d capabilityClaim) Capabilities() Capabilities { return d.claimed }

func TestRegistryRejectsUnimplementedCapabilityClaims(t *testing.T) {
	local, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	for name, caps := range map[string]Capabilities{"range": {RangeRead: true}, "multipart": {Multipart: true}, "copy": {ServerCopy: true}} {
		r := NewRegistry()
		require.NoError(t, r.Register(name, func(DriverConfig) (Driver, error) { return capabilityClaim{local, caps}, nil }))
		_, err := r.Build(name, nil)
		require.ErrorContains(t, err, "no implementation")
	}
}

func TestLocalRangeCancellationAndReaderFailure(t *testing.T) {
	driver, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	_, err = driver.Put(t.Context(), "objects/1", bytes.NewBufferString("abc"), 3)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	reader, err := driver.Open(ctx, "objects/1")
	require.NoError(t, err)
	cancel()
	_, err = io.ReadAll(reader)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, reader.Close())
	_, err = driver.OpenRange(ctx, "objects/1", 0, 1)
	require.ErrorIs(t, err, context.Canceled)
	_, err = driver.OpenRange(t.Context(), "../escape", 0, 1)
	require.ErrorIs(t, err, ErrInvalidKey)
	_, err = driver.OpenRange(t.Context(), "objects/1", -1, 1)
	require.ErrorIs(t, err, ErrRange)
	_, err = driver.OpenRange(t.Context(), "objects/missing", 0, 1)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = driver.Put(t.Context(), "objects/failure", failedReader{}, 1)
	require.ErrorContains(t, err, "read failure")
	_, err = driver.Open(t.Context(), "objects/failure")
	require.ErrorIs(t, err, os.ErrNotExist)
}

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, errors.New("read failure") }

func TestLocalFileURIUsesPathNotDriveHost(t *testing.T) {
	for _, item := range []struct{ filename, uri string }{
		{"/store/objects/a b", "file:///store/objects/a%20b"},
		{"C:/store/objects/a b", "file:///C:/store/objects/a%20b"},
		{"//server/share/a b", "file://server/share/a%20b"},
	} {
		location := localFileURI(item.filename)
		require.Equal(t, item.uri, location.String())
	}
}

func TestLocalBrokenRootNeverReadsSourceOrReportsSuccess(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(filename, []byte("root obstacle"), 0600))
	driver, err := NewLocal(filename)
	require.NoError(t, err)
	_, err = driver.Put(t.Context(), "objects/1", failedReader{}, 1)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "read failure", "bad storage root must fail before consuming content")
	_, err = driver.Open(t.Context(), "objects/1")
	require.Error(t, err)
	_, err = driver.OpenRange(t.Context(), "objects/1", 0, 1)
	require.Error(t, err)
	require.Error(t, driver.Delete(t.Context(), "objects/1"))
	require.Error(t, driver.ReclaimWrite(t.Context(), "objects/1"))
	_, err = driver.Open(t.Context(), "../escape")
	require.ErrorIs(t, err, ErrInvalidKey)
	cancelledContext, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, driver.Delete(cancelledContext, "objects/1"), context.Canceled)
	require.ErrorIs(t, driver.ReclaimWrite(cancelledContext, "objects/1"), context.Canceled)
	require.ErrorIs(t, driver.ReclaimWrite(t.Context(), "../escape"), ErrInvalidKey)
}
