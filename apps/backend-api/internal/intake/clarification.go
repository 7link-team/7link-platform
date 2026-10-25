package intake

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// ClarificationAction distinguishes opening a round from client activity in an
// existing round. A triage revision may advance the source while it is open.
type ClarificationAction string

const (
	ClarificationLateProgress ClarificationAction = "LATE_PROGRESS"
	ClarificationRequest      ClarificationAction = "REQUEST"
	ClarificationProgress     ClarificationAction = "PROGRESS"
	ClarificationComplete     ClarificationAction = "COMPLETE"
	ClarificationCancel       ClarificationAction = "CANCEL"
)

// clarificationStateOn is the narrow state hook for clientwork's authorized
// round command. The caller fences the current Human/client and project before
// invoking it, and commits the round, audit and outbox in this same transaction.
func (m *Module) clarificationStateOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, expected int, round guid.ID, action ClarificationAction) (int, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || id <= 0 || expected < 1 || round <= 0 {
		return 0, ErrInvalidArgument
	}
	if action != ClarificationRequest && action != ClarificationProgress && action != ClarificationComplete && action != ClarificationCancel && action != ClarificationLateProgress {
		return 0, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	if authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return 0, ErrForbidden
	}
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if err != nil {
		return 0, err
	}
	if action == ClarificationRequest && row.Version != expected {
		return 0, ErrConflict
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	version := row.Version
	changes := map[string]any{"version": version + 1, "updated_at": at, "updated_by": authn.PrincipalIDFromContext(ctx)}
	if action == ClarificationLateProgress {
		changes["client_unresponsive"], changes["last_client_activity_at"] = false, at
	} else if action != ClarificationRequest {
		if row.Status != "CLARIFYING" || row.OpenRoundID == nil || *row.OpenRoundID != round {
			return 0, ErrConflict
		}
		if action == ClarificationCancel {
			if err := m.changeSourceAuthority(ctx, tx, tenant, project, "scope_change"); err != nil {
				return 0, err
			}
			changes["last_team_activity_at"] = at
		} else {
			changes["client_unresponsive"], changes["last_client_activity_at"] = false, at
		}
		if action == ClarificationComplete || action == ClarificationCancel {
			changes["status"], changes["open_round_id"] = "TRIAGED", nil
		}
	} else {
		if err := m.changeSourceAuthority(ctx, tx, tenant, project, "scope_change"); err != nil {
			return 0, err
		}
		if (row.Status != "RECEIVED" && row.Status != "TRIAGED") || row.OpenRoundID != nil {
			return 0, ErrConflict
		}
		changes["status"], changes["open_round_id"], changes["last_team_activity_at"] = "CLARIFYING", round, at
	}
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Updates(changes)
	if err != nil {
		return 0, err
	}
	if result.RowsAffected != 1 {
		return 0, ErrConflict
	}
	if m.brief.audit == nil || m.brief.events == nil {
		return 0, ErrUnavailable
	}
	kind, permission := "intake.clarification_answered", "IAM:intake_submit"
	if action == ClarificationRequest {
		kind, permission = "intake.clarification_requested", "IAM:scope_change"
	}
	if action == ClarificationCancel {
		kind, permission = "intake.clarification_cancelled", "IAM:scope_change" //nolint:misspell // Preserve the published domain or serialized contract spelling.
	}
	if err := m.sourceEvent(ctx, tx, tenant, project, id, kind, version+1, map[string]any{}, permission); err != nil {
		return 0, err
	}
	return version + 1, nil
}
