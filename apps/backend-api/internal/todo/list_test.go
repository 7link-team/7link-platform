package todo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"7link.com/platform/internal/todo/model"
	"7link.com/platform/internal/todo/query"
	platformquery "7link.com/platform/pkg/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seed creates tasks in a known order.
func seed(t *testing.T, svc *service, drafts ...Draft) []*model.TodoRow {
	t.Helper()
	rows := make([]*model.TodoRow, 0, len(drafts))
	for _, d := range drafts {
		row, err := svc.Create(todoContext(t), d)
		require.NoError(t, err)
		rows = append(rows, row)
	}
	return rows
}

func titles(rows []*model.TodoRow) []string {
	out := make([]string, len(rows))
	for index, row := range rows {
		out[index] = row.Title
	}
	return out
}

// query builds a list request the way the transport does: the declared
// transport parameters on the struct, everything else parsed from the raw
// query against the module's contract.
func listRequest(t *testing.T, raw string) ListInput {
	t.Helper()
	values, err := url.ParseQuery(raw)
	require.NoError(t, err)

	in := ListInput{Limit: 20, RawQuery: values}
	in.Search = values.Get("search")
	in.Sort = values.Get("sort")
	in.Fields = values.Get("fields")
	in.Deleted = values.Get("deleted") == "true"
	return in
}

func listing(t *testing.T, svc *service, raw string) Page {
	t.Helper()
	page, err := svc.List(todoContext(t), listRequest(t, raw))
	require.NoError(t, err)
	return page
}

func listError(t *testing.T, svc *service, raw string) platformquery.Error {
	t.Helper()
	_, err := svc.List(todoContext(t), listRequest(t, raw))
	var queryErr platformquery.Error
	require.ErrorAs(t, err, &queryErr, "expected a contract violation for %q", raw)
	return queryErr
}

func TestListReturnsNewestFirstByDefault(t *testing.T) {
	svc, _ := newTestService(t, "")
	seed(t, svc, draft("first"), draft("second"), draft("third"))

	page := listing(t, svc, "")
	assert.Equal(t, []string{"third", "second", "first"}, titles(page.Rows))
	assert.Equal(t, int64(3), page.Total)
}

func TestListFiltersByStatus(t *testing.T) {
	svc, _ := newTestService(t, "")
	doing := draft("in progress")
	doing.Status = StatusDoing
	seed(t, svc, draft("open one"), doing, draft("open two"))

	page := listing(t, svc, "status=eq:doing")
	assert.Equal(t, []string{"in progress"}, titles(page.Rows))
	assert.Equal(t, int64(1), page.Total, "total describes the filter, not the page")

	assert.Len(t, listing(t, svc, "status=in:open,doing").Rows, 3)
	assert.Len(t, listing(t, svc, "status=notEq:open").Rows, 1)
}

func TestListFiltersByPriorityRange(t *testing.T) {
	svc, _ := newTestService(t, "")
	for _, p := range []int{1, 3, 5} {
		d := draft("p" + string(rune('0'+p)))
		d.Priority = p
		seed(t, svc, d)
	}

	assert.Len(t, listing(t, svc, "priority=gte:3").Rows, 2)
	assert.Len(t, listing(t, svc, "priority=lte:3").Rows, 2)
	assert.Len(t, listing(t, svc, "priority=eq:3").Rows, 1)
	// Two operators on one field are a range; the same one twice is rejected.
	assert.Len(t, listing(t, svc, "priority=gte:2&priority=lte:4").Rows, 1)
}

func TestListFiltersByDueDateRangeAndNullness(t *testing.T) {
	svc, _ := newTestService(t, "")
	early := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	late := early.Add(72 * time.Hour)

	withEarly, withLate := draft("early"), draft("late")
	withEarly.DueAt, withLate.DueAt = &early, &late
	seed(t, svc, withEarly, withLate, draft("undated"))

	assert.Equal(t, []string{"undated"}, titles(listing(t, svc, "due_at=isNull:true").Rows))
	assert.Len(t, listing(t, svc, "due_at=isNull:false").Rows, 2)

	// A timestamp carries colons of its own; only the first one separates.
	encoded := url.Values{"due_at": {"lte:" + early.Format(time.RFC3339)}}.Encode()
	assert.Equal(t, []string{"early"}, titles(listing(t, svc, encoded).Rows))
}

func TestListSearchesTitleAndDescription(t *testing.T) {
	svc, _ := newTestService(t, "")
	described := draft("unrelated title")
	described.Description = "mentions reimbursement"
	seed(t, svc, draft("reimbursement report"), described, draft("something else"))

	assert.Len(t, listing(t, svc, "search=reimbursement").Rows, 2, "search spans title and description")
	assert.Equal(t, []string{"reimbursement report"},
		titles(listing(t, svc, "title=like:reimbursement").Rows))
}

func TestListExcludesDeletedUnlessAsked(t *testing.T) {
	svc, _ := newTestService(t, "")
	rows := seed(t, svc, draft("kept"), draft("removed"))
	require.NoError(t, svc.Delete(todoContext(t), rows[1].ID, rows[1].Version))

	assert.Equal(t, []string{"kept"}, titles(listing(t, svc, "").Rows))
	assert.Len(t, listing(t, svc, "deleted=true").Rows, 2)
}

// TestListPagesWithoutDuplicatingOrSkipping is why the identifier is appended
// to every ordering: these rows share a priority, so without a unique final key
// paging would repeat or lose rows with nothing reported.
func TestListPagesWithoutDuplicatingOrSkipping(t *testing.T) {
	svc, _ := newTestService(t, "")
	for index := range 10 {
		d := draft("task" + string(rune('a'+index)))
		d.Priority = 3
		seed(t, svc, d)
	}

	seen := map[string]bool{}
	for offset := 0; offset < 10; offset += 3 {
		in := listRequest(t, "sort=priority")
		in.Offset, in.Limit = offset, 3
		page, err := svc.List(todoContext(t), in)
		require.NoError(t, err)
		assert.Equal(t, int64(10), page.Total)
		for _, row := range page.Rows {
			assert.False(t, seen[row.Title], "%s appeared on two pages", row.Title)
			seen[row.Title] = true
		}
	}
	assert.Len(t, seen, 10, "every row must appear exactly once across the pages")
}

func TestListOrdersNullsAfterValues(t *testing.T) {
	svc, _ := newTestService(t, "")
	due := time.Now().UTC().Add(24 * time.Hour)
	dated := draft("dated")
	dated.DueAt = &due
	seed(t, svc, draft("undated"), dated)

	// The three dialects disagree on where nulls land by default, so the
	// ordering names it and rows with a value come first either way.
	assert.Equal(t, []string{"dated", "undated"}, titles(listing(t, svc, "sort=due_at").Rows))
	assert.Equal(t, []string{"dated", "undated"}, titles(listing(t, svc, "sort=-due_at").Rows))
}

func TestListRejectsContractViolations(t *testing.T) {
	svc, _ := newTestService(t, "")
	for name, tc := range map[string]struct{ raw, field string }{
		"missing operator":  {"status=open", "status"},
		"unknown parameter": {"nonsense=eq:1", "nonsense"},
		"unsupported op":    {"status=gte:open", "status"},
		"repeated op":       {"status=eq:open&status=eq:done", "status"},
		"value outside set": {"status=eq:archived", "status"},
		"out of range":      {"priority=eq:9", "priority"},
		"unknown sort":      {"sort=nonsense", "sort"},
		"unknown field":     {"fields=nonsense", "fields"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.field, listError(t, svc, tc.raw).Field)
		})
	}
}

func TestListSelectsOnlyRequestedFields(t *testing.T) {
	svc, _ := newTestService(t, "")
	seed(t, svc, draft("task"))

	page := listing(t, svc, "fields=title,status")
	require.Len(t, page.Rows, 1)
	assert.True(t, page.Rendered["title"])
	assert.True(t, page.Rendered["id"], "id is always rendered")
	assert.False(t, page.Rendered["description"])
	assert.False(t, page.Rendered["version"], "version is read for the ETag but not rendered")
	assert.NotZero(t, page.Rows[0].Version, "version is still read from the database")
}

// TestCapabilitiesDescribeWhatTheFilterAccepts is what a client builds its
// filter interface from; it comes from the same declaration that parses.
func TestCapabilitiesDescribeWhatTheFilterAccepts(t *testing.T) {
	svc, _ := newTestService(t, "")
	page := listing(t, svc, "")
	require.NotNil(t, page.Capabilities)

	ops := map[string][]string{}
	for _, filter := range page.Capabilities.Filters {
		ops[filter.Field] = filter.Ops
	}
	assert.Equal(t, []string{"eq", "notEq", "in"}, ops["status"])
	assert.Contains(t, ops["due_at"], "isNull")
	assert.Contains(t, page.Capabilities.Sortable, "due_at")
	assert.Contains(t, page.Capabilities.Selectable, "title")

	for _, filter := range page.Capabilities.Filters {
		switch filter.Field {
		case "status":
			assert.Equal(t, StatusStrings(), filter.Values)
		case "priority":
			require.NotNil(t, filter.Min)
			assert.Equal(t, MinPriority, *filter.Min)
			assert.Equal(t, MaxPriority, *filter.Max)
		}
	}
}

func TestActorFilterAcceptsOnlyRealIdentifiers(t *testing.T) {
	svc, _ := newTestService(t, "")
	// The audit columns hold platform snowflakes; zero means "unknown" and can
	// never name an actor, so it is rejected rather than matching every row
	// whose actor is unset.
	assert.Equal(t, "created_by", listError(t, svc, "created_by=eq:0").Field)
	assert.Equal(t, "created_by", listError(t, svc, "created_by=eq:abc").Field)

	_, err := svc.List(todoContext(t), listRequest(t, "created_by=eq:2366488506482438144"))
	require.NoError(t, err)
}

func TestRawQueryMiddlewareCarriesFiltersToTheHandler(t *testing.T) {
	// huma binds only declared struct fields, so without the middleware the
	// filters silently vanish and every list comes back unfiltered.
	var seen url.Values
	handler := RawQueryMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = rawQueryFrom(r.Context())
	}))
	request := httptest.NewRequest(http.MethodGet, "/todos?status=eq:open", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	assert.Equal(t, "eq:open", seen.Get("status"))

	// A context that never passed through the middleware yields no filters
	// rather than panicking.
	assert.Empty(t, rawQueryFrom(context.Background()))
}

// TestTimeRangesExcludeTheUpperBoundWhenAsked is why the time fields carry the
// strict bounds as well as the closed ones.
//
// A calendar month is [first, next first) — half-open. Expressed with lte the
// upper bound has to be written as the last representable instant, and any task
// in the gap between that literal and the true boundary is silently missed. The
// gap is invisible in a test that uses whole seconds, so this one puts a task
// inside it.
func TestTimeRangesExcludeTheUpperBoundWhenAsked(t *testing.T) {
	svc, _ := newTestService(t, "")
	var (
		september = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		october   = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		// Inside the month, but after the last whole second of it.
		almostOctober = october.Add(-time.Millisecond)
	)
	for title, due := range map[string]time.Time{
		"on the lower bound": september,
		"late september":     almostOctober,
		"on the upper bound": october,
	} {
		d := draft(title)
		d.DueAt = &due
		seed(t, svc, d)
	}

	rang := func(lower, upper string) []string {
		return titles(listing(t, svc, url.Values{"due_at": {lower, upper}}.Encode()).Rows)
	}
	iso := func(at time.Time) string { return at.Format(time.RFC3339Nano) }

	assert.ElementsMatch(t, []string{"on the lower bound", "late september"},
		rang("gte:"+iso(september), "lt:"+iso(october)),
		"[september, october) is the month")
	assert.ElementsMatch(t, []string{"on the lower bound", "late september", "on the upper bound"},
		rang("gte:"+iso(september), "lte:"+iso(october)),
		"lte reaches the next month's first instant")
	assert.ElementsMatch(t, []string{"late september"},
		rang("gt:"+iso(september), "lt:"+iso(october)),
		"both bounds strict")

	// The whole-second upper bound a caller is forced into without lt: the task
	// in the sub-second gap is lost, which is the defect the strict bound fixes.
	assert.ElementsMatch(t, []string{"on the lower bound"},
		rang("gte:"+iso(september), "lte:"+iso(october.Add(-time.Second))))
}

// TestSearchTermDropsAWildcardOnlyQuery covers the same normalization order the
// filter layer enforces. EscapeLike removes the wildcards, so a search of nothing but
// them would build LIKE '%%' on both columns — a full scan that matches every
// row anyway. The result set is unchanged; the pointless predicate is not built.
func TestSearchTermDropsAWildcardOnlyQuery(t *testing.T) {
	_, db := newTestService(t, "")
	cols := query.Bind(db, "")
	for _, term := range []string{"%", "___", `\`, "%_%"} {
		assert.Nil(t, searchTerm(cols, term), "search=%s", term)
	}
	assert.NotNil(t, searchTerm(cols, "task"))
	assert.NotNil(t, searchTerm(cols, "a%b"))
}

// TestFilterDescriptionIsEmpty runs the operator against a real database, so
// the predicate is verified as SQL rather than as a built expression.
func TestFilterDescriptionIsEmpty(t *testing.T) {
	svc, _ := newTestService(t, "")
	seed(t, svc,
		Draft{Title: "documented", Description: "why", Status: StatusOpen, Priority: 3},
		Draft{Title: "bare", Status: StatusOpen, Priority: 3},
	)

	titles := func(filter string) []string {
		page, err := svc.List(todoContext(t), ListInput{
			Limit: 10, RawQuery: url.Values{"description": {filter}},
		})
		require.NoError(t, err)
		out := make([]string, 0, len(page.Rows))
		for _, row := range page.Rows {
			out = append(out, row.Title)
		}
		return out
	}

	assert.Equal(t, []string{"bare"}, titles("isEmpty:true"))
	assert.Equal(t, []string{"documented"}, titles("isEmpty:false"))
}
