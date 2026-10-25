package core

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

// EntityRoleBindingRevoker runs the existing audited IAM delete command on the
// business owner's transaction, including its policy revision/cache fence.
// The caller first authorizes its domain revocation and identifies its carrier.
type EntityRoleBindingRevoker func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error)

func (m *Module) provideBindingRevoker(s *module.Services) error {
	return module.Provide[EntityRoleBindingRevoker](s, m.revokeEntityBindingOn)
}
func (m *Module) revokeEntityBindingOn(ctx context.Context, tx *gorm.DB, tenant, entity, role, principal guid.ID) (bool, error) {
	service, e := m.entityServiceOn(tx)
	if e != nil {
		return false, e
	}
	return service.DeleteEntityRoleBinding(ctx, tenant, entity, role, principal)
}
