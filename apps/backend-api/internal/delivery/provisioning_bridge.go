package delivery

import (
	"errors"

	"7link.com/platform/internal/collab"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/module"
)

// WireProjectProvisioning keeps optional provisioning modules out of delivery's
// required lifecycle dependencies. Missing capabilities remain unavailable;
// Initialize reports each one and creation fails closed.
func WireProjectProvisioning(services *module.Services) error {
	connector, err := module.Resolve[ProjectProvisioningConnector](services)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrUnavailable
	}
	entities, err := module.Resolve[core.EntityProvisioner](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	roles, err := module.Resolve[core.EntityRoleBindingStore](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	channels, err := module.Resolve[collab.ChannelProvisioner](services)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	return connector.Bind(entities, roles, channels)
}
