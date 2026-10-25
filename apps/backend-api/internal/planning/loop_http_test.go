package planning

import (
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestLoopHTTPCurrentAuthorityIdempotencyAndRealLifecycle(t *testing.T) {
	h := loopHarness(t)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, h.m.RegisterHTTP(t.Context(), api))
	call := func(actor guid.ID, method, path, key, match string, body any, status int) *httptest.ResponseRecorder {
		t.Helper()
		b, e := json.Marshal(body)
		require.NoError(t, e)
		req := httptest.NewRequest(method, path, strings.NewReader(string(b))).WithContext(claims(t, actor))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if match != "" {
			req.Header.Set("If-Match", match)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, status, rec.Code, rec.Body.String())
		return rec
	}
	input := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(loopJSON(testLoopDefinition())), &input))
	input["activate"] = true
	call(2, "POST", "/tenants/1/loop-contracts", "unsupported-activation", "", input, 503)
	var contractCount int64
	require.NoError(t, h.db.Table(h.m.names.LoopContracts.String()).Count(&contractCount).Error)
	require.Zero(t, contractCount)
	input["activate"] = false
	created := call(2, "POST", "/tenants/1/loop-contracts", "loop-http-create", "", input, 201)
	var envelope struct {
		Data LoopContractView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &envelope))
	contract := envelope.Data
	require.Equal(t, "DRAFT", contract.State)
	replay := call(2, "POST", "/tenants/1/loop-contracts", "loop-http-create", "", input, 201)
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	var replayEnvelope struct {
		Data LoopContractView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &replayEnvelope))
	require.Equal(t, contract, replayEnvelope.Data)
	path := "/loop-contracts/" + contract.ID.String()
	// Seed an existing active contract through the tested internal adapter;
	// public activation remains unavailable until mandatory evidence exists.
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, contract.ID, 1, "ACTIVE")
		return e
	}))
	read := call(2, "GET", path, "", "", nil, 200)
	require.Equal(t, `"2"`, read.Header().Get("ETag"))
	call(2, "GET", "/tenants/1/loop-contracts?limit=1", "", "", nil, 200)
	call(2, "PUT", path+"/state", "missing-match", "", map[string]any{"state": "PAUSED"}, 428)
	call(2, "PUT", path+"/state", "bad-match", "1", map[string]any{"state": "PAUSED"}, 422)
	paused := call(2, "PUT", path+"/state", "pause", read.Header().Get("ETag"), map[string]any{"state": "PAUSED"}, 200)
	call(2, "PUT", path+"/state", "stale", read.Header().Get("ETag"), map[string]any{"state": "ARCHIVED"}, 409)
	call(2, "PUT", path+"/state", "stale-active", read.Header().Get("ETag"), map[string]any{"state": "ACTIVE"}, 409)
	call(2, "PATCH", path, "patch-missing", "", map[string]any{"name": "Renamed check"}, 428)
	call(2, "PATCH", path, "patch-bad", "bad", map[string]any{"name": "Renamed check"}, 422)
	patched := call(2, "PATCH", path, "rename", paused.Header().Get("ETag"), map[string]any{"name": "Renamed check", "allow_overlap": true}, 200)
	call(2, "PUT", path+"/state", "resume", patched.Header().Get("ETag"), map[string]any{"state": "ACTIVE"}, 503)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, contract.ID, 4, "ACTIVE")
		return e
	}))
	active := call(2, "GET", path, "", "", nil, 200)
	var run LoopRunView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		run, _, e = h.m.openLoopRunOn(t.Context(), tx, 1, contract.ID, 1, h.m.now())
		return e
	}))
	call(2, "GET", path+"/runs?limit=1", "", "", nil, 200)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, _, e := h.m.openLoopRunOn(t.Context(), tx, 1, contract.ID, 2, h.m.now())
		return e
	}))
	page, err := h.m.loopRunsHTTP(claims(t, 2), &LoopRunListInput{LoopPath: LoopPath{ID: contract.ID.String()}, Limit: 1})
	require.NoError(t, err)
	require.True(t, page.Body.Data.HasMore)
	require.Len(t, page.Body.Data.Items, 1)
	nextPage, err := h.m.loopRunsHTTP(claims(t, 2), &LoopRunListInput{LoopPath: LoopPath{ID: contract.ID.String()}, Limit: 1, Cursor: page.Body.Data.NextCursor})
	require.NoError(t, err)
	require.False(t, nextPage.Body.Data.HasMore)
	require.Len(t, nextPage.Body.Data.Items, 1)
	// The earlier PATCH enabled overlap, so both real fires remain open.
	require.Equal(t, "OPEN", nextPage.Body.Data.Items[0].State)
	call(2, "POST", "/loop-runs/"+run.ID.String()+"/results", "not-agent", "", map[string]any{"self_check_results": []any{}}, 403)
	call(7, "POST", "/loop-runs/"+run.ID.String()+"/results", "no-cost", "", map[string]any{"self_check_results": []any{}, "check_result": map[string]any{"status": "OK"}}, 503)
	call(2, "PUT", path+"/state", "archive", active.Header().Get("ETag"), map[string]any{"state": "ARCHIVED"}, 200)
	call(7, "POST", "/loop-runs/"+run.ID.String()+"/results", "closed", "", map[string]any{"self_check_results": []any{}}, 409)
	// Losing the Agent-owner relationship cannot be repaired by replaying a key.
	h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	h.m.agents.ByPrincipalOn = nil
	call(2, "POST", "/tenants/1/loop-contracts", "loop-http-create", "", input, 503)
	// The registered tenant guard rejects this principal before the object handler.
	call(6, "GET", path, "", "", nil, 403)
}

func TestLoopListsFilterVisibilityBeforePagingAndScopeCursors(t *testing.T) {
	h := loopHarness(t)
	first := createTestLoop(t, h, false)
	second := createTestLoop(t, h, false)
	ctx := claims(t, 2)
	in := &LoopListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 1, AgentID: "7"}
	page, e := h.m.listLoopsHTTP(ctx, in)
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.True(t, page.Body.Data.HasMore)
	require.Equal(t, first.ID, page.Body.Data.Items[0].ID)
	in.Cursor = page.Body.Data.NextCursor
	page, e = h.m.listLoopsHTTP(ctx, in)
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, second.ID, page.Body.Data.Items[0].ID)
	require.False(t, page.Body.Data.HasMore)
	in.AgentID = "6"
	_, e = h.m.listLoopsHTTP(ctx, in)
	require.Error(t, e)
	for _, cursor := range []string{"?", loopCursor("wrong", 1)} {
		in.Cursor = cursor
		_, e = h.m.listLoopsHTTP(ctx, in)
		require.Error(t, e)
	}
	h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	in.AgentID = ""
	in.Cursor = ""
	page, e = h.m.listLoopsHTTP(claims(t, 6), in)
	require.NoError(t, e)
	require.Empty(t, page.Body.Data.Items)
	page, e = h.m.listLoopsHTTP(claims(t, 7), in)
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	for _, actor := range []guid.ID{8, 9} {
		_, e = h.m.listLoopsHTTP(claims(t, actor), in)
		require.Error(t, e)
	}
	for _, path := range []*LoopPath{{ID: "0"}, {ID: "999"}} {
		_, e = h.m.getLoopHTTP(ctx, path)
		require.Error(t, e)
	}
	_, found, e := h.m.loopOwner(ctx, first.ID)
	require.NoError(t, e)
	require.True(t, found)
	_, found, e = h.m.loopOwner(ctx, 999)
	require.NoError(t, e)
	require.False(t, found)
	_, found, e = h.m.loopRunOwner(ctx, 999)
	require.NoError(t, e)
	require.False(t, found)
}

func TestLoopHTTPRejectsMalformedCommandsAndUnavailableReplay(t *testing.T) {
	h := loopHarness(t)
	ctx := claims(t, 2)
	_, e := parseLoopCursor("scope", loopCursor("scope", 0))
	require.ErrorIs(t, e, ErrInvalid)
	for _, id := range []string{"0", "bad"} {
		_, e := h.m.patchLoopHTTP(ctx, &LoopPatchInput{LoopPath: LoopPath{ID: id}})
		require.Error(t, e)
		_, e = h.m.stateLoopHTTP(ctx, &LoopStateInput{LoopPath: LoopPath{ID: id}})
		require.Error(t, e)
		_, e = h.m.loopResultHTTP(ctx, &LoopResultInput{ID: id})
		require.Error(t, e)
		_, e = h.m.loopRunsHTTP(ctx, &LoopRunListInput{LoopPath: LoopPath{ID: id}, Limit: 1})
		require.Error(t, e)
	}
	_, e = h.m.createLoopHTTP(ctx, &LoopCreateInput{PlanningTenantInput: PlanningTenantInput{TenantID: "0"}})
	require.Error(t, e)
	for _, limit := range []int{0, 101} {
		_, e = h.m.listLoopsHTTP(ctx, &LoopListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: limit})
		require.Error(t, e)
	}
	_, e = h.m.listLoopsHTTP(ctx, &LoopListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "0"}, Limit: 1})
	require.Error(t, e)
	_, e = h.m.listLoopsHTTP(ctx, &LoopListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 1, AgentID: "bad"})
	require.Error(t, e)
	v := createTestLoop(t, h, false)
	_, e = h.m.loopRunsHTTP(ctx, &LoopRunListInput{LoopPath: LoopPath{ID: v.ID.String()}, Limit: 1, Cursor: "bad"})
	require.Error(t, e)
	h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = h.m.loopRunsHTTP(claims(t, 6), &LoopRunListInput{LoopPath: LoopPath{ID: v.ID.String()}, Limit: 1})
	require.Error(t, e)
	_, e = h.m.loopResultHTTP(ctx, &LoopResultInput{ID: "999"})
	require.Error(t, e)
	_, e = h.m.loopCommand(ctx, 1, "", "test", nil, func(*gorm.DB) (LoopContractView, error) {
		t.Fatal("invalid key must not execute")
		return LoopContractView{}, nil
	})
	require.Error(t, e)
	h.m.replay = nil
	_, e = h.m.loopCommand(ctx, 1, "valid", "test", nil, func(*gorm.DB) (LoopContractView, error) {
		t.Fatal("missing replay must not execute")
		return LoopContractView{}, nil
	})
	require.Error(t, e)
}

func TestLoopTenantIsolationReplayConflictAndRevokedIdentity(t *testing.T) {
	h := loopHarness(t)
	v := createTestLoop(t, h, true)
	foreign := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 2, PrincipalID: 2, SubjectType: authn.SubjectTypePrincipal})
	path := LoopPath{ID: v.ID.String()}
	_, e := h.m.getLoopHTTP(foreign, &path)
	require.Error(t, e)
	_, e = h.m.loopRunsHTTP(foreign, &LoopRunListInput{LoopPath: path, Limit: 1})
	require.Error(t, e)
	_, e = h.m.stateLoopHTTP(foreign, &LoopStateInput{LoopPath: path, Key: "foreign", IfMatch: `"1"`, Body: struct {
		State string `json:"state" enum:"ACTIVE,PAUSED,ARCHIVED"`
	}{State: "ACTIVE"}})
	require.Error(t, e)
	var run LoopRunView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		run, _, e = h.m.openLoopRunOn(t.Context(), tx, 1, v.ID, 1, h.m.now())
		return e
	}))
	owner, found, e := h.m.loopRunOwner(t.Context(), run.ID)
	require.NoError(t, e)
	require.True(t, found)
	require.Equal(t, guid.ID(1), owner.TenantID)
	_, e = h.m.loopResultHTTP(foreign, &LoopResultInput{ID: run.ID.String()})
	require.Error(t, e)
	input := &LoopPatchInput{LoopPath: path, Key: "patch-once", IfMatch: strconv.Quote(strconv.Itoa(v.Version))}
	name := "Committed once"
	input.Body.Name = &name
	first, e := h.m.patchLoopHTTP(claims(t, 2), input)
	require.NoError(t, e)
	require.Equal(t, v.Version+1, first.Body.Data.Version)
	again, e := h.m.patchLoopHTTP(claims(t, 2), input)
	require.NoError(t, e)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, first.Body.Data, again.Body.Data)
	changed := "Different request"
	input.Body.Name = &changed
	_, e = h.m.patchLoopHTTP(claims(t, 2), input)
	require.Error(t, e)
	row, e := h.m.loopOn(t.Context(), h.db, 1, v.ID)
	require.NoError(t, e)
	require.Equal(t, name, row.Name)
	require.Equal(t, v.Version+1, row.Version)
	input.Body.Name = &name
	h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	_, e = h.m.patchLoopHTTP(claims(t, 2), input)
	require.Error(t, e)
	_, e = h.m.loopResultHTTP(claims(t, 7), &LoopResultInput{ID: run.ID.String()})
	require.Error(t, e)
	row, e = h.m.loopOn(t.Context(), h.db, 1, v.ID)
	require.NoError(t, e)
	require.Equal(t, v.Version+1, row.Version)
}

func TestLoopHTTPMissingDatabaseFailsUnavailableWithoutExecution(t *testing.T) {
	m := &Module{}
	ctx := claims(t, 2)
	checks := []func() error{
		func() error { _, e := m.getLoopHTTP(ctx, &LoopPath{ID: "1"}); return e },
		func() error {
			_, e := m.listLoopsHTTP(ctx, &LoopListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 1})
			return e
		},
		func() error {
			_, e := m.loopRunsHTTP(ctx, &LoopRunListInput{LoopPath: LoopPath{ID: "1"}, Limit: 1})
			return e
		},
		func() error { _, e := m.loopResultHTTP(ctx, &LoopResultInput{ID: "1"}); return e },
		func() error {
			_, e := m.loopCommand(ctx, 1, "valid", "test", nil, func(*gorm.DB) (LoopContractView, error) {
				t.Fatal("database unavailable must not execute")
				return LoopContractView{}, nil
			})
			return e
		},
	}
	for _, check := range checks {
		e := check()
		var status huma.StatusError
		require.ErrorAs(t, e, &status)
		require.Equal(t, 503, status.GetStatus())
	}
	_, found, e := m.loopOwner(ctx, 1)
	require.ErrorIs(t, e, ErrUnavailable)
	require.False(t, found)
	_, found, e = m.loopRunOwner(ctx, 1)
	require.ErrorIs(t, e, ErrUnavailable)
	require.False(t, found)
}

func TestLoopCorruptDefinitionAndPolicyFailureDoNotProducePartialViews(t *testing.T) {
	h := loopHarness(t)
	v := createTestLoop(t, h, true)
	path := LoopPath{ID: v.ID.String()}
	failure := errors.New("policy offline")
	h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, failure
	}
	_, e := h.m.getLoopHTTP(claims(t, 6), &path)
	require.Error(t, e)
	_, e = h.m.listLoopsHTTP(claims(t, 2), &LoopListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 10})
	require.Error(t, e)
	q := query.BindLoopContracts(h.db, h.m.names)
	_, e = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.Anchor, "{")
	require.NoError(t, e)
	_, e = h.m.getLoopHTTP(claims(t, 2), &path)
	require.Error(t, e)
	h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	page, e := h.m.listLoopsHTTP(claims(t, 2), &LoopListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 10})
	require.Error(t, e)
	require.Nil(t, page)
	name := "Cannot commit"
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.patchLoopOn(claims(t, 2), tx, 1, v.ID, v.Version, LoopPatch{Name: &name})
		return e
	}))
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, v.ID, v.Version, "PAUSED")
		return e
	}))
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.openLoopReminderOn(t.Context(), tx, 1, *v.ReminderID, 1000, h.m.now())
		return e
	}))
	row, e := h.m.loopOn(t.Context(), h.db, 1, v.ID)
	require.NoError(t, e)
	require.Equal(t, v.Version, row.Version)
	require.Equal(t, v.Name, row.Name)
	require.Equal(t, "ACTIVE", row.State)
	n, e := query.BindLoopRuns(h.db, h.m.names).WithContext(t.Context()).Count()
	require.NoError(t, e)
	require.Zero(t, n)
}
