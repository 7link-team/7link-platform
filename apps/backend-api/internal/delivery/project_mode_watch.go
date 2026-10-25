package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const autonomyWatchJob = "delivery.autonomy_watch"
const autonomyHold = "AUTONOMY_PRECONDITION"

type autonomyWatchBody struct {
	ProjectID guid.ID `json:"project_id,omitempty"`
}

func (m *Module) injectAutonomyWatch(s *module.Services) error {
	var err error
	if m.autonomyQueue, err = module.Resolve[jobs.Queue](s); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	return catalog.AddSubscribers(Name, events.Subscriber{Name: autonomyWatchJob, EventTypes: []string{"delegation.granted", "delegation.activated", "delegation.revoked", "delegation.expired", "governance.rule_updated", "governance.settings_updated", "fleet.agent_registered", "fleet.agent_presence", "agent.spawned", "agent.retired", "budget.threshold_reached", "budget.settled", "project.mode_changed"}, Mode: events.Transactional, StartFrom: events.Latest, Handle: m.autonomyWatchEventOn})
}

func (m *Module) autonomyWatchEventOn(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
	if e.TenantID <= 0 {
		return nil
	}
	if m.autonomyQueue == nil {
		return ErrUnavailable
	}
	// Enqueue only: acquiring principal/project locks here would invert the
	// source governance transaction's lock order. The job rereads current facts.
	_, err := m.autonomyQueue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: e.TenantID, Kind: autonomyWatchJob, Payload: autonomyWatchBody{ProjectID: e.ProjectID}, IdempotencyKey: "autonomy-event:" + e.EventID.String(), ConcurrencyKey: "delivery:autonomy:" + e.TenantID.String()})
	return err
}

func (m *Module) autonomyWatchHandler() jobs.Handler {
	return jobs.Handler{Kind: autonomyWatchJob, Timeout: 5 * time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		var body autonomyWatchBody
		if job.TenantID <= 0 || (len(job.Payload) > 0 && json.Unmarshal(job.Payload, &body) != nil) || body.ProjectID < 0 {
			return jobs.Permanent(ErrInvalidArgument)
		}
		return m.watchAutonomy(ctx, job.TenantID, body.ProjectID)
	}}
}

func (m *Module) watchAutonomy(ctx context.Context, tenant, project guid.ID) error {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.assignmentGuard == nil {
		return ErrUnavailable
	}
	db := m.runtime.Database().Write()
	q := query.BindProjects(db, m.service.names)
	var after guid.ID
	for {
		base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Gt(after), q.TeamMode.Eq("AUTONOMOUS"), q.ArchivedAt.IsNull(), q.Stage.NotIn("CLOSED", "CANCELLED")) //nolint:misspell // Frozen protocol state.
		if project > 0 {
			base = base.Where(q.ProjectID.Eq(project))
		}
		rows, err := base.Order(q.ProjectID).Limit(100).Find()
		if err != nil {
			return err
		}
		for _, row := range rows {
			locked, err := m.modePrincipalCandidates(ctx, db, tenant, row.ProjectID)
			if err != nil {
				return err
			}
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := m.assignmentGuard(ctx, tx, "delivery:autonomy:"+tenant.String()); err != nil {
					return err
				}
				return m.watchAutonomyOn(ctx, tx, tenant, row.ProjectID, locked)
			})
			if err != nil {
				return err
			}
			after = row.ProjectID
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

func (m *Module) watchAutonomyOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, locked []guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	// Background evaluation has no acting Human. Lock actual principals without
	// inventing one, then serialize the project just like explicit mode changes.
	if m.principals.Lock == nil || m.service.audit == nil || m.service.events == nil {
		return ErrUnavailable
	}
	for _, id := range locked {
		if id <= 0 {
			return ErrInvalidArgument
		}
		if err := m.principals.Lock(ctx, tx, id); err != nil {
			return err
		}
	}
	q := query.BindProjects(tx, m.service.names)
	p, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if p.TeamMode != "AUTONOMOUS" || slices.Contains([]string{"CLOSED", "CANCELLED"}, p.Stage) { //nolint:misspell // Frozen protocol state.
		return nil
	}
	current, err := m.modePrincipalCandidates(ctx, tx, tenant, project)
	if err != nil {
		return err
	}
	if !slices.Equal(current, locked) {
		return ErrWorkVersion
	}
	criteria, err := m.modePreconditionsOn(ctx, tx, p, locked)
	if err != nil {
		return err
	}
	failed := []string{}
	for _, c := range criteria {
		if c.Key != "P5" && !c.Passed {
			failed = append(failed, c.Key)
		}
	}
	var holds []map[string]json.RawMessage
	if json.Unmarshal([]byte(p.Holds), &holds) != nil {
		return ErrUnavailable
	}
	held := slices.ContainsFunc(holds, func(h map[string]json.RawMessage) bool { return string(h["kind"]) == `"AUTONOMY_PRECONDITION"` })
	lost := len(failed) > 0
	if held == lost {
		return nil
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	if lost {
		kind, _ := json.Marshal(autonomyHold)
		since, _ := json.Marshal(at)
		holds = append(holds, map[string]json.RawMessage{"kind": kind, "since": since})
	} else {
		holds = slices.DeleteFunc(holds, func(h map[string]json.RawMessage) bool { return string(h["kind"]) == `"AUTONOMY_PRECONDITION"` })
	}
	raw, err := json.Marshal(holds)
	if err != nil {
		return err
	}
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Version.Eq(p.Version)).Updates(map[string]any{"holds": string(raw), "version": p.Version + 1, "autonomy_version": p.Version + 1, "updated_at": at})
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrWorkVersion
	}
	typ := "project.autonomy_restored"
	if lost {
		typ = "project.autonomy_lost"
	}
	// The job is a system action even when a test or event caller carries claims.
	system := authn.WithClaims(ctx, nil)
	event := audit.NewDomainEvent(system, tenant, "delivery.autonomy_preconditions_changed", "project", project)
	event.Detail["actor_kind"], event.Detail["authority_source"], event.Detail["held"], event.Detail["failed_conditions"] = "SYSTEM", "SYSTEM:autonomy_watch", lost, failed
	if err := m.service.audit(system, tx, event); err != nil {
		return err
	}
	_, err = m.service.events.Publish(system, tx, events.Event{TenantID: tenant, ProjectID: project, Type: typ, Subject: events.Subject{Kind: "project", ID: project}, SubjectVersion: int64(p.Version + 1), Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:autonomy_watch"}, Data: map[string]any{"held": lost, "failed_conditions": failed, "autonomy_version": int64(p.Version + 1)}})
	return err
}
