package delivery

import (
	"context"
	"database/sql/driver"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LoopAssignments returns historical Agent principals, including ended assignments.
// Current work ownership must not rewrite the Agent attributed to an execution.
type LoopAssignments struct {
	AgentsOn func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (map[guid.ID]guid.ID, error)
}

func (m *Module) loopAssignmentAgentsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, ids []guid.ID) (map[guid.ID]guid.ID, error) {
	out := map[guid.ID]guid.ID{}
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || len(ids) > 1000 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	if len(ids) == 0 {
		return out, nil
	}
	vals := make([]driver.Valuer, len(ids))
	for i, id := range ids {
		if id <= 0 {
			return nil, ErrInvalidArgument
		}
		vals[i] = id
	}
	q := query.BindAssignments(tx, m.service.names)
	rows, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.In(vals...)).Find()
	if e != nil {
		return nil, e
	}
	for _, row := range rows {
		if row.AssigneeKind == "AGENT" && row.AssigneePrincipalID > 0 {
			out[row.ID] = row.AssigneePrincipalID
		} else {
			out[row.ID] = 0
		}
	}
	return out, nil
}
func (m *Module) provideHLoopAssignments(s *module.Services) error {
	return module.Provide[LoopAssignments](s, LoopAssignments{AgentsOn: m.loopAssignmentAgentsOn})
}
