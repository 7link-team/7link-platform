package execution

import (
	"github.com/danielgtaylor/huma/v2"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFullAutoHTTPRequiresProofAndReportsProjectPolicy(t *testing.T) {
	f, handler := executionRoutesFixture(t)
	in := validCreate()
	in.Plan.AuthorizationMode = "FULL_AUTO"
	var v ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; v, e = f.m.CreateOn(f.human, tx, 7, in); return e }))
	r := executionRouteRequest(t, handler, http.MethodPut, "/executions/"+v.ID.String()+"/full-auto-grant", map[string]any{"reauth_proof": "unverified", "reason": "Human approves the frozen plan"}, 0, false)
	require.Equal(t, http.StatusForbidden, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), "REAUTH_REQUIRED")
	r = executionRouteRequest(t, handler, http.MethodPut, "/executions/"+v.ID.String()+"/project-execution-policy", map[string]any{"expected_version": 0, "allow_full_auto": false, "reason": "Project disallows unattended execution"}, 0, false)
	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), `"allow_full_auto":false`)
	r = executionRouteRequest(t, handler, http.MethodGet, "/executions/"+v.ID.String()+"/project-execution-policy", nil, 0, false)
	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), `"version":1`)
	require.Contains(t, r.Body.String(), `"allow_full_auto":false`)

}

func TestFullAutoHttpRejectsMalformedIdentityAndUnavailableWriter(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		id := "invalid"
		expected := 422
		f := executionHarness(t)
		if unavailable {
			id = "1"
			expected = 503
			f.m.runtime = nil
		}
		t.Run("grantFullAutoHTTP"+id, func(t *testing.T) {
			_, err := f.m.grantFullAutoHTTP(f.human, &fullAutoInput{ExecutionInput: ExecutionInput{ExecutionID: id}})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("putFullAutoPolicyHTTP"+id, func(t *testing.T) {
			_, err := f.m.putFullAutoPolicyHTTP(f.human, &fullAutoPolicyInput{ExecutionInput: ExecutionInput{ExecutionID: id}})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("getFullAutoPolicyHTTP"+id, func(t *testing.T) {
			_, err := f.m.getFullAutoPolicyHTTP(f.human, &ExecutionInput{ExecutionID: id})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
	}
}
