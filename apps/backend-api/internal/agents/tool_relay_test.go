package agents

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolCredentialRotationCASProofReplacementAndRedaction(t *testing.T) {
	m, db, _, _ := agentHTTPHarness(t)
	m.config.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	m.credentialProof = func(_ context.Context, _ *gorm.DB, tenant guid.ID, key, proof string) error {
		if tenant != 1 || key != "relay-test" || proof != "bound" {
			return ErrForbidden
		}
		return nil
	}
	var tool ToolView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		tool, e = m.createToolOn(agentContext(t, 2), tx, 1, ToolInput{EntryKey: "relay-test", Kind: "MCP_SERVER", Title: "Relay", Transport: "STREAMABLE_HTTP", Endpoint: "https://mcp.example.test", Credential: "initial-secret", ReauthProof: "bound"})
		return e
	}))
	q := query.BindToolEntry(db, m.names)
	before, e := q.WithContext(t.Context()).Where(q.ID.Eq(tool.ID)).First()
	require.NoError(t, e)
	input := ToolCredentialInput{Version: 1, Secret: "replacement-1234", ReauthProof: "bound"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.rotateToolCredentialOn(agentContext(t, 2), tx, 1, tool.ID, input)
		require.Equal(t, "1234", out.Last4)
		require.Equal(t, 2, out.Version)
		return e
	}))
	after, e := q.WithContext(t.Context()).Where(q.ID.Eq(tool.ID)).First()
	require.NoError(t, e)
	require.NotEqual(t, before.CredentialSealed, after.CredentialSealed)
	require.Equal(t, "1234", toolView(after).Credential.Last4)
	require.NotNil(t, toolView(after).Credential.RotatedAt)
	ring, e := m.keys()
	require.NoError(t, e)
	plain, e := ring.Open("tool_credential", []byte("1:"+tool.ID.String()), after.CredentialSealed)
	require.NoError(t, e)
	require.Equal(t, input.Secret, string(plain))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.rotateToolCredentialOn(agentContext(t, 2), tx, 1, tool.ID, input)
		return e
	}), ErrConflict)
	input.Version = 2
	input.ReauthProof = "bad"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.rotateToolCredentialOn(agentContext(t, 2), tx, 1, tool.ID, input)
		return e
	}), ErrForbidden)
}

func TestToolRelayAuthFailuresPersistResetAndNotifyOncePer24Hours(t *testing.T) {
	m, db, _, _ := agentHTTPHarness(t)
	var tool ToolView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		tool, err = m.createToolOn(agentContext(t, 2), tx, 1, ToolInput{EntryKey: "auth-failure", Kind: "MCP_SERVER", Title: "Remote", Transport: "STREAMABLE_HTTP", Endpoint: "https://mcp.example.test"})
		return err
	}))
	now := m.now()
	m.now = func() time.Time { return now }
	notices := 0
	observer := ToolAuthFailureObserver(func(_ context.Context, tx *gorm.DB, f ToolAuthFailure) error {
		require.NotNil(t, tx)
		require.Equal(t, tool.ID, f.ToolID)
		notices++
		return nil
	})
	services := module.NewServices()
	require.NoError(t, m.provideToolAuthNotifications(services))
	require.NoError(t, services.Seal())
	notifications, e := module.Resolve[ToolAuthNotifications](services)
	require.NoError(t, e)
	require.ErrorIs(t, notifications.Bind(nil), ErrConflict)
	require.NoError(t, notifications.Bind(observer))
	require.ErrorIs(t, notifications.Bind(observer), ErrConflict)
	token := relayToken{TenantID: 1, Tool: RelayToolPin{ToolID: tool.ID}}
	read := func() ToolCredentialStatus {
		q := query.BindToolEntry(db, m.names)
		row, err := q.WithContext(t.Context()).Where(q.ID.Eq(tool.ID)).First()
		require.NoError(t, err)
		return toolView(row).Credential
	}
	for range 4 {
		require.NoError(t, m.recordToolUpstreamStatus(t.Context(), token, 401))
	}
	require.False(t, read().UpstreamAuthFailing)
	require.Zero(t, notices)
	require.NoError(t, m.recordToolUpstreamStatus(t.Context(), token, 403))
	require.True(t, read().UpstreamAuthFailing)
	require.Equal(t, 1, notices)
	require.NoError(t, m.recordToolUpstreamStatus(t.Context(), token, 401))
	require.Equal(t, 1, notices)
	require.NoError(t, m.recordToolUpstreamStatus(t.Context(), token, 200))
	require.False(t, read().UpstreamAuthFailing)
	for range 5 {
		require.NoError(t, m.recordToolUpstreamStatus(t.Context(), token, 401))
	}
	require.Equal(t, 1, notices)
	now = now.Add(24 * time.Hour)
	require.NoError(t, m.recordToolUpstreamStatus(t.Context(), token, 401))
	require.Equal(t, 2, notices)
}

func TestToolRelayActuallyCallsHTTPWithCurrentCredentialAndRejectsRevocation(t *testing.T) {
	m, db, handler, _ := agentHTTPHarness(t)
	m.catalogPolicy = fleet.AgentCatalogPolicy{AllowedOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (bool, error) { return true, nil }}
	m.config.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	m.credentialProof = func(context.Context, *gorm.DB, guid.ID, string, string) error { return nil }
	seen := []string{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		data, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
			return
		}
		if !strings.Contains(string(data), "tools/call") {
			t.Error("missing actual JSON-RPC body")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`)
	}))
	defer upstream.Close()
	m.relayClient = upstream.Client()
	var tool ToolView
	var grant GrantView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		tool, e = m.createToolOn(agentContext(t, 2), tx, 1, ToolInput{EntryKey: "relay-live", Kind: "MCP_SERVER", Title: "Relay", Transport: "STREAMABLE_HTTP", Endpoint: "https://mcp.example.test", Credential: "first-credential", ReauthProof: "bound"})
		if e != nil {
			return e
		}
		grant, e = m.grantOn(agentContext(t, 2), tx, 1, tool.ID, 7, "ACTIVE")
		return e
	}))
	// The real HTTP client is retained; only its address is pinned to the test
	// TLS server. Production uses httpx's dial-time SSRF policy and no redirects.
	v := query.BindToolVersion(db, m.names)
	_, e := v.WithContext(t.Context()).Where(v.ToolID.Eq(tool.ID)).Update(v.Endpoint, upstream.URL)
	require.NoError(t, e)
	pin := RelayToolPin{ToolID: tool.ID, EntryKey: tool.EntryKey, VersionNo: 1, ContentHash: tool.ContentHash, GrantID: grant.ID, GrantVersion: grant.Version}
	m.relayExecution = func(context.Context, *gorm.DB, guid.ID, guid.ID) (RelayExecution, error) {
		return RelayExecution{AgentID: 7, PlanHash: "frozen-plan", DispatchEpoch: 1, Tools: []RelayToolPin{pin}}, nil
	}
	in := &toolTokenInput{AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "7"}}
	in.Body.ExecutionID = 31
	in.Body.ToolID = tool.ID
	token, e := m.toolTokenHTTP(agentContext(t, 7), in)
	require.NoError(t, e)
	request := &relayInput{EntryKey: tool.EntryKey, Authorization: "Bearer " + token.Body.Data.Token, RawBody: []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read","arguments":{"q":"private"}}}`)}
	events := []audit.DomainEvent{}
	m.audit = func(_ context.Context, _ *gorm.DB, event audit.DomainEvent) error {
		events = append(events, event)
		return nil
	}
	// Private network egress remains blocked even with a granted test endpoint.
	_, e = m.relayHTTP(t.Context(), request, "POST")
	require.Error(t, e)
	require.Empty(t, seen)
	// Use a public hostname with a TLS test transport to exercise the complete
	// HTTP request/response path while preserving production URL validation.
	_, e = v.WithContext(t.Context()).Where(v.ToolID.Eq(tool.ID)).Update(v.Endpoint, "https://mcp.example.test")
	require.NoError(t, e)
	transport := upstream.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(upstream.URL, "https://"))
	}
	m.relayClient = &http.Client{Transport: transport}
	output, e := m.relayHTTP(t.Context(), request, "POST")
	require.NoError(t, e)
	require.Equal(t, 200, output.Status)
	require.Contains(t, string(output.Body), `"result"`)
	require.Equal(t, []string{"Bearer first-credential"}, seen)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.rotateToolCredentialOn(agentContext(t, 2), tx, 1, tool.ID, ToolCredentialInput{Version: tool.Version, Secret: "second-credential", ReauthProof: "bound"})
		return e
	}))
	_, e = m.relayHTTP(t.Context(), request, "POST")
	require.NoError(t, e)
	require.Equal(t, "Bearer second-credential", seen[1])
	httpRequest := httptest.NewRequest(http.MethodPost, "/mcp-relay/"+tool.EntryKey, strings.NewReader(string(request.RawBody)))
	httpRequest.Header.Set("Authorization", request.Authorization)
	httpRequest.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpRequest)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Contains(t, recorder.Body.String(), `"jsonrpc":"2.0"`)
	require.NotContains(t, recorder.Body.String(), `"meta"`)
	g := query.BindToolGrant(db, m.names)
	_, e = g.WithContext(t.Context()).Where(g.ID.Eq(grant.ID)).Update(g.State, "REVOKED")
	require.NoError(t, e)
	_, e = m.relayHTTP(t.Context(), request, "POST")
	require.Error(t, e)
	require.Len(t, seen, 3)
	encoded, e := json.Marshal(events)
	require.NoError(t, e)
	require.NotContains(t, string(encoded), "first-credential")
	require.NotContains(t, string(encoded), "second-credential")
	require.NotContains(t, string(encoded), `"q":"private"`)
}

func TestRelayPinsAndValidationUseCurrentGrantPolicyAndImmutableConfiguration(t *testing.T) {
	m, db, _, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 7)
	m.config.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	m.credentialProof = func(context.Context, *gorm.DB, guid.ID, string, string) error { return nil }
	allow := fleet.AgentCatalogPolicy{AllowedOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (bool, error) { return true, nil }}
	m.catalogPolicy = allow
	var tool ToolView
	var grant GrantView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		tool, e = m.createToolOn(agentContext(t, 2), tx, 1, ToolInput{EntryKey: "relay-current", Kind: "MCP_SERVER", Title: "Current", Transport: "STREAMABLE_HTTP", Endpoint: "https://mcp.example.test", Credential: "sealed-secret", ReauthProof: "bound"})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		grant, e = m.grantOn(agentContext(t, 2), tx, 1, tool.ID, 7, "ACTIVE")
		return e
	}))
	db = db.Begin()
	require.NoError(t, db.Error)
	defer db.Rollback()
	pins, e := m.relayPinsOn(ctx, db, 1, 7)
	require.NoError(t, e)
	require.Len(t, pins, 1)
	pin := pins[0]
	require.Equal(t, grant.ID, pin.GrantID)
	require.Equal(t, tool.ContentHash, pin.ContentHash)
	empty, e := m.relayPinsOn(ctx, db, 1, 0)
	require.NoError(t, e)
	require.Empty(t, empty)
	token := relayToken{TenantID: 1, AgentID: 7, ExecutionID: 31, PlanHash: "frozen", DispatchEpoch: 2, Tool: pin, ExpiresAt: m.now().Add(time.Minute)}
	execution := RelayExecution{AgentID: 7, PlanHash: "frozen", DispatchEpoch: 2, Tools: pins}
	source := func(context.Context, *gorm.DB, guid.ID, guid.ID) (RelayExecution, error) { return execution, nil }
	m.relayExecution = source
	version, secret, e := m.validateRelayOn(ctx, db, token)
	require.NoError(t, e)
	require.Equal(t, pin.ContentHash, version.ContentHash)
	require.Equal(t, "sealed-secret", secret)
	fault := errors.New("current source unavailable")
	for _, mode := range []string{"missing-policy", "failed-policy", "denied-policy", "missing-execution", "failed-execution", "other-agent", "other-plan", "other-epoch", "missing-pin", "expired", "grant-version", "tool-state", "version-state", "corrupt-secret", "missing-key"} {
		t.Run(mode, func(t *testing.T) {
			m.catalogPolicy = allow
			m.relayExecution = source
			candidate := token
			want := ErrForbidden
			switch mode {
			case "missing-policy":
				m.catalogPolicy = fleet.AgentCatalogPolicy{}
				want = ErrUnavailable
			case "failed-policy":
				m.catalogPolicy.AllowedOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (bool, error) { return false, fault }
				want = fault
			case "denied-policy":
				m.catalogPolicy.AllowedOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (bool, error) { return false, nil }
			case "missing-execution":
				m.relayExecution = nil
			case "failed-execution":
				m.relayExecution = func(context.Context, *gorm.DB, guid.ID, guid.ID) (RelayExecution, error) {
					return RelayExecution{}, fault
				}
				want = fault
			case "other-agent":
				candidate.AgentID = 9
			case "other-plan":
				candidate.PlanHash = "changed"
			case "other-epoch":
				candidate.DispatchEpoch++
			case "missing-pin":
				candidate.Tool.VersionNo++
			case "expired":
				candidate.ExpiresAt = m.now()
			case "grant-version":
				require.NoError(t, db.Exec("UPDATE k_agents_tool_grants SET version=version+1 WHERE id=?", grant.ID).Error)
				defer db.Exec("UPDATE k_agents_tool_grants SET version=? WHERE id=?", grant.Version, grant.ID)
			case "tool-state":
				require.NoError(t, db.Exec("UPDATE k_agents_tool_entries SET state='ARCHIVED' WHERE id=?", tool.ID).Error)
				defer db.Exec("UPDATE k_agents_tool_entries SET state='ACTIVE' WHERE id=?", tool.ID)
			case "version-state":
				require.NoError(t, db.Exec("UPDATE k_agents_tool_versions SET state='REVOKED' WHERE tool_id=?", tool.ID).Error)
				defer db.Exec("UPDATE k_agents_tool_versions SET state='ACTIVE' WHERE tool_id=?", tool.ID)
			case "corrupt-secret":
				q := query.BindToolEntry(db, m.names)
				row, err := q.WithContext(ctx).Where(q.ID.Eq(tool.ID)).First()
				require.NoError(t, err)
				require.NoError(t, db.Exec("UPDATE k_agents_tool_entries SET credential_sealed='corrupt' WHERE id=?", tool.ID).Error)
				defer db.Exec("UPDATE k_agents_tool_entries SET credential_sealed=? WHERE id=?", row.CredentialSealed, tool.ID)
				want = ErrUnavailable
			case "missing-key":
				old := m.config.EncryptionKey
				m.config.EncryptionKey = ""
				defer func() { m.config.EncryptionKey = old }()
				want = ErrUnavailable
			}
			v, s, err := m.validateRelayOn(ctx, db, candidate)
			require.Error(t, err)
			if mode != "other-agent" {
				require.ErrorIs(t, err, want)
			}
			require.Nil(t, v)
			require.Empty(t, s)
		})
	}
	m.relayExecution = source
	m.catalogPolicy = fleet.AgentCatalogPolicy{}
	_, e = m.relayPinsOn(ctx, db, 1, 7)
	require.ErrorIs(t, e, ErrUnavailable)
	m.catalogPolicy.AllowedOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (bool, error) { return false, fault }
	_, e = m.relayPinsOn(ctx, db, 1, 7)
	require.ErrorIs(t, e, fault)
	m.catalogPolicy.AllowedOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (bool, error) { return false, nil }
	pins, e = m.relayPinsOn(ctx, db, 1, 7)
	require.NoError(t, e)
	require.Empty(t, pins)
	m.catalogPolicy = allow
	require.NoError(t, db.Exec("UPDATE k_agents_tool_entries SET state='ARCHIVED' WHERE id=?", tool.ID).Error)
	pins, e = m.relayPinsOn(ctx, db, 1, 7)
	require.NoError(t, e)
	require.Empty(t, pins)
}

func TestToolTokenRejectsUnavailableOrChangedExecutionAndMalformedRelayBeforeEgress(t *testing.T) {
	m, _, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	input := &toolTokenInput{AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "7"}}
	input.Body.ExecutionID = 31
	input.Body.ToolID = 91
	_, err := m.toolTokenHTTP(ctx, input)
	require.Error(t, err)
	for _, execution := range []RelayExecution{{AgentID: 9}, {AgentID: 7}, {AgentID: 7, Tools: []RelayToolPin{{ToolID: 92}}}} {
		m.relayExecution = func(context.Context, *gorm.DB, guid.ID, guid.ID) (RelayExecution, error) { return execution, nil }
		_, err = m.toolTokenHTTP(ctx, input)
		require.Error(t, err)
	}
	m.config.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	for _, input := range []*relayInput{{RawBody: make([]byte, (4<<20)+1)}, {EntryKey: "current", Authorization: "Basic bad"}, {EntryKey: "current", Authorization: "Bearer corrupt"}} {
		output, err := m.relayHTTP(ctx, input, "POST")
		require.Error(t, err)
		require.Nil(t, output)
	}
	ring, err := m.keys()
	require.NoError(t, err)
	for _, raw := range []string{"bad json", `{"tool":{"entry_key":"other"}}`} {
		sealed, err := ring.Seal("mcp_token", []byte("current"), []byte(raw))
		require.NoError(t, err)
		output, err := m.relayHTTP(ctx, &relayInput{EntryKey: "current", Authorization: "Bearer " + sealed}, "POST")
		require.Error(t, err)
		require.Nil(t, output)
	}
}

func TestRelayHTTPCurrentTokenLimitsUpstreamFailuresAndAuditNeverLeakCredentials(t *testing.T) {
	m, db, _, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 7)
	m.catalogPolicy = fleet.AgentCatalogPolicy{AllowedOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (bool, error) { return true, nil }}
	m.config.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	mode := "ok"
	calls := 0
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "session-42", r.Header.Get("Mcp-Session-Id"))
		assert.Equal(t, "2025-03-26", r.Header.Get("MCP-Protocol-Version"))
		w.Header().Set("Mcp-Session-Id", "next-session")
		w.Header().Set("Content-Type", "application/json")
		if mode == "large" {
			_, _ = io.WriteString(w, strings.Repeat("x", (16<<20)+1))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"actual upstream failure"}`)
	}))
	defer upstream.Close()
	transport := upstream.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(c context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(c, network, strings.TrimPrefix(upstream.URL, "https://"))
	}
	defer transport.CloseIdleConnections()
	m.relayClient = &http.Client{Transport: transport}
	var tool ToolView
	var grant GrantView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		tool, e = m.createToolOn(agentContext(t, 2), tx, 1, ToolInput{EntryKey: "relay-errors", Kind: "MCP_SERVER", Title: "Current", Transport: "STREAMABLE_HTTP", Endpoint: "https://mcp.example.test"})
		if e != nil {
			return e
		}
		grant, e = m.grantOn(agentContext(t, 2), tx, 1, tool.ID, 7, "ACTIVE")
		return e
	}))
	pin := RelayToolPin{ToolID: tool.ID, EntryKey: tool.EntryKey, VersionNo: 1, ContentHash: tool.ContentHash, GrantID: grant.ID, GrantVersion: grant.Version}
	m.relayExecution = func(context.Context, *gorm.DB, guid.ID, guid.ID) (RelayExecution, error) {
		return RelayExecution{AgentID: 7, PlanHash: "plan", DispatchEpoch: 1, Tools: []RelayToolPin{pin}}, nil
	}
	token := relayToken{TenantID: 1, AgentID: 7, ExecutionID: 31, PlanHash: "plan", DispatchEpoch: 1, Tool: pin, ExpiresAt: m.now().Add(time.Minute)}
	raw, e := json.Marshal(token)
	require.NoError(t, e)
	ring, e := m.keys()
	require.NoError(t, e)
	sealed, e := ring.Seal("mcp_token", []byte(tool.EntryKey), raw)
	require.NoError(t, e)
	input := &relayInput{EntryKey: tool.EntryKey, Authorization: "Bearer " + sealed, Session: "session-42", Protocol: "2025-03-26", RawBody: []byte(`{"method":"tools/call","params":{"name":"read","arguments":{"private":"value"}}}`)}
	output, e := m.relayHTTP(ctx, input, "POST")
	require.NoError(t, e)
	require.Equal(t, 500, output.Status)
	require.Equal(t, "next-session", output.Session)
	require.Contains(t, string(output.Body), "actual upstream failure")
	mode = "large"
	output, e = m.relayHTTP(ctx, input, "POST")
	require.Error(t, e)
	require.Nil(t, output)
	require.Equal(t, 2, calls)
	m.relay.active = map[guid.ID]int{31: 10}
	output, e = m.relayHTTP(ctx, input, "POST")
	require.Error(t, e)
	require.Nil(t, output)
	require.Equal(t, 2, calls)
	m.relay.active = nil
	m.relayExecution = nil
	original := m.audit
	m.audit = nil
	output, e = m.relayHTTP(ctx, input, "POST")
	require.Error(t, e)
	require.Nil(t, output)
	require.Equal(t, 2, calls)
	m.audit = original
	require.Empty(t, m.relay.active)
}

func TestToolCredentialHTTPReplayRedactsSecretAndFailurePreservesSealedValue(t *testing.T) {
	m, db, _, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	m.config.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	m.credentialProof = func(context.Context, *gorm.DB, guid.ID, string, string) error { return nil }
	var tool ToolView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		tool, e = m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "credential-replay", Kind: "MCP_SERVER", Title: "Current", Transport: "STREAMABLE_HTTP", Endpoint: "https://tools.example.test"})
		return e
	}))
	in := &toolCredentialHTTPInput{ToolPath: ToolPath{TeamPath: TeamPath{TenantID: "1"}, ToolID: tool.ID.String()}, IdempotencyKey: "rotate-once", Body: ToolCredentialInput{Version: 1, Secret: "replacement-secret", ReauthProof: "bound"}}
	first, err := m.toolCredentialHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, 2, first.Body.Data.Version)
	replay, err := m.toolCredentialHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	q := query.BindToolEntry(db, m.names)
	before, err := q.WithContext(ctx).First()
	require.NoError(t, err)
	for _, mode := range []string{"empty", "newline", "proof", "keys", "audit", "write"} {
		t.Run(mode, func(t *testing.T) {
			oldProof, oldKey, oldAudit := m.credentialProof, m.config.EncryptionKey, m.audit
			defer func() { m.credentialProof = oldProof; m.config.EncryptionKey = oldKey; m.audit = oldAudit }()
			body := ToolCredentialInput{Version: 2, Secret: "next-secret", ReauthProof: "bound"}
			switch mode {
			case "empty":
				body.Secret = ""
			case "newline":
				body.Secret = "bad\nsecret"
			case "proof":
				m.credentialProof = nil
			case "keys":
				m.config.EncryptionKey = ""
			case "audit":
				m.audit = nil
			case "write":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("credential-write", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ToolEntry.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Update().Remove("credential-write")
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.rotateToolCredentialOn(ctx, tx, 1, tool.ID, body); return e }))
			after, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, before.CredentialSealed, after.CredentialSealed)
			require.Equal(t, 2, after.Version)
		})
	}
	in.ToolID = "bad"
	_, err = m.toolCredentialHTTP(ctx, in)
	require.Error(t, err)
}

func TestRelayExecutionConnectorBindsOnceAndUsesAuthoritativeExecutionForTokens(t *testing.T) {
	m, _, _ := agentHarness(t)
	services := module.NewServices()
	require.NoError(t, m.provideToolRelay(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[ToolRelayExecutionConnector](services)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(nil), ErrConflict)
	fault := errors.New("execution reader unavailable")
	reader := func(context.Context, *gorm.DB, guid.ID, guid.ID) (RelayExecution, error) {
		return RelayExecution{}, fault
	}
	require.NoError(t, connector.Bind(reader))
	require.ErrorIs(t, connector.Bind(reader), ErrConflict)
	in := &toolTokenInput{AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "7"}}
	in.Body.ExecutionID = 31
	out, err := m.toolTokenHTTP(agentContext(t, 7), in)
	require.Error(t, err)
	require.Nil(t, out)
	in.AgentPrincipalID = "bad"
	out, err = m.toolTokenHTTP(agentContext(t, 7), in)
	require.Error(t, err)
	require.Nil(t, out)
}

func TestRelayPinStorageFailuresAndUntrustedActorNeverReturnApprovedPins(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	for _, mode := range []string{"authority", "grants"} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			if mode == "authority" {
				_, e := m.relayPinsOn(agentContext(t, 2), tx, 1, 7)
				return e
			}
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("pin-source", func(q *gorm.DB) {
				if q.Statement.Table == m.names.ToolGrant.String() {
					q.AddError(ErrUnavailable)
				}
			}))
			defer db.Callback().Query().Remove("pin-source")
			pins, e := m.relayPinsOn(ctx, tx, 1, 7)
			require.Nil(t, pins)
			return e
		}))
	}
}
