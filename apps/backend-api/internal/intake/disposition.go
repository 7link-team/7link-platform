package intake

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
)

type SourceDisposition struct {
	SeenSeq       *int64  `json:"seen_seq,omitempty" minimum:"0" doc:"Consumed original-thread cursor for an Agent client notification."`
	Action        string  `json:"action" enum:"MERGE,DECLINE"`
	TargetID      guid.ID `json:"target_id,omitempty"`
	ReasonCode    string  `json:"reason_code,omitempty" enum:"OUT_OF_SCOPE,INSUFFICIENT_INFO,CAPACITY,SPAM,OTHER"`
	Reason        string  `json:"reason" minLength:"1" maxLength:"2000"`
	ClientMessage string  `json:"client_message,omitempty" maxLength:"2000"`
	NotifyClient  bool    `json:"notify_client"`
}

// The caller holds the authorized project fence. Original content, attachments,
// triage cards and message references remain at their original source.
func (m *Module) disposeSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, expected int, in SourceDisposition) (IntakeLedgerEntry, error) {
	var out IntakeLedgerEntry
	if expected < 1 || !validTriageText(in.Reason, 2000) || len([]rune(in.ClientMessage)) > 2000 || (in.Action != "MERGE" && in.Action != "DECLINE") {
		return out, ErrInvalidArgument
	}
	permission, kind, state := "intake_convert", "intake.merged", "MERGED"
	if in.Action == "DECLINE" {
		permission, kind, state = "intake_decline", "intake.declined", "DECLINED"
		if !slices.Contains([]string{"OUT_OF_SCOPE", "INSUFFICIENT_INFO", "CAPACITY", "SPAM", "OTHER"}, in.ReasonCode) || in.TargetID != 0 {
			return out, ErrInvalidArgument
		}
		if in.ReasonCode == "SPAM" {
			in.NotifyClient = false
		}
	} else if in.TargetID <= 0 || in.TargetID == id {
		return out, ErrInvalidArgument
	}
	if in.NotifyClient && strings.TrimSpace(in.ClientMessage) == "" {
		return out, ErrInvalidArgument
	}
	actorKind, authority, authErr := m.dispositionAuthorityOn(ctx, tx, tenant, project, id, in.Action)
	if authErr != nil {
		return out, authErr
	}
	if m.brief.audit == nil || m.brief.events == nil {
		return out, ErrUnavailable
	}
	q := query.BindRecords(tx, m.names)
	ids := []guid.ID{id}
	if in.Action == "MERGE" {
		ids = append(ids, in.TargetID)
	}
	slices.Sort(ids)
	var sourceVersion int
	for _, rowID := range ids {
		row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(rowID)).First()
		if e != nil {
			return out, e
		}
		if rowID == id {
			// Structured REWORK remains with its owner; an active round closes
			// through its owner in this same transaction.
			if row.Kind == "REWORK" || !slices.Contains([]string{"RECEIVED", "TRIAGED", "CLARIFYING"}, row.Status) || row.Version != expected {
				return out, ErrConflict
			}
			if row.OpenRoundID != nil {
				if m.dispositionRoundCloser == nil {
					return out, ErrUnavailable
				}
				if e := m.dispositionRoundCloser(ctx, tx, tenant, project, id, *row.OpenRoundID, permission, in.Reason); e != nil {
					return out, e
				}
			} else if row.Status == "CLARIFYING" {
				return out, ErrConflict
			}
			sourceVersion = row.Version
			out = intakeLedgerEntry(row, project)
		} else if !slices.Contains([]string{"RECEIVED", "TRIAGED", "CLARIFYING", "CONVERTED"}, row.Status) {
			return out, ErrConflict
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	actor := authn.PrincipalIDFromContext(ctx)
	updates := map[string]any{"open_round_id": nil, "status": state, "version": sourceVersion + 1, "resolved_at": now, "resolved_actor_kind": actorKind, "resolved_actor_id": actor, "resolved_authority_source": authority, "updated_at": now, "updated_by": actor, "client_message": in.ClientMessage}
	updates["client_notification_state"], updates["client_notification_message_id"] = "NOT_REQUESTED", nil
	if in.Action == "MERGE" {
		updates["merged_into_intake_id"] = in.TargetID
	} else {
		updates["decline_reason_code"], updates["decline_reason"] = in.ReasonCode, in.Reason
	}
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.Version.Eq(expected)).Updates(updates)
	if err != nil {
		return out, err
	}
	if changed.RowsAffected != 1 {
		return out, ErrConflict
	}
	notice := DispositionDelivery{State: "NOT_REQUESTED"}
	if in.NotifyClient {
		notice.State = "UNAVAILABLE"
		if m.dispositionNotification != nil {
			notice, err = m.dispositionNotification(ctx, tx, tenant, project, id, in.ClientMessage, in.SeenSeq)
			if err != nil {
				return out, err
			}
			if (notice.State != "POSTED" && notice.State != "UNAVAILABLE") || (notice.State == "POSTED" && (notice.MessageID == nil || *notice.MessageID <= 0)) || (notice.State == "UNAVAILABLE" && notice.MessageID != nil) {
				return out, ErrUnavailable
			}
		}
	}
	if in.NotifyClient {
		receiptWrite, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.Version.Eq(sourceVersion+1)).Updates(map[string]any{"client_notification_state": notice.State, "client_notification_message_id": notice.MessageID})
		if err != nil {
			return out, err
		}
		if receiptWrite.RowsAffected != 1 {
			return out, ErrConflict
		}
	}
	if err = m.sourceEvent(ctx, tx, tenant, project, id, kind, sourceVersion+1, map[string]any{"actor_kind": actorKind, "reason": in.Reason, "reason_code": in.ReasonCode, "merged_into_intake_id": in.TargetID.String(), "client_message": in.ClientMessage, "notify_client": in.NotifyClient, "client_notification": notice.State, "client_notification_message_id": notice.MessageID}, authority); err != nil {
		return out, err
	}
	out.State, out.Version = state, sourceVersion+1
	return out, nil
}
