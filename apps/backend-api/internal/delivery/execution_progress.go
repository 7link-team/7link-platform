package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/events"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"time"
)

// The receipt has already acquired current principal/project/assignment guards.
// Persist the hold and escalation in that transaction, never a runtime pause ACK.
func (m *Module) progressStopOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, reason string) error {
	if !slices.Contains([]string{"AUTONOMY_NO_PROGRESS", "AUTONOMY_DEPENDENCY_MISSING", "AUTONOMY_SPINNING"}, reason) {
		return ErrInvalidArgument
	}
	var work *model.WorkItem
	var e error
	if reason == "AUTONOMY_SPINNING" {
		// A Claim is deliberately kept before BR02-27 holds its work. The ordinary
		// finish callback has cleared current_execution_id in this transaction.
		q := query.BindWorkItems(tx, m.service.names)
		work, e = q.WithContext(ctx).Where(q.TenantID.Eq(l.TenantID), q.ProjectID.Eq(l.ProjectID), q.ID.Eq(l.WorkItemID), q.CurrentClaimID.Eq(l.ExecutionID)).Clauses(clause.Locking{Strength: "UPDATE"}).Take()
	} else {
		work, _, e = m.executionStopWorkOn(ctx, tx, l, false)
	}
	if e != nil {
		return e
	}
	if work.PausedAt != nil {
		return nil
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	q := query.BindWorkItems(tx, m.service.names)
	pauseReason := "ESCALATION_RULE"
	if reason == "AUTONOMY_SPINNING" {
		pauseReason = reason
	}
	escalation := "MANDATORY_HUMAN_GATE"
	var blocks []string
	if json.Unmarshal([]byte(work.BlockReasons), &blocks) != nil {
		return ErrUnavailable
	}
	if !slices.Contains(blocks, reason) {
		blocks = append(blocks, reason)
	}
	blockJSON, e := json.Marshal(blocks)
	if e != nil {
		return e
	}
	update, e := q.WithContext(ctx).Where(q.TenantID.Eq(l.TenantID), q.ID.Eq(l.WorkItemID), q.Version.Eq(work.Version)).Updates(map[string]any{"paused_at": now, "pause_reason": pauseReason, "block_reasons": string(blockJSON), "require_human_decision": true, "escalation_reason": escalation})
	if e != nil {
		return e
	}
	if update.RowsAffected != 1 {
		return ErrWorkVersion
	}
	work.PausedAt, work.PauseReason, work.EscalationReason = &now, &pauseReason, &escalation
	work.RequireHumanDecision, work.BlockReasons = true, string(blockJSON)
	if e = m.executionWorkChangedOn(ctx, tx, l, work, work.State, reason, now); e != nil {
		return e
	}
	_, e = m.service.events.Publish(ctx, tx, events.Event{TenantID: l.TenantID, ProjectID: l.ProjectID, Type: "work_item.escalated", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "template:approved_progress"}, Subject: events.Subject{Kind: "work_item", ID: work.ID}, SubjectVersion: int64(work.Version), Data: map[string]any{"work_item_id": work.ID.String(), "execution_id": l.ExecutionID.String(), "trigger": reason}})
	return e
}
