package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSourceLifecycleMetadataArchiveCursorAndRollback(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	in := sourceDraft()
	in.Content = "# First\n\n" + strings.Repeat("First evidence paragraph. ", 80) + "\n\n# Second\n\n" + strings.Repeat("Second evidence paragraph. ", 80)
	v := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, in), nil, false)
	var page SourceChunkPage
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		page, e = m.sourceChunksCursorOn(ctx, tx, 1, v.ID, "", 0, 1)
		return e
	}))
	require.True(t, page.HasMore)
	require.NotEmpty(t, page.NextCursor)
	first := page.Items[0]
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		next, e := m.sourceChunksCursorOn(ctx, tx, 1, v.ID, page.NextCursor, 0, 1)
		require.NoError(t, e)
		require.NotEqual(t, first.ID, next.Items[0].ID)
		_, e = m.sourceChunksCursorOn(knowledgeContext(t, 3), tx, 1, v.ID, page.NextCursor, 0, 1)
		require.ErrorIs(t, e, ErrInvalid)
		_, e = m.sourceChunksCursorOn(ctx, tx, 1, v.ID, page.NextCursor, 1, 1)
		require.ErrorIs(t, e, ErrInvalid)
		return nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.sourceMetadataOn(ctx, tx, 1, v.ID, SourceMetadataInput{Version: v.Version, Title: "Revised evidence", Tags: []string{"audit", "audit", "evidence"}})
		return e
	}))
	require.Equal(t, []string{"audit", "evidence"}, v.Tags)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.sourceChunksCursorOn(ctx, tx, 1, v.ID, page.NextCursor, 0, 1)
		return e
	}), ErrInvalid)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		e := query.BindEntries(tx, m.names)
		entry, err := e.WithContext(ctx).Where(e.ID.Eq(first.EntryID)).First()
		require.NoError(t, err)
		require.Equal(t, "Revised evidence", entry.Title)
		require.Equal(t, sourceHash(entry.Title+"\n"+entry.Body), entry.ContentHash)
		return nil
	}))
	priorAudit := m.audit
	m.audit = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.archiveSourceOn(ctx, tx, 1, v.ID, v.Version); return e }), ErrUnavailable)
	m.audit = priorAudit
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		row, _, e := m.sourceOn(ctx, tx, 1, v.ID, "knowledge_read", false)
		require.NoError(t, e)
		require.Nil(t, row.ArchivedAt)
		var err error
		v, err = m.archiveSourceOn(ctx, tx, 1, v.ID, v.Version)
		return err
	}))
	require.NotNil(t, v.ArchivedAt)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		live, e := m.sourcesOn(ctx, tx, 1, sourceListInput{Scope: "TEAM", Limit: 50})
		require.NoError(t, e)
		require.Empty(t, live.Items)
		archived, e := m.sourcesOn(ctx, tx, 1, sourceListInput{Scope: "TEAM", Limit: 50, Archived: true})
		require.NoError(t, e)
		require.Len(t, archived.Items, 1)
		chunks, e := m.sourceChunksCursorOn(ctx, tx, 1, v.ID, "", 0, 50)
		require.NoError(t, e)
		require.Empty(t, chunks.Items)
		_, e = m.syncSourceOn(ctx, tx, 1, v.ID, SourceSyncInput{Version: v.Version}, false)
		require.ErrorIs(t, e, ErrConflict)
		_, e = m.sourceMetadataOn(ctx, tx, 1, v.ID, SourceMetadataInput{Version: v.Version, Title: "Late"})
		require.ErrorIs(t, e, ErrConflict)
		return nil
	}))
}

func TestSourceLifecycleMultipartRawHTTPAndReplayAuthority(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	request := func(names []string, text, key string, status int) []byte {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for _, name := range names {
			part, e := form.CreateFormFile("files", name)
			require.NoError(t, e)
			_, e = part.Write([]byte(text))
			require.NoError(t, e)
		}
		require.NoError(t, form.Close())
		req := httptest.NewRequest(http.MethodPost, "/tenants/1/knowledge/source-uploads?scope=TEAM", &body).WithContext(knowledgeContext(t, 2))
		req.Header.Set("Content-Type", form.FormDataContentType())
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, status, rec.Code, rec.Body.String())
		return rec.Body.Bytes()
	}
	first := request([]string{"one.md", "two.txt"}, "# Actual uploaded evidence", "upload", http.StatusCreated)
	var decoded struct {
		Data []SourceView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first, &decoded))
	require.Len(t, decoded.Data, 2)
	request([]string{"one.md", "two.txt"}, "# Actual uploaded evidence", "upload", http.StatusCreated)
	request([]string{"one.md", "two.txt"}, "Changed evidence", "upload", http.StatusConflict)
	request([]string{"bad.md"}, "%PDF-1.7 binary", "binary", http.StatusUnprocessableEntity)
	request([]string{"bad.pdf"}, "%PDF-1.7", "pdf", http.StatusServiceUnavailable)
	request([]string{"bad.txt"}, strings.Repeat("x", (10<<20)+1), "size", http.StatusRequestEntityTooLarge)
	request([]string{"bad.exe"}, "executable", "format", http.StatusUnsupportedMediaType)
	request([]string{"1.txt", "2.txt", "3.txt", "4.txt", "5.txt", "6.txt", "7.txt", "8.txt", "9.txt", "10.txt", "11.txt"}, "text", "count", http.StatusUnprocessableEntity)
	// The documented 10 MiB transport supports files larger than the legacy JSON limit.
	request([]string{"large.txt"}, strings.Repeat("x", 3<<20), "large", http.StatusCreated)
	q := query.BindSources(db, m.names)
	count, e := q.WithContext(t.Context()).Count()
	require.NoError(t, e)
	require.EqualValues(t, 3, count)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	request([]string{"one.md", "two.txt"}, "# Actual uploaded evidence", "upload", http.StatusNotFound)
}

func TestSourceMetadataAndArchiveStorageFailuresPreserveCanonicalBytes(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	failure := errors.New("transactional storage unavailable")
	rollback := errors.New("rollback diagnostic transaction")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	install := func() {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("transaction-storage-failure", func(tx *gorm.DB) {
			if tx.Statement.Table != m.names.Chunks.String() {
				probe(tx)
			}
		}))
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("transaction-storage-failure", probe))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("transaction-storage-failure", probe))
	}
	remove := func() {
		require.NoError(t, db.Callback().Query().Remove("transaction-storage-failure"))
		require.NoError(t, db.Callback().Create().Remove("transaction-storage-failure"))
		require.NoError(t, db.Callback().Update().Remove("transaction-storage-failure"))
	}
	exercise := func(run func(*gorm.DB) error) {
		calls, failAt = 0, 0
		install()
		apply := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				if e := run(tx); e != nil {
					return e
				}
				return rollback
			})
		}
		require.ErrorIs(t, apply(), rollback)
		total := calls
		require.Greater(t, total, 2)
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, apply(), failure, "storage stage %d", i)
		}
		remove()
	}
	exercise(func(tx *gorm.DB) error {
		_, e := m.sourceMetadataOn(knowledgeContext(t, 2), tx, 1, source.ID, SourceMetadataInput{Version: source.Version, Title: "Updated canonical title", Tags: []string{"reviewed"}})
		return e
	})
	exercise(func(tx *gorm.DB) error {
		_, e := m.archiveSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, source.Version)
		return e
	})
	q := query.BindSources(db, m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(source.ID)).First()
	require.NoError(t, e)
	require.Equal(t, source.Version, row.Version)
	require.Nil(t, row.ArchivedAt)
	require.Len(t, readKnowledgeChunks(t, m, db, source).Items, source.ChunkCount)
}

func TestSourcePublicMetadataAndArchiveRejectOverflowIDsAndReauthorizeReplay(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	call := func(method, path, key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(knowledgeContext(t, 2))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	path := "/tenants/1/knowledge/sources/" + source.ID.String()
	metadata := `{"version":1,"title":"Reviewed evidence","tags":["reviewed"]}`
	updated := call("PATCH", path, "metadata-edit", metadata)
	require.Equal(t, 200, updated.Code, updated.Body.String())
	replay := call("PATCH", path, "metadata-edit", metadata)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	for _, method := range []string{"PATCH", "POST"} {
		suffix, body := "", metadata
		if method == "POST" {
			suffix, body = "/archives", `{"version":2}`
		}
		invalid := call(method, "/tenants/1/knowledge/sources/9223372036854775808"+suffix, "overflow-source", body)
		require.Equal(t, 422, invalid.Code, invalid.Body.String())
	}
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	denied := call("PATCH", path, "metadata-edit", metadata)
	require.Equal(t, 404, denied.Code, denied.Body.String())
}

func TestSourceMetadataAndArchiveRejectInvalidMutationWithoutChangingVersion(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	for _, input := range []SourceMetadataInput{{Version: 0, Title: "Title"}, {Version: 1, Title: " "}, {Version: 1, Title: strings.Repeat("x", 201)}, {Version: 1, Title: "Title", Tags: make([]string, 11)}, {Version: 1, Title: "Title", Tags: []string{" "}}, {Version: 1, Title: "Title", Tags: []string{strings.Repeat("x", 33)}}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.sourceMetadataOn(knowledgeContext(t, 2), tx, 1, source.ID, input)
			return err
		}), ErrInvalid)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.archiveSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, 0)
		return err
	}), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.archiveSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, 2)
		return err
	}), ErrConflict)
	remote := sourceDraft()
	remote.SourceKind = "URL"
	remote.Locator = "https://docs.example.test/guide"
	remote.Content = ""
	other := bindKnowledgeSource(t, m, db, remote)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.sourceMetadataOn(knowledgeContext(t, 2), tx, 1, other.ID, SourceMetadataInput{Version: other.Version, Title: "Changed remote title"})
		return err
	}), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.archiveSourceOn(knowledgeContext(t, 2), tx, 1, other.ID, other.Version)
		return err
	}), ErrInvalid)
	q := query.BindSources(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(source.ID)).First()
	require.NoError(t, err)
	require.Equal(t, source.Version, row.Version)
	require.Equal(t, source.Title, row.Title)
}
