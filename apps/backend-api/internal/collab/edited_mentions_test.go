package collab

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestEditedMentionRecipientsNeverRepeatExistingMentions(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "test", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{2}})
	for _, added := range [][]guid.ID{{}, {4}, {2, 2}} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			actual, err := m.editedMentionRecipientsOn(conversationContext(t, 2), tx, 1, message.ID, added)
			if len(added) > 0 && added[0] == 2 {
				require.Equal(t, []guid.ID{2}, actual)
			} else {
				require.Empty(t, actual)
			}
			return err
		}))
	}
	s := module.NewServices()
	require.NoError(t, m.provideEditedMentions(s))
	require.NoError(t, s.Seal())
	capability, err := module.Resolve[EditedMentionNotifications](s)
	require.NoError(t, err)
	require.NotNil(t, capability.RecipientsOn)
}
