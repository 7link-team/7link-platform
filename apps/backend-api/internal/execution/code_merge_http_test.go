package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	goosex "7link.com/platform/pkg/extension/goose"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/module"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func mergeRoutesFixture(t *testing.T) (*executionFixture, guid.ID, string, func(string, string, string, any, bool) *httptest.ResponseRecorder) {
	t.Helper()
	f, id, head := mergeFixture(t)
	items := idempotency.NewModule(f.m.runtime)
	raw, err := f.db.DB()
	require.NoError(t, err)
	source := items.MigrationSource()
	source.Prefix = "ex_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, f.m.ids))
	services.Seal()
	require.NoError(t, items.InjectServices(services))
	f.m.commentIdempotency = items
	f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	f.m.registrar = executionHTTPRegistrar(t)
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	require.NoError(t, f.m.registerCodeMergeHTTP(api))
	call := func(method, path, key string, body any, runner bool) *httptest.ResponseRecorder {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if runner {
			req.Header.Set("Authorization", "Bearer runner")
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	return f, id, head, call
}

func TestCodeMergeHTTPExactReplayCurrentAuthorityAndReadOwnership(t *testing.T) {
	f, id, head, call := mergeRoutesFixture(t)
	path := "/executions/" + id.String() + "/merges"
	body := MergeRequest{TargetBranch: "release/stable", Reason: "Integrate accepted verified work", Push: true}
	firstResponse := call(http.MethodPost, path, "merge-request", body, false)
	first := executionRouteData[MergeView](t, firstResponse, http.StatusCreated)
	require.Equal(t, "PENDING", first.State)
	require.Equal(t, head, first.SourceCommit)
	require.Nil(t, first.Observation, "request acceptance must not fabricate runtime success")
	again := call(http.MethodPost, path, "merge-request", body, false)
	require.Equal(t, first, executionRouteData[MergeView](t, again, http.StatusCreated))
	require.Equal(t, "true", again.Header().Get("Idempotent-Replayed"))
	changed := body
	changed.Push = false
	require.Equal(t, http.StatusUnprocessableEntity, call(http.MethodPost, path, "merge-request", changed, false).Code)
	require.Equal(t, http.StatusConflict, call(http.MethodPost, path, "another-request", body, false).Code)
	rows := executionRouteData[[]MergeView](t, call(http.MethodGet, path, "", nil, false), http.StatusOK)
	require.Equal(t, []MergeView{first}, rows)
	getPath := "/merges/" + first.ID.String()
	require.Equal(t, first, executionRouteData[MergeView](t, call(http.MethodGet, getPath, "", nil, false), http.StatusOK))
	require.Equal(t, http.StatusNotFound, call(http.MethodGet, "/merges/999", "", nil, false).Code)
	require.Equal(t, http.StatusNotFound, call(http.MethodGet, "/executions/999/merges", "", nil, false).Code)
	f.authorized = false
	for _, request := range []struct {
		method, path, key string
		body              any
	}{
		{http.MethodPost, path, "merge-request", body},
		{http.MethodGet, path, "", nil},
		{http.MethodGet, getPath, "", nil},
	} {
		require.Equal(t, http.StatusForbidden, call(request.method, request.path, request.key, request.body, false).Code, "current authority is required even for an exact replay")
	}
	f.authorized = true
	f.humanActive = false
	require.Equal(t, http.StatusForbidden, call(http.MethodGet, getPath, "", nil, false).Code)
	var count int64
	require.NoError(t, f.m.mergeTable(f.db).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestCodeMergeHTTPOutboxFailureRollsBackRequestAndRetryIdentity(t *testing.T) {
	f, id, _, call := mergeRoutesFixture(t)
	path := "/executions/" + id.String() + "/merges"
	body := MergeRequest{Reason: "Atomic integration request"}
	f.pub.fail = true
	failed := call(http.MethodPost, path, "atomic-request", body, false)
	require.GreaterOrEqual(t, failed.Code, 500, failed.Body.String())
	var count int64
	require.NoError(t, f.m.mergeTable(f.db).Count(&count).Error)
	require.Zero(t, count)
	var effects int64
	require.NoError(t, f.db.Table("effects").Where("kind = ?", "execution.merge_requested").Count(&effects).Error)
	require.Zero(t, effects, "audit must roll back with the failed outbox")
	f.pub.fail = false
	response := call(http.MethodPost, path, "atomic-request", body, false)
	executionRouteData[MergeView](t, response, http.StatusCreated)
	require.Empty(t, response.Header().Get("Idempotent-Replayed"))
	require.NoError(t, f.m.mergeTable(f.db).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestCodeMergeHTTPRunnerBindingFencingAndImmutableReceipt(t *testing.T) {
	f, id, head, call := mergeRoutesFixture(t)
	created := executionRouteData[MergeView](t, call(http.MethodPost, "/executions/"+id.String()+"/merges", "runner-merge", MergeRequest{Reason: "Recorded integration"}, false), http.StatusCreated)
	pull := "/runner/merge-pulls"
	wrongFence := f.fence
	wrongFence.ConnEpoch++
	require.Equal(t, http.StatusConflict, call(http.MethodPost, pull, "", wrongFence, true).Code)
	plans := executionRouteData[[]MergePlan](t, call(http.MethodPost, pull, "", f.fence, true), http.StatusOK)
	require.Len(t, plans, 1)
	raw, err := json.Marshal(plans[0])
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	observation := MergeObservation{PlanHash: hash(raw), State: "MERGED", SourceCommit: head, MergeCommit: strings.Repeat("c", 40), Reason: "MERGE_COMMITTED", StartedAt: now, EndedAt: now}
	receiptPath := "/runner/merges/" + created.ID.String() + "/receipts"
	body := map[string]any{"fence": f.fence, "nonce": strings.Repeat("f", 64), "observation": observation}
	wrongNonce := call(http.MethodPost, receiptPath, "", body, true)
	require.Equal(t, http.StatusConflict, wrongNonce.Code, wrongNonce.Body.String())
	body["nonce"] = plans[0].Nonce
	executionRouteData[bool](t, call(http.MethodPost, receiptPath, "", body, true), http.StatusOK)
	executionRouteData[bool](t, call(http.MethodPost, receiptPath, "", body, true), http.StatusOK)
	observation.Reason = "Different outcome"
	body["observation"] = observation
	require.Equal(t, http.StatusConflict, call(http.MethodPost, receiptPath, "", body, true).Code)
	stored := executionRouteData[MergeView](t, call(http.MethodGet, "/merges/"+created.ID.String(), "", nil, false), http.StatusOK)
	require.Equal(t, "MERGED", stored.State)
	require.Equal(t, "MERGE_COMMITTED", stored.Observation.Reason)
	require.False(t, stored.Observation.Pushed)
	f.m.machines.BindRunnerContext = nil
	require.Equal(t, http.StatusServiceUnavailable, call(http.MethodPost, pull, "", f.fence, true).Code)
}

func TestCodeMergeHTTPEmptyQueueStillValidatesRunnerFence(t *testing.T) {
	f, _, _, call := mergeRoutesFixture(t)
	wrongFence := f.fence
	wrongFence.ConnEpoch++
	response := call(http.MethodPost, "/runner/merge-pulls", "", wrongFence, true)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	plans := executionRouteData[[]MergePlan](t, call(http.MethodPost, "/runner/merge-pulls", "", f.fence, true), http.StatusOK)
	require.Empty(t, plans)
}

func TestCodeMergeHttpRejectsMalformedIdentityAndUnavailableWriter(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		id := "invalid"
		expected := 422
		f := executionHarness(t)
		if unavailable {
			id = "1"
			expected = 503
			f.m.runtime = nil
		}
		t.Run("requestMergeHTTP"+id, func(t *testing.T) {
			_, err := f.m.requestMergeHTTP(f.human, &mergeInput{ExecutionInput: ExecutionInput{ExecutionID: id}, IdempotencyKey: "valid"})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("getMergeHTTP"+id, func(t *testing.T) {
			_, err := f.m.getMergeHTTP(f.human, &mergeReadInput{MergeID: id})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("listMergesHTTP"+id, func(t *testing.T) {
			_, err := f.m.listMergesHTTP(f.human, &ExecutionInput{ExecutionID: id})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
	}
}

func TestMergeOwnerRejectsMissingWriterAndRecord(t *testing.T) {
	for _, missingWriter := range []bool{false, true} {
		f := executionHarness(t)
		if missingWriter {
			f.m.runtime = nil
		}
		_, found, e := f.m.mergeOwner(f.human, 999)
		if missingWriter {
			require.ErrorIs(t, e, ErrUnavailable)
		} else {
			require.NoError(t, e)
		}
		require.False(t, found)
	}
}
