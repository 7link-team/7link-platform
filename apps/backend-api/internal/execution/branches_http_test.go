package execution

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	humax "7link.com/platform/pkg/extension/huma"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestStrategyBranchHTTPRequiresCurrentMachinesAndValidCommandHeaders(t *testing.T) {
	for _, fault := range []string{"version", "idempotency_missing", "key", "machine_missing", "machine_error", "source_machine_error", "lock"} {
		t.Run(fault, func(t *testing.T) {
			f := verifierRegistryFixture(t)
			root := f.create(t)
			finishBranchSource(t, f, root)
			root = f.read(t, root.ID)
			in := &strategyBranchInput{ExecutionInput: ExecutionInput{ExecutionID: root.ID.String()}, IfMatch: strconv.Itoa(root.Version), IdempotencyKey: "branch", Body: StrategyBranch{MachineID: 1, Reason: "Alternative", Objective: "Use another algorithm"}}
			failure := errors.New("current machine unavailable")
			statusCode := 503
			switch fault {
			case "version":
				in.IfMatch = "invalid"
				statusCode = 422
			case "idempotency_missing":
				f.m.commentIdempotency = nil
			case "key":
				in.IdempotencyKey = ""
				statusCode = 422
			case "machine_missing":
				f.m.machines.ByIDOn = nil
			case "machine_error", "source_machine_error":
				old := f.m.machines.ByIDOn
				calls := 0
				f.m.machines.ByIDOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.MachineFact, error) {
					calls++
					if fault == "machine_error" || calls == 2 {
						return fleet.MachineFact{}, failure
					}
					return old(ctx, tx, tenant, id)
				}
			case "lock":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			}
			_, e := f.m.strategyBranchHTTP(f.human, in)
			var status huma.StatusError
			require.ErrorAs(t, e, &status)
			require.Equal(t, statusCode, status.GetStatus())
			var n int64
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("strategy_parent_id=?", root.ID).Count(&n).Error)
			require.Zero(t, n)
		})
	}
}

func TestStrategyBranchHTTPReplayRevocationCASAndHistory(t *testing.T) {
	f := verifierRegistryFixture(t)
	f.m.registrar = executionHTTPRegistrar(t)
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	require.NoError(t, f.m.registerBranchesHTTP(api))
	root := f.create(t)
	finishBranchSource(t, f, root)
	root = f.read(t, root.ID)
	path := "/executions/" + root.ID.String() + "/branches"
	call := func(method, url, key string, version int, body any) *httptest.ResponseRecorder {
		b, e := json.Marshal(body)
		require.NoError(t, e)
		req := httptest.NewRequest(method, url, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if version > 0 {
			req.Header.Set("If-Match", strconv.Quote(strconv.Itoa(version)))
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	in := StrategyBranch{Reason: "Alternative solution", Objective: "Use a different algorithm"}
	require.Equal(t, 422, call("POST", path, "", root.Version, in).Code)
	require.Equal(t, 409, call("POST", path, "stale", root.Version-1, in).Code)
	f.pub.fail = true
	require.Equal(t, 503, call("POST", path, "branch", root.Version, in).Code)
	f.pub.fail = false
	created := call("POST", path, "branch", root.Version, in)
	v := executionRouteData[ExecutionView](t, created, 201)
	require.Equal(t, root.ID, *v.ParentExecutionID)
	require.Equal(t, "private, no-store", created.Header().Get("Cache-Control"))
	again := call("POST", path, "branch", root.Version, in)
	require.Equal(t, v, executionRouteData[ExecutionView](t, again, 201))
	require.Equal(t, "true", again.Header().Get("Idempotent-Replayed"))
	in.Reason = "Changed request"
	require.Equal(t, 422, call("POST", path, "branch", root.Version, in).Code)
	in.Reason = "Alternative solution"
	var count int64
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("strategy_parent_id=?", root.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	first := executionRouteData[ExecutionPage](t, call("GET", path+"?limit=1", "", 0, nil), 200)
	require.True(t, first.HasMore)
	require.Equal(t, root.ID, first.Items[0].ID)
	last := executionRouteData[ExecutionPage](t, call("GET", path+"?limit=1&cursor="+first.NextCursor, "", 0, nil), 200)
	require.False(t, last.HasMore)
	require.Equal(t, v.ID, last.Items[0].ID)
	require.Equal(t, 422, call("GET", path+"?limit=101", "", 0, nil).Code)
	require.Equal(t, 404, call("GET", "/executions/999/branches", "", 0, nil).Code)
	f.authorized = false
	require.Equal(t, 403, call("POST", path, "branch", root.Version, in).Code)
	require.Equal(t, 403, call("GET", path, "", 0, nil).Code)
	f.authorized = true
	f.humanActive = false
	require.Equal(t, 403, call("POST", path, "branch", root.Version, in).Code)
}

func TestBranchesHttpRejectsMalformedIdentityAndUnavailableWriter(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		id := "invalid"
		expected := 422
		f := executionHarness(t)
		if unavailable {
			id = "1"
			expected = 503
			f.m.runtime = nil
		}
		t.Run("branchHistoryHTTP"+id, func(t *testing.T) {
			_, err := f.m.branchHistoryHTTP(f.human, &executionTraceInput{ExecutionInput: ExecutionInput{ExecutionID: id}, Limit: 10})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("strategyBranchHTTP"+id, func(t *testing.T) {
			_, err := f.m.strategyBranchHTTP(f.human, &strategyBranchInput{ExecutionInput: ExecutionInput{ExecutionID: id}, IfMatch: "1", IdempotencyKey: "valid"})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
	}
}

func TestStrategyHTTPStorageFailuresPreserveBranchAndIdempotency(t *testing.T) {
	for _, command := range []string{"create", "replay", "history"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f := verifierRegistryFixture(t)
				root := f.create(t)
				finishBranchSource(t, f, root)
				root = f.read(t, root.ID)
				in := &strategyBranchInput{ExecutionInput: ExecutionInput{ExecutionID: root.ID.String()}, IfMatch: strconv.Itoa(root.Version), IdempotencyKey: "frozen-branch", Body: StrategyBranch{MachineID: 1, Reason: "Use the frozen root", Objective: "Use a different algorithm"}}
				if command == "replay" {
					out, e := f.m.strategyBranchHTTP(f.human, in)
					require.NoError(t, e)
					require.NotEmpty(t, out.Body.Data.ID)
				}
				return f, func(*gorm.DB) error {
					if command == "history" {
						out, e := f.m.branchHistoryHTTP(f.human, &executionTraceInput{ExecutionInput: in.ExecutionInput, Limit: 1})
						if e == nil {
							require.Len(t, out.Body.Data.Items, 1)
						}
						return e
					}
					out, e := f.m.strategyBranchHTTP(f.human, in)
					if e == nil {
						require.Equal(t, 201, out.Status)
						if command == "replay" {
							require.Equal(t, "true", out.Replayed)
						}
					}
					return e
				}
			}, true)
		})
	}
}
