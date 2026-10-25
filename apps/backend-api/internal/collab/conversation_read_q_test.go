package collab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type sharedClientAuthority struct{ contractAuthority }

func (*sharedClientAuthority) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{PrincipalID: 4, TenantID: 1, SubjectType: authn.SubjectTypePrincipal}, nil
}

func TestSharedClientReadsWithoutInternalChannelPermission(t *testing.T) {
	m, db := conversationHarness(t)
	shared := createConversation(t, m, db, "CLIENT_SHARED")
	internal := createConversation(t, m, db, "INTERNAL")
	postConversation(t, m, db, shared.ID, PostMessageInput{Body: "client visible", Visibility: "CHANNEL"})
	postConversation(t, m, db, shared.ID, PostMessageInput{Body: "internal secret", Visibility: "INTERNAL"})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, shared.ID, 4, MemberInput{External: true})
		return err
	}))
	policy := m.policy
	m.policy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if actor == 4 {
			return authz.DataConstraint{}, nil
		}
		return policy(ctx, tx, tenant, project, permission, actor)
	}
	// The real registrar denies ordinary channel_read. Only membership-scoped
	// reads can reach their source check; no administrative permissions change.
	authority := &sharedClientAuthority{}
	m.registrar = authz.NewRegistrar(m.registrar.Registry(), authority, authority, authority)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	read := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer fixture")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, r)
		return out
	}
	channels := read("/entities/3/channels")
	require.Equal(t, 200, channels.Code, channels.Body.String())
	require.Contains(t, channels.Body.String(), shared.ID.String())
	require.NotContains(t, channels.Body.String(), internal.ID.String())
	path := "/channels/" + shared.ID.String() + "/messages"
	messages := read(path)
	require.Equal(t, 200, messages.Code, messages.Body.String())
	require.Contains(t, messages.Body.String(), "client visible")
	require.NotContains(t, messages.Body.String(), "internal secret")
	require.Equal(t, 404, read("/channels/"+internal.ID.String()+"/messages").Code)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, err := m.listChannelsOn(conversationContext(t, 4), tx, 1, 3, nextCursor(int64(shared.ID), cursorScope("channels", "1", "3", "4")), 1)
		require.NoError(t, err, "cursor exhaustion does not revoke the explicit shared membership")
		require.Empty(t, page.Items)
		return err
	}))
	require.NoError(t, db.Table("c_collab_channel_members").Where("channel_id = ? AND principal_id = ?", shared.ID, 4).Update("left_at", m.now()).Error)
	require.Equal(t, 404, read(path).Code)
	require.NotContains(t, read("/entities/3/channels").Body.String(), shared.ID.String())
}
