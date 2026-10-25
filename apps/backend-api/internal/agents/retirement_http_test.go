package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementHTTPRechecksFrozenSourceVisibilityForProgressAndDocument(t *testing.T) {
	m, db, handler, _ := agentHTTPHarness(t)
	retirementWorkFixture(m)
	m.retirementWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]delivery.RetirementAssignment, error) {
		return []delivery.RetirementAssignment{{AssignmentID: 90, ProjectID: 30, WorkItemID: 40, WorkVersion: 1, AssignmentVersion: 1, Title: "Private source task"}}, nil
	}
	allowed := true
	m.retirementWork.CheckReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error {
		if !allowed {
			return delivery.ErrWorkForbidden
		}
		return nil
	}
	m.retirementRuntime.BeginOn = func(_ context.Context, _ *gorm.DB, _, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRING", Version: version + 1}, nil
	}
	start := func() *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/tenants/1/agents/7/retirements", strings.NewReader(`{"mode":"NORMAL","reason":"Retirement source authorization check"}`)).WithContext(agentContext(t, 2))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", "5")
		req.Header.Set("Idempotency-Key", "source-start")
		handler.ServeHTTP(out, req)
		return out
	}
	created := start()
	require.Equal(t, 201, created.Code, created.Body.String())
	var envelope struct {
		Data model.Retirement `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &envelope))
	record := envelope.Data
	services := module.NewServices()
	require.NoError(t, m.provideRetirementHandover(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[RetirementHandoverConnector](services)
	require.NoError(t, err)
	checkDispatch := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			return connector.AuthorizeOn(agentContext(t, 2), tx, 1, 7, record.ID)
		})
	}
	require.NoError(t, checkDispatch(), "dispatch may copy the frozen objective only with current source visibility")
	m.retirementHandover.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
		return HandoverArtifact{ID: 800, Source: "SYSTEM", Content: "Private source document", SHA256: "source hash"}, nil
	}
	for _, suffix := range []string{"", "/handover"} {
		allowed = true
		get := func() *httptest.ResponseRecorder {
			out := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/tenants/1/agent-retirements/"+record.ID.String()+suffix, nil).WithContext(agentContext(t, 2))
			handler.ServeHTTP(out, req)
			return out
		}
		require.Equal(t, 200, get().Code)
		allowed = false
		denied := get()
		require.Equal(t, 403, denied.Code, denied.Body.String())
		require.NotContains(t, denied.Body.String(), "Private source")
	}
	deniedReplay := start()
	require.Equal(t, 403, deniedReplay.Code, deniedReplay.Body.String())
	require.NotContains(t, deniedReplay.Body.String(), "Private source")
	require.ErrorIs(t, checkDispatch(), ErrForbidden, "retaining agent_retire must not disclose the frozen objective to Runner after source read revocation")
}

func TestRetirementForcedHTTPBindsProofAndReplaysWithoutPersistingSecret(t *testing.T) {
	m, db, handler, _ := agentHTTPHarness(t)
	retirementWorkFixture(m)
	m.credentialProof = nil
	services := module.NewServices()
	proofCalls, runtimeCalls := 0, 0
	require.NoError(t, module.Provide[authn.ReauthenticationConsumer](services, authn.ReauthenticationConsumer(func(ctx context.Context, tx *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		proofCalls++
		require.NotSame(t, db, tx)
		require.EqualValues(t, 2, actor)
		require.Equal(t, iamauthn.ReauthenticationAgentForcedRetire, purpose)
		require.Equal(t, "tenant:1:agent:7", target)
		if cookie != "session=browser-session" || proof != "fresh-forced-proof" {
			return authn.ErrInvalidCredentials
		}
		return nil
	})))
	require.NoError(t, services.Seal())
	require.NoError(t, m.injectStepUp(services))
	m.retirementRuntime.ForceOn = func(_ context.Context, _ *gorm.DB, tenant, agent, id guid.ID, version int, confirmName string, loss fleet.RetirementLoss) (fleet.AgentRetirementBinding, error) {
		if confirmName != "Test agent" {
			return fleet.AgentRetirementBinding{}, fleet.ErrRetirementNameMismatch
		}
		runtimeCalls++
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRED", Version: version + 1}, nil
	}
	call := func(actor guid.ID, key, proof, cookie, name string) *httptest.ResponseRecorder {
		body, e := json.Marshal(RetirementStart{Mode: "FORCED", Reason: "Machine cannot be restored safely", ConfirmName: name, ReauthProof: proof})
		require.NoError(t, e)
		req := httptest.NewRequest(http.MethodPost, "/tenants/1/agents/7/retirements", strings.NewReader(string(body))).WithContext(agentContext(t, actor))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", "5")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Cookie", cookie)
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, req)
		return r
	}
	require.Equal(t, 403, call(2, "missing", "", "session=browser-session", "Test agent").Code)
	require.Equal(t, 403, call(2, "wrong", "wrong-proof", "session=browser-session", "Test agent").Code)
	require.Equal(t, 403, call(2, "cookie", "fresh-forced-proof", "", "Test agent").Code)
	require.Equal(t, 422, call(2, "name", "fresh-forced-proof", "session=browser-session", "test agent").Code)
	require.Zero(t, runtimeCalls)
	first := call(2, "forced-ok", "fresh-forced-proof", "session=browser-session", "Test agent")
	require.Equal(t, 201, first.Code, first.Body.String())
	require.NotContains(t, first.Body.String(), "fresh-forced-proof")
	var envelope struct {
		Data model.Retirement `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &envelope))
	require.Equal(t, "COMPLETED", envelope.Data.State)
	require.NotNil(t, envelope.Data.Precheck.Lost)
	previous := proofCalls
	replay := call(2, "forced-ok", "", "session=browser-session", "Test agent")
	require.Equal(t, 201, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	require.Equal(t, previous, proofCalls)
	require.Equal(t, 1, runtimeCalls)
	require.Equal(t, 403, call(7, "forced-ok", "", "session=browser-session", "Test agent").Code)
}

func TestRetirementHandoverHTTPAdvanceReplaysAndRechecksCurrentAuthority(t *testing.T) {
	m, db, handler, _ := agentHTTPHarness(t)
	retirementWorkFixture(m)
	m.retirementRuntime.BeginOn = func(_ context.Context, _ *gorm.DB, _, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRING", Version: version + 1}, nil
	}
	var r model.Retirement
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		r, err = m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 5, RetirementStart{Mode: "NORMAL", Reason: "Transfer actual responsibility"})
		return err
	}))
	launches := 0
	m.retirementHandover = RetirementHandover{
		LaunchOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (guid.ID, error) {
			launches++
			return 701, nil
		},
		ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
			return HandoverArtifact{}, ErrNotFound
		},
		SystemOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (HandoverArtifact, error) {
			return HandoverArtifact{}, ErrUnavailable
		},
		CancelOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return nil },
	}
	call := func(actor guid.ID, key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/tenants/1/agent-retirements/"+r.ID.String(), strings.NewReader(body)).WithContext(agentContext(t, actor))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", "1")
		req.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	body := `{"advance":true,"agent_version":6}`
	first := call(2, "advance", body)
	require.Equal(t, 200, first.Code, first.Body.String())
	require.Contains(t, first.Body.String(), `"handover_execution_id":"701"`)
	replay := call(2, "advance", body)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	require.Equal(t, 1, launches)
	require.Equal(t, 409, call(2, "fresh-stale", body).Code)
	require.Equal(t, 403, call(7, "advance", body).Code)
	require.Equal(t, 422, call(2, "ambiguous", `{"advance":true,"state":"CANCELLED","agent_version":6}`).Code) //nolint:misspell // Preserve the published domain or serialized contract spelling.
}

func TestRetirementHTTPDurableReplayCurrentAuthorityAndCancel(t *testing.T) {
	m, _, handler, api := agentHTTPHarness(t)
	retirementWorkFixture(m)
	for _, path := range []string{"/tenants/{tenant_id}/agents/{agent_principal_id}/retirements", "/tenants/{tenant_id}/agent-retirements/{retirement_id}"} {
		require.Contains(t, api.OpenAPI().Paths, path)
	}
	beginCalls, cancelCalls := 0, 0
	m.retirementRuntime.BeginOn = func(_ context.Context, _ *gorm.DB, tenant, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
		beginCalls++
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRING", Version: version + 1}, nil
	}
	m.retirementRuntime.CancelOn = func(_ context.Context, _ *gorm.DB, tenant, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
		cancelCalls++
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "STOPPED", Version: version + 1}, nil
	}
	call := func(actor guid.ID, method, path, key, version, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(agentContext(t, actor))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if version != "" {
			req.Header.Set("If-Match", version)
		}
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, req)
		return r
	}
	path := "/tenants/1/agents/7/retirements"
	body := `{"mode":"NORMAL","reason":"Transfer responsibilities safely"}`
	first := call(2, "POST", path, "retirement-start", "5", body)
	require.Equal(t, 201, first.Code, first.Body.String())
	var envelope struct {
		Data model.Retirement `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &envelope))
	id := envelope.Data.ID.String()
	replayed := call(2, "POST", path, "retirement-start", "5", body)
	require.Equal(t, 201, replayed.Code, replayed.Body.String())
	require.Equal(t, "true", replayed.Header().Get("Idempotent-Replayed"))
	require.Equal(t, 1, beginCalls)
	changedFence := call(2, "POST", path, "retirement-start", "6", body)
	require.Equal(t, 409, changedFence.Code, changedFence.Body.String())
	denied := call(9, "POST", path, "retirement-start", "5", body)
	require.Equal(t, 403, denied.Code, denied.Body.String())
	read := call(2, "GET", "/tenants/1/agent-retirements/"+id, "", "", "")
	require.Equal(t, 200, read.Code, read.Body.String())
	m.retirementHandover.ReadOn = func(_ context.Context, _ *gorm.DB, tenant, agent, retirement guid.ID) (HandoverArtifact, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 7, agent)
		require.Equal(t, envelope.Data.ID, retirement)
		return HandoverArtifact{ID: 800, Source: "SYSTEM", Content: "Actual source document", SHA256: "source hash"}, nil
	}
	document := call(2, "GET", "/tenants/1/agent-retirements/"+id+"/handover", "", "", "")
	require.Equal(t, 200, document.Code, document.Body.String())
	require.Contains(t, document.Body.String(), "Actual source document")
	require.NotContains(t, document.Body.String(), "execution_id")
	deniedDocument := call(7, "GET", "/tenants/1/agent-retirements/"+id+"/handover", "", "", "")
	require.Equal(t, 403, deniedDocument.Code, deniedDocument.Body.String())
	denied = call(7, "GET", "/tenants/1/agent-retirements/"+id, "", "", "")
	require.Equal(t, 403, denied.Code, denied.Body.String())
	canceled := call(2, "PATCH", "/tenants/1/agent-retirements/"+id, "retirement-cancel", "1", `{"state":"CANCELLED","agent_version":8}`) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	require.Equal(t, 200, canceled.Code, canceled.Body.String())
	replayed = call(2, "PATCH", "/tenants/1/agent-retirements/"+id, "retirement-cancel", "1", `{"state":"CANCELLED","agent_version":8}`) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	require.Equal(t, 200, replayed.Code, replayed.Body.String())
	require.Equal(t, "true", replayed.Header().Get("Idempotent-Replayed"))
	require.Equal(t, 1, cancelCalls)
	late := call(2, "PATCH", "/tenants/1/agent-retirements/"+id, "retirement-late", "2", `{"state":"CANCELLED","agent_version":9}`) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	require.Equal(t, 409, late.Code, late.Body.String())
	missing := call(2, "POST", path, "missing-version", "", body)
	require.Equal(t, 422, missing.Code, missing.Body.String())
	badAdvance := call(2, http.MethodPatch, "/tenants/1/agent-retirements/"+id, "bad-advance", "2", `{"state":"COMPLETED","agent_version":9}`)
	require.Equal(t, 422, badAdvance.Code, badAdvance.Body.String())
}

func TestRetirementPublicSuccessorAdvanceAndMissingArtifactCapability(t *testing.T) {
	m, db, h, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	agent, artifact := guid.ID(7), guid.ID(800)
	r := model.Retirement{ID: 900, TenantID: 1, AgentPrincipalID: agent, ActiveAgentID: &agent, Mode: "NORMAL", State: "IN_PROGRESS", Step: "SUCCESSOR", Reason: "Transfer current responsibilities", AgentVersion: 6, Version: 1, CreatedBy: 2, CreatedAt: m.now(), StepStartedAt: m.now(), Precheck: model.RetirementPrecheck{Progress: &model.RetirementProgress{HandoverArtifactID: &artifact, HandoverSource: "SYSTEM"}}}
	require.NoError(t, query.BindRetirement(db, m.names).WithContext(ctx).Create(&r))
	path := "/tenants/1/agent-retirements/900"
	require.Equal(t, 503, callAgent(t, h, ctx, "GET", path+"/handover", "", "").Code)
	m.retirementWork.PrincipalIDsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }
	m.retirementHandover.PrepareOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, ...guid.ID) error { return nil }
	m.retirementHandover.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
		return HandoverArtifact{ID: artifact, Source: "SYSTEM"}, nil
	}
	m.retirementWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]delivery.RetirementAssignment, error) {
		return nil, nil
	}
	m.retirementWork.ReleaseOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, delivery.RetirementAssignment) error {
		return nil
	}
	m.retirementWork.TransferOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.RetirementAssignment, delivery.RetirementTransferInput) (guid.ID, error) {
		return 1, nil
	}
	patch := func(version, key, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		request.Header.Set("If-Match", version)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, request)
		return out
	}
	selected := patch("1", "select-later", `{"successor_kind":"LATER","agent_version":6}`)
	require.Equal(t, 200, selected.Code, selected.Body.String())
	require.Contains(t, selected.Body.String(), `"step":"REASSIGN"`)
	advanced := patch("2", "advance-cleanup", `{"advance":true,"agent_version":6}`)
	require.Equal(t, 200, advanced.Code, advanced.Body.String())
	require.Contains(t, advanced.Body.String(), `"step":"CLEANUP"`)
	blocked := patch("3", "unavailable-cleanup", `{"advance":true,"agent_version":6}`)
	require.Equal(t, 503, blocked.Code, blocked.Body.String())
	for _, path := range []RetirementPath{{TeamPath: TeamPath{TenantID: "bad"}, RetirementID: "900"}, {TeamPath: TeamPath{TenantID: "1"}, RetirementID: "bad"}} {
		_, err := m.getRetirementHTTP(ctx, &path)
		require.Error(t, err)
		_, err = m.getRetirementHandoverHTTP(ctx, &path)
		require.Error(t, err)
	}
}
