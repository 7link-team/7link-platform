package agents

import (
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
)

// reminderMutationContext lets the canonical anchor authorization apply
// the project state fence without replacing SELF or tenant management policy.
func reminderMutationContext(ctx context.Context) context.Context {
	return core.WithEntityMutation(ctx)
}

func (m *Module) injectArchiveResources(s *module.Services) error {
	c, err := module.Resolve[delivery.ProjectArchiveConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.Bind("agents", m.projectArchiveResourcesOn)
}

func (m *Module) projectArchiveResourcesOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]delivery.ProjectArchiveResource, error) {
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return nil, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalid
	}
	q := query.BindReminder(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	out := []delivery.ProjectArchiveResource{}
	for _, r := range rows {
		out = append(out, delivery.ProjectArchiveResource{Kind: "AGENT_REMINDER", ID: r.ID, State: r.State, Version: r.Version, Ready: slices.Contains([]string{"PAUSED", "DONE", reminderCancelled}, r.State)})
	}
	return out, nil
}
