package governance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectRuleStrictnessAndEffectiveMerge(t *testing.T) {
	for _, tc := range []struct {
		kind, params string
		strict       bool
	}{
		{"LOW_SELF_CONFIDENCE", `{"min_confidence":70}`, true},
		{"LOW_SELF_CONFIDENCE", `{"min_confidence":50}`, false},
		{"EXECUTION_STALLED", `{"stall_minutes":15}`, true},
		{"EXECUTION_STALLED", `{"stall_minutes":60}`, false},
		{"CLIENT_REJECTION", `{"include_partial":false}`, false},
	} {
		t.Run(tc.kind+tc.params, func(t *testing.T) {
			floor := defaultRule(tc.kind).EscalationRuleValue
			next := floor
			next.Params = json.RawMessage(tc.params)
			require.Equal(t, tc.strict, projectRuleAtLeastAsStrict(tc.kind, floor, next))
			merged := mergeProjectRule(tc.kind, floor, next)
			require.True(t, projectRuleAtLeastAsStrict(tc.kind, floor, merged))
			require.True(t, projectRuleAtLeastAsStrict(tc.kind, next, merged))
		})
	}
	for _, kind := range EscalationKinds() {
		floor := defaultRule(kind).EscalationRuleValue
		require.True(t, projectRuleAtLeastAsStrict(kind, floor, floor), kind)
		next := floor
		next.Enabled = false
		require.False(t, projectRuleAtLeastAsStrict(kind, floor, next), kind)
		next = floor
		next.ActionsAutonomous = []string{}
		require.False(t, projectRuleAtLeastAsStrict(kind, floor, next), kind)
		next = floor
		next.ActionsHybrid = []string{}
		if len(floor.ActionsHybrid) > 0 {
			require.False(t, projectRuleAtLeastAsStrict(kind, floor, next), kind)
		}
	}
	floor := defaultRule("SENSITIVE_PATH").EscalationRuleValue
	var params sensitiveRuleParams
	require.NoError(t, json.Unmarshal(floor.Params, &params))
	params.Sets[0].Globs = append(params.Sets[0].Globs, "**/extra/**")
	next := floor
	next.Params, _ = json.Marshal(params)
	require.True(t, projectRuleAtLeastAsStrict("SENSITIVE_PATH", floor, next))
	params.Sets[0].Enabled = false
	next.Params, _ = json.Marshal(params)
	require.False(t, projectRuleAtLeastAsStrict("SENSITIVE_PATH", floor, next))
	params.Sets[0].Enabled = true
	params.Sets[0].Globs = []string{"**/*"}
	next.Params, _ = json.Marshal(params)
	require.False(t, projectRuleAtLeastAsStrict("SENSITIVE_PATH", floor, next), "arbitrary glob inclusion is not assumed")
	merged := mergeProjectRule("SENSITIVE_PATH", floor, next)
	require.True(t, projectRuleAtLeastAsStrict("SENSITIVE_PATH", floor, merged))
	require.True(t, projectRuleAtLeastAsStrict("SENSITIVE_PATH", next, merged))
	params.MaxPaths = 3000
	next.Params, _ = json.Marshal(params)
	require.False(t, projectRuleAtLeastAsStrict("SENSITIVE_PATH", floor, next))
}

func projectRuleCommand(m *Module, db *gorm.DB, ctx context.Context, project guid.ID, version int, value EscalationRuleValue) (EscalationRuleView, error) {
	var out EscalationRuleView
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.putProjectRuleOn(ctx, tx, 1, project, "LOW_SELF_CONFIDENCE", version, value, "cookie", "proof")
		return err
	})
	return out, err
}

func TestProjectRuleCASIsolationTenantTighteningAndAtomicity(t *testing.T) {
	m, db, ctx, pub, _ := delegationHarness(t)
	value := defaultRule("LOW_SELF_CONFIDENCE").EscalationRuleValue
	value.Params = json.RawMessage(`{"min_confidence":70}`)
	created, err := projectRuleCommand(m, db, ctx, 99, 0, value)
	require.NoError(t, err)
	require.Equal(t, 1, created.Version)
	_, err = projectRuleCommand(m, db, ctx, 99, 0, value)
	require.ErrorIs(t, err, ErrVersion)
	read := func(tenant, project guid.ID) EscalationRuleView {
		rules, e := m.readEffectiveRulesOn(ctx, db, tenant, project)
		require.NoError(t, e)
		for _, rule := range rules {
			if rule.Kind == "LOW_SELF_CONFIDENCE" {
				return rule
			}
		}
		t.Fatal("rule missing")
		return EscalationRuleView{}
	}
	require.JSONEq(t, `{"min_confidence":70}`, string(read(1, 99).Params))
	require.JSONEq(t, `{"min_confidence":60}`, string(read(1, 100).Params))
	require.JSONEq(t, `{"min_confidence":60}`, string(read(2, 99).Params))
	value.Params = json.RawMessage(`{"min_confidence":80}`)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putRuleOn(ctx, tx, 1, "LOW_SELF_CONFIDENCE", 0, value, "", "")
		return e
	}))
	require.JSONEq(t, `{"min_confidence":80}`, string(read(1, 99).Params), "current tenant tightening survives existing override")
	value.Params = json.RawMessage(`{"min_confidence":75}`)
	_, err = projectRuleCommand(m, db, ctx, 99, 1, value)
	require.ErrorIs(t, err, ErrInvalidArgument)
	value.Params = json.RawMessage(`{"min_confidence":90}`)
	pub.Fail = true
	_, err = projectRuleCommand(m, db, ctx, 99, 1, value)
	require.Error(t, err)
	require.Equal(t, 1, read(1, 99).Version)
	require.EqualValues(t, 2, seedCount(t, db, "event_probe"))
	require.EqualValues(t, 2, seedCount(t, db, "audit_probe"))
	pub.Fail = false
	_, err = projectRuleCommand(m, db, ctx, 99, 1, value)
	require.NoError(t, err)
	value.Params = json.RawMessage(`{"min_confidence":85}`)
	m.reauth = func(_ context.Context, _ *gorm.DB, _ guid.ID, cookie, proof, purpose, target string) error {
		require.Equal(t, "escalation_rule_relax", purpose)
		require.Equal(t, "1:project:99:escalation-rule:LOW_SELF_CONFIDENCE:2", target)
		return errors.New("proof rejected")
	}
	_, err = projectRuleCommand(m, db, ctx, 99, 2, value)
	require.ErrorIs(t, err, ErrForbidden)
	m.reauth = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
	_, err = projectRuleCommand(m, db, ctx, 99, 2, value)
	require.NoError(t, err)
	m.explain = func(_ context.Context, _ *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: req.Permission != "project_read"}, nil
	}
	_, err = projectRuleCommand(m, db, ctx, 99, 3, value)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestProjectRuleHTTPUsesScopedVersionAndCurrentProjectAccess(t *testing.T) {
	m, _, handler, _ := governanceHTTPHarness(t)
	m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
		return DelegationProjectState{Stage: "ACTIVE"}, nil
	}
	value := defaultRule("LOW_SELF_CONFIDENCE").EscalationRuleValue
	value.Params = json.RawMessage(`{"min_confidence":75}`)
	w := governanceRequest(t, handler, http.MethodPut, "/tenants/1/escalation-rules/LOW_SELF_CONFIDENCE?project_id=99", value, `"0"`)
	require.Equal(t, 200, w.Code, w.Body.String())
	w = governanceRequest(t, handler, http.MethodGet, "/tenants/1/escalation-rules?project_id=99", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	w = governanceRequest(t, handler, http.MethodPut, "/tenants/1/escalation-rules/LOW_SELF_CONFIDENCE?project_id=99", value, `"0"`)
	require.Equal(t, 409, w.Code, w.Body.String())
	value.Params = json.RawMessage(`{"min_confidence":50}`)
	w = governanceRequest(t, handler, http.MethodPut, "/tenants/1/escalation-rules/LOW_SELF_CONFIDENCE?project_id=99", value, `"1"`)
	require.Equal(t, 400, w.Code, w.Body.String())
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 2})
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: false}, nil
	}
	_, err := m.getRulesHTTP(ctx, &rulesListInput{TenantID: "1", ProjectID: "99"})
	require.Error(t, err)
}

func TestProjectEscalationRuleStoreFailuresPreserveTenantFloor(t *testing.T) {
	for _, scenario := range []string{"identifier", "zero_identifier", "insert", "update", "read", "tenant_read", "project", "unavailable", "corrupt"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, _ := delegationHarness(t)
			fault := errors.New("project rule source failed")
			want := fault
			value := defaultRule("LOW_SELF_CONFIDENCE").EscalationRuleValue
			value.Params = json.RawMessage(`{"min_confidence":80}`)
			version := 0
			if scenario == "update" || scenario == "corrupt" {
				_, err := projectRuleCommand(m, db, ctx, 99, 0, value)
				require.NoError(t, err)
				version = 1
				value.Params = json.RawMessage(`{"min_confidence":90}`)
			}
			before := seedCount(t, db, m.seed.names.ProjectEscalationRules.String())
			events := seedCount(t, db, "event_probe")
			switch scenario {
			case "identifier":
				m.seed.ids = seedTestIDs{err: fault}
			case "zero_identifier":
				m.seed.ids = seedTestIDs{}
				want = ErrUnavailable
			case "insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:project-rule", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.ProjectEscalationRules.String() {
						tx.AddError(fault)
					}
				}))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:project-rule", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.ProjectEscalationRules.String() {
						tx.AddError(fault)
					}
				}))
			case "read", "tenant_read":
				table := m.seed.names.ProjectEscalationRules.String()
				if scenario == "tenant_read" {
					table = m.seed.names.EscalationRules.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:project-rule", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
			case "project":
				m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
					return DelegationProjectState{}, fault
				}
			case "unavailable":
				m.delegations.projects.InspectOn = nil
				want = ErrUnavailable
			case "corrupt":
				require.NoError(t, db.Table(m.seed.names.ProjectEscalationRules.String()).Where("tenant_id=?", 1).Update("value_json", "[]").Error)
				want = ErrUnavailable
			}
			_, err := projectRuleCommand(m, db, ctx, 99, version, value)
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Query().Remove("test:project-rule"))
			require.NoError(t, db.Callback().Create().Remove("test:project-rule"))
			require.NoError(t, db.Callback().Update().Remove("test:project-rule"))
			require.Equal(t, before, seedCount(t, db, m.seed.names.ProjectEscalationRules.String()))
			require.Equal(t, events, seedCount(t, db, "event_probe"))
			rules, err := m.readRulesOn(ctx, db, 1)
			require.NoError(t, err)
			for _, r := range rules {
				if r.Kind == "LOW_SELF_CONFIDENCE" {
					require.Equal(t, defaultRule(r.Kind).EscalationRuleValue, r.EscalationRuleValue)
				}
			}
		})
	}
}
