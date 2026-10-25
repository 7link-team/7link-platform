package execution

import (
	"context"
	"encoding/json"
	"errors"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) bindAgentPerformance(s *module.Services) error {
	c, err := module.Resolve[agents.AgentPerformanceConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.BindRunning(m.agentPerformanceRunningOn)
}

func (m *Module) agentPerformanceRunningOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (int64, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || agent <= 0 {
		return 0, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	if authn.TenantIDFromContext(ctx) != tenant {
		return 0, ErrForbidden
	}
	q := query.BindExecutions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.In("CREATED", "RUNNING", "PAUSED", "INTERRUPTED")).Order(q.ID).Limit(10001).Find()
	if err != nil {
		return 0, err
	}
	if len(rows) > 10000 {
		return 0, ErrUnavailable
	}
	allowed, checked := map[guid.ID]bool{}, map[guid.ID]bool{}
	var count int64
	for _, row := range rows {
		if !checked[row.ProjectID] {
			checked[row.ProjectID] = true
			err = m.humanAuthority(ctx, tx, tenant, row.ProjectID, "execution_read")
			if errors.Is(err, ErrForbidden) {
				continue
			}
			if err != nil {
				return 0, err
			}
			allowed[row.ProjectID] = true
		}
		if !allowed[row.ProjectID] {
			continue
		}
		var plan ExecutionPlan
		if json.Unmarshal([]byte(row.PlanJSON), &plan) != nil || plan.ExecutorPrincipalID <= 0 {
			return 0, ErrCapabilityUnavailable
		}
		if plan.ExecutorPrincipalID == agent {
			count++
		}
	}
	return count, nil
}
