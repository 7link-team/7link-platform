package collab

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelMembershipIsExplicitAndClientClassificationCannotDowngrade(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	internal := createConversation(t, m, db, "INTERNAL")
	for i := 0; i < 2; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			v, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
			require.True(t, v.External)
			return e
		}))
	}
	var n int64
	require.NoError(t, db.Table("c_collab_channel_members").Where("principal_id=4").Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{})
		return e
	}), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, internal.ID, 4, MemberInput{External: true})
		return e
	}), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 4), tx, 1, 3, ch.ID, 5, MemberInput{External: true})
		return e
	}), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 9, MemberInput{})
		return e
	}), ErrMemberInactive)
	require.NoError(t, db.Table("c_collab_channel_members").Where("principal_id=4").Update("left_at", m.now()).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
		return e
	}))
}

func TestChannelCreationCurrentAuthorityAndAtomicFailure(t *testing.T) {
	boom := errors.New("authority provider failure")
	for _, tc := range []struct {
		name   string
		change func(*Module)
		want   error
	}{
		{"principal failure", func(m *Module) {
			m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, false, boom
			}
		}, boom},
		{"inactive principal", func(m *Module) {
			m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, false, nil
			}
		}, ErrForbidden},
		{"human lookup failure", func(m *Module) {
			m.principals.HumanByLogin = func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
				return identity.Principal{}, false, boom
			}
		}, boom},
		{"service account", func(m *Module) {
			m.principals.HumanByLogin = func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
				return identity.Principal{}, false, nil
			}
		}, ErrForbidden},
		{"permission failure", func(m *Module) {
			m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, boom
			}
		}, boom},
		{"permission revoked", func(m *Module) {
			m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, nil
			}
		}, ErrForbidden},
		{"missing policy", func(m *Module) { m.policy = nil }, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, db := conversationHarness(t)
			tc.change(m)
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.createChannelOn(conversationContext(t, 2), tx, 1, 3, CreateChannelInput{Name: "Protected", Visibility: "INTERNAL"})
				return err
			})
			require.ErrorIs(t, err, tc.want)
			var n int64
			require.NoError(t, db.Table("c_collab_channels").Count(&n).Error)
			require.Zero(t, n)
		})
	}
	for _, table := range []string{"c_collab_channels", "c_collab_channel_members", "audit_probe", "c_domain_events"} {
		t.Run(table, func(t *testing.T) {
			m, db := conversationHarness(t)
			if table == "audit_probe" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return boom }
			}
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:channel-atomic", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(boom)
				}
			}))
			t.Cleanup(func() { _ = db.Callback().Create().Remove("test:channel-atomic") })
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.createChannelOn(conversationContext(t, 2), tx, 1, 3, CreateChannelInput{Name: "Atomic", Visibility: "INTERNAL"})
				return err
			})
			require.ErrorIs(t, err, boom)
			for _, name := range []string{"c_collab_channels", "c_collab_channel_members", "audit_probe", "c_domain_events"} {
				var n int64
				require.NoError(t, db.Table(name).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}

func TestChannelAgentInvitationAndScopedKeyset(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	createConversation(t, m, db, "INTERNAL")
	original := m.principals.HumanByLogin
	m.principals.HumanByLogin = func(ctx context.Context, tx *gorm.DB, login string) (identity.Principal, bool, error) {
		if login == "7" {
			return identity.Principal{}, false, nil
		}
		return original(ctx, tx, login)
	}
	m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 70, TenantID: 1, PrincipalID: 7, State: "ACTIVE"}, true, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 7, MemberInput{})
		require.Equal(t, "AGENT", v.MemberKind)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 7, MemberInput{External: true})
		return err
	}), ErrMemberInactive)
	var cursor string
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, err := m.listChannelsOn(conversationContext(t, 2), tx, 1, 3, "", 1)
		require.Len(t, page.Items, 1)
		require.True(t, page.HasMore)
		cursor = page.NextCursor
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, err := m.listChannelsOn(conversationContext(t, 2), tx, 1, 3, cursor, 1)
		require.Len(t, page.Items, 1)
		require.False(t, page.HasMore)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.listChannelsOn(conversationContext(t, 4), tx, 1, 3, cursor, 1)
		return err
	}), ErrInvalidArgument)
	for _, limit := range []int{0, 101} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.listChannelsOn(conversationContext(t, 2), tx, 1, 3, "", limit)
			return err
		}), ErrInvalidArgument)
	}
}

func TestChannelCommandsRejectMissingTransactionsAndMemberIdentity(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	_, err := m.createChannelOn(conversationContext(t, 2), db, 1, 3, CreateChannelInput{Name: "Outside transaction", Visibility: "INTERNAL"})
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 0, MemberInput{})
		return err
	}), ErrInvalidArgument)
	m.principals.LockActiveRow = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{})
		return err
	}), ErrUnavailable)
	_, err = readCursor(strings.Repeat("x", 513), cursorScope("channels"))
	require.ErrorIs(t, err, ErrInvalidArgument)
}
func TestChannelCreationBoundsAndMembershipFilters(t *testing.T) {
	m, db := conversationHarness(t)
	for _, input := range []CreateChannelInput{{Name: "", Visibility: "INTERNAL"}, {Name: strings.Repeat("a", 41), Visibility: "INTERNAL"}, {Name: "a", Visibility: "INVALID"}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.createChannelOn(conversationContext(t, 2), tx, 1, 3, input)
			return e
		}), ErrInvalidArgument)
	}
	ch := createConversation(t, m, db, "INTERNAL")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		p, e := m.listChannelsOn(conversationContext(t, 2), tx, 1, 3, "", 1)
		require.Len(t, p.Items, 1)
		require.Equal(t, ch.ID, p.Items[0].ID)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		p, e := m.listChannelsOn(conversationContext(t, 4), tx, 1, 3, "", 1)
		require.Empty(t, p.Items)
		return e
	}))
}
