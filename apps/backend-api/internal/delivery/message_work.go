package delivery

import (
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
)

// injectWork reuses the authorized command; collab owns its idempotency
// savepoint and source association, so creation and linkage commit together.
func (m *Module) injectWork(s *module.Services) error {
	c, err := module.Resolve[collab.MessageWorkConnector](s)
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(m.createMessageWorkOn, m.replayMessageWorkOn)
}
func messageWorkError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return collab.ErrInvalidArgument
	case errors.Is(err, ErrWorkForbidden), errors.Is(err, ErrInvalidMember):
		return collab.ErrForbidden
	case errors.Is(err, ErrProjectNotFound):
		return collab.ErrNotFound
	case errors.Is(err, ErrWorkProjectReadOnly):
		return collab.ErrWorkReadOnly
	default:
		return err
	}
}
func (m *Module) createMessageWorkOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in collab.ConvertWorkInput, body string) (guid.ID, error) {
	criteria := make([]workCriterionInput, len(in.Criteria))
	for i, c := range in.Criteria {
		criteria[i] = workCriterionInput{Text: c.Text, VerificationMethod: c.VerificationMethod}
	}
	v, err := m.createWorkOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), workCreateBody{Title: in.Title, Description: body, Type: "TASK", Objective: in.Objective, AccountablePrincipalID: in.AccountablePrincipalID, Criteria: criteria})
	return v.ID, messageWorkError(err)
}
func (m *Module) replayMessageWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) error {
	if m.workPolicy == nil {
		return collab.ErrUnavailable
	}
	_, err := m.readWork(ctx, tx, tenant, project, id, authn.PrincipalIDFromContext(ctx))
	return messageWorkError(err)
}
