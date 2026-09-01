package query

import (
	"database/sql/driver"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gen/field"
)

// A small contract standing in for a module's declaration.
func testFields() *Fields {
	title := field.NewString("todos", "title")
	status := field.NewString("todos", "status")
	priority := field.NewInt("todos", "priority")
	dueAt := field.NewTime("todos", "due_at")
	owner := field.NewField("todos", "created_by")

	return New(
		String("title", title).Ops(Eq, Like),
		Enum("status", status, []string{"open", "doing", "done"}).Ops(Eq, In),
		Int("priority", priority).Ops(Eq, Gte, Lte).Range(1, 5),
		Time("due_at", dueAt).Ops(Gte, Lte).Nullable(),
		ID("created_by", owner, func(raw string) (driver.Valuer, error) {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed <= 0 {
				return nil, err
			}
			return idValue(parsed), nil
		}).Ops(Eq, In),
	).Sortable(map[string]SortKey{
		"created_at": {Column: field.NewTime("todos", "created_at")},
		"priority":   {Column: priority},
		"due_at":     {Column: dueAt, Nullable: true},
	}).Selectable(map[string]field.Expr{
		"id": field.NewField("todos", "id"), "title": title, "status": status,
	}).Reserve("sort", "fields", "offset", "limit")
}

type idValue int64

func (v idValue) Value() (driver.Value, error) { return int64(v), nil }

func parse(t *testing.T, raw string) ([]field.Expr, error) {
	t.Helper()
	values, err := url.ParseQuery(raw)
	require.NoError(t, err)
	return testFields().Parse(values)
}

func TestParseBuildsPredicates(t *testing.T) {
	exprs, err := parse(t, "status=eq:open&priority=gte:3&title=like:report")
	require.NoError(t, err)
	assert.Len(t, exprs, 3)
}

// TestOperatorIsMandatory is the decision that makes the syntax unambiguous: a
// value beginning with "eq:" is data, never an operator, because the operator
// position is always filled by the client.
func TestOperatorIsMandatory(t *testing.T) {
	_, err := parse(t, "title=report")
	var queryErr Error
	require.ErrorAs(t, err, &queryErr)
	assert.Equal(t, "title", queryErr.Field)
	assert.Contains(t, queryErr.Message, "<operator>:<value>")
}

// TestOnlyFirstColonSeparates covers both a timestamp and a value that itself
// looks like an operator.
func TestOnlyFirstColonSeparates(t *testing.T) {
	exprs, err := parse(t, "due_at=gte:2026-09-01T00:00:00Z")
	require.NoError(t, err)
	require.Len(t, exprs, 1)

	// A user searching for the literal text "eq:123" writes it as the value of
	// an explicit operator, and it stays data.
	exprs, err = parse(t, url.Values{"title": {"like:eq:123 like:456"}}.Encode())
	require.NoError(t, err)
	require.Len(t, exprs, 1)
}

func TestUnknownParameterIsRejected(t *testing.T) {
	// Ignoring an unrecognized parameter is how a typo silently returns
	// unfiltered data, and how an injected parameter passes unnoticed.
	_, err := parse(t, "nonsense=eq:1")
	var queryErr Error
	require.ErrorAs(t, err, &queryErr)
	assert.Equal(t, "nonsense", queryErr.Field)
}

func TestReservedParametersAreNotFilters(t *testing.T) {
	_, err := parse(t, "sort=-created_at&limit=20&offset=0&fields=id,title")
	require.NoError(t, err, "transport parameters are consumed elsewhere")
}

func TestOperatorMustBeAllowedForTheField(t *testing.T) {
	// A status is never greater than another status.
	_, err := parse(t, "status=gte:open")
	var queryErr Error
	require.ErrorAs(t, err, &queryErr)
	assert.Contains(t, queryErr.Message, "eq")
}

func TestRepeatedOperatorIsRejected(t *testing.T) {
	// Last-one-wins is how an injected duplicate overrides a real filter.
	_, err := parse(t, "status=eq:open&status=eq:done")
	var queryErr Error
	require.ErrorAs(t, err, &queryErr)
	assert.Contains(t, queryErr.Message, "repeated")

	// Different operators on one field are a legitimate range.
	_, err = parse(t, "priority=gte:2&priority=lte:4")
	require.NoError(t, err)
}

func TestEnumRejectsValueOutsideTheSet(t *testing.T) {
	_, err := parse(t, "status=eq:archived")
	var queryErr Error
	require.ErrorAs(t, err, &queryErr)
	assert.Contains(t, queryErr.Message, "open")

	_, err = parse(t, "status=in:open,archived")
	require.ErrorAs(t, err, &queryErr)
}

func TestIntRangeIsEnforced(t *testing.T) {
	_, err := parse(t, "priority=eq:9")
	var queryErr Error
	require.ErrorAs(t, err, &queryErr)
	assert.Contains(t, queryErr.Message, "between 1 and 5")

	_, err = parse(t, "priority=eq:abc")
	require.ErrorAs(t, err, &queryErr)
}

func TestValueLengthIsCapped(t *testing.T) {
	long := "like:"
	for range MaxValueLength + 10 {
		long += "a"
	}
	_, err := parse(t, url.Values{"title": {long}}.Encode())
	var queryErr Error
	require.ErrorAs(t, err, &queryErr)
	assert.Contains(t, queryErr.Message, "at most")
}

// TestErrorsDoNotEchoCallerInput guards the one place caller data could reach a
// client's DOM: a filter error is exactly the message a UI renders.
func TestErrorsDoNotEchoCallerInput(t *testing.T) {
	payload := "<img src=x onerror=alert(1)>"
	_, err := parse(t, url.Values{"status": {"eq:" + payload}}.Encode())
	var queryErr Error
	require.ErrorAs(t, err, &queryErr)
	assert.NotContains(t, queryErr.Message, payload)
	assert.NotContains(t, queryErr.Error(), payload)
}

func TestLikeWildcardsAreRemoved(t *testing.T) {
	// A caller supplying % would widen any search into a full scan. Escaping is
	// not portable, so the wildcards are dropped.
	assert.Equal(t, "report", EscapeLike("%report%"))
	assert.Equal(t, "ab", EscapeLike("a_b"))
	assert.Equal(t, "ab", EscapeLike(`a\b`))
}

func TestOrderingAlwaysEndsWithTheTiebreak(t *testing.T) {
	tiebreak := field.NewField("todos", "id").Desc()
	order, err := testFields().Ordering("", 3, tiebreak)
	require.NoError(t, err)
	require.Len(t, order, 1, "an empty sort still orders, never nothing")

	order, err = testFields().Ordering("-priority", 3, tiebreak)
	require.NoError(t, err)
	assert.Len(t, order, 2)

	// A nullable key contributes its null-placement key first, so the three
	// dialects agree on where empty values land.
	order, err = testFields().Ordering("due_at", 3, tiebreak)
	require.NoError(t, err)
	assert.Len(t, order, 3)
}

func TestOrderingRejectsUnknownAndExcessiveKeys(t *testing.T) {
	tiebreak := field.NewField("todos", "id").Desc()
	_, err := testFields().Ordering("nonsense", 3, tiebreak)
	var queryErr Error
	require.ErrorAs(t, err, &queryErr)
	assert.Equal(t, "sort", queryErr.Field)

	_, err = testFields().Ordering("priority,due_at,created_at,priority", 3, tiebreak)
	require.ErrorAs(t, err, &queryErr)
	assert.Contains(t, queryErr.Message, "at most")
}

func TestProjectionForcesAlwaysReadColumns(t *testing.T) {
	columns, rendered, err := testFields().Projection("title", "id")
	require.NoError(t, err)
	assert.Len(t, columns, 2, "id is read even though it was not requested")
	assert.True(t, rendered["title"])
	assert.False(t, rendered["id"], "a column read for the transport is not rendered")

	_, rendered, err = testFields().Projection("")
	require.NoError(t, err)
	assert.Len(t, rendered, 3, "an empty selection renders everything")

	_, _, err = testFields().Projection("nonsense")
	var queryErr Error
	require.ErrorAs(t, err, &queryErr)
}

// TestDescribeMatchesWhatParseAccepts is what makes the descriptor safe to
// build a UI from: both come from the same declaration.
func TestDescribeMatchesWhatParseAccepts(t *testing.T) {
	fields := testFields()
	described := fields.Describe()

	require.Len(t, described.Filters, 5)
	assert.Equal(t, fields.SortableNames(), described.Sortable)
	assert.Equal(t, fields.SelectableNames(), described.Selectable)

	byField := map[string][]string{}
	for _, filter := range described.Filters {
		byField[filter.Field] = filter.Ops
	}
	// Every advertised operator must name something.
	for _, filter := range described.Filters {
		for _, op := range filter.Ops {
			assert.NotEmpty(t, op)
		}
	}
	assert.Equal(t, []string{"eq", "in"}, byField["status"])
	assert.Contains(t, byField["due_at"], "isNull", "Nullable adds the operator it documents")

	for _, filter := range described.Filters {
		switch filter.Field {
		case "status":
			assert.Equal(t, []string{"open", "doing", "done"}, filter.Values)
		case "priority":
			require.NotNil(t, filter.Min)
			assert.Equal(t, 1, *filter.Min)
			assert.Equal(t, 5, *filter.Max)
		}
	}
}
