package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

const dispatchScanJob = "clientwork.dispatch_scan"
const dispatchCycleJob = "clientwork.dispatch_cycle"

type dispatchCycleBody struct {
	ProjectID guid.ID `json:"project_id"`
}

func (m *Module) injectDispatchScheduling(s *module.Services) error {
	var err error
	if m.dispatchStarvation, err = module.Resolve[delivery.DispatchStarvationFacts](s); err != nil {
		return err
	}
	if m.dispatchSchedule, err = module.Resolve[delivery.DispatchScheduleFacts](s); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	return catalog.AddSubscribers(Name, events.Subscriber{Name: dispatchCycleJob, EventTypes: []string{"work_item.created", "work_item.state_changed", "work_item.assigned", "project.stage_changed", "agents.changed", "fleet.agent_presence", "machine.state_changed"}, Mode: events.Transactional, StartFrom: events.Latest, Handle: m.dispatchScheduleEventOn})
}
func (m *Module) queueDispatchOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, at time.Time) error {
	if m.forecastQueue == nil {
		return ErrUnavailable
	}
	kind := dispatchCycleJob
	if project == 0 {
		kind = dispatchScanJob
	}
	_, err := m.forecastQueue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: tenant, Kind: kind, Payload: dispatchCycleBody{ProjectID: project}, Delay: 30 * time.Second, IdempotencyKey: fmt.Sprintf("dispatch:%s:%d", project, at.Unix()/30), ConcurrencyKey: "dispatch:" + project.String()})
	return err
}
func (m *Module) dispatchScheduleEventOn(ctx context.Context, tx *gorm.DB, event events.Envelope) error {
	if event.TenantID <= 0 {
		return nil
	}
	return m.queueDispatchOn(ctx, tx, event.TenantID, event.ProjectID, event.OccurredAt)
}
func (m *Module) dispatchHandlers() []jobs.Handler {
	return []jobs.Handler{
		{Kind: dispatchStarvationJob, Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
			return m.scanDispatchStarvation(ctx, job.TenantID, now())
		}},
		{Kind: dispatchScanJob, Timeout: 5 * time.Minute, Run: func(ctx context.Context, job jobs.Job) error { return m.scanDispatch(ctx, job.TenantID, now()) }},
		{Kind: dispatchCycleJob, Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
			var body dispatchCycleBody
			if err := json.Unmarshal(job.Payload, &body); err != nil || job.TenantID <= 0 || body.ProjectID <= 0 {
				return jobs.Permanent(ErrInvalid)
			}
			return m.runDispatchCycle(ctx, job.TenantID, body.ProjectID, now())
		}},
	}
}
func (m *Module) scanDispatch(ctx context.Context, tenant guid.ID, at time.Time) error {
	if tenant <= 0 || at.IsZero() {
		return jobs.Permanent(ErrInvalid)
	}
	db := m.writer()
	if db == nil || m.dispatchSchedule.ProjectsOn == nil {
		return ErrUnavailable
	}
	after := guid.ID(0)
	for {
		var projects []guid.ID
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var err error
			projects, err = m.dispatchSchedule.ProjectsOn(ctx, tx, tenant, after)
			if err != nil {
				return err
			}
			for _, project := range projects {
				if err = m.queueDispatchOn(ctx, tx, tenant, project, at); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if len(projects) == 0 {
			return nil
		}
		last := projects[len(projects)-1]
		if last <= after {
			return ErrUnavailable
		}
		after = last
		if len(projects) < 100 {
			return nil
		}
	}
}
func (m *Module) runDispatchCycle(ctx context.Context, tenant, project guid.ID, at time.Time) error {
	if tenant <= 0 || project <= 0 || at.IsZero() {
		return jobs.Permanent(ErrInvalid)
	}
	db := m.writer()
	if db == nil || m.dispatchSchedule.ReadyOn == nil {
		return ErrUnavailable
	}
	var batch delivery.DispatchScheduleBatch
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		batch, err = m.dispatchSchedule.ReadyOn(ctx, tx, tenant, project, at)
		return err
	})
	if err != nil || batch.PrincipalID <= 0 {
		return err
	}
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, EntityID: project, PrincipalID: batch.PrincipalID, SubjectType: authn.SubjectTypePrincipal})
	var failures []error
	attempted := 0
	for _, work := range batch.Items {
		q := query.BindDispatchProposal(db, m.names)
		count, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(work.ID), q.State.Eq("PROPOSED"), q.ExpiresAt.Gt(at)).Count()
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if count > 0 {
			continue
		}
		if attempted == 10 {
			break
		}
		attempted++
		_, err = m.autoDispatchHTTP(ctx, &DispatchAutoInput{EntityID: project.String(), Key: fmt.Sprintf("schedule:%s:%d:%d", work.ID, work.Version, at.Unix()/300), Body: DispatchAutoBody{WorkItemID: work.ID, ExpectedWorkVersion: work.Version}})
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
