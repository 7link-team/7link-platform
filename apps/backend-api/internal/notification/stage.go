package notification

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"gorm.io/gorm"
)

func (m *Module) stageAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if m.decisionFacts.StageSuggestionOn == nil {
		return false, ErrUnavailable
	}
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	target, err := m.decisionFacts.StageSuggestionOn(ctx, tx, row.TenantID, row.ProjectID)
	if err != nil || target == "" || target != params["target"] {
		return false, err
	}
	return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, actor)
}

func (m *Module) stageSuggestionJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return ErrInvalid
	}
	if m.decisionFacts.StageProjectsOn == nil || m.decisionFacts.PublishStageReadyOn == nil {
		return ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	if len(job.Payload) > 0 && string(job.Payload) != "null" && string(job.Payload) != "{}" {
		var in struct {
			Project guid.ID `json:"project_id"`
		}
		if json.Unmarshal(job.Payload, &in) != nil || in.Project <= 0 {
			return jobs.Permanent(ErrInvalid)
		}
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if m.projectOccurrences == nil {
				return ErrUnavailable
			}
			recent, err := m.projectOccurrences(ctx, tx, job.TenantID, in.Project, stageHealthEventTypes, m.now().Add(-10*time.Second))
			if err != nil {
				return err
			}
			var due time.Time
			for _, event := range recent {
				if next := event.At.Add(10 * time.Second); next.After(due) {
					due = next
				}
			}
			if due.After(m.now()) {
				return jobs.RetryAfter(due.Sub(m.now()), ErrConflict)
			}
			return nil
		})
		if err != nil {
			return err
		}
		// Release event-history locks before acquiring source project/principal
		// locks; the source rechecks all guards in its publication transaction.
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.decisionFacts.PublishStageReadyOn(ctx, tx, job.TenantID, in.Project) })
	}
	var after guid.ID
	for {
		var ids []guid.ID
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var err error
			ids, err = m.decisionFacts.StageProjectsOn(ctx, tx, job.TenantID, after, 100)
			return err
		})
		if err != nil {
			return err
		}
		for _, project := range ids {
			if project <= after {
				return ErrInvalid
			}
			after = project
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.decisionFacts.PublishStageReadyOn(ctx, tx, job.TenantID, project) }); err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}

var stageHealthEventTypes = []string{"budget.threshold_reached", "clientwork.package_decided", "project.stage_changed", "delegation.granted", "delegation.revoked", "delegation.expired", "work_item.state_changed", "work_item.created", "work_item.ready_checked", "work_item.dependency_changed", "verification.completed", "project.constitution_activated"}

func (m *Module) stageHealthEventOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if e.ProjectID <= 0 || m.decisionFacts.PublishStageReadyOn == nil {
		return nil
	}
	if m.queue == nil || m.admission == nil {
		return ErrUnavailable
	}
	if err := m.admission(ctx, tx, e.TenantID); err != nil {
		return err
	}
	_, err := m.queue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: e.TenantID, Kind: Name + ".stage_suggestions", IdempotencyKey: "stage-health:" + e.ProjectID.String() + ":" + m.now().UTC().Truncate(10*time.Second).Format(time.RFC3339), RunAt: m.now().Add(10 * time.Second), Payload: map[string]any{"project_id": e.ProjectID}})
	return err
}

func (m *Module) notifyStageSuggestionOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	target, err := m.decisionFacts.StageSuggestionOn(ctx, tx, tenant, project)
	if err != nil || target == "" {
		return err
	}
	params := map[string]string{"subject_id": project.String(), "target": target}
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
			row := &model.Notification{TenantID: tenant, ProjectID: project, RecipientPrincipalID: actor, Type: "project.stage_suggestion", Permission: "project_read", Params: string(raw)}
			allowed, err := m.visibleOn(ctx, tx, row, actor)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			if err := m.principal.LockActive(ctx, tx, actor); err != nil {
				return err
			}
			if err := m.guard(ctx, tx, "notification:stage:"+tenant.String()+":"+actor.String()); err != nil {
				return err
			}
			key := "stage-suggestion:" + project.String() + ":" + target + ":"
			q := query.BindNotifications(tx, m.names)
			old, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.RecipientPrincipalID.Eq(actor), q.Type.Eq(row.Type), q.DedupeKey.Like(key+"%")).Order(q.CreatedAt.Desc(), q.ID.Desc()).First()
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if old != nil && m.now().Before(old.CreatedAt.Add(24*time.Hour)) {
				continue
			}
			_, err = m.Notify(ctx, tx, Notification{TenantID: tenant, ProjectID: project, Recipients: []guid.ID{actor}, Type: row.Type, Severity: Info, TemplateKey: "notification.project_stage_suggestion", Permission: row.Permission, Params: params, LinkPath: "/admin/" + tenant.String() + "/entities/" + project.String() + "/project?view=stages", DedupeKey: key + m.now().UTC().Format("20060102T150405.000000000")})
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
