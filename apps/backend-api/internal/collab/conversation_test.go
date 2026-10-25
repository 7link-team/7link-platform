package collab

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func provideConversationServices(t *testing.T, m *Module, s *module.Services) (*events.Module, *idempotency.Module) {
	t.Helper()
	policy := core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	})
	require.NoError(t, module.Provide[core.EntityConstraintOn](s, policy))
	require.NoError(t, module.Provide[core.TenantConstraintOn](s, func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}))
	require.NoError(t, module.Provide[core.EntityOwnerResolver](s, func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{ScopeType: "tenant", TenantID: 1, EntityID: 3}, true, nil
	}))
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, module.Provide[*authz.Registrar](s, authz.NewDeclarationOnlyRegistrar(registry)))
	locks := dlock.NewModule(m.runtime)
	require.NoError(t, locks.ProvideServices(s))
	publisher := events.NewModule(m.runtime)
	require.NoError(t, publisher.ProvideServices(s))
	replay := idempotency.NewModule(m.runtime)
	require.NoError(t, replay.ProvideServices(s))
	return publisher, replay
}
func conversationHarness(t *testing.T) (*Module, *gorm.DB) {
	t.Helper()
	m, db := channelHarness(t)
	m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id, LoginName: id.String()}, id > 0 && id != 8, nil
	}
	m.principals.HumanByLogin = func(_ context.Context, _ *gorm.DB, login string) (identity.Principal, bool, error) {
		id, err := strconv.ParseInt(login, 10, 64)
		return identity.Principal{ID: guid.ID(id)}, err == nil, nil
	}
	s := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](s, m.ids))
	publisher, replay := provideConversationServices(t, m, s)
	require.NoError(t, s.Seal())
	require.NoError(t, publisher.InjectServices(s))
	require.NoError(t, replay.InjectServices(s))
	require.NoError(t, m.injectConversations(s))
	require.NoError(t, publisher.Initialize(t.Context()))
	require.NoError(t, platformi18n.NewModule(m.runtime, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, replay.Initialize(t.Context()))
	raw, err := db.DB()
	require.NoError(t, err)
	for _, source := range []goosex.Source{dlock.NewModule(m.runtime).MigrationSource(), publisher.MigrationSource(), replay.MigrationSource()} {
		source.Prefix = "c_"
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	}
	return m, db
}
func conversationContext(t *testing.T, actor guid.ID) context.Context {
	t.Helper()
	return authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: actor, TenantID: 1, SubjectType: authn.SubjectTypePrincipal})
}

func TestPostingWithConnectorStorageFailuresRollsBackAllMessageEffects(t *testing.T) {
	for _, scenario := range []struct{ operation, table string }{
		{"query", "c_collab_channel_connectors"}, {"create", "c_collab_channel_deliveries"},
		{"query", "c_collab_channels"}, {"query", "c_collab_messages"},
	} {
		t.Run(scenario.operation+scenario.table, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "INTERNAL")
			configureTestConnector(t, m, db, ch.ID, "HOOK")
			root := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "root", Visibility: "CHANNEL"})
			fault := errors.New("message dependency unavailable")
			inject := func(tx *gorm.DB) {
				if tx.Statement.Table == scenario.table {
					tx.AddError(fault)
				}
			}
			var remove func()
			if scenario.operation == "query" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:post", inject))
				remove = func() { require.NoError(t, db.Callback().Query().Remove("test:post")) }
			} else {
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:post", inject))
				remove = func() { require.NoError(t, db.Callback().Create().Remove("test:post")) }
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, PostMessageInput{Body: "must roll back", Visibility: "CHANNEL", ReplyToMessageID: &root.ID})
				return err
			})
			remove()
			require.ErrorIs(t, err, fault)
			var n int64
			for _, table := range []string{m.names.Messages.String(), m.names.Deliveries.String()} {
				require.NoError(t, db.Table(table).Count(&n).Error)
				require.EqualValues(t, 1, n)
			}
		})
	}
}
func createConversation(t *testing.T, m *Module, db *gorm.DB, visibility string) ChannelView {
	t.Helper()
	var v ChannelView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		v, err = m.createChannelOn(conversationContext(t, 2), tx, 1, 3, CreateChannelInput{Name: "Discussion " + visibility, Visibility: visibility})
		return err
	}))
	return v
}
func postConversation(t *testing.T, m *Module, db *gorm.DB, ch guid.ID, in PostMessageInput) MessageView {
	t.Helper()
	var v MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		v, err = m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch, in)
		return err
	}))
	return v
}
func TestPostMessagePersistsAtomicFactsAndInternalAncestry(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	root := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "# Internal decision", Visibility: "INTERNAL", MentionPrincipalIDs: []guid.ID{2, 2}})
	reply := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Still internal", Visibility: "CHANNEL", ReplyToMessageID: &root.ID})
	require.Equal(t, "INTERNAL", reply.Visibility)
	require.EqualValues(t, 2, reply.Seq)
	require.Equal(t, []guid.ID{2}, root.Mentions)
	var eventsCount int64
	require.NoError(t, db.Table("c_domain_events").Where("event_type=?", "collab.message_posted").Count(&eventsCount).Error)
	require.EqualValues(t, 2, eventsCount)
	var payload string
	require.NoError(t, db.Table("c_domain_events").Select("data").Where("event_type=?", "collab.message_posted").Take(&payload).Error)
	require.NotContains(t, payload, "Internal decision")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, PostMessageInput{Body: "Nested", Visibility: "CHANNEL", ReplyToMessageID: &reply.ID})
		return err
	}), ErrNotFound)
	for _, table := range []string{"c_collab_messages", "c_collab_message_mentions"} {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		require.Equal(t, map[string]int64{"c_collab_messages": 2, "c_collab_message_mentions": 1}[table], n)
	}
}
func TestPostMessageRollbackOnOutboxFailure(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:outbox", func(tx *gorm.DB) {
		if tx.Statement.Table == "c_domain_events" {
			tx.AddError(errors.New("injected outbox failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove("test:outbox") })
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, PostMessageInput{Body: "Must roll back", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{2}})
		return err
	})
	require.ErrorContains(t, err, "injected outbox")
	for _, table := range []string{"c_collab_messages", "c_collab_message_mentions"} {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		require.Zero(t, n)
	}
	var channel model.Channel
	require.NoError(t, db.Table("c_collab_channels").Take(&channel).Error)
	require.Zero(t, channel.LastSeq)
	var auditCount int64
	require.NoError(t, db.Table("audit_probe").Where("event_type=?", "collab.message_posted").Count(&auditCount).Error)
	require.Zero(t, auditCount)
}
func TestPostMessageRejectsInvalidInputAndIdentity(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	for _, in := range []PostMessageInput{{Body: "", Visibility: "CHANNEL"}, {Body: strings.Repeat("文", 8001), Visibility: "CHANNEL"}, {Body: "a", Visibility: "SECRET"}, {Body: "a", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{4}}, {Body: "a", Visibility: "CHANNEL", Attachments: []AttachmentInput{{}}}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, in)
			return err
		}))
	}
	for _, actor := range []guid.ID{0, 4, 8, 9} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.postMessageOn(conversationContext(t, actor), tx, 1, 3, ch.ID, PostMessageInput{Body: "a", Visibility: "CHANNEL"})
			return err
		}))
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.postMessageOn(conversationContext(t, 2), tx, 2, 3, ch.ID, PostMessageInput{Body: "a", Visibility: "CHANNEL"})
		return err
	}), ErrForbidden)
	require.NoError(t, db.Table("c_collab_channels").Where("id=?", ch.ID).Update("archived_at", m.now()).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, PostMessageInput{Body: "a", Visibility: "CHANNEL"})
		return err
	}), ErrNotFound)
}

func TestConversationAttachmentsFailClosedWithoutFileAuthorization(t *testing.T) {
	m, db := conversationHarness(t)
	channel := createConversation(t, m, db, "CLIENT_SHARED")
	var before int64
	require.NoError(t, db.Table("c_collab_messages").Count(&before).Error)
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := m.postMessageOn(conversationContext(t, 2), tx, 1, 3, channel.ID, PostMessageInput{Body: "Private file", Visibility: "CHANNEL", Attachments: []AttachmentInput{{SpaceID: 11, FileID: 12, VersionID: 13, ExpectedFileVersion: 1}}})
		return err
	})
	require.ErrorIs(t, err, ErrUnavailable)
	var after int64
	require.NoError(t, db.Table("c_collab_messages").Count(&after).Error)
	require.Equal(t, before, after)
}

func TestMessagePersistenceFailuresRollBackSequenceMentionsAndAudit(t *testing.T) {
	for _, table := range []string{"c_collab_messages", "c_collab_message_mentions", "c_collab_channels", "audit_probe", "c_domain_events"} {
		t.Run(table, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "INTERNAL")
			before := map[string]int64{}
			for _, name := range []string{"c_collab_messages", "c_collab_message_mentions", "audit_probe", "c_domain_events"} {
				var count int64
				require.NoError(t, db.Table(name).Count(&count).Error)
				before[name] = count
			}
			boom := errors.New("injected persistence failure")
			if table == "audit_probe" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return boom }
			}
			inject := func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(boom)
				}
			}
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:message-atomic", inject))
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:message-atomic", inject))
			t.Cleanup(func() {
				_ = db.Callback().Create().Remove("test:message-atomic")
				_ = db.Callback().Update().Remove("test:message-atomic")
			})
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, PostMessageInput{Body: "Atomic message", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{2}})
				return err
			})
			require.ErrorIs(t, err, boom)
			for name, count := range before {
				var actual int64
				require.NoError(t, db.Table(name).Count(&actual).Error)
				require.Equal(t, count, actual)
			}
			var channel model.Channel
			require.NoError(t, db.Table("c_collab_channels").Where("id=?", ch.ID).Take(&channel).Error)
			require.Zero(t, channel.LastSeq)
		})
	}
}

type conversationIDs func() (int64, error)

func (f conversationIDs) Next() (int64, error) { return f() }

func TestConversationIdentifierAndCurrentPrincipalFailures(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	boom := errors.New("identifier provider failed")
	for _, zero := range []bool{false, true} {
		m.ids = conversationIDs(func() (int64, error) {
			if zero {
				return 0, nil
			}
			return 0, boom
		})
		for _, create := range []bool{false, true} {
			err := db.Transaction(func(tx *gorm.DB) error {
				if create {
					_, err := m.createChannelOn(conversationContext(t, 2), tx, 1, 3, CreateChannelInput{Name: "No ID", Visibility: "INTERNAL"})
					return err
				}
				_, err := m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, PostMessageInput{Body: "No ID", Visibility: "CHANNEL"})
				return err
			})
			if zero {
				require.ErrorIs(t, err, ErrUnavailable)
			} else {
				require.ErrorIs(t, err, boom)
			}
		}
	}
	original := m.principals.LockActiveRow
	for _, inactive := range []bool{false, true} {
		m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
			if inactive {
				return identity.Principal{}, false, nil
			}
			return identity.Principal{}, false, boom
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			_, _, err := m.authorizeChannel(conversationContext(t, 2), tx, 1, 3, ch.ID, "channel_read")
			return err
		})
		if inactive {
			require.ErrorIs(t, err, ErrForbidden)
		} else {
			require.ErrorIs(t, err, boom)
		}
	}
	m.principals.LockActiveRow = original
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.authorizeChannel(t.Context(), tx, 1, 3, ch.ID, "channel_read")
		return err
	}), ErrForbidden)
	m.policy = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.authorizeChannel(conversationContext(t, 2), tx, 1, 3, ch.ID, "channel_read")
		return err
	}), ErrUnavailable)
	var n int64
	require.NoError(t, db.Table("c_collab_messages").Count(&n).Error)
	require.Zero(t, n)
}
