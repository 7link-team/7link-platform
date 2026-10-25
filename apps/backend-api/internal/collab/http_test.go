package collab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type contractAuthority struct{ allow bool }

func (a *contractAuthority) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{PrincipalID: 2, TenantID: 1, SubjectType: authn.SubjectTypePrincipal}, nil
}
func (a *contractAuthority) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return a.allow, nil
}
func (a *contractAuthority) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return a.allow, nil
}
func (a *contractAuthority) CheckEntity(context.Context, guid.ID, guid.ID, string, guid.ID) (bool, error) {
	return a.allow, nil
}
func (a *contractAuthority) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

func TestConversionContractUsesProductionRegistrationAndCurrentAuthority(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	// Declare once, then mount using a real registrar, as app composition does.
	declarations := humachi.New(chi.NewRouter(), huma.DefaultConfig("declarations", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), declarations))
	authority := &contractAuthority{allow: true}
	m.registrar = authz.NewRegistrar(m.registrar.Registry(), authority, authority, authority)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	second := humachi.New(chi.NewRouter(), huma.DefaultConfig("inventory", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), second))
	read := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodOptions, "/channels/"+ch.ID.String()+"/messages/41/work-items?method=POST", nil)
		request.Header.Set("Authorization", "Bearer fixture")
		router.ServeHTTP(response, request)
		return response
	}
	response := read()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"operation_id":"channel.message_convert"`)
	require.Contains(t, response.Body.String(), `"accountable_principal_id"`)
	authority.allow = false
	require.Equal(t, http.StatusForbidden, read().Code)
}

func TestConversationErrorsHideProviderDetailsAndUnavailableRoutes(t *testing.T) {
	m, _ := conversationHarness(t)
	ctx := conversationContext(t, 2)
	for _, tc := range []struct {
		err    error
		status int
	}{{ErrForbidden, 403}, {ErrNotFound, 404}, {idempotency.ErrInProgress, 409}, {ErrMemberInactive, 422}, {ErrUnavailable, 503}, {files.ErrPinnedForbidden, 403}, {files.ErrPinnedConflict, 409}, {files.ErrPinnedInvalid, 422}, {files.ErrPinnedUnavailable, 503}, {files.ErrNotFound, 404}, {gorm.ErrRecordNotFound, 404}, {errors.New("private SQL credentials"), 500}} {
		err := conversationHTTPError(ctx, tc.err)
		var status huma.StatusError
		require.ErrorAs(t, err, &status)
		require.Equal(t, tc.status, status.GetStatus())
		require.NotContains(t, err.Error(), "private SQL")
	}
	m.runtime = nil
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/entities/3/channels", ""},
		{http.MethodGet, "/channels/5/messages", ""},
		{http.MethodGet, "/channels/5/members", ""},
		{http.MethodPost, "/channels/5/messages", `{"body":"Message","visibility":"CHANNEL"}`},
		{http.MethodPut, "/channels/5/members/4", `{"external":false}`},
		{http.MethodPut, "/channels/5/read-marker", `{"seq":1}`},
		{http.MethodPut, "/channels/5/messages/6/saved", `{"saved":true}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, 503, rec.Code, rec.Body.String())
	}
}

func TestConversationLifecycleHTTPRejectsMalformedAndUnavailableResources(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	ctx := conversationContext(t, 2)
	for _, id := range []string{"invalid", "999"} {
		_, _, err := m.lifecycleChannelIDs(ctx, id)
		require.Error(t, err)
		out, err := m.channelDetailHTTP(ctx, &ConversationChannelInput{ChannelID: id})
		require.Error(t, err)
		require.Nil(t, out)
		_, err = m.channelUpdateHTTP(ctx, &channelUpdateHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: id}})
		require.Error(t, err)
		_, err = m.channelMemberDeleteHTTP(ctx, &channelMemberDeleteInput{ConversationChannelInput: ConversationChannelInput{ChannelID: id}, PrincipalID: "4"})
		require.Error(t, err)
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrSeenSeqRequired, 422, "145022"}, {ErrChannelImmutable, 409, "145011"}, {ErrChannelArchived, 409, "145008"}, {ErrMessageNotEditable, 409, "145006"},
	} {
		err := conversationHTTPError(ctx, tc.err)
		var status huma.StatusError
		require.ErrorAs(t, err, &status)
		require.Equal(t, tc.status, status.GetStatus())
		raw, e := json.Marshal(err)
		require.NoError(t, e)
		require.Contains(t, string(raw), tc.code)
	}
	_, err := m.channelMemberDeleteHTTP(ctx, &channelMemberDeleteInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, PrincipalID: "bad"})
	require.Error(t, err)
	m.runtime = nil
	_, _, err = m.lifecycleChannelIDs(ctx, ch.ID.String())
	require.ErrorIs(t, err, ErrUnavailable)
	_, _, err = m.lifecycleChannelOwner(ctx, ch.ID)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.channelDetailHTTP(ctx, &ConversationChannelInput{ChannelID: ch.ID.String()})
	require.Error(t, err)
	_, err = m.channelMemberDeleteHTTP(ctx, &channelMemberDeleteInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, PrincipalID: "4"})
	require.Error(t, err)
}

func TestChannelOwnershipResolversRespectDirectScopeAndStorageFailures(t *testing.T) {
	m, db := conversationHarness(t)
	ctx := conversationContext(t, 2)
	var direct DirectChannelResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; direct, err = m.createDirectOn(ctx, tx, 1, 4); return err }))
	for _, resolve := range []func(context.Context, guid.ID) (authz.ResourceOwner, bool, error){m.channelOwner, m.lifecycleChannelOwner} {
		_, found, err := resolve(ctx, direct.ID)
		require.NoError(t, err)
		require.False(t, found)
		_, found, err = resolve(ctx, 999)
		require.NoError(t, err)
		require.False(t, found)
	}
	_, _, err := m.lifecycleChannelIDs(ctx, direct.ID.String())
	require.ErrorIs(t, err, ErrChannelImmutable)
	fault := errors.New("private channel storage")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:owner", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Channels.String() {
			tx.AddError(fault)
		}
	}))
	for _, resolve := range []func(context.Context, guid.ID) (authz.ResourceOwner, bool, error){m.channelOwner, m.lifecycleChannelOwner} {
		_, found, err := resolve(ctx, direct.ID)
		require.ErrorIs(t, err, fault)
		require.False(t, found)
	}
	_, _, err = m.lifecycleChannelIDs(ctx, direct.ID.String())
	require.ErrorIs(t, err, fault)
	require.NoError(t, db.Callback().Query().Remove("test:owner"))
}

func TestConversationReadProviderFailuresReturnNoPartialContent(t *testing.T) {
	for _, table := range []string{"c_collab_channels", "c_collab_channel_members", "c_collab_messages", "c_collab_message_attachments", "c_collab_message_mentions", "c_collab_saved_messages"} {
		t.Run(table, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "INTERNAL")
			postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Confidential", Visibility: "CHANNEL"})
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:read-provider", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(errors.New("private read provider details"))
				}
			}))
			t.Cleanup(func() { _ = db.Callback().Query().Remove("test:read-provider") })
			response, err := m.listMessagesHTTP(conversationContext(t, 2), &messageListInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, Limit: 50, Activity: "all"})
			require.Nil(t, response)
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, 500, status.GetStatus())
			require.NotContains(t, err.Error(), "private read")
		})
	}
}

func TestConversationCommandBoundariesAndOptionalReplay(t *testing.T) {
	m, _ := conversationHarness(t)
	ctx := conversationContext(t, 2)
	invalid := ConversationChannelInput{ChannelID: "invalid"}
	calls := []func() error{
		func() error {
			_, e := m.listChannelsHTTP(ctx, &channelListInput{ConversationEntityInput: ConversationEntityInput{EntityID: "bad"}})
			return e
		},
		func() error {
			_, e := m.createChannelHTTP(ctx, &channelCreateInput{ConversationEntityInput: ConversationEntityInput{EntityID: "bad"}})
			return e
		},
		func() error {
			_, e := m.listMessagesHTTP(ctx, &messageListInput{ConversationChannelInput: invalid})
			return e
		},
		func() error {
			_, e := m.postMessageHTTP(ctx, &messagePostInput{ConversationChannelInput: invalid})
			return e
		},
		func() error {
			_, e := m.putMemberHTTP(ctx, &memberPutInput{ConversationChannelInput: invalid})
			return e
		},
		func() error {
			_, e := m.putMemberHTTP(ctx, &memberPutInput{ConversationChannelInput: ConversationChannelInput{ChannelID: "5"}, PrincipalID: "bad"})
			return e
		},
		func() error { _, e := m.markReadHTTP(ctx, &readInput{ConversationChannelInput: invalid}); return e },
		func() error { _, e := m.saveMessageHTTP(ctx, &saveInput{ConversationChannelInput: invalid}); return e },
		func() error {
			_, e := m.saveMessageHTTP(ctx, &saveInput{ConversationChannelInput: ConversationChannelInput{ChannelID: "5"}, MessageID: "bad"})
			return e
		},
		func() error {
			_, e := m.listMembersHTTP(ctx, &memberListInput{ConversationChannelInput: invalid})
			return e
		},
	}
	for _, call := range calls {
		var status huma.StatusError
		require.ErrorAs(t, call(), &status)
		require.Equal(t, 422, status.GetStatus())
	}
	created, err := m.createChannelHTTP(ctx, &channelCreateInput{ConversationEntityInput: ConversationEntityInput{EntityID: "3"}, Body: CreateChannelInput{Name: "No replay key", Visibility: "INTERNAL"}})
	require.NoError(t, err)
	channel := created.Body.Data.ID.String()
	_, err = m.postMessageHTTP(ctx, &messagePostInput{ConversationChannelInput: ConversationChannelInput{ChannelID: channel}, Body: PostMessageInput{Body: "Message without key", Visibility: "CHANNEL"}})
	require.NoError(t, err)
	_, err = m.postMessageHTTP(ctx, &messagePostInput{ConversationChannelInput: ConversationChannelInput{ChannelID: channel}, IdempotencyKey: strings.Repeat("a", 65), Body: PostMessageInput{Body: "Invalid key", Visibility: "CHANNEL"}})
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 422, status.GetStatus())
	m.replay = nil
	_, err = m.postMessageHTTP(ctx, &messagePostInput{ConversationChannelInput: ConversationChannelInput{ChannelID: channel}, Body: PostMessageInput{Body: "No provider", Visibility: "CHANNEL"}})
	require.ErrorAs(t, err, &status)
	require.Equal(t, 503, status.GetStatus())
}

func TestConversationHTTPWorkflowAndRevokedMembership(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	msg := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Budget 100%_approved", Visibility: "CHANNEL"})
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	base := "/channels/" + ch.ID.String()
	call := func(method, path, body string, actor guid.ID, status int) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(conversationContext(t, actor))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, status, rec.Code, rec.Body.String())
		return rec.Body.String()
	}
	require.Contains(t, call(http.MethodGet, "/entities/3/channels", "", 2, 200), ch.Name)
	call(http.MethodPut, base+"/members/4", `{"external":true}`, 2, 200)
	call(http.MethodGet, base+"/members?limit=1", "", 2, 200)
	members := call(http.MethodGet, base+"/members", "", 4, 200)
	require.Contains(t, members, `"external":true`)
	call(http.MethodPut, base+"/messages/"+msg.ID.String()+"/saved", `{"saved":true}`, 4, 200)
	require.Contains(t, call(http.MethodGet, base+"/messages?activity=saved", "", 4, 200), msg.Body)
	call(http.MethodPut, base+"/read-marker", `{"seq":1}`, 4, 200)
	require.NotContains(t, call(http.MethodGet, base+"/messages?activity=unread", "", 4, 200), msg.Body)
	call(http.MethodPut, base+"/messages/"+msg.ID.String()+"/saved", `{"saved":false}`, 4, 200)
	call(http.MethodGet, base+"/messages?reply_to_message_id=invalid", "", 2, 422)
	call(http.MethodGet, base+"/members?cursor=malformed", "", 2, 422)
	require.NoError(t, db.Table("c_collab_channel_members").Where("channel_id=? AND principal_id=4", ch.ID).Update("left_at", m.now()).Error)
	for _, path := range []string{base + "/messages", base + "/members"} {
		call(http.MethodGet, path, "", 4, 404)
	}
	call(http.MethodPut, base+"/read-marker", `{"seq":1}`, 4, 404)
	call(http.MethodPut, base+"/messages/"+msg.ID.String()+"/saved", `{"saved":true}`, 4, 404)
}

func TestConversationHTTPReplayRechecksAccessAndPayload(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	ctx := conversationContext(t, 2)
	in := &messagePostInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, IdempotencyKey: "post-1", Body: PostMessageInput{Body: "Hello", Visibility: "CHANNEL"}}
	first, err := m.postMessageHTTP(ctx, in)
	require.NoError(t, err)
	require.Empty(t, first.Replayed)
	second, err := m.postMessageHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", second.Replayed)
	require.Equal(t, first.Body.Data.ID, second.Body.Data.ID)
	in.Body.Body = "Changed"
	_, err = m.postMessageHTTP(ctx, in)
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 409, status.GetStatus())
	in.Body.Body = "Hello"
	require.NoError(t, db.Table("c_collab_channel_members").Where("channel_id=? AND principal_id=2", ch.ID).Update("left_at", m.now()).Error)
	_, err = m.postMessageHTTP(ctx, in)
	require.ErrorAs(t, err, &status)
	require.Equal(t, 404, status.GetStatus())
	var n int64
	require.NoError(t, db.Table("c_collab_messages").Count(&n).Error)
	require.EqualValues(t, 1, n)
}
func TestConversationHTTPRegistersAndSerializesPrivacyFilteredHistory(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
		return e
	}))
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Internal secret", Visibility: "INTERNAL"})
	public := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Welcome", Visibility: "CHANNEL"})
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	respx.Install()
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	req := httptest.NewRequest(http.MethodGet, "/channels/"+ch.ID.String()+"/messages?limit=1", nil).WithContext(conversationContext(t, 4))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "Internal secret")
	var body respx.Envelope[CursorPage[MessageView]]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data.Items, 1)
	require.Equal(t, public.ID, body.Data.Items[0].ID)
	require.False(t, body.Data.HasMore)
	bad := httptest.NewRequest(http.MethodGet, "/channels/"+ch.ID.String()+"/messages?limit=101", nil).WithContext(conversationContext(t, 4))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, bad)
	require.Equal(t, 422, rec.Code)
	owner, found, err := m.channelOwner(t.Context(), guid.ID(123))
	require.NoError(t, err)
	require.False(t, found)
	require.Zero(t, owner.TenantID)
}
func TestCreateChannelReplayReturnsOneChannelAndScopesToEntity(t *testing.T) {
	m, db := conversationHarness(t)
	ctx := conversationContext(t, 2)
	in := &channelCreateInput{ConversationEntityInput: ConversationEntityInput{EntityID: "3"}, IdempotencyKey: "create-1", Body: CreateChannelInput{Name: "New", Visibility: "INTERNAL"}}
	first, err := m.createChannelHTTP(ctx, in)
	require.NoError(t, err)
	second, err := m.createChannelHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", second.Replayed)
	require.Equal(t, first.Body.Data.ID, second.Body.Data.ID)
	in.EntityID = "5"
	_, err = m.createChannelHTTP(ctx, in)
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 409, status.GetStatus())
	var n int64
	require.NoError(t, db.Table("c_collab_channels").Count(&n).Error)
	require.EqualValues(t, 1, n)
}
