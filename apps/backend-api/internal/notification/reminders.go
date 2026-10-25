package notification

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type sourceReminder struct {
	Episode  string  `json:"episode,omitempty"`
	Kind     string  `json:"kind"`
	SourceID guid.ID `json:"source_id"`
	Ordinal  int     `json:"ordinal"`
}
type reminderFact struct {
	episode               string
	project, subject      guid.ID
	due                   time.Time
	active                bool
	typ, permission, path string
}

func (m *Module) injectReminderSources(s *module.Services) error {
	if source, err := module.Resolve[intake.TriageReminderSource](s); err == nil {
		m.triageReminder = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if source, err := module.Resolve[execution.InteractionReminderSource](s); err == nil {
		m.interactionReminder = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	return nil
}

func (m *Module) sourceReminderFactOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in sourceReminder) (reminderFact, error) {
	f := reminderFact{}
	root := "/admin/" + tenant.String()
	switch in.Kind {
	case "review_age", "project_blocked":
		if m.decisionFacts.DeadlineOn == nil {
			return f, ErrUnavailable
		}
		r, found, err := m.decisionFacts.DeadlineOn(ctx, tx, tenant, in.SourceID, in.Kind)
		if err != nil || !found || r.ChangedAt.IsZero() {
			return f, err
		}
		episode := r.ChangedAt.UTC().Format(time.RFC3339Nano)
		active := r.Active && (in.Episode == "" || in.Episode == episode)
		if in.Kind == "review_age" {
			return reminderFact{episode: episode, project: r.ProjectID, subject: in.SourceID, due: r.ChangedAt.Add(24 * time.Hour), active: active, typ: "work_item.review_overdue", permission: "decision_approve", path: root + "/entities/" + r.ProjectID.String() + "/work-items/" + in.SourceID.String()}, nil
		}
		return reminderFact{episode: episode, project: r.ProjectID, subject: in.SourceID, due: r.ChangedAt.Add(72 * time.Hour), active: active, typ: "project.blocked_long", permission: "project_read", path: root + "/entities/" + r.ProjectID.String() + "/project"}, nil

	case "spawn":
		if m.spawnFacts == nil {
			return f, ErrUnavailable
		}
		r, found, err := m.spawnFacts(ctx, tx, tenant, in.SourceID, "grant")
		return reminderFact{subject: in.SourceID, due: r.EndsAt.Add(-72 * time.Hour), active: found && r.State == "ACTIVE", typ: "governance.spawn_grant_expiring", permission: "spawn_grant_manage", path: root + "/governance/spawn-grants?spawn_grant_id=" + in.SourceID.String()}, err
	case "review":
		if in.Ordinal != 1 {
			return f, nil
		}
		if m.decisionFacts.ReviewSheetOn == nil {
			return f, ErrUnavailable
		}
		r, found, err := m.decisionFacts.ReviewSheetOn(ctx, tx, tenant, in.SourceID)
		if err != nil || !found || r.DueAt == nil {
			return f, err
		}
		return reminderFact{project: r.ProjectID, subject: in.SourceID, due: r.DueAt.Add(time.Duration(in.Ordinal-2) * 24 * time.Hour), active: r.State == "OPEN", typ: "review_sheet.due", permission: "requirement_read", path: root + "/entities/" + r.ProjectID.String() + "/review-sheet?review_sheet_id=" + in.SourceID.String()}, nil
	case "triage":
		if m.triageReminder == nil {
			return f, ErrUnavailable
		}
		project, _, due, active, err := m.triageReminder(ctx, tx, tenant, in.SourceID)
		return reminderFact{project: project, subject: in.SourceID, due: due, active: active, typ: "intake.triage_overdue", permission: "intake_read", path: root + "/entities/" + project.String() + "/intake"}, err
	case "interaction":
		if m.interactionReminder == nil {
			return f, ErrUnavailable
		}
		execution, project, due, active, err := m.interactionReminder(ctx, tx, tenant, in.SourceID)
		return reminderFact{project: project, subject: execution, due: due, active: active, typ: "interaction.expiring", permission: "interaction_respond", path: root + "/entities/" + project.String() + "/executions/" + execution.String()}, err
	case "incident":
		if m.incidentFacts == nil {
			return f, ErrUnavailable
		}
		fact, found, err := m.incidentFacts(ctx, tx, tenant, in.SourceID)
		return reminderFact{project: fact.ProjectID, subject: in.SourceID, due: fact.CreatedAt.Add(time.Duration(in.Ordinal) * 24 * time.Hour), active: found && fact.State == "OPEN" && !fact.CreatedAt.IsZero(), typ: "governance.escalation_reminder", permission: "project_read", path: root + "/governance/incidents?incident_id=" + in.SourceID.String()}, err
	}
	return f, ErrInvalid
}

func (m *Module) scheduleSourceReminderOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in sourceReminder) error {
	f, err := m.sourceReminderFactOn(ctx, tx, tenant, in)
	if err != nil || !f.active {
		return err
	}
	if f.episode != "" {
		in.Episode = f.episode
	}
	if m.queue == nil || m.admission == nil {
		return ErrUnavailable
	}
	if err := m.admission(ctx, tx, tenant); err != nil {
		return err
	}
	_, err = m.queue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: tenant, Kind: Name + ".source_reminder", IdempotencyKey: sourceReminderIdentity(in), RunAt: f.due, Payload: in})
	return err
}

func (m *Module) sourceReminderAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, principal guid.ID) (bool, error) {
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(params["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	ordinal, err := strconv.Atoi(params["ordinal"])
	if err != nil || ordinal < 1 || ordinal > 100000 {
		return false, ErrInvalid
	}
	kind := map[string]string{"intake.triage_overdue": "triage", "interaction.expiring": "interaction", "governance.escalation_reminder": "incident"}[row.Type]
	f, err := m.sourceReminderFactOn(ctx, tx, row.TenantID, sourceReminder{Kind: kind, SourceID: id, Ordinal: ordinal})
	if err != nil || !f.active || f.project != row.ProjectID || m.now().Before(f.due) {
		return false, err
	}
	switch kind {
	case "triage":
		if m.intakeTargets == nil {
			return false, ErrUnavailable
		}
		project, channel, _, active, err := m.triageReminder(ctx, tx, row.TenantID, id)
		if err != nil || !active {
			return false, err
		}
		ids, err := m.intakeTargets(ctx, tx, row.TenantID, project, channel)
		return slices.Contains(ids, principal), err
	case "interaction":
		if m.interactionFacts == nil {
			return false, ErrUnavailable
		}
		execution, project, yes, err := m.interactionFacts(ctx, tx, row.TenantID, id, principal)
		return yes && execution == f.subject && project == f.project, err
	case "incident":
		candidate := *row
		candidate.Type = "governance.escalation_triggered"
		return m.sourceAudienceOn(ctx, tx, &candidate, principal)
	}
	return false, ErrInvalid
}

func (m *Module) sourceReminderJob(ctx context.Context, job jobs.Job) error {
	var in sourceReminder
	if json.Unmarshal(job.Payload, &in) != nil || job.TenantID <= 0 || in.SourceID <= 0 || in.Ordinal < 1 || in.Ordinal > 100000 {
		return jobs.Permanent(ErrInvalid)
	}
	if m.candidates == nil {
		return ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	ctx, changes := collectInboxChanges(ctx)
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		f, err := m.sourceReminderFactOn(ctx, tx, job.TenantID, in)
		if err != nil || !f.active {
			return err
		}
		if m.now().Before(f.due) {
			return jobs.RetryAfter(f.due.Sub(m.now()), ErrConflict)
		}
		params := map[string]string{"subject_id": in.SourceID.String(), "ordinal": strconv.Itoa(in.Ordinal)}
		if in.Episode != "" {
			params["episode"] = in.Episode
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		var after guid.ID
		for {
			ids, err := m.candidates(ctx, tx, after, 100)
			if err != nil {
				return err
			}
			for _, id := range ids {
				if id <= after {
					return ErrInvalid
				}
				after = id
				row := &model.Notification{TenantID: job.TenantID, ProjectID: f.project, RecipientPrincipalID: id, Type: f.typ, Permission: f.permission, Params: string(raw)}
				yes, err := m.visibleOn(ctx, tx, row, id)
				if err != nil {
					return err
				}
				if !yes {
					continue
				}
				_, err = m.Notify(ctx, tx, Notification{TenantID: job.TenantID, ProjectID: f.project, Recipients: []guid.ID{id}, Type: f.typ, Severity: Action, TemplateKey: "notification." + map[string]string{"triage": "intake_triage_overdue", "interaction": "interaction_expiring", "incident": "escalation_reminder", "review": "review_sheet_due", "spawn": "spawn_grant_expiring", "review_age": "work_review_overdue", "project_blocked": "project_blocked_long"}[in.Kind], Permission: f.permission, Params: params, LinkPath: f.path, DedupeKey: sourceReminderIdentity(in)})
				if err != nil {
					return err
				}
			}
			if len(ids) < 100 {
				break
			}
		}
		if in.Kind == "incident" {
			in.Ordinal += max(1, int(m.now().Sub(f.due)/(24*time.Hour))+1)
			return m.scheduleSourceReminderOn(ctx, tx, job.TenantID, in)
		}
		return nil
	})
	if err == nil {
		m.broadcastInboxChanges(context.WithoutCancel(ctx), changes)
	}
	return err
}

func (m *Module) deadlineAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	var p map[string]string
	if json.Unmarshal([]byte(row.Params), &p) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(p["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	kind := "review_age"
	if row.Type == "project.blocked_long" {
		kind = "project_blocked"
	}
	f, err := m.sourceReminderFactOn(ctx, tx, row.TenantID, sourceReminder{Kind: kind, SourceID: id, Ordinal: 1, Episode: p["episode"]})
	if err != nil || !f.active || f.project != row.ProjectID || f.episode != p["episode"] || m.now().Before(f.due) {
		return false, err
	}
	if kind == "review_age" {
		if m.workFacts.ReviewEligibleOn == nil {
			return false, ErrUnavailable
		}
		return m.workFacts.ReviewEligibleOn(ctx, tx, row.TenantID, id, actor)
	}
	lead, err := m.leadOn(ctx, tx, row.TenantID, row.ProjectID, actor)
	if err != nil || lead {
		return lead, err
	}
	return m.projectPermissionOn(ctx, tx, row.TenantID, row.ProjectID, actor, "project_takeover")
}

func sourceReminderIdentity(in sourceReminder) string {
	key := "source-reminder:" + in.Kind + ":" + in.SourceID.String() + ":" + strconv.Itoa(in.Ordinal)
	if in.Episode != "" {
		key += ":" + in.Episode
	}
	return key
}
