package notification

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"gorm.io/gorm"
)

func (m *Module) machineEmailOn(ctx context.Context, tx *gorm.DB, row *model.Notification, msg *EmailMessage, runAt *time.Time) (bool, error) {
	if row.Type == "machine.offline" {
		at, err := time.Parse(time.RFC3339Nano, msg.Params["offline_at"])
		if err != nil {
			return false, ErrInvalid
		}
		due := at.Add(15*time.Minute + time.Millisecond)
		if due.After(*runAt) {
			*runAt = due
		}
		return true, nil
	}
	// Recovery email is a reply to an actual sent outage message. A queued or
	// suppressed email is not evidence that this recipient received the thread.
	original, err := guid.RequireID(msg.Params["original_notification_id"])
	if err != nil {
		return false, ErrInvalid
	}
	q := query.BindDeliveries(tx, m.names)
	old, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.NotificationID.Eq(original), q.RecipientPrincipalID.Eq(row.RecipientPrincipalID), q.Channel.Eq(string(Email)), q.State.Eq("SENT")).Order(q.ID.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !validMessageID(old.MessageID) {
		return false, nil
	}
	msg.InReplyTo, msg.References = old.MessageID, []string{old.MessageID}
	return true, nil
}

func (m *Module) machineEmailNextOn(ctx context.Context, tx *gorm.DB, row *model.Delivery) (time.Time, error) {
	if row.NotificationID == nil || row.RecipientPrincipalID == nil {
		return time.Time{}, nil
	}
	n := query.BindNotifications(tx, m.names)
	notice, err := n.WithContext(ctx).Where(n.TenantID.Eq(row.TenantID), n.ID.Eq(*row.NotificationID)).First()
	if err != nil {
		return time.Time{}, err
	}
	if notice.Type != "machine.offline" || notice.Permission != "" {
		return time.Time{}, nil
	}
	q := query.BindDeliveries(tx, m.names)
	last, err := q.WithContext(ctx).Join(n, n.TenantID.EqCol(q.TenantID), n.ID.EqCol(q.NotificationID)).Where(q.TenantID.Eq(row.TenantID), q.RecipientPrincipalID.Eq(*row.RecipientPrincipalID), q.ID.Neq(row.ID), q.Channel.Eq(string(Email)), q.State.In("SENT", "SENDING"), q.UpdatedAt.Gt(m.now().Add(-time.Hour)), n.Type.Eq("machine.offline")).Order(q.UpdatedAt.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return last.UpdatedAt.Add(time.Hour), nil
}

type machineOfflineInput struct {
	MachineID guid.ID `json:"machine_id"`
	SessionID guid.ID `json:"session_id"`
}

// The scan covers already-offline machines whose older source event was
// consumed before this consumer existed. Per-cycle receipts make both paths safe.
func (m *Module) machineOfflineScanJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return jobs.Permanent(ErrInvalid)
	}
	if m.machineOfflineMachines == nil || m.machineNoticeFacts == nil {
		return ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	var after guid.ID
	for {
		var ids []guid.ID
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var err error
			ids, err = m.machineOfflineMachines(ctx, tx, job.TenantID, after, 100)
			return err
		})
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id <= after {
				return ErrInvalid
			}
			after = id
			eventCtx, changes := collectInboxChanges(ctx)
			err := db.WithContext(eventCtx).Transaction(func(tx *gorm.DB) error {
				f, found, err := m.machineNoticeFacts(eventCtx, tx, job.TenantID, id, 0)
				if err != nil || !found || !f.Offline || !m.now().After(f.OfflineAt.Add(time.Minute)) {
					return err
				}
				if m.candidates == nil {
					return ErrUnavailable
				}
				return m.notifyOfflineMachineOn(eventCtx, tx, job.TenantID, f)
			})
			if err != nil {
				return err
			}
			m.broadcastInboxChanges(context.WithoutCancel(ctx), changes)
		}
		if len(ids) < 100 {
			return nil
		}
	}
}

func (m *Module) machineAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if row.Type == "machine.offline" && row.ID > 0 {
		return m.sourceGroupAudienceOn(ctx, tx, row, actor)
	}
	if m.machineNoticeFacts == nil {
		return false, ErrUnavailable
	}
	var p map[string]string
	if json.Unmarshal([]byte(row.Params), &p) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(p["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	f, found, err := m.machineNoticeFacts(ctx, tx, row.TenantID, 0, id)
	if err != nil || !found {
		return false, err
	}
	if row.Type == "machine.offline" && (!f.Offline || !m.now().After(f.OfflineAt.Add(time.Minute))) {
		return false, nil
	}
	if row.Type == "machine.recovered" && !f.Recovered {
		return false, nil
	}
	c, err := m.tenantPermission(ctx, tx, row.TenantID, "machine_manage", actor)
	if err != nil || c.AllowAll {
		return c.AllowAll, err
	}
	if slices.Contains(f.Owners, actor) {
		return true, nil
	}
	if len(f.Executions) == 0 {
		return false, nil
	}
	if m.executionFacts == nil || m.decisionFacts.FactsOn == nil || m.decisionFacts.WorkReadableOn == nil {
		return false, ErrUnavailable
	}
	for _, id := range f.Executions {
		e, err := m.executionFacts(ctx, tx, row.TenantID, id)
		if err != nil {
			return false, err
		}
		w, found, err := m.decisionFacts.FactsOn(ctx, tx, row.TenantID, e.WorkItemID, "work_item")
		if err != nil {
			return false, err
		}
		if !found || w.ProjectID != e.ProjectID || (e.InitiatorID != actor && w.AccountableID != actor) {
			continue
		}
		yes, err := m.decisionFacts.WorkReadableOn(ctx, tx, row.TenantID, e.WorkItemID, actor)
		if err != nil || yes {
			return yes, err
		}
	}
	return false, nil
}

func (m *Module) projectMachineOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent, state string) error {
	if e.TenantID <= 0 || e.SubjectID <= 0 || e.SubjectKind != "machine" {
		return ErrInvalid
	}
	if m.machineNoticeFacts == nil {
		return ErrUnavailable
	}
	if state == "OFFLINE" {
		f, found, err := m.machineNoticeFacts(ctx, tx, e.TenantID, e.SubjectID, 0)
		if err != nil || !found || !f.Offline {
			return err
		}
		if m.queue == nil || m.admission == nil {
			return ErrUnavailable
		}
		if err = m.admission(ctx, tx, e.TenantID); err != nil {
			return err
		}
		_, err = m.queue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: e.TenantID, Kind: Name + ".machine_offline", IdempotencyKey: "machine-offline:" + f.SessionID.String(), RunAt: f.OfflineAt.Add(time.Minute + time.Millisecond), Payload: machineOfflineInput{f.MachineID, f.SessionID}})
		return err
	}
	if state != "ONLINE" && state != "DRAINING" {
		return nil
	}
	// Recovery goes only to recipients of a real offline notice. It is not a
	// broadcast to newly authorized managers or unrelated historical sessions.
	q := query.BindSourceReceipts(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.Type.Eq("machine.offline")).Find()
	if err != nil {
		return err
	}
	for _, receipt := range rows {
		f, found, err := m.machineNoticeFacts(ctx, tx, e.TenantID, e.SubjectID, receipt.SourceID)
		if err != nil {
			return err
		}
		if !found || !f.Recovered {
			continue
		}
		params := map[string]string{"subject_id": f.SessionID.String(), "machine_id": f.MachineID.String(), "original_notification_id": receipt.NotificationID.String()}
		raw, _ := json.Marshal(params)
		row := &model.Notification{TenantID: e.TenantID, RecipientPrincipalID: receipt.RecipientPrincipalID, Type: "machine.recovered", Params: string(raw)}
		yes, err := m.visibleOn(ctx, tx, row, receipt.RecipientPrincipalID)
		if err != nil {
			return err
		}
		if !yes {
			continue
		}
		err = m.notifySourceOn(ctx, tx, Notification{TenantID: e.TenantID, Recipients: []guid.ID{receipt.RecipientPrincipalID}, Type: "machine.recovered", Severity: Info, TemplateKey: "notification.machine_recovered", Params: params, LinkPath: "/admin/" + e.TenantID.String() + "/machines", DedupeKey: "machine-recovered:" + f.SessionID.String()}, f.SessionID, f.SessionID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) machineOfflineJob(ctx context.Context, job jobs.Job) error {
	var in machineOfflineInput
	if json.Unmarshal(job.Payload, &in) != nil || job.TenantID <= 0 || in.MachineID <= 0 || in.SessionID <= 0 {
		return jobs.Permanent(ErrInvalid)
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	eventCtx, changes := collectInboxChanges(ctx)
	err = db.WithContext(eventCtx).Transaction(func(tx *gorm.DB) error {
		if m.machineNoticeFacts == nil || m.candidates == nil {
			return ErrUnavailable
		}
		f, found, err := m.machineNoticeFacts(eventCtx, tx, job.TenantID, in.MachineID, in.SessionID)
		if err != nil || !found || !f.Offline {
			return err
		}
		due := f.OfflineAt.Add(time.Minute + time.Millisecond)
		if m.now().Before(due) {
			return jobs.RetryAfter(due.Sub(m.now()), ErrConflict)
		}
		return m.notifyOfflineMachineOn(eventCtx, tx, job.TenantID, f)
	})
	if err == nil {
		m.broadcastInboxChanges(context.WithoutCancel(ctx), changes)
	}
	return err
}

func (m *Module) notifyOfflineMachineOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, f fleet.MachineNoticeFact) error {
	params := map[string]string{"subject_id": f.SessionID.String(), "machine_id": f.MachineID.String(), "source_count": "1", "offline_at": f.OfflineAt.UTC().Format(time.RFC3339Nano)}
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
			row := &model.Notification{TenantID: tenant, RecipientPrincipalID: actor, Type: "machine.offline", Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, actor)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			err = m.notifySourceOn(ctx, tx, Notification{TenantID: tenant, Recipients: []guid.ID{actor}, Type: "machine.offline", Severity: Action, TemplateKey: "notification.machine_offline", Params: params, LinkPath: "/admin/" + tenant.String() + "/machines", DedupeKey: "machine-offline"}, f.SessionID, f.SessionID)
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
