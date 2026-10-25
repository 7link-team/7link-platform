package delivery

import (
	"context"
	"slices"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// ScheduledPlanningFacts is exclusively for registered planning SYSTEM jobs.
// It does not confer user access and rejects an authenticated user context.
type ScheduledPlanningFacts struct {
	ProjectsOn func(context.Context, *gorm.DB, guid.ID, time.Time, time.Time) ([]guid.ID, error)
	ReadOn     func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (PlanningSnapshot, error)
}

func (m *Module) provideHPlanning(s *module.Services) error {
	return module.Provide[ScheduledPlanningFacts](s, ScheduledPlanningFacts{ProjectsOn: m.scheduledPlanningProjectsOn, ReadOn: m.scheduledPlanningFactsOn})
}

func scheduledPlanningScope(ctx context.Context, tx *gorm.DB, tenant guid.ID, start, end time.Time) error {
	if tx == nil || tx.Statement == nil {
		return ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if tenant <= 0 || !start.Before(end) {
		return ErrInvalidArgument
	}
	if authn.PrincipalIDFromContext(ctx) != 0 {
		return ErrWorkForbidden
	}
	return nil
}

func (m *Module) scheduledPlanningProjectsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, start, end time.Time) ([]guid.ID, error) {
	if err := scheduledPlanningScope(ctx, tx, tenant, start, end); err != nil {
		return nil, err
	}
	q := query.BindProjects(tx, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.CreatedAt.Lt(end)).Order(q.ProjectID).Limit(501).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 500 {
		return nil, ErrUnavailable
	}
	active := []string{"DISCOVERY", "PLANNING", "ACTIVE", "BLOCKED", "ACCEPTANCE_PENDING"}
	out := []guid.ID{}
	for _, row := range rows {
		if row.ArchivedAt != nil && !row.ArchivedAt.After(start) {
			continue
		}
		eligible := slices.Contains(active, row.Stage) && row.StageChangedAt.Before(end) && (row.ArchivedAt == nil || row.ArchivedAt.After(start))
		if !eligible {
			t := query.BindStageTransitions(tx, m.service.names)
			transitions, err := t.WithContext(ctx).Where(t.TenantID.Eq(tenant), t.ProjectID.Eq(row.ProjectID), t.OccurredAt.Gte(start), t.OccurredAt.Lt(end)).Find()
			if err != nil {
				return nil, err
			}
			for _, transition := range transitions {
				eligible = eligible || slices.Contains(active, transition.ToStage) || (transition.OccurredAt.After(start) && slices.Contains(active, transition.FromStage))
			}
			if !eligible {
				// A later closure must not erase a phase spanning this whole
				// period. The first following receipt proves the prior stage;
				// transitions inside the period were considered above.
				following, err := t.WithContext(ctx).Where(t.TenantID.Eq(tenant), t.ProjectID.Eq(row.ProjectID), t.OccurredAt.Gte(end)).Order(t.OccurredAt, t.ID).Limit(1).Find()
				if err != nil {
					return nil, err
				}
				if len(following) > 0 {
					eligible = slices.Contains(active, following[0].FromStage)
				}
			}
		}
		if eligible {
			out = append(out, row.ProjectID)
		}
	}
	return out, nil
}

func (m *Module) scheduledPlanningFactsOn(ctx context.Context, tx *gorm.DB, tenant, project, subject guid.ID, start, end time.Time) (PlanningSnapshot, error) {
	out := PlanningSnapshot{Work: []PlanningWorkFact{}, MissingSources: []string{"EXECUTIONS", "EXECUTION_COST", "AUTOMATIC_VERIFICATION", "CLIENT_ACCEPTANCE", "QUALITY_METRICS", "RESULT_COUNTS", "HANDOFF_COUNTS"}}
	if err := scheduledPlanningScope(ctx, tx, tenant, start, end); err != nil {
		return out, err
	}
	if project <= 0 || subject < 0 {
		return out, ErrInvalidArgument
	}
	q := query.BindWorkItems(tx, m.service.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.CreatedAt.Lt(end), q.ArchivedAt.IsNull())
	if subject > 0 {
		base = base.Where(q.AccountablePrincipalID.Eq(subject))
	}
	rows, err := base.Order(q.ID).Limit(501).Find()
	if err != nil {
		return out, err
	}
	if len(rows) > 500 {
		return out, ErrUnavailable
	}
	for _, row := range rows {
		out.Work = append(out.Work, PlanningWorkFact{ID: row.ID, Title: row.Title, State: row.State, AccountablePrincipalID: row.AccountablePrincipalID, AcceptedAt: row.AcceptedAt, ResultID: row.CurrentClaimID})
	}
	return out, nil
}
