package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"strconv"
	"time"
)

func (m *Module) bindMetrics(s *module.Services) error {
	c, err := module.Resolve[delivery.DeliveryMetricsSourcesConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.BindExecutions == nil {
		return ErrUnavailable
	}
	return c.BindExecutions(m.metricExecutionsOn)
}

// Enumerate every branch, including failed, canceled and unaccepted executions.
// Settlement amounts are read by the budget owner; no estimates are returned.
func (m *Module) metricExecutionsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, work []guid.ID, start, end time.Time) ([]delivery.MetricExecution, error) {
	out := []delivery.MetricExecution{}
	if transaction(tx, tenant, project) != nil || !start.Before(end) || len(work) > 10000 {
		return nil, ErrInvalidArgument
	}
	if err := m.humanAuthority(ctx, tx, tenant, project, "execution_read"); err != nil {
		return nil, err
	}
	if len(work) == 0 {
		return out, nil
	}
	ids := make([]driver.Valuer, len(work))
	for i, id := range work {
		if id <= 0 {
			return nil, ErrInvalidArgument
		}
		ids[i] = id
	}
	q := query.BindExecutions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.In(ids...), q.CreatedAt.Gte(start.UTC()), q.CreatedAt.Lt(end.UTC())).Order(q.CreatedAt, q.ID).Limit(10001).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 10000 {
		return nil, ErrUnavailable
	}
	for _, row := range rows {
		var plan ExecutionPlan
		if err := json.Unmarshal([]byte(row.PlanJSON), &plan); err != nil {
			return nil, err
		}
		workflow := ""
		if plan.ProgressTemplateVersionID > 0 {
			workflow = strconv.FormatInt(int64(plan.ProgressTemplateVersionID), 10) + ":" + plan.ProgressDefinitionHash
		}
		out = append(out, delivery.MetricExecution{ID: row.ID, WorkItemID: row.WorkItemID, ParentID: row.ParentExecutionID, CreatedAt: row.CreatedAt, Model: plan.Model, WorkflowVersion: workflow})
	}
	return out, nil
}
