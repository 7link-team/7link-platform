package notification

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type rotationWarning struct {
	MachineID  guid.ID `json:"machine_id"`
	RotationID guid.ID `json:"rotation_id"`
}

func (m *Module) scheduleRotationWarningOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent, data map[string]json.RawMessage) error {
	var retire time.Time
	if err := json.Unmarshal(data["retire_old_at"], &retire); err != nil {
		return ErrInvalid
	}
	rotation := rawID(data["rotation_id"])
	if rotation <= 0 || e.SubjectID <= 0 || retire.IsZero() {
		return ErrInvalid
	}
	if m.queue == nil || m.admission == nil {
		return ErrUnavailable
	}
	if err := m.admission(ctx, tx, e.TenantID); err != nil {
		return err
	}
	_, err := m.queue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: e.TenantID, Kind: Name + ".rotation_warning", IdempotencyKey: "rotation:" + rotation.String() + ":grace", RunAt: retire.Add(-2 * time.Hour), Payload: rotationWarning{e.SubjectID, rotation}})
	return err
}
func (m *Module) claimRotationAlertOn(ctx context.Context, tx *gorm.DB, tenant, rotation guid.ID, condition string) (bool, error) {
	if tenant <= 0 || rotation <= 0 {
		return false, ErrInvalid
	}
	if m.admission == nil {
		return false, ErrUnavailable
	}
	if err := m.admission(ctx, tx, tenant); err != nil {
		return false, err
	}
	row := model.RotationAlert{TenantID: tenant, RotationID: rotation, Condition: condition, CreatedAt: m.now()}
	result := tx.WithContext(ctx).Table(m.names.RotationAlerts.String()).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	return result.RowsAffected == 1, result.Error
}
func (m *Module) rotationWarningJob(ctx context.Context, j jobs.Job) error {
	var input rotationWarning
	if json.Unmarshal(j.Payload, &input) != nil || j.TenantID <= 0 || input.MachineID <= 0 || input.RotationID <= 0 {
		return jobs.Permanent(ErrInvalid)
	}
	if m.rotationFacts == nil || m.admission == nil {
		return ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	eventCtx, changes := collectInboxChanges(ctx)
	err = db.WithContext(eventCtx).Transaction(func(tx *gorm.DB) error {
		if err := m.admission(eventCtx, tx, j.TenantID); err != nil {
			return err
		}
		fact, exists, err := m.rotationFacts(eventCtx, tx, j.TenantID, input.MachineID, input.RotationID)
		if err != nil {
			return err
		}
		if !exists || fact.State == "ACTIVE" {
			return nil
		}
		if fact.State != "PENDING_DELIVERY" && fact.State != "FAILED" {
			return nil
		}
		due := fact.RetireOldAt.Add(-2 * time.Hour)
		if due.After(m.now()) {
			return jobs.RetryAfter(due.Sub(m.now()), ErrConflict)
		}
		data, _ := json.Marshal(map[string]any{"rotation_id": input.RotationID, "state": fact.State, "reason_code": "ROTATION_GRACE_EXPIRING", "retire_old_at": fact.RetireOldAt, "requested_by": fact.RequestedBy})
		return m.projectEventOn(eventCtx, tx, events.RecordedEvent{TenantID: j.TenantID, SubjectID: input.MachineID, SubjectKind: "machine", Type: "machine.credential_changed", Data: data})
	})
	if err == nil {
		m.broadcastInboxChanges(context.WithoutCancel(ctx), changes)
	}
	return err
}
