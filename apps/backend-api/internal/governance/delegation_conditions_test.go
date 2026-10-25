package governance

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDelegationConditionsFailClosedAtEveryBoundary(t *testing.T) {
	c := DelegationConditions{Actions: []string{"decision_approve"}, WorkItemTypes: []string{"TASK"}, MaxRisk: "MEDIUM", CostCapMicros: 100, MaxRework: 3}
	f := WorkItemFacts{Type: "TASK", Risk: "MEDIUM", CostMicros: 100, QualityReworkCount: 2, RequiredVerificationsPassed: true}
	g := DelegationGuards{ProjectAllowed: true, Autonomous: true, ClaimEvaluated: true}
	require.Empty(t, delegationConditionReason(c, "decision_approve", &f, g))
	for _, tc := range []struct {
		name, reason string
		change       func(*WorkItemFacts, *DelegationGuards)
	}{
		{"type", "DELEGATION_TYPE", func(f *WorkItemFacts, _ *DelegationGuards) { f.Type = "BUG" }},
		{"high", "DELEGATION_RISK", func(f *WorkItemFacts, _ *DelegationGuards) { f.Risk = "HIGH" }},
		{"unknown risk", "DELEGATION_RISK", func(f *WorkItemFacts, _ *DelegationGuards) { f.Risk = "" }},
		{"cost", "DELEGATION_COST", func(f *WorkItemFacts, _ *DelegationGuards) { f.CostMicros++ }},
		{"negative cost", "DELEGATION_COST", func(f *WorkItemFacts, _ *DelegationGuards) { f.CostMicros = -1 }},
		{"verification", "DELEGATION_VERIFICATION", func(f *WorkItemFacts, _ *DelegationGuards) { f.RequiredVerificationsPassed = false }},
		{"rework", "DELEGATION_REWORK", func(f *WorkItemFacts, _ *DelegationGuards) { f.QualityReworkCount = 3 }},
		{"negative rework", "DELEGATION_REWORK", func(f *WorkItemFacts, _ *DelegationGuards) { f.QualityReworkCount = -1 }},
		{"human", "DELEGATION_HUMAN_REQUIRED", func(f *WorkItemFacts, _ *DelegationGuards) { f.RequireHumanDecision = true }},
		{"hold", "ESCALATION_HOLD", func(_ *WorkItemFacts, g *DelegationGuards) { g.Hold = true }},
		{"gate", "PROJECT_GATE", func(_ *WorkItemFacts, g *DelegationGuards) { g.ProjectAllowed = false }},
		{"receipt", "ESCALATION_PENDING", func(_ *WorkItemFacts, g *DelegationGuards) { g.ClaimEvaluated = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, g := f, g
			tc.change(&f, &g)
			require.Equal(t, tc.reason, delegationConditionReason(c, "decision_approve", &f, g))
		})
	}
	require.Equal(t, "NO_DELEGATION", delegationConditionReason(c, "scope_change", &f, g))
	require.Equal(t, "NO_DELEGATION", delegationConditionReason(c, "decision_approve", nil, g))
	c.WorkItemTypes = nil
	f.Type = "BUG"
	require.Empty(t, delegationConditionReason(c, "decision_approve", &f, g))
}

func TestDelegationIntakeHasItsOwnGuards(t *testing.T) {
	c := DelegationConditions{Actions: []string{"intake_convert"}, MaxRisk: "LOW", CostCapMicros: 1, MaxRework: 1}
	f := WorkItemFacts{IntakeConvertMode: "NEW_REQUIREMENT"}
	g := DelegationGuards{ProjectAllowed: true, Autonomous: true}
	require.Empty(t, delegationConditionReason(c, "intake_convert", &f, g), "intake has no work-item verification, cost, risk or claim receipt")
	f.IntakeConvertMode = "MERGE"
	require.Empty(t, delegationConditionReason(c, "intake_convert", &f, g), "an explicitly delegated merge retains the same autonomous and escalation gates")
	g.Autonomous = false
	require.Equal(t, "DELEGATION_MODE", delegationConditionReason(c, "intake_convert", &f, g))
	g.Autonomous = true
	f.IntakeConvertMode = "APPEND"
	require.Equal(t, "DELEGATION_MODE", delegationConditionReason(c, "intake_convert", &f, g))
	g.Hold = true
	require.Equal(t, "ESCALATION_HOLD", delegationConditionReason(c, "intake_convert", &f, g))
}

func TestDelegationConditionSchemaRejectsInvalidGrants(t *testing.T) {
	valid := DelegationConditions{Actions: []string{"code_merge"}, MaxRisk: "LOW", CostCapMicros: 1, MaxRework: 1}
	require.True(t, validDelegationConditions(valid))
	for _, mutate := range []func(*DelegationConditions){
		func(c *DelegationConditions) { c.Actions = nil },
		func(c *DelegationConditions) { c.Actions = []string{"policy_manage"} },
		func(c *DelegationConditions) { c.Actions = []string{"code_merge", "code_merge"} },
		func(c *DelegationConditions) { c.MaxRisk = "MEDIUM" },
		func(c *DelegationConditions) { c.MaxRisk = "HIGH" },
		func(c *DelegationConditions) { c.CostCapMicros = 0 },
		func(c *DelegationConditions) { c.MaxRework = 0 },
		func(c *DelegationConditions) { c.MaxRework = 4 },
		func(c *DelegationConditions) { c.WorkItemTypes = []string{"UNKNOWN"} },
		func(c *DelegationConditions) { c.WorkItemTypes = []string{"TASK", "TASK"} },
	} {
		c := valid
		mutate(&c)
		require.False(t, validDelegationConditions(c))
	}
	valid.Actions = []string{"decision_approve", "decision_reject", "decision_request_changes", "code_merge", "intake_convert"}
	require.True(t, validDelegationConditions(valid))
}
