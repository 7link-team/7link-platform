package collab

import (
	"context"
	"slices"
	"testing"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type teamAnchorMembers struct {
	core.ActiveMemberChecker
	check func()
}

func (m teamAnchorMembers) IsMemberActiveOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) (bool, error) {
	m.check()
	return m.ActiveMemberChecker.IsMemberActiveOn(ctx, tx, tenant, principal)
}

func TestTeamLoopAnchorRequiresActualPrivateParticipants(t *testing.T) {
	m, db := conversationHarness(t)
	channel := createConversation(t, m, db, "INTERNAL")
	root := postConversation(t, m, db, channel.ID, PostMessageInput{Body: "Team loop", Visibility: "CHANNEL"})
	require.NoError(t, query.BindMembers(db, m.names).WithContext(t.Context()).Create(&model.ChannelMember{TenantID: 1, ChannelID: channel.ID, PrincipalID: 7, MemberKind: "AGENT", Source: "EXPLICIT", Role: "MEMBER", JoinedAt: m.now(), CreatedAt: m.now(), UpdatedAt: m.now()}))
	c := query.BindChannels(db, m.names)
	_, err := c.WithContext(t.Context()).Where(c.ID.Eq(channel.ID)).Updates(map[string]any{"project_id": nil, "kind": "DIRECT"})
	require.NoError(t, err)
	locked := []guid.ID{}
	expected := []guid.ID{}
	fenced := false
	enforce := true
	original := m.principals.LockActiveRow
	m.principals.LockActiveRow = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		if fenced {
			require.True(t, slices.Contains(locked, id))
		}
		locked = append(locked, id)
		return original(ctx, tx, id)
	}
	m.members = teamAnchorMembers{ActiveMemberChecker: m.members, check: func() {
		if enforce {
			require.Equal(t, expected, locked)
			fenced = true
		}
	}}
	check := func(actor, tenant, project, thread, target guid.ID) error {
		locked = nil
		fenced = false
		enforce = project == 0
		expected = []guid.ID{actor, target}
		slices.Sort(expected)
		expected = slices.Compact(expected)
		return db.Transaction(func(tx *gorm.DB) error {
			return m.loopAnchorOn(conversationContext(t, actor), tx, tenant, project, channel.ID, thread, target)
		})
	}
	require.NoError(t, check(2, 1, 0, root.ID, 7))
	require.NoError(t, check(7, 1, 0, root.ID, 7))
	for _, input := range [][5]guid.ID{{3, 1, 0, root.ID, 7}, {8, 1, 0, root.ID, 7}, {2, 2, 0, root.ID, 7}, {2, 1, 3, root.ID, 7}, {2, 1, 0, root.ID, 9}, {2, 1, 0, root.ID + 100, 7}} {
		require.Error(t, check(input[0], input[1], input[2], input[3], input[4]))
	}
	members := query.BindMembers(db, m.names)
	_, err = members.WithContext(t.Context()).Where(members.ChannelID.Eq(channel.ID), members.PrincipalID.Eq(guid.ID(7))).Update(members.LeftAt, m.now())
	require.NoError(t, err)
	require.Error(t, check(2, 1, 0, root.ID, 7))
	_, err = members.WithContext(t.Context()).Where(members.ChannelID.Eq(channel.ID), members.PrincipalID.Eq(guid.ID(7))).Updates(map[string]any{"left_at": nil, "external": true})
	require.NoError(t, err)
	require.Error(t, check(2, 1, 0, root.ID, 7))
	_, err = members.WithContext(t.Context()).Where(members.ChannelID.Eq(channel.ID), members.PrincipalID.Eq(guid.ID(7))).Update(members.External, false)
	require.NoError(t, err)
	_, err = c.WithContext(t.Context()).Where(c.ID.Eq(channel.ID)).Update(c.ArchivedAt, m.now())
	require.NoError(t, err)
	require.Error(t, check(2, 1, 0, root.ID, 7))
	_, err = c.WithContext(t.Context()).Where(c.ID.Eq(channel.ID)).Updates(map[string]any{"archived_at": nil})
	require.NoError(t, err)
	messages := query.BindMessages(db, m.names)
	_, err = messages.WithContext(t.Context()).Where(messages.ID.Eq(root.ID)).Update(messages.WithdrawnAt, m.now())
	require.NoError(t, err)
	require.Error(t, check(2, 1, 0, root.ID, 7), "withdrawn root remains persisted but cannot anchor a loop")
	_, err = messages.WithContext(t.Context()).Where(messages.ID.Eq(root.ID)).Updates(map[string]any{"withdrawn_at": nil})
	require.NoError(t, err)
	_, err = messages.WithContext(t.Context()).Where(messages.ID.Eq(root.ID)).Delete()
	require.NoError(t, err)
	require.Error(t, check(2, 1, 0, root.ID, 7))
	_, err = m.teamThreadAccessOn(conversationContext(t, 2), db, 1, root.ID, 7)
	require.ErrorIs(t, err, ErrInvalidArgument)
}
