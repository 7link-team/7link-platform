package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestLoopAnchorVerifiesChannelRootAndCurrentMembership(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	root := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Loop anchor", Visibility: "CHANNEL"})
	ctx := conversationContext(t, 2)
	member := &model.ChannelMember{TenantID: 1, ChannelID: ch.ID, PrincipalID: 7, MemberKind: "AGENT", Source: "EXPLICIT", Role: "MEMBER", JoinedAt: m.now(), CreatedAt: m.now(), UpdatedAt: m.now()}
	require.NoError(t, query.BindMembers(db, m.names).WithContext(ctx).Create(member))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.loopAnchorOn(ctx, tx, 1, 3, ch.ID, root.ID, 7) }))
	for _, input := range []struct{ channel, thread int64 }{{int64(ch.ID) + 1, int64(root.ID)}, {int64(ch.ID), 999}, {0, int64(root.ID)}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			return m.loopAnchorOn(ctx, tx, 1, 3, guid.ID(input.channel), guid.ID(input.thread), 7)
		}))
	}
	q := query.BindMembers(db, m.names)
	_, e := q.WithContext(ctx).Where(q.ChannelID.Eq(ch.ID), q.PrincipalID.Eq(guid.ID(7))).Update(q.LeftAt, m.now())
	require.NoError(t, e)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.loopAnchorOn(ctx, tx, 1, 3, ch.ID, root.ID, 7) }))
}
