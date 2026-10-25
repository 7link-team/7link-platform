package yunxiao

import (
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDefectFloorSaveRejectsAgentWontFixWithoutWriting(t *testing.T) {
	m, db, ctx := workHarness(t)
	body := defaultWorkflow("BUG").WorkflowInput
	body.States = append(body.States, StateDefinition{Key: "wont_fix", DisplayName: "Will not fix", Group: "CANCELLED", MapsTo: "CANCELLED"}) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	body.Transitions = append(body.Transitions, TransitionDefinition{From: "draft", To: "wont_fix", Enabled: true, ManualAllowed: true, Conditions: []TransitionCondition{{Kind: "ACTOR_SCOPE", ActorKinds: []string{"HUMAN", "AGENT"}}}})
	_, err := m.saveWorkflowHTTP(ctx, &workflowSaveInput{WorkflowPath: WorkflowPath{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, WorkItemType: "BUG"}, WriteHeaders: WriteHeaders{IdempotencyKey: "reject-agent-product-decision"}, Body: body})
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 422, status.GetStatus())
	raw, e := respx.MarshalEnvelope(ctx, err)
	require.NoError(t, e)
	require.Contains(t, string(raw), `"code":126002`)
	require.Contains(t, string(raw), `"reason_key":"defect_floor"`)
	var count int64
	require.NoError(t, db.Table(m.names.WorkflowConfigs.String()).Count(&count).Error)
	require.Zero(t, count)
}

func TestDefectFloorCannotDelegateWontFixOrRemoveEvidence(t *testing.T) {
	from := StateDefinition{Key: "triage", MapsTo: "DRAFT"}
	for _, kind := range []string{"BUG", "INCIDENT"} {
		for _, target := range []string{"wont_fix", "invalid", "closed", "merged", "deployed"} {
			t.Run(kind+"_"+target, func(t *testing.T) {
				to := StateDefinition{Key: target, MapsTo: "VERIFYING"}
				if target == "closed" {
					to.MapsTo = "ACCEPTED"
				}
				cell := TransitionDefinition{From: from.Key, To: target, Enabled: true, ManualAllowed: true, Conditions: []TransitionCondition{{Kind: "ACTOR_SCOPE", ActorKinds: []string{"HUMAN", "AGENT"}}}}
				require.ErrorIs(t, defectFloor(kind, from, to, cell), ErrDefectFloor)
				if target == "merged" {
					cell.Conditions = append(cell.Conditions, TransitionCondition{Kind: "EVIDENCE", Rule: "LINKED_MR_MERGED"})
				} else if target == "deployed" {
					cell.Conditions = append(cell.Conditions, TransitionCondition{Kind: "EVIDENCE", Rule: "DEPLOYMENT_CONTAINS_MERGE"})
				} else {
					cell.Conditions = []TransitionCondition{{Kind: "ACTOR_SCOPE", ActorKinds: []string{"HUMAN"}}}
				}
				require.NoError(t, defectFloor(kind, from, to, cell))
			})
		}
	}
	require.NoError(t, validateWorkflow("BUG", defaultWorkflow("BUG").WorkflowInput))
}
