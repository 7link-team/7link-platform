package agents

import (
	activityi18n "7link.com/platform/internal/agents/i18n"
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

const handoverTimeout = 10 * time.Minute

func (m *Module) prepareRetirementHandoverOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
	q := query.BindRetirement(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if r.State != "IN_PROGRESS" {
		return nil
	}
	if m.retirementHandover.PrepareOn != nil {
		return m.retirementHandover.PrepareOn(ctx, tx, tenant, r.AgentPrincipalID)
	}
	return nil
}

func (m *Module) retirementJob() jobs.Handler {
	return jobs.Handler{DisplayNameKey: activityi18n.Namespace + ".activity_job_agents_retirement_progress_name", DescriptionKey: activityi18n.Namespace + ".activity_job_agents_retirement_progress_description", Kind: "agents.retirement_progress", Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID <= 0 {
			return jobs.Permanent(ErrInvalid)
		}
		db, err := m.database()
		if err != nil {
			return err
		}
		var after guid.ID
		var failures []error
		for {
			if err = ctx.Err(); err != nil {
				return errors.Join(append(failures, err)...)
			}
			q := query.BindRetirement(db, m.names)
			rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.ID.Gt(after), q.State.In("IN_PROGRESS", "COMPLETED"), q.Step.In("HANDOVER", "REASSIGN", "CLEANUP", "DONE")).Order(q.ID).Limit(100).Find()
			if err != nil {
				return errors.Join(append(failures, err)...)
			}
			for _, r := range rows {
				if r.State == "COMPLETED" && r.Precheck.Progress != nil && r.Precheck.Progress.ReconciliationStatus == "COMPLETE" {
					continue
				}
				// Scheduling confers no authority: each command rechecks the
				// initiating Human's current IAM grants in its own transaction.
				human := authn.WithClaims(ctx, &authn.Claims{TenantID: job.TenantID, PrincipalID: r.CreatedBy, SubjectType: authn.SubjectTypePrincipal})
				err = db.WithContext(human).Transaction(func(tx *gorm.DB) error {
					if r.State == "COMPLETED" {
						return m.reconcileCompletedRetirementOn(human, tx, job.TenantID, r.ID, r.Version)
					}
					_, err := m.advanceRetirementOn(human, tx, job.TenantID, r.ID, r.Version, 0)
					return err
				})
				if err != nil {
					failures = append(failures, fmt.Errorf("retirement %s: %w", r.ID, err))
				}
			}
			if len(rows) < 100 {
				return errors.Join(failures...)
			}
			after = rows[len(rows)-1].ID
		}
	}}
}

func (m *Module) saveRetirementProgressOn(ctx context.Context, tx *gorm.DB, r *model.Retirement, version int, action string) error {
	q := query.BindRetirement(tx, m.names)
	r.Version = version + 1
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(r.ID), q.Version.Eq(version), q.State.Eq("IN_PROGRESS")).Select(q.Precheck, q.Step, q.State, q.AgentVersion, q.StepStartedAt, q.ActiveAgentID, q.CompletedAt, q.Version).Updates(r)
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrConflict
	}
	return m.recordRetirementOn(ctx, tx, r, action)
}

func (m *Module) advanceRetirementHandoverOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, retry bool) (model.Retirement, error) {
	var empty model.Retirement
	if err := m.prepareRetirementHandoverOn(ctx, tx, tenant, id); err != nil {
		return empty, err
	}
	r, err := m.retirementOn(ctx, tx, tenant, id, "agent_retire", true)
	if err != nil {
		return empty, err
	}
	if version < 1 || r.Version != version || r.State != "IN_PROGRESS" {
		return empty, ErrConflict
	}
	if r.Precheck.Progress == nil {
		r.Precheck.Progress = &model.RetirementProgress{}
	}
	p := r.Precheck.Progress
	if retry {
		if r.Step != "SUCCESSOR" || p.HandoverSource != "SYSTEM" || p.HandoverRetryCount != 0 {
			return empty, ErrConflict
		}
		p.HandoverRetryCount = 1
		p.HandoverExecutionID, p.HandoverArtifactID, p.HandoverSource = nil, nil, ""
		r.Step, r.StepStartedAt = "HANDOVER", m.now()
		// Publish the step to the source-owner authority callback in this same
		// transaction. Failure of Launch or final CAS rolls the step back too.
		q := query.BindRetirement(tx, m.names)
		if _, err = q.WithContext(ctx).Where(q.ID.Eq(r.ID), q.TenantID.Eq(tenant), q.Version.Eq(version)).Update(q.Step, "HANDOVER"); err != nil {
			return empty, err
		}
	} else if r.Step != "HANDOVER" {
		return empty, ErrConflict
	}
	if m.retirementHandover.LaunchOn == nil || m.retirementHandover.ReadOn == nil || m.retirementHandover.SystemOn == nil || m.retirementHandover.CancelOn == nil {
		return empty, ErrUnavailable
	}
	if m.retirementWork.ClearLeadsOn != nil {
		if err := m.retirementWork.ClearLeadsOn(ctx, tx, tenant, r.AgentPrincipalID); err != nil {
			return empty, retirementWorkError(err)
		}
	}
	if p.HandoverExecutionID == nil && m.retirementHandover.ReconcileOn != nil {
		ready, err := m.retirementHandover.ReconcileOn(ctx, tx, tenant, r.AgentPrincipalID, id)
		if err != nil {
			return empty, err
		}
		if !ready {
			return *r, nil
		}
	}
	if p.HandoverExecutionID == nil && m.now().Before(r.StepStartedAt.Add(handoverTimeout)) {
		summary, err := m.retirementSummaryOn(ctx, tx, r)
		if err != nil {
			return empty, err
		}
		producer, err := m.retirementHandover.LaunchOn(ctx, tx, tenant, r.AgentPrincipalID, id, "Write the retirement handover document. Preserve the five source sections below and add ## 给接手人的注意事项. Cite only actual source facts; disclose unavailable data.\n\n"+summary)
		if err != nil {
			return empty, err
		}
		if producer <= 0 {
			return empty, ErrUnavailable
		}
		p.HandoverExecutionID = &producer
	} else {
		artifact, readErr := m.retirementHandover.ReadOn(ctx, tx, tenant, r.AgentPrincipalID, id)
		if readErr != nil && !errors.Is(readErr, ErrNotFound) {
			return empty, readErr
		}
		matches := readErr == nil && artifact.Source == "AGENT" && artifact.ExecutionID != nil && p.HandoverExecutionID != nil && *artifact.ExecutionID == *p.HandoverExecutionID
		if !matches {
			if m.now().Before(r.StepStartedAt.Add(handoverTimeout)) {
				return *r, nil
			}
			if p.HandoverExecutionID != nil {
				if err = m.retirementHandover.CancelOn(ctx, tx, tenant, r.AgentPrincipalID, id, *p.HandoverExecutionID); err != nil {
					return empty, err
				}
			}
			summary, err := m.retirementSummaryOn(ctx, tx, r)
			if err != nil {
				return empty, err
			}
			artifact, err = m.retirementHandover.SystemOn(ctx, tx, tenant, r.AgentPrincipalID, id, summary)
			if err != nil {
				return empty, err
			}
			if artifact.Source != "SYSTEM" || artifact.ExecutionID != nil {
				return empty, ErrUnavailable
			}
		}
		if artifact.ID <= 0 || artifact.Content == "" || artifact.SHA256 == "" {
			return empty, ErrUnavailable
		}
		p.HandoverArtifactID, p.HandoverSource = &artifact.ID, artifact.Source
		r.Step, r.StepStartedAt = "SUCCESSOR", m.now()
	}
	if err = m.saveRetirementProgressOn(ctx, tx, r, version, "HANDOVER_PROGRESS"); err != nil {
		return empty, err
	}
	return *r, nil
}
