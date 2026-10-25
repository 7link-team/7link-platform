package governance

import (
	"context"
	"net/http"
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIncidentHTTPPaginationCurrentVisibilityAndDispositions(t *testing.T) {
	m, db, handler, _ := governanceHTTPHarness(t)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 2})
	for n := guid.ID(1); n <= 3; n++ {
		fact := escalationFixture()
		fact.SubjectID = n
		fact.TriggerEventID = n
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordEscalationOn(ctx, tx, fact); return e }))
	}
	first, err := m.listIncidentsHTTP(ctx, &incidentListInput{TenantID: "1", Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.Body.Data.Items, 1)
	require.True(t, first.Body.Data.HasMore)
	second, err := m.listIncidentsHTTP(ctx, &incidentListInput{TenantID: "1", Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, err)
	require.NotEqual(t, first.Body.Data.Items[0].ID, second.Body.Data.Items[0].ID)
	_, err = m.listIncidentsHTTP(ctx, &incidentListInput{TenantID: "1", Limit: 1, Cursor: first.Body.Data.NextCursor, ProjectID: "3"})
	require.Error(t, err)
	item := first.Body.Data.Items[0]
	w := governanceRequest(t, handler, http.MethodPost, "/tenants/1/escalation-incidents/"+item.ID.String()+"/acknowledgements", map[string]string{}, `"1"`)
	require.Equal(t, 200, w.Code, w.Body.String())
	w = governanceRequest(t, handler, http.MethodPost, "/tenants/1/escalation-incidents/"+item.ID.String()+"/dismissals", map[string]string{"reason": "Human reviewed the actual source"}, `"2"`)
	require.Equal(t, 200, w.Code, w.Body.String())
	w = governanceRequest(t, handler, http.MethodPost, "/tenants/1/escalation-incidents/999/dismissals", map[string]string{"reason": "Human reviewed the actual source"}, `"1"`)
	require.Equal(t, 404, w.Code, w.Body.String())
	page, err := m.listIncidentsHTTP(ctx, &incidentListInput{TenantID: "1", ProjectID: "3", State: "DISMISSED", Limit: 50})
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 1)
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: false}, nil
	}
	page, err = m.listIncidentsHTTP(ctx, &incidentListInput{TenantID: "1", Limit: 50})
	require.NoError(t, err)
	require.Empty(t, page.Body.Data.Items)
	for _, in := range []*incidentListInput{{TenantID: "bad", Limit: 1}, {TenantID: "2", Limit: 1}, {TenantID: "1", Limit: 0}, {TenantID: "1", Limit: 1, Sort: "bad"}, {TenantID: "1", Limit: 1, State: "bad"}, {TenantID: "1", Limit: 1, ProjectID: "bad"}, {TenantID: "1", Limit: 1, Cursor: "invalid"}} {
		_, err := m.listIncidentsHTTP(ctx, in)
		require.Error(t, err)
	}
	for _, in := range []*incidentDecisionInput{{TenantID: "bad"}, {TenantID: "1", ID: "bad"}, {TenantID: "1", ID: "1", IfMatch: "bad"}} {
		_, err := m.decideIncidentHTTP(ctx, in, "ACKNOWLEDGED")
		require.Error(t, err)
	}
	m.runtime = nil
	_, err = m.listIncidentsHTTP(ctx, &incidentListInput{TenantID: "1", Limit: 1})
	require.Error(t, err)
	_, err = m.decideIncidentHTTP(ctx, &incidentDecisionInput{TenantID: "1", ID: "1", IfMatch: `"1"`}, "ACKNOWLEDGED")
	require.Error(t, err)
}
