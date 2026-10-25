package knowledge

import (
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSkillHTTPValidationIdempotencyAndCurrentAuthority(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	call := func(actor guid.ID, method, path, key string, body any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		request := httptest.NewRequest(method, path, strings.NewReader(string(raw))).WithContext(knowledgeContext(t, actor))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, request)
		return rec
	}
	bad := call(2, http.MethodPost, "/tenants/1/skills", "invalid-skill", SkillContentInput{Content: "---\nname: Bad-Name\n---\nbody"})
	require.Equal(t, 422, bad.Code, bad.Body.String())
	require.Contains(t, bad.Body.String(), `"code":150427`)
	require.Contains(t, bad.Body.String(), `"field":"name"`)
	first := call(2, http.MethodPost, "/tenants/1/skills", "create-skill", SkillContentInput{Content: skillDocument("review-code", "First version")})
	require.Equal(t, 201, first.Code, first.Body.String())
	var created struct {
		Data SkillResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &created))
	require.Equal(t, "DRAFT", created.Data.Version.State)
	replay := call(2, http.MethodPost, "/tenants/1/skills", "create-skill", SkillContentInput{Content: skillDocument("review-code", "First version")})
	require.Equal(t, 201, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	var replayed struct {
		Data SkillResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &replayed))
	require.Equal(t, created.Data, replayed.Data)
	path := "/tenants/1/skill-versions/" + created.Data.Version.ID.String() + "/reviews"
	denied := call(7, http.MethodPost, path, "agent-review", SkillReviewInput{Decision: "APPROVED"})
	require.Equal(t, 403, denied.Code, denied.Body.String())
	approved := call(2, http.MethodPost, path, "human-review", SkillReviewInput{Decision: "APPROVED", Reason: "Reviewed exact content"})
	require.Equal(t, 201, approved.Code, approved.Body.String())
	mount := call(2, http.MethodPut, "/tenants/1/agents/7/skills/"+created.Data.Skill.ID.String(), "mount-skill", SkillMountInput{})
	require.Equal(t, 200, mount.Code, mount.Body.String())
	listed := call(2, http.MethodGet, "/tenants/1/skills?offset=0&limit=1&search=review", "", nil)
	require.Equal(t, 200, listed.Code, listed.Body.String())
	require.Contains(t, listed.Body.String(), `"skill_key":"review-code"`)
	invalidLimit := call(2, http.MethodGet, "/tenants/1/skills?limit=201", "", nil)
	require.Equal(t, 422, invalidLimit.Code, invalidLimit.Body.String())
	m.tenantPolicy = core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	})
	denied = call(2, http.MethodPost, path, "human-review", SkillReviewInput{Decision: "APPROVED", Reason: "Reviewed exact content"})
	require.Equal(t, 403, denied.Code, denied.Body.String())
}

func TestSkillPublicVersionMountAndArchiveRoutesRetainExactHistoricalContent(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	first := createTestSkill(t, m, db, "public-review")
	approveTestSkill(t, m, db, first.Version.ID)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	call := func(method, path, key string, body any) *httptest.ResponseRecorder {
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw))).WithContext(knowledgeContext(t, 2))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, r)
		return out
	}
	base := "/tenants/1/skills/" + first.Skill.ID.String()
	version := "/tenants/1/skill-versions/" + first.Version.ID.String()
	mount := "/tenants/1/agents/7/skills/" + first.Skill.ID.String()
	response := call(http.MethodGet, version, "", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Original instructions")
	response = call(http.MethodPost, base+"/versions", "new-public-version", SkillContentInput{Content: skillDocument("public-review", "Second immutable version")})
	require.Equal(t, 201, response.Code, response.Body.String())
	response = call(http.MethodGet, base+"/versions?limit=1&search=version", "", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"version_no":2`)
	response = call(http.MethodPut, mount, "pin-public-version", SkillMountInput{PinnedVersionID: &first.Version.ID})
	require.Equal(t, 200, response.Code, response.Body.String())
	response = call(http.MethodGet, "/tenants/1/agents/7/skills?limit=1&search=PUBLIC", "", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "public-review")
	response = call(http.MethodDelete, mount, "remove-public-mount", nil)
	require.Equal(t, 204, response.Code, response.Body.String())
	response = call(http.MethodDelete, mount, "remove-public-mount", nil)
	require.Equal(t, 204, response.Code, response.Body.String())
	response = call(http.MethodPatch, base, "archive-public-skill", SkillStateInput{Version: 2, State: "ARCHIVED"})
	require.Equal(t, 200, response.Code, response.Body.String())
	response = call(http.MethodPatch, base, "archive-public-skill", SkillStateInput{Version: 2, State: "ARCHIVED"})
	require.Equal(t, 200, response.Code, response.Body.String())
	response = call(http.MethodGet, version, "", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Original instructions")
	response = call(http.MethodGet, "/tenants/1/skills?state=ARCHIVED&limit=1", "", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "public-review")
}

func TestSkillPublicReadsPropagateStorageFailureAndCurrentAuthority(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	skill := createTestSkill(t, m, db, "stored-guidance")
	approveTestSkill(t, m, db, skill.Version.ID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.mountSkillOn(knowledgeContext(t, 2), tx, 1, 7, skill.Skill.ID, nil)
		return e
	}))
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	call := func(path string) int {
		r := httptest.NewRequest(http.MethodGet, "/tenants/1"+path, nil).WithContext(knowledgeContext(t, 2))
		out := httptest.NewRecorder()
		router.ServeHTTP(out, r)
		return out.Code
	}
	failure := errors.New("skill read unavailable")
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("skill-public-query", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	paths := []string{"/skills?limit=1&search=stored", "/skills/" + skill.Skill.ID.String(), "/skills/" + skill.Skill.ID.String() + "/versions?limit=1&search=review", "/skill-versions/" + skill.Version.ID.String(), "/agents/7/skills?limit=1&search=stored"}
	for _, path := range paths {
		calls, failAt = 0, 0
		require.Equal(t, 200, call(path), path)
		total := calls
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.NotEqual(t, 200, call(path), "%s query %d", path, i)
		}
	}
	require.NoError(t, db.Callback().Query().Remove("skill-public-query"))
	m.runtime = nil
	for _, path := range paths {
		require.Equal(t, 503, call(path), path)
	}
}

func TestSkillPublicRoutesRejectOverflowIdentifiersBeforeAnyMutation(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	overflow := "9223372036854775808"
	cases := []struct{ method, path, body string }{
		{"GET", "/skills/" + overflow, ""},
		{"PATCH", "/skills/" + overflow, `{"version":1,"state":"ARCHIVED"}`},
		{"GET", "/skills/" + overflow + "/versions", ""},
		{"POST", "/skills/" + overflow + "/versions", `{"content":"placeholder"}`},
		{"GET", "/skill-versions/" + overflow, ""},
		{"POST", "/skill-versions/" + overflow + "/reviews", `{"decision":"APPROVED","reason":"Current review"}`},
		{"GET", "/agents/" + overflow + "/skills", ""},
		{"PUT", "/agents/" + overflow + "/skills/1", `{}`},
		{"PUT", "/agents/7/skills/" + overflow, `{}`},
		{"DELETE", "/agents/" + overflow + "/skills/1", ""},
		{"DELETE", "/agents/7/skills/" + overflow, ""},
	}
	for _, test := range cases {
		req := httptest.NewRequest(test.method, "/tenants/1"+test.path, strings.NewReader(test.body)).WithContext(knowledgeContext(t, 2))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "overflow-identifier")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		require.Equal(t, 422, out.Code, "%s %s: %s", test.method, test.path, out.Body.String())
	}
	var writes int64
	require.NoError(t, db.Table("audit_probe").Count(&writes).Error)
	require.Zero(t, writes)
}
