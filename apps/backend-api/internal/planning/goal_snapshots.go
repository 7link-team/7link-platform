package planning

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"time"
)

// GoalKRChange contains only persisted versions observed at each boundary.
// A missing boundary is unavailable, never an inferred zero.
type GoalKRChange struct {
	GoalID         guid.ID  `json:"goal_id"`
	Key            string   `json:"key"`
	Title          string   `json:"title"`
	Start          *int64   `json:"start,omitempty"`
	End            *int64   `json:"end,omitempty"`
	Delta          *int64   `json:"delta,omitempty"`
	StartVersion   *int     `json:"start_version,omitempty"`
	EndVersion     *int     `json:"end_version,omitempty"`
	MissingSources []string `json:"missing_sources"`
}

func (m *Module) appendGoalSnapshotOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, r PlanningRecord) error {
	if r.Kind != "goal" {
		return nil
	}
	if r.ID <= 0 || r.Version <= 0 || len(r.ContentHash) != 64 {
		return ErrInvalid
	}
	return query.BindGoalSnapshots(tx, m.names).WithContext(ctx).Create(&model.GoalSnapshot{TenantID: tenant, GoalID: r.ID, Version: r.Version, Content: string(r.Content), ContentHash: r.ContentHash, State: r.State, ObservedAt: m.now().UTC()})
}

func (m *Module) reportKRChangesOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, report *model.Report) ([]GoalKRChange, error) {
	out := []GoalKRChange{}
	start, end, err := reportPeriod(report)
	if err != nil {
		return nil, err
	}
	cutoff := minTime(m.now(), end)
	q := query.BindGoals(tx, m.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.CreatedAt.Lt(end.UTC()))
	if report.ProjectID > 0 || report.PrincipalID == 0 {
		base = base.Where(q.ProjectID.Eq(report.ProjectID))
	}
	if report.PrincipalID > 0 {
		base = base.Where(q.PrincipalID.Eq(report.PrincipalID))
	}
	rows, err := base.Order(q.ID).Limit(501).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 500 {
		return nil, ErrUnavailable
	}
	for _, candidate := range rows {
		row, goal, err := m.goalOn(ctx, tx, tenant, candidate.ProjectID, candidate.ID)
		if errors.Is(err, ErrForbidden) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if goal.Level != bodyScopeLevel(report) {
			continue
		}
		var body PlanningReportInput
		if err = json.Unmarshal([]byte(report.Content), &body); err != nil {
			return nil, err
		}
		a, b, err := settingsPeriod(row.PeriodKind, row.PeriodStart, body.CalendarTimezone)
		if err != nil {
			return nil, err
		}
		if a.After(start) || b.Before(end) {
			continue
		}
		first, err := m.goalSnapshotAtOn(ctx, tx, tenant, row.ID, start, false)
		if err != nil {
			return nil, err
		}
		last, err := m.goalSnapshotAtOn(ctx, tx, tenant, row.ID, cutoff, !m.now().Before(end))
		if err != nil {
			return nil, err
		}
		keys := goal.KeyResults
		if last != nil {
			var snapshot PlanningGoalInput
			if err = json.Unmarshal([]byte(last.Content), &snapshot); err != nil {
				return nil, err
			}
			keys = snapshot.KeyResults
		}
		for _, kr := range keys {
			change := GoalKRChange{GoalID: row.ID, Key: kr.Key, Title: kr.Title, MissingSources: []string{}}
			for _, boundary := range []struct {
				snapshot *model.GoalSnapshot
				value    **int64
				version  **int
				missing  string
			}{{first, &change.Start, &change.StartVersion, "KR_PERIOD_START"}, {last, &change.End, &change.EndVersion, "KR_PERIOD_END"}} {
				if boundary.snapshot == nil {
					change.MissingSources = append(change.MissingSources, boundary.missing)
					continue
				}
				var snapshot PlanningGoalInput
				if err = json.Unmarshal([]byte(boundary.snapshot.Content), &snapshot); err != nil {
					return nil, err
				}
				found := false
				for _, k := range snapshot.KeyResults {
					if k.Key == kr.Key {
						value := k.Current
						version := boundary.snapshot.Version
						*boundary.value = &value
						*boundary.version = &version
						found = true
						break
					}
				}
				if !found {
					change.MissingSources = append(change.MissingSources, boundary.missing)
				}
			}
			if change.Start != nil && change.End != nil {
				delta := *change.End - *change.Start
				change.Delta = &delta
			}
			out = append(out, change)
		}
	}
	return out, nil
}

func (m *Module) goalSnapshotAtOn(ctx context.Context, tx *gorm.DB, tenant, goal guid.ID, at time.Time, exclusive bool) (*model.GoalSnapshot, error) {
	q := query.BindGoalSnapshots(tx, m.names)
	d := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.GoalID.Eq(goal))
	if exclusive {
		d = d.Where(q.ObservedAt.Lt(at.UTC()))
	} else {
		d = d.Where(q.ObservedAt.Lte(at.UTC()))
	}
	rows, err := d.Order(q.ObservedAt.Desc(), q.Version.Desc()).Limit(1).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}
