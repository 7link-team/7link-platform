package delivery

import (
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestProjectProvisioningWiringPreservesOptionalProvidersAndBindingFailures(t *testing.T) {
	pending := module.NewServices()
	require.ErrorIs(t, WireProjectProvisioning(pending), module.ErrServicesProviding)
	missing := module.NewServices()
	require.NoError(t, missing.Seal())
	require.NoError(t, WireProjectProvisioning(missing))
	for _, mode := range []string{"missing_bind", "optional_absent", "configured", "bind_failure"} {
		t.Run(mode, func(t *testing.T) {
			services := module.NewServices()
			called := false
			failure := errors.New("provisioning bind rejected")
			connector := ProjectProvisioningConnector{}
			if mode != "missing_bind" {
				connector.Bind = func(entities core.EntityProvisioner, roles core.EntityRoleBindingStore, channels collab.ChannelProvisioner) error {
					called = true
					if mode == "configured" {
						require.NotNil(t, entities.CreateOn)
					}
					if mode == "bind_failure" {
						return failure
					}
					return nil
				}
			}
			require.NoError(t, module.Provide[ProjectProvisioningConnector](services, connector))
			if mode == "configured" {
				require.NoError(t, module.Provide[core.EntityProvisioner](services, core.EntityProvisioner{CreateOn: func(_ context.Context, _ *gorm.DB, e core.Entity) (core.Entity, error) { return e, nil }}))
				require.NoError(t, module.Provide[core.EntityRoleBindingStore](services, core.EntityRoleBindingStore{}))
				require.NoError(t, module.Provide[collab.ChannelProvisioner](services, collab.ChannelProvisioner{}))
			}
			require.NoError(t, services.Seal())
			err := WireProjectProvisioning(services)
			switch mode {
			case "missing_bind":
				require.ErrorIs(t, err, ErrUnavailable)
				require.False(t, called)
			case "bind_failure":
				require.ErrorIs(t, err, failure)
				require.True(t, called)
			default:
				require.NoError(t, err)
				require.True(t, called)
			}
		})
	}
}
