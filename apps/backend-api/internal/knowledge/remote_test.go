package knowledge

import (
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"net/http"
	"strings"
	"testing"
)

type remoteTransport func(*http.Request) (*http.Response, error)

func (f remoteTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func remoteArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(gz)
	for name, body := range files {
		require.NoError(t, archive.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := io.WriteString(archive, body)
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	require.NoError(t, gz.Close())
	return buffer.Bytes()
}
func TestRemoteConnectorsUseConfiguredRepositoryCredentialsAndCanonicalDocs(t *testing.T) {
	connector, err := newRemoteConnector(RemoteConfig{Enabled: true, RepositoryHost: "git.example.test", RepositoryAPIBaseURL: "https://api.example.test", RepositoryToken: "deployment-token"})
	require.NoError(t, err)
	archive := remoteArchive(t, map[string]string{"repository-a/docs/setup.md": "Verified setup steps", "repository-a/README.md": "Read this first", "repository-a/src/main.go": "package hidden", "repository-a/docs/binary.png": "ignored"})
	requests := []*http.Request{}
	connector.client.Transport = remoteTransport(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request)
		data := []byte("<html><head><script>hidden secret</script></head><body><h1>Visible guide</h1><p>Use verified evidence.</p></body></html>")
		kind := "text/html"
		if strings.Contains(request.URL.Path, "/tarball") {
			data = archive
			kind = "application/gzip"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{kind}}, Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
	})
	text, err := connector.fetch(t.Context(), "URL", "https://docs.example.test/guide", "TEXT")
	require.NoError(t, err)
	require.Contains(t, text, "Visible guide")
	require.NotContains(t, text, "hidden secret")
	require.Empty(t, requests[0].Header.Get("Authorization"))
	text, err = connector.fetch(t.Context(), "REPOSITORY", "https://git.example.test/owner/project.git?ref=release%2Fstable", "MARKDOWN")
	require.NoError(t, err)
	require.Contains(t, text, "# README.md")
	require.Contains(t, text, "# docs/setup.md")
	require.NotContains(t, text, "package hidden")
	require.NotContains(t, text, "binary.png")
	require.Equal(t, "Bearer deployment-token", requests[1].Header.Get("Authorization"))
	require.Equal(t, "/repos/owner/project/tarball/release%2Fstable", requests[1].URL.EscapedPath())
	require.Less(t, strings.Index(text, "# README.md"), strings.Index(text, "# docs/setup.md"))
	_, err = connector.fetch(t.Context(), "REPOSITORY", "https://other.example.test/owner/project", "MARKDOWN")
	require.ErrorIs(t, err, ErrInvalid)
	require.Len(t, requests, 2)
	_, err = connector.fetch(t.Context(), "REPOSITORY", "https://git.example.test/owner/project?path=../secret", "MARKDOWN")
	require.ErrorIs(t, err, ErrInvalid)
	redirected, _ := http.NewRequest(http.MethodGet, "https://archive.example.test/repo.tar.gz", nil)
	redirected.Header.Set("Authorization", "Bearer deployment-token")
	require.NoError(t, connector.client.CheckRedirect(redirected, []*http.Request{requests[1]}))
	require.Empty(t, redirected.Header.Get("Authorization"))
}
func TestRemoteSSRFBoundsAndUnavailableCapabilities(t *testing.T) {
	disabled, err := newRemoteConnector(RemoteConfig{})
	require.NoError(t, err)
	require.False(t, disabled.supports("URL"))
	_, err = disabled.fetch(t.Context(), "URL", "https://docs.example.test", "TEXT")
	require.ErrorIs(t, err, ErrUnavailable)
	for _, config := range []RemoteConfig{{Enabled: true, RepositoryHost: "git.example.test"}, {Enabled: true, RepositoryHost: "git.example.test", RepositoryAPIBaseURL: "http://api.example.test"}, {Enabled: true, RepositoryHost: "git.example.test", RepositoryAPIBaseURL: "https://127.0.0.1"}} {
		_, err = newRemoteConnector(config)
		require.ErrorIs(t, err, ErrInvalid)
	}
	connector, err := newRemoteConnector(RemoteConfig{Enabled: true})
	require.NoError(t, err)
	require.True(t, connector.supports("URL"))
	require.False(t, connector.supports("REPOSITORY"))
	for _, locator := range []string{"http://example.test", "https://127.0.0.1", "https://169.254.169.254/latest/meta-data", "https://[::1]/", "file:///etc/passwd", "https://user:password@example.test/guide", "https://example.test/guide#fragment"} {
		_, err = connector.fetch(t.Context(), "URL", locator, "TEXT")
		require.ErrorIs(t, err, ErrInvalid, locator)
	}
	connector.client.Transport = remoteTransport(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", remoteContentLimit+1))), Request: request}, nil
	})
	_, err = connector.fetch(t.Context(), "URL", "https://docs.example.test/large", "TEXT")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = repositoryText(remoteArchive(t, map[string]string{"root/../outside.md": "no"}), "")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = repositoryText(remoteArchive(t, map[string]string{"root/docs/a.md": strings.Repeat("x", (1<<20)+1)}), "")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = repositoryText([]byte("not gzip"), "")
	require.ErrorIs(t, err, ErrInvalid)
}
func TestRemoteSyncIndexesActualFetchedTextAndRechecksAuthority(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	connector, err := newRemoteConnector(RemoteConfig{Enabled: true})
	require.NoError(t, err)
	m.remote = connector
	calls := 0
	body := "# Downloaded source\n\nActual remote evidence content."
	connector.client.Transport = remoteTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/markdown"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	var source SourceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		source, e = m.bindSourceOn(knowledgeContext(t, 2), tx, 1, SourceInput{Scope: Scope{Scope: "TEAM"}, SourceKind: "URL", Title: "Remote guide", Locator: "https://docs.example.test/guide", Format: "MARKDOWN"})
		return e
	}))
	require.Equal(t, "PENDING", source.State)
	require.Zero(t, calls)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		source, e = m.syncSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, SourceSyncInput{Version: source.Version}, false)
		return e
	}))
	require.Equal(t, "INDEXED", source.State)
	require.Equal(t, 1, calls)
	require.NotEmpty(t, source.ContentHash)
	var original guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.sourceChunksOn(knowledgeContext(t, 2), tx, 1, source.ID, 0, 50)
		require.NotEmpty(t, page.Items)
		require.Contains(t, page.Items[0].Content, "Actual remote evidence")
		original = page.Items[0].ID
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		source, e = m.syncSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, SourceSyncInput{Version: source.Version}, false)
		return e
	}))
	require.Equal(t, 2, calls)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.sourceChunksOn(knowledgeContext(t, 2), tx, 1, source.ID, 0, 50)
		require.NoError(t, e)
		require.Equal(t, original, page.Items[0].ID)
		settings, e := m.settingsOn(knowledgeContext(t, 2), tx, 1)
		require.True(t, settings.URLSyncAvailable)
		require.False(t, settings.RepositorySyncAvailable)
		return e
	}))
	m.tenantPolicy = core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	})
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.syncSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, SourceSyncInput{Version: source.Version}, false)
		return e
	}))
	require.Equal(t, 2, calls)
}

func TestRemoteDocumentFailuresNeverExposeUpstreamBodiesAndRejectBinaryText(t *testing.T) {
	connector, e := newRemoteConnector(RemoteConfig{Enabled: true, RepositoryHost: "git.example.test", RepositoryAPIBaseURL: "https://api.example.test"})
	require.NoError(t, e)
	for _, tc := range []struct {
		status     int
		kind, body string
		want       error
	}{{401, "text/plain", "upstream private detail", ErrRemoteAccess}, {404, "text/plain", "missing private detail", ErrRemoteMissing}, {500, "text/plain", "SQL private detail", ErrUnavailable}, {200, "application/octet-stream", "binary", ErrInvalid}, {200, "text/plain", "bad\x00bytes", ErrInvalid}, {200, "text/plain", string([]byte{0xff}), ErrInvalid}} {
		connector.client.Transport = remoteTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{tc.kind}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
		})
		text, e := connector.fetch(t.Context(), "URL", "https://docs.example.test/guide", "TEXT")
		require.Empty(t, text)
		require.ErrorIs(t, e, tc.want)
		require.NotContains(t, e.Error(), "private detail")
	}
	connector.client.Transport = remoteTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("upstream private detail") })
	_, e = connector.fetch(t.Context(), "URL", "https://docs.example.test/guide", "TEXT")
	require.ErrorIs(t, e, ErrUnavailable)
	for _, locator := range []string{"https://git.example.test/owner/project?unknown=yes", "https://git.example.test/owner/.git", "https://git.example.test/owner/project/extra"} {
		_, e = connector.fetch(t.Context(), "REPOSITORY", locator, "MARKDOWN")
		require.ErrorIs(t, e, ErrInvalid)
	}
	_, e = repositoryText([]byte("not gzip"), "")
	require.ErrorIs(t, e, ErrInvalid)
	_, e = repositoryText(remoteArchive(t, map[string]string{"root/README.md": "visible", "root/docs/data.md": "bad\x00bytes"}), "docs")
	require.ErrorIs(t, e, ErrInvalid)
	text, e := repositoryText(remoteArchive(t, map[string]string{"root/README.md": "excluded", "root/src/main.go": "package canonical"}), "src")
	require.NoError(t, e)
	require.Contains(t, text, "package canonical")
	require.NotContains(t, text, "excluded")
	connector.client.Transport = remoteTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/custom"}}, Body: io.NopCloser(strings.NewReader("visible custom text")), Request: r}, nil
	})
	text, e = connector.fetch(t.Context(), "URL", "https://docs.example.test/guide", "TEXT")
	require.NoError(t, e)
	require.Equal(t, "visible custom text", text)
	_, e = remoteHTMLText([]byte("<script>hidden</script>"))
	require.ErrorIs(t, e, ErrInvalid)
}

func TestRepositoryArchivesRejectTruncationTraversalAndDocumentFloods(t *testing.T) {
	var broken bytes.Buffer
	gz := gzip.NewWriter(&broken)
	_, err := gz.Write([]byte("broken tar header"))
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	_, err = repositoryText(broken.Bytes(), "")
	require.ErrorIs(t, err, ErrInvalid)
	for _, name := range []string{"/root/docs/a.md", "root/docs/../a.md", "root\\docs\\a.md"} {
		_, err = repositoryText(remoteArchive(t, map[string]string{name: "unsafe"}), "")
		require.ErrorIs(t, err, ErrInvalid, name)
	}
	_, err = repositoryText(remoteArchive(t, map[string]string{"README.md": "no repository root", "root/src/a.go": "not selected"}), "")
	require.ErrorIs(t, err, ErrInvalid)
	files := map[string]string{}
	for i := 0; i < 201; i++ {
		files["root/docs/"+strings.Repeat("a", i+1)+".md"] = "document"
	}
	_, err = repositoryText(remoteArchive(t, files), "")
	require.ErrorIs(t, err, ErrInvalid)
	var large bytes.Buffer
	gz = gzip.NewWriter(&large)
	archive := tar.NewWriter(gz)
	require.NoError(t, archive.WriteHeader(&tar.Header{Name: "root/docs/large.md", Mode: 0600, Size: 65 << 20, Typeflag: tar.TypeReg}))
	require.NoError(t, gz.Close())
	_, err = repositoryText(large.Bytes(), "")
	require.ErrorIs(t, err, ErrInvalid)
}
