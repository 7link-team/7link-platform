package todo

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fieldPolicy(t *testing.T, svc *service) *todoPolicy {
	t.Helper()
	constraint, err := (&todoPolicy{}).FieldConstraint(todoContext(t), 1, 10, Name, 11)
	require.NoError(t, err)
	policy := &todoPolicy{fields: constraint.Fields}
	svc.fields = policy
	return policy
}

func TestFieldPolicyProtectsSingleListExportAndQueryInference(t *testing.T) {
	api, svc := newTestAPIService(t)
	response := api.Post("/entities/10/todos", map[string]any{"title": "private title", "description": "private description", "priority": 2})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	id := decode(t, response.Body.Bytes()).Data.ID.String()
	policy := fieldPolicy(t, svc)
	policy.fields["title"] = authz.FieldDecision{Read: "redact", Create: true, Update: true}
	policy.fields["description"] = authz.FieldDecision{Read: "omit", Create: true, Update: true}
	policy.fields["priority"] = authz.FieldDecision{Read: "omit", Create: true, Update: true}
	policy.fields["created_at"] = authz.FieldDecision{Read: "omit"}
	policy.fields["deleted_at"] = authz.FieldDecision{Read: "omit"}
	for _, path := range []string{"/todos/" + id, "/entities/10/todos", "/entities/10/todos?fields=title,description,priority", "/entities/10/todos/export?format=csv", "/entities/10/todos/export?fields=title,description"} {
		response = api.Get(path)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "private title", path)
		assert.NotContains(t, response.Body.String(), "private description", path)
		assert.Contains(t, response.Body.String(), "[REDACTED]", path)
		if !strings.Contains(path, "export") {
			assert.NotContains(t, response.Body.String(), `"description":`, path)
			assert.NotContains(t, response.Body.String(), `"priority":`, path)
			assert.NotContains(t, response.Body.String(), `"created_at":`, path)
		}
	}
	for _, query := range []string{"title=eq:private", "description=like:private", "priority=eq:2", "sort=title", "sort=-created_at"} {
		for _, path := range []string{"/entities/10/todos?", "/entities/10/todos/export?"} {
			response = api.Get(path + query)
			assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
		}
	}
	for _, query := range []string{"q=private", "deleted=true"} {
		response = api.Get("/entities/10/todos?" + query)
		assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
	page, err := svc.List(todoContext(t), ListInput{Limit: 20})
	require.NoError(t, err)
	require.Len(t, page.Rows, 1)
	assert.Empty(t, page.Rows[0].Description, "unreadable data is not selected from SQL")
	assert.Zero(t, page.Rows[0].Priority)
	assert.True(t, page.Rows[0].CreatedAt.IsZero())
	assert.Equal(t, "[REDACTED]", page.Rows[0].Title)
	response = api.Get("/entities/10/todos?meta=capabilities")
	require.Equal(t, http.StatusOK, response.Code)
	assert.NotContains(t, response.Body.String(), `"name":"description"`)
}

func TestFieldPolicyRejectsExplicitWritesIncludingNullAndDefaults(t *testing.T) {
	api, svc := newTestAPIService(t)
	id, version := createTask(t, api, "original")
	policy := fieldPolicy(t, svc)
	policy.fields["description"] = authz.FieldDecision{Read: "allow"}
	policy.fields["priority"] = authz.FieldDecision{Read: "allow"}
	policy.fields["due_at"] = authz.FieldDecision{Read: "allow"}
	for _, body := range []map[string]any{
		{"title": "rejected", "description": ""},
		{"title": "rejected", "priority": 3},
		{"title": "rejected", "due_at": nil},
	} {
		response := api.Post("/entities/10/todos", body)
		assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
	for _, body := range []map[string]any{{"description": ""}, {"description": nil}, {"priority": 3}, {"due_at": nil}} {
		response := api.Patch("/todos/"+id, "If-Match: "+version, body)
		assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
	response := api.Get("/todos/" + id)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, version, response.Header().Get("ETag"), "rejected writes have no side effects")
	page, err := svc.List(todoContext(t), ListInput{Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(1), page.Total)
	_, err = svc.Create(todoContext(t), draft("internal call"))
	assert.ErrorIs(t, err, ErrForbidden, "internal callers cannot bypass field validation")
	priority := 3
	_, err = svc.Update(todoContext(t), page.Rows[0].ID, 1, Patch{Priority: &priority})
	assert.ErrorIs(t, err, ErrForbidden)
	response = api.Post("/entities/10/todos", map[string]any{"title": "allowed with server defaults"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	response = api.Patch("/todos/"+id, "If-Match: "+version, map[string]any{"title": "allowed change"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

func TestFieldPolicyImportValidatesWholeBatchAndTemplate(t *testing.T) {
	svc, _ := newTestService(t, "sl_")
	policy := fieldPolicy(t, svc)
	policy.fields["description"] = authz.FieldDecision{Read: "allow"}
	table, err := svc.importTemplate(todoContext(t), true)
	require.NoError(t, err)
	assert.Len(t, table, 2)
	assert.Len(t, table[0], len(importableColumns())-1)
	for _, input := range []grid{
		{{"title", "description"}, {"one", ""}},
		{{"title", "description"}, {"one"}, {"two", "hidden"}},
	} {
		drafts, rowErrors, err := parseImport(todoContext(t), input, ImportRequest{}.Limits())
		require.NoError(t, err)
		require.Empty(t, rowErrors)
		count, err := svc.ImportAll(todoContext(t), drafts)
		assert.ErrorIs(t, err, ErrForbidden)
		assert.Zero(t, count)
	}
	page, err := svc.List(todoContext(t), ListInput{Limit: 20})
	require.NoError(t, err)
	assert.Zero(t, page.Total)
	drafts, rowErrors, err := parseImport(todoContext(t), grid{{"title"}, {"accepted"}}, ImportRequest{}.Limits())
	require.NoError(t, err)
	require.Empty(t, rowErrors)
	count, err := svc.ImportAll(todoContext(t), drafts)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	policy.fields["title"] = authz.FieldDecision{Read: "allow"}
	_, err = svc.importTemplate(todoContext(t), false)
	assert.ErrorIs(t, err, ErrForbidden)
}

func TestFieldPolicyFailsClosedWithoutProviderOrDecision(t *testing.T) {
	api, svc := newTestAPIService(t)
	id, _ := createTask(t, api, "secret")
	for _, provider := range []authz.FieldPermissionChecker{nil, &todoPolicy{err: errors.New("offline")}, &todoPolicy{fields: map[string]authz.FieldDecision{}}} {
		svc.fields = provider
		response := api.Get("/todos/" + id)
		assert.Contains(t, []int{http.StatusForbidden, http.StatusServiceUnavailable}, response.Code)
		assert.NotContains(t, response.Body.String(), "secret")
		_, err := svc.List(todoContext(t), ListInput{Limit: 20, RawQuery: url.Values{}})
		assert.Error(t, err)
		_, err = svc.Create(todoContext(t), draft("cannot write"))
		assert.Error(t, err)
	}
}
