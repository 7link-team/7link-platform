package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
)

func (m *Module) bindAgentRebind(s *module.Services) error {
	connector, err := module.Resolve[fleet.AgentRebindConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return connector.Bind(m.agentRebindQuietOn)
}

// The fleet command holds the Agent and old/new machine locks. Execution
// creation uses those same machine locks before freezing the source binding.
func (m *Module) agentRebindQuietOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (bool, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || agent <= 0 {
		return false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return false, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	cursor := guid.ID(0)
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.In("CREATED", "RUNNING", "PAUSED"), q.ID.Gt(cursor)).Order(q.ID).Limit(100).Find()
		if err != nil {
			return false, err
		}
		for _, row := range rows {
			v, e := view(row)
			if e != nil {
				return false, e
			}
			if v.Plan.ExecutorPrincipalID == agent {
				return false, nil
			}
			cursor = row.ID
		}
		if len(rows) < 100 {
			return true, nil
		}
	}
}
