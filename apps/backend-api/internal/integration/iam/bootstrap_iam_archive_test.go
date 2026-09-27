package iamintegration

import (
	"7link.com/platform/internal/app"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposedArchiveBrowserAcceptance(t *testing.T) {
	endpoint := os.Getenv("IAM_ARCHIVE_MINIO_ENDPOINT")
	if os.Getenv("IAM_ARCHIVE_BROWSER") != "1" {
		t.Skip("set IAM_ARCHIVE_BROWSER=1 and isolated MinIO credentials for real browser acceptance")
	}
	require.NotEmpty(t, endpoint)
	access, secret := os.Getenv("IAM_ARCHIVE_MINIO_USER"), os.Getenv("IAM_ARCHIVE_MINIO_PASSWORD")
	require.NotEmpty(t, access)
	require.NotEmpty(t, secret)
	backend, err := filepath.Abs("../../..")
	require.NoError(t, err)
	console := filepath.Join(backend, "..", "frontend-web-admin", "apps", "iam")
	database := filepath.Join(t.TempDir(), "archive-browser.sqlite")
	seed := exec.CommandContext(t.Context(), "go", "test", "./internal/iam/audit", "-run", "^TestArchiveBrowserSeed$", "-count=1")
	seed.Dir = backend
	seed.Env = append(os.Environ(), "IAM_ARCHIVE_BROWSER_DATABASE="+database)
	output, err := seed.CombinedOutput()
	require.NoError(t, err, "%s", output)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	_, port, err := net.SplitHostPort(address)
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	consoleURL := "http://" + address
	target, err := url.Parse(endpoint)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var available atomic.Bool
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available.Load() {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>isolated outage fixture</Message></Error>`)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(storage.Close)
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/restore-storage" {
			http.NotFound(w, r)
			return
		}
		available.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(control.Close)
	fixture := newIAMTenantFixtureWithDatabase(t, app.Database{Type: "sqlite", Dsn: database, Writable: true, Readable: true}, "", func(config *app.AppConfig) {
		config.Authn.Web.CookieSecure = false
		config.Authn.Web.PostLoginURL = consoleURL + "/"
		config.Authn.Web.AllowedReturnURLs = []string{consoleURL + "/"}
		config.Authn.Web.AllowedOrigins = []string{consoleURL}
		config.IAMAudit = app.IAMAudit{
			Anchor:  app.IAMAuditAnchor{Enabled: true, Endpoint: storage.URL, Bucket: "audit-browser-" + time.Now().UTC().Format("20060102-150405.000000000"), AccessKey: access, SecretKey: secret, RetentionDays: 1, SigningKey: base64.StdEncoding.EncodeToString(make([]byte, 32))},
			Archive: app.IAMAuditArchive{Enabled: true, Interval: 15 * time.Second, Timeout: time.Minute},
		}
	})
	require.Eventually(t, func() bool {
		return fixture.count(t, "iam_audit_archive_jobs", "state = 'failed'") == 3
	}, 10*time.Second, 50*time.Millisecond)
	runGovernanceBrowser(t, fixture, console, consoleURL, port, control.URL, false)
	assert.EqualValues(t, 4, fixture.count(t, "iam_audit_archive_jobs", "state = 'complete'"))
	assert.Zero(t, fixture.count(t, "iam_audit_archive_jobs", "state <> 'complete'"))
	assert.EqualValues(t, 504, fixture.count(t, "iam_audit_events", "event_type = 'archive_browser_fixture'"))
}

func TestComposedGovernanceDisabledBrowserAcceptance(t *testing.T) {
	if os.Getenv("IAM_ARCHIVE_BROWSER") != "1" {
		t.Skip("set IAM_ARCHIVE_BROWSER=1 for isolated browser acceptance")
	}
	backend, err := filepath.Abs("../../..")
	require.NoError(t, err)
	console := filepath.Join(backend, "..", "frontend-web-admin", "apps", "iam")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	_, port, err := net.SplitHostPort(address)
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	consoleURL := "http://" + address
	fixture := newIAMTenantFixtureWithDatabase(t, app.Database{Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "governance.sqlite"), Writable: true, Readable: true}, "", func(config *app.AppConfig) {
		config.Authn.Web.CookieSecure = false
		config.Authn.Web.PostLoginURL = consoleURL + "/"
		config.Authn.Web.AllowedReturnURLs = []string{consoleURL + "/"}
		config.Authn.Web.AllowedOrigins = []string{consoleURL}
	})
	runGovernanceBrowser(t, fixture, console, consoleURL, port, "", true)
	assert.Zero(t, fixture.count(t, "iam_audit_archive_jobs", "1 = 1"))
}

func runGovernanceBrowser(t *testing.T, fixture *iamTenantFixture, console, consoleURL, port, controlURL string, disabled bool) {
	t.Helper()
	frontend := exec.CommandContext(t.Context(), "bun", "--bun", filepath.Join(console, "node_modules", "vite", "bin", "vite.js"), "--host", "127.0.0.1", "--port", port, "--strictPort")
	frontend.Dir = console
	frontend.Env = append(os.Environ(), "VITE_API_PROXY_TARGET="+fixture.server.URL)
	frontendLog, err := os.CreateTemp(t.TempDir(), "frontend-*.log")
	require.NoError(t, err)
	frontend.Stdout, frontend.Stderr = frontendLog, frontendLog
	require.NoError(t, frontend.Start())
	t.Cleanup(func() {
		_ = frontend.Process.Kill()
		_ = frontend.Wait()
		_ = frontendLog.Close()
	})
	require.Eventually(t, func() bool {
		client := &http.Client{Timeout: time.Second}
		response, err := client.Get(consoleURL)
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, 15*time.Second, 100*time.Millisecond)
	browserCtx, cancelBrowser := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancelBrowser()
	browser := exec.CommandContext(browserCtx, "node", "scripts/archive-live-browser-audit.mjs")
	browser.Dir = console
	browser.Env = append(os.Environ(), "IAM_CONSOLE_URL="+consoleURL, "IAM_ARCHIVE_FIXTURE_CONTROL="+controlURL,
		fmt.Sprintf("IAM_GOVERNANCE_DISABLED=%t", disabled), "IAM_BROWSER_PASSWORD=correct horse battery staple")
	output, err := browser.CombinedOutput()
	require.NoError(t, err, "%s", output)
	t.Log(string(output))
}
