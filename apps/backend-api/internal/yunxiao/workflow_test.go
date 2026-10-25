package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestWorkflowSelectionUsesExactDisplayCellAndNeverGrantsAgentAcceptance(t *testing.T) {
	m, db, ctx := workHarness(t)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	body := WorkflowInput{States: []StateDefinition{
		{Key: "draft", DisplayName: "Draft", Group: "TODO", MapsTo: "DRAFT"},
		{Key: "ready", DisplayName: "Ready", Group: "TODO", MapsTo: "READY"},
		{Key: "scheduled", DisplayName: "Scheduled", Group: "TODO", MapsTo: "READY"},
	}, Transitions: []TransitionDefinition{
		{From: "draft", To: "ready", Enabled: false, ManualAllowed: true},
		{From: "draft", To: "scheduled", Enabled: true, ManualAllowed: true, Conditions: []TransitionCondition{{Kind: "ACTOR_SCOPE", ActorKinds: []string{"AGENT"}}, {Kind: "REQUIRED_FIELDS", Fields: []string{"estimate_minutes"}}}},
	}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveWorkflowOn(ctx, tx, 1, 99, "TASK", 0, body, false)
		return err
	}))
	r := delivery.WorkflowTransition{TenantID: 1, ProjectID: 99, WorkItemID: 123, Type: "TASK", From: "DRAFT", FromKey: "draft", ToKey: "scheduled", ActorKind: "AGENT", ActorID: 11, Trigger: "COMMAND"}
	r.FactOn = func(kind, rule, argument string) (bool, error) {
		return kind == "REQUIRED_FIELDS" && rule == "estimate_minutes", nil
	}
	resolve := func() (string, error) {
		var out string
		err := db.Transaction(func(tx *gorm.DB) error { var err error; out, err = m.selectWorkflowStateOn(ctx, tx, r); return err })
		return out, err
	}
	to, err := resolve()
	require.NoError(t, err, "disabled sibling alias must not block the selected cell")
	require.Equal(t, "READY", to)
	r.ToKey = "ready"
	_, err = resolve()
	require.ErrorIs(t, err, delivery.ErrWorkPreparationState)
	r.ToKey = "scheduled"
	r.ActorKind = "HUMAN"
	_, err = resolve()
	require.Error(t, err, "selected ACTOR_SCOPE must remain effective")
	r.ActorKind = "AGENT"
	r.From = "READY"
	_, err = resolve()
	require.ErrorIs(t, err, delivery.ErrWorkPreparationState, "stale display provenance cannot select a different source business state")
	r.From = "NEEDS_HUMAN_REVIEW"
	r.FromKey = "needs_human_review"
	r.ToKey = "accepted"
	_, err = resolve()
	require.Error(t, err, "Agent acceptance remains forbidden")
	_, err = m.selectWorkflowStateOn(ctx, nil, r)
	require.ErrorIs(t, err, delivery.ErrInvalidArgument)
}

func TestWorkflowPreviewMatchesEffectiveSaveAndResetWithoutWrites(t *testing.T) {
	m, db, ctx := workHarness(t)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	body := workflowBody()
	body.Transitions[0].Enabled = false
	preview := func(project guid.ID, version int, in WorkflowInput, remove bool) WorkflowPreview {
		t.Helper()
		var out WorkflowPreview
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.previewWorkflowOn(ctx, tx, 1, project, "TASK", version, in, remove)
			return err
		}))
		return out
	}
	beforeIDs := m.ids.(*testIDs).n
	p := preview(0, 0, body, false)
	require.Equal(t, "NOT_EVALUATED", p.RuntimeImpact)
	require.Contains(t, p.ChangedTransitions, "draft:ready")
	require.Equal(t, "TEAM", p.After.Source)
	require.Equal(t, beforeIDs, m.ids.(*testIDs).n)
	var effects int64
	require.NoError(t, db.Table("test_effects").Count(&effects).Error)
	require.Zero(t, effects)
	count, err := query.BindWorkflowConfigs(db, m.names).WithContext(ctx).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.saveWorkflowOn(ctx, tx, 1, 0, "TASK", 0, body, false); return err }))
	var actual WorkflowView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		actual, err = m.effectiveWorkflowOn(ctx, tx, 1, 0, "TASK")
		return err
	}))
	p.After.ID = actual.ID
	require.Equal(t, actual, p.After)
	body.Transitions[0].Enabled = true
	p = preview(99, 0, body, false)
	require.Equal(t, 1, p.After.TeamVersion)
	require.Contains(t, p.ChangedTransitions, "draft:ready")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveWorkflowOn(ctx, tx, 1, 99, "TASK", 0, body, false)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		actual, err = m.effectiveWorkflowOn(ctx, tx, 1, 99, "TASK")
		return err
	}))
	p.After.ID = actual.ID
	require.Equal(t, actual, p.After)
	p = preview(99, 1, WorkflowInput{}, true)
	require.Equal(t, "TEAM", p.After.Source)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveWorkflowOn(ctx, tx, 1, 99, "TASK", 1, WorkflowInput{}, true)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		actual, err = m.effectiveWorkflowOn(ctx, tx, 1, 99, "TASK")
		return err
	}))
	require.Equal(t, actual, p.After)
	for _, scenario := range []struct {
		version int
		body    WorkflowInput
		remove  bool
	}{{1, body, false}, {2, WorkflowInput{}, false}, {2, WorkflowInput{}, true}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.previewWorkflowOn(ctx, tx, 1, 99, "TASK", scenario.version, scenario.body, scenario.remove)
			return err
		}))
	}
	m.projects = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.previewWorkflowOn(ctx, tx, 1, 99, "TASK", 2, body, false)
		return err
	}), ErrForbidden)
}

func TestWorkflowEffectiveInheritanceAndTransactionalEnforcement(t *testing.T) {
	m, db, ctx := workHarness(t)
	m.policy = func(_ context.Context, _ *gorm.DB, tenant guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.EqualValues(t, 1, tenant)
		require.Equal(t, "workflow_manage", permission)
		require.EqualValues(t, 11, actor)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	check := func(project guid.ID, from, to, kind, trigger string) error {
		return db.Transaction(func(tx *gorm.DB) error {
			return m.checkTransitionOn(ctx, tx, delivery.WorkflowTransition{TenantID: 1, ProjectID: project, Type: "TASK", From: from, To: to, ActorKind: kind, Trigger: trigger, ActorID: 11, FactOn: func(k, r, a string) (bool, error) { return false, nil }})
		})
	}
	get := func(tenant, project guid.ID) WorkflowView {
		var v WorkflowView
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			v, e = m.effectiveWorkflowOn(ctx, tx, tenant, project, "TASK")
			return e
		}))
		return v
	}
	require.Equal(t, "SYSTEM", get(1, 99).Source)
	require.NoError(t, check(99, "READY", "CLAIMED", "HUMAN", "COMMAND"))
	body := workflowBody()
	body.Transitions[0].Enabled = false
	save := func(project guid.ID, version int, body WorkflowInput, remove bool) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.saveWorkflowOn(ctx, tx, 1, project, "TASK", version, body, remove)
			return e
		})
	}
	require.NoError(t, save(0, 0, body, false))
	require.Equal(t, "TEAM", get(1, 99).Source)
	require.ErrorIs(t, check(99, "DRAFT", "READY", "HUMAN", "COMMAND"), delivery.ErrWorkPreparationState)
	require.ErrorIs(t, check(98, "DRAFT", "READY", "HUMAN", "COMMAND"), delivery.ErrWorkPreparationState)
	require.Equal(t, "SYSTEM", get(2, 99).Source, "tenant configuration cannot bleed")
	body.Transitions[0].Enabled = true
	body.Transitions[0].Conditions = []TransitionCondition{}
	require.NoError(t, save(99, 0, body, false))
	require.NoError(t, check(99, "DRAFT", "READY", "HUMAN", "COMMAND"))
	require.ErrorIs(t, check(98, "DRAFT", "READY", "HUMAN", "COMMAND"), delivery.ErrWorkPreparationState)
	body.Transitions[0].ManualAllowed = false
	require.NoError(t, save(99, 1, body, false))
	require.ErrorIs(t, check(99, "DRAFT", "READY", "AGENT", "DECISION"), delivery.ErrWorkPreparationState)
	require.NoError(t, check(99, "DRAFT", "READY", "SYSTEM", "SYSTEM"))
	require.NoError(t, save(99, 2, WorkflowInput{}, true))
	require.Equal(t, "TEAM", get(1, 99).Source)
	require.Equal(t, 3, get(1, 99).Version)
	require.ErrorIs(t, check(99, "DRAFT", "READY", "HUMAN", "COMMAND"), delivery.ErrWorkPreparationState)
	require.NoError(t, save(0, 1, body, false))
	require.NoError(t, check(99, "DRAFT", "READY", "SYSTEM", "SYSTEM"), "team changes immediately propagate after reset")
	require.ErrorIs(t, check(99, "ACCEPTED", "DRAFT", "SYSTEM", "SYSTEM"), delivery.ErrWorkPreparationState)
}

func TestWorkflowConditionOrderAndMissingFactsNeverPass(t *testing.T) {
	r := delivery.WorkflowTransition{ActorID: 11, ActorKind: "HUMAN"}
	ok, e := evaluateTransitionCondition(r, TransitionCondition{Kind: "ACTOR_SCOPE", ActorKinds: []string{"AGENT"}})
	require.NoError(t, e)
	require.False(t, ok)
	ok, e = evaluateTransitionCondition(r, TransitionCondition{Kind: "EVIDENCE", Rule: "INTERNAL_QA_PASSED"})
	require.ErrorIs(t, e, delivery.ErrUnavailable)
	require.False(t, ok)
	var fields []string
	r.FactOn = func(k, rule, a string) (bool, error) { fields = append(fields, rule); return rule != "assignee", nil }
	ok, e = evaluateTransitionCondition(r, TransitionCondition{Kind: "REQUIRED_FIELDS", Fields: []string{"acceptance_criteria", "assignee", "estimate_minutes"}})
	require.NoError(t, e)
	require.False(t, ok)
	require.Equal(t, []string{"acceptance_criteria", "assignee"}, fields)
}

func TestWorkflowSystemBypassesActorScopeButNeverEvidence(t *testing.T) {
	m, db, ctx := workHarness(t)
	body := workflowBody()
	body.Transitions[0].ManualAllowed = false
	body.Transitions[0].Conditions = []TransitionCondition{
		{Kind: "ACTOR_SCOPE", ActorKinds: []string{"HUMAN"}, PrincipalIDs: []guid.ID{12}},
		{Kind: "EVIDENCE", Rule: "INTERNAL_QA_PASSED"},
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveWorkflowOn(ctx, tx, 1, 99, "TASK", 0, body, false)
		return err
	}))
	for _, tc := range []struct {
		name, actor, trigger string
		evidence, allowed    bool
	}{
		{"system passing evidence", "SYSTEM", "SYSTEM", true, true},
		{"system failing evidence", "SYSTEM", "SYSTEM", false, false},
		{"human command", "HUMAN", "COMMAND", true, false},
		{"agent cannot claim system bypass", "AGENT", "SYSTEM", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := db.Transaction(func(tx *gorm.DB) error {
				return m.checkTransitionOn(ctx, tx, delivery.WorkflowTransition{TenantID: 1, ProjectID: 99, Type: "TASK", From: "DRAFT", To: "READY", ActorID: 11, ActorKind: tc.actor, Trigger: tc.trigger, FactOn: func(kind, rule, argument string) (bool, error) {
					require.Equal(t, "EVIDENCE", kind)
					require.Equal(t, "INTERNAL_QA_PASSED", rule)
					return tc.evidence, nil
				}})
			})
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func workflowBody() WorkflowInput {
	return WorkflowInput{States: []StateDefinition{{Key: "draft", DisplayName: "Draft", Group: "TODO", MapsTo: "DRAFT"}, {Key: "ready", DisplayName: "Ready", Group: "TODO", MapsTo: "READY"}}, Transitions: []TransitionDefinition{{From: "draft", To: "ready", Enabled: true, ManualAllowed: true, Conditions: []TransitionCondition{{Kind: "REQUIRED_FIELDS", Fields: []string{"assignee"}}}}}}
}
func TestWorkflowCRUDVersioningAndGroupCompatibility(t *testing.T) {
	m, db, ctx := workHarness(t)
	body := workflowBody()
	var v WorkflowView
	save := func(version int, remove bool) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var e error
			v, e = m.saveWorkflowOn(ctx, tx, 1, 99, "TASK", version, body, remove)
			return e
		})
	}
	require.NoError(t, save(0, false))
	require.Equal(t, 1, v.Version)
	require.Equal(t, body, v.WorkflowInput)
	require.ErrorIs(t, save(0, false), ErrConflict)
	require.NoError(t, save(1, false))
	require.Equal(t, 2, v.Version)
	body.States[0].Group = "DONE"
	require.ErrorIs(t, save(2, false), ErrInvalid)
	body = workflowBody()
	require.NoError(t, save(2, true))
	require.Equal(t, 3, v.Version)
	require.ErrorIs(t, save(3, true), ErrConflict)
	require.ErrorIs(t, save(0, false), ErrConflict)
	require.NoError(t, save(3, false))
	require.Equal(t, 4, v.Version)
	require.ErrorIs(t, save(1, false), ErrConflict)
}
func TestWorkflowRejectsInvalidMatricesAndConditions(t *testing.T) {
	for _, mutate := range []func(*WorkflowInput){
		func(b *WorkflowInput) { b.States = append(b.States, b.States[0]) },
		func(b *WorkflowInput) { b.Transitions[0].To = "absent" },
		func(b *WorkflowInput) {
			b.Transitions[0].Conditions = []TransitionCondition{{Kind: "EVIDENCE", Rule: "FAKE_PASS"}}
		},
		func(b *WorkflowInput) { b.States[0].Key = "Bad Key" },
		func(b *WorkflowInput) { b.Transitions[0].Conditions = []TransitionCondition{{Kind: "REQUIRED_FIELDS"}} },
	} {
		b := workflowBody()
		mutate(&b)
		require.ErrorIs(t, validateWorkflow("TASK", b), ErrInvalid)
	}
	require.True(t, validCondition(TransitionCondition{Kind: "ACTOR_SCOPE", ActorKinds: []string{"HUMAN"}}))
	require.True(t, validCondition(TransitionCondition{Kind: "EVIDENCE", Rule: "INTERNAL_QA_PASSED"}))
	require.True(t, validCondition(TransitionCondition{Kind: "RELATED_OBJECTS", Rule: "HAS_PARENT"}))
}

func TestWorkflowStorageFailuresPreserveConfigurationAndPolicyInheritance(t *testing.T) {
	for _, mode := range []string{"query", "id", "insert", "update", "outbox"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			body := workflowBody()
			version := 0
			if mode == "update" || mode == "outbox" {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveWorkflowOn(ctx, tx, 1, 99, "TASK", 0, body, false); return e }))
				version = 1
			}
			switch mode {
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("workflow_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.WorkflowConfigs.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("workflow_storage")
			case "id":
				m.ids = &testIDs{err: ErrUnavailable}
			case "insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("workflow_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.WorkflowConfigs.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Create().Remove("workflow_storage")
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("workflow_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.WorkflowConfigs.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Update().Remove("workflow_storage")
			case "outbox":
				m.events = testPublisher{err: ErrUnavailable}
			}
			body.CarryOverMode = "BACKLOG"
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.saveWorkflowOn(ctx, tx, 1, 99, "TASK", version, body, false)
				return e
			}))
			if mode == "query" {
				require.NoError(t, db.Callback().Query().Remove("workflow_storage"))
			}
			effective, e := m.effectiveWorkflowOn(ctx, db, 1, 99, "TASK")
			require.NoError(t, e)
			require.NotEqual(t, "BACKLOG", effective.CarryOverMode)
			require.Equal(t, version, effective.Version)
		})
	}
}
