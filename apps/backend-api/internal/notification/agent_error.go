package notification

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type agentErrorInput struct {
	AgentID guid.ID `json:"agent_id"`
	EntryID guid.ID `json:"entry_id"`
}

func (m *Module) activityChangedOn(ctx context.Context, tx *gorm.DB, change agents.ActivityTransition) error {
	if !validTransaction(tx) || change.TenantID <= 0 || change.AgentPrincipalID <= 0 {
		return ErrInvalid
	}
	if m.admission == nil || m.queue == nil || m.ids == nil {
		return ErrUnavailable
	}
	if err := m.admission(ctx, tx, change.TenantID); err != nil {
		return err
	}
	q := query.BindAgentErrors(tx, m.names)
	rows := q.WithContext(ctx)
	if change.Activity != "ERROR" {
		_, err := rows.Where(q.TenantID.Eq(change.TenantID), q.AgentID.Eq(change.AgentPrincipalID)).Update(q.Active, false)
		return err
	}
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	if id <= 0 || change.OccurredAt.IsZero() {
		return ErrInvalid
	}
	row := model.AgentError{TenantID: change.TenantID, AgentID: change.AgentPrincipalID, EntryID: guid.ID(id), SessionEpoch: change.SessionEpoch, EnteredAt: change.OccurredAt, Active: true}
	if err := rows.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "agent_id"}}, DoUpdates: clause.AssignmentColumns([]string{"entry_id", "session_epoch", "entered_at", "active", "notified"})}).Create(&row); err != nil {
		return err
	}
	_, err = m.queue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: change.TenantID, Kind: Name + ".agent_error", IdempotencyKey: "agent-error:" + row.EntryID.String(), RunAt: change.OccurredAt.Add(10 * time.Minute), Payload: agentErrorInput{row.AgentID, row.EntryID}})
	return err
}

func (m *Module) agentErrorJob(ctx context.Context, job jobs.Job) error {
	var input agentErrorInput
	if json.Unmarshal(job.Payload, &input) != nil || job.TenantID <= 0 || input.AgentID <= 0 || input.EntryID <= 0 {
		return jobs.Permanent(ErrInvalid)
	}
	if m.admission == nil || m.activity.SnapshotOn == nil {
		return ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	eventCtx, changes := collectInboxChanges(ctx)
	err = db.WithContext(eventCtx).Transaction(func(tx *gorm.DB) error {
		if err := m.admission(eventCtx, tx, job.TenantID); err != nil {
			return err
		}
		q := query.BindAgentErrors(tx, m.names)
		rows := q.WithContext(eventCtx)
		row, err := rows.Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(job.TenantID), q.AgentID.Eq(input.AgentID)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil || !row.Active || row.Notified || row.EntryID != input.EntryID {
			return err
		}
		current, exists, err := m.activity.SnapshotOn(eventCtx, tx, job.TenantID, input.AgentID)
		if err != nil || !exists || current.Activity != "ERROR" || current.SessionEpoch != row.SessionEpoch {
			return err
		}
		due := row.EnteredAt.Add(10 * time.Minute)
		if due.After(m.now()) {
			return jobs.RetryAfter(due.Sub(m.now()), ErrConflict)
		}
		if current.ResponsiblePrincipalID <= 0 {
			return nil
		}
		_, err = m.Notify(eventCtx, tx, Notification{TenantID: job.TenantID, Recipients: []guid.ID{current.ResponsiblePrincipalID}, Type: "fleet.agent_error_persisting", Severity: Action, TemplateKey: "notification.agent_error", Params: map[string]string{"subject_id": input.AgentID.String(), "name": current.Name}, LinkPath: "/admin/" + job.TenantID.String() + "/agents", DedupeKey: "agent-error:" + input.EntryID.String()})
		if err != nil {
			return err
		}
		_, err = rows.Where(q.TenantID.Eq(job.TenantID), q.AgentID.Eq(input.AgentID), q.EntryID.Eq(input.EntryID)).Update(q.Notified, true)
		return err
	})
	if err == nil {
		m.broadcastInboxChanges(context.WithoutCancel(ctx), changes)
	}
	return err
}
