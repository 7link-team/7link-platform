package collab

import (
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestModuleWiresTransactionalProvisioner(t *testing.T) {
	m, db := channelHarness(t)
	require.Equal(t, Name, m.ModuleName())
	require.Equal(t, []string{jobs.Name, files.Name, guid.Name, core.Name, identity.Name, fleet.Name, audit.Name, events.Name, idempotency.Name, platformi18n.Name}, m.ModuleDependencies())
	require.False(t, m.Enabled())
	m.config.Enabled = true
	require.True(t, m.Enabled())
	s := module.NewServices()
	require.NoError(t, m.ProvideServices(s))
	require.NoError(t, module.Provide[files.ConversationResources](s, files.ConversationResources{}))
	bindings, e := files.NewResourceBindings("c_")
	require.NoError(t, e)
	require.NoError(t, module.Provide[*files.ResourceBindings](s, bindings))
	require.NoError(t, module.Provide[fleet.AgentDirectory](s, m.agents))
	require.NoError(t, module.Provide[guid.Source](s, m.ids))
	require.NoError(t, module.Provide[core.ActiveMemberChecker](s, m.members))
	require.NoError(t, module.Provide[identity.PrincipalStateStore](s, m.principals))
	require.NoError(t, module.Provide[audit.Appender](s, m.audit))
	provideConversationServices(t, m, s)
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

func TestConversationStartupRejectsEachMissingCapability(t *testing.T) {
	m, _ := conversationHarness(t)
	providers := []func(*module.Services) error{
		func(s *module.Services) error { return module.Provide[core.TenantConstraintOn](s, m.tenantPolicy) },
		func(s *module.Services) error { return module.Provide[core.EntityConstraintOn](s, m.policy) },
		func(s *module.Services) error { return module.Provide[core.EntityOwnerResolver](s, m.owner) },
		func(s *module.Services) error { return module.Provide[*authz.Registrar](s, m.registrar) },
		func(s *module.Services) error { return module.Provide[events.Publisher](s, m.publisher) },
		func(s *module.Services) error { return module.Provide[idempotency.Store](s, m.replay) },
	}
	for missing := 0; missing <= len(providers); missing++ {
		s := module.NewServices()
		for i, provide := range providers {
			if i != missing {
				require.NoError(t, provide(s))
			}
		}
		require.NoError(t, s.Seal())
		require.Error(t, (&Module{}).injectConversations(s), "missing dependency %d must prevent startup", missing)
	}
}
func TestModuleRejectsMissingDependencies(t *testing.T) {
	m, _ := channelHarness(t)
	for _, missing := range []string{files.Name, guid.Name, core.Name, identity.Name, fleet.Name, audit.Name} {
		s := module.NewServices()
		if missing != files.Name {
			require.NoError(t, module.Provide[files.ConversationResources](s, files.ConversationResources{}))
			bindings, err := files.NewResourceBindings("c_")
			require.NoError(t, err)
			require.NoError(t, module.Provide[*files.ResourceBindings](s, bindings))
		}
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

func TestModuleRejectsConflictingPublishedCapabilities(t *testing.T) {
	m := &Module{}
	providers := []func(*module.Services) error{
		func(s *module.Services) error {
			return module.Provide[PackageDeliverySender](s, PackageDeliverySender{})
		},
		func(s *module.Services) error {
			return module.Provide[IntakeDispositionNotice](s, m.intakeDispositionNoticeOn)
		},
		func(s *module.Services) error { return module.Provide[IntakeReplyRouting](s, IntakeReplyRouting{}) },
		func(s *module.Services) error {
			return module.Provide[IntakeDispositionReceipts](s, m.intakeDispositionReceiptOn)
		},
		func(s *module.Services) error {
			return module.Provide[ChannelDispatchConnector](s, ChannelDispatchConnector{})
		},
		func(s *module.Services) error { return module.Provide[ChannelPoster](s, ChannelPoster{}) },
		func(s *module.Services) error {
			return module.Provide[EditedMentionNotifications](s, EditedMentionNotifications{})
		},
		func(s *module.Services) error { return module.Provide[IntakeConversation](s, IntakeConversation{}) },
		func(s *module.Services) error { return module.Provide[RealtimeTopics](s, RealtimeTopics{}) },
		func(s *module.Services) error { return module.Provide[AgentDirectReader](s, AgentDirectReader{}) },
		func(s *module.Services) error { return module.Provide[MentionNotifications](s, MentionNotifications{}) },
		func(s *module.Services) error { return module.Provide[ThreadAnchor](s, ThreadAnchor{}) },
		func(s *module.Services) error { return module.Provide[MessageWorkConnector](s, MessageWorkConnector{}) },
		func(s *module.Services) error {
			return module.Provide[MessageObserverConnector](s, MessageObserverConnector{})
		},
		func(s *module.Services) error { return module.Provide[ThreadAccess](s, ThreadAccess{}) },
		func(s *module.Services) error { return module.Provide[ChannelProvisioner](s, ChannelProvisioner{}) },
	}
	for i, provide := range providers {
		s := module.NewServices()
		require.NoError(t, provide(s), "provider %d", i)
		require.ErrorIs(t, (&Module{}).ProvideServices(s), module.ErrServiceDuplicate, "provider %d", i)
	}
}
