package composition_test

import (
	platformapp "7link.com/platform/internal/app"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	iamauthn "7link.com/platform/internal/iam/authn"
	transportaudit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Opt-in acceptance, with actual composed IAM, production HTTP transport and a
// separate runner process using native ConPTY. No desktop service is installed.
func TestTerminalBackendNativeLive(t *testing.T) {
	worker := os.Getenv("RUNNER_TERMINAL_TEST_BINARY")
	if worker == "" || runtime.GOOS != "windows" {
		t.Skip("requires Windows and RUNNER_TERMINAL_TEST_BINARY")
	}
	worker, err := filepath.Abs(worker)
	require.NoError(t, err)
	// Keep even the configured default local recording backend inside scratch.
	t.Chdir(t.TempDir())
	// Reuse the test TLS certificate, with every request dialed directly to
	// this owned loopback listener. No DNS or external application is contacted.
	tlsFixture := httptest.NewTLSServer(http.NotFoundHandler())
	gatewayAddress := tlsFixture.Listener.Addr().String()
	certificate := tlsFixture.TLS.Certificates[0]
	tlsFixture.Close()
	keys := t.TempDir()
	certFile, keyFile := filepath.Join(keys, "cert.pem"), filepath.Join(keys, "key.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0600))
	privateKey, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}), 0600))
	f := newApprovalFixture(t, func(config *platformapp.AppConfig) {
		config.Fleet.Preview = fleet.PreviewConfig{Domain: "preview.example.net", ConsoleOrigin: "https://app.example.test", Listen: gatewayAddress, TLS: fleet.PreviewTLSConfig{CertFile: certFile, KeyFile: keyFile}}
	})
	db := f.app.Runtime().Database().Write()
	workspace := filepath.Join(t.TempDir(), "execution-workspace")
	git := exec.CommandContext(t.Context(), "git", "clone", "--quiet", "--no-hardlinks", f.repo, workspace)
	hide(git)
	require.NoError(t, git.Run())
	var planRow struct {
		PlanJSON string `gorm:"column:plan_json"`
	}
	require.NoError(t, db.Table("execution_executions").Where("id=601").Take(&planRow).Error)
	var plan execution.ExecutionPlan
	require.NoError(t, json.Unmarshal([]byte(planRow.PlanJSON), &plan))
	plan.WorkdirID = 701
	planBytes, err := json.Marshal(plan)
	require.NoError(t, err)
	planHash := sha256.Sum256(planBytes)
	require.NoError(t, db.Table("execution_executions").Where("id=601").Updates(map[string]any{"plan_json": string(planBytes), "plan_hash": hex.EncodeToString(planHash[:])}).Error)
	credentials, err := module.Resolve[iamauthn.BootstrapCredentialStore](f.app.Modules().Services())
	require.NoError(t, err)
	const password = "LiveTerminal-Fixture-Only-42!"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return credentials.Create(t.Context(), tx, 301, password, time.Now().UTC()) }))
	web, err := module.Resolve[iamauthn.WebService](f.app.Modules().Services())
	require.NoError(t, err)
	now := time.Now().UTC()
	// Installing the fixture password advances token_version; issue credentials
	// after that mutation instead of reusing the earlier revoked fixture token.
	f.humanToken, _, err = web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	session, err := web.CreateSession(t.Context(), 301, now, authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	cookie := (&http.Cookie{Name: web.SessionCookieName(), Value: session}).String()
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	defer cancel()
	api := func(method, path string, body, out any, browserOnly bool, version string, keys ...string) int {
		t.Helper()
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(ctx, method, f.url+path, bytes.NewReader(raw))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", cookie)
		req.Header.Set("Origin", "https://app.example.test")
		if !browserOnly {
			req.Header.Set("Authorization", "Bearer "+f.humanToken)
		}
		if version != "" {
			req.Header.Set("If-Match", version)
		}
		if len(keys) > 0 {
			req.Header.Set("Idempotency-Key", keys[0])
		}
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		require.NoError(t, err)
		if res.StatusCode >= 200 && res.StatusCode < 300 && out != nil {
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(data, &envelope))
			require.NoError(t, json.Unmarshal(envelope.Data, out))
		}
		return res.StatusCode
	}
	proof := func(purpose, target string) string {
		t.Helper()
		var options iamauthn.ReauthenticationOptions
		require.Equal(t, 201, api("POST", "/principals/301/reauthentication/challenges", map[string]string{"purpose": purpose, "target": target, "method": "password"}, &options, true, ""))
		require.Equal(t, 201, api("POST", "/principals/301/reauthentication/verifications", map[string]string{"proof": options.Proof, "password": password}, nil, true, ""))
		return options.Proof
	}
	var access fleet.TerminalAccessView
	require.Equal(t, 200, api("GET", "/machines/401/remote-access", nil, &access, false, ""))
	require.False(t, access.AllowRemoteTerminal)
	enableProof := proof("machine_terminal_enable", "machine:401")
	status := api("PUT", "/machines/401/remote-access", map[string]any{"allow_remote_terminal": true, "allow_remote_preview": true, "reauth_proof": enableProof}, &access, false, "1")
	require.Equal(t, 200, status)
	require.True(t, access.AllowRemoteTerminal)
	privateDir := t.TempDir()
	stopFile := filepath.Join(privateDir, "stop")
	previewPortFile := filepath.Join(privateDir, "preview-port")
	config, err := json.Marshal(map[string]string{"URL": f.url, "Token": f.runnerToken, "MachineID": "401", "SessionID": "402", "ExecutionID": "601", "Repository": f.repo, "Workspace": workspace, "StopFile": stopFile, "PreviewPortFile": previewPortFile})
	require.NoError(t, err)
	private := filepath.Join(privateDir, "worker.json")
	require.NoError(t, os.WriteFile(private, config, 0600))
	cmd := exec.CommandContext(ctx, worker, "-test.run=^TestTerminalBackendLiveWorker$", "-test.v", "-test.timeout=100s")
	hide(cmd)
	workerTemp := filepath.Join(privateDir, "runner-temp")
	require.NoError(t, os.Mkdir(workerTemp, 0700))
	cmd.Env = append(os.Environ(), "RUNNER_TERMINAL_BACKEND_CONFIG="+private, "TMP="+workerTemp, "TEMP="+workerTemp)
	// Do not print subprocess output on failures: the fixture holds real tokens.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.ProcessState != nil {
			return
		}
		_ = os.WriteFile(stopFile, []byte("stop"), 0600)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		cancel()
	})
	require.Eventually(t, func() bool {
		if ctx.Err() != nil {
			return false
		}
		return api("GET", "/machines/401/remote-access", nil, &access, false, "") == 200 && access.TunnelAvailable
	}, 10*time.Second, 50*time.Millisecond)
	var terminal fleet.TerminalSession
	openBody := map[string]any{"execution_id": "601", "cols": 80, "rows": 24, "reauth_proof": proof("terminal_interactive", "execution:601")}
	require.Equal(t, 201, api("POST", "/machines/401/terminal-sessions", openBody, &terminal, false, "", "native-live-terminal"))
	require.Equal(t, "OPENING", terminal.State)
	var replay fleet.TerminalSession
	require.Equal(t, 201, api("POST", "/machines/401/terminal-sessions", openBody, &replay, false, "", "native-live-terminal"))
	require.Equal(t, terminal, replay)
	var nativeSessions int64
	require.NoError(t, db.Table("fleet_terminal_sessions").Where("execution_id=601").Count(&nativeSessions).Error)
	require.EqualValues(t, 1, nativeSessions)
	require.Equal(t, 200, api("GET", "/terminal-sessions/"+terminal.ID.String(), nil, &terminal, false, ""))
	require.Equal(t, "OPEN", terminal.State, "terminal closed with reason %q", terminal.CloseReason)
	require.False(t, terminal.Sandboxed)
	path := "/terminal-sessions/" + terminal.ID.String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url+path+"/stream", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+f.humanToken)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })
	if res.StatusCode != http.StatusOK {
		failure, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		t.Logf("terminal stream error: %s", failure)
	}
	require.Equal(t, 200, res.StatusCode)
	defer res.Body.Close()
	var outputMu sync.Mutex
	var output bytes.Buffer
	streamDone := make(chan error, 1)
	closedFrame := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(res.Body)
		for scanner.Scan() {
			var frame struct {
				Kind string `json:"kind"`
				Data []byte `json:"data"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
				streamDone <- err
				return
			}
			if frame.Kind == "output" {
				outputMu.Lock()
				output.Write(frame.Data)
				outputMu.Unlock()
			}
			if frame.Kind == "closed" {
				close(closedFrame)
				streamDone <- nil
				return
			}
		}
		streamDone <- scanner.Err()
	}()
	input := []byte(">terminal-proof.txt echo terminal-live-proof\r\nset \"terminalMarker=TERMINAL_\"\r\necho %terminalMarker%DONE\r\n")
	require.Equal(t, 200, api("POST", path+"/input", map[string]any{"seq": 1, "data": input}, nil, false, ""))
	require.Eventually(t, func() bool {
		actual, err := os.ReadFile(filepath.Join(workspace, "terminal-proof.txt"))
		outputMu.Lock()
		marker := strings.Contains(output.String(), "TERMINAL_DONE")
		outputMu.Unlock()
		return err == nil && string(actual) == "terminal-live-proof\r\n" && marker
	}, 15*time.Second, 50*time.Millisecond)
	_, err = os.Stat(filepath.Join(f.repo, "terminal-proof.txt"))
	require.True(t, os.IsNotExist(err), "terminal modified source checkout")
	require.Equal(t, 200, api("POST", path+"/revoke", nil, &terminal, false, ""))
	require.Equal(t, "CLOSED", terminal.State)
	require.Equal(t, "USER", terminal.CloseReason)
	select {
	case <-closedFrame:
	case <-ctx.Done():
		t.Fatal("terminal output never reported closure")
	}
	require.NoError(t, <-streamDone)
	var metadata struct {
		State           string
		RecordingFileID *guid.ID
		RecordingSHA256 string
		RecordingChunks string
	}
	require.NoError(t, db.Table("fleet_terminal_sessions").Where("id=?", terminal.ID).Take(&metadata).Error)
	require.Equal(t, "CLOSED", metadata.State)
	require.NotNil(t, metadata.RecordingFileID)
	require.Len(t, metadata.RecordingSHA256, 64)
	var recordingChunks []guid.ID
	require.NoError(t, json.Unmarshal([]byte(metadata.RecordingChunks), &recordingChunks))
	require.Len(t, recordingChunks, 1, "small live transcript must share one durable recording chunk")
	var object struct{ StorageKey, SHA256 string }
	require.NoError(t, db.Table("file_objects AS o").Select("o.storage_key,o.sha256").Joins("JOIN file_versions AS v ON v.object_id=o.id").Where("v.node_id=?", *metadata.RecordingFileID).Take(&object).Error)
	driver, err := module.Resolve[files.Driver](f.app.Modules().Services())
	require.NoError(t, err)
	recording, err := driver.Open(ctx, object.StorageKey)
	require.NoError(t, err)
	cast, err := io.ReadAll(recording)
	require.NoError(t, err)
	require.NoError(t, recording.Close())
	castHash := sha256.Sum256(cast)
	require.Equal(t, metadata.RecordingSHA256, hex.EncodeToString(castHash[:]))
	require.Equal(t, object.SHA256, metadata.RecordingSHA256)
	require.Contains(t, string(cast), "terminal-live-proof")
	var audits int64
	require.NoError(t, db.Table("iam_audit_events").Where("target_id=? AND event_type IN ?", terminal.ID, []string{"execution.terminal_opened", "execution.terminal_closed"}).Count(&audits).Error)
	require.Equal(t, int64(2), audits)
	var previewPort int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(previewPortFile)
		if err != nil {
			return false
		}
		previewPort, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil && previewPort > 0
	}, 10*time.Second, 50*time.Millisecond)
	var offered []execution.PreviewOfferedPort
	require.Eventually(t, func() bool {
		if api("GET", "/executions/601/offered-ports", nil, &offered, false, "") != 200 {
			return false
		}
		for _, p := range offered {
			if p.Port == previewPort {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond)
	unrelated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("unrelated-service")) }))
	t.Cleanup(unrelated.Close)
	unrelatedURL, err := url.Parse(unrelated.URL)
	require.NoError(t, err)
	unrelatedPort, err := strconv.Atoi(unrelatedURL.Port())
	require.NoError(t, err)
	require.Equal(t, 403, api("POST", "/executions/601/preview-sessions", map[string]any{"kind": "SERVICE", "port": unrelatedPort}, nil, false, ""))
	var preview execution.PreviewSessionView
	require.Equal(t, 201, api("POST", "/executions/601/preview-sessions", map[string]any{"kind": "SERVICE", "port": previewPort}, &preview, false, ""))
	var grant execution.PreviewAccessGrant
	previewPath := "/preview-sessions/" + preview.ID.String()
	require.Equal(t, 201, api("POST", previewPath+"/access-grants", map[string]any{}, &grant, true, ""))
	grantURL, err := url.Parse(grant.URL)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", gatewayAddress)
	}} // Test certificate is scoped to loopback, not the synthetic preview domain.
	t.Cleanup(transport.CloseIdleConnections)
	previewClient := &http.Client{Transport: transport, Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	exchange, err := previewClient.Get(grant.URL)
	require.NoError(t, err)
	require.Equal(t, 303, exchange.StatusCode)
	require.NoError(t, exchange.Body.Close())
	baseURL := "https://" + grantURL.Host
	getPreview := func(path string, expected int) []byte {
		t.Helper()
		response, err := previewClient.Get(baseURL + path)
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, expected, response.StatusCode)
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		require.NoError(t, err)
		return body
	}
	require.Contains(t, string(getPreview("/", 200)), "preview-native-ok")
	require.Equal(t, []byte{0, 255, 1, 2, 0, 128}, getPreview("/binary", 200))
	rangeRequest, err := http.NewRequest(http.MethodGet, baseURL+"/range", nil)
	require.NoError(t, err)
	rangeRequest.Header.Set("Range", "bytes=3-7")
	rangeResponse, err := previewClient.Do(rangeRequest)
	require.NoError(t, err)
	require.Equal(t, 206, rangeResponse.StatusCode)
	rangeBody, err := io.ReadAll(rangeResponse.Body)
	require.NoError(t, rangeResponse.Body.Close())
	require.NoError(t, err)
	require.Equal(t, "34567", string(rangeBody))
	require.Equal(t, "data: first\n\ndata: second\n\n", string(getPreview("/sse", 200)))
	var upstreamHeaders http.Header
	require.NoError(t, json.Unmarshal(getPreview("/headers", 200), &upstreamHeaders))
	require.NotContains(t, upstreamHeaders.Get("Cookie"), "preview=")
	// Exercise a real browser-to-gateway upgrade and masked client frame;
	// the runner tunnel carries the opaque duplex bytes without a WS dependency.
	ws, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", gatewayAddress, &tls.Config{InsecureSkipVerify: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ws.Close() })
	require.NoError(t, ws.SetDeadline(time.Now().Add(5*time.Second)))
	wsRequest, err := http.NewRequest(http.MethodGet, baseURL+"/ws", nil)
	require.NoError(t, err)
	for _, cookie := range jar.Cookies(grantURL) {
		wsRequest.AddCookie(cookie)
	}
	wsRequest.Header.Set("Connection", "Upgrade")
	wsRequest.Header.Set("Upgrade", "websocket")
	wsRequest.Header.Set("Sec-WebSocket-Version", "13")
	wsRequest.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	require.NoError(t, wsRequest.Write(ws))
	wsReader := bufio.NewReader(ws)
	wsResponse, err := http.ReadResponse(wsReader, wsRequest)
	require.NoError(t, err)
	t.Cleanup(func() { _ = wsResponse.Body.Close() })
	require.Equal(t, 101, wsResponse.StatusCode)
	require.Equal(t, "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=", wsResponse.Header.Get("Sec-WebSocket-Accept"))
	_, err = ws.Write([]byte{0x81, 0x84, 1, 2, 3, 4, 'p' ^ 1, 'i' ^ 2, 'n' ^ 3, 'g' ^ 4})
	require.NoError(t, err)
	wsEcho := make([]byte, 6)
	_, err = io.ReadFull(wsReader, wsEcho)
	require.NoError(t, err)
	require.Equal(t, []byte{0x81, 4, 'p', 'i', 'n', 'g'}, wsEcho)
	require.NoError(t, ws.Close())
	require.Equal(t, 200, api("PUT", previewPath+"/termination", nil, &preview, false, ""))
	require.Equal(t, "STOPPED", preview.State)
	getPreview("/", 302)
	require.NoError(t, os.WriteFile(stopFile, []byte("stop"), 0600))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("native runner did not join terminal cleanup")
	}
	t.Log("real_backend=true native_conpty=true browser_stepup=true input_output=true frozen_workspace=true revoke=true recording_sha256=true audit=true preview_native_owned_http=true isolated_tls_gateway=true preview_binary=true unrelated_port_denied=true preview_stop=true temporary_profile_cleaned=true service_installed=false")
}

func TestTerminalAndPreviewTransportAuditNeverRecordsCredentialsOrBytes(t *testing.T) {
	for _, path := range []string{"/machines/1/terminal-tunnel/frames", "/terminal-sessions/1/input", "/machines/1/remote-access", "/machines/1/preview-tunnel/frames", "/preview-sessions/1/access-grants", "/preview-access-grants"} {
		for _, status := range []int{201, 403, 422} {
			t.Run(path+fmt.Sprint(status), func(t *testing.T) {
				sink := &capturingAuditSink{}
				auditor := transportaudit.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "test")), transportaudit.Config{Enabled: true, CaptureBodies: true, QueueSize: 32, BatchSize: 1, FlushInterval: time.Millisecond, MaxBodyBytes: 4096}, transportaudit.WithSinks(sink))
				require.NoError(t, auditor.Start(t.Context()))
				t.Cleanup(func() { require.NoError(t, auditor.Stop(context.Background())) })
				body := `{"data":"PRIVATE-TRANSCRIPT","reauth_proof":"PRIVATE-STEP-UP","url":"https://opaque.invalid/__access?code=PRIVATE-PREVIEW-CODE"}`
				if status == 422 {
					body = strings.TrimSuffix(body, "}")
				}
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					received, err := io.ReadAll(r.Body)
					if !assert.NoError(t, err) {
						return
					}
					if !assert.Equal(t, body, string(received), "body omission must preserve business-handler bytes") {
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, err = w.Write([]byte(`{"code":1,"message":"PRIVATE-ERROR-ECHO","data":{"url":"https://opaque.invalid/__access?code=PRIVATE-RESPONSE-CODE"},"errors":[{"value":"PRIVATE-TRANSCRIPT"}]}`))
					if !assert.NoError(t, err) {
						return
					}
				})
				business := (&fleet.Module{}).HTTPMiddleware((&execution.Module{}).HTTPMiddleware(handler))
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				auditor.HTTPMiddleware(business).ServeHTTP(httptest.NewRecorder(), request)
				require.Eventually(t, func() bool { return len(sink.snapshot()) == 1 }, time.Second, time.Millisecond)
				event := sink.snapshot()[0]
				encoded, err := json.Marshal(event)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "PRIVATE-")
				require.Contains(t, event.Request.Body, "confidential_resource")
				require.Contains(t, event.Response.Body, "confidential_resource")
			})
		}
	}
}
