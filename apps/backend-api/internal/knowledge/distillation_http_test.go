package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDistillationHTTPRequiresAcceptedWorkReturns201AndRechecksReplayAuthority(t *testing.T) {
	m, _, launched, _ := distillationHarness(t)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	call := func(method, path, key string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(`{"agent_id":"7"}`)).WithContext(knowledgeContext(t, 2))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	invalid := call(http.MethodPost, "/tenants/1/work-items/99/skill-distillations", "not-accepted")
	require.Equal(t, 422, invalid.Code, invalid.Body.String())
	require.Contains(t, invalid.Body.String(), `"code":150423`)
	first := call(http.MethodPost, "/tenants/1/work-items/44/skill-distillations", "distill-once")
	require.Equal(t, 201, first.Code, first.Body.String())
	var created struct {
		Data model.Distillation `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &created))
	require.Positive(t, int64(created.Data.ID))
	require.Equal(t, "RUNNING", created.Data.State)
	require.Nil(t, created.Data.SkillVersionID)
	replay := call(http.MethodPost, "/tenants/1/work-items/44/skill-distillations", "distill-once")
	require.Equal(t, 201, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	require.Equal(t, 1, *launched)
	got := call(http.MethodGet, "/tenants/1/skill-distillations/"+created.Data.ID.String(), "")
	require.Equal(t, 200, got.Code, got.Body.String())
	m.distillation.source.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (AcceptedSkillSource, error) {
		return AcceptedSkillSource{}, ErrForbidden
	}
	denied := call(http.MethodPost, "/tenants/1/work-items/44/skill-distillations", "distill-once")
	require.Equal(t, 403, denied.Code, denied.Body.String())
	denied = call(http.MethodGet, "/tenants/1/skill-distillations/"+created.Data.ID.String(), "")
	require.Equal(t, 403, denied.Code, denied.Body.String())
	require.Equal(t, 1, *launched)
}
