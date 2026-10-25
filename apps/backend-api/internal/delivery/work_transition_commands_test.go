package delivery

import (
	"context"
	"encoding/json"
	"testing"

	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkflowCommandClaimUsesActualAssignmentAndRollsBackDisplayFailure(t *testing.T) {
	m, db, ctx, id := transitionFixture(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	m.workflowSelection = func(context.Context, *gorm.DB, WorkflowTransition) (string, error) { return "CLAIMED", nil }
	in := &workTransitionInput{WorkItemID: id.String(), IdempotencyKey: "claim-display", Body: workTransitionBody{ToStateKey: "assigned"}}
	// This aborts only the final display receipt, after the canonical assignment
	// and its effects have already been executed in the savepoint.
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_display BEFORE INSERT ON d_delivery_work_item_transitions WHEN NEW.reason_code='DISPLAY_TRANSITIONED' BEGIN SELECT RAISE(ABORT,'display failed'); END`).Error)
	before := transitionSnapshot(t, m, db, id)
	_, err := m.applyWorkTransitionHTTP(ctx, in)
	require.Error(t, err)
	require.Equal(t, before, transitionSnapshot(t, m, db, id))
	var count int64
	require.NoError(t, db.Table(m.service.names.Assignments.String()).Where("work_item_id=?", id).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Exec("DROP TRIGGER reject_display").Error)
	first, err := m.applyWorkTransitionHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "CLAIMED", first.Body.Data.State)
	require.Equal(t, "assigned", first.Body.Data.StateKey)
	require.NoError(t, db.Table(m.service.names.Assignments.String()).Where("work_item_id=? AND status='ACTIVE' AND assignee_principal_id=11", id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	again, err := m.applyWorkTransitionHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", again.Replayed)
	view, err := m.readWork(ctx, db, 1, 99, id, 11)
	require.NoError(t, err)
	require.Equal(t, "assigned", view.StateKey)
}

func TestWorkflowCommandResultSubmissionAndAcceptanceRequireCanonicalEvidence(t *testing.T) {
	h := newResultFixture(t)
	h.m.workflowSelection = func(_ context.Context, _ *gorm.DB, r WorkflowTransition) (string, error) {
		switch r.ToKey {
		case "review":
			return "NEEDS_HUMAN_REVIEW", nil
		case "delivered":
			return "ACCEPTED", nil
		default:
			return "VERIFYING", nil
		}
	}
	fields := map[string]json.RawMessage{}
	for key, value := range map[string]any{"summary": h.input.Summary, "evidence": h.input.Evidence} {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		fields[key] = encoded
	}
	in := &workTransitionInput{WorkItemID: h.work.String(), IdempotencyKey: "submit-through-display", Body: workTransitionBody{ToStateKey: "review", Fields: fields}}
	submitted, err := h.m.applyWorkTransitionHTTP(h.ctx, in)
	require.NoError(t, err)
	require.Equal(t, "NEEDS_HUMAN_REVIEW", submitted.Body.Data.State)
	result := *h.stored().CurrentClaimID
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	in = &workTransitionInput{WorkItemID: h.work.String(), IdempotencyKey: "accept-no-check", Body: workTransitionBody{ToStateKey: "delivered", Fields: map[string]json.RawMessage{"result_id": encoded, "reason": json.RawMessage(`"Exact evidence reviewed"`)}}}
	before := transitionSnapshot(t, h.m, h.db, h.work)
	_, err = h.m.applyWorkTransitionHTTP(h.actor(12), in)
	require.Error(t, err, "the selected DONE cell cannot invent an independent passing check")
	require.Equal(t, before, transitionSnapshot(t, h.m, h.db, h.work))
	_, err = h.check(result, 12, "PASSED")
	require.NoError(t, err)
	in.IdempotencyKey = "accept-checked-result"
	accepted, err := h.m.applyWorkTransitionHTTP(h.actor(12), in)
	require.NoError(t, err)
	require.Equal(t, "ACCEPTED", accepted.Body.Data.State)
	require.Equal(t, "delivered", accepted.Body.Data.StateKey)
	require.NotNil(t, h.stored().AcceptedDecisionID)
	// Unsupported system/Runner state cannot be reached by supplying a display
	// key, even when a permissive selection test double returns it.
	in = &workTransitionInput{WorkItemID: h.work.String(), IdempotencyKey: "no-fabricated-verdict", Body: workTransitionBody{ToStateKey: "verified"}}
	before = transitionSnapshot(t, h.m, h.db, h.work)
	_, err = h.m.applyWorkTransitionHTTP(h.actor(12), in)
	require.Error(t, err)
	require.Equal(t, before, transitionSnapshot(t, h.m, h.db, h.work))
}
