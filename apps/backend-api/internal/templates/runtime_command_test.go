package templates

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRuntimeTransformsUseDeclaredFactsAndPersistOutputs(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Workflow.Parameters = map[string]any{"text": "frozen"}
	d.Workflow.Nodes[0] = TemplateNode{ID: "work", Kind: "TRANSFORM", Name: "Map", Condition: json.RawMessage(`{"var":"parameters.text"}`)}
	v := publishRuntime(t, m, db, ctx, "map", d)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.instantiateOn(ctx, tx, v.ID, 99, 11)
		require.Equal(t, "COMPLETED", out.Runs[0].State)
		require.JSONEq(t, `"frozen"`, string(out.Runs[0].Output))
		return e
	}))
}
func TestHumanGateUsesExactDecisionAndRechecksAuthorityOnReplay(t *testing.T) {
	m, db, ctx := templateHarness(t)
	v := publishRuntime(t, m, db, ctx, "gates", testDefinition())
	m.delivery.WorkStatusOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return "ACCEPTED", nil }
	decisions := map[string]governance.WorkflowDecisionFact{}
	allow := true
	m.gates = governance.WorkflowDecisions{
		ReadOn: func(_ context.Context, _ *gorm.DB, ref governance.WorkflowDecisionRef) (governance.WorkflowDecisionFact, bool, error) {
			v, ok := decisions[ref.ExpansionKey]
			return v, ok, nil
		},
		RecordOn: func(_ context.Context, _ *gorm.DB, in governance.WorkflowDecisionInput) (governance.WorkflowDecisionFact, error) {
			if !allow {
				return governance.WorkflowDecisionFact{}, governance.ErrForbidden
			}
			fact := governance.WorkflowDecisionFact{ID: 100, WorkflowDecisionInput: in, ActorID: 11}
			decisions[in.ExpansionKey] = fact
			return fact, nil
		},
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }))
	in := &gateInput{ProjectID: "99", IdempotencyKey: "gate-once"}
	in.Body.Hash = v.Hash
	in.Body.ExpansionKey = runtimeKey("", "team")
	in.Body.Outcome = "APPROVE"
	in.Body.Reason = "Reviewed output"
	first, e := m.gateHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "COMPLETED", first.Body.Data.Runs[1].State)
	require.NotNil(t, first.Body.Data.Runs[1].DecisionID)
	require.Equal(t, "MISSING_GATE_DECISION", first.Body.Data.Runs[2].Reason)
	_, e = m.gateHTTP(ctx, in)
	require.NoError(t, e)
	allow = false
	_, e = m.gateHTTP(ctx, in)
	assertStatus(t, e, 403)
	allow = true
	in.Body.Hash = strings.Repeat("0", 64)
	_, e = m.gateHTTP(ctx, in)
	assertStatus(t, e, 409)
}
func TestSignedFanoutFreezesActualItemIDsAndDescription(t *testing.T) {
	m, db, ctx := templateHarness(t)
	child := testDefinition()
	child.Workflow.WorkflowID = "child"
	publishRuntime(t, m, db, ctx, "child", child)
	d := testDefinition()
	d.Workflow.Nodes[0] = TemplateNode{ID: "work", Kind: "FAN_OUT", Name: "Expand", InputPath: "signed_brief.items", Subflow: &TemplateWorkflowRef{WorkflowID: "child", Version: 1}}
	v := publishRuntime(t, m, db, ctx, "fan", d)
	signed := intake.SignedBriefFact{ID: 8, Hash: strings.Repeat("a", 64), SignedBriefInput: intake.SignedBriefInput{Items: []intake.BriefItem{{ID: "a/b", Text: "Human confirmed item"}, {ID: "b", Text: "Second signed item"}}}}
	present := false
	m.briefs.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.SignedBriefFact, bool, error) {
		return signed, present, nil
	}
	calls := 0
	m.delivery.CreateWorkOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, in delivery.TemplateWorkInput) (guid.ID, error) {
		require.Contains(t, in.Description, "signed_brief_hash")
		calls++
		return guid.ID(100 + calls), nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.instantiateOn(ctx, tx, v.ID, 99, 11)
		require.Equal(t, "MISSING_SIGNED_BRIEF_ITEMS", out.Runs[0].Reason)
		return e
	}))
	require.Zero(t, calls)
	present = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.advanceOn(ctx, tx, 99)
		require.Equal(t, []string{"a/b", "b"}, out.Runs[0].FrozenItems)
		return e
	}))
	require.Equal(t, 2, calls)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }))
	require.Equal(t, 2, calls)
	signed.Hash = strings.Repeat("b", 64)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }), ErrConflict)
}
func TestBoundedLoopEscalatesAfterActualSettledIterations(t *testing.T) {
	m, db, ctx := templateHarness(t)
	child := testDefinition()
	child.Workflow.WorkflowID = "child"
	publishRuntime(t, m, db, ctx, "child", child)
	d := testDefinition()
	d.Workflow.Nodes[0] = TemplateNode{ID: "work", Kind: "LOOP", Name: "Review", MaxIterations: 2, Budget: 10, EscalateTo: "accountable", Condition: json.RawMessage(`false`), Subflow: &TemplateWorkflowRef{WorkflowID: "child", Version: 1}}
	v := publishRuntime(t, m, db, ctx, "loop", d)
	calls := 0
	m.delivery.CreateWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TemplateWorkInput) (guid.ID, error) {
		calls++
		return guid.ID(100 + calls), nil
	}
	m.delivery.WorkStatusOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return "ACCEPTED", nil }
	m.gates.ReadOn = func(_ context.Context, _ *gorm.DB, ref governance.WorkflowDecisionRef) (governance.WorkflowDecisionFact, bool, error) {
		return governance.WorkflowDecisionFact{ID: 77, WorkflowDecisionInput: governance.WorkflowDecisionInput{WorkflowDecisionRef: ref, Outcome: "APPROVE"}}, true, nil
	}
	settled := true
	m.loopCosts.WorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) {
		return 0, settled, nil
	}
	escalations := 0
	m.escalate = func(_ context.Context, _ *gorm.DB, f governance.EscalationFact) (guid.ID, error) {
		require.Equal(t, "REWORK_LIMIT", f.Kind)
		require.Equal(t, "PROJECT", f.SubjectKind)
		escalations++
		return 42, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	require.Equal(t, 1, calls)
	settled = false
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.advanceOn(ctx, tx, 99)
		require.Equal(t, "MISSING_LOOP_BUDGET_FACTS", out.Runs[0].Reason)
		return e
	}))
	require.Equal(t, 1, calls)
	settled = true
	for range 3 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }))
	}
	require.Equal(t, 2, calls)
	require.Equal(t, 1, escalations)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.instanceOn(ctx, tx, 99)
		require.Equal(t, "ESCALATED", out.Runs[0].State)
		require.Equal(t, "LOOP_ITERATIONS_EXHAUSTED", out.Runs[0].Reason)
		return e
	}))
}
func TestSignedBriefCommandPinsBindingAndReauthorizesReplay(t *testing.T) {
	m, db, ctx := templateHarness(t)
	v := publishRuntime(t, m, db, ctx, "sign", testDefinition())
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	in := &signBriefInput{ProjectID: "99", IdempotencyKey: "sign-once"}
	in.Body.Hash = v.Hash
	in.Body.IntakeID = 7
	in.Body.Items = []intake.BriefItem{{ID: "one", Text: "Signed scope"}}
	_, e := m.signBriefHTTP(ctx, in)
	assertStatus(t, e, 503)
	calls := 0
	allow := true
	m.briefs.SignOn = func(_ context.Context, _ *gorm.DB, input intake.SignedBriefInput) (intake.SignedBriefFact, error) {
		calls++
		require.Equal(t, guid.ID(99), input.ProjectID)
		require.Positive(t, input.BindingID)
		require.Equal(t, in.Body.Items, input.Items)
		if !allow {
			return intake.SignedBriefFact{}, intake.ErrForbidden
		}
		return intake.SignedBriefFact{}, nil
	}
	_, e = m.signBriefHTTP(ctx, in)
	require.NoError(t, e)
	_, e = m.signBriefHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, 2, calls)
	allow = false
	_, e = m.signBriefHTTP(ctx, in)
	assertStatus(t, e, 403)
	in.Body.Hash = strings.Repeat("0", 64)
	_, e = m.signBriefHTTP(ctx, in)
	assertStatus(t, e, 409)
	in.ProjectID = "bad"
	_, e = m.signBriefHTTP(ctx, in)
	assertStatus(t, e, 422)
}
func TestGateCommandRejectsUnreadyUnknownAndMissingAuthority(t *testing.T) {
	m, db, ctx := templateHarness(t)
	v := publishRuntime(t, m, db, ctx, "gate-errors", testDefinition())
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	in := &gateInput{ProjectID: "99", IdempotencyKey: "gate-once"}
	in.Body.Hash = v.Hash
	in.Body.ExpansionKey = "unknown"
	_, e := m.gateHTTP(ctx, in)
	assertStatus(t, e, 404)
	in.Body.ExpansionKey = runtimeKey("", "team")
	_, e = m.gateHTTP(ctx, in)
	assertStatus(t, e, 409)
	m.delivery.WorkStatusOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return "ACCEPTED", nil }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }))
	_, e = m.gateHTTP(ctx, in)
	assertStatus(t, e, 503)
	in.ProjectID = "invalid"
	_, e = m.gateHTTP(ctx, in)
	assertStatus(t, e, 422)
}
func TestLoopExactBudgetCompletesOnlyWhenStopConditionSatisfied(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(map[bool]string{false: "exhausted", true: "stopped"}[stop], func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			child := testDefinition()
			child.Workflow.WorkflowID = "child"
			publishRuntime(t, m, db, ctx, "child", child)
			d := testDefinition()
			condition := json.RawMessage(`false`)
			if stop {
				condition = json.RawMessage(`true`)
			}
			d.Workflow.Nodes[0] = TemplateNode{ID: "work", Kind: "LOOP", Name: "Review", MaxIterations: 3, Budget: 10, EscalateTo: "accountable", Condition: condition, Subflow: &TemplateWorkflowRef{WorkflowID: "child", Version: 1}}
			v := publishRuntime(t, m, db, ctx, "loop-budget", d)
			calls := 0
			m.delivery.CreateWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TemplateWorkInput) (guid.ID, error) {
				calls++
				return guid.ID(100 + calls), nil
			}
			m.delivery.WorkStatusOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return "ACCEPTED", nil }
			m.gates.ReadOn = func(_ context.Context, _ *gorm.DB, ref governance.WorkflowDecisionRef) (governance.WorkflowDecisionFact, bool, error) {
				return governance.WorkflowDecisionFact{ID: 77, WorkflowDecisionInput: governance.WorkflowDecisionInput{WorkflowDecisionRef: ref, Outcome: "APPROVE"}}, true, nil
			}
			m.loopCosts.WorkOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, ids []guid.ID) (int64, bool, error) {
				if len(ids) == 0 {
					return 0, true, nil
				}
				return 10, true, nil
			}
			escalations := 0
			m.escalate = func(_ context.Context, _ *gorm.DB, f governance.EscalationFact) (guid.ID, error) {
				require.Equal(t, "BUDGET", f.Kind)
				escalations++
				return 42, nil
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
			for range 3 {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }))
			}
			require.Equal(t, 1, calls)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				out, e := m.instanceOn(ctx, tx, 99)
				if stop {
					require.Equal(t, "COMPLETED", out.Runs[0].State)
					require.Zero(t, escalations)
				} else {
					require.Equal(t, "ESCALATED", out.Runs[0].State)
					require.Equal(t, 1, escalations)
				}
				return e
			}))
		})
	}
}

func TestLoopExceededBudgetEscalatesAndMissingStopFactsWait(t *testing.T) {
	for _, scenario := range []string{"over-budget", "missing-stop", "transform-missing"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			child := testDefinition()
			child.Workflow.WorkflowID = "budget-child"
			child.Workflow.Parameters = map[string]any{"fixed": "value"}
			publishRuntime(t, m, db, ctx, "budget-child", child)
			d := testDefinition()
			condition := json.RawMessage(`false`)
			if scenario == "missing-stop" {
				condition = json.RawMessage(`{"var":"parameters.missing"}`)
			}
			d.Workflow.Nodes[0] = TemplateNode{ID: "work", Kind: "LOOP", Name: "Review", MaxIterations: 3, Budget: 10, EscalateTo: "accountable", Condition: condition, Subflow: &TemplateWorkflowRef{WorkflowID: "budget-child", Version: 1}}
			if scenario == "transform-missing" {
				d.Workflow.Nodes[0] = TemplateNode{ID: "work", Kind: "TRANSFORM", Name: "Map", Condition: json.RawMessage(`{"var":"parameters.absent"}`)}
			}
			v := publishRuntime(t, m, db, ctx, "budget-boundary", d)
			m.delivery.WorkStatusOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error) { return "ACCEPTED", nil }
			m.gates.ReadOn = func(_ context.Context, _ *gorm.DB, ref governance.WorkflowDecisionRef) (governance.WorkflowDecisionFact, bool, error) {
				return governance.WorkflowDecisionFact{ID: 77, WorkflowDecisionInput: governance.WorkflowDecisionInput{WorkflowDecisionRef: ref, Outcome: "APPROVE"}}, true, nil
			}
			cost := int64(0)
			m.loopCosts.WorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) {
				return cost, true, nil
			}
			escalations := 0
			m.escalate = func(_ context.Context, _ *gorm.DB, f governance.EscalationFact) (guid.ID, error) {
				require.Equal(t, "BUDGET", f.Kind)
				escalations++
				return 42, nil
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
			cost = 11
			if scenario == "missing-stop" {
				cost = 0
			}
			for range 3 {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceOn(ctx, tx, 99); return e }))
				if scenario == "missing-stop" {
					cost = 0
				}
			}
			out, e := m.instanceOn(ctx, db, 99)
			require.NoError(t, e)
			switch scenario {
			case "over-budget":
				require.Equal(t, "ESCALATED", out.Runs[0].State)
				require.Equal(t, "LOOP_BUDGET_EXCEEDED", out.Runs[0].Reason)
				require.Equal(t, 1, escalations)
			case "transform-missing":
				require.Equal(t, "MISSING_TRANSFORM_FACT", out.Runs[0].Reason)
				require.Zero(t, escalations)
			case "missing-stop":
				require.Equal(t, "MISSING_LOOP_STOP_FACT", out.Runs[0].Reason)
				require.Zero(t, escalations)
			}
		})
	}
}
