package execution

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func bindFleetAgentRoles(s *module.Services) error {
	c, err := module.Resolve[fleet.AgentRegistrationRolesConnector](s)
	// Focused adapter tests do not provide an entire fleet; an unbound fleet
	// capability still refuses registration rather than granting any role.
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	roles, err := module.Resolve[governance.SeededRoles](s)
	if err != nil {
		return err
	}
	grants, err := module.Resolve[core.RoleGrantStore](s)
	if err != nil {
		return err
	}
	if c.Bind == nil || roles.RoleIDOn == nil || grants.RoleGranted == nil {
		return fleet.ErrUnavailable
	}
	return c.Bind(func(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, keys []string) error {
		for _, key := range keys {
			role, err := roles.RoleIDOn(ctx, tx, tenant, key)
			if err != nil {
				return err
			}
			if role <= 0 {
				return fleet.ErrUnavailable
			}
			held, err := grants.RoleGranted(ctx, tx, tenant, role, principal, time.Now().UTC())
			if err != nil {
				return err
			}
			if !held {
				return fleet.ErrMachineForbidden
			}
		}
		return nil
	})
}
