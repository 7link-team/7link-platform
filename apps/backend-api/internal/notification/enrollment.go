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

type enrollmentWarning struct {
	ID guid.ID `json:"id"`
}

func (m *Module) enrollmentAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if m.enrollmentFacts == nil {
		return false, ErrUnavailable
	}
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(params["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	fact, found, err := m.enrollmentFacts(ctx, tx, row.TenantID, id)
	if err != nil || !found {
		return false, err
	}
	if row.Type == "machine.enrolled" {
		if fact.ApproverID == actor {
			return false, nil
		}
		c, err := m.tenantPermission(ctx, tx, row.TenantID, "machine_manage", actor)
		return c.AllowAll, err
	}
	if fact.EverOnline || m.now().Before(fact.ApprovedAt.Add(10*time.Minute)) {
		return false, nil
	}
	if fact.ApproverID == actor {
		return true, nil
	}
	c, err := m.tenantPermission(ctx, tx, row.TenantID, "machine_manage", actor)
	return c.AllowAll, err
}

func (m *Module) projectEnrollmentOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	var data struct {
		State string `json:"state"`
	}
	if json.Unmarshal(e.Data, &data) != nil {
		return ErrInvalid
	}
	if data.State != "APPROVED" {
		return nil
	}
	if m.enrollmentFacts == nil || m.queue == nil || m.admission == nil {
		return ErrUnavailable
	}
	fact, found, err := m.enrollmentFacts(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || !found {
		return err
	}
	if err := m.admission(ctx, tx, e.TenantID); err != nil {
		return err
	}
	if _, err := m.queue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: e.TenantID, Kind: "notification.enrollment_warning", IdempotencyKey: "enrollment-warning:" + e.SubjectID.String(), RunAt: fact.ApprovedAt.Add(10 * time.Minute), Payload: enrollmentWarning{ID: e.SubjectID}}); err != nil {
		return err
	}
	return m.notifyEnrollmentOn(ctx, tx, e.TenantID, e.SubjectID, "machine.enrolled")
}

func (m *Module) enrollmentWarningJob(ctx context.Context, job jobs.Job) error {
	var in enrollmentWarning
	if json.Unmarshal(job.Payload, &in) != nil || in.ID <= 0 || job.TenantID <= 0 {
		return jobs.Permanent(ErrInvalid)
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return m.notifyEnrollmentOn(ctx, tx, job.TenantID, in.ID, "machine.never_online")
	})
}

func (m *Module) notifyEnrollmentOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, typ string) error {
	if m.enrollmentFacts == nil || m.candidates == nil {
		return ErrUnavailable
	}
	fact, found, err := m.enrollmentFacts(ctx, tx, tenant, id)
	if err != nil || !found {
		return err
	}
	if typ == "machine.never_online" && (fact.EverOnline || m.now().Before(fact.ApprovedAt.Add(10*time.Minute))) {
		return nil
	}
	params := map[string]string{"subject_id": id.String(), "machine_id": fact.MachineID.String(), "approver_id": fact.ApproverID.String()}
	raw, _ := json.Marshal(params)
	var after guid.ID
	for {
		ids, err := m.candidates(ctx, tx, after, 100)
		if err != nil {
			return err
		}
		for _, actor := range ids {
			if actor <= after {
				return ErrInvalid
			}
			after = actor
			row := &model.Notification{TenantID: tenant, RecipientPrincipalID: actor, Type: typ, Permission: "machine_read", Params: string(raw)}
			allowed, err := m.visibleOn(ctx, tx, row, actor)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			n := Notification{TenantID: tenant, Recipients: []guid.ID{actor}, Type: typ, Severity: Info, TemplateKey: "notification.machine_never_online", Permission: row.Permission, Params: params, LinkPath: "/admin/" + tenant.String() + "/machines/" + fact.MachineID.String(), DedupeKey: "machine-never-online:" + fact.MachineID.String()}
			if typ == "machine.enrolled" {
				n.TemplateKey, n.DedupeKey = "notification.machine_enrolled", "machine-enrolled:"+fact.ApproverID.String()
				err = m.notifySourceOn(ctx, tx, n, id, id)
			} else {
				_, err = m.Notify(ctx, tx, n)
			}
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
