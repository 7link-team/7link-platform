package collab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentPairCreationStorageFailuresDoNotLeaveHalfPairs(t *testing.T) {
	for _, scenario := range []string{"policy", "query", "channel_create", "member_create", "audit"} {
		t.Run(scenario, func(t *testing.T) {
			m, db := conversationHarness(t)
			m.principals.HumanByLogin = func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
				return identity.Principal{}, false, nil
			}
			m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: id + 10, TenantID: tenant, PrincipalID: id, State: "ACTIVE"}, true, nil
			}
			fault := errors.New("pair storage failure")
			remove := func() {}
			switch scenario {
			case "policy":
				m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, fault
				}
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:pair", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Channels.String() {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, db.Callback().Query().Remove("test:pair")) }
			default:
				table := m.names.Channels.String()
				if scenario == "member_create" {
					table = m.names.Members.String()
				}
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:pair", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, db.Callback().Create().Remove("test:pair")) }
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.createAgentDirectOn(conversationContext(t, 6), tx, 1, 3, 7)
				return err
			}), fault)
			remove()
			var n int64
			for _, table := range []string{m.names.Channels.String(), m.names.Members.String()} {
				require.NoError(t, db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}

func TestAgentPairRejectsUnavailableDependenciesAndIdentifiers(t *testing.T) {
	m, db := conversationHarness(t)
	ctx := conversationContext(t, 6)
	m.principals.HumanByLogin = func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: id + 10, TenantID: tenant, PrincipalID: id, State: "ACTIVE"}, true, nil
	}
	require.ErrorIs(t, m.agentPairOn(ctx, nil, 1, 3, 7), ErrInvalidArgument)
	policy := m.policy
	m.policy = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.agentPairOn(ctx, tx, 1, 3, 7) }), ErrUnavailable)
	require.ErrorIs(t, m.inspectAgentProjectOn(ctx, db, 1, 3), ErrUnavailable)
	m.policy = policy
	fault := errors.New("identifier unavailable")
	for _, id := range []int64{0, -1} {
		m.ids = conversationIDs(func() (int64, error) { return id, nil })
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.createAgentDirectOn(ctx, tx, 1, 3, 7); return err }), ErrUnavailable)
	}
	m.ids = conversationIDs(func() (int64, error) { return 0, fault })
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.createAgentDirectOn(ctx, tx, 1, 3, 7); return err }), fault)
	m.ids = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.createAgentDirectOn(ctx, tx, 1, 3, 7); return err }), ErrUnavailable)
	var n int64
	require.NoError(t, db.Table(m.names.Channels.String()).Count(&n).Error)
	require.Zero(t, n)
}

func TestAgentDirectProjectExchangeCurrentAuthorityAndRollback(t *testing.T) {
	m, db := conversationHarness(t)
	human := m.principals.HumanByLogin
	m.principals.HumanByLogin = func(ctx context.Context, tx *gorm.DB, login string) (identity.Principal, bool, error) {
		if login == "6" || login == "7" {
			return identity.Principal{}, false, nil
		}
		return human(ctx, tx, login)
	}
	state := "ACTIVE"
	m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: id + 10, TenantID: tenant, PrincipalID: id, OwnerPrincipalID: 2, State: state}, tenant == 1 && (id == 6 || id == 7), nil
	}
	ctx := conversationContext(t, 6)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("agent-pair", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	req := httptest.NewRequest(http.MethodPost, "/entities/3/channels", strings.NewReader(`{"kind":"AGENT_DIRECT","peer_principal_id":"7"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "schema-pair")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var channel ChannelView
	create := func(actor, project, peer guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var err error
			channel, err = m.createChannelOn(conversationContext(t, actor), tx, 1, project, CreateChannelInput{Kind: "AGENT_DIRECT", PeerPrincipalID: peer})
			return err
		})
	}
	require.ErrorIs(t, create(2, 3, 7), ErrForbidden)
	require.ErrorIs(t, create(6, 3, 6), ErrInvalidArgument)
	require.NoError(t, create(6, 3, 7))
	first := channel.ID
	require.Equal(t, "AGENT_DIRECT", channel.Kind)
	require.Equal(t, guid.ID(3), *channel.ProjectID)
	require.NoError(t, create(7, 3, 6))
	require.Equal(t, first, channel.ID)
	require.NoError(t, create(6, 4, 7))
	require.NotEqual(t, first, channel.ID, "pairs cannot collapse across projects")
	second := channel.ID
	m.policy = func(_ context.Context, _ *gorm.DB, _, project guid.ID, _ string, actor guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: actor != 2 || project == 4}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		items, err := m.agentDirectChannelsOn(conversationContext(t, 2), tx, 1, 6, 0, 1)
		require.Len(t, items, 1, "hidden first project must not exhaust the visible page")
		if len(items) == 1 {
			require.Equal(t, second, items[0].ID)
		}
		return err
	}))
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	request := &channelCreateInput{ConversationEntityInput: ConversationEntityInput{EntityID: "3"}, IdempotencyKey: "agent-pair", Body: CreateChannelInput{Kind: "AGENT_DIRECT", PeerPrincipalID: 7}}
	opened, err := m.createChannelHTTP(ctx, request)
	require.NoError(t, err)
	require.Equal(t, first, opened.Body.Data.ID)
	replayed, err := m.createChannelHTTP(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	seen := int64(0)
	post := func(actor guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.postMessageOn(conversationContext(t, actor), tx, 1, 3, first, PostMessageInput{Body: "private exchange", Visibility: "CHANNEL", SeenSeq: &seen})
			return err
		})
	}
	require.ErrorIs(t, post(6), ErrUnavailable, "missing inbox binding must roll back")
	observed := []MessageReference{}
	m.messageObserver = func(_ context.Context, _ *gorm.DB, ref MessageReference) error {
		observed = append(observed, ref)
		return nil
	}
	require.NoError(t, post(6))
	require.Len(t, observed, 1)
	require.Equal(t, guid.ID(7), observed[0].AgentPrincipalID)
	require.Equal(t, "MESSAGE", observed[0].Kind)
	require.Error(t, post(7), "stale context must interrupt")
	seen = 1
	require.NoError(t, post(7))
	require.Len(t, observed, 2)
	require.Equal(t, guid.ID(6), observed[1].AgentPrincipalID)
	require.ErrorIs(t, post(2), ErrForbidden, "Human inspection never grants send authority")
	q := query.BindMembers(db, m.names)
	require.NoError(t, q.WithContext(ctx).Create(&model.ChannelMember{ChannelID: first, TenantID: 1, PrincipalID: 2, MemberKind: "HUMAN", Role: "MEMBER", Source: "EXPLICIT", JoinedAt: m.now(), CreatedAt: m.now(), UpdatedAt: m.now()}))
	require.ErrorIs(t, post(2), ErrUnavailable, "even a corrupt extra member cannot send")
	_, err = q.WithContext(ctx).Where(q.ChannelID.Eq(first), q.PrincipalID.Eq(guid.ID(2))).Delete()
	require.NoError(t, err)
	state = "RETIRED"
	require.ErrorIs(t, post(6), ErrMemberInactive)
	_, err = m.createChannelHTTP(ctx, request)
	require.Error(t, err, "a previously successful key cannot bypass current retirement")
	state = "ACTIVE"
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, create(6, 3, 7), ErrForbidden)
	require.ErrorIs(t, post(6), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.agentDirectMessagesOn(conversationContext(t, 2), tx, 1, 6, first, 0, 10)
		return err
	}), ErrForbidden)
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, err := m.agentDirectMessagesOn(conversationContext(t, 2), tx, 1, 6, first, 0, 10)
		require.Len(t, rows, 2)
		return err
	}))
	m.messageObserver = func(context.Context, *gorm.DB, MessageReference) error { return errors.New("inbox failed") }
	seen = 2
	require.Error(t, post(6))
	var count int64
	require.NoError(t, db.Table("c_collab_messages").Where("channel_id=?", first).Count(&count).Error)
	require.Equal(t, int64(2), count, "failed inbox must roll back message and sequence")
}
