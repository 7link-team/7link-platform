package collab

import (
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHubActivityDeniesRevokedAuthorityAndDoesNotExposeHiddenParents(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
		return err
	}))
	root := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "root", Visibility: "CHANNEL"})
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "reply mention", Visibility: "CHANNEL", ReplyToMessageID: &root.ID, MentionPrincipalIDs: []guid.ID{4}})
	read := func() (CursorPage[HubActivity], error) {
		var out CursorPage[HubActivity]
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.hubActivityOn(conversationContext(t, 4), tx, 1, HubActivityInput{Kind: "MENTIONS", Limit: 50})
			return err
		})
		return out, err
	}
	page, err := read()
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.NoError(t, db.Table(m.names.Messages.String()).Where("id = ?", root.ID).Update("withdrawn_at", m.now()).Error)
	page, err = read()
	require.NoError(t, err)
	require.Empty(t, page.Items)
	policy := m.policy
	m.policy = core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	})
	page, err = read()
	require.NoError(t, err)
	require.Empty(t, page.Items)
	fault := errors.New("current authority unavailable")
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, fault
	}
	_, err = read()
	require.ErrorIs(t, err, fault)
	m.policy = policy
	for _, in := range []HubActivityInput{{Kind: "INVALID", Limit: 50}, {Kind: "MENTIONS", Limit: 0}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.hubActivityOn(conversationContext(t, 4), tx, 1, in); return err }), ErrInvalidArgument)
	}
	m.members = nil
	_, err = read()
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestHubActivityCurrentMembershipVisibilityAndScopedPagination(t *testing.T) {
	m, db := conversationHarness(t)
	shared := createConversation(t, m, db, "CLIENT_SHARED")
	internal := createConversation(t, m, db, "INTERNAL")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, shared.ID, 4, MemberInput{External: true})
		return e
	}))
	first := postConversation(t, m, db, shared.ID, PostMessageInput{Body: "Visible mention one", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{4}})
	second := postConversation(t, m, db, shared.ID, PostMessageInput{Body: "Visible mention two", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{4}})
	postConversation(t, m, db, internal.ID, PostMessageInput{Body: "Not a member", Visibility: "CHANNEL"})
	read := func(actor guid.ID, kind, cursor string) (CursorPage[HubActivity], error) {
		var out CursorPage[HubActivity]
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.hubActivityOn(conversationContext(t, actor), tx, 1, HubActivityInput{Kind: kind, Cursor: cursor, Limit: 1})
			return e
		})
		return out, err
	}
	page, e := read(4, "MENTIONS", "")
	require.NoError(t, e)
	require.Len(t, page.Items, 1)
	require.Equal(t, second.ID, page.Items[0].Message.ID)
	require.True(t, page.HasMore)
	next, e := read(4, "MENTIONS", page.NextCursor)
	require.NoError(t, e)
	require.Equal(t, first.ID, next.Items[0].Message.ID)
	require.False(t, next.HasMore)
	_, e = read(2, "MENTIONS", page.NextCursor)
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = read(4, "REPLIES", page.NextCursor)
	require.ErrorIs(t, e, ErrInvalidArgument)
	var own MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		own, e = m.postMessageOn(conversationContext(t, 4), tx, 1, 3, shared.ID, PostMessageInput{Body: "Client question", Visibility: "CHANNEL"})
		return e
	}))
	reply := postConversation(t, m, db, shared.ID, PostMessageInput{Body: "Actual reply", Visibility: "CHANNEL", ReplyToMessageID: &own.ID})
	page, e = read(4, "REPLIES", "")
	require.NoError(t, e)
	require.Equal(t, reply.ID, page.Items[0].Message.ID)
	// Withdrawal removes the collection entry without changing the parent or its history.
	q := query.BindMessages(db, m.names)
	_, e = q.WithContext(t.Context()).Where(q.ID.Eq(reply.ID)).Update(q.WithdrawnAt, m.now())
	require.NoError(t, e)
	page, e = read(4, "REPLIES", "")
	require.NoError(t, e)
	require.Empty(t, page.Items)
	member := query.BindMembers(db, m.names)
	_, e = member.WithContext(t.Context()).Where(member.TenantID.Eq(guid.ID(1)), member.ChannelID.Eq(shared.ID), member.PrincipalID.Eq(guid.ID(4))).Update(member.LeftAt, m.now())
	require.NoError(t, e)
	page, e = read(4, "MENTIONS", "")
	require.NoError(t, e)
	require.Empty(t, page.Items)
}

func TestHubActivityHTTPUsesCurrentMembershipAndHidesStorageDetails(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "mention", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{2}})
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("activity", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	read := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tenants/1"+path, nil).WithContext(conversationContext(t, 2)))
		return rec
	}
	response := read("/conversations/activity?kind=MENTIONS")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"items":[]`, "own messages do not mention the author in the activity feed")
	response = read("/conversations/activity?kind=REPLIES&cursor=invalid")
	require.Equal(t, 422, response.Code, response.Body.String())
	fault := errors.New("private activity storage")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:activity", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Messages.String() {
			tx.AddError(fault)
		}
	}))
	response = read("/conversations/activity")
	require.Equal(t, 500, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), fault.Error())
	require.NoError(t, db.Callback().Query().Remove("test:activity"))
	m.runtime = nil
	response = read("/conversations/activity")
	require.Equal(t, 503, response.Code, response.Body.String())
}
