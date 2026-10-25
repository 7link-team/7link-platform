package notification

import (
	"context"
	"encoding/json"
	"time"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

func (m *Module) scheduleDelegationWarningOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if m.delegationFacts == nil || m.queue == nil || m.admission == nil {
		return ErrUnavailable
	}
	fact, exists, err := m.delegationFacts(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || !exists {
		return err
	}
	if fact.ProjectID != e.ProjectID || fact.EndsAt.IsZero() {
		return ErrInvalid
	}
	if fact.State != "ACTIVE" || !fact.EndsAt.After(m.now()) {
		return nil
	}
	if err := m.admission(ctx, tx, e.TenantID); err != nil {
		return err
	}
	_, err = m.queue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: e.TenantID, Kind: Name + ".delegation_warning", IdempotencyKey: "delegation-warning:" + e.SubjectID.String(), RunAt: fact.EndsAt.Add(-72 * time.Hour), Payload: map[string]string{"delegation_id": e.SubjectID.String()}})
	return err
}

func (m *Module) delegationWarningJob(ctx context.Context, job jobs.Job) error {
	var input struct {
		ID guid.ID `json:"delegation_id"`
	}
	if json.Unmarshal(job.Payload, &input) != nil || input.ID <= 0 || job.TenantID <= 0 {
		return jobs.Permanent(ErrInvalid)
	}
	if m.delegationFacts == nil || m.candidates == nil {
		return ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	ctx, changes := collectInboxChanges(ctx)
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fact, exists, err := m.delegationFacts(ctx, tx, job.TenantID, input.ID)
		if err != nil || !exists {
			return err
		}
		if fact.State != "ACTIVE" || !fact.EndsAt.After(m.now()) {
			return nil
		}
		if due := fact.EndsAt.Add(-72 * time.Hour); m.now().Before(due) {
			return jobs.RetryAfter(due.Sub(m.now()), ErrConflict)
		}
		var cursor guid.ID
		for {
			ids, err := m.candidates(ctx, tx, cursor, 100)
			if err != nil || len(ids) == 0 {
				return err
			}
			for _, id := range ids {
				cursor = id
				row := &model.Notification{Type: "governance.delegation_expiring", TenantID: job.TenantID, ProjectID: fact.ProjectID, RecipientPrincipalID: id, Params: `{"subject_id":"` + input.ID.String() + `"}`}
				visible, err := m.visibleOn(ctx, tx, row, id)
				if err != nil {
					return err
				}
				if !visible {
					continue
				}
				_, err = m.Notify(ctx, tx, Notification{TenantID: job.TenantID, ProjectID: fact.ProjectID, Recipients: []guid.ID{id}, Type: row.Type, Severity: Action, TemplateKey: "notification.delegation_expiring", Params: map[string]string{"subject_id": input.ID.String(), "ends_at": fact.EndsAt.UTC().Format(time.RFC3339)}, DedupeKey: "delegation-expiring:" + input.ID.String()})
				if err != nil {
					return err
				}
			}
		}
	})
	if err == nil {
		m.broadcastInboxChanges(context.WithoutCancel(ctx), changes)
	}
	return err
}

func (m *Module) projectDelegationEndOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent, data map[string]json.RawMessage) error {
	var reason string
	if json.Unmarshal(data["end_reason"], &reason) != nil || reason == "" {
		return ErrInvalid
	}
	if reason == "MANUAL" {
		return nil
	}
	if m.delegationFacts == nil {
		return ErrUnavailable
	}
	fact, exists, err := m.delegationFacts(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || !exists {
		return err
	}
	expected := "REVOKED"
	if e.Type == "delegation.expired" {
		expected = "EXPIRED"
	}
	if fact.State != expected || fact.EndReason != reason || fact.ProjectID != e.ProjectID || fact.GrantorID <= 0 {
		return ErrInvalid
	}
	_, err = m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: fact.ProjectID, Recipients: []guid.ID{fact.GrantorID}, Type: "governance.delegation_ended", Severity: Info, TemplateKey: "notification.delegation_ended", Params: map[string]string{"subject_id": e.SubjectID.String()}, DedupeKey: "delegation-ended:" + e.SubjectID.String()})
	return err
}
