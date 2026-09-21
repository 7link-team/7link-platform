package todo

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestAPI(t *testing.T) humatest.TestAPI {
	api, _ := newTestAPIService(t)
	return api
}

func newTestAPIService(t *testing.T) (humatest.TestAPI, *service) {
	t.Helper()
	svc, _ := newTestService(t, "")
	// respx owns the error envelope's messages; without its catalog every
	// failure renders as an unresolved key and hides what actually went wrong.
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))

	router := chi.NewRouter()
	// The module contributes this middleware in production; without it the
	// filter parameters never reach the handler and every list is unfiltered.
	router.Use(RawQueryMiddleware)
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	contract, err := svc.contract()
	require.NoError(t, err)
	filterParams := contract.Parameters()
	registerTransferHTTP(api, svc, filterParams, todoRegistrar(&todoPolicy{}))
	registerHTTP(api, svc, filterParams, todoRegistrar(&todoPolicy{}))
	return humatest.Wrap(t, api), svc
}

// decode unpacks the platform envelope so assertions read the typed view
// rather than poking at raw JSON.
func decode(t *testing.T, body []byte) respx.Envelope[View] {
	t.Helper()
	var envelope respx.Envelope[View]
	require.NoError(t, json.Unmarshal(body, &envelope))
	return envelope
}

// rawEnvelope keeps Data as bytes, for assertions about which keys are present
// rather than what they contain.
func rawEnvelope(t *testing.T, body []byte) (respx.Envelope[json.RawMessage], map[string]json.RawMessage) {
	t.Helper()
	var envelope respx.Envelope[json.RawMessage]
	require.NoError(t, json.Unmarshal(body, &envelope))
	fields := map[string]json.RawMessage{}
	if len(envelope.Data) > 0 {
		require.NoError(t, json.Unmarshal(envelope.Data, &fields))
	}
	return envelope, fields
}

// createTask returns the new task's identifier and ETag, which every subsequent
// write needs.
func createTask(t *testing.T, api humatest.TestAPI, title string) (string, string) {
	t.Helper()
	resp := api.Post("/entities/10/todos", map[string]any{"title": title})
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
	envelope := decode(t, resp.Body.Bytes())
	require.NotZero(t, envelope.Data.ID)
	return envelope.Data.ID.String(), resp.Header().Get("ETag")
}

func TestCreateReturnsEnvelopeAndETag(t *testing.T) {
	api := newTestAPI(t)
	resp := api.Post("/entities/10/todos", map[string]any{"title": "write the spec"})

	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
	envelope := decode(t, resp.Body.Bytes())
	assert.Equal(t, 0, envelope.Code, "success envelopes carry code 0")
	require.NotNil(t, envelope.Data.Title)
	assert.Equal(t, "write the spec", *envelope.Data.Title)
	assert.Equal(t, `"1"`, resp.Header().Get("ETag"), "a new task is version 1")

	// The identifier crosses JSON as a string so large snowflakes survive a
	// JavaScript client.
	_, fields := rawEnvelope(t, resp.Body.Bytes())
	assert.Equal(t, byte('"'), fields["id"][0], "id must serialize as a JSON string")
}

func TestCreateRejectsEmptyTitle(t *testing.T) {
	api := newTestAPI(t)
	resp := api.Post("/entities/10/todos", map[string]any{"title": ""})
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code, resp.Body.String())
}

func TestGetMissingTaskReturns404(t *testing.T) {
	api := newTestAPI(t)
	resp := api.Get("/todos/999")
	require.Equal(t, http.StatusNotFound, resp.Code)
	envelope, _ := rawEnvelope(t, resp.Body.Bytes())
	assert.Equal(t, 404, envelope.Code, "the envelope code mirrors the HTTP status")
}

func TestUpdateWithoutIfMatchIsRefused(t *testing.T) {
	api := newTestAPI(t)
	id, _ := createTask(t, api, "task")

	resp := api.Patch("/todos/"+id, map[string]any{"title": "renamed"})
	assert.Equal(t, http.StatusPreconditionRequired, resp.Code,
		"an unconditional write must be refused rather than silently overwrite")
}

func TestUpdateWithStaleIfMatchConflicts(t *testing.T) {
	api := newTestAPI(t)
	id, etag := createTask(t, api, "task")

	first := api.Patch("/todos/"+id, "If-Match: "+etag, map[string]any{"title": "first"})
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	assert.Equal(t, `"2"`, first.Header().Get("ETag"))

	second := api.Patch("/todos/"+id, "If-Match: "+etag, map[string]any{"title": "second"})
	assert.Equal(t, http.StatusConflict, second.Code)
	conflict, _ := rawEnvelope(t, second.Body.Bytes())
	assert.Equal(t, 409, conflict.Code)
}

func TestIfMatchAcceptsBareVersion(t *testing.T) {
	api := newTestAPI(t)
	id, _ := createTask(t, api, "task")

	// Hand-written clients often omit the quotes; accepting both spares a
	// confusing 400 that teaches nothing.
	resp := api.Patch("/todos/"+id, "If-Match: 1", map[string]any{"title": "renamed"})
	assert.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
}

func TestIfMatchRejectsGarbage(t *testing.T) {
	api := newTestAPI(t)
	id, _ := createTask(t, api, "task")

	resp := api.Patch("/todos/"+id, "If-Match: not-a-version", map[string]any{"title": "x"})
	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestDeleteThenRestoreRoundTrip(t *testing.T) {
	api := newTestAPI(t)
	id, etag := createTask(t, api, "task")

	deleted := api.Delete("/todos/"+id, "If-Match: "+etag)
	require.Equal(t, http.StatusNoContent, deleted.Code)

	missing := api.Get("/todos/" + id)
	assert.Equal(t, http.StatusNotFound, missing.Code, "a deleted task is gone from the live view")

	// The delete advanced the version, so restore uses the next one.
	restored := api.Delete("/todos/"+id+"/deletion", "If-Match: 2")
	require.Equal(t, http.StatusOK, restored.Code, restored.Body.String())

	live := api.Get("/todos/" + id)
	assert.Equal(t, http.StatusOK, live.Code)
}

func TestRestoreLiveTaskConflicts(t *testing.T) {
	api := newTestAPI(t)
	id, etag := createTask(t, api, "task")

	resp := api.Delete("/todos/"+id+"/deletion", "If-Match: "+etag)
	assert.Equal(t, http.StatusConflict, resp.Code)
}

func TestPatchClearsDueAtWithExplicitNull(t *testing.T) {
	api := newTestAPI(t)
	resp := api.Post("/entities/10/todos", map[string]any{
		"title": "task", "due_at": "2026-10-01T00:00:00Z",
	})
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
	created := decode(t, resp.Body.Bytes())
	id := created.Data.ID.String()
	require.NotNil(t, created.Data.DueAt)

	cleared := api.Patch("/todos/"+id, "If-Match: 1", map[string]any{"due_at": nil})
	require.Equal(t, http.StatusOK, cleared.Code, cleared.Body.String())
	_, fields := rawEnvelope(t, cleared.Body.Bytes())
	assert.NotContains(t, fields, "due_at",
		"a cleared nullable field is omitted from the response")
}

func TestStatusTransitionsAreUnrestricted(t *testing.T) {
	api := newTestAPI(t)
	id, _ := createTask(t, api, "task")

	// done -> doing would be rejected by a state machine; this module has none
	// on purpose, so it must succeed.
	done := api.Patch("/todos/"+id, "If-Match: 1", map[string]any{"status": "done"})
	require.Equal(t, http.StatusOK, done.Code, done.Body.String())
	_, doneFields := rawEnvelope(t, done.Body.Bytes())
	assert.Contains(t, doneFields, "completed_at")

	doing := api.Patch("/todos/"+id, "If-Match: 2", map[string]any{"status": "doing"})
	require.Equal(t, http.StatusOK, doing.Code, doing.Body.String())
	_, doingFields := rawEnvelope(t, doing.Body.Bytes())
	assert.NotContains(t, doingFields, "completed_at",
		"leaving done clears the completion time")
}

func TestCreateRejectsUnknownStatus(t *testing.T) {
	api := newTestAPI(t)
	resp := api.Post("/entities/10/todos", map[string]any{"title": "task", "status": "archived"})
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code,
		"the enum tag rejects a status outside the declared set")
}

func TestListReturnsPagedEnvelope(t *testing.T) {
	api := newTestAPI(t)
	for _, title := range []string{"one", "two", "three"} {
		createTask(t, api, title)
	}

	resp := api.Get("/entities/10/todos?limit=2")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var envelope respx.Envelope[[]View]
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data, 2)

	require.NotNil(t, envelope.Meta.Page, "a list response carries pagination")
	assert.Equal(t, 2, envelope.Meta.Page.Count)
	assert.Equal(t, int64(3), envelope.Meta.Page.Total, "total counts the filter, not the page")
	assert.Equal(t, 0, envelope.Meta.Page.Offset)
}

func TestListAppliesFilters(t *testing.T) {
	api := newTestAPI(t)
	createTask(t, api, "keep")
	id, _ := createTask(t, api, "finish me")
	done := api.Patch("/todos/"+id, "If-Match: 1", map[string]any{"status": "done"})
	require.Equal(t, http.StatusOK, done.Code, done.Body.String())

	resp := api.Get("/entities/10/todos?status=eq:done")
	require.Equal(t, http.StatusOK, resp.Code)

	var envelope respx.Envelope[[]View]
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data, 1)
	require.NotNil(t, envelope.Data[0].Title)
	assert.Equal(t, "finish me", *envelope.Data[0].Title)
}

func TestListOmitsUnselectedFields(t *testing.T) {
	api := newTestAPI(t)
	createTask(t, api, "task")

	resp := api.Get("/entities/10/todos?fields=title")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var envelope respx.Envelope[[]json.RawMessage]
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data, 1)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope.Data[0], &fields))
	assert.Contains(t, fields, "title")
	assert.Contains(t, fields, "id", "id is always returned")
	assert.NotContains(t, fields, "description", "an unselected field is absent, not null")
	assert.NotContains(t, fields, "version")
}

func TestListRejectsUnknownSortKey(t *testing.T) {
	api := newTestAPI(t)
	resp := api.Get("/entities/10/todos?sort=nonsense")
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code, resp.Body.String())
}

func TestListRejectsOversizedLimit(t *testing.T) {
	api := newTestAPI(t)
	// The bound is declared on the parameter, so huma rejects it before the
	// handler runs.
	resp := api.Get("/entities/10/todos?limit=1000")
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
}

// uploadCSV posts a CSV body as the multipart file the import endpoint expects.
func uploadCSV(t *testing.T, api humatest.TestAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	var buffer bytes.Buffer
	form := multipart.NewWriter(&buffer)
	part, err := form.CreateFormFile("file", "todos.csv")
	require.NoError(t, err)
	_, err = part.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, form.Close())

	// humatest treats a []byte argument as a value to JSON-encode; a raw body
	// must be passed as an io.Reader.
	return api.Post("/entities/10/todos/import", "Content-Type: "+form.FormDataContentType(), bytes.NewReader(buffer.Bytes()))
}

func TestTemplateDownloadsCSVWithLocaleHeaders(t *testing.T) {
	api := newTestAPI(t)

	resp := api.Get("/entities/10/todos/import-template?format=csv&lang=zh-CN")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	assert.Contains(t, resp.Header().Get("Content-Type"), "text/csv")
	assert.Contains(t, resp.Header().Get("Content-Disposition"), "todo-import-template.csv")

	body := resp.Body.Bytes()
	assert.True(t, bytes.HasPrefix(body, utf8BOM), "Excel needs the BOM to read UTF-8")
	assert.Contains(t, string(body), "标题", "headers follow the requested locale")
}

func TestTemplateDownloadsXLSX(t *testing.T) {
	api := newTestAPI(t)
	resp := api.Get("/entities/10/todos/import-template?format=xlsx&sample=true")
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Header().Get("Content-Type"), "spreadsheetml")

	table, err := decodeGrid(bytes.NewReader(resp.Body.Bytes()), FormatXLSX)
	require.NoError(t, err)
	assert.Len(t, table, 2, "the sample row is present when asked for")
}

func TestTemplateRejectsUnsupportedFormat(t *testing.T) {
	api := newTestAPI(t)
	resp := api.Get("/entities/10/todos/import-template?format=pdf")
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
}

func TestImportCreatesTasks(t *testing.T) {
	api := newTestAPI(t)
	resp := uploadCSV(t, api, "title,priority,status\nfirst,1,open\nsecond,5,done\n")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var envelope respx.Envelope[ImportReport]
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &envelope))
	assert.Equal(t, 2, envelope.Data.Imported)

	listed := api.Get("/entities/10/todos")
	var page respx.Envelope[[]View]
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &page))
	assert.Len(t, page.Data, 2)
}

// TestImportWritesNothingWhenAnyRowIsInvalid is the guarantee that makes a
// failed import safe to retry with the whole corrected file.
func TestImportWritesNothingWhenAnyRowIsInvalid(t *testing.T) {
	api := newTestAPI(t)
	resp := uploadCSV(t, api, "title,priority\ngood,3\nbad,99\n")
	require.Equal(t, http.StatusUnprocessableEntity, resp.Code)

	listed := api.Get("/entities/10/todos")
	var page respx.Envelope[[]View]
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &page))
	assert.Empty(t, page.Data, "a rejected import leaves the table untouched")
}

func TestImportRejectsUnreadableFile(t *testing.T) {
	api := newTestAPI(t)
	// An xlsx parser cannot read arbitrary bytes.
	var buffer bytes.Buffer
	form := multipart.NewWriter(&buffer)
	part, err := form.CreateFormFile("file", "todos.xlsx")
	require.NoError(t, err)
	_, err = part.Write([]byte("not a workbook"))
	require.NoError(t, err)
	require.NoError(t, form.Close())

	resp := api.Post("/entities/10/todos/import?format=xlsx",
		"Content-Type: "+form.FormDataContentType(), bytes.NewReader(buffer.Bytes()))
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
}

func TestImportAcceptsBodiesAboveHumaDefaultLimit(t *testing.T) {
	api := newTestAPI(t)
	body := "title\n" + strings.Repeat(" ", 2<<20)
	resp := uploadCSV(t, api, body)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code, resp.Body.String())
	assert.NotContains(t, resp.Body.String(), "request body too large")
}

func TestExportReturnsFilteredRows(t *testing.T) {
	api := newTestAPI(t)
	createTask(t, api, "keep")
	id, _ := createTask(t, api, "finish")
	require.Equal(t, http.StatusOK,
		api.Patch("/todos/"+id, "If-Match: 1", map[string]any{"status": "done"}).Code)

	resp := api.Get("/entities/10/todos/export?status=eq:done&format=csv")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	assert.Contains(t, resp.Header().Get("Content-Disposition"), "todos.csv")

	table, err := decodeGrid(bytes.NewReader(resp.Body.Bytes()), FormatCSV)
	require.NoError(t, err)
	require.Len(t, table, 2, "one header plus the single matching row")
	assert.Contains(t, strings.Join(table[1], ","), "finish")
}

func TestExportSelectsColumns(t *testing.T) {
	api := newTestAPI(t)
	createTask(t, api, "task")

	resp := api.Get("/entities/10/todos/export?fields=title,status")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	table, err := decodeGrid(bytes.NewReader(resp.Body.Bytes()), FormatCSV)
	require.NoError(t, err)
	require.Len(t, table[0], 2, "only the requested columns are written")
}

func TestExportRejectsUnknownField(t *testing.T) {
	api := newTestAPI(t)
	resp := api.Get("/entities/10/todos/export?fields=nonsense")
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
}

func TestExportRoundTripsThroughImport(t *testing.T) {
	api := newTestAPI(t)
	createTask(t, api, "first")
	createTask(t, api, "second")

	exported := api.Get("/entities/10/todos/export?format=csv&lang=zh-CN")
	require.Equal(t, http.StatusOK, exported.Code)

	// A full export carries server-maintained columns; the importer ignores
	// them, so the file goes straight back in.
	resp := uploadCSV(t, api, exported.Body.String())
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var envelope respx.Envelope[ImportReport]
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &envelope))
	assert.Equal(t, 2, envelope.Data.Imported)
}

// TestEveryDeclaredQueryParameterIsReservedByTheContract ties the two halves of
// the list contract together.
//
// The filters are parsed from the raw query, so the contract rejects any
// parameter it does not recognize — which means every parameter huma declares
// must also be one the contract accepts, or the endpoint rejects a parameter
// its own documentation advertises. A parameter is accepted either because the
// transport reserves it or because it is a declared filter. Nothing in the type
// system connects the struct tags to either list, so it is asserted here.
//
// This caught a field tagged `query:"-"`. A parameter tag is a literal name to
// huma, not a skip sentinel, so that declared a parameter named "-" that showed
// up in the generated documentation and rejected the request when sent.
func TestEveryDeclaredQueryParameterIsReservedByTheContract(t *testing.T) {
	api := newTestAPI(t)

	accepted := map[string]bool{}
	for _, name := range transportParams {
		accepted[name] = true
	}
	// Declared by the transfer endpoints, which do not go through the filter
	// contract but share the same struct in the export case.
	accepted["sample"] = true
	// The filter parameters are documented from the query contract, so they are
	// accepted as filters rather than reserved. Both halves are read from the
	// same declaration the endpoint parses with.
	svc, _ := newTestService(t, "")
	contract, err := svc.contract()
	require.NoError(t, err)
	for _, filter := range contract.Describe().Filters {
		accepted[filter.Field] = true
	}

	for path, item := range api.OpenAPI().Paths {
		for method, op := range map[string]*huma.Operation{
			"GET": item.Get, "POST": item.Post, "PUT": item.Put,
			"PATCH": item.Patch, "DELETE": item.Delete,
		} {
			if op == nil {
				continue
			}
			for _, param := range op.Parameters {
				if param.In != "query" {
					continue
				}
				assert.True(t, accepted[param.Name],
					"%s %s declares query parameter %q, which the contract neither reserves "+
						"nor declares as a filter; the endpoint would reject its own "+
						"documented parameter",
					method, path, param.Name)
			}
		}
	}
}

// TestListAcceptsAnEmptyQueryString is the shape a documentation UI sends when
// it renders a parameter and the user leaves it blank.
func TestListAcceptsAnEmptyQueryString(t *testing.T) {
	api := newTestAPI(t)
	createTask(t, api, "task")

	for _, raw := range []string{"", "?", "?q=", "?fields=&sort="} {
		resp := api.Get("/entities/10/todos" + raw)
		assert.Equal(t, http.StatusOK, resp.Code, "GET /todos%s: %s", raw, resp.Body.String())
	}
}

// TestListDeclaresItsFilterParameters covers the half of the contract huma
// cannot see. The filters are parsed from the raw query rather than bound to
// struct fields, so without an explicit declaration they are absent from the
// generated document and from every client built from it.
func TestListDeclaresItsFilterParameters(t *testing.T) {
	api := newTestAPI(t)

	for _, path := range []string{"/entities/{entity_id}/todos", "/entities/{entity_id}/todos/export"} {
		operation := api.OpenAPI().Paths[path].Get
		require.NotNil(t, operation, path)
		declared := map[string]bool{}
		for _, param := range operation.Parameters {
			declared[param.Name] = true
		}
		for _, name := range []string{"title", "status", "priority", "due_at", "created_by"} {
			assert.True(t, declared[name], "%s does not document filter %q", path, name)
		}
	}
}

// TestWildcardOnlySearchStillReturnsEverything pins the half of the wildcard
// rule that must not change. Dropping the predicate is an optimization, not a
// filter: q=% matched every row when it built LIKE '%%', and it still does.
func TestWildcardOnlySearchStillReturnsEverything(t *testing.T) {
	api := newTestAPI(t)
	createTask(t, api, "first")
	createTask(t, api, "second")

	titles := func(path string) []string {
		resp := api.Get(path)
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		var page respx.Envelope[[]View]
		require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &page))
		out := make([]string, 0, len(page.Data))
		for _, view := range page.Data {
			require.NotNil(t, view.Title)
			out = append(out, *view.Title)
		}
		return out
	}
	assert.Equal(t, titles("/entities/10/todos"), titles("/entities/10/todos?q=%25"),
		"a search for nothing must return the same page as no search")
}
