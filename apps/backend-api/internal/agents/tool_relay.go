package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RelayToolPin contains approved configuration identities, never credentials.
type RelayToolPin struct {
	ToolID       guid.ID `json:"tool_id"`
	EntryKey     string  `json:"entry_key"`
	VersionNo    int     `json:"version_no"`
	ContentHash  string  `json:"content_hash"`
	GrantID      guid.ID `json:"grant_id"`
	GrantVersion int     `json:"grant_version"`
}
type RelayExecution struct {
	AgentID       guid.ID
	PlanHash      string
	DispatchEpoch int64
	Tools         []RelayToolPin
}
type ToolRelayExecutionConnector struct {
	Bind   func(func(context.Context, *gorm.DB, guid.ID, guid.ID) (RelayExecution, error)) error
	PinsOn func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]RelayToolPin, error)
}
type relayState struct {
	mu     sync.Mutex
	active map[guid.ID]int
}
type relayToken struct {
	TenantID      guid.ID      `json:"tenant_id"`
	ExecutionID   guid.ID      `json:"execution_id"`
	AgentID       guid.ID      `json:"agent_id"`
	PlanHash      string       `json:"plan_hash"`
	DispatchEpoch int64        `json:"dispatch_epoch"`
	Tool          RelayToolPin `json:"tool"`
	ExpiresAt     time.Time    `json:"expires_at"`
}
type ToolCredentialInput struct {
	Version     int    `json:"version" minimum:"1"`
	Secret      string `json:"secret" minLength:"1" maxLength:"8000" writeOnly:"true"`
	ReauthProof string `json:"reauth_proof" minLength:"1" maxLength:"512" writeOnly:"true"`
}
type ToolCredentialView struct {
	Set       bool      `json:"set"`
	Last4     string    `json:"last4"`
	RotatedAt time.Time `json:"rotated_at"`
	Version   int       `json:"version"`
}
type toolCredentialHTTPInput struct {
	ToolPath
	Cookie         string `header:"Cookie"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           ToolCredentialInput
}
type toolTokenInput struct {
	AgentPath
	Body struct {
		ExecutionID guid.ID `json:"execution_id"`
		ToolID      guid.ID `json:"tool_id"`
	}
}
type toolTokenView struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	RelayURL  string    `json:"relay_url"`
}
type relayInput struct {
	EntryKey      string `path:"entry_key" pattern:"^[a-z0-9][a-z0-9-]{1,63}$" maxLength:"64"`
	Authorization string `header:"Authorization"`
	Session       string `header:"Mcp-Session-Id" maxLength:"512"`
	Protocol      string `header:"MCP-Protocol-Version" maxLength:"64"`
	RawBody       []byte
}
type relayOutput struct {
	CacheControl string `header:"Cache-Control"`
	ContentType  string `header:"Content-Type"`
	Session      string `header:"Mcp-Session-Id"`
	Status       int    `status:""`
	Body         []byte
}

func (m *Module) provideToolRelay(s *module.Services) error {
	return module.Provide(s, ToolRelayExecutionConnector{PinsOn: m.relayPinsOn, Bind: func(fn func(context.Context, *gorm.DB, guid.ID, guid.ID) (RelayExecution, error)) error {
		if fn == nil || m.relayExecution != nil {
			return ErrConflict
		}
		m.relayExecution = fn
		return nil
	}})
}
func (m *Module) registerToolRelayHTTP(api huma.API) error {
	op := func(id, method, p, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: p, Summary: summary, Tags: []string{"tenants"}, MaxBodyBytes: 4 << 20, Middlewares: huma.Middlewares{privateResponseHeaders}, Errors: []int{401, 403, 404, 409, 413, 422, 429, 500, 502, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, op("agent_tools.credential", "PUT", "/agent-tools/{tool_id}/credential", "Replace a sealed MCP credential after bound reauthentication"), m.toolCredentialHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_tools.token", "POST", agentPath+"/tool-tokens", "Issue a short-lived execution-scoped MCP tool token"), m.toolTokenHTTP)
	// Spec05 T13 authenticates the dedicated execution tool token rather than an
	// IAM member bearer. The handler verifies it before touching the database.
	if api.OpenAPI().Components.SecuritySchemes == nil {
		api.OpenAPI().Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	api.OpenAPI().Components.SecuritySchemes["ExecutionToolToken"] = &huma.SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "encrypted execution-scoped tool token"}
	for _, method := range []string{"POST", "GET", "DELETE"} {
		verb := method
		operation := op("agent_tools.relay_"+strings.ToLower(method), method, "/mcp-relay/{entry_key}", "Relay a scoped MCP request without disclosing its credential")
		operation.Security = []map[string][]string{{"ExecutionToolToken": {}}}
		huma.Register(api, operation, func(ctx context.Context, in *relayInput) (*relayOutput, error) { return m.relayHTTP(ctx, in, verb) })
	}
	if !m.registrar.IsDeclarationOnly() {
		for _, p := range []string{"/tenants/{tenant_id}/agent-tools/{tool_id}/credential", "/tenants/{tenant_id}" + agentPath + "/tool-tokens"} {
			if e := authz.RegisterRequestContract(api, p); e != nil {
				return e
			}
		}
	}
	return nil
}
func (m *Module) rotateToolCredentialOn(ctx context.Context, tx *gorm.DB, tenant, tool guid.ID, in ToolCredentialInput) (ToolCredentialView, error) {
	var out ToolCredentialView
	row, e := m.toolLockedOn(ctx, tx, tenant, tool, in.Version)
	if e != nil {
		return out, e
	}
	if row.Kind != "MCP_SERVER" || row.Transport != "STREAMABLE_HTTP" || row.State == "ARCHIVED" || in.Secret == "" || len(in.Secret) > 8000 || !utf8.ValidString(in.Secret) || strings.ContainsAny(in.Secret, "\r\n") {
		return out, ErrInvalid
	}
	if m.credentialProof == nil || in.ReauthProof == "" {
		return out, ErrForbidden
	}
	if e = m.credentialProof(ctx, tx, tenant, row.EntryKey, in.ReauthProof); e != nil {
		return out, ErrForbidden
	}
	ring, e := m.keys()
	if e != nil {
		return out, e
	}
	sealed, e := ring.Seal("tool_credential", []byte(tenant.String()+":"+tool.String()), []byte(in.Secret))
	if e != nil {
		return out, ErrUnavailable
	}
	q := query.BindToolEntry(tx, m.names)
	now := m.now()
	changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(tool), q.Version.Eq(in.Version)).Updates(map[string]any{"credential_sealed": sealed, "credential_last4": credentialLast4(in.Secret), "credential_rotated_at": now, "upstream_auth_failures": 0, "version": in.Version + 1})
	if e != nil {
		return out, e
	}
	if changed.RowsAffected != 1 {
		return out, ErrConflict
	}
	last := []rune(in.Secret)
	if len(last) > 4 {
		last = last[len(last)-4:]
	}
	out = ToolCredentialView{Set: true, Last4: string(last), RotatedAt: m.now(), Version: in.Version + 1}
	event := audit.NewDomainEvent(ctx, tenant, "fleet.tool_credential_rotated", "agent_tool", tool)
	event.Detail["authority_source"] = "IAM:tool_catalog_manage"
	event.Detail["last4"] = out.Last4
	event.Detail["reauth_purpose"] = "tool_credential_manage"
	if m.audit == nil {
		return out, ErrUnavailable
	}
	if e = m.audit(ctx, tx, event); e != nil {
		return out, e
	}
	return out, m.record(ctx, tx, tenant, tool, "TOOL_CREDENTIAL_ROTATED", true)
}
func (m *Module) toolCredentialHTTP(ctx context.Context, in *toolCredentialHTTPInput) (*CommandOutput[ToolCredentialView], error) {
	tenant, tool, e := toolPathIDs(ctx, in.ToolPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	ctx = context.WithValue(ctx, proofCookieKey{}, in.Cookie)
	return agentCommand(ctx, m, in.IdempotencyKey, "agent_tools.credential", "/agent-tools/{tool_id}/credential", map[string]string{"tenant_id": in.TenantID, "tool_id": in.ToolID}, in.Body, func(tx *gorm.DB) error { return m.teamToolAuthority(ctx, tx, tenant) }, func(tx *gorm.DB) (ToolCredentialView, error) {
		return m.rotateToolCredentialOn(ctx, tx, tenant, tool, in.Body)
	})
}
func (m *Module) relayPinsOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) ([]RelayToolPin, error) {
	out := []RelayToolPin{}
	if agent <= 0 {
		return out, nil
	}
	if _, e := m.authorize(ctx, tx, tenant, agent, "SELF", false); e != nil {
		return nil, e
	}
	g := query.BindToolGrant(tx, m.names)
	grants, e := g.WithContext(ctx).Where(g.TenantID.Eq(tenant), g.AgentPrincipalID.Eq(agent), g.State.Eq("ACTIVE")).Order(g.ID).Limit(201).Find()
	if e != nil {
		return nil, e
	}
	if len(grants) > 200 {
		return nil, ErrUnavailable
	}
	for _, grant := range grants {
		q := query.BindToolEntry(tx, m.names)
		row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(grant.ToolID), q.Kind.Eq("MCP_SERVER"), q.Transport.Eq("STREAMABLE_HTTP"), q.State.Eq("ACTIVE")).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if m.catalogPolicy.AllowedOn == nil {
			return nil, ErrUnavailable
		}
		allowed, err := m.catalogPolicy.AllowedOn(ctx, tx, tenant, agent, row.EntryKey)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		v := query.BindToolVersion(tx, m.names)
		version, e := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ToolID.Eq(row.ID), v.State.Eq("ACTIVE")).Order(v.VersionNo.Desc()).First()
		if e != nil {
			return nil, e
		}
		out = append(out, RelayToolPin{ToolID: row.ID, EntryKey: row.EntryKey, VersionNo: version.VersionNo, ContentHash: version.ContentHash, GrantID: grant.ID, GrantVersion: grant.Version})
	}
	return out, nil
}
func (m *Module) toolTokenHTTP(ctx context.Context, in *toolTokenInput) (*workspaceOutput[toolTokenView], error) {
	tenant, agent, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	db, e := m.database()
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	var token relayToken
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := m.authorize(ctx, tx, tenant, agent, "SELF", false); err != nil {
			return err
		}
		if m.relayExecution == nil {
			return ErrUnavailable
		}
		execution, err := m.relayExecution(ctx, tx, tenant, in.Body.ExecutionID)
		if err != nil {
			return err
		}
		if execution.AgentID != agent {
			return ErrForbidden
		}
		for _, pin := range execution.Tools {
			if pin.ToolID == in.Body.ToolID {
				token = relayToken{TenantID: tenant, ExecutionID: in.Body.ExecutionID, AgentID: agent, PlanHash: execution.PlanHash, DispatchEpoch: execution.DispatchEpoch, Tool: pin, ExpiresAt: m.now().Add(5 * time.Minute)}
				return nil
			}
		}
		return ErrForbidden
	})
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	ring, e := m.keys()
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	data, e := json.Marshal(token)
	if e != nil {
		return nil, agentHTTPError(ctx, ErrUnavailable)
	}
	sealed, e := ring.Seal("mcp_token", []byte(token.Tool.EntryKey), data)
	if e != nil {
		return nil, agentHTTPError(ctx, ErrUnavailable)
	}
	return workspaceOK(ctx, toolTokenView{Token: sealed, ExpiresAt: token.ExpiresAt, RelayURL: "/mcp-relay/" + token.Tool.EntryKey}), nil
}
func (m *Module) validateRelayOn(ctx context.Context, tx *gorm.DB, token relayToken) (*model.ToolVersion, string, error) {
	if m.catalogPolicy.AllowedOn == nil {
		return nil, "", ErrUnavailable
	}
	allowed, e := m.catalogPolicy.AllowedOn(ctx, tx, token.TenantID, token.AgentID, token.Tool.EntryKey)
	if e != nil {
		return nil, "", e
	}
	if !allowed {
		return nil, "", ErrForbidden
	}
	if _, e := m.authorize(ctx, tx, token.TenantID, token.AgentID, "SELF", false); e != nil {
		return nil, "", e
	}
	if m.relayExecution == nil || !token.ExpiresAt.After(m.now()) {
		return nil, "", ErrForbidden
	}
	execution, e := m.relayExecution(ctx, tx, token.TenantID, token.ExecutionID)
	if e != nil {
		return nil, "", e
	}
	if execution.AgentID != token.AgentID || execution.PlanHash != token.PlanHash || execution.DispatchEpoch != token.DispatchEpoch {
		return nil, "", ErrForbidden
	}
	found := false
	for _, pin := range execution.Tools {
		if pin == token.Tool {
			found = true
		}
	}
	if !found {
		return nil, "", ErrForbidden
	}
	g := query.BindToolGrant(tx, m.names)
	count, e := g.WithContext(ctx).Where(g.TenantID.Eq(token.TenantID), g.ID.Eq(token.Tool.GrantID), g.ToolID.Eq(token.Tool.ToolID), g.AgentPrincipalID.Eq(token.AgentID), g.Version.Eq(token.Tool.GrantVersion), g.State.Eq("ACTIVE")).Count()
	if e != nil {
		return nil, "", e
	}
	if count != 1 {
		return nil, "", ErrForbidden
	}
	q := query.BindToolEntry(tx, m.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(token.TenantID), q.ID.Eq(token.Tool.ToolID), q.EntryKey.Eq(token.Tool.EntryKey), q.State.Eq("ACTIVE"), q.Transport.Eq("STREAMABLE_HTTP")).First()
	if e != nil {
		return nil, "", ErrForbidden
	}
	v := query.BindToolVersion(tx, m.names)
	version, e := v.WithContext(ctx).Where(v.TenantID.Eq(token.TenantID), v.ToolID.Eq(row.ID), v.VersionNo.Eq(token.Tool.VersionNo), v.ContentHash.Eq(token.Tool.ContentHash), v.State.Eq("ACTIVE")).First()
	if e != nil {
		return nil, "", ErrForbidden
	}
	secret := ""
	if row.CredentialSealed != "" {
		ring, err := m.keys()
		if err != nil {
			return nil, "", err
		}
		plain, err := ring.Open("tool_credential", []byte(token.TenantID.String()+":"+row.ID.String()), row.CredentialSealed)
		if err != nil {
			return nil, "", ErrUnavailable
		}
		secret = string(plain)
	}
	return version, secret, nil
}
func relayHTTPClient() *http.Client {
	client := httpx.NewClient(120*time.Second, httpx.Policy{})
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}
func (m *Module) recordRelayAudit(ctx context.Context, token relayToken, body []byte, eventType, outcome string, duration time.Duration) error {
	db, e := m.database()
	if e != nil {
		return e
	}
	arguments := body
	toolName := ""
	var rpc struct {
		Method string `json:"method"`
		Params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	if json.Unmarshal(body, &rpc) == nil && rpc.Method == "tools/call" {
		arguments = rpc.Params.Arguments
		if utf8.RuneCountInString(rpc.Params.Name) <= 200 {
			toolName = rpc.Params.Name
		}
	}
	sum := sha256.Sum256(arguments)
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if m.audit == nil {
			return ErrUnavailable
		}
		event := audit.NewDomainEvent(ctx, token.TenantID, eventType, "agent_tool", token.Tool.ToolID)
		event.Detail["authority_source"] = "TOKEN:execution_tool"
		event.Detail["entry_key"] = token.Tool.EntryKey
		event.Detail["version_no"] = token.Tool.VersionNo
		event.Detail["tool_name"] = toolName
		event.Detail["execution_id"] = token.ExecutionID.String()
		event.Detail["agent_id"] = token.AgentID.String()
		event.Detail["arguments_sha256"] = hex.EncodeToString(sum[:])
		event.Detail["duration_ms"] = duration.Milliseconds()
		event.Detail["outcome"] = outcome
		return m.audit(ctx, tx, event)
	})
}
func (m *Module) relayHTTP(ctx context.Context, in *relayInput, method string) (*relayOutput, error) {
	if len(in.RawBody) > 4<<20 {
		return nil, respx.ErrStatus(ctx, 413, 142413, "agents.invalid", nil)
	}
	ring, e := m.keys()
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	raw := strings.TrimPrefix(in.Authorization, "Bearer ")
	if raw == in.Authorization || len(raw) > 16000 {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	plain, e := ring.Open("mcp_token", []byte(in.EntryKey), raw)
	if e != nil {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	var token relayToken
	if json.Unmarshal(plain, &token) != nil || token.Tool.EntryKey != in.EntryKey {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: token.TenantID, PrincipalID: token.AgentID, SubjectType: authn.SubjectTypePrincipal})
	m.relay.mu.Lock()
	if m.relay.active == nil {
		m.relay.active = map[guid.ID]int{}
	}
	if m.relay.active[token.ExecutionID] >= 10 {
		m.relay.mu.Unlock()
		return nil, respx.ErrStatus(ctx, 429, 142429, "agents.unavailable", nil)
	}
	m.relay.active[token.ExecutionID]++
	m.relay.mu.Unlock()
	defer func() {
		m.relay.mu.Lock()
		m.relay.active[token.ExecutionID]--
		if m.relay.active[token.ExecutionID] == 0 {
			delete(m.relay.active, token.ExecutionID)
		}
		m.relay.mu.Unlock()
	}()
	db, e := m.database()
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	var version *model.ToolVersion
	var secret string
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		version, secret, err = m.validateRelayOn(ctx, tx, token)
		return err
	})
	if e != nil {
		if auditErr := m.recordRelayAudit(ctx, token, in.RawBody, "fleet.tool_call_denied", "denied", 0); auditErr != nil {
			return nil, agentHTTPError(ctx, auditErr)
		}
		return nil, agentHTTPError(ctx, e)
	}
	if (httpx.Policy{}).ValidateURL(version.Endpoint) != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	request, e := http.NewRequestWithContext(ctx, method, version.Endpoint, bytes.NewReader(in.RawBody))
	if e != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	if in.Session != "" {
		request.Header.Set("Mcp-Session-Id", in.Session)
	}
	if in.Protocol != "" {
		request.Header.Set("MCP-Protocol-Version", in.Protocol)
	}
	client := m.relayClient
	if client == nil {
		client = relayHTTPClient()
	}
	started := time.Now()
	response, e := client.Do(request)
	secret = ""
	if e != nil {
		if auditErr := m.recordRelayAudit(ctx, token, in.RawBody, "fleet.tool_called", "error", time.Since(started)); auditErr != nil {
			return nil, agentHTTPError(ctx, auditErr)
		}
		return nil, respx.ErrStatus(ctx, 502, 142057, "agents.unavailable", nil)
	}
	defer func() { _ = response.Body.Close() }()
	data, e := io.ReadAll(io.LimitReader(response.Body, 16<<20+1))
	if e != nil || len(data) > 16<<20 {
		if auditErr := m.recordRelayAudit(ctx, token, in.RawBody, "fleet.tool_called", "error", time.Since(started)); auditErr != nil {
			return nil, agentHTTPError(ctx, auditErr)
		}
		return nil, respx.ErrStatus(ctx, 502, 142057, "agents.unavailable", nil)
	}
	if e = m.recordToolUpstreamStatus(ctx, token, response.StatusCode); e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	e = m.recordRelayAudit(ctx, token, in.RawBody, "fleet.tool_called", map[bool]string{true: "ok", false: "error"}[response.StatusCode < 400], time.Since(started))
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return &relayOutput{CacheControl: "no-store", ContentType: response.Header.Get("Content-Type"), Session: response.Header.Get("Mcp-Session-Id"), Status: response.StatusCode, Body: data}, nil
}

func credentialLast4(secret string) string {
	chars := []rune(secret)
	if len(chars) > 4 {
		chars = chars[len(chars)-4:]
	}
	return string(chars)
}

type ToolAuthFailure struct {
	TenantID, ToolID guid.ID
	Title            string
}
type ToolAuthFailureObserver func(context.Context, *gorm.DB, ToolAuthFailure) error
type ToolAuthNotifications struct {
	Bind func(ToolAuthFailureObserver) error
}

func (m *Module) provideToolAuthNotifications(s *module.Services) error {
	return module.Provide(s, ToolAuthNotifications{Bind: func(fn ToolAuthFailureObserver) error {
		if fn == nil || m.toolAuthObserver != nil {
			return ErrConflict
		}
		m.toolAuthObserver = fn
		return nil
	}})
}
func (m *Module) recordToolUpstreamStatus(ctx context.Context, token relayToken, status int) error {
	if status != 401 && status != 403 && (status < 200 || status >= 400) {
		return nil
	}
	db, err := m.database()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindToolEntry(tx, m.names)
		row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(token.TenantID), q.ID.Eq(token.Tool.ToolID)).First()
		if err != nil {
			return err
		}
		failures := 0
		if status == 401 || status == 403 {
			failures = row.UpstreamAuthFailures + 1
		}
		updates := map[string]any{"upstream_auth_failures": failures}
		now := m.now()
		if failures >= 5 && (row.UpstreamAuthNotifiedAt == nil || !now.Before(row.UpstreamAuthNotifiedAt.Add(24*time.Hour))) {
			if m.toolAuthObserver == nil {
				return ErrUnavailable
			}
			if err = m.toolAuthObserver(ctx, tx, ToolAuthFailure{TenantID: token.TenantID, ToolID: row.ID, Title: row.Title}); err != nil {
				return err
			}
			updates["upstream_auth_notified_at"] = now
		}
		_, err = q.WithContext(ctx).Where(q.TenantID.Eq(token.TenantID), q.ID.Eq(row.ID)).Updates(updates)
		return err
	})
}
