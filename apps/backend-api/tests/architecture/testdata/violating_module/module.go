package violating

import (
	"7link.com/platform/internal/iam/identity"
	identitymodel "7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
)

type Module struct {
	service *identity.Service
	row     identitymodel.Principal
}

func LeakedPrincipal() identitymodel.Principal { return identitymodel.Principal{} }

func ModuleDependencies() []string { return []string{guid.Name} }

func (*Module) ModuleDependencies() []string {
	_ = guid.Name
	return []string{identity.Name}
}

func (*Module) InjectServices(services *module.Services) error {
	_, _ = module.Resolve[identity.IdentityProvisioner](services)
	_, _ = module.Resolve[guid.Source](services)
	_ = identity.NewService(nil, nil, nil, nil)
	return nil
}
