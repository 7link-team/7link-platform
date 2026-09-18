package files

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"net/http"
	"strings"
	"testing"
)

func TestFourRequiredDigestHeaders(t *testing.T) {
	expected := NewChecksums().Digests()
	headers := http.Header{}
	headers.Set("X-File-MD5", strings.ToUpper(expected.MD5))
	headers.Set("X-File-SHA1", expected.SHA1)
	headers.Set("X-File-SHA256", expected.SHA256)
	headers.Set("X-File-SHA512", expected.SHA512)
	actual, err := ParseDigestHeaders(headers)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	for _, name := range []string{"X-File-MD5", "X-File-SHA1", "X-File-SHA256", "X-File-SHA512"} {
		missing := headers.Clone()
		missing.Del(name)
		_, err := ParseDigestHeaders(missing)
		require.ErrorIs(t, err, ErrDigestHeaders)
		duplicate := headers.Clone()
		duplicate.Add(name, headers.Get(name))
		_, err = ParseDigestHeaders(duplicate)
		require.ErrorIs(t, err, ErrDigestHeaders)
	}
	invalid := headers.Clone()
	invalid.Set("X-File-MD5", strings.Repeat("g", 32))
	_, err = ParseDigestHeaders(invalid)
	require.ErrorIs(t, err, ErrDigestHeaders)
}

func TestFourDigestRestartAtEveryBoundary(t *testing.T) {
	data := bytes.Repeat([]byte("four-checksum-resume"), 73)
	full := NewChecksums()
	_, err := full.Write(data)
	require.NoError(t, err)
	for offset := 0; offset <= len(data); offset++ {
		partial := NewChecksums()
		_, err := partial.Write(data[:offset])
		require.NoError(t, err)
		state, err := partial.Checkpoint()
		require.NoError(t, err)
		require.Less(t, len(state), 8192)
		restored, err := RestoreChecksums(state, int64(offset))
		require.NoError(t, err)
		_, err = restored.Write(data[offset:])
		require.NoError(t, err)
		require.Equal(t, full.Digests(), restored.Digests())
		require.Equal(t, int64(len(data)), restored.Offset())
	}
}

func TestRejectCorruptAndStaleChecksumState(t *testing.T) {
	c := NewChecksums()
	_, err := c.Write([]byte("abc"))
	require.NoError(t, err)
	state, err := c.Checkpoint()
	require.NoError(t, err)
	for _, raw := range [][]byte{nil, []byte("{}"), bytes.Replace(state, []byte(`"offset":3`), []byte(`"offset":4`), 1), bytes.Repeat([]byte("x"), 8193)} {
		_, err := RestoreChecksums(raw, 3)
		require.ErrorIs(t, err, ErrCheckpoint)
	}
	_, err = RestoreChecksums(state, 2)
	require.ErrorIs(t, err, ErrCheckpoint)
}
