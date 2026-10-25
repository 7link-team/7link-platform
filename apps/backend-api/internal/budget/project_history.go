package budget

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"7link.com/platform/internal/budget/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// Bind to the earlier Delivery module rather than reversing module dependencies.
func (m *Module) bindProjectHistory(s *module.Services) error {
	c, e := module.Resolve[delivery.ProjectBudgetHistoryConnector](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(m.projectHistoryOn)
}

func (m *Module) projectHistoryOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, spent int64) (delivery.ProjectBudgetTrend, error) {
	out := delivery.ProjectBudgetTrend{Available: true, Points: []delivery.ProjectBudgetPoint{}}
	if err := validTransaction(tx, tenant, project); err != nil {
		return out, err
	}
	if m == nil || m.names.Reservations.IsZero() || spent < 0 {
		return out, ErrInvalidArgument
	}
	q := query.BindReservations(tx, m.names)
	rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.Eq("SETTLED")).Order(q.SettledAt.Desc(), q.ID.Desc()).Limit(101).Find()
	if e != nil {
		return out, e
	}
	out.Truncated = len(rows) > 100
	if out.Truncated {
		rows = rows[:100]
	}
	for _, r := range rows {
		if r.SettledAt == nil || r.ActualMicros == nil || *r.ActualMicros < 0 || *r.ActualMicros > spent {
			return out, ErrUnavailable
		}
		out.Points = append(out.Points, delivery.ProjectBudgetPoint{ID: r.ID, At: *r.SettledAt, SpentMicros: strconv.FormatInt(spent, 10)})
		spent -= *r.ActualMicros
	}
	slices.Reverse(out.Points)
	return out, nil
}
