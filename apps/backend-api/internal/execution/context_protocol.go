package execution

import (
	"context"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// Source ownership stays in delivery: its guard locks and checks the same row
// that its recorder advances. No execution-local copy of the cursor exists.
func (m *Module) checkExecutionContextOn(ctx context.Context, tx *gorm.DB, row *model.Execution, seen *int64, required bool) error {
	if row.WorkItemID == 0 {
		return nil
	}
	if m.commentContext.CheckOn == nil {
		return ErrCapabilityUnavailable
	}
	return m.commentContext.CheckOn(ctx, tx, row.TenantID, row.WorkItemID, seen, required)
}

func (m *Module) recordExecutionActivityOn(ctx context.Context, tx *gorm.DB, row *model.Execution, kind, ref string, id guid.ID, actorKind string) error {
	if row.WorkItemID == 0 {
		return nil
	}
	if m.commentActivities.RecordOn == nil {
		return ErrCapabilityUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 {
		actorKind = "SYSTEM"
	}
	// A machine receipt is a runtime fact, with the pinned executor as its
	// logical actor. Machine principals are not collaboration participants.
	if actorKind == "SYSTEM" {
		actor = 0
	}
	if actorKind == "MACHINE" {
		actor, actorKind = 0, "SYSTEM"
		execution, err := view(row)
		if err != nil {
			return err
		}
		if execution.Plan.ExecutorPrincipalID > 0 {
			actor, actorKind = execution.Plan.ExecutorPrincipalID, "AGENT"
		}
	}
	_, err := m.commentActivities.RecordOn(ctx, tx, row.TenantID, row.WorkItemID, delivery.WorkActivityInput{Kind: kind, ActorKind: actorKind, ActorID: actor, RefKind: ref, RefID: id, Title: "Execution " + kind})
	return err
}
