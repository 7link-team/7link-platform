package knowledge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestKnowledgeReviewRejectsWriterAndRechecksCurrentAuthorityOnReplay(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Independent decision", Body: "Require a second Human.", Provenance: "Human-authored source"})
	in := &knowledgeReviewInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, EntryID: entry.ID.String(), IdempotencyKey: "independent-review", Body: ReviewInput{Version: 1, Action: "APPROVED", Reason: "Reviewed the immutable rule and its source"}}
	_, err := m.reviewHTTP(knowledgeContext(t, 2), in)
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 403, status.GetStatus())
	body, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)
	require.Contains(t, string(body), `"reason_code":"SELF_REVIEW"`)
	first, err := m.reviewHTTP(knowledgeContext(t, 3), in)
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", first.Body.Data.State)
	replayed, err := m.reviewHTTP(knowledgeContext(t, 3), in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	m.tenantPolicy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission != "memory_approve"}, nil
	}
	_, err = m.reviewHTTP(knowledgeContext(t, 3), in)
	require.ErrorAs(t, err, &status)
	require.Equal(t, 403, status.GetStatus(), "knowledge_read cannot substitute for revoked review authority")
	for _, table := range []string{m.names.Reviews.String(), "k_domain_events", "audit_probe"} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.EqualValues(t, 2, count, "only creation and the independent review may write facts")
	}
}

func TestKnowledgeErrorsHideProviderDetailsAndUnavailableCommands(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	for _, tc := range []struct {
		err    error
		status int
	}{{ErrForbidden, 403}, {ErrNotFound, 404}, {ErrConflict, 409}, {idempotency.ErrInProgress, 409}, {ErrInvalid, 422}, {ErrUnavailable, 503}, {errors.New("private SQL credentials"), 500}} {
		err := knowledgeError(ctx, tc.err)
		var status huma.StatusError
		require.ErrorAs(t, err, &status)
		require.Equal(t, tc.status, status.GetStatus())
		require.NotContains(t, err.Error(), "private SQL")
	}
	m.runtime = nil
	_, err := m.listHTTP(ctx, &knowledgeListInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}})
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 503, status.GetStatus())
	_, err = m.createHTTP(ctx, &knowledgeCreateInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}})
	require.ErrorAs(t, err, &status)
	require.Equal(t, 503, status.GetStatus())
	_, err = m.reviewHTTP(ctx, &knowledgeReviewInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, EntryID: "5"})
	require.ErrorAs(t, err, &status)
	require.Equal(t, 503, status.GetStatus())
}

func TestKnowledgeCommandBoundariesAndUnkeyedWrite(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	invalid := KnowledgeTenantInput{TenantID: "bad"}
	for _, call := range []func() error{
		func() error {
			_, err := m.listHTTP(ctx, &knowledgeListInput{KnowledgeTenantInput: invalid})
			return err
		},
		func() error {
			_, err := m.createHTTP(ctx, &knowledgeCreateInput{KnowledgeTenantInput: invalid})
			return err
		},
		func() error {
			_, err := m.reviewHTTP(ctx, &knowledgeReviewInput{KnowledgeTenantInput: invalid})
			return err
		},
		func() error {
			_, err := m.reviewHTTP(ctx, &knowledgeReviewInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, EntryID: "bad"})
			return err
		},
	} {
		var status huma.StatusError
		require.ErrorAs(t, call(), &status)
		require.Equal(t, 422, status.GetStatus())
	}
	in := &knowledgeCreateInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, Body: EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Guide", Body: "Contents", Provenance: "Human notes"}}
	first, err := m.createHTTP(ctx, in)
	require.NoError(t, err)
	require.Empty(t, first.Replayed)
	in.IdempotencyKey = strings.Repeat("a", 65)
	_, err = m.createHTTP(ctx, in)
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 422, status.GetStatus())
	in.IdempotencyKey = ""
	m.replay = nil
	_, err = m.createHTTP(ctx, in)
	require.ErrorAs(t, err, &status)
	require.Equal(t, 503, status.GetStatus())
}

func TestKnowledgeHTTPHumanReviewRejectReplayAndScopeIsolation(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Proposal", Body: "Review me", Provenance: "Author notes"})
	ctx := knowledgeContext(t, 3)
	in := &knowledgeReviewInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, EntryID: entry.ID.String(), IdempotencyKey: "reject-1", Body: ReviewInput{Version: 1, Action: "REJECTED", Reason: "Insufficient supporting evidence"}}
	first, err := m.reviewHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "REJECTED", first.Body.Data.State)
	second, err := m.reviewHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", second.Replayed)
	require.Equal(t, first.Body.Data.Version, second.Body.Data.Version)
	in.Body.Action = "APPROVED"
	_, err = m.reviewHTTP(ctx, in)
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 409, status.GetStatus())
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/tenants/1/knowledge?scope=TEAM&state=REJECTED", 200},
		{"/tenants/2/knowledge?scope=TEAM", 403},
		{"/tenants/1/knowledge?scope=PROJECT&project_id=invalid", 422},
		{"/tenants/1/knowledge?scope=AGENT&agent_principal_id=7", 200},
		{"/tenants/1/knowledge?scope=TEAM&cursor=malformed", 422},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil).WithContext(ctx))
		require.Equal(t, tc.status, rec.Code, rec.Body.String())
	}
	// A malformed command never creates a review or emits an event.
	request := httptest.NewRequest(http.MethodPost, "/tenants/1/knowledge/"+entry.ID.String()+"/reviews", strings.NewReader(`{"version":2,"action":"REVOKED","reason":"short"}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)
	require.Equal(t, 409, rec.Code, rec.Body.String())
	var n int64
	require.NoError(t, db.Table("k_knowledge_reviews").Count(&n).Error)
	require.EqualValues(t, 2, n)
}

func TestKnowledgeHTTPReplayQueueAndCursorScope(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	in := &knowledgeCreateInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, IdempotencyKey: "knowledge-1", Body: EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Review first", Body: "Evidence must be reviewed.", Provenance: "Human notes"}}
	first, e := m.createHTTP(ctx, in)
	require.NoError(t, e)
	second, e := m.createHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", second.Replayed)
	require.Equal(t, first.Body.Data.ID, second.Body.Data.ID)
	var n int64
	require.NoError(t, db.Table("k_knowledge_entries").Count(&n).Error)
	require.EqualValues(t, 1, n)
	in.Body.Body = "Changed content"
	_, e = m.createHTTP(ctx, in)
	var status huma.StatusError
	require.ErrorAs(t, e, &status)
	require.Equal(t, 409, status.GetStatus())
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	request := httptest.NewRequest(http.MethodGet, "/tenants/1/knowledge?scope=TEAM&state=PENDING_REVIEW", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "Review first")
	query := knowledgeListInput{Scope: "TEAM", State: "PENDING_REVIEW", Limit: 1}
	writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Second", Body: "Second rule", Provenance: "Human notes"})
	var page KnowledgePage
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; page, e = m.listOn(ctx, tx, 1, query); return e }))
	require.True(t, page.HasMore)
	require.NotEmpty(t, page.NextCursor)
	query.Cursor = page.NextCursor
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { page, e = m.listOn(ctx, tx, 1, query); return e }))
	require.Len(t, page.Items, 1)
	require.False(t, page.HasMore)
	query.State = "ACTIVE"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.listOn(ctx, tx, 1, query); return e }), ErrInvalid)
}

func TestKnowledgeErrorKeepsChosenStatusAndHeaders(t *testing.T) {
	chosen := huma.ErrorWithHeaders(huma.Error422UnprocessableEntity("Input rejected"), http.Header{"Retry-After": {"3"}})
	require.Same(t, chosen, knowledgeError(t.Context(), chosen))
}

func TestKnowledgeListRejectsMalformedAndCrossScopeCursorBeforeReadingEntries(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	input := knowledgeListInput{Scope: "TEAM", State: "ACTIVE", Limit: 10}
	hash := scopeCursor(Scope{Scope: "TEAM"}, 1, 2, "ACTIVE")
	for _, cursor := range []string{strings.Repeat("x", 513), "%", base64.RawURLEncoding.EncodeToString([]byte("no-delimiter")), base64.RawURLEncoding.EncodeToString([]byte("0:" + hash)), knowledgeCursor(1, "other-scope")} {
		input.Cursor = cursor
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.listOn(knowledgeContext(t, 2), tx, 1, input); return err }), ErrInvalid)
	}
	input.Cursor = ""
	input.Scope = "AGENT"
	input.AgentPrincipalID = "invalid"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.listOn(knowledgeContext(t, 2), tx, 1, input); return err }), ErrInvalid)
	input.Scope = "TEAM"
	input.AgentPrincipalID = ""
	input.State = "UNKNOWN"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.listOn(knowledgeContext(t, 2), tx, 1, input); return err }), ErrInvalid)
}

func TestKnowledgeCreateQuotaResponseIncludesRetryAfterAndRetainsOnlyDeniedClassifier(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	for i := 0; i < 20; i++ {
		input := memoryInput()
		input.Kind = "RULE"
		input.Title = fmt.Sprintf("Pending independent policy %d", i)
		writeKnowledge(t, m, db, input)
	}
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	input := memoryInput()
	input.Kind = "RULE"
	input.Title = "One more pending independent policy"
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/tenants/1/knowledge", bytes.NewReader(raw)).WithContext(knowledgeContext(t, 2))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, 429, rec.Code, rec.Body.String())
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
	require.Contains(t, rec.Body.String(), "MEMORY_QUOTA")
	var count int64
	require.NoError(t, db.Table(m.names.Entries.String()).Count(&count).Error)
	require.EqualValues(t, 20, count)
	require.NoError(t, db.Table("audit_probe").Where("event_type = ?", "knowledge.memory_write_denied").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
