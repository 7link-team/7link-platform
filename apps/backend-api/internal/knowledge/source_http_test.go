package knowledge

import (
	"7link.com/platform/internal/knowledge/query"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSourceHTTPIdempotencyReplayReauthorizesAndDoesNotRepeatIndexWrites(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	create := &sourceCreateInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, IdempotencyKey: "bind-source", Body: sourceDraft()}
	first, err := m.bindSourceHTTP(ctx, create)
	require.NoError(t, err)
	require.Empty(t, first.Replayed)
	replay, err := m.bindSourceHTTP(ctx, create)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data.ID, replay.Body.Data.ID)
	create.Body.Title = "Different body with the same key"
	_, err = m.bindSourceHTTP(ctx, create)
	assertKnowledgeHTTPStatus(t, err, 409)
	path := SourcePathInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, SourceID: first.Body.Data.ID.String()}
	sync := &sourceSyncHTTPInput{SourcePathInput: path, IdempotencyKey: "sync-source", Body: SourceSyncInput{Version: 1}}
	indexed, err := m.syncSourceHTTP(ctx, sync)
	require.NoError(t, err)
	require.Equal(t, "INDEXED", indexed.Body.Data.State)
	replay, err = m.syncSourceHTTP(ctx, sync)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, indexed.Body.Data.Version, replay.Body.Data.Version)
	chunks, err := m.sourceChunksHTTP(ctx, &sourceChunkListInput{SourcePathInput: path, Limit: 200})
	require.NoError(t, err)
	require.Len(t, chunks.Body.Data.Items, 1)
	_, err = m.sourceChunksHTTP(ctx, &sourceChunkListInput{SourcePathInput: path, Limit: 201})
	assertKnowledgeHTTPStatus(t, err, http.StatusUnprocessableEntity)
	reindex := &sourceVersionHTTPInput{SourcePathInput: path, IdempotencyKey: "reindex-source", Body: SourceVersionInput{Version: 2}}
	rebuilt, err := m.reindexSourceHTTP(ctx, reindex)
	require.NoError(t, err)
	require.Equal(t, 3, rebuilt.Body.Data.Version)
	reindex.Body.Version = 1
	reindex.IdempotencyKey = "stale-source"
	_, err = m.reindexSourceHTTP(ctx, reindex)
	assertKnowledgeHTTPStatus(t, err, 409)
	invalidation := &sourceVersionHTTPInput{SourcePathInput: path, IdempotencyKey: "invalidate-source", Body: SourceVersionInput{Version: 3}}
	invalidated, err := m.invalidateSourceHTTP(ctx, invalidation)
	require.NoError(t, err)
	require.Equal(t, "INVALIDATED", invalidated.Body.Data.State)
	replay, err = m.invalidateSourceHTTP(ctx, invalidation)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	_, err = m.sourcesHTTP(ctx, &sourceListInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, Scope: "TEAM", Limit: 50})
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.sourceChunksOn(ctx, tx, 1, first.Body.Data.ID, 0, 100)
		require.Empty(t, page.Items)
		return e
	}))
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.syncSourceHTTP(ctx, sync)
	assertKnowledgeHTTPStatus(t, err, 404)
	_, err = m.sourceHTTP(ctx, &path)
	assertKnowledgeHTTPStatus(t, err, 404)
}

func assertKnowledgeHTTPStatus(t *testing.T, err error, status int) {
	t.Helper()
	var problem huma.StatusError
	require.ErrorAs(t, err, &problem)
	require.Equal(t, status, problem.GetStatus())
}

func TestSettingsHTTPPersistReplayAndCurrentManagementPermission(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	get, err := m.settingsHTTP(ctx, &KnowledgeTenantInput{TenantID: "1"})
	require.NoError(t, err)
	require.Zero(t, get.Body.Data.Version)
	in := &settingsHTTPInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, IdempotencyKey: "settings-save", Body: KnowledgeSettingsInput{CandidateLimit: 25}}
	out, err := m.saveSettingsHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, 25, out.Body.Data.CandidateLimit)
	replay, err := m.saveSettingsHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, out.Body.Data.Version, replay.Body.Data.Version)
	in.Body.CandidateLimit = 30
	_, err = m.saveSettingsHTTP(ctx, in)
	assertKnowledgeHTTPStatus(t, err, 409)
	in.Body.CandidateLimit = 25
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.saveSettingsHTTP(ctx, in)
	assertKnowledgeHTTPStatus(t, err, 403)
	_, err = m.settingsHTTP(ctx, &KnowledgeTenantInput{TenantID: "1"})
	assertKnowledgeHTTPStatus(t, err, 403)
}

func TestKnowledgeSourceRoutesPublishTypedContractsAndRequiredIdempotency(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	api := humachi.New(chi.NewRouter(), huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	paths := api.OpenAPI().Paths
	require.NotNil(t, paths["/tenants/{tenant_id}/knowledge/settings"].Get)
	require.NotNil(t, paths["/tenants/{tenant_id}/knowledge/settings"].Put)
	require.NotNil(t, paths["/tenants/{tenant_id}/knowledge/sources/{source_id}/chunks"].Get)
	for _, p := range []string{"/knowledge/sources/{source_id}", "/knowledge/sources/{source_id}/chunks", "/knowledge/sources/{source_id}/syncs", "/knowledge/sources/{source_id}/reindexes", "/knowledge/sources/{source_id}/invalidations"} {
		item := paths["/tenants/{tenant_id}"+p]
		require.NotNil(t, item, p)
		operation := item.Get
		if operation == nil {
			operation = item.Post
		}
		require.NotNil(t, operation, p)
		for _, name := range []string{"tenant_id", "source_id"} {
			found := false
			for _, parameter := range operation.Parameters {
				if parameter.In == "path" && parameter.Name == name {
					found = true
					require.True(t, parameter.Required, p+" "+name)
					require.NotNil(t, parameter.Schema, p+" "+name)
					require.Equal(t, "^[1-9][0-9]*$", parameter.Schema.Pattern, p+" "+name)
					require.NotNil(t, parameter.Schema.MaxLength, p+" "+name)
					require.Equal(t, 19, *parameter.Schema.MaxLength, p+" "+name)
				}
			}
			require.True(t, found, p+" "+name)
		}
	}
	chunkOperation := paths["/tenants/{tenant_id}/knowledge/sources/{source_id}/chunks"].Get
	chunkCursor := false
	for _, parameter := range chunkOperation.Parameters {
		if parameter.In == "query" {
			require.NotEqual(t, "offset", parameter.Name, "legacy offset must not advertise a second pagination contract")
			chunkCursor = chunkCursor || parameter.Name == "cursor"
		}
		if parameter.In == "query" && parameter.Name == "limit" {
			require.NotNil(t, parameter.Schema.Maximum)
			require.InDelta(t, float64(100), *parameter.Schema.Maximum, 0)
		}
	}
	require.True(t, chunkCursor)
	for _, p := range []string{"/knowledge/sources", "/knowledge/sources/{source_id}/syncs", "/knowledge/sources/{source_id}/reindexes", "/knowledge/sources/{source_id}/invalidations"} {
		op := paths["/tenants/{tenant_id}"+p].Post
		require.NotNil(t, op)
		require.Contains(t, op.Responses, "201")
		found := false
		for _, param := range op.Parameters {
			if param.In == "header" && param.Name == "Idempotency-Key" {
				found = true
				require.True(t, param.Required)
			}
		}
		require.True(t, found, p)
	}
	// Handlers also enforce the key when invoked outside Huma validation.
	_, err := m.bindSourceHTTP(knowledgeContext(t, 2), &sourceCreateInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, Body: sourceDraft()})
	assertKnowledgeHTTPStatus(t, err, http.StatusUnprocessableEntity)
}

func TestKnowledgeSourceHTTPRejectsMalformedIDsPaginationAndMissingDatabase(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	bad := SourcePathInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, SourceID: "bad"}
	for _, call := range []func() error{
		func() error { _, e := m.sourceHTTP(ctx, &bad); return e },
		func() error { _, e := m.syncSourceHTTP(ctx, &sourceSyncHTTPInput{SourcePathInput: bad}); return e },
		func() error {
			_, e := m.reindexSourceHTTP(ctx, &sourceVersionHTTPInput{SourcePathInput: bad})
			return e
		},
		func() error {
			_, e := m.invalidateSourceHTTP(ctx, &sourceVersionHTTPInput{SourcePathInput: bad})
			return e
		},
		func() error { _, e := m.sourceChunksHTTP(ctx, &sourceChunkListInput{SourcePathInput: bad}); return e },
		func() error { _, e := m.settingsHTTP(ctx, &KnowledgeTenantInput{TenantID: "bad"}); return e },
	} {
		assertKnowledgeHTTPStatus(t, call(), 422)
	}
	m.runtime = nil
	_, err := m.settingsHTTP(ctx, &KnowledgeTenantInput{TenantID: "1"})
	assertKnowledgeHTTPStatus(t, err, 503)
	_, err = m.bindSourceHTTP(ctx, &sourceCreateInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, IdempotencyKey: "test", Body: sourceDraft()})
	assertKnowledgeHTTPStatus(t, err, 503)
}

func TestSourcePublicReadStorageFailuresNeverReturnPartialChunksOrUsage(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	ctx := knowledgeContext(t, 2)
	path := SourcePathInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, SourceID: source.ID.String()}
	failure := errors.New("source read unavailable")
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("source-public-query", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	for _, run := range []func() error{func() error { _, e := m.sourceHTTP(ctx, &path); return e }, func() error {
		_, e := m.sourceChunksHTTP(ctx, &sourceChunkListInput{SourcePathInput: path, Limit: 1})
		return e
	}, func() error { _, e := m.sourceUsageHTTP(ctx, &path); return e }, func() error {
		_, e := m.sourcesHTTP(ctx, &sourceListInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, Scope: "TEAM", Limit: 1})
		return e
	}} {
		calls, failAt = 0, 0
		require.NoError(t, run())
		total := calls
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.Error(t, run(), "source read stage %d", i)
		}
	}
	require.NoError(t, db.Callback().Query().Remove("source-public-query"))
	for _, raw := range []string{"bad", "0"} {
		bad := SourcePathInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, SourceID: raw}
		_, e := m.sourceHTTP(ctx, &bad)
		require.Error(t, e)
		_, e = m.sourceChunksHTTP(ctx, &sourceChunkListInput{SourcePathInput: bad, Limit: 1})
		require.Error(t, e)
		_, e = m.sourceUsageHTTP(ctx, &bad)
		require.Error(t, e)
		_, e = m.syncSourceHTTP(ctx, &sourceSyncHTTPInput{SourcePathInput: bad})
		require.Error(t, e)
		_, e = m.ingestSourceHTTP(ctx, &sourceSyncHTTPInput{SourcePathInput: bad})
		require.Error(t, e)
		_, e = m.reindexSourceHTTP(ctx, &sourceVersionHTTPInput{SourcePathInput: bad})
		require.Error(t, e)
		_, e = m.invalidateSourceHTTP(ctx, &sourceVersionHTTPInput{SourcePathInput: bad})
		require.Error(t, e)
	}
}

func TestSourcePublicReadsAndCommandsRejectOverflowAndUnavailableDatabase(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	call := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"version":1}`)).WithContext(knowledgeContext(t, 2))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "source-identifier-check")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	paths := []struct{ method, suffix string }{{"GET", ""}, {"GET", "/chunks"}, {"GET", "/usage"}, {"POST", "/syncs"}, {"POST", "/ingestions"}, {"POST", "/reindexes"}, {"POST", "/invalidations"}}
	for _, test := range paths {
		out := call(test.method, "/tenants/1/knowledge/sources/9223372036854775808"+test.suffix)
		require.Equal(t, 422, out.Code, "%s: %s", test.suffix, out.Body.String())
	}
	m.runtime = nil
	for _, test := range paths {
		out := call(test.method, "/tenants/1/knowledge/sources/1"+test.suffix)
		require.Equal(t, 503, out.Code, "%s: %s", test.suffix, out.Body.String())
	}
}

func TestSourceCommandRejectsWrongTenantAndMissingReplayWithoutWrites(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	input := sourceCreateInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "2"}, IdempotencyKey: "tenant-fence", Body: sourceDraft()}
	_, err := m.bindSourceHTTP(knowledgeContext(t, 2), &input)
	assertKnowledgeHTTPStatus(t, err, 403)
	input.TenantID = "bad"
	_, err = m.bindSourceHTTP(knowledgeContext(t, 2), &input)
	assertKnowledgeHTTPStatus(t, err, 422)
	input.TenantID = "1"
	m.replay = nil
	_, err = m.bindSourceHTTP(knowledgeContext(t, 2), &input)
	assertKnowledgeHTTPStatus(t, err, 503)
	q := query.BindSources(db, m.names)
	n, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, n)
	for _, bad := range []sourceListInput{{Scope: "PROJECT", ProjectID: "bad", Limit: 1}, {Scope: "TEAM", Limit: 0}, {Scope: "TEAM", Limit: 1, State: "unknown"}, {Scope: "TEAM", Limit: 1, Cursor: "bad"}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.sourcesOn(knowledgeContext(t, 2), tx, 1, bad); return err }), ErrInvalid)
	}
}
