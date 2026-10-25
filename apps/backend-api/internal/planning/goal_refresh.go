package planning

import (
	"context"
	"encoding/json"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// refreshGoals is a tenant-scoped system calculation. It uses the existing
// scheduled source reader, never a synthetic Human or expanded IAM grant.
func (m *Module) refreshGoals(ctx context.Context, tenant guid.ID) error {
	if tenant <= 0 {
		return ErrInvalid
	}
	if authn.PrincipalIDFromContext(ctx) != 0 {
		return ErrForbidden
	}
	db, err := m.database()
	if err != nil {
		return err
	}
	q := query.BindGoals(db, m.names)
	var after guid.ID
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(after), q.State.In("ACTIVE", "AT_RISK")).Order(q.ID).Limit(100).Find()
		if err != nil {
			return err
		}
		for _, row := range rows {
			after = row.ID
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.refreshGoalOn(ctx, tx, tenant, row.ID) }); err != nil {
				return err
			}
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

func goalRiskState(g PlanningGoalInput, previous string, settings PlanningSettings, start, end, now time.Time) (string, error) {
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return "", err
	}
	if elapsed := elapsedWorkdays(start, end, now, loc, settings.Workdays); elapsed != nil {
		if *elapsed-goalProgress(g) > settings.AtRiskThreshold {
			return "AT_RISK", nil
		}
		return "ACTIVE", nil
	}
	return previous, nil
}

func (m *Module) refreshGoalOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
	if authn.PrincipalIDFromContext(ctx) != 0 {
		return ErrForbidden
	}
	q := query.BindGoals(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil {
		return err
	}
	if row.State != "ACTIVE" && row.State != "AT_RISK" {
		return nil
	}
	var g PlanningGoalInput
	if err := json.Unmarshal([]byte(row.Content), &g); err != nil {
		return err
	}
	start, end, err := m.periodForOn(ctx, tx, tenant, row.PeriodKind, row.PeriodStart)
	if err != nil {
		return err
	}
	needsFacts := false
	for _, k := range g.KeyResults {
		if k.Source == "AUTO" {
			if k.Metric != "ACCEPTED_WORK_ITEMS" {
				return ErrUnavailable
			}
			needsFacts = true
		}
	}
	if needsFacts {
		if m.scheduledFacts.ReadOn == nil {
			return ErrUnavailable
		}
		projects := []guid.ID{row.ProjectID}
		if row.ProjectID == 0 {
			if m.scheduledFacts.ProjectsOn == nil {
				return ErrUnavailable
			}
			projects, err = m.scheduledFacts.ProjectsOn(ctx, tx, tenant, start, end)
			if err != nil {
				return err
			}
		}
		var subject guid.ID
		if g.Level == "MEMBER" {
			subject = row.PrincipalID
		}
		accepted := map[guid.ID]bool{}
		for _, project := range projects {
			facts, err := m.scheduledFacts.ReadOn(ctx, tx, tenant, project, subject, start, end)
			if err != nil {
				return err
			}
			for _, work := range facts.Work {
				if work.State == "ACCEPTED" && work.AcceptedAt != nil && !work.AcceptedAt.Before(start) && work.AcceptedAt.Before(end) && (subject == 0 || work.AccountablePrincipalID == subject) {
					accepted[work.ID] = true
				}
			}
		}
		for i := range g.KeyResults {
			if g.KeyResults[i].Source == "AUTO" {
				g.KeyResults[i].Current = int64(len(accepted))
			}
		}
	}
	settings, err := m.readSettingsOn(ctx, tx, tenant)
	if err != nil {
		return err
	}
	state, err := goalRiskState(g, row.State, settings, start, end, m.now())
	if err != nil {
		return err
	}
	body, hash, err := hashJSON(g)
	if err != nil {
		return err
	}
	if state == row.State && hash == row.ContentHash {
		return nil
	}
	previous := row.State
	info, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(row.Version)).Updates(map[string]any{"content": body, "content_hash": hash, "state": state, "version": row.Version + 1, "updated_at": m.now()})
	if err != nil {
		return err
	}
	if info.RowsAffected != 1 {
		return ErrConflict
	}
	row.Content, row.ContentHash, row.State, row.Version = body, hash, state, row.Version+1
	r, err := goalView(row)
	if err != nil {
		return err
	}
	return m.record(ctx, tx, tenant, r, "SYSTEM", "REFRESH", "", map[string]any{"from_state": previous})
}
