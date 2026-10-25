package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"gorm.io/gorm"
)

// TestCaseScopeOn resolves only the requirement attached to an authorized work
// item. Importers cannot attach a case to an unrelated or unreadable requirement.
func (a clientWork) TestCaseScopeOn(ctx context.Context, tx *gorm.DB, tenant, project, work, requirement guid.ID) (ClientRequirement, error) {
	row, err := a.m.readWork(ctx, tx, tenant, project, work, authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return ClientRequirement{}, err
	}
	if row.RequirementID == nil || *row.RequirementID != requirement {
		return ClientRequirement{}, ErrInvalidArgument
	}
	if err = a.LockRequirementReadOn(ctx, tx, tenant, project, requirement); err != nil {
		return ClientRequirement{}, err
	}
	items, err := a.RequirementsOn(ctx, tx, tenant, project, []guid.ID{requirement})
	if err != nil {
		return ClientRequirement{}, err
	}
	if len(items) != 1 {
		return ClientRequirement{}, ErrUnavailable
	}
	return items[0], nil
}
