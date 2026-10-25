package knowledge

import (
	"context"
	"errors"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// RetirementRules returns references to the retiring Agent's active rule
// memories. Every scope is authorized for the current Human; private bodies are
// not copied into a handover document or broadened to the successor's access.
type RetirementRules struct {
	CheckOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
	ReadOn  func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]RetirementRule, error)
}

type RetirementRule struct {
	ID        guid.ID  `json:"id"`
	ProjectID *guid.ID `json:"project_id,omitempty"`
	Title     string   `json:"title"`
	SHA256    string   `json:"sha256"`
	Version   int      `json:"version"`
}

func (m *Module) provideRetirementRules(s *module.Services) error {
	return module.Provide(s, RetirementRules{ReadOn: m.retirementRulesOn, CheckOn: m.checkRetirementRuleOn})
}

func (m *Module) checkRetirementRuleOn(ctx context.Context, tx *gorm.DB, tenant, agent, id guid.ID) error {
	q := query.BindEntries(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Scope.Eq("AGENT"), q.AgentPrincipalID.Eq(agent), q.Kind.Eq("RULE")).First()
	if err != nil {
		return err
	}
	_, err = m.authorize(ctx, tx, tenant, Scope{Scope: "AGENT", ProjectID: r.ProjectID, AgentPrincipalID: &agent}, "knowledge_read", true)
	return err
}

func (m *Module) retirementRulesOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) ([]RetirementRule, error) {
	if _, err := m.authorize(ctx, tx, tenant, Scope{Scope: "AGENT", AgentPrincipalID: &agent}, "knowledge_read", true); err != nil {
		return nil, err
	}
	q := query.BindEntries(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Scope.Eq("AGENT"), q.AgentPrincipalID.Eq(agent), q.Kind.Eq("RULE"), q.State.Eq("ACTIVE")).Order(q.ID).Limit(1001).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 1000 {
		return nil, ErrUnavailable
	}
	out := make([]RetirementRule, 0, len(rows))
	for _, r := range rows {
		if r.ExpiresAt != nil && !r.ExpiresAt.After(m.now()) {
			continue
		}
		if _, err = m.authorize(ctx, tx, tenant, Scope{Scope: "AGENT", ProjectID: r.ProjectID, AgentPrincipalID: &agent}, "knowledge_read", true); err != nil {
			if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, RetirementRule{ID: r.ID, ProjectID: r.ProjectID, Title: r.Title, SHA256: r.ContentHash, Version: r.Version})
	}
	return out, nil
}
