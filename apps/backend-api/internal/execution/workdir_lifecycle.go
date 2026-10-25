package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) bindWorkdirLifecycle(s *module.Services) error {
	connector, err := module.Resolve[fleet.WorkdirLifecycleConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return connector.Bind(m.workdirQuietOn)
}

// Caller holds the machine lock shared by creation and recovery. Interrupted
// executions retain their exact frozen path, even after their slot is released.
func (m *Module) workdirQuietOn(ctx context.Context, tx *gorm.DB, tenant, machine, workdir guid.ID) (bool, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || machine <= 0 || workdir <= 0 {
		return false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return false, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	cursor := guid.ID(0)
	for {
		rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.State.In("CREATED", "RUNNING", "PAUSED", "INTERRUPTED"), q.ID.Gt(cursor)).Order(q.ID).Limit(100).Find()
		if err != nil {
			return false, err
		}
		for _, row := range rows {
			v, e := view(row)
			if e != nil {
				return false, e
			}
			if v.Plan.WorkdirID == workdir {
				return false, nil
			}
			cursor = row.ID
		}
		if len(rows) < 100 {
			return true, nil
		}
	}
}
