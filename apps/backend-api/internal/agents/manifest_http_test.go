package agents

import (
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManifestConditionalReadReturnsETagAndNoNotModifiedEnvelope(t *testing.T) {
	m, _, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	require.NoError(t, m.registry.Register(manifestTestOperation("inbox_events.list")))
	require.NoError(t, m.publishManifest(ctx))
	first, err := m.manifestVersionHTTP(ctx, &ManifestReadInput{TeamPath: TeamPath{TenantID: "1"}})
	require.NoError(t, err)
	require.Equal(t, 200, first.Status)
	require.NotNil(t, first.Body)
	second, err := m.manifestVersionHTTP(ctx, &ManifestReadInput{TeamPath: TeamPath{TenantID: "1"}, IfNoneMatch: first.ETag})
	require.NoError(t, err)
	require.Equal(t, 304, second.Status)
	require.Nil(t, second.Body)
	_, err = m.manifestVersionHTTP(ctx, &ManifestReadInput{TeamPath: TeamPath{TenantID: "2"}})
	require.Error(t, err)
}

func TestManifestHTTPConditionalReadAndBoundedVersionHistory(t *testing.T) {
	m, _, handler, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 7)
	require.NoError(t, m.publishManifest(ctx))
	first := callAgent(t, handler, ctx, "GET", "/tenants/1/agent-operation-manifest", "", "")
	require.Equal(t, 200, first.Code, first.Body.String())
	require.NotEmpty(t, first.Header().Get("ETag"))
	request := httptest.NewRequest(http.MethodGet, "/tenants/1/agent-operation-manifest", nil).WithContext(ctx)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	require.Equal(t, 304, result.Code, result.Body.String())
	require.Empty(t, result.Body.String())
	history := callAgent(t, handler, ctx, "GET", "/tenants/1/agent-operation-manifest-versions?limit=1", "", "")
	require.Equal(t, 200, history.Code, history.Body.String())
	require.Contains(t, history.Body.String(), `"manifest_version":"1.0.0"`)
	require.Equal(t, 422, callAgent(t, handler, ctx, "GET", "/tenants/1/agent-operation-manifest-versions?limit=101", "", "").Code)
	require.Equal(t, 404, callAgent(t, handler, ctx, "GET", "/tenants/1/agent-operation-manifest?version=99.0.0", "", "").Code)
}

func TestManifestHistoryPagesAndCapabilityBoundaryErrors(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	for _, name := range []string{"inbox_events.list", "agent_operations.list", "protocol.live"} {
		require.NoError(t, m.registry.Register(manifestTestOperation(name)))
		require.NoError(t, m.publishManifest(ctx))
	}
	first, err := m.manifestVersionsHTTP(ctx, &ManifestVersionsInput{TeamPath: TeamPath{TenantID: "1"}, Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.Body.Data.Items, 1)
	require.True(t, first.Body.Data.HasMore)
	require.NotEmpty(t, first.Body.Data.NextCursor)
	second, err := m.manifestVersionsHTTP(ctx, &ManifestVersionsInput{TeamPath: TeamPath{TenantID: "1"}, Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, err)
	require.NotEqual(t, first.Body.Data.Items[0].ManifestVersion, second.Body.Data.Items[0].ManifestVersion)
	for _, in := range []ManifestVersionsInput{{TeamPath: TeamPath{TenantID: "2"}}, {TeamPath: TeamPath{TenantID: "1"}, Limit: -1}, {TeamPath: TeamPath{TenantID: "1"}, Cursor: "bad"}} {
		out, err := m.manifestVersionsHTTP(ctx, &in)
		require.Error(t, err)
		require.Nil(t, out)
	}
	for _, in := range []CapabilityInput{{AgentPath: AgentPath{TenantID: "2", AgentPrincipalID: "7"}}, {AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "bad"}}, {AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "7"}, ProjectID: "bad"}} {
		out, err := m.capabilitiesHTTP(ctx, &in)
		require.Error(t, err)
		require.Nil(t, out)
	}
	db = db.Begin()
	require.NoError(t, db.Error)
	defer db.Rollback()
	_, err = m.capabilitiesOn(ctx, db, 1, 7, 3, "")
	require.ErrorIs(t, err, ErrUnavailable)
	fault := errors.New("authority source unavailable")
	m.capabilityExplain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{}, fault
	}
	_, err = m.capabilitiesOn(ctx, db, 1, 7, 3, "")
	require.ErrorIs(t, err, fault)
	m.capabilityExplain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{}, nil
	}
	_, err = m.capabilitiesOn(ctx, db, 1, 7, 3, "")
	require.ErrorIs(t, err, ErrForbidden)
}

func TestCapabilitiesExplainHumanOnlyQuotaAndUnavailableHistoricalOperations(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	for _, mode := range []string{"human", "guarded", "quota", "resource", "removed"} {
		op := manifestTestOperation("capability." + mode)
		switch mode {
		case "human":
			op.HumanOnly = true
		case "guarded":
			op.Via = "ROLE"
			op.RequiredPermission = "agent_read"
		case "quota":
			op.Quota = "RATE_LIMIT"
		case "resource":
			op.Path += "/{resource_id}"
		}
		require.NoError(t, m.registry.Register(op))
	}
	require.NoError(t, m.publishManifest(ctx))
	m.registry.mu.Lock()
	delete(m.registry.operations, "capability.removed")
	m.registry.mu.Unlock()
	db = db.Begin()
	require.NoError(t, db.Error)
	defer db.Rollback()
	read := func() map[string]EffectiveOperation {
		v, err := m.capabilitiesOn(ctx, db, 1, 7, 0, "")
		require.NoError(t, err)
		out := map[string]EffectiveOperation{}
		for _, op := range v.Operations {
			out[op.Name] = op
		}
		return out
	}
	view := read()
	require.Equal(t, "AGENT_PROHIBITED", view["capability.human"].ReasonCode)
	require.Equal(t, "AUTHORITY_UNAVAILABLE", view["capability.guarded"].ReasonCode)
	require.Equal(t, "QUOTA_CONTEXT_REQUIRED", view["capability.quota"].ReasonCode)
	require.Equal(t, "RESOURCE_CONTEXT_REQUIRED", view["capability.resource"].ReasonCode)
	require.Equal(t, "OPERATION_UNAVAILABLE", view["capability.removed"].ReasonCode)
	m.capabilityExplain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{}, nil
	}
	require.Equal(t, "MISSING_PERMISSION", read()["capability.guarded"].ReasonCode)
	fault := errors.New("explain failed")
	m.capabilityExplain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{}, fault
	}
	_, err := m.capabilitiesOn(ctx, db, 1, 7, 0, "")
	require.ErrorIs(t, err, fault)
}
