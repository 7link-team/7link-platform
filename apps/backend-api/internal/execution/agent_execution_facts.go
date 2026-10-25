package execution

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// loopAgentExecutionFactsOn attributes accepted starts to the immutable executor
// in the hash-validated plan, across actual projects in this tenant. It never
// treats an initiator or current assignment holder as the execution author.
func (m *Module) loopAgentExecutionFactsOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, start, end time.Time) ([]LoopExecutionFact, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || agent <= 0 || !start.Before(end) {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.CreatedAt.Lt(end)).Order(q.ProjectID, q.ID).Find()
	if err != nil {
		return nil, err
	}
	out := []LoopExecutionFact{}
	for _, row := range rows {
		r := query.BindReceipts(tx, m.names)
		receipt, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ExecutionID.Eq(row.ID), r.Kind.Eq("ACCEPTED")).Order(r.CreatedAt, r.ID).First()
		if errors.Is(err, gorm.ErrRecordNotFound) && row.State == "CREATED" {
			continue
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil && (receipt.CreatedAt.Before(start) || !receipt.CreatedAt.Before(end)) {
			continue
		}
		v, viewErr := view(row)
		if viewErr != nil {
			return nil, viewErr
		}
		// Historical executions without frozen authorship cannot be safely included
		// or excluded. Keep their cost explicitly unavailable.
		if v.Plan.ExecutorPrincipalID <= 0 {
			return nil, ErrUnavailable
		}
		if v.Plan.ExecutorPrincipalID != agent {
			continue
		}
		if row.ProjectID <= 0 || row.AssignmentID <= 0 {
			return nil, ErrUnavailable
		}
		fact := LoopExecutionFact{ID: row.ID, ProjectID: row.ProjectID, AssignmentID: row.AssignmentID, WorkItemID: row.WorkItemID}
		if err == nil {
			at := receipt.CreatedAt
			fact.StartedAt = &at
		}
		out = append(out, fact)
	}
	return out, nil
}
