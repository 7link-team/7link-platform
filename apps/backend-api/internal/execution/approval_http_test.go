package execution

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestApprovalHTTPBindsMachineContextAndInboxUsesCurrentHumanAuthority(t *testing.T) {
	f, handler := executionRoutesFixture(t)
	f.m.machines.BindRunnerContext = nil
	r := executionRouteRequest(t, handler, http.MethodPost, "/executions/100/approval-requests", map[string]any{"fence": f.fence, "request": ApprovalRequest{DispatchEpoch: 1, RequestID: "r", Tool: "Read", Input: []byte(`{}`)}}, 0, true)
	require.Equal(t, 503, r.Code, r.Body.String())
	f, v, in := approvalFixture(t)
	a := requestApproval(t, f, v, in)
	req := governance.InboxRequest{TenantID: 7, PrincipalID: 11, Kind: "INTERACTION", Limit: 10}
	items, err := f.m.interactionInbox(f.human, req)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, a.ID, items[0].ID)
	require.Equal(t, v.ID, items[0].ObjectID)
	f.authorized = false
	items, err = f.m.interactionInbox(f.human, req)
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestApprovalHTTPActualRequestDecisionAndFencedPoll(t *testing.T) {
	f, handler := executionRoutesFixture(t)
	in := validCreate()
	in.Plan.Runtime, in.Plan.AuthorizationMode = "CLAUDE_CODE", "ON_REQUEST"
	v := executionRouteData[ExecutionView](t, executionRouteRequest(t, handler, http.MethodPost, "/tenants/7/executions", in, 0, false), http.StatusCreated)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	path := "/executions/" + v.ID.String() + "/approval-requests"
	request := ApprovalRequest{DispatchEpoch: 1, RequestID: "observed-tool-request", Tool: "Bash", Input: json.RawMessage(`{"command":"git status"}`)}
	a := executionRouteData[ApprovalView](t, executionRouteRequest(t, handler, http.MethodPost, path, map[string]any{"fence": f.fence, "request": request}, 0, true), http.StatusOK)
	require.Equal(t, "PENDING", a.State)
	poll := path + "/" + a.ID.String() + "/polls"
	body := map[string]any{"fence": f.fence, "dispatch_epoch": 1}
	current := executionRouteData[ApprovalView](t, executionRouteRequest(t, handler, http.MethodPost, poll, body, 0, true), http.StatusOK)
	require.Equal(t, a.ID, current.ID)
	decided := executionRouteData[ApprovalView](t, executionRouteRequest(t, handler, http.MethodPut, path+"/"+a.ID.String()+"/decision", map[string]any{"state": "DENY", "reason": "Human rejected this exact command"}, 0, false), http.StatusOK)
	require.Equal(t, "DENY", decided.State)
	require.Equal(t, guid.ID(11), *decided.ActorID)
	current = executionRouteData[ApprovalView](t, executionRouteRequest(t, handler, http.MethodPost, poll, body, 0, true), http.StatusOK)
	require.Equal(t, "DENY", current.State)
	body["dispatch_epoch"] = 2
	require.Equal(t, http.StatusConflict, executionRouteRequest(t, handler, http.MethodPost, poll, body, 0, true).Code)
	require.EqualValues(t, 1, f.read(t, v.ID).LastSeq, "permission decisions must not manufacture runtime receipts")
}

func TestApprovalHTTPRejectsInvalidIdentityAndUnavailableStorage(t *testing.T) {
	for _, operation := range []string{"request", "poll", "decide"} {
		for _, fault := range []string{"execution_id", "approval_id", "binding_missing", "binding_denied", "storage"} {
			t.Run(operation+"/"+fault, func(t *testing.T) {
				f := executionHarness(t)
				id, a := "1", "2"
				want := http.StatusUnprocessableEntity
				switch fault {
				case "execution_id":
					id = "invalid"
				case "approval_id":
					a = "invalid"
					if operation == "request" {
						return
					}
				case "binding_missing":
					if operation == "decide" {
						return
					}
					f.m.machines.BindRunnerContext = nil
					want = http.StatusServiceUnavailable
				case "binding_denied":
					if operation == "decide" {
						return
					}
					f.m.machines.BindRunnerContext = func(context.Context, guid.ID) (context.Context, error) { return nil, fleet.ErrMachineForbidden }
					want = http.StatusForbidden
				case "storage":
					f.m.runtime = nil
					want = http.StatusServiceUnavailable
				}
				var err error
				switch operation {
				case "request":
					in := &approvalRequestInput{ExecutionInput: ExecutionInput{ExecutionID: id}}
					in.Body.Fence = f.fence
					_, err = f.m.requestApprovalHTTP(f.runner, in)
				case "poll":
					in := &approvalPollInput{ApprovalInput: ApprovalInput{ExecutionInput: ExecutionInput{ExecutionID: id}, ApprovalID: a}}
					in.Body.Fence = f.fence
					_, err = f.m.pollApprovalHTTP(f.runner, in)
				case "decide":
					_, err = f.m.decideApprovalHTTP(f.human, &approvalDecisionInput{ApprovalInput: ApprovalInput{ExecutionInput: ExecutionInput{ExecutionID: id}, ApprovalID: a}})
				}
				var status huma.StatusError
				require.ErrorAs(t, err, &status)
				require.Equal(t, want, status.GetStatus())
			})
		}
	}
}
