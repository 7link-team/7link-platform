package fleet

import (
	"7link.com/platform/internal/infra/guid"
	"bufio"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPreviewGatewayStartsRealTLSListenerAndStopsEveryConnection(t *testing.T) {
	m, _, _, _, _ := enrollmentHarness(t)
	require.NoError(t, m.Start(t.Context()))
	m.config.Preview.Domain = "preview.invalid"
	require.Error(t, m.Start(t.Context()), "configured domain requires TLS and listener")
	certificateSource := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := certificateSource.TLS.Certificates[0]
	client := certificateSource.Client()
	certificateSource.Close()
	privateKey, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	require.NoError(t, err)
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "certificate.pem"), filepath.Join(dir, "private-key.pem")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}), 0600))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	m.config.Preview.Listen = address
	m.config.Preview.TLS.CertFile = certPath
	m.config.Preview.TLS.KeyFile = keyPath
	m.previewBroker = newPreviewBroker(func() (int64, error) { return 42, nil }, func(context.Context, PreviewGrant) error { return nil })
	require.Error(t, m.Start(t.Context()), "occupied listener must reject startup")
	require.NoError(t, listener.Close())
	m.config.Preview.TLS.KeyFile = filepath.Join(dir, "missing.pem")
	require.Error(t, m.Start(t.Context()))
	m.config.Preview.TLS.KeyFile = keyPath
	m.previewAccess.Authorize = func(context.Context, string, string, string) (PreviewGrant, error) {
		return PreviewGrant{}, ErrMachineForbidden
	}
	require.NoError(t, m.Start(t.Context()))
	t.Cleanup(func() { require.NoError(t, m.Stop(context.Background())) })
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+address+"/", nil)
	require.NoError(t, err)
	request.Host = "unrelated.invalid"
	response, err := client.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	require.NoError(t, response.Body.Close())
	client.CloseIdleConnections()
	require.NoError(t, m.Stop(t.Context()))
	response, err = client.Get("https://" + address + "/")
	if response != nil {
		require.NoError(t, response.Body.Close())
	}
	require.Error(t, err, "stopped gateway must release listener")
}

func TestPreviewGatewayRejectsConsoleHostAndExchangesOnlyOnPreviewHost(t *testing.T) {
	const label = "abcdefghijklmnopqrstuvwxyz"
	exchanged := 0
	m := &Module{config: Config{Preview: PreviewConfig{Domain: "preview.example.net", ConsoleOrigin: "https://console.example.org"}}, previewBroker: newPreviewBroker(nil, nil)}
	m.previewAccess = PreviewAccessResolver{Authorize: func(context.Context, string, string, string) (PreviewGrant, error) {
		return PreviewGrant{}, ErrUnavailable
	}, ExchangeCode: func(_ context.Context, got, code string) (string, string, time.Time, error) {
		exchanged++
		require.Equal(t, label, got)
		require.Equal(t, "one-time", code)
		return "__Host-preview=secret; Secure; HttpOnly; Path=/", "/", time.Now(), nil
	}}
	handler := m.previewGatewayHandler()
	for _, host := range []string{"console.example.org", "preview.example.net", "evilpreview.example.net", label + ".preview.example.net.evil.test"} {
		req := httptest.NewRequest(http.MethodGet, "https://"+host+"/__access?code=one-time", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		require.Equal(t, 404, w.Code)
	}
	require.Zero(t, exchanged)
	req := httptest.NewRequest(http.MethodGet, "https://"+label+".preview.example.net/__access?code=one-time", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.Equal(t, 303, w.Code)
	require.Equal(t, 1, exchanged)
	require.Contains(t, w.Header().Get("Set-Cookie"), "HttpOnly")
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "/", w.Header().Get("Location"))
	m.config.Preview.Mode = "PATH"
	req = httptest.NewRequest(http.MethodGet, "https://preview.example.net/p/"+label+"/__access?code=one-time", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.Equal(t, 303, w.Code)
	require.Equal(t, "/p/"+label+"/", w.Header().Get("Location"))
	require.NotContains(t, w.Header().Get("Location"), "code=")
}

func TestPreviewSSEHeartbeatPreservesPartialEvents(t *testing.T) {
	source, writer := io.Pipe()
	body := previewSSEHeartbeat(t.Context(), source, 10*time.Millisecond)
	defer body.Close()
	read := make(chan string, 8)
	go func() {
		buffer := make([]byte, 1024)
		for {
			n, err := body.Read(buffer)
			if n > 0 {
				read <- string(buffer[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case got := <-read:
		require.Equal(t, ": heartbeat\n\n", got)
	case <-time.After(time.Second):
		t.Fatal("no heartbeat")
	}
	_, err := writer.Write([]byte("data: partial"))
	require.NoError(t, err)
	for {
		got := <-read
		if strings.Contains(got, "data:") {
			require.Equal(t, "data: partial", got)
			break
		}
	}
	select {
	case got := <-read:
		t.Fatalf("heartbeat corrupted partial event: %q", got)
	case <-time.After(30 * time.Millisecond):
	}
	_, err = writer.Write([]byte(" event\n\n"))
	require.NoError(t, err)
	require.Equal(t, " event\n\n", <-read)
	select {
	case got := <-read:
		require.Equal(t, ": heartbeat\n\n", got)
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not resume")
	}
	require.NoError(t, writer.Close())
}

func TestPreviewGatewayProxiesActualTunnelAndSeparatesPreviewCredentials(t *testing.T) {
	for _, contentType := range []string{"text/plain", "text/event-stream"} {
		t.Run(contentType, func(t *testing.T) {
			m, _, human, row, _ := terminalFixture(t)
			enabled := true
			_, e := m.SetTerminalAccessOptions(human, 7, 1, 1, TerminalAccessOptions{Enabled: true, AllowRemotePreview: &enabled}, "browser", "fresh")
			require.NoError(t, e)
			m.config.Preview = PreviewConfig{Domain: "preview.example.net", ConsoleOrigin: "https://console.example.org"}
			m.terminalBroker = newTerminalBroker()
			require.NoError(t, m.initializePreview(t.Context()))
			grant := PreviewGrant{TenantID: 7, MachineID: 1, RunnerSessionID: row.RunnerSessionID, ConnEpoch: row.ConnEpoch, ExecutionID: 91, Port: 3000, Cookie: "__Host-preview=renewed; Secure; HttpOnly; Path=/"}
			m.previewAccess.Authorize = func(_ context.Context, label, cookie, method string) (PreviewGrant, error) {
				if label != "abcdefghijklmnopqrstuvwxyz" || method != "GET" {
					return PreviewGrant{}, ErrMachineForbidden
				}
				return grant, nil
			}
			b := m.previewBroker
			c := &previewConnection{tenant: 7, machine: 1, session: row.RunnerSessionID, epoch: row.ConnEpoch, frames: make(chan previewFrame, 256), done: make(chan struct{}), streams: make(map[string]*previewStream), closed: make(map[string]bool), pending: make(chan struct{}, 256)}
			b.connections[[2]guid.ID{7, 1}] = c
			requests := make(chan *http.Request, 1)
			failed := make(chan error, 1)
			go func() {
				var id string
				var request string
				for {
					select {
					case frame := <-c.frames:
						switch frame.Kind {
						case "open":
							id = frame.ID
							if e := b.receive(t.Context(), c, previewFrame{Kind: "opened", ID: id}); e != nil {
								failed <- e
								return
							}
						case "data":
							request += string(frame.Data)
							if strings.Contains(request, "\r\n\r\n") {
								parsed, e := http.ReadRequest(bufio.NewReader(strings.NewReader(request)))
								if e != nil {
									failed <- e
									return
								}
								requests <- parsed
								payload := "tunnel response"
								if contentType == "text/event-stream" {
									payload = "data: response\n\n"
								}
								raw := "HTTP/1.1 200 OK\r\nContent-Type: " + contentType + "\r\nConnection: close\r\nSet-Cookie: __Host-preview=upstream-forgery; Secure; Path=/\r\nSet-Cookie: app_session=preserved; Path=/\r\n\r\n" + payload
								if e = b.receive(t.Context(), c, previewFrame{Kind: "data", ID: id, Data: []byte(raw)}); e != nil {
									failed <- e
									return
								}
								failed <- b.receive(t.Context(), c, previewFrame{Kind: "fin", ID: id})
								return
							}
						}
					case <-time.After(2 * time.Second):
						failed <- errors.New("gateway did not send request")
						return
					}
				}
			}()
			request := httptest.NewRequest(http.MethodGet, "https://abcdefghijklmnopqrstuvwxyz.preview.example.net/resource?q=1", nil)
			request.RemoteAddr = "192.0.2.8:1234"
			request.Header.Set("Cookie", "__Host-preview=browser-secret; __Secure-preview=other-secret; app_session=client")
			request.Header.Set("Forwarded", "for=forged")
			request.Header.Set("X-Forwarded-Host", "forged.test")
			request.Header.Set("X-Forwarded-For", "203.0.113.9")
			response := httptest.NewRecorder()
			m.previewGatewayHandler().ServeHTTP(response, request)
			require.Equal(t, 200, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "response")
			require.NoError(t, <-failed)
			actual := <-requests
			require.Equal(t, "/resource?q=1", actual.URL.String())
			require.Equal(t, "127.0.0.1:3000", actual.Host)
			require.NotContains(t, actual.Header.Get("Cookie"), "preview")
			require.Contains(t, actual.Header.Get("Cookie"), "app_session=client")
			require.Empty(t, actual.Header.Get("Forwarded"))
			require.Empty(t, actual.Header.Get("X-Forwarded-Host"))
			require.Equal(t, "192.0.2.8", actual.Header.Get("X-Forwarded-For"))
			cookies := strings.Join(response.Header().Values("Set-Cookie"), ";")
			require.NotContains(t, cookies, "upstream-forgery")
			require.Contains(t, cookies, "renewed")
			require.Contains(t, cookies, "app_session=preserved")
			require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
			require.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
			require.NoError(t, m.Stop(t.Context()))
			require.Zero(t, m.tunnelBudget(7, 1).streams)
		})
	}
}

func TestPreviewGatewayDeniesUnsafeExchangeRedirectsAndCurrentUnavailableGrant(t *testing.T) {
	m, _, human, row, _ := terminalFixture(t)
	m.config.Preview = PreviewConfig{Domain: "preview.example.net", ConsoleOrigin: "https://console.example.org"}
	require.NoError(t, m.initializePreview(t.Context()))
	const label = "abcdefghijklmnopqrstuvwxyz"
	m.previewAccess.Authorize = func(context.Context, string, string, string) (PreviewGrant, error) {
		return PreviewGrant{TenantID: 7, MachineID: 1, RunnerSessionID: row.RunnerSessionID, ConnEpoch: row.ConnEpoch, Port: 3000}, nil
	}
	for _, path := range []string{"//evil.test/", "relative", "/bad\\path", "/bad\r\nheader"} {
		m.previewAccess.ExchangeCode = func(context.Context, string, string) (string, string, time.Time, error) {
			return "cookie", path, time.Time{}, nil
		}
		response := httptest.NewRecorder()
		m.previewGatewayHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://"+label+".preview.example.net/__access?code=x", nil))
		require.Equal(t, 403, response.Code)
		require.Empty(t, response.Header().Get("Set-Cookie"))
	}
	response := httptest.NewRecorder()
	m.previewGatewayHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://"+label+".preview.example.net/", nil))
	require.Equal(t, 503, response.Code, "disabled persisted preview access cannot be bypassed by a grant")
	m.previewAccess.Authorize = func(context.Context, string, string, string) (PreviewGrant, error) {
		return PreviewGrant{}, ErrMachineForbidden
	}
	response = httptest.NewRecorder()
	m.previewGatewayHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://"+label+".preview.example.net/", nil))
	require.Equal(t, 302, response.Code)
	require.Contains(t, response.Header().Get("Location"), "https://console.example.org/preview-access")
	_ = human
}
