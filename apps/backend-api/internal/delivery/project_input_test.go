package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
)

func projectContractAPI(t *testing.T) (humatest.TestAPI, *projectCreateBody) {
	t.Helper()
	_, api := humatest.New(t)
	captured := new(projectCreateBody)
	huma.Register(api, huma.Operation{OperationID: "test-project-create", Method: http.MethodPost, Path: "/projects", Summary: "Validate project", Tags: []string{"projects"}}, func(_ context.Context, in *struct{ Body projectCreateBody }) (*struct{ Body string }, error) {
		*captured = in.Body
		return &struct{ Body string }{Body: "accepted"}, nil
	})
	return api, captured
}

func TestProjectCreateContractDefaultsAndExactValues(t *testing.T) {
	api, got := projectContractAPI(t)
	response := api.Post("/projects", strings.NewReader(`{"name":"项目","code":"AB","contacts":[{"intake_contact_id":"123"}],"repositories":[{"name":"api","remote_url":"git@example.org:team/api.git"}]}`))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 80, got.BudgetNotifyPct)
	require.Nil(t, got.BudgetCapMicros)
	require.True(t, *got.Contacts[0].CanAccept)
	require.True(t, *got.Contacts[0].ReceivesDeliveries)
	require.Equal(t, "CC", got.Contacts[0].Role)
	require.Equal(t, "main", got.Repositories[0].DefaultBranch)
	require.Equal(t, []string{"main"}, got.Repositories[0].ProtectedBranches)
	require.Equal(t, "TEAM_ONLY", got.PortalSettings.ContributorDisplay)
	for _, value := range []*bool{got.PortalSettings.ShowAILabel, got.PortalSettings.ShowWorkItemTitles, got.PortalSettings.ShowEvidenceSummary, got.PortalSettings.ShowReleaseNotes, got.PortalSettings.MilestonesVisible} {
		require.NotNil(t, value)
		require.True(t, *value)
	}
	response = api.Post("/projects", strings.NewReader(`{"name":"项目","code":"AB","budget_cap_micros":9007199254740993,"budget_notify_pct":50,"target_date":"2028-02-29","contacts":[{"intake_contact_id":"123","receives_deliveries":false,"can_accept":false}],"portal_settings":{"show_ai_label":false,"milestones_visible":false},"repositories":[{"name":"api","remote_url":"ssh://git@example.org/team/api.git","protected_branches":[]}]}`))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, int64(9007199254740993), *got.BudgetCapMicros)
	require.Equal(t, "2028-02-29", *got.TargetDate)
	require.False(t, *got.Contacts[0].CanAccept)
	require.False(t, *got.Contacts[0].ReceivesDeliveries)
	require.False(t, *got.PortalSettings.ShowAILabel)
	require.False(t, *got.PortalSettings.MilestonesVisible)
	require.True(t, *got.PortalSettings.ShowReleaseNotes)
	require.Empty(t, got.Repositories[0].ProtectedBranches)
	response = api.Post("/projects", strings.NewReader(`{"name":"项目","code":"AB","budget_cap_micros":null,"description":null,"target_date":null}`))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Nil(t, got.BudgetCapMicros)
	require.Nil(t, got.TargetDate)
}

func TestProjectCreateContractRejectsInvalidFields(t *testing.T) {
	cases := []struct {
		name     string
		field    string
		value    any
		location string
	}{
		{"name length", "name", strings.Repeat("字", 201), "body.name"},
		{"empty name", "name", "", "body.name"},
		{"code", "code", "lower", "body.code"},
		{"description", "description", strings.Repeat("字", 2001), "body.description"},
		{"date", "target_date", "2027-02-29", "body.target_date"},
		{"date time", "target_date", "2028-02-29T00:00:00Z", "body.target_date"},
		{"budget zero", "budget_cap_micros", 0, "body.budget_cap_micros"},
		{"budget fraction", "budget_cap_micros", 1.5, "body.budget_cap_micros"},
		{"notify low", "budget_notify_pct", 49, "body.budget_notify_pct"},
		{"notify high", "budget_notify_pct", 96, "body.budget_notify_pct"},
		{"client neither", "client", map[string]any{}, "body.client"},
		{"client both", "client", map[string]any{"entity_id": "123", "new_name": "Client"}, "body.client"},
		{"contact null id", "contacts", []any{map[string]any{"intake_contact_id": nil}}, "body.contacts[0].intake_contact_id"},
		{"primary acceptance", "contacts", []any{map[string]any{"intake_contact_id": "123", "role": "PRIMARY", "can_accept": false}}, "body.contacts[0].can_accept"},
		{"two primary contacts", "contacts", []any{map[string]any{"intake_contact_id": "123", "role": "PRIMARY"}, map[string]any{"intake_contact_id": "124", "role": "PRIMARY"}}, "body.contacts[1].role"},
		{"portal enum", "portal_settings", map[string]any{"contributor_display": "NAMES"}, "body.portal_settings.contributor_display"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, _ := projectContractAPI(t)
			body := map[string]any{"name": "Project", "code": "AB", tc.field: tc.value}
			response := api.Post("/projects", body)
			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), tc.location)
		})
	}
	for _, tc := range []struct {
		field string
		count int
		item  any
	}{
		{"contacts", 21, map[string]any{"intake_contact_id": "123"}},
		{"members", 51, map[string]any{"principal_id": "123", "role_key": "engineer"}},
		{"repositories", 11, map[string]any{"name": "api", "remote_url": "https://example.org/api.git"}},
	} {
		t.Run(tc.field+" limit", func(t *testing.T) {
			api, _ := projectContractAPI(t)
			items := make([]any, tc.count)
			for i := range items {
				items[i] = tc.item
			}
			response := api.Post("/projects", map[string]any{"name": "Project", "code": "AB", tc.field: items})
			require.Equal(t, 422, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "body."+tc.field)
		})
	}
}

func TestProjectRepositoryContract(t *testing.T) {
	for _, raw := range []string{"https://example.org/team/api.git", "ssh://git@example.org/team/api.git", "ssh://example.org/team/api.git", "git@example.org:team/api.git"} {
		require.True(t, validRepositoryURL(raw), raw)
	}
	for _, raw := range []string{"file:///tmp/repo", "http://example.org/repo", "https://user:secret@example.org/repo", "https://secret@example.org/repo", "ssh://git:secret@example.org/repo", "https://example.org/repo?token=secret", "https://example.org/repo#secret", "git@:repo", "git@example.org:", "git@bad/host:repo", "https://example.org", "https://example.org/", "ssh://@example.org/repo", "https://%zz/repo", " https://example.org/repo"} {
		require.False(t, validRepositoryURL(raw), raw)
	}
	api, _ := projectContractAPI(t)
	response := api.Post("/projects", strings.NewReader(`{"name":"Project","code":"AB","repositories":[{"name":"api","remote_url":"https://example.org/repo","is_primary":true},{"name":"api","remote_url":"https://user:SECRET@example.org/repo","is_primary":true}]}`))
	require.Equal(t, 422, response.Code, response.Body.String())
	for _, field := range []string{"remote_url", "name", "is_primary"} {
		require.Contains(t, response.Body.String(), "body.repositories[1]."+field)
	}
	require.NotContains(t, response.Body.String(), "SECRET")
	schema := api.OpenAPI().Components.Schemas.Map()["ProjectCreateBody"]
	raw, err := json.Marshal(schema)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"maxItems":50`)
	require.Contains(t, string(raw), `"format":"date"`)
}
