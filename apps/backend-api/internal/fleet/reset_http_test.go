package fleet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentResetHTTPMachineBindingAndExactReceipt(t *testing.T) {
	f := resetHarness(t)
	f.enqueueReset(t, 900)
	router := fleetBusinessRouter(t, f.m)
	headers := map[string]string{"Authorization": "Bearer runner"}
	body := fmt.Sprintf(`{"session_id":%q,"conn_epoch":%d}`, f.session.ID.String(), f.session.ConnEpoch)
	response := fleetHTTPRequest(router, http.MethodPost, "/machines/1/agent-reset-pulls", body, headers)
	require.Equal(t, 200, response.Code, response.Body.String())
	var out struct {
		Data []ResetCommandView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &out))
	require.Len(t, out.Data, 1)
	require.EqualValues(t, 900, out.Data[0].ID)
	body = fmt.Sprintf(`{"session_id":%q,"conn_epoch":%d,"state":"FAILED","reason_code":"UNSUPPORTED_AGENT_RUNTIME"}`, f.session.ID.String(), f.session.ConnEpoch)
	for range 2 {
		response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/agent-resets/900/result", body, headers)
		require.Equal(t, 200, response.Code, response.Body.String())
	}
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/999/agent-resets/900/result", body, headers)
	require.Equal(t, 404, response.Code, response.Body.String())
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/agent-resets/999/result", body, headers)
	require.Equal(t, 404, response.Code, response.Body.String())
}

func TestAgentResetHTTPRejectsInvalidIDsAndUnavailableRuntime(t *testing.T) {
	f := resetHarness(t)
	_, err := f.m.pullResetHTTP(f.ctx, &heartbeatInput{MachineInput: MachineInput{MachineID: "invalid"}})
	require.Error(t, err)
	_, err = f.m.reportResetHTTP(f.ctx, &resetReportInput{MachineInput: MachineInput{MachineID: "invalid"}, ResetID: "900"})
	require.Error(t, err)
	_, err = f.m.reportResetHTTP(f.ctx, &resetReportInput{MachineInput: MachineInput{MachineID: "1"}, ResetID: "invalid"})
	require.Error(t, err)
	f.m.runtime = nil
	_, err = f.m.pullResetHTTP(f.ctx, &heartbeatInput{MachineInput: MachineInput{MachineID: "1"}})
	require.Error(t, err)
	_, err = f.m.reportResetHTTP(f.ctx, &resetReportInput{MachineInput: MachineInput{MachineID: "1"}, ResetID: "900"})
	require.Error(t, err)
}
