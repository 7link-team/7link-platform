package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"testing"
)

type failingMembership struct {
	active bool
	err    error
}

func (f failingMembership) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return f.active, f.err
}
func (f failingMembership) IsMemberActiveOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
	return f.active, f.err
}

func TestConversationParticipantIdentityFailuresNeverGrantAccess(t *testing.T) {
	for _, scenario := range []string{"missing", "principal_error", "inactive", "human_error", "wrong_human", "membership_error", "membership_inactive", "agent_missing", "agent_error"} {
		t.Run(scenario, func(t *testing.T) {
			m, db := conversationHarness(t)
			fault := errors.New("identity dependency failure")
			expected := fault
			switch scenario {
			case "missing":
				m.members = nil
				expected = ErrUnavailable
			case "principal_error":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "inactive":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				expected = ErrMemberInactive
			case "human_error":
				m.principals.HumanByLogin = func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "wrong_human":
				m.principals.HumanByLogin = func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
					return identity.Principal{ID: 99}, true, nil
				}
				expected = ErrForbidden
			case "membership_error":
				m.members = failingMembership{err: fault}
			case "membership_inactive":
				m.members = failingMembership{}
				expected = ErrMemberInactive
			case "agent_missing", "agent_error":
				m.principals.HumanByLogin = func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				if scenario == "agent_missing" {
					m.agents.ByPrincipalOn = nil
					expected = ErrUnavailable
				} else {
					m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
						return fleet.AgentIdentity{}, false, fault
					}
				}
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				kind, err := m.directParticipantOn(conversationContext(t, 2), tx, 1, 2)
				require.ErrorIs(t, err, expected)
				require.Empty(t, kind)
				if scenario != "agent_missing" && scenario != "agent_error" {
					p, err := m.humanOn(conversationContext(t, 2), tx, 1, 2)
					require.ErrorIs(t, err, expected)
					require.Zero(t, p.ID)
				}
				return nil
			}))
		})
	}
}

func TestAgentDirectReadersRejectInvalidScopesAndPagination(t *testing.T) {
	m, db := conversationHarness(t)
	ctx := conversationContext(t, 2)
	_, err := m.agentDirectChannelsOn(ctx, nil, 1, 7, 0, 50)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.agentDirectMessagesOn(ctx, nil, 1, 7, 3, 0, 50)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, limit := range []int{0, 102} {
			_, err := m.agentDirectChannelsOn(ctx, tx, 1, 7, 0, limit)
			require.ErrorIs(t, err, ErrInvalidArgument)
			_, err = m.agentDirectMessagesOn(ctx, tx, 1, 7, 3, 0, limit)
			require.ErrorIs(t, err, ErrInvalidArgument)
		}
		return nil
	}))
}

func TestHumanAgentDirectDeliveryPrivacyAndCurrentAuthority(t *testing.T) {
	m, db := conversationHarness(t)
	humanLookup := m.principals.HumanByLogin
	m.principals.HumanByLogin = func(ctx context.Context, tx *gorm.DB, login string) (identity.Principal, bool, error) {
		if login == "7" {
			return identity.Principal{}, false, nil
		}
		return humanLookup(ctx, tx, login)
	}
	state := "ACTIVE"
	m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 70, TenantID: tenant, PrincipalID: id, OwnerPrincipalID: 5, State: state}, tenant == 1 && id == 7, nil
	}
	var direct DirectChannelResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		direct, e = m.createDirectOn(conversationContext(t, 2), tx, 1, 7)
		return e
	}))
	var members []model.ChannelMember
	require.NoError(t, db.Table("c_collab_channel_members").Where("channel_id=?", direct.ID).Order("principal_id").Find(&members).Error)
	require.Len(t, members, 2)
	require.Equal(t, "HUMAN", members[0].MemberKind)
	require.Equal(t, "AGENT", members[1].MemberKind)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createDirectOn(conversationContext(t, 7), tx, 1, 2); return e }), ErrForbidden)
	// An unbound inbox must fail closed with no message, mention, or event.
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postDirectOn(conversationContext(t, 2), tx, 1, direct.ID, DirectTextInput{Body: "Do not lose this message"})
		return e
	}), ErrUnavailable)
	var count int64
	require.NoError(t, db.Table("c_collab_messages").Count(&count).Error)
	require.Zero(t, count)
	refs := []MessageReference{}
	m.messageObserver = func(ctx context.Context, tx *gorm.DB, r MessageReference) error {
		refs = append(refs, r)
		return nil
	}
	var sent MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		sent, e = m.postDirectOn(conversationContext(t, 2), tx, 1, direct.ID, DirectTextInput{Body: "Human to Agent"})
		return e
	}))
	require.Equal(t, []guid.ID{7}, sent.Mentions)
	require.Len(t, refs, 1)
	require.Equal(t, "MENTION", refs[0].Kind)
	require.Equal(t, sent.ID, refs[0].MessageID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		seen := sent.Seq
		_, e := m.postDirectOn(conversationContext(t, 7), tx, 1, direct.ID, DirectTextInput{Body: "Agent to Human", SeenSeq: &seen})
		return e
	}))
	require.Len(t, refs, 1, "Agent replies must not notify themselves")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, kind, e := m.messageAccessOn(conversationContext(t, 7), tx, 1, sent.ID, "channel_read")
		require.Equal(t, "AGENT", kind, "direct mutation audit must preserve the actual actor kind")
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		visible, e := m.notificationMentionVisibleOn(t.Context(), tx, 1, sent.ID, 5)
		require.False(t, visible, "Agent ownership does not grant participant notifications")
		return e
	}))
	page, e := m.readDirectHTTP(conversationContext(t, 7), &DirectReadInput{ConversationChannelInput: ConversationChannelInput{ChannelID: direct.ID.String()}, Limit: 10})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 2)
	_, e = m.readDirectHTTP(conversationContext(t, 5), &DirectReadInput{ConversationChannelInput: ConversationChannelInput{ChannelID: direct.ID.String()}, Limit: 10})
	require.Error(t, e, "responsible Human uses the separate read-only inspection API")
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createDirectOn(conversationContext(t, 2), tx, 1, 7); return e }), ErrForbidden)
	state = "RETIRED"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postDirectOn(conversationContext(t, 2), tx, 1, direct.ID, DirectTextInput{Body: "Retired"})
		return e
	}), ErrMemberInactive)
	page, e = m.readDirectHTTP(conversationContext(t, 2), &DirectReadInput{ConversationChannelInput: ConversationChannelInput{ChannelID: direct.ID.String()}, Limit: 10})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 2, "retirement preserves Human history")
}

type directAuthority struct{ contractAuthority }

func (a *directAuthority) Authenticate(ctx context.Context, bearer, cookie string) (*authn.Claims, error) {
	if bearer == "" && cookie == "" {
		return nil, errors.New("missing credentials")
	}
	return a.contractAuthority.Authenticate(ctx, bearer, cookie)
}

func TestHumanDirectHTTPReplayPrivacyAndCursorScope(t *testing.T) {
	m, db := conversationHarness(t)
	declarations := humachi.New(chi.NewRouter(), huma.DefaultConfig("declarations", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), declarations))
	authority := &directAuthority{contractAuthority{allow: true}}
	m.registrar = authz.NewRegistrar(m.registrar.Registry(), authority, authority, authority)
	api := humachi.New(chi.NewRouter(), huma.DefaultConfig("direct-test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	h := humatest.Wrap(t, api)
	const path = "/tenants/1/direct-channels"
	const auth = "Authorization: Bearer fixture"
	body := map[string]any{"peer_principal_id": "4"}
	require.Equal(t, http.StatusUnauthorized, h.Post(path, "Idempotency-Key: create", body).Code)
	contract := h.Do(http.MethodOptions, path+"?method=POST", auth)
	require.Equal(t, http.StatusOK, contract.Code, contract.Body.String())
	require.Contains(t, contract.Body.String(), `"peer_principal_id"`)
	created := h.Post(path, auth, "Idempotency-Key: create", body)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var envelope struct {
		Data DirectChannelResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &envelope))
	channel := envelope.Data.ID
	require.Positive(t, channel)
	require.Nil(t, envelope.Data.ProjectID)
	hub := h.Get("/tenants/1/conversations?limit=1", auth)
	require.Equal(t, http.StatusOK, hub.Code, hub.Body.String())
	require.Contains(t, hub.Body.String(), `"kind":"DIRECT"`)
	filtered := h.Get("/tenants/1/conversations?search=no-matching-conversation", auth)
	require.Equal(t, http.StatusOK, filtered.Code, filtered.Body.String())
	var filteredPage struct {
		Data CursorPage[ChannelView] `json:"data"`
	}
	require.NoError(t, json.Unmarshal(filtered.Body.Bytes(), &filteredPage))
	require.Empty(t, filteredPage.Data.Items, "the public search parameter must actually filter the hub")
	again := h.Post(path, auth, "Idempotency-Key: create", body)
	require.Equal(t, http.StatusCreated, again.Code, again.Body.String())
	require.Equal(t, "true", again.Header().Get("Idempotent-Replayed"))
	var replayEnvelope struct {
		Data DirectChannelResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(again.Body.Bytes(), &replayEnvelope))
	require.Equal(t, envelope.Data, replayEnvelope.Data)
	existing := h.Post(path, auth, "Idempotency-Key: reuse-pair", body)
	require.Equal(t, http.StatusOK, existing.Code, existing.Body.String())
	require.Equal(t, http.StatusConflict, h.Post(path, auth, "Idempotency-Key: create", map[string]any{"peer_principal_id": "5"}).Code)
	messages := path + "/" + channel.String() + "/messages"
	marker := path + "/" + channel.String() + "/read-marker"
	require.Equal(t, http.StatusUnprocessableEntity, h.Put(marker, auth, map[string]any{"seq": -1}).Code)
	for _, key := range []string{"first", "second"} {
		posted := h.Post(messages, auth, "Idempotency-Key: "+key, map[string]any{"body": key})
		require.Equal(t, http.StatusCreated, posted.Code, posted.Body.String())
	}
	replayed := h.Post(messages, auth, "Idempotency-Key: first", map[string]any{"body": "first"})
	require.Equal(t, http.StatusCreated, replayed.Code, replayed.Body.String())
	require.Equal(t, "true", replayed.Header().Get("Idempotent-Replayed"))
	read := h.Get(messages+"?limit=1", auth)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	var page struct {
		Data CursorPage[MessageView] `json:"data"`
	}
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &page))
	require.True(t, page.Data.HasMore)
	require.Equal(t, "second", page.Data.Items[0].Body)
	older := h.Get(messages+"?limit=1&cursor="+page.Data.NextCursor, auth)
	require.Equal(t, http.StatusOK, older.Code, older.Body.String())
	require.Contains(t, older.Body.String(), `"body":"first"`)
	_, err := m.readDirectHTTP(conversationContext(t, 4), &DirectReadInput{ConversationChannelInput: ConversationChannelInput{ChannelID: channel.String()}, Limit: 1, Cursor: page.Data.NextCursor})
	require.Error(t, err, "a cursor is bound to its reader, even the other participant")
	_, err = m.readDirectHTTP(conversationContext(t, 5), &DirectReadInput{ConversationChannelInput: ConversationChannelInput{ChannelID: channel.String()}, Limit: 1})
	require.Error(t, err, "ordinary tenant membership grants no private read")
	var count int64
	require.NoError(t, db.Table("c_collab_messages").Count(&count).Error)
	require.EqualValues(t, 2, count, "replayed send has no second message")
	require.Equal(t, http.StatusNotFound, h.Put(marker, auth, map[string]any{"seq": 99}).Code)
	marked := h.Put(marker, auth, map[string]any{"seq": 2})
	require.Equal(t, http.StatusOK, marked.Code, marked.Body.String())
	monotonic := h.Put(marker, auth, map[string]any{"seq": 1})
	require.Equal(t, http.StatusOK, monotonic.Code, monotonic.Body.String())
	var markerEnvelope struct {
		Data int64 `json:"data"`
	}
	require.NoError(t, json.Unmarshal(monotonic.Body.Bytes(), &markerEnvelope))
	require.EqualValues(t, 2, markerEnvelope.Data)
	var stored model.ChannelMember
	require.NoError(t, db.Table("c_collab_channel_members").Where("channel_id=? AND principal_id=2", channel).Take(&stored).Error)
	require.EqualValues(t, 2, stored.LastReadSeq)
	cleared := h.Get("/tenants/1/conversations", auth)
	require.Equal(t, http.StatusOK, cleared.Code)
	require.Contains(t, cleared.Body.String(), `"unread":0`)
	original := m.principals.LockActiveRow
	m.principals.LockActiveRow = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		p, active, err := original(ctx, tx, id)
		return p, active && id != 4, err
	}
	require.Equal(t, http.StatusUnprocessableEntity, h.Post(messages, auth, "Idempotency-Key: first", map[string]any{"body": "first"}).Code, "replay must recheck the peer's current status")
	require.Equal(t, http.StatusUnprocessableEntity, h.Post(path, auth, "Idempotency-Key: create", body).Code)
	require.Equal(t, http.StatusOK, h.Get(messages, auth).Code, "inactive peer preserves reader history")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.directReadMarkerOn(conversationContext(t, 2), tx, 1, channel, -1)
		return e
	}), ErrInvalidArgument)
	m.runtime = nil
	require.Equal(t, http.StatusServiceUnavailable, h.Put(marker, auth, map[string]any{"seq": 2}).Code)
}

func TestDirectStorageFailuresNeverCommitPartialConversationOrMessage(t *testing.T) {
	for _, operation := range []string{"create", "send", "read", "hub", "mark"} {
		for _, table := range []string{"c_collab_channels", "c_collab_channel_members", "c_collab_messages", "audit_probe", "c_domain_events"} {
			if operation == "mark" && (table == "c_collab_channels" || table == "c_collab_messages") {
				continue
			}
			if (operation == "read" || operation == "hub") && (table == "audit_probe" || table == "c_domain_events") {
				continue
			}
			if operation == "create" && table == "c_collab_messages" {
				continue
			}
			if operation == "send" && table == "c_collab_channel_members" {
				continue
			}
			t.Run(operation+"/"+table, func(t *testing.T) {
				m, db := conversationHarness(t)
				ctx := conversationContext(t, 2)
				var channel DirectChannelResult
				if operation != "create" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; channel, e = m.createDirectOn(ctx, tx, 1, 4); return e }))
				}
				if operation == "mark" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						_, e := m.postDirectOn(ctx, tx, 1, channel.ID, DirectTextInput{Body: "Visible message"})
						return e
					}))
				}
				before := map[string]int64{}
				for _, name := range []string{"c_collab_channels", "c_collab_channel_members", "c_collab_messages", "audit_probe", "c_domain_events"} {
					var n int64
					require.NoError(t, db.Table(name).Count(&n).Error)
					before[name] = n
				}
				boom := errors.New("private storage failure")
				inject := func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(boom)
					}
				}
				if table == "audit_probe" {
					m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return boom }
				}
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("fixture:fail", inject))
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("fixture:fail", inject))
				if operation == "read" || operation == "hub" && table != "c_collab_channel_members" {
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("fixture:fail", inject))
				}
				var err error
				switch operation {
				case "mark":
					err = db.Transaction(func(tx *gorm.DB) error { _, e := m.directReadMarkerOn(ctx, tx, 1, channel.ID, 1); return e })
				case "create":
					err = db.Transaction(func(tx *gorm.DB) error { _, e := m.createDirectOn(ctx, tx, 1, 4); return e })
				case "send":
					err = db.Transaction(func(tx *gorm.DB) error {
						_, e := m.postDirectOn(ctx, tx, 1, channel.ID, DirectTextInput{Body: "No partial message"})
						return e
					})
				case "read":
					page, e := m.readDirectHTTP(ctx, &DirectReadInput{ConversationChannelInput: ConversationChannelInput{ChannelID: channel.ID.String()}, Limit: 1})
					err = e
					require.Nil(t, page)
				case "hub":
					err = db.Transaction(func(tx *gorm.DB) error {
						// Exercise a real SQL error in the membership subquery.
						if table == "c_collab_channel_members" {
							if e := tx.Exec("DROP TABLE c_collab_channel_members").Error; e != nil {
								return e
							}
						}
						_, e := m.hubOn(ctx, tx, 1, ConversationHubInput{Limit: 1})
						return e
					})
				}
				require.Error(t, err)
				require.NoError(t, db.Callback().Create().Remove("fixture:fail"))
				require.NoError(t, db.Callback().Update().Remove("fixture:fail"))
				if operation == "read" || operation == "hub" {
					require.NoError(t, db.Callback().Query().Remove("fixture:fail"))
				}
				for name, want := range before {
					var n int64
					require.NoError(t, db.Table(name).Count(&n).Error)
					require.Equal(t, want, n, name)
				}
				if operation == "send" {
					var row model.Channel
					require.NoError(t, db.Table("c_collab_channels").Where("id=?", channel.ID).Take(&row).Error)
					require.Zero(t, row.LastSeq)
				}
				if operation == "mark" {
					var member model.ChannelMember
					require.NoError(t, db.Table("c_collab_channel_members").Where("channel_id=? AND principal_id=2", channel.ID).Take(&member).Error)
					require.Zero(t, member.LastReadSeq)
				}
			})
		}
	}
}

func TestDirectRejectsInvalidCurrentStateWithoutInventingHistory(t *testing.T) {
	m, db := conversationHarness(t)
	ctx := conversationContext(t, 2)
	for _, ids := range []guid.Source{nil, conversationIDs(func() (int64, error) { return 0, errors.New("allocator offline") }), conversationIDs(func() (int64, error) { return 0, nil })} {
		original := m.ids
		m.ids = ids
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createDirectOn(ctx, tx, 1, 4); return e }))
		m.ids = original
	}
	var channel DirectChannelResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; channel, e = m.createDirectOn(ctx, tx, 1, 4); return e }))
	for _, body := range []string{" ", string([]byte{255}), strings.Repeat("x", 8001)} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.postDirectOn(ctx, tx, 1, channel.ID, DirectTextInput{Body: body})
			return e
		}), ErrInvalidArgument)
	}
	for _, ids := range []guid.Source{nil, conversationIDs(func() (int64, error) { return 0, errors.New("allocator offline") }), conversationIDs(func() (int64, error) { return 0, nil })} {
		original := m.ids
		m.ids = ids
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.postDirectOn(ctx, tx, 1, channel.ID, DirectTextInput{Body: "Must not commit"})
			return e
		}))
		m.ids = original
	}
	require.NoError(t, db.Table("c_collab_channels").Where("id=?", channel.ID).Update("archived_at", m.now()).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postDirectOn(ctx, tx, 1, channel.ID, DirectTextInput{Body: "No archived send"})
		return e
	}), ErrForbidden)
	_, err := m.readDirectHTTP(ctx, &DirectReadInput{ConversationChannelInput: ConversationChannelInput{ChannelID: channel.ID.String()}, Limit: 1})
	require.NoError(t, err, "archived history remains readable")
	for _, in := range []ConversationHubInput{{Limit: 0}, {Limit: 101}, {Limit: 1, Search: strings.Repeat("x", 201)}, {Limit: 1, Cursor: "invalid"}} {
		err = db.Transaction(func(tx *gorm.DB) error { _, e := m.hubOn(ctx, tx, 1, in); return e })
		require.Error(t, err)
	}
	err = db.Transaction(func(tx *gorm.DB) error { _, e := m.hubOn(ctx, tx, 2, ConversationHubInput{Limit: 1}); return e })
	require.ErrorIs(t, err, ErrForbidden)
	_, err = m.readDirectHTTP(ctx, &DirectReadInput{ConversationChannelInput: ConversationChannelInput{ChannelID: "invalid"}, Limit: 1})
	require.Error(t, err)
	_, err = m.directAccessOn(ctx, nil, 1, channel.ID, false)
	require.Error(t, err)
	err = db.Transaction(func(tx *gorm.DB) error { _, e := m.directAccessOn(ctx, tx, 2, channel.ID, false); return e })
	require.ErrorIs(t, err, ErrForbidden)
	var count int64
	require.NoError(t, db.Table("c_collab_messages").Count(&count).Error)
	require.Zero(t, count)
}

func TestHubPaginationDoesNotDropConversationsOrReuseForeignCursor(t *testing.T) {
	m, db := conversationHarness(t)
	ctx := conversationContext(t, 2)
	var ids []guid.ID
	for _, peer := range []guid.ID{4, 5} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			v, e := m.createDirectOn(ctx, tx, 1, peer)
			ids = append(ids, v.ID)
			return e
		}))
	}
	var first CursorPage[ChannelView]
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		first, e = m.hubOn(ctx, tx, 1, ConversationHubInput{Limit: 1})
		return e
	}))
	require.True(t, first.HasMore)
	require.Equal(t, ids[0], first.Items[0].ID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		second, e := m.hubOn(ctx, tx, 1, ConversationHubInput{Limit: 1, Cursor: first.NextCursor})
		require.NoError(t, e)
		require.False(t, second.HasMore)
		require.Equal(t, ids[1], second.Items[0].ID)
		return e
	}))
	for _, in := range []ConversationHubInput{{Limit: 1, Search: "changed", Cursor: first.NextCursor}, {Limit: 1, Cursor: first.NextCursor}} {
		actor := guid.ID(2)
		if in.Search == "" {
			actor = 4
		}
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.hubOn(conversationContext(t, actor), tx, 1, in); return e }))
	}
	original := m.principals.LockActiveRow
	m.principals.LockActiveRow = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.hubOn(ctx, tx, 1, ConversationHubInput{Limit: 1}); return e }), ErrUnavailable)
	m.principals.LockActiveRow = original
}

func TestHumanDirectFixedParticipantsHistoryAndAtomicWrites(t *testing.T) {
	m, db := conversationHarness(t)
	ctx := conversationContext(t, 2)
	var direct DirectChannelResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; direct, e = m.createDirectOn(ctx, tx, 1, 4); return e }))
	require.True(t, direct.Created)
	require.Nil(t, direct.ProjectID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		again, e := m.createDirectOn(conversationContext(t, 4), tx, 1, 2)
		require.Equal(t, direct.ID, again.ID)
		require.False(t, again.Created)
		return e
	}))
	var sent MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		sent, e = m.postDirectOn(ctx, tx, 1, direct.ID, DirectTextInput{Body: "Private source <script>inert</script>"})
		return e
	}))
	require.EqualValues(t, 1, sent.Seq)
	require.Equal(t, "CHANNEL", sent.Visibility)
	page, e := m.readDirectHTTP(conversationContext(t, 4), &DirectReadInput{ConversationChannelInput: ConversationChannelInput{ChannelID: direct.ID.String()}, Limit: 1})
	require.NoError(t, e)
	require.Equal(t, sent.ID, page.Body.Data.Items[0].ID)
	_, e = m.readDirectHTTP(conversationContext(t, 5), &DirectReadInput{ConversationChannelInput: ConversationChannelInput{ChannelID: direct.ID.String()}, Limit: 1})
	require.Error(t, e, "tenant owner/another member has no private read")
	beforeAudit := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit failed") }
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postDirectOn(ctx, tx, 1, direct.ID, DirectTextInput{Body: "Must roll back"})
		return e
	}))
	count, e := query.BindMessages(db, m.names).WithContext(ctx).Where(query.BindMessages(db, m.names).ChannelID.Eq(direct.ID)).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, count)
	m.audit = beforeAudit
	original := m.principals.LockActiveRow
	m.principals.LockActiveRow = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		p, active, e := original(ctx, tx, id)
		return p, active && id != 4, e
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postDirectOn(ctx, tx, 1, direct.ID, DirectTextInput{Body: "Inactive peer"})
		return e
	}))
	page, e = m.readDirectHTTP(ctx, &DirectReadInput{ConversationChannelInput: ConversationChannelInput{ChannelID: direct.ID.String()}, Limit: 1})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1, "peer departure must preserve participant history")
	for _, peer := range []guid.ID{0, 2, 4, 8} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createDirectOn(ctx, tx, 1, peer); return e }))
	}
	m.principals.LockActiveRow = original
	m.principals.HumanByLogin = func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createDirectOn(ctx, tx, 1, 5); return e }), "service-account identities must not enter Human direct creation")
}

func TestConversationHubChecksCurrentProjectPermissionBeforePageBoundary(t *testing.T) {
	m, db := conversationHarness(t)
	ctx := conversationContext(t, 2)
	project := createConversation(t, m, db, "INTERNAL")
	var direct DirectChannelResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; direct, e = m.createDirectOn(ctx, tx, 1, 4); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postDirectOn(conversationContext(t, 4), tx, 1, direct.ID, DirectTextInput{Body: "Actual unread"})
		return e
	}))
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.hubOn(ctx, tx, 1, ConversationHubInput{Limit: 1})
		require.NoError(t, e)
		require.Len(t, page.Items, 1)
		require.Equal(t, direct.ID, page.Items[0].ID)
		require.NotEqual(t, project.ID, page.Items[0].ID)
		require.EqualValues(t, 1, page.Items[0].Unread)
		require.False(t, page.HasMore)
		return nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.hubOn(conversationContext(t, 5), tx, 1, ConversationHubInput{Limit: 1})
		require.NoError(t, e)
		require.Empty(t, page.Items)
		return nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.hubOn(ctx, tx, 1, ConversationHubInput{Limit: 1, Search: "missing"})
		require.NoError(t, e)
		require.Empty(t, page.Items)
		return nil
	}))
}

func TestAgentDirectReaderExcludesHumanAndProjectChannels(t *testing.T) {
	m, db := conversationHarness(t)
	now := m.now()
	for _, row := range []model.Channel{
		{ID: 20, TenantID: 1, Kind: "DIRECT", Visibility: "INTERNAL", Name: "Agent direct", Version: 1, CreatedAt: now, UpdatedAt: now},
		{ID: 21, TenantID: 1, Kind: "DIRECT", Visibility: "INTERNAL", Name: "Human direct", Version: 1, CreatedAt: now, UpdatedAt: now},
		{ID: 22, TenantID: 2, Kind: "DIRECT", Visibility: "INTERNAL", Name: "Other tenant", Version: 1, CreatedAt: now, UpdatedAt: now},
	} {
		require.NoError(t, db.Table("c_collab_channels").Create(&row).Error)
	}
	for _, member := range []model.ChannelMember{
		{ChannelID: 20, TenantID: 1, PrincipalID: 7, MemberKind: "AGENT", JoinedAt: now, CreatedAt: now, UpdatedAt: now},
		{ChannelID: 21, TenantID: 1, PrincipalID: 7, MemberKind: "HUMAN", JoinedAt: now, CreatedAt: now, UpdatedAt: now},
		{ChannelID: 22, TenantID: 2, PrincipalID: 7, MemberKind: "AGENT", JoinedAt: now, CreatedAt: now, UpdatedAt: now},
	} {
		member.Role = "MEMBER"
		member.Source = "DERIVED"
		require.NoError(t, db.Table("c_collab_channel_members").Create(&member).Error)
	}
	for _, message := range []model.Message{
		{ID: 30, TenantID: 1, ChannelID: 20, Seq: 1, AuthorID: 7, Body: "First", Visibility: "CHANNEL", Version: 1, CreatedAt: now, UpdatedAt: now},
		{ID: 31, TenantID: 1, ChannelID: 20, Seq: 2, AuthorID: 2, Body: "Second", Visibility: "CHANNEL", Version: 1, CreatedAt: now, UpdatedAt: now},
		{ID: 32, TenantID: 1, ChannelID: 20, Seq: 3, AuthorID: 7, Body: "Withdrawn", Visibility: "CHANNEL", WithdrawnAt: &now, Version: 1, CreatedAt: now, UpdatedAt: now},
	} {
		require.NoError(t, db.Table("c_collab_messages").Create(&message).Error)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		items, e := m.agentDirectChannelsOn(t.Context(), tx, 1, 7, 0, 10)
		require.NoError(t, e)
		require.Len(t, items, 1)
		require.Equal(t, guid.ID(20), items[0].ID)
		messages, e := m.agentDirectMessagesOn(t.Context(), tx, 1, 7, 20, 0, 10)
		require.NoError(t, e)
		require.Len(t, messages, 2)
		require.Equal(t, "Second", messages[0].Body)
		messages, e = m.agentDirectMessagesOn(t.Context(), tx, 1, 7, 20, 31, 10)
		require.NoError(t, e)
		require.Len(t, messages, 1)
		require.Equal(t, "First", messages[0].Body)
		_, e = m.agentDirectMessagesOn(t.Context(), tx, 1, 7, 21, 0, 10)
		require.ErrorIs(t, e, ErrNotFound)
		_, e = m.agentDirectMessagesOn(t.Context(), tx, 1, 7, 22, 0, 10)
		require.ErrorIs(t, e, ErrNotFound)
		return nil
	}))
	require.NoError(t, db.Table("c_collab_channel_members").Where("channel_id=20").Update("left_at", now).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		items, e := m.agentDirectChannelsOn(t.Context(), tx, 1, 7, 0, 10)
		require.NoError(t, e)
		require.Empty(t, items)
		_, e = m.agentDirectMessagesOn(t.Context(), tx, 1, 7, 20, 0, 10)
		require.ErrorIs(t, e, ErrNotFound)
		return nil
	}))
}

func TestClientMembershipProjectionAndRevocation(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
		return e
	}))
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: 4}, true, nil
	}
	a := clientAccess{m}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		ok, e := a.IsClientOn(t.Context(), tx, 1, 3, 4)
		require.NoError(t, e)
		require.True(t, ok)
		ids, e := a.ProjectsOn(t.Context(), tx, 1, 4, 0, 20)
		require.NoError(t, e)
		require.Equal(t, []guid.ID{3}, ids)
		ids, e = a.ProjectsOn(t.Context(), tx, 1, 4, 3, 20)
		require.NoError(t, e)
		require.Empty(t, ids)
		_, ok, e = a.ChannelOn(t.Context(), tx, ch.ID, 4)
		require.NoError(t, e)
		require.True(t, ok)
		return nil
	}))
	require.NoError(t, db.Table("c_collab_channel_members").Where("principal_id=4").Update("left_at", m.now()).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		external, e := a.IsExternalOn(t.Context(), tx, 1, 4)
		require.NoError(t, e)
		require.True(t, external)
		ok, e := a.IsClientOn(t.Context(), tx, 1, 3, 4)
		require.NoError(t, e)
		require.False(t, ok)
		ids, e := a.ProjectsOn(t.Context(), tx, 1, 4, 0, 20)
		require.NoError(t, e)
		require.Empty(t, ids)
		_, ok, e = a.ChannelOn(t.Context(), tx, ch.ID, 4)
		require.NoError(t, e)
		require.False(t, ok)
		return nil
	}))
}

func TestClientAccessAppliesCurrentResourceAndOwnerGrants(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
		return err
	}))
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: 4}, true, nil
	}
	a := clientAccess{m}
	for _, tc := range []struct {
		name       string
		constraint authz.DataConstraint
		allowed    bool
	}{
		{"resource grant", authz.DataConstraint{ResourceIDs: []string{"100", "3"}}, true},
		{"owner grant", authz.DataConstraint{OwnerIDs: []string{"100", "4"}}, true},
		{"foreign owner", authz.DataConstraint{OwnerIDs: []string{"100"}}, false},
		{"explicit denial", authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"3"}}, false},
		{"foreign resource", authz.DataConstraint{ResourceIDs: []string{"100"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return tc.constraint, nil
			}
			ok, err := a.IsClientOn(t.Context(), db, 1, 3, 4)
			require.NoError(t, err)
			require.Equal(t, tc.allowed, ok)
		})
	}
	failure := errors.New("current client policy unavailable")
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, failure
	}
	ok, err := a.IsClientOn(t.Context(), db, 1, 3, 4)
	require.ErrorIs(t, err, failure)
	require.False(t, ok)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, failure
	}
	ok, err = a.IsClientOn(t.Context(), db, 1, 3, 4)
	require.ErrorIs(t, err, failure)
	require.False(t, ok)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: 5}, true, nil
	}
	ok, err = a.IsClientOn(t.Context(), db, 1, 3, 4)
	require.NoError(t, err)
	require.False(t, ok)
	m.policy = core.EntityConstraintOn(nil)
	ok, err = a.IsClientOn(t.Context(), db, 1, 3, 4)
	require.ErrorIs(t, err, ErrUnavailable)
	require.False(t, ok)
	for _, limit := range []int{0, 102} {
		ids, err := a.ProjectsOn(t.Context(), db, 1, 4, 0, limit)
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.Nil(t, ids)
	}
	ids, err := a.ProjectsOn(t.Context(), nil, 1, 4, 0, 10)
	require.ErrorIs(t, err, ErrUnavailable)
	require.Nil(t, ids)
}
