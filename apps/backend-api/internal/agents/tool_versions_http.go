package agents

import (
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"unicode/utf8"
)

type ToolPath struct {
	TeamPath
	ToolID string `path:"tool_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type ToolCommandInput[T any] struct {
	ToolPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           T
}
type ToolMetadataInput struct {
	Version int    `json:"version" minimum:"1"`
	Title   string `json:"title" minLength:"1" maxLength:"200"`
}
type ToolSourcesInput struct {
	TeamPath
	MachineID string `query:"machine_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) registerToolVersionsHTTP(api huma.API) error {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: "agent_tools." + id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, op("presets", http.MethodGet, "/agent-tool-presets", "Read built-in public tool templates", 200), m.toolPresetsHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("sources", http.MethodGet, "/agent-tool-sources", "Read authorized persisted machine tool sources", 200), m.toolSourcesHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("detail", http.MethodGet, "/agent-tools/{tool_id}", "Read published tool versions and upstream reports", 200), m.toolDetailHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("versions", http.MethodGet, "/agent-tools/{tool_id}/versions", "Read immutable published configuration history", 200), m.toolVersionsHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("publish", http.MethodPost, "/agent-tools/{tool_id}/versions", "Publish a new immutable tool configuration", 201), m.toolPublishHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("state", http.MethodPost, "/agent-tools/{tool_id}/state", "Enable disable archive or revoke a tool version", 201), m.toolStateHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("upstream", http.MethodPost, "/agent-tools/{tool_id}/upstream-changes", "Report upstream content without changing approved configuration", 201), m.toolUpstreamHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("metadata", http.MethodPost, "/agent-tools/{tool_id}/metadata", "Update catalog metadata with revision fencing", 201), m.toolMetadataHTTP)
	if !m.registrar.IsDeclarationOnly() {
		for _, p := range []string{"/agent-tools/{tool_id}/versions", "/agent-tools/{tool_id}/state", "/agent-tools/{tool_id}/upstream-changes", "/agent-tools/{tool_id}/metadata"} {
			if err := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+p); err != nil {
				return err
			}
		}
	}
	return nil
}
func toolPathIDs(ctx context.Context, in ToolPath) (guid.ID, guid.ID, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return 0, 0, ErrInvalid
	}
	tool, e := guid.RequireID(in.ToolID)
	if e != nil {
		return 0, 0, ErrInvalid
	}
	if tenant != tenantID(ctx) {
		return 0, 0, ErrForbidden
	}
	return tenant, tool, nil
}
func (m *Module) toolDetailHTTP(ctx context.Context, in *ToolPath) (*ProtocolBody[ToolDetail], error) {
	tenant, id, e := toolPathIDs(ctx, *in)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (ToolDetail, error) { return m.toolDetailOn(ctx, tx, tenant, id) })
}
func (m *Module) toolVersionsHTTP(ctx context.Context, in *ToolPath) (*ProtocolBody[[]ToolVersionView], error) {
	tenant, id, e := toolPathIDs(ctx, *in)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) ([]ToolVersionView, error) {
		v, e := m.toolDetailOn(ctx, tx, tenant, id)
		return v.Versions, e
	})
}
func toolHTTPCommand[T any, R any](ctx context.Context, m *Module, in *ToolCommandInput[T], operation, suffix string, fn func(context.Context, *gorm.DB, guid.ID, guid.ID, T) (R, error)) (*CommandOutput[R], error) {
	tenant, id, e := toolPathIDs(ctx, in.ToolPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return agentCommand(ctx, m, in.IdempotencyKey, "agent_tools."+operation, "/agent-tools/{tool_id}"+suffix, map[string]string{"tenant_id": in.TenantID, "tool_id": in.ToolID}, in.Body, func(tx *gorm.DB) error { return m.teamToolAuthority(ctx, tx, tenant) }, func(tx *gorm.DB) (R, error) { return fn(ctx, tx, tenant, id, in.Body) })
}
func (m *Module) toolPublishHTTP(ctx context.Context, in *ToolCommandInput[ToolPublishInput]) (*CommandOutput[ToolVersionView], error) {
	return toolHTTPCommand(ctx, m, in, "publish", "/versions", m.publishToolOn)
}
func (m *Module) toolStateHTTP(ctx context.Context, in *ToolCommandInput[ToolStateChange]) (*CommandOutput[ToolView], error) {
	return toolHTTPCommand(ctx, m, in, "state", "/state", m.changeToolStateOn)
}
func (m *Module) toolUpstreamHTTP(ctx context.Context, in *ToolCommandInput[ToolUpstreamInput]) (*CommandOutput[ToolDetail], error) {
	return toolHTTPCommand(ctx, m, in, "upstream", "/upstream-changes", m.reportToolUpstreamOn)
}
func (m *Module) toolMetadataHTTP(ctx context.Context, in *ToolCommandInput[ToolMetadataInput]) (*CommandOutput[ToolView], error) {
	return toolHTTPCommand(ctx, m, in, "metadata", "/metadata", m.updateToolMetadataOn)
}
func (m *Module) updateToolMetadataOn(ctx context.Context, tx *gorm.DB, tenant, tool guid.ID, in ToolMetadataInput) (ToolView, error) {
	if in.Version < 1 || strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 200 {
		return ToolView{}, ErrInvalid
	}
	r, e := m.toolLockedOn(ctx, tx, tenant, tool, in.Version)
	if e != nil {
		return ToolView{}, e
	}
	if r.State == "ARCHIVED" {
		return ToolView{}, ErrConflict
	}
	q := query.BindToolEntry(tx, m.names)
	res, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(tool), q.Version.Eq(in.Version)).Updates(map[string]any{"title": strings.TrimSpace(in.Title), "version": in.Version + 1})
	if e != nil {
		return ToolView{}, e
	}
	if res.RowsAffected != 1 {
		return ToolView{}, ErrConflict
	}
	r.Title = strings.TrimSpace(in.Title)
	r.Version++
	if e = m.recordToolChange(ctx, tx, tenant, tool, "TOOL_METADATA_CHANGED", "Catalog title updated", r.Version, 0); e != nil {
		return ToolView{}, e
	}
	return toolView(r), nil
}
func (m *Module) toolPresetsHTTP(ctx context.Context, in *TeamPath) (*ProtocolBody[[]ToolPreset], error) {
	if in.TenantID != tenantID(ctx).String() {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) ([]ToolPreset, error) {
		if e := m.teamToolAuthority(ctx, tx, tenantID(ctx)); e != nil {
			return nil, e
		}
		return toolPresets(), nil
	})
}
func (m *Module) toolSourcesHTTP(ctx context.Context, in *ToolSourcesInput) (*ProtocolBody[fleet.MachineCapabilities], error) {
	if in.TenantID != tenantID(ctx).String() {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	machine, e := guid.RequireID(in.MachineID)
	if e != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (fleet.MachineCapabilities, error) {
		if e := m.teamToolAuthority(ctx, tx, tenantID(ctx)); e != nil {
			return fleet.MachineCapabilities{}, e
		}
		if m.toolSourceFacts.ReadOn == nil {
			return fleet.MachineCapabilities{}, ErrUnavailable
		}
		v, e := m.toolSourceFacts.ReadOn(ctx, tx, tenantID(ctx), machine)
		if e != nil {
			return v, ErrForbidden
		}
		return v, nil
	})
}
