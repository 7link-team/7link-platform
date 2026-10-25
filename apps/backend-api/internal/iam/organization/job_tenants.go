package organization

import (
	"errors"

	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/module"
)

// WireJobTenants is optional for deployments without tenant enumeration. Declaring a
// PER_TENANT schedule without this capability is rejected by jobs.Initialize.
func WireJobTenants(services *module.Services) error {
	connector, err := module.Resolve[jobs.TenantFanoutConnector](services)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return jobs.ErrUnavailable
	}
	enumerate, err := module.Resolve[ActiveTenantEnumerator](services)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return connector.Bind(enumerate)
}
