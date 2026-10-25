package governance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEscalationRulesPreserveNormativeLimits(t *testing.T) {
	require.Len(t, EscalationKinds(), 11)
	for _, kind := range EscalationKinds() {
		t.Run(kind, func(t *testing.T) {
			v := defaultRule(kind)
			require.True(t, validRule(kind, v.EscalationRuleValue))
			require.False(t, ruleRelaxed(kind, v.EscalationRuleValue, v.EscalationRuleValue))
			for _, params := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`{"extra":1}`)} {
				bad := v.EscalationRuleValue
				bad.Params = params
				require.False(t, validRule(kind, bad))
			}
			for _, actions := range [][]string{nil, {"INVALID"}, {"NOTIFY", "NOTIFY"}, {"NOTIFY", "HOLD_DELEGATIONS", "PROJECT_HOLD", "INVALID"}} {
				bad := v.EscalationRuleValue
				bad.ActionsAutonomous = actions
				require.False(t, validRule(kind, bad))
			}
			next := v.EscalationRuleValue
			next.Enabled = false
			require.True(t, ruleRelaxed(kind, v.EscalationRuleValue, next))
			next = v.EscalationRuleValue
			next.ActionsAutonomous = nil
			require.True(t, ruleRelaxed(kind, v.EscalationRuleValue, next))
			if len(v.ActionsHybrid) > 0 {
				next = v.EscalationRuleValue
				next.ActionsHybrid = nil
				require.True(t, ruleRelaxed(kind, v.EscalationRuleValue, next))
			}
		})
	}
	for kind, params := range map[string]string{"REWORK_LIMIT": `{"threshold":4}`, "SPINNING": `{"ratio_pct":9}`, "NO_ELIGIBLE_AUTHORITY": `{"agent_review_timeout_min":31}`, "LOW_SELF_CONFIDENCE": `{"min_confidence":29}`, "EXECUTION_STALLED": `{"stall_minutes":1441}`, "CLIENT_REJECTION": `{"include_partial":1}`} {
		v := defaultRule(kind).EscalationRuleValue
		v.Params = json.RawMessage(params)
		require.False(t, validRule(kind, v), kind)
	}
	for _, params := range []string{`{"pattern_sets":null}`, `{"pattern_sets":[{"key":"x","label":"x","enabled":true,"globs":["x"]}]}`, `{"pattern_sets":[{"key":"AUTH","label":"x","enabled":true,"globs":[]}]}`, `{"pattern_sets":[{"key":"AUTH","label":"x","globs":["x"]}]}`, `{"pattern_sets":[{"key":"AUTH","label":"x","enabled":true,"globs":["../secret"]}]}`, `{"pattern_sets":[{"key":"AUTH","label":"x","enabled":true,"globs":["!secret"]}]}`, `{"pattern_sets":[],"max_paths_checked":99}`, `{"pattern_sets":[],"max_paths_checked":5001}`, `{"pattern_sets":[],"extra":true}`} {
		v := defaultRule("SENSITIVE_PATH").EscalationRuleValue
		v.Params = json.RawMessage(params)
		require.False(t, validRule("SENSITIVE_PATH", v), params)
	}
	for _, pair := range []struct {
		kind, old, next string
		relax           bool
	}{{"LOW_SELF_CONFIDENCE", `{"min_confidence":60}`, `{"min_confidence":50}`, true}, {"LOW_SELF_CONFIDENCE", `{"min_confidence":60}`, `{"min_confidence":70}`, false}, {"EXECUTION_STALLED", `{"stall_minutes":30}`, `{"stall_minutes":60}`, true}, {"CLIENT_REJECTION", `{"include_partial":true}`, `{"include_partial":false}`, true}, {"SENSITIVE_PATH", `{"pattern_sets":[]}`, `{"pattern_sets":[],"max_paths_checked":100}`, true}} {
		old, next := defaultRule(pair.kind).EscalationRuleValue, defaultRule(pair.kind).EscalationRuleValue
		old.Params = json.RawMessage(pair.old)
		next.Params = json.RawMessage(pair.next)
		require.Equal(t, pair.relax, ruleRelaxed(pair.kind, old, next))
	}
	require.False(t, jsonEquivalent(json.RawMessage(`{`), json.RawMessage(`{}`)))
	v := defaultRule("BUDGET").EscalationRuleValue
	require.False(t, validRule("UNKNOWN", v))
	v.Params = json.RawMessage(strings.Repeat(" ", 16385))
	require.False(t, validRule("BUDGET", v))
}

func TestEscalationRuleVersioningStepUpAndAuditRollback(t *testing.T) {
	m, db, ctx, pub := settingsHarness(t)
	rules, err := m.readRulesOn(ctx, db, 1)
	require.NoError(t, err)
	require.Len(t, rules, 11)
	put := func(kind string, version int, value EscalationRuleValue) (EscalationRuleView, error) {
		var out EscalationRuleView
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.putRuleOn(ctx, tx, 1, kind, version, value, "cookie", "proof")
			return e
		})
		return out, err
	}
	value := defaultRule("EXECUTION_STALLED").EscalationRuleValue
	value.Params = json.RawMessage(`{"stall_minutes":15}`)
	created, err := put("EXECUTION_STALLED", 0, value)
	require.NoError(t, err)
	require.Equal(t, 1, created.Version)
	rules, err = m.readRulesOn(ctx, db, 1)
	require.NoError(t, err)
	for _, rule := range rules {
		if rule.Kind == created.Kind {
			require.Equal(t, created, rule)
		}
	}
	_, err = put(created.Kind, 0, value)
	require.ErrorIs(t, err, ErrVersion)
	value.Params = json.RawMessage(`{"stall_minutes":60}`)
	m.reauth = func(_ context.Context, _ *gorm.DB, _ guid.ID, _ string, _ string, purpose, target string) error {
		require.Equal(t, "escalation_rule_relax", purpose)
		require.Equal(t, "1:escalation-rule:EXECUTION_STALLED:1", target)
		return errors.New("expired")
	}
	_, err = put(created.Kind, 1, value)
	require.ErrorIs(t, err, ErrForbidden)
	m.reauth = nil
	_, err = put(created.Kind, 1, value)
	require.ErrorIs(t, err, ErrUnavailable)
	m.reauth = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
	pub.Fail = true
	_, err = put(created.Kind, 1, value)
	require.Error(t, err)
	pub.Fail = false
	saved, err := put(created.Kind, 1, value)
	require.NoError(t, err)
	require.Equal(t, 2, saved.Version)
	// Relaxing a previously unpersisted default also consumes step-up.
	m.reauth = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error {
		return errors.New("no proof")
	}
	value = defaultRule("REVIEW_SLA").EscalationRuleValue
	value.Enabled = false
	_, err = put("REVIEW_SLA", 0, value)
	require.ErrorIs(t, err, ErrForbidden)
	for _, kind := range []string{"BUDGET", "REWORK_LIMIT", "CLIENT_REJECTION", "VERIFICATION_INCONCLUSIVE", "SPAWN_BUDGET"} {
		value = defaultRule(kind).EscalationRuleValue
		value.Enabled = false
		_, err = put(kind, 0, value)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	value = defaultRule("CLIENT_REJECTION").EscalationRuleValue
	value.ActionsAutonomous = []string{"NOTIFY"}
	_, err = put("CLIENT_REJECTION", 0, value)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.readRulesOn(ctx, nil, 1)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.readRulesOn(ctx, db, 0)
	require.ErrorIs(t, err, ErrInvalidArgument)
	q := query.BindEscalationRules(db, m.seed.names)
	_, err = q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(1))).Update(q.ValueJSON, "{}")
	require.NoError(t, err)
	_, err = m.readRulesOn(ctx, db, 1)
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putRuleOn(ctx, tx, 1, created.Kind, 2, defaultRule(created.Kind).EscalationRuleValue, "", "")
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
}

func escalationFixture() EscalationFact {
	return EscalationFact{TenantID: 1, ProjectID: 3, SubjectID: 4, TriggerEventID: 5, Kind: "REWORK_LIMIT", SubjectKind: "WORK_ITEM", Reason: "Third quality return", Facts: map[string]any{"threshold": 3, "quality_returns": 3}, Actor: events.Actor{Kind: "HUMAN", ID: 2, AuthoritySource: "ROLE"}}
}

func TestEscalationIncidentLifecycleDeduplicationAndCurrentVisibility(t *testing.T) {
	m, db, ctx, pub := settingsHarness(t)
	var id guid.ID
	record := func(f EscalationFact) (guid.ID, error) {
		var out guid.ID
		err := db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.recordEscalationOn(ctx, tx, f); return e })
		return out, err
	}
	id, err := record(escalationFixture())
	require.NoError(t, err)
	require.Positive(t, id)
	replayed, err := record(escalationFixture())
	require.NoError(t, err)
	require.Equal(t, id, replayed)
	next := escalationFixture()
	next.TriggerEventID = 6
	replayed, err = record(next)
	require.NoError(t, err)
	require.Equal(t, id, replayed, "one open issue must not fan out repeated Human interruptions")
	decide := func(version int, state, note string) (IncidentView, error) {
		var out IncidentView
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.incidentDecisionOn(ctx, tx, 1, id, version, state, note)
			return e
		})
		return out, err
	}
	view, err := decide(1, "ACKNOWLEDGED", "")
	require.NoError(t, err)
	require.Equal(t, "ACKNOWLEDGED", view.State)
	require.Equal(t, 2, view.Version)
	require.EqualValues(t, 2, *view.HandledBy)
	require.NotNil(t, view.HandledAt)
	replay, err := decide(1, "ACKNOWLEDGED", "")
	require.NoError(t, err)
	require.Equal(t, view, replay)
	_, err = decide(1, "DISMISSED", "A reason with enough context")
	require.ErrorIs(t, err, ErrVersion)
	pub.Fail = true
	_, err = decide(2, "DISMISSED", "A reason with enough context")
	require.Error(t, err)
	pub.Fail = false
	dismissed, err := decide(2, "DISMISSED", "A reason with enough context")
	require.NoError(t, err)
	require.Equal(t, "DISMISSED", dismissed.State)
	require.Equal(t, 3, dismissed.Version)
	_, err = decide(2, "DISMISSED", "Different reason for same event")
	require.ErrorIs(t, err, ErrVersion)
	_, err = decide(3, "ACKNOWLEDGED", "")
	require.ErrorIs(t, err, ErrVersion)
	next.TriggerEventID = 7
	newID, err := record(next)
	require.NoError(t, err)
	require.NotEqual(t, id, newID, "closed issues release the open dedupe key")
	m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.Permission != "project_read"}, nil
	}
	_, err = decide(3, "DISMISSED", "A reason with enough context")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestEscalationFactValidationAndRollback(t *testing.T) {
	m, db, ctx, pub := settingsHarness(t)
	for name, mutate := range map[string]func(*EscalationFact){"tenant": func(f *EscalationFact) { f.TenantID = 0 }, "project": func(f *EscalationFact) { f.ProjectID = 0 }, "subject": func(f *EscalationFact) { f.SubjectID = 0 }, "event": func(f *EscalationFact) { f.TriggerEventID = 0 }, "kind": func(f *EscalationFact) { f.Kind = "INTERNAL_QA_FAILED" }, "subject kind": func(f *EscalationFact) { f.SubjectKind = "FAKE" }, "empty reason": func(f *EscalationFact) { f.Reason = "" }, "large reason": func(f *EscalationFact) { f.Reason = strings.Repeat("x", 2001) }, "actor": func(f *EscalationFact) { f.Actor.Kind = "UNKNOWN" }, "actor id": func(f *EscalationFact) { f.Actor.ID = 0 }, "invalid facts": func(f *EscalationFact) { f.Facts = map[string]any{"invalid": make(chan int)} }, "large facts": func(f *EscalationFact) { f.Facts = map[string]any{"huge": strings.Repeat("x", 32768)} }} {
		t.Run(name, func(t *testing.T) {
			f := escalationFixture()
			mutate(&f)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.recordEscalationOn(ctx, tx, f)
				require.ErrorIs(t, e, ErrInvalidArgument)
				return nil
			}))
		})
	}
	_, err := m.recordEscalationOn(ctx, db, escalationFixture())
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = (*Module)(nil).recordEscalationOn(ctx, nil, escalationFixture())
	require.ErrorIs(t, err, ErrUnavailable)
	pub.Fail = true
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordEscalationOn(ctx, tx, escalationFixture()); return e }))
	pub.Fail = false
	q := query.BindEscalationIncidents(db, m.seed.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	for _, args := range []struct {
		state, note string
		version     int
	}{{"INVALID", "", 1}, {"DISMISSED", "short", 1}, {"ACKNOWLEDGED", "", 0}} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.incidentDecisionOn(ctx, tx, 1, 1, args.version, args.state, args.note)
			require.ErrorIs(t, e, ErrInvalidArgument)
			return nil
		}))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.incidentDecisionOn(ctx, tx, 1, 99, 1, "ACKNOWLEDGED", "")
		require.ErrorIs(t, e, ErrNotFound)
		return nil
	}))
}
