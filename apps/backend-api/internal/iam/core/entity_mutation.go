package core

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

// EntityStateFence is an owner supplied transaction fence. IAM still evaluates
// current policy first; the owner may restrict an allowed operation, never grant it.
type EntityStateFence func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error
type EntityStateFenceConnector struct{ Bind func(EntityStateFence) error }

type entityMutationKey struct{}

// WithEntityMutation marks a domain write whose existing relationship policy may
// deliberately use a read permission. It does not change policy authorization.
func WithEntityMutation(ctx context.Context) context.Context {
	return context.WithValue(ctx, entityMutationKey{}, true)
}

func EntityMutationFromContext(ctx context.Context) bool {
	return ctx.Value(entityMutationKey{}) == true
}

func (m *Module) provideEntityConstraint(s *module.Services) error {
	var fence EntityStateFence
	if err := module.Provide[EntityStateFenceConnector](s, EntityStateFenceConnector{Bind: func(f EntityStateFence) error {
		if f == nil || fence != nil {
			return ErrInvalidArgument
		}
		fence = f
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[EntityConstraintOn](s, func(ctx context.Context, tx *gorm.DB, tenant, entity guid.ID, permission string, subject guid.ID) (authz.DataConstraint, error) {
		c, err := m.checker.EntityConstraintOn(ctx, tx, tenant, entity, permission, subject)
		if err != nil {
			return c, err
		}
		if fence != nil && (c.AllowsEntityRows(entity) || EntityMutationFromContext(ctx)) {
			if err = fence(ctx, tx, tenant, entity, permission); err != nil {
				return authz.DataConstraint{}, err
			}
		}
		return c, nil
	})
}
