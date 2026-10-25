package governance

import (
	"net/http"
	"testing"

	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
)

func TestSettingsHTTPETagSaveAndStaleWrite(t *testing.T) {
	m, _, handler, authority := governanceHTTPHarness(t)
	w := governanceRequest(t, handler, http.MethodGet, "/tenants/1/governance-settings", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, `"0"`, w.Header().Get("ETag"))
	value := defaultSettings().SettingsValue
	value.DefaultBudgetNotifyPct = 90
	w = governanceRequest(t, handler, http.MethodPut, "/tenants/1/governance-settings", value, `"0"`)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, `"1"`, w.Header().Get("ETag"))
	require.Contains(t, w.Body.String(), `"default_budget_notify_pct":90`)
	w = governanceRequest(t, handler, http.MethodPut, "/tenants/1/governance-settings", value, `"0"`)
	require.Equal(t, 409, w.Code, w.Body.String())
	authority.allow = false
	w = governanceRequest(t, handler, http.MethodPut, "/tenants/1/governance-settings", value, `"1"`)
	require.Equal(t, 403, w.Code)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 2})
	for _, text := range []string{"", "0", "W/\"1\"", "\"-1\"", "\"01\"", "\"bad\""} {
		_, err := parseGovernanceVersion(text)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	n, err := parseGovernanceVersion(`"0"`)
	require.NoError(t, err)
	require.Zero(t, n)
	_, err = m.getSettingsHTTP(ctx, &governanceCatalogInput{TenantID: "bad"})
	require.Error(t, err)
	_, err = m.putSettingsHTTP(ctx, &settingsPutInput{TenantID: "bad"})
	require.Error(t, err)
	_, err = m.putSettingsHTTP(ctx, &settingsPutInput{TenantID: "1", IfMatch: "bad"})
	require.Error(t, err)
	m.runtime = nil
	_, err = m.getSettingsHTTP(ctx, &governanceCatalogInput{TenantID: "1"})
	require.Error(t, err)
	_, err = m.putSettingsHTTP(ctx, &settingsPutInput{TenantID: "1", IfMatch: `"0"`})
	require.Error(t, err)
}

func TestEscalationRulesHTTPCurrentDefaultsAndVersionedUpdate(t *testing.T) {
	m, _, handler, _ := governanceHTTPHarness(t)
	w := governanceRequest(t, handler, http.MethodGet, "/tenants/1/escalation-rules", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "REWORK_LIMIT")
	rule := defaultRule("EXECUTION_STALLED").EscalationRuleValue
	w = governanceRequest(t, handler, http.MethodPut, "/tenants/1/escalation-rules/EXECUTION_STALLED", rule, `"0"`)
	require.Equal(t, 200, w.Code, w.Body.String())
	w = governanceRequest(t, handler, http.MethodPut, "/tenants/1/escalation-rules/EXECUTION_STALLED", rule, `"0"`)
	require.Equal(t, 409, w.Code, w.Body.String())
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 2})
	_, err := m.getRulesHTTP(ctx, &rulesListInput{TenantID: "bad"})
	require.Error(t, err)
	for _, in := range []*rulePutInput{{TenantID: "bad"}, {TenantID: "2"}, {TenantID: "1", IfMatch: "bad"}} {
		_, err := m.putRuleHTTP(ctx, in)
		require.Error(t, err)
	}
	m.runtime = nil
	_, err = m.getRulesHTTP(ctx, &rulesListInput{TenantID: "1"})
	require.Error(t, err)
	_, err = m.putRuleHTTP(ctx, &rulePutInput{TenantID: "1", IfMatch: `"0"`})
	require.Error(t, err)
}
