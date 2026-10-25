package collab

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementBindingRejectsUnavailableAndPropagatesConnectorFailure(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.ErrorIs(t, m.injectRetirement(s), module.ErrServicesProviding)
	require.NoError(t, s.Seal())
	require.NoError(t, m.injectRetirement(s))
	s = module.NewServices()
	require.NoError(t, module.Provide[fleet.AgentRetirementRevocationConnector](s, fleet.AgentRetirementRevocationConnector{}))
	require.NoError(t, s.Seal())
	require.ErrorIs(t, m.injectRetirement(s), ErrUnavailable)
	fault := errors.New("retirement binding unavailable")
	called := false
	s = module.NewServices()
	require.NoError(t, module.Provide[fleet.AgentRetirementRevocationConnector](s, fleet.AgentRetirementRevocationConnector{Bind: func(name string, revoke func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error) error {
		called = true
		require.Equal(t, Name, name)
		require.NotNil(t, revoke)
		return fault
	}}))
	require.NoError(t, s.Seal())
	require.ErrorIs(t, m.injectRetirement(s), fault)
	require.True(t, called)
}

func TestRetirementRejectsMissingAuthorityAndInvalidAgentFacts(t *testing.T) {
	for _, scenario := range []string{"nil_tx", "foreign_tenant", "human", "missing_policy", "missing_agent", "policy_error", "agent_error", "active_agent"} {
		t.Run(scenario, func(t *testing.T) {
			m, db := conversationHarness(t)
			ctx := conversationContext(t, 2)
			fault := errors.New("retirement provider failure")
			expected := ErrForbidden
			m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 90, TenantID: 1, PrincipalID: 9, State: "ACTIVE"}, true, nil
			}
			switch scenario {
			case "nil_tx":
				require.ErrorIs(t, m.retireChannelMembershipsOn(ctx, nil, 1, 9, 900), ErrInvalidArgument)
				return
			case "foreign_tenant":
				ctx = conversationContext(t, 2)
			case "human":
				ctx = conversationContext(t, 8)
				expected = ErrMemberInactive
			case "missing_policy":
				m.tenantPolicy = nil
				expected = ErrUnavailable
			case "missing_agent":
				m.agents.ByPrincipalOn = nil
				expected = ErrUnavailable
			case "policy_error":
				m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, fault
				}
				expected = fault
			case "agent_error":
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, fault
				}
				expected = fault
			}
			tenant := guid.ID(1)
			if scenario == "foreign_tenant" {
				tenant = 2
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.retireChannelMembershipsOn(ctx, tx, tenant, 9, 900) }), expected)
		})
	}
}

func TestRetirementChannelMembershipRevocationIsAtomicAndPreservesHistory(t *testing.T) {
	m, db := conversationHarness(t)
	ctx := conversationContext(t, 2)
	channel := createConversation(t, m, db, "INTERNAL")
	postConversation(t, m, db, channel.ID, PostMessageInput{Body: "Historical evidence", Visibility: "CHANNEL"})
	m.tenantPolicy = core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	})
	m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 90, TenantID: 1, PrincipalID: 9, State: "RETIRING"}, true, nil
	}
	q := query.BindMembers(db, m.names)
	now := m.now()
	require.NoError(t, q.WithContext(ctx).Create(&model.ChannelMember{ChannelID: channel.ID, PrincipalID: 9, TenantID: 1, MemberKind: "AGENT", Role: "MEMBER", Source: "EXPLICIT", JoinedAt: now, CreatedAt: now, UpdatedAt: now}))
	abort := errors.New("late retirement failure")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.retireChannelMembershipsOn(ctx, tx, 1, 9, 900))
		return abort
	}), abort)
	member, err := q.WithContext(ctx).Where(q.ChannelID.Eq(channel.ID), q.PrincipalID.Eq(guid.ID(9))).First()
	require.NoError(t, err)
	require.Nil(t, member.LeftAt)
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.retireChannelMembershipsOn(ctx, tx, 1, 9, 900) }))
	}
	member, err = q.WithContext(ctx).Where(q.ChannelID.Eq(channel.ID), q.PrincipalID.Eq(guid.ID(9))).First()
	require.NoError(t, err)
	require.NotNil(t, member.LeftAt)
	human, err := q.WithContext(ctx).Where(q.ChannelID.Eq(channel.ID), q.PrincipalID.Eq(guid.ID(2))).First()
	require.NoError(t, err)
	require.Nil(t, human.LeftAt)
	var messages int64
	require.NoError(t, db.Table(m.names.Messages.String()).Count(&messages).Error)
	require.EqualValues(t, 1, messages)
	m.tenantPolicy = core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.retireChannelMembershipsOn(ctx, tx, 1, 9, 900) }), ErrForbidden, "replay must check current authority")
	require.Error(t, m.retireChannelMembershipsOn(ctx, db, 1, 9, 900))
}
