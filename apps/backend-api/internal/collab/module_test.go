package collab

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestModuleWiresTransactionalProvisioner(t *testing.T) {
	m, db := channelHarness(t)
	require.Equal(t, Name, m.ModuleName())
	require.Equal(t, []string{guid.Name, core.Name, identity.Name, fleet.Name, audit.Name}, m.ModuleDependencies())
	require.False(t, m.Enabled())
	m.config.Enabled = true
	require.True(t, m.Enabled())
	s := module.NewServices()
	require.NoError(t, m.ProvideServices(s))
	require.NoError(t, module.Provide[fleet.AgentDirectory](s, m.agents))
	require.NoError(t, module.Provide[guid.Source](s, m.ids))
	require.NoError(t, module.Provide[core.ActiveMemberChecker](s, m.members))
	require.NoError(t, module.Provide[identity.PrincipalStateStore](s, m.principals))
	require.NoError(t, module.Provide[audit.Appender](s, m.audit))
	require.NoError(t, s.Seal())
	require.NoError(t, m.InjectServices(s))
	require.NoError(t, m.Initialize(t.Context()))
	provisioner, err := module.Resolve[ChannelProvisioner](s)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := provisioner.CreateDefaultOn(t.Context(), tx, DefaultChannelInput{Audit: ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"}, TenantID: 1, ProjectID: 3, Name: "Project", MemberPrincipalIDs: []guid.ID{2}})
		return err
	}))
}
func TestModuleRejectsMissingDependencies(t *testing.T) {
	m, _ := channelHarness(t)
	for _, missing := range []string{guid.Name, core.Name, identity.Name, fleet.Name, audit.Name} {
		s := module.NewServices()
		if missing != fleet.Name {
			require.NoError(t, module.Provide[fleet.AgentDirectory](s, m.agents))
		}
		if missing != guid.Name {
			require.NoError(t, module.Provide[guid.Source](s, m.ids))
		}
		if missing != core.Name {
			require.NoError(t, module.Provide[core.ActiveMemberChecker](s, m.members))
		}
		if missing != identity.Name {
			require.NoError(t, module.Provide[identity.PrincipalStateStore](s, m.principals))
		}
		if missing != audit.Name {
			require.NoError(t, module.Provide[audit.Appender](s, m.audit))
		}
		require.NoError(t, s.Seal())
		require.Error(t, (&Module{}).InjectServices(s))
	}
	require.ErrorIs(t, (&Module{}).Initialize(t.Context()), ErrUnavailable)
}
