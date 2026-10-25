package budget

import (
	"context"
	"database/sql/driver"
	"math"

	"7link.com/platform/internal/budget/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SettlementFacts reads actual execution settlements. Missing, reserved, or
// executions without an actual settlement are incomplete; they never contribute guessed zero.
type SettlementFacts struct {
	ExecutionsOn func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error)
}

func (m *Module) executionSettlementsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, ids []guid.ID) (int64, bool, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || len(ids) > 1000 {
		return 0, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, false, ErrInvalidArgument
	}
	if len(ids) == 0 {
		return 0, true, nil
	}
	vals := make([]driver.Valuer, 0, len(ids))
	seen := map[guid.ID]bool{}
	for _, id := range ids {
		if id <= 0 {
			return 0, false, ErrInvalidArgument
		}
		if !seen[id] {
			seen[id] = true
			vals = append(vals, id)
		}
	}
	q := query.BindReservations(tx, m.names)
	rows, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Source.Eq("EXECUTION"), q.SourceID.In(vals...)).Find()
	if e != nil {
		return 0, false, e
	}
	if len(rows) != len(vals) {
		return 0, false, nil
	}
	total := int64(0)
	for _, row := range rows {
		if row.State != "SETTLED" || row.SettledAt == nil || row.ActualMicros == nil || *row.ActualMicros < 0 {
			return 0, false, nil
		}
		if total > math.MaxInt64-*row.ActualMicros {
			return 0, false, ErrInvalidArgument
		}
		total += *row.ActualMicros
	}
	return total, true, nil
}
func (m *Module) provideWorkflowSettlements(s *module.Services) error {
	return module.Provide[SettlementFacts](s, SettlementFacts{ExecutionsOn: m.executionSettlementsOn})
}
