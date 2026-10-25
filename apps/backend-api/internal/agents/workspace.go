package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

var errWorkspaceLarge = errors.New("agents: workspace file too large")

type WorkspaceEntry struct {
	Name string `json:"name" maxLength:"255"`
	Kind string `json:"kind" enum:"DIRECTORY,FILE"`
	Size int64  `json:"size" minimum:"0"`
}
type WorkspacePage struct {
	Path    string           `json:"path"`
	Items   []WorkspaceEntry `json:"items"`
	Total   int              `json:"total"`
	HasMore bool             `json:"has_more"`
}
type WorkspaceContent struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type workspaceReadInput struct {
	AgentPath
	Path   string `query:"path" maxLength:"2048"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset int    `query:"offset" default:"0" minimum:"0" maximum:"1000"`
	Search string `query:"search" maxLength:"200"`
}
type WorkspaceReply struct {
	Items   []WorkspaceEntry `json:"items" maxItems:"1000"`
	Content string           `json:"content" maxLength:"1048576"`
	Reason  string           `json:"reason,omitempty" enum:"INVALID_PATH,TOO_LARGE,UNAVAILABLE,NOT_FOUND"`
}
type workspaceCommand struct {
	ID               guid.ID `json:"id"`
	AgentPrincipalID guid.ID `json:"agent_principal_id"`
	fleet.AgentWorkspaceFence
	Path      string    `json:"path"`
	Kind      string    `json:"kind" enum:"LIST,CONTENT"`
	ExpiresAt time.Time `json:"expires_at"`
}
type workspacePending struct {
	command workspaceCommand
	tenant  guid.ID
	result  chan WorkspaceReply
}

// Only active synchronous requests live in memory. Content never enters SQL,
// outbox, audit or a replay store. Restart and timeout discard the request.
type workspaceRequests struct {
	mu      sync.Mutex
	pending map[guid.ID]*workspacePending
}
type workspaceMachineInput struct {
	MachineID string `path:"machine_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body      struct {
		SessionID guid.ID `json:"session_id"`
		ConnEpoch int64   `json:"conn_epoch" minimum:"1"`
	}
}
type workspaceResultInput struct {
	MachineID string `path:"machine_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RequestID string `path:"request_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body      struct {
		SessionID guid.ID `json:"session_id"`
		ConnEpoch int64   `json:"conn_epoch" minimum:"1"`
		WorkspaceReply
	}
}
type workspaceOutput[T any] struct {
	CacheControl string `header:"Cache-Control"`
	Body         ProtocolEnvelope[T]
}

func workspaceOK[T any](ctx context.Context, v T) *workspaceOutput[T] {
	return &workspaceOutput[T]{CacheControl: "no-store", Body: protocolOK(ctx, v).Body}
}
func privateResponseHeaders(ctx huma.Context, next func(huma.Context)) {
	ctx.SetHeader("Cache-Control", "no-store")
	next(ctx)
}
func (m *Module) registerWorkspaceHTTP(api huma.API) error {
	op := func(id, method, p, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: p, Summary: summary, Tags: []string{"tenants"}, MaxBodyBytes: 2 << 20, Middlewares: huma.Middlewares{privateResponseHeaders}, Errors: []int{401, 403, 404, 409, 413, 422, 500, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, op("agent_workspace.list", "GET", agentPath+"/workspace-entries", "Read the restricted Agent private directory"), m.workspaceListHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_workspace.content", "GET", agentPath+"/workspace-entries/content", "Read bounded Agent private text content"), m.workspaceContentHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_workspace.access", "GET", agentPath+"/workspace-access", "Check current restricted Agent workspace access"), m.workspaceAccessHTTP)
	authz.RegisterAuthenticated(m.registrar, api, op("agent_workspace.pull", "POST", "/machines/{machine_id}/agent-workspace-pulls", "Pull transient fenced private directory reads"), m.workspacePullHTTP)
	authz.RegisterAuthenticated(m.registrar, api, op("agent_workspace.result", "PUT", "/machines/{machine_id}/agent-workspace-reads/{request_id}/result", "Return a fenced transient private directory read"), m.workspaceResultHTTP)
	if !m.registrar.IsDeclarationOnly() {
		for _, p := range []string{"/machines/{machine_id}/agent-workspace-pulls", "/machines/{machine_id}/agent-workspace-reads/{request_id}/result"} {
			if err := authz.RegisterRequestContract(api, p); err != nil {
				return err
			}
		}
	}
	return nil
}
func (m *Module) workspaceAccessHTTP(ctx context.Context, in *AgentPath) (*workspaceOutput[bool], error) {
	tenant, agent, e := pathIDs(ctx, *in)
	if e != nil {
		return nil, workspaceError(ctx, e)
	}
	db, e := m.database()
	if e != nil {
		return nil, workspaceError(ctx, e)
	}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := m.authorize(ctx, tx, tenant, agent, "agent_workspace_read", true)
		return err
	})
	if e != nil {
		return nil, workspaceError(ctx, e)
	}
	return workspaceOK(ctx, true), nil
}
func validWorkspacePath(p string) bool {
	if len(p) > 2048 || !utf8.ValidString(p) || strings.ContainsAny(p, "\\:\x00") || strings.HasPrefix(p, "/") {
		return false
	}
	if p == "" {
		return true
	}
	return path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../")
}
func workspaceError(ctx context.Context, e error) error {
	if errors.Is(e, errWorkspaceLarge) {
		return respx.ErrStatus(ctx, http.StatusRequestEntityTooLarge, 142028, "agents.invalid", nil)
	}
	if errors.Is(e, fleet.ErrMachineOffline) || errors.Is(e, fleet.ErrSessionStale) {
		e = ErrConflict
	}
	return agentHTTPError(ctx, e)
}
func (m *Module) workspaceBindingOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (fleet.AgentWorkspaceFence, error) {
	if _, e := m.authorize(ctx, tx, tenant, agent, "agent_workspace_read", true); e != nil {
		return fleet.AgentWorkspaceFence{}, e
	}
	if m.workspaceMachine.BindingOn == nil {
		return fleet.AgentWorkspaceFence{}, ErrUnavailable
	}
	return m.workspaceMachine.BindingOn(ctx, tx, tenant, agent)
}
func (m *Module) workspaceRead(ctx context.Context, in *workspaceReadInput, kind string) (WorkspaceReply, error) {
	var empty WorkspaceReply
	tenant, agent, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return empty, e
	}
	if !validWorkspacePath(in.Path) || in.Limit < 1 || in.Limit > 200 || in.Offset < 0 || in.Offset > 1000 || utf8.RuneCountInString(in.Search) > 200 {
		return empty, ErrInvalid
	}
	db, e := m.database()
	if e != nil {
		return empty, e
	}
	var fence fleet.AgentWorkspaceFence
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		fence, err = m.workspaceBindingOn(ctx, tx, tenant, agent)
		return err
	})
	if e != nil {
		return empty, e
	}
	id, e := m.ids.Next()
	if e != nil {
		return empty, e
	}
	pending := &workspacePending{tenant: tenant, result: make(chan WorkspaceReply, 1), command: workspaceCommand{ID: guid.ID(id), AgentPrincipalID: agent, AgentWorkspaceFence: fence, Path: in.Path, Kind: kind, ExpiresAt: m.now().Add(20 * time.Second)}}
	m.workspace.mu.Lock()
	if m.workspace.pending == nil {
		m.workspace.pending = map[guid.ID]*workspacePending{}
	}
	if len(m.workspace.pending) >= 64 {
		m.workspace.mu.Unlock()
		return empty, ErrUnavailable
	}
	m.workspace.pending[guid.ID(id)] = pending
	m.workspace.mu.Unlock()
	defer func() { m.workspace.mu.Lock(); delete(m.workspace.pending, guid.ID(id)); m.workspace.mu.Unlock() }()
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	var result WorkspaceReply
	select {
	case <-ctx.Done():
		return empty, ctx.Err()
	case <-timer.C:
		return empty, ErrUnavailable
	case result = <-pending.result:
	}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := m.workspaceBindingOn(ctx, tx, tenant, agent)
		if err != nil {
			return err
		}
		if current != fence {
			return ErrConflict
		}
		sum := sha256.Sum256([]byte(in.Path))
		event := audit.NewDomainEvent(ctx, tenant, "fleet.agent_workspace_read", "agent", agent)
		event.Detail["authority_source"] = "RELATION:agent_workspace_read"
		event.Detail["path_sha256"] = hex.EncodeToString(sum[:])
		event.Detail["kind"] = kind
		if m.audit == nil {
			return ErrUnavailable
		}
		return m.audit(ctx, tx, event)
	})
	if e != nil {
		return empty, e
	}
	switch result.Reason {
	case "INVALID_PATH":
		return empty, ErrInvalid
	case "TOO_LARGE":
		return empty, errWorkspaceLarge
	case "NOT_FOUND":
		return empty, ErrNotFound
	case "UNAVAILABLE":
		return empty, ErrUnavailable
	}
	return result, nil
}
func (m *Module) workspaceListHTTP(ctx context.Context, in *workspaceReadInput) (*workspaceOutput[WorkspacePage], error) {
	r, e := m.workspaceRead(ctx, in, "LIST")
	if e != nil {
		return nil, workspaceError(ctx, e)
	}
	items := []WorkspaceEntry{}
	for _, item := range r.Items {
		if strings.Contains(strings.ToLower(item.Name), strings.ToLower(in.Search)) {
			items = append(items, item)
		}
	}
	total := len(items)
	start := min(in.Offset, total)
	end := min(start+in.Limit, total)
	return workspaceOK(ctx, WorkspacePage{Path: in.Path, Items: items[start:end], Total: total, HasMore: end < total}), nil
}
func (m *Module) workspaceContentHTTP(ctx context.Context, in *workspaceReadInput) (*workspaceOutput[WorkspaceContent], error) {
	r, e := m.workspaceRead(ctx, in, "CONTENT")
	if e != nil {
		return nil, workspaceError(ctx, e)
	}
	return workspaceOK(ctx, WorkspaceContent{Path: in.Path, Content: r.Content}), nil
}
func (m *Module) validateWorkspaceMachine(ctx context.Context, tx *gorm.DB, tenant, machine, session guid.ID, epoch int64) error {
	if tenant != tenantID(ctx) || m.workspaceMachine.ValidateOn == nil {
		return ErrForbidden
	}
	return m.workspaceMachine.ValidateOn(ctx, tx, tenant, machine, session, epoch)
}
func (m *Module) workspacePullHTTP(ctx context.Context, in *workspaceMachineInput) (*workspaceOutput[[]workspaceCommand], error) {
	machine, e := guid.RequireID(in.MachineID)
	if e != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	if m.workspaceMachine.BindContext == nil {
		return nil, agentHTTPError(ctx, ErrUnavailable)
	}
	ctx, e = m.workspaceMachine.BindContext(ctx, machine)
	if e != nil {
		return nil, workspaceError(ctx, e)
	}
	tenant := tenantID(ctx)
	db, e := m.database()
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return m.validateWorkspaceMachine(ctx, tx, tenant, machine, in.Body.SessionID, in.Body.ConnEpoch)
	})
	if e != nil {
		return nil, workspaceError(ctx, e)
	}
	out := []workspaceCommand{}
	m.workspace.mu.Lock()
	defer m.workspace.mu.Unlock()
	for _, p := range m.workspace.pending {
		c := p.command
		if p.tenant == tenant && c.MachineID == machine && c.SessionID == in.Body.SessionID && c.ConnEpoch == in.Body.ConnEpoch && c.ExpiresAt.After(m.now()) {
			out = append(out, c)
			break
		}
	}
	return workspaceOK(ctx, out), nil
}
func (m *Module) workspaceResultHTTP(ctx context.Context, in *workspaceResultInput) (*workspaceOutput[bool], error) {
	machine, e := guid.RequireID(in.MachineID)
	if e != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.RequestID)
	if e != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	r := in.Body.WorkspaceReply
	if len(r.Items) > 1000 || len(r.Content) > 1048576 || !utf8.ValidString(r.Content) {
		return nil, workspaceError(ctx, errWorkspaceLarge)
	}
	for _, entry := range r.Items {
		if entry.Name == "" || strings.ContainsAny(entry.Name, "/\\:\x00") || entry.Name == "." || entry.Name == ".." || len(entry.Name) > 255 || !utf8.ValidString(entry.Name) || entry.Size < 0 || (entry.Kind != "FILE" && entry.Kind != "DIRECTORY") {
			return nil, agentHTTPError(ctx, ErrInvalid)
		}
	}
	if m.workspaceMachine.BindContext == nil {
		return nil, agentHTTPError(ctx, ErrUnavailable)
	}
	ctx, e = m.workspaceMachine.BindContext(ctx, machine)
	if e != nil {
		return nil, workspaceError(ctx, e)
	}
	tenant := tenantID(ctx)
	db, e := m.database()
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return m.validateWorkspaceMachine(ctx, tx, tenant, machine, in.Body.SessionID, in.Body.ConnEpoch)
	})
	if e != nil {
		return nil, workspaceError(ctx, e)
	}
	m.workspace.mu.Lock()
	defer m.workspace.mu.Unlock()
	p := m.workspace.pending[id]
	if p == nil || p.tenant != tenant || p.command.MachineID != machine || p.command.SessionID != in.Body.SessionID || p.command.ConnEpoch != in.Body.ConnEpoch || !p.command.ExpiresAt.After(m.now()) {
		return nil, agentHTTPError(ctx, ErrConflict)
	}
	select {
	case p.result <- r:
	default:
		return nil, agentHTTPError(ctx, ErrConflict)
	}
	return workspaceOK(ctx, true), nil
}
