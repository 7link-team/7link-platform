package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestToolVersionHTTPContractsDeclareRealCommands(t *testing.T) {
	m, _, _ := agentHarness(t)
	_, api := humatest.New(t)
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	for path, ids := range map[string][]string{
		"/tenants/{tenant_id}/agent-tools/{tool_id}/versions":         {"agent_tools.versions", "agent_tools.publish"},
		"/tenants/{tenant_id}/agent-tools/{tool_id}/state":            {"agent_tools.state"},
		"/tenants/{tenant_id}/agent-tools/{tool_id}/upstream-changes": {"agent_tools.upstream"},
		"/tenants/{tenant_id}/agent-tool-presets":                     {"agent_tools.presets"},
	} {
		p := api.OpenAPI().Paths[path]
		require.NotNil(t, p, path)
		for _, id := range ids {
			found := p.Get != nil && p.Get.OperationID == id || p.Post != nil && p.Post.OperationID == id
			require.True(t, found, id)
		}
	}
}

func TestToolVersionHTTPReplayReauthorizesAndLocalImportRechecksMachine(t *testing.T) {
	m, db, h, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	m.toolSourceFacts = fleet.ToolSourceFacts{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineCapabilities, error) {
		return fleet.MachineCapabilities{Discoveries: []fleet.InspectionView{{ScopeKey: "home", Discovery: []fleet.RuntimeDiscovery{{Runtime: "CODEX", State: "READY", MCP: []string{"observed"}}}}}}, nil
	}}
	create := `{"entry_key":"imported-replay","kind":"MCP_SERVER","title":"Imported","transport":"STREAMABLE_HTTP","endpoint":"https://example.invalid/mcp","content_hash":"","skill_body":"","from_local":{"machine_id":"55","runtime":"CODEX","name":"observed","scope":"home"}}`
	first := callAgent(t, h, ctx, "POST", "/tenants/1/agent-tools", "local-import", create)
	require.Equal(t, 201, first.Code, first.Body.String())
	var response struct{ Data ToolView }
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &response))
	again := callAgent(t, h, ctx, "POST", "/tenants/1/agent-tools", "local-import", create)
	require.Equal(t, 201, again.Code, again.Body.String())
	require.Equal(t, "true", again.Header().Get("Idempotent-Replayed"))
	m.toolSourceFacts = fleet.ToolSourceFacts{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineCapabilities, error) {
		return fleet.MachineCapabilities{}, errors.New("machine permission removed")
	}}
	denied := callAgent(t, h, ctx, "POST", "/tenants/1/agent-tools", "local-import", create)
	require.Equal(t, 403, denied.Code, denied.Body.String())
	path := fmt.Sprintf("/tenants/1/agent-tools/%s/versions", response.Data.ID.String())
	publish := `{"version":1,"transport":"STREAMABLE_HTTP","endpoint":"https://next.example.invalid/mcp","content_hash":"","change_note":"Reviewed replacement"}`
	first = callAgent(t, h, ctx, "POST", path, "version-publish", publish)
	require.Equal(t, 201, first.Code, first.Body.String())
	again = callAgent(t, h, ctx, "POST", path, "version-publish", publish)
	require.Equal(t, 201, again.Code, again.Body.String())
	require.Equal(t, "true", again.Header().Get("Idempotent-Replayed"))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		d, e := m.toolDetailOn(ctx, tx, 1, response.Data.ID)
		require.NoError(t, e)
		require.Len(t, d.Versions, 2)
		return nil
	}))
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	denied = callAgent(t, h, ctx, "POST", path, "version-publish", publish)
	require.Equal(t, 403, denied.Code, denied.Body.String())
}

func TestToolCatalogPublicReadsMetadataReplayAndCurrentSourceAuthority(t *testing.T) {
	m, db, h, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	created := callAgent(t, h, ctx, "POST", "/tenants/1/agent-tools", "metadata-create", `{"entry_key":"metadata-current","kind":"MCP_SERVER","title":"Before","transport":"STREAMABLE_HTTP","endpoint":"https://tools.example.test","content_hash":"","skill_body":""}`)
	require.Equal(t, 201, created.Code, created.Body.String())
	var out struct{ Data ToolView }
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &out))
	path := "/tenants/1/agent-tools/" + out.Data.ID.String()
	for _, suffix := range []string{"", "/versions"} {
		r := callAgent(t, h, ctx, "GET", path+suffix, "", "")
		require.Equal(t, 200, r.Code, r.Body.String())
		require.Contains(t, r.Body.String(), out.Data.ContentHash)
	}
	title := `{"version":1,"title":"  Reviewed title  "}`
	changed := callAgent(t, h, ctx, "POST", path+"/metadata", "title-update", title)
	require.Equal(t, 201, changed.Code, changed.Body.String())
	require.Contains(t, changed.Body.String(), `"title":"Reviewed title"`)
	replay := callAgent(t, h, ctx, "POST", path+"/metadata", "title-update", title)
	require.Equal(t, 201, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	stale := callAgent(t, h, ctx, "POST", path+"/metadata", "title-stale", title)
	require.Equal(t, 409, stale.Code, stale.Body.String())
	presets := callAgent(t, h, ctx, "GET", "/tenants/1/agent-tool-presets", "", "")
	require.Equal(t, 200, presets.Code, presets.Body.String())
	sources := "/tenants/1/agent-tool-sources?machine_id=55"
	require.Equal(t, 503, callAgent(t, h, ctx, "GET", sources, "", "").Code)
	calls := 0
	m.toolSourceFacts = fleet.ToolSourceFacts{ReadOn: func(_ context.Context, tx *gorm.DB, tenant, machine guid.ID) (fleet.MachineCapabilities, error) {
		calls++
		require.NotNil(t, tx)
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 55, machine)
		return fleet.MachineCapabilities{}, nil
	}}
	require.Equal(t, 200, callAgent(t, h, ctx, "GET", sources, "", "").Code)
	require.Equal(t, 1, calls)
	m.toolSourceFacts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineCapabilities, error) {
		return fleet.MachineCapabilities{}, errors.New("revoked source")
	}
	require.Equal(t, 403, callAgent(t, h, ctx, "GET", sources, "", "").Code)
	require.NoError(t, db.Exec("UPDATE k_agents_tool_entries SET state='ARCHIVED' WHERE id=?", out.Data.ID).Error)
	require.Equal(t, 409, callAgent(t, h, ctx, "POST", path+"/metadata", "title-archived", `{"version":2,"title":"After archive"}`).Code)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Equal(t, 403, callAgent(t, h, ctx, "GET", "/tenants/1/agent-tool-presets", "", "").Code)
	require.Equal(t, 403, callAgent(t, h, ctx, "POST", path+"/metadata", "title-update", title).Code)
}

func TestToolHTTPPathsRejectInvalidIdentitiesAndMetadataRollsBackAuditFailure(t *testing.T) {
	m, db, _, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	for _, path := range []ToolPath{{TeamPath: TeamPath{TenantID: "bad"}, ToolID: "1"}, {TeamPath: TeamPath{TenantID: "1"}, ToolID: "bad"}, {TeamPath: TeamPath{TenantID: "2"}, ToolID: "1"}} {
		_, err := m.toolDetailHTTP(ctx, &path)
		require.Error(t, err)
		_, err = m.toolVersionsHTTP(ctx, &path)
		require.Error(t, err)
		_, err = m.toolMetadataHTTP(ctx, &ToolCommandInput[ToolMetadataInput]{ToolPath: path})
		require.Error(t, err)
	}
	_, err := m.toolPresetsHTTP(ctx, &TeamPath{TenantID: "2"})
	require.Error(t, err)
	for _, in := range []ToolSourcesInput{{TeamPath: TeamPath{TenantID: "2"}, MachineID: "55"}, {TeamPath: TeamPath{TenantID: "1"}, MachineID: "bad"}} {
		_, err = m.toolSourcesHTTP(ctx, &in)
		require.Error(t, err)
	}
	var tool ToolView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		tool, e = m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "metadata-rollback", Kind: "SKILL", Title: "Original", SkillBody: "Read actual evidence"})
		return e
	}))
	for _, in := range []ToolMetadataInput{{Version: 0, Title: "Valid"}, {Version: 1, Title: " "}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.updateToolMetadataOn(ctx, tx, 1, tool.ID, in); return e }), ErrInvalid)
	}
	original := m.audit
	m.audit = nil
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.updateToolMetadataOn(ctx, tx, 1, tool.ID, ToolMetadataInput{Version: 1, Title: "Lost"})
		return e
	}))
	m.audit = original
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		view, e := m.toolDetailOn(ctx, tx, 1, tool.ID)
		require.NoError(t, e)
		require.Equal(t, "Original", view.Entry.Title)
		require.Equal(t, 1, view.Entry.Version)
		return nil
	}))
}

func TestToolPublicStateAndUpstreamPersistReviewedFacts(t *testing.T) {
	m, db, h, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	var tool ToolView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		tool, e = m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "public-state", Kind: "MCP_SERVER", Title: "Current", Transport: "STREAMABLE_HTTP", Endpoint: "https://tools.example.test"})
		return e
	}))
	path := "/tenants/1/agent-tools/" + tool.ID.String()
	upstream := callAgent(t, h, ctx, "POST", path+"/upstream-changes", "actual-upstream", `{"version":1,"content_hash":"`+strings.Repeat("b", 64)+`","note":"Observed actual upstream release"}`)
	require.Equal(t, 201, upstream.Code, upstream.Body.String())
	require.Contains(t, upstream.Body.String(), `"upstream_changed":true`)
	state := callAgent(t, h, ctx, "POST", path+"/state", "actual-disable", `{"version":2,"state":"DISABLED","reason":"Current review is pending"}`)
	require.Equal(t, 201, state.Code, state.Body.String())
	require.Contains(t, state.Body.String(), `"state":"DISABLED"`)
}
