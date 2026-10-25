package knowledge

import (
	"context"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// LearningRules writes human-authored escape learning through the canonical
// command. Rules remain PENDING_REVIEW until the existing memory review command
// approves them; recording learning never asserts runtime generation or approval.
type LearningRuleDraft struct {
	Title string `json:"title" minLength:"1" maxLength:"200"`
	Body  string `json:"body" minLength:"1" maxLength:"8000"`
}

type LearningRules struct {
	AuthorizeOn func(context.Context, *gorm.DB, guid.ID, guid.ID) error
	ProposeOn   func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, LearningRuleDraft) (EntryView, error)
}

func (m *Module) provideLearning(s *module.Services) error {
	return module.Provide[LearningRules](s, LearningRules{AuthorizeOn: m.learningAuthorityOn, ProposeOn: m.proposeLearningOn})
}

func (m *Module) learningAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	if project <= 0 {
		return ErrInvalid
	}
	_, err := m.authorize(ctx, tx, tenant, Scope{Scope: "PROJECT", ProjectID: &project}, "knowledge_write", true)
	return err
}

func (m *Module) proposeLearningOn(ctx context.Context, tx *gorm.DB, tenant, project, escape guid.ID, draft LearningRuleDraft) (EntryView, error) {
	if escape <= 0 {
		return EntryView{}, ErrInvalid
	}
	if err := m.learningAuthorityOn(ctx, tx, tenant, project); err != nil {
		return EntryView{}, err
	}
	return m.writeOn(ctx, tx, tenant, EntryInput{Scope: Scope{Scope: "PROJECT", ProjectID: &project}, Kind: "RULE", Title: draft.Title, Body: draft.Body, Provenance: "Human-authored learning from quality escape " + escape.String()})
}
