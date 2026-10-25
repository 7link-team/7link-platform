package delivery

import (
	"context"
	"encoding/json"
	"slices"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

var transitionPreparationKeys = []string{"estimate_minutes", "risk", "autonomy_level", "out_of_scope", "out_of_scope_confirmed", "context_refs", "verification_plan", "budget"}
var transitionCommandKeys = []string{"acknowledge_readiness_issues", "lease_ttl_seconds", "summary", "evidence", "result_id", "reason"}

// Dispatch only to existing domain commands. A selected cell cannot create an
// execution, verification verdict, approval or Human acceptance on its own.
func (m *Module) workflowCommandOn(ctx context.Context, tx *gorm.DB, row *model.WorkItem, to string, in workTransitionBody, preparation *WorkPreparationBody) error {
	actor := authn.PrincipalIDFromContext(ctx)
	readiness := slices.Contains([]string{"DRAFT", "READY", "BLOCKED"}, to)
	allowed := transitionPreparationKeys
	if !readiness {
		if preparation != nil {
			return ErrInvalidArgument
		}
		switch to {
		case "CLAIMED":
			allowed = []string{"acknowledge_readiness_issues", "lease_ttl_seconds"}
		case "NEEDS_HUMAN_REVIEW":
			allowed = []string{"summary", "evidence"}
		case "ACCEPTED":
			allowed = []string{"result_id", "reason"}
		default:
			return ErrWorkPreparationState
		}
	}
	for key := range in.Fields {
		if !slices.Contains(allowed, key) {
			return ErrInvalidArgument
		}
	}
	if readiness {
		_, err := m.prepareWorkActorOn(ctx, tx, row.TenantID, row.ProjectID, row.ID, actor, row.AccountablePrincipalID, row.Version, preparation, true)
		if err != nil {
			return err
		}
	} else {
		encoded, err := json.Marshal(in.Fields)
		if err != nil {
			return err
		}
		switch to {
		case "CLAIMED":
			var input assignmentCreateBody
			if err = json.Unmarshal(encoded, &input); err != nil {
				return ErrInvalidArgument
			}
			input.Role, input.AssigneePrincipalID, input.SeenSeq = "EXECUTOR", actor, in.SeenSeq
			if _, err = m.claimHumanWorkOn(ctx, tx, row.TenantID, row.ProjectID, row.ID, actor, row.AccountablePrincipalID, row.Version, input); err != nil {
				return err
			}
		case "NEEDS_HUMAN_REVIEW":
			var input resultSubmitBody
			if err = json.Unmarshal(encoded, &input); err != nil {
				return ErrInvalidArgument
			}
			input.SeenSeq = in.SeenSeq
			if _, err = m.submitResultOn(ctx, tx, row.TenantID, row.ProjectID, row.ID, actor, row.AccountablePrincipalID, row.Version, input); err != nil {
				return err
			}
		case "ACCEPTED":
			var input struct {
				ResultID guid.ID `json:"result_id"`
				Reason   string  `json:"reason"`
			}
			if err = json.Unmarshal(encoded, &input); err != nil || input.ResultID <= 0 {
				return ErrInvalidArgument
			}
			if _, err = m.acceptResultOn(ctx, tx, row.TenantID, row.ProjectID, row.ID, input.ResultID, actor, row.AccountablePrincipalID, row.Version, resultAcceptBody{Reason: input.Reason, SeenSeq: in.SeenSeq}); err != nil {
				return err
			}
		}
	}
	q := query.BindWorkItems(tx, m.service.names)
	current, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.ID.Eq(row.ID)).First()
	if err != nil {
		return err
	}
	if current.State != to {
		return ErrWorkPreparationState
	}
	*row = *current
	return nil
}
