package notification

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

func (*Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: "notification.worklog_weekly", Kind: "notification.worklog_weekly", Cron: "0 0 * * 1", Timezone: "UTC", TenantScope: jobs.ScopePerTenant, Misfire: jobs.MisfireCoalesce, Payload: map[string]any{}}, {Name: "notification.stage_suggestions", Kind: "notification.stage_suggestions", Interval: 5 * time.Minute, Timezone: "UTC", TenantScope: jobs.ScopePerTenant, Misfire: jobs.MisfireCoalesce, Payload: map[string]any{}}, {Name: "notification.machine_offline_scan", Kind: "notification.machine_offline_scan", Interval: 15 * time.Second, Timezone: "UTC", TenantScope: jobs.ScopePerTenant, Misfire: jobs.MisfireCoalesce, Payload: map[string]any{}}, {Name: "notification.accepted_summary", Kind: "notification.accepted_summary", Interval: time.Hour, Timezone: "UTC", TenantScope: jobs.ScopePerTenant, Misfire: jobs.MisfireCoalesce, Payload: map[string]any{}}}
}

func (m *Module) worklogAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if m.worklogApprovalCount == nil {
		return false, ErrUnavailable
	}
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	cutoff, err := time.Parse(time.RFC3339Nano, params["cutoff"])
	if err != nil {
		return false, ErrInvalid
	}
	count, err := m.worklogApprovalCount(ctx, tx, row.TenantID, actor, cutoff)
	if err != nil || count == 0 {
		return false, err
	}
	params["source_count"] = strconv.Itoa(count)
	data, err := json.Marshal(params)
	row.Params, row.MergeCount = string(data), count
	return true, err
}

func (m *Module) worklogWeeklyJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return ErrInvalid
	}
	if m.worklogApprovalCount == nil || m.candidates == nil {
		return ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	now := m.now().UTC()
	// A manual or delayed job cannot invent an extra weekly occurrence.
	if now.Weekday() != time.Monday {
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
				count, err := m.worklogApprovalCount(ctx, tx, job.TenantID, actor, now)
				if err != nil {
					return err
				}
				if count == 0 {
					continue
				}
				_, err = m.Notify(ctx, tx, Notification{TenantID: job.TenantID, Recipients: []guid.ID{actor}, Type: "worklog.approval_pending", Severity: Info, TemplateKey: "notification.worklog_approval_pending", Params: map[string]string{"source_count": strconv.Itoa(count), "cutoff": now.Format(time.RFC3339Nano)}, LinkPath: "/admin/" + job.TenantID.String() + "/governance/inbox?kind=WORKLOG_APPROVAL", DedupeKey: "worklog-week:" + now.Format("2006-01-02")})
				if err != nil {
					return err
				}
			}
			if len(ids) < 100 {
				return nil
			}
		}
	})
}
