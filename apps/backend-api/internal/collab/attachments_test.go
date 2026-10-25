package collab

import (
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAttachmentPinsAndCurrentMessageVisibilityAuthorizeStreams(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
		return e
	}))
	m.fileResources = files.ConversationResources{
		InspectOn: func(ctx context.Context, _ *gorm.DB, file guid.ID) (files.PinnedCandidate, error) {
			claims, _ := authn.FromContext(ctx)
			require.Equal(t, guid.ID(3), claims.EntityID)
			return files.PinnedCandidate{PinnedResource: files.PinnedResource{SpaceID: 11, FileID: file, VersionID: 13, Name: "report.md", MIME: "text/markdown", Size: 6}, NodeVersion: 1}, nil
		},
		PinOn: func(ctx context.Context, tx *gorm.DB, in files.ResourceBindingInput) (files.PinnedResource, error) {
			require.NoError(t, m.authorizeAttachmentResourceOn(ctx, tx, in.ResourceID, files.ResourceOwner{Scope: "entity", ID: 3}))
			return files.PinnedResource{BindingID: in.ID, FileID: in.FileID, VersionID: *in.VersionID}, nil
		},
		OpenOn: func(_ context.Context, _ *gorm.DB, id, message guid.ID) (files.PinnedResourceRead, error) {
			return files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: id, ResourceID: message, VersionID: 13, Name: "report.md", MIME: "text/markdown", Size: 6}, Reader: io.NopCloser(strings.NewReader("pinned"))}, nil
		},
	}
	candidate, e := m.attachmentCandidateHTTP(conversationContext(t, 2), &AttachmentCandidateInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, FileID: "12"})
	require.NoError(t, e)
	require.Equal(t, guid.ID(13), candidate.Body.Data.VersionID)
	message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "report", Visibility: "CHANNEL", Attachments: []AttachmentInput{candidate.Body.Data.AttachmentInput}})
	require.Len(t, message.Attachments, 1)
	page, e := func() (CursorPage[MessageView], error) {
		var page CursorPage[MessageView]
		e := db.Transaction(func(tx *gorm.DB) error {
			var err error
			page, err = m.listMessagesOn(conversationContext(t, 4), tx, 1, 3, ch.ID, MessageFilter{Limit: 50, Activity: "all"})
			return err
		})
		return page, e
	}()
	require.NoError(t, e)
	require.Equal(t, message.Attachments, page.Items[0].Attachments)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	path := "/channels/" + ch.ID.String() + "/messages/" + message.ID.String() + "/attachments/" + message.Attachments[0].ID.String() + "/content"
	read := func(actor guid.ID) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil).WithContext(conversationContext(t, actor)))
		return r
	}
	response := read(4)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "pinned", response.Body.String())
	require.Contains(t, response.Header().Get("Content-Disposition"), "report.md")
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	private := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "secret", Visibility: "INTERNAL", Attachments: []AttachmentInput{candidate.Body.Data.AttachmentInput}})
	path = "/channels/" + ch.ID.String() + "/messages/" + private.ID.String() + "/attachments/" + private.Attachments[0].ID.String() + "/content"
	require.Equal(t, 404, read(4).Code)
	require.NoError(t, db.Table("c_collab_channel_members").Where("channel_id=? AND principal_id=4", ch.ID).Update("left_at", m.now()).Error)
	require.Equal(t, 404, read(4).Code)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, m.authorizeAttachmentResourceOn(conversationContext(t, 2), tx, message.ID, files.ResourceOwner{Scope: "account", ID: 2}), ErrForbidden)
		require.Error(t, m.authorizeAttachmentResourceOn(conversationContext(t, 2), tx, 999, files.ResourceOwner{Scope: "entity", ID: 3}))
		return nil
	}))
	require.Equal(t, t.Context(), fileContext(t.Context(), 3))
}
func TestAttachmentRouteErrorsAndProviderRollback(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	post := PostMessageInput{Body: "report", Visibility: "CHANNEL", Attachments: []AttachmentInput{{SpaceID: 11, FileID: 12, VersionID: 13, ExpectedFileVersion: 1}}}
	boom := errors.New("storage unavailable")
	m.fileResources.PinOn = func(context.Context, *gorm.DB, files.ResourceBindingInput) (files.PinnedResource, error) {
		return files.PinnedResource{}, boom
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, post)
		return e
	}), boom)
	var count int64
	require.NoError(t, db.Table("c_collab_messages").Count(&count).Error)
	require.Zero(t, count)
	ctx := conversationContext(t, 2)
	for _, in := range []*AttachmentCandidateInput{{ConversationChannelInput: ConversationChannelInput{ChannelID: "bad"}, FileID: "12"}, {ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, FileID: "bad"}, {ConversationChannelInput: ConversationChannelInput{ChannelID: "999"}, FileID: "12"}, {ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, FileID: "12"}} {
		_, e := m.attachmentCandidateHTTP(ctx, in)
		require.Error(t, e)
	}
	m.fileResources.InspectOn = func(context.Context, *gorm.DB, guid.ID) (files.PinnedCandidate, error) {
		return files.PinnedCandidate{}, boom
	}
	_, e := m.attachmentCandidateHTTP(ctx, &AttachmentCandidateInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, FileID: "12"})
	require.Error(t, e)
	m.fileResources.PinOn = nil
	message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "plain", Visibility: "CHANNEL"})
	for _, in := range []*AttachmentPath{{ConversationChannelInput: ConversationChannelInput{ChannelID: "bad"}, MessageID: "1", AttachmentID: "1"}, {ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, MessageID: "bad", AttachmentID: "1"}, {ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, MessageID: message.ID.String(), AttachmentID: "bad"}, {ConversationChannelInput: ConversationChannelInput{ChannelID: "999"}, MessageID: message.ID.String(), AttachmentID: "1"}, {ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, MessageID: message.ID.String(), AttachmentID: "999"}} {
		_, e := m.attachmentHTTP(ctx, in)
		require.Error(t, e)
	}

	// An existing attachment must fail closed if its streaming capability disappears.
	require.NoError(t, db.Exec("INSERT INTO c_collab_message_attachments(id,tenant_id,channel_id,message_id,file_id,version_id) VALUES(88,1,?,?,12,13)", ch.ID, message.ID).Error)
	valid := &AttachmentPath{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, MessageID: message.ID.String(), AttachmentID: "88"}
	_, e = m.attachmentHTTP(ctx, valid)
	require.Error(t, e)
	m.fileResources.OpenOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (files.PinnedResourceRead, error) {
		return files.PinnedResourceRead{}, boom
	}
	_, e = m.attachmentHTTP(ctx, valid)
	require.Error(t, e)
	m.runtime = nil
	_, e = m.attachmentCandidateHTTP(ctx, &AttachmentCandidateInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, FileID: "12"})
	require.Error(t, e)
}

func TestAttachmentProviderClassifications(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	for _, tc := range []struct {
		err    error
		status int
	}{{files.ErrPinnedForbidden, 403}, {files.ErrPinnedConflict, 409}, {files.ErrPinnedInvalid, 422}, {files.ErrPinnedUnavailable, 503}, {files.ErrNotFound, 404}, {gorm.ErrRecordNotFound, 404}} {
		m.fileResources.InspectOn = func(context.Context, *gorm.DB, guid.ID) (files.PinnedCandidate, error) {
			return files.PinnedCandidate{}, tc.err
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/channels/"+ch.ID.String()+"/attachment-candidates/12", nil).WithContext(conversationContext(t, 2)))
		require.Equal(t, tc.status, response.Code, response.Body.String())
	}
}
