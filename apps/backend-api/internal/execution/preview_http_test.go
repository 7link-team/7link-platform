package execution

import (
	"7link.com/platform/internal/fleet"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"net/http"
	"testing"

	"7link.com/platform/internal/execution/query"
	"github.com/stretchr/testify/require"
)

func TestPreviewBrowserGrantBindingAndNoStore(t *testing.T) {
	f, handler := executionRoutesFixture(t)
	configurePreviewFixture(t, f)
	execution := f.create(t)
	session, err := f.m.CreatePreview(f.human, 7, execution.ID, 5173)
	require.NoError(t, err)
	existing := executionRouteRequest(t, handler, http.MethodPost, "/executions/"+execution.ID.String()+"/preview-sessions", map[string]any{"kind": "SERVICE", "port": 5173}, 0, false)
	require.Equal(t, http.StatusOK, existing.Code, existing.Body.String())
	created := executionRouteRequest(t, handler, http.MethodPost, "/executions/"+execution.ID.String()+"/preview-sessions", map[string]any{"kind": "SERVICE", "port": 5174}, 0, false)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	q := query.BindPreviewSessions(f.db, f.m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(session.ID)).First()
	require.NoError(t, err)
	response := executionRouteRequest(t, handler, http.MethodPost, "/preview-access-grants", map[string]any{"label": row.Label}, 0, false)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Contains(t, response.Body.String(), "https://"+row.Label+".preview.example.net/__access?code=")
	sessionGrant := executionRouteRequest(t, handler, http.MethodPost, "/preview-sessions/"+session.ID.String()+"/access-grants", map[string]any{}, 0, false)
	require.Equal(t, http.StatusCreated, sessionGrant.Code, sessionGrant.Body.String())
	require.Equal(t, "no-store", sessionGrant.Header().Get("Cache-Control"))
	denied := executionRouteRequest(t, handler, http.MethodPost, "/preview-sessions/"+session.ID.String()+"/access-grants", map[string]any{}, 0, true)
	require.Equal(t, http.StatusUnauthorized, denied.Code, denied.Body.String())
	f.authorized = false
	denied = executionRouteRequest(t, handler, http.MethodPost, "/preview-access-grants", map[string]any{"label": row.Label}, 0, false)
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
}

func TestPreviewPublicReadStopAndCurrentAuthority(t *testing.T) {
	f, h := executionRoutesFixture(t)
	configurePreviewFixture(t, f)
	f.m.preview.runtime.Ports = func(context.Context, fleet.PreviewGrant) ([]int, error) { return []int{5173, 5173}, nil }
	v := f.create(t)
	s, e := f.m.CreatePreview(f.human, 7, v.ID, 5173)
	require.NoError(t, e)
	root := "/executions/" + v.ID.String()
	session := "/preview-sessions/" + s.ID.String()
	listed := executionRouteData[[]PreviewSessionView](t, executionRouteRequest(t, h, http.MethodGet, root+"/preview-sessions", nil, 0, false), http.StatusOK)
	require.Len(t, listed, 1)
	require.Equal(t, s.ID, listed[0].ID)
	read := executionRouteData[PreviewSessionView](t, executionRouteRequest(t, h, http.MethodGet, session, nil, 0, false), http.StatusOK)
	require.Equal(t, s.ID, read.ID)
	offered := executionRouteRequest(t, h, http.MethodGet, root+"/offered-ports", nil, 0, false)
	require.Equal(t, http.StatusOK, offered.Code, offered.Body.String())
	f.authorized = false
	for _, path := range []string{root + "/preview-sessions", root + "/offered-ports", session} {
		r := executionRouteRequest(t, h, http.MethodGet, path, nil, 0, false)
		require.Equal(t, http.StatusForbidden, r.Code, r.Body.String())
	}
	denied := executionRouteRequest(t, h, http.MethodPut, session+"/termination", map[string]any{}, 0, false)
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
	f.authorized = true
	stopped := executionRouteData[PreviewSessionView](t, executionRouteRequest(t, h, http.MethodPut, session+"/termination", map[string]any{}, 0, false), http.StatusOK)
	require.Equal(t, s.ID, stopped.ID)
	require.Equal(t, "STOPPED", stopped.State)
	grant := executionRouteRequest(t, h, http.MethodPost, session+"/access-grants", map[string]any{}, 0, false)
	require.NotEqual(t, http.StatusCreated, grant.Code, grant.Body.String())
	var count int64
	require.NoError(t, f.db.Table(f.m.names.PreviewAccess.String()).Count(&count).Error)
	require.Zero(t, count)
}

func TestPreviewHttpRejectsMalformedIdentityAndUnavailableWriter(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		id := "invalid"
		expected := 422
		f := executionHarness(t)
		if unavailable {
			id = "1"
			expected = 503
			f.m.runtime = nil
		}
		t.Run("listPreviewsHTTP"+id, func(t *testing.T) {
			_, err := f.m.listPreviewsHTTP(f.human, &ExecutionInput{ExecutionID: id})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("getPreviewHTTP"+id, func(t *testing.T) {
			_, err := f.m.getPreviewHTTP(f.human, &PreviewSessionInput{SessionID: id})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("stopPreviewHTTP"+id, func(t *testing.T) {
			_, err := f.m.stopPreviewHTTP(f.human, &PreviewSessionInput{SessionID: id})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("issuePreviewAccessHTTP"+id, func(t *testing.T) {
			_, err := f.m.issuePreviewAccessHTTP(f.human, &previewAccessInput{PreviewSessionInput: PreviewSessionInput{SessionID: id}})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("offeredPreviewPortsHTTP"+id, func(t *testing.T) {
			_, err := f.m.offeredPreviewPortsHTTP(f.human, &ExecutionInput{ExecutionID: id})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
	}
}

func TestPreviewOwnerRejectsMissingWriterAndRecord(t *testing.T) {
	for _, missingWriter := range []bool{false, true} {
		f := executionHarness(t)
		if missingWriter {
			f.m.runtime = nil
		}
		_, found, e := f.m.previewOwner(f.human, 999)
		if missingWriter {
			require.ErrorIs(t, e, ErrUnavailable)
		} else {
			require.NoError(t, e)
		}
		require.False(t, found)
	}
}
