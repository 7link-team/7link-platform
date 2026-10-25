package collab

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestMessageMutationHTTPPreconditionsReplayAndRedaction(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	declaration := humachi.New(chi.NewRouter(), huma.DefaultConfig("declarations", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), declaration))
	authority := &directAuthority{contractAuthority{allow: true}}
	m.registrar = authz.NewRegistrar(m.registrar.Registry(), authority, authority, authority)
	api := humachi.New(chi.NewRouter(), huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	h := humatest.Wrap(t, api)
	const auth = "Authorization: Bearer fixture"
	postPath := "/channels/" + ch.ID.String() + "/messages"
	sent := h.Post(postPath, auth, "Idempotency-Key: post-text", map[string]any{"body": "original secret", "visibility": "CHANNEL"})
	require.Equal(t, 201, sent.Code, sent.Body.String())
	var sentBody struct {
		Data MessageView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(sent.Body.Bytes(), &sentBody))
	path := "/tenants/1/messages/" + sentBody.Data.ID.String()
	body := map[string]any{"body": "edited secret"}
	require.Equal(t, 428, h.Patch(path, auth, body).Code)
	require.Equal(t, 409, h.Patch(path, auth, `If-Match: "99"`, body).Code)
	edited := h.Patch(path, auth, `If-Match: "1"`, "Idempotency-Key: edit-text", body)
	require.Equal(t, http.StatusOK, edited.Code, edited.Body.String())
	again := h.Patch(path, auth, `If-Match: "1"`, "Idempotency-Key: edit-text", body)
	require.Equal(t, 200, again.Code, again.Body.String())
	require.Equal(t, "true", again.Header().Get("Idempotent-Replayed"))
	removed := h.Delete(path, auth, `If-Match: "2"`, "Idempotency-Key: retract-text")
	require.Equal(t, 204, removed.Code, removed.Body.String())
	again = h.Patch(path, auth, `If-Match: "1"`, "Idempotency-Key: edit-text", body)
	require.Equal(t, 200, again.Code, again.Body.String())
	require.Contains(t, again.Body.String(), `"body":null`)
	require.NotContains(t, again.Body.String(), "edited secret")
	replayPost := h.Post(postPath, auth, "Idempotency-Key: post-text", map[string]any{"body": "original secret", "visibility": "CHANNEL"})
	require.Equal(t, 201, replayPost.Code, replayPost.Body.String())
	require.NotContains(t, replayPost.Body.String(), "original secret")
	require.Contains(t, replayPost.Body.String(), `"body":null`)
	history := h.Get(path+"/revisions", auth)
	require.Equal(t, 200, history.Code, history.Body.String())
	require.Contains(t, history.Body.String(), "original secret")
	require.Contains(t, history.Body.String(), "edited secret")
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Equal(t, 403, h.Patch(path, auth, `If-Match: "1"`, "Idempotency-Key: edit-text", body).Code)
}
