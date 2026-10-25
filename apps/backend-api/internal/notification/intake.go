package notification

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"gorm.io/gorm"
)

func (m *Module) intakeAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, principal guid.ID) (bool, error) {
	if m.intakeSource == nil || m.intakeTargets == nil {
		return false, ErrUnavailable
	}
	sourceReader := m.intakeSource
	if row.Type == "intake.triaged" {
		if m.intakeTriaged == nil {
			return false, ErrUnavailable
		}
		sourceReader = intake.NotificationSource(m.intakeTriaged)
	}
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(params["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	q := query.BindIntakeReceipts(tx, m.names)
	receipts, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.NotificationID.Eq(row.ID), q.RecipientPrincipalID.Eq(principal)).Find()
	if err != nil {
		return false, err
	}
	ids := []guid.ID{id}
	if len(receipts) > 0 {
		ids = nil
		for _, receipt := range receipts {
			ids = append(ids, receipt.IntakeID)
		}
	}
	count := 0
	for _, source := range ids {
		project, channel, found, err := sourceReader(ctx, tx, row.TenantID, source)
		if err != nil {
			return false, err
		}
		if !found || project != row.ProjectID {
			continue
		}
		targets, err := m.intakeTargets(ctx, tx, row.TenantID, project, channel)
		if err != nil {
			return false, err
		}
		if slices.Contains(targets, principal) {
			count++
		}
	}
	if count > 0 {
		params["intake_count"] = strconv.Itoa(count)
		data, err := json.Marshal(params)
		if err != nil {
			return false, err
		}
		row.Params, row.MergeCount = string(data), count
	}
	return count > 0, nil
}

func (m *Module) projectIntakeEventOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if e.TenantID <= 0 || e.ProjectID <= 0 || e.SubjectID <= 0 || e.SubjectKind != "intake" {
		return ErrInvalid
	}
	if m.intakeSource == nil || m.intakeTargets == nil {
		return ErrUnavailable
	}
	if m.triageReminder != nil {
		if err := m.scheduleSourceReminderOn(ctx, tx, e.TenantID, sourceReminder{Kind: "triage", SourceID: e.SubjectID, Ordinal: 1}); err != nil {
			return err
		}
	}
	project, channel, found, err := m.intakeSource(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || !found || project != e.ProjectID {
		return err
	}
	targets, err := m.intakeTargets(ctx, tx, e.TenantID, project, channel)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if m.guard == nil {
			return ErrUnavailable
		}
		if err := m.principal.LockActive(ctx, tx, target); err != nil {
			return err
		}
		if err := m.guard(ctx, tx, "notification:intake:"+e.TenantID.String()+":"+target.String()); err != nil {
			return err
		}
		q := query.BindIntakeReceipts(tx, m.names)
		old, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.IntakeID.Eq(e.SubjectID), q.RecipientPrincipalID.Eq(target)).First()
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if old != nil {
			continue
		}
		ids, err := m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: project, Recipients: []guid.ID{target}, Type: "intake.received", Severity: Info, TemplateKey: "notification.intake_received", Params: map[string]string{"subject_id": e.SubjectID.String(), "intake_count": "1"}, Permission: "intake_read", LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + project.String() + "/intake", DedupeKey: "intake:channel:" + project.String() + ":" + channel.String()})
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := q.WithContext(ctx).Create(&model.IntakeReceipt{TenantID: e.TenantID, ProjectID: project, ChannelID: channel, IntakeID: e.SubjectID, RecipientPrincipalID: target, NotificationID: ids[0], CreatedAt: m.now()}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Module) projectIntakeTriagedOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent, data map[string]json.RawMessage) error {
	// Only the first persisted card produces the ready notification. Later card
	// revisions are edits, and replay retains the same permanent source key.
	if rawID(data["card_revision"]) != 1 {
		return nil
	}
	if m.intakeTriaged == nil || m.intakeTargets == nil {
		return ErrUnavailable
	}
	project, channel, found, err := m.intakeTriaged(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || !found {
		return err
	}
	ids, err := m.intakeTargets(ctx, tx, e.TenantID, project, channel)
	if err != nil {
		return err
	}
	for _, id := range ids {
		_, err = m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: project, Recipients: []guid.ID{id}, Type: "intake.triaged", Severity: Info, TemplateKey: "notification.intake_triaged", Permission: "intake_read", Params: map[string]string{"subject_id": e.SubjectID.String()}, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + project.String() + "/intake?intake_id=" + e.SubjectID.String(), DedupeKey: "intake-triaged:" + e.SubjectID.String()})
		if err != nil {
			return err
		}
	}
	return nil
}
