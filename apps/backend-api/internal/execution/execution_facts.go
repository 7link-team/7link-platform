package execution

import (
	"context"
	"database/sql/driver"
	"errors"
	"time"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// LoopExecutionFact identifies actual accepted starts for a trusted scoped
// work-item set. Initiator is deliberately absent: it is not the assigned Agent.
// StartedAt is the server-observed ACCEPTED receipt timestamp (RUNNING transition),
// not execution creation or a claimed runtime timestamp. Nil means unproven.
type LoopExecutionFact struct {
	ID, ProjectID, AssignmentID, WorkItemID guid.ID
	StartedAt                               *time.Time
}
type LoopExecutionFacts struct {
	AgentOn func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) ([]LoopExecutionFact, error)
	ReadOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]LoopExecutionFact, error)
}

// VerificationSourceFacts supplies immutable scope for committed execution
// events. It takes no execution lock before delivery acquires its domain locks.
type VerificationSourceFact struct {
	WorkItemID guid.ID
	State      string
}
type VerificationSourceFacts struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (VerificationSourceFact, error)
}

func (m *Module) verificationSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (VerificationSourceFact, error) {
	if transaction(tx, tenant, project) != nil || id <= 0 {
		return VerificationSourceFact{}, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return VerificationSourceFact{}, ErrNotFound
	}
	if err != nil {
		return VerificationSourceFact{}, err
	}
	return VerificationSourceFact{WorkItemID: r.WorkItemID, State: r.State}, nil
}

func (m *Module) provideLoopExecutionFacts(s *module.Services) error {
	if err := module.Provide[VerificationSourceFacts](s, VerificationSourceFacts{ReadOn: m.verificationSourceOn}); err != nil {
		return err
	}
	return module.Provide[LoopExecutionFacts](s, LoopExecutionFacts{ReadOn: m.loopExecutionFactsOn, AgentOn: m.loopAgentExecutionFactsOn})
}
func (m *Module) loopExecutionFactsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, work []guid.ID, start, end time.Time) ([]LoopExecutionFact, error) {
	if transaction(tx, tenant, project) != nil || !start.Before(end) || len(work) > 500 {
		return nil, ErrInvalidArgument
	}
	out := []LoopExecutionFact{}
	if work != nil && len(work) == 0 {
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
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.CreatedAt.Lt(end))
	if work != nil {
		base = base.Where(q.WorkItemID.In(ids...))
	}
	rows, err := base.Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		r := query.BindReceipts(tx, m.names)
		receipt, e := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ExecutionID.Eq(row.ID), r.Kind.Eq("ACCEPTED")).Order(r.CreatedAt, r.ID).First()
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return nil, e
		}
		if errors.Is(e, gorm.ErrRecordNotFound) {
			// A queued execution has not begun. Other outcomes with no accepted
			// receipt remain explicitly unknown; never substitute CreatedAt.
			if row.State != "CREATED" {
				out = append(out, LoopExecutionFact{ID: row.ID, AssignmentID: row.AssignmentID, WorkItemID: row.WorkItemID})
			}
			continue
		}
		if !receipt.CreatedAt.Before(start) && receipt.CreatedAt.Before(end) {
			at := receipt.CreatedAt
			out = append(out, LoopExecutionFact{ID: row.ID, AssignmentID: row.AssignmentID, WorkItemID: row.WorkItemID, StartedAt: &at})
		}
	}
	return out, nil
}
