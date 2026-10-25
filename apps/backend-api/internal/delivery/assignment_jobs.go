package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/module"
	"github.com/prometheus/client_golang/prometheus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const assignmentReclaimJob = "delivery.lease_reclaim"

func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{m.artifactReconcileHandler(), {Kind: assignmentReclaimJob, Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID <= 0 {
			return jobs.Permanent(ErrInvalidArgument)
		}
		return m.reclaimHumanAssignments(ctx, job.TenantID, time.Now().UTC().Truncate(time.Millisecond))
	}}, m.autonomyWatchHandler(), m.attentionCloseHandler(), m.projectBudgetEnforceHandler()}
}
func (*Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: artifactReconcileJob, Kind: artifactReconcileJob, Interval: 5 * time.Minute, TenantScope: jobs.ScopePerTenant}, {Name: assignmentReclaimJob, Kind: assignmentReclaimJob, Interval: 30 * time.Second, TenantScope: jobs.ScopePerTenant}, {Name: autonomyWatchJob, Kind: autonomyWatchJob, Interval: time.Hour, TenantScope: jobs.ScopePerTenant}, {Name: attentionJob, Kind: attentionJob, Interval: time.Minute, TenantScope: jobs.ScopePerTenant}, {Name: projectBudgetEnforceJob, Kind: projectBudgetEnforceJob, Interval: 5 * time.Second, TenantScope: jobs.ScopePerTenant}}
}

func (m *Module) reclaimHumanAssignments(ctx context.Context, tenant guid.ID, now time.Time) (result error) {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.service == nil || m.assignmentGuard == nil {
		return ErrUnavailable
	}
	defer func() {
		m.leaseFailureMu.Lock()
		if m.leaseFailureRounds == nil {
			m.leaseFailureRounds = make(map[guid.ID]int)
		}
		if result != nil {
			m.leaseFailureRounds[tenant]++
		} else {
			delete(m.leaseFailureRounds, tenant)
		}
		rounds := m.leaseFailureRounds[tenant]
		m.leaseFailureMu.Unlock()
		if rounds >= 5 {
			slog.ErrorContext(ctx, "Human assignment lease recovery failed for at least five consecutive sweeps", "tenant_id", tenant, "consecutive_sweeps", rounds)
		}
	}()
	db := m.runtime.Database().Write()
	q := query.BindAssignments(db, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Status.Eq("ACTIVE"), q.AssigneeKind.Eq("HUMAN"), q.Role.Eq("EXECUTOR"), q.EnsemblePlanID.IsNull(), q.LeaseExpiresAt.Lt(now.Add(-time.Minute))).Order(q.LeaseExpiresAt, q.ID).Limit(200).Find()
	if err != nil {
		if m.leaseFailures != nil {
			m.leaseFailures.Inc()
		}
		return err
	}
	var failures []error
	for _, row := range rows {
		owner, e := m.preparationOwner(ctx, db, tenant, row.ProjectID, row.WorkItemID)
		if e == nil {
			e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if e := m.assignmentGuard(ctx, tx, fmt.Sprintf("delivery:lease:%s:%s", tenant.String(), row.ID.String())); e != nil {
					return e
				}
				return m.reclaimHumanAssignmentOn(ctx, tx, tenant, row.ProjectID, row.WorkItemID, row.ID, row.AssigneePrincipalID, owner, now)
			})
		}
		if e != nil {
			failures = append(failures, e)
			if m.leaseFailures != nil {
				m.leaseFailures.Inc()
			}
			slog.ErrorContext(ctx, "Human assignment lease recovery failed", "assignment_id", row.ID, "tenant_id", tenant, "error", e)
		}
	}
	return errors.Join(failures...)
}

func (m *Module) Collector() prometheus.Collector { return m.leaseFailures }

// System recovery releases expired ownership even after its holder loses
// authority. The current IAM fence is still acquired before business locks.
// It never infers execution completion from a missing execution capability.
func (m *Module) reclaimHumanAssignmentOn(ctx context.Context, tx *gorm.DB, tenant, entity, workID, id, holder, owner guid.ID, now time.Time) error {
	if m == nil || m.service == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.members == nil || m.workPolicy == nil || m.ids == nil || m.service.audit == nil || m.service.events == nil {
		return ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 || entity <= 0 || workID <= 0 || id <= 0 || holder <= 0 || owner <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	principals := []guid.ID{holder, owner}
	slices.Sort(principals)
	principals = slices.Compact(principals)
	for _, principal := range principals {
		if err := m.principals.Lock(ctx, tx, principal); err != nil {
			return err
		}
	}
	if _, err := m.workPolicy(ctx, tx, tenant, entity, "work_item_execute", holder); err != nil {
		return err
	}
	p := query.BindProjects(tx, m.service.names)
	if _, err := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(entity)).First(); err != nil {
		return err
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, err := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(entity), w.ID.Eq(workID)).First()
	if err != nil {
		return err
	}
	if work.AccountablePrincipalID != owner {
		return ErrWorkVersion
	}
	q := query.BindAssignments(tx, m.service.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(entity), q.WorkItemID.Eq(workID), q.ID.Eq(id)).First()
	if err != nil {
		return err
	}
	if row.Status != "ACTIVE" || row.AssigneeKind != "HUMAN" || row.Role != "EXECUTOR" || row.EnsemblePlanID != nil || row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.Before(now.Add(-time.Minute)) {
		return nil
	}
	if row.AssigneePrincipalID != holder {
		return ErrWorkVersion
	}
	if work.CurrentExecutionID != nil {
		return ErrUnavailable
	}
	if work.CurrentClaimID != nil || work.ArchivedAt != nil || work.PausedAt != nil || !slices.Contains([]string{"CLAIMED", "IN_PROGRESS"}, work.State) {
		return nil
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, owner)
	if err != nil {
		return err
	}
	member, err := m.members.IsMemberActiveOn(ctx, tx, tenant, owner)
	if err != nil {
		return err
	}
	issues, blocks, err := m.evaluatePreparationOn(ctx, tx, work, active && human.ID == owner && member)
	if err != nil {
		return err
	}
	expected := row.Version
	row.Status = "EXPIRED"
	row.ActiveSlot = nil
	row.EndedAt = &now
	reason := "LEASE_EXPIRED"
	row.EndReason = &reason
	row.Version++
	row.UpdatedAt = now
	row.UpdatedBy = nil
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(expected), q.Status.Eq("ACTIVE")).Select(q.Status, q.ActiveSlot, q.EndedAt, q.EndReason, q.Version, q.UpdatedAt, q.UpdatedBy).Updates(row)
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrWorkVersion
	}
	before := work.State
	work.ReadinessOK = len(issues) == 0
	encoded, _ := json.Marshal(issues)
	work.ReadinessIssues = string(encoded)
	encoded, _ = json.Marshal(blocks)
	work.BlockReasons = string(encoded)
	switch {
	case len(blocks) > 0:
		work.State = "BLOCKED"
	case !work.ReadinessOK:
		work.State = "DRAFT"
	default:
		work.State = "READY"
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.lease_expired", "assignment", row.ID)
	event.Detail["work_item_id"] = workID.String()
	event.Detail["system_job"] = assignmentReclaimJob
	if err = m.service.audit(ctx, tx, event); err != nil {
		return err
	}
	return m.assignmentWorkChangedByOn(ctx, tx, work, row, 0, "SYSTEM", "SYSTEM:"+assignmentReclaimJob, before, reason, now)
}

var _ jobs.HandlerProvider = (*Module)(nil)
var _ jobs.ScheduleProvider = (*Module)(nil)
var _ module.PrometheusCollectorModule = (*Module)(nil)
