package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"database/sql/driver"
	"encoding/json"
	"gorm.io/gorm"
	"time"
)

// DispatchExecutionFact attributes all persisted branches to the server-frozen
// executor, never to the initiator or to a command ACCEPTED acknowledgement.
// Settled cost is owned by Budget and is deliberately absent here.
type DispatchExecutionFact struct {
	ID, WorkItemID, AssignmentID, ExecutorPrincipalID guid.ID
	CreatedAt                                         time.Time
	State, BranchReason                               string
}
type DispatchHistory struct {
	ForWorkOn func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) ([]DispatchExecutionFact, error)
}

func (m *Module) provideDispatchHistory(s *module.Services) error {
	return module.Provide[DispatchHistory](s, DispatchHistory{ForWorkOn: m.dispatchHistoryOn})
}
func (m *Module) dispatchHistoryOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, work []guid.ID, at time.Time) ([]DispatchExecutionFact, error) {
	out := []DispatchExecutionFact{}
	if m == nil || transaction(tx, tenant, project) != nil || at.IsZero() || len(work) > 500 {
		return out, ErrInvalidArgument
	}
	if err := m.humanAuthority(ctx, tx, tenant, project, "execution_read"); err != nil {
		return out, err
	}
	if len(work) == 0 {
		return out, nil
	}
	ids := make([]driver.Valuer, len(work))
	for i, id := range work {
		if id <= 0 {
			return out, ErrInvalidArgument
		}
		ids[i] = id
	}
	q := query.BindExecutions(tx, m.names)
	at = at.UTC()
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.In(ids...), q.CreatedAt.Lte(at)).Order(q.ID).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(rows) > 10000 {
		return out, ErrUnavailable
	}
	for _, row := range rows {
		var plan ExecutionPlan
		if err := json.Unmarshal([]byte(row.PlanJSON), &plan); err != nil {
			return out, err
		}
		// Legacy executions without a frozen identity are explicitly unavailable.
		if plan.ExecutorPrincipalID <= 0 {
			return out, ErrCapabilityUnavailable
		}
		out = append(out, DispatchExecutionFact{ID: row.ID, WorkItemID: row.WorkItemID, AssignmentID: row.AssignmentID, ExecutorPrincipalID: plan.ExecutorPrincipalID, CreatedAt: row.CreatedAt, State: row.State, BranchReason: row.BranchReason})
	}
	return out, nil
}
