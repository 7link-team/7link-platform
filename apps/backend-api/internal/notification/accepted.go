package notification

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"strconv"
	"time"
)

const acceptedUndeliveredType = "delivery.accepted_undelivered"

func (m *Module) acceptedAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if m.decisionFacts.AcceptedUndeliveredOn == nil {
		return false, ErrUnavailable
	}
	ids, err := m.decisionFacts.AcceptedUndeliveredOn(ctx, tx, row.TenantID, row.ProjectID, m.now())
	if err != nil || len(ids) == 0 {
		return false, err
	}
	yes, err := m.leadOn(ctx, tx, row.TenantID, row.ProjectID, actor)
	if err != nil || yes {
		return yes, err
	}
	mode, err := m.workFacts.ProjectModeOn(ctx, tx, row.TenantID, row.ProjectID)
	if err != nil || mode != "AUTONOMOUS" {
		return false, err
	}
	return m.projectPermissionOn(ctx, tx, row.TenantID, row.ProjectID, actor, "project_takeover")
}
func (m *Module) acceptedSummaryOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	ids, err := m.decisionFacts.AcceptedUndeliveredOn(ctx, tx, tenant, project, m.now())
	if err != nil || len(ids) == 0 {
		return err
	}
	params := map[string]string{"subject_id": project.String(), "source_count": strconv.Itoa(len(ids)), "coverage": "ACCEPTED_ONLY_READY_SENT_MEMBERSHIP_UNAVAILABLE"}
	raw, _ := json.Marshal(params)
	var after guid.ID
	for {
		actors, err := m.candidates(ctx, tx, after, 100)
		if err != nil {
			return err
		}
		for _, actor := range actors {
			if actor <= after {
				return ErrInvalid
			}
			after = actor
			row := &model.Notification{TenantID: tenant, ProjectID: project, RecipientPrincipalID: actor, Type: acceptedUndeliveredType, Permission: "project_read", Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, actor)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			if err = m.principal.LockActive(ctx, tx, actor); err != nil {
				return err
			}
			if err = m.guard(ctx, tx, "notification:accepted:"+tenant.String()+":"+project.String()+":"+actor.String()); err != nil {
				return err
			}
			q := query.BindNotifications(tx, m.names)
			old, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.RecipientPrincipalID.Eq(actor), q.Type.Eq(acceptedUndeliveredType)).Order(q.CreatedAt.Desc()).First()
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if old != nil && m.now().Before(old.CreatedAt.Add(24*time.Hour)) {
				continue
			}
			_, err = m.Notify(ctx, tx, Notification{TenantID: tenant, ProjectID: project, Recipients: []guid.ID{actor}, Type: acceptedUndeliveredType, Severity: Info, Permission: "project_read", TemplateKey: "notification.accepted_undelivered", Params: params, LinkPath: "/admin/" + tenant.String() + "/entities/" + project.String() + "/project?view=delivery", DedupeKey: "accepted:" + project.String() + ":" + m.now().Format(time.RFC3339Nano)})
			if err != nil {
				return err
			}
		}
		if len(actors) < 100 {
			return nil
		}
	}
}
func (m *Module) acceptedSummaryJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return jobs.Permanent(ErrInvalid)
	}
	if m.decisionFacts.AcceptedUndeliveredOn == nil || m.decisionFacts.StageProjectsOn == nil || m.candidates == nil {
		return ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	ctx, changes := collectInboxChanges(ctx)
	var after guid.ID
	for {
		var ids []guid.ID
		err = db.Transaction(func(tx *gorm.DB) error {
			var e error
			ids, e = m.decisionFacts.StageProjectsOn(ctx, tx, job.TenantID, after, 100)
			return e
		})
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id <= after {
				return ErrInvalid
			}
			after = id
			if err = db.Transaction(func(tx *gorm.DB) error { return m.acceptedSummaryOn(ctx, tx, job.TenantID, id) }); err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			m.broadcastInboxChanges(context.WithoutCancel(ctx), changes)
			return nil
		}
	}
}
