package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

const toleranceForecastJob = "clientwork.tolerance_forecast"

type forecastJobBody struct {
	ProjectID guid.ID `json:"project_id,omitempty"`
}

func (m *Module) injectForecastServices(s *module.Services) error {
	var e error
	if m.forecastWork, e = module.Resolve[delivery.ForecastWorkFacts](s); e != nil {
		return e
	}
	if m.forecastExecutions, e = module.Resolve[execution.ForecastFacts](s); e != nil {
		return e
	}
	if m.forecastCosts, e = module.Resolve[budget.SettlementFacts](s); e != nil {
		return e
	}
	if m.forecastQueue, e = module.Resolve[jobs.Queue](s); e != nil {
		return e
	}
	catalog, e := module.Resolve[events.SubscriberCatalog](s)
	if e != nil {
		return e
	}
	return catalog.AddSubscribers(Name, events.Subscriber{Name: "clientwork.tolerance_forecast", EventTypes: []string{"work_item.created", "work_item.state_changed", "requirement.created", "requirement.versioned", "execution.state_changed", "budget.settled", "clientwork.tolerance_changed"}, Mode: events.Transactional, StartFrom: events.Latest, Handle: m.forecastEventOn})
}
func (m *Module) forecastEventOn(ctx context.Context, tx *gorm.DB, event events.Envelope) error {
	if event.TenantID <= 0 || event.ProjectID <= 0 {
		return nil
	}
	if m.forecastQueue == nil {
		return ErrUnavailable
	}
	// One queued refresh per project/minute; execution also enforces the 60s
	// measurement fence, including retries and the hourly schedule overlap.
	_, e := m.forecastQueue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: event.TenantID, Kind: toleranceForecastJob, Payload: forecastJobBody{ProjectID: event.ProjectID}, Delay: time.Minute, IdempotencyKey: fmt.Sprintf("forecast:%s:%d", event.ProjectID, event.OccurredAt.Unix()/60), ConcurrencyKey: "forecast:" + event.ProjectID.String()})
	return e
}
func (m *Module) forecastHandler() jobs.Handler {
	return jobs.Handler{Kind: toleranceForecastJob, Timeout: 5 * time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID <= 0 {
			return jobs.Permanent(ErrInvalid)
		}
		var body forecastJobBody
		if len(job.Payload) > 0 {
			if e := json.Unmarshal(job.Payload, &body); e != nil {
				return jobs.Permanent(ErrInvalid)
			}
		}
		return m.forecastDue(ctx, job.TenantID, body.ProjectID, now())
	}}
}
func (m *Module) forecastDue(ctx context.Context, tenant, project guid.ID, at time.Time) error {
	if tenant <= 0 || project < 0 || at.IsZero() {
		return ErrInvalid
	}
	db := m.writer()
	if db == nil || m.forecastWork.ReadOn == nil || m.forecastExecutions.ReadOn == nil || m.forecastCosts.ExecutionsOn == nil {
		return ErrUnavailable
	}
	var failures []error
	after := guid.ID(0)
	for {
		q := query.BindTolerance(db, m.names)
		scope := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(after))
		if project > 0 {
			scope = scope.Where(q.ProjectID.Eq(project))
		}
		rows, e := scope.Order(q.ID).Limit(100).Find()
		if e != nil {
			return e
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.forecastOneOn(ctx, tx, tenant, row, at) }); e != nil {
				failures = append(failures, e)
			}
		}
		after = rows[len(rows)-1].ID
		if len(rows) < 100 {
			break
		}
	}
	return errors.Join(failures...)
}
