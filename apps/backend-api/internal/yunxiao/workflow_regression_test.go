package yunxiao

import (
	"errors"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkflowConditionValidationRejectsInvalidAuthorityAndEvidence(t *testing.T) {
	for _, c := range []TransitionCondition{
		{Kind: "ACTOR_SCOPE", PrincipalIDs: []guid.ID{0}},
		{Kind: "ACTOR_SCOPE", PrincipalIDs: make([]guid.ID, 51)},
		{Kind: "ACTOR_SCOPE", RoleKeys: []string{""}},
		{Kind: "ACTOR_SCOPE", ActorKinds: []string{"BOT"}},
		{Kind: "ACTOR_SCOPE", WorkItemRoles: []string{"OWNER"}},
		{Kind: "EVIDENCE", Rule: "INTERNAL_QA_PASSED", PipelineIDs: []guid.ID{0}},
		{Kind: "UNKNOWN"},
	} {
		require.False(t, validCondition(c), "%+v", c)
	}
	for _, c := range []TransitionCondition{
		{Kind: "ACTOR_SCOPE", PrincipalIDs: []guid.ID{11}, RoleKeys: []string{"reviewer"}},
		{Kind: "EVIDENCE", Rule: "EXTERNAL_CI_PASSED", PipelineIDs: []guid.ID{12}},
	} {
		require.True(t, validCondition(c))
	}
	view, err := workflowView(&model.WorkflowConfig{})
	require.NoError(t, err)
	require.Empty(t, view.States)
	require.Empty(t, view.Transitions)
	_, err = workflowView(&model.WorkflowConfig{Document: "{"})
	require.Error(t, err)
}

func TestWorkflowActorRolesAndFactFailuresDenyTransitions(t *testing.T) {
	failure := errors.New("fact unavailable")
	for _, tc := range []struct {
		name      string
		condition TransitionCondition
		fact      func(string, string, string) (bool, error)
		wantErr   error
		want      bool
	}{
		{"principal mismatch", TransitionCondition{Kind: "ACTOR_SCOPE", PrincipalIDs: []guid.ID{12}}, nil, nil, false},
		{"role source absent", TransitionCondition{Kind: "ACTOR_SCOPE", WorkItemRoles: []string{"REVIEWER"}}, nil, delivery.ErrUnavailable, false},
		{"role source error", TransitionCondition{Kind: "ACTOR_SCOPE", WorkItemRoles: []string{"REVIEWER"}}, func(string, string, string) (bool, error) { return false, failure }, failure, false},
		{"role missing", TransitionCondition{Kind: "ACTOR_SCOPE", WorkItemRoles: []string{"REVIEWER"}}, func(string, string, string) (bool, error) { return false, nil }, nil, false},
		{"role matches", TransitionCondition{Kind: "ACTOR_SCOPE", WorkItemRoles: []string{"REVIEWER"}}, func(k, r, a string) (bool, error) {
			require.Equal(t, "WORK_ITEM_ROLE", k)
			require.Equal(t, "REVIEWER", r)
			return true, nil
		}, nil, true},
		{"policy roles unavailable", TransitionCondition{Kind: "ACTOR_SCOPE", RoleKeys: []string{"reviewer"}}, nil, delivery.ErrUnavailable, false},
		{"required fields pass", TransitionCondition{Kind: "REQUIRED_FIELDS", Fields: []string{"assignee"}}, func(string, string, string) (bool, error) { return true, nil }, nil, true},
		{"required fact error", TransitionCondition{Kind: "REQUIRED_FIELDS", Fields: []string{"assignee"}}, func(string, string, string) (bool, error) { return false, failure }, failure, false},
		{"evidence source error", TransitionCondition{Kind: "EVIDENCE", Rule: "INTERNAL_QA_PASSED"}, func(string, string, string) (bool, error) { return false, failure }, failure, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := evaluateTransitionCondition(delivery.WorkflowTransition{ActorID: 11, ActorKind: "HUMAN", FactOn: tc.fact}, tc.condition)
			require.Equal(t, tc.want, ok)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
	m, db, ctx := workHarness(t)
	r := delivery.WorkflowTransition{TenantID: 1, ProjectID: 99, Type: "TASK", From: "DRAFT", To: "READY", ActorID: 11, ActorKind: "HUMAN", Trigger: "COMMAND"}
	require.ErrorIs(t, m.checkTransitionOn(ctx, nil, r), delivery.ErrInvalidArgument)
	r.Type = "OTHER"
	require.NoError(t, m.checkTransitionOn(ctx, db, r))
	r.Type = "TASK"
	body := workflowBody()
	body.Transitions[0].Conditions = []TransitionCondition{{Kind: "ACTOR_SCOPE", ActorKinds: []string{"HUMAN"}}, {Kind: "REQUIRED_FIELDS", Fields: []string{"assignee"}}, {Kind: "EVIDENCE", Rule: "INTERNAL_QA_PASSED"}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveWorkflowOn(ctx, tx, 1, 99, "TASK", 0, body, false)
		return err
	}))
	r.FactOn = func(k, rule, a string) (bool, error) { return k != "EVIDENCE", nil }
	require.EqualError(t, m.checkTransitionOn(ctx, db, r), "yunxiao.transition_blocked")
	r.FactOn = func(string, string, string) (bool, error) { return false, failure }
	require.ErrorIs(t, m.checkTransitionOn(ctx, db, r), failure)
	var order []string
	r.FactOn = func(k, rule, a string) (bool, error) { order = append(order, k); return true, nil }
	require.NoError(t, m.checkTransitionOn(ctx, db, r))
	require.Equal(t, []string{"REQUIRED_FIELDS", "EVIDENCE"}, order)
}
