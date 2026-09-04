package query

import (
	"database/sql/driver"
	"net/url"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// The three rows are chosen so that every gap in SQL's three-valued logic
// shows: one column with a value, one with the empty string, one with nothing
// at all. A predicate that quietly loses the third row is the failure these
// tests exist to catch, and only real SQL exhibits it — an expression built in
// memory looks correct right up until the database evaluates it against NULL.
func partitionDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec(
		`CREATE TABLE samples (id INTEGER PRIMARY KEY, text TEXT, num INT, ref BIGINT)`).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO samples VALUES (1,'x',1,10), (2,'',2,20), (3,NULL,NULL,NULL)`).Error)
	return db
}

// matching runs one filter as SQL and returns the rows it selects.
func matching(t *testing.T, db *gorm.DB, fields *Fields, param, value string) []int {
	t.Helper()
	exprs, err := fields.Parse(url.Values{param: {value}})
	require.NoError(t, err, "%s=%s", param, value)
	require.Len(t, exprs, 1, "%s=%s", param, value)

	stmt := &gorm.Statement{DB: db, Table: "samples", Clauses: map[string]clause.Clause{}}
	exprs[0].Build(stmt)
	var ids []int
	require.NoError(t, db.Raw(
		"SELECT id FROM samples WHERE "+stmt.SQL.String(), stmt.Vars...).Scan(&ids).Error)
	slices.Sort(ids)
	return ids
}

func nullableFields() *Fields {
	return New(
		String("text", field.NewString("samples", "text")).
			Ops(Eq, NotEq, In, NotIn).Nullable().Empty(),
		Int("num", field.NewInt("samples", "num")).
			Ops(Eq, NotEq, In, NotIn).Nullable(),
		ID("ref", field.NewField("samples", "ref"), func(raw string) (driver.Valuer, error) {
			switch raw {
			case "10":
				return idValue(10), nil
			case "20":
				return idValue(20), nil
			}
			return nil, nil
		}).Ops(Eq, NotEq, In, NotIn).Nullable(),
	)
}

// TestNegationPartitionsANullableColumn is the invariant behind every negative
// operator: a question and its opposite have to account for every row. SQL does
// not give that for free — NULL compares to nothing, so `col <> 'x'` and
// `col NOT IN ('x')` are UNKNOWN for a row with no value and it falls out of
// both answers. A caller asking for everything that is not urgent would
// silently lose the rows that are not anything.
//
// Adding a negative operator to this package means adding it here. The pairs
// are listed rather than derived because only a person knows which question a
// new operator is the opposite of.
func TestNegationPartitionsANullableColumn(t *testing.T) {
	db := partitionDB(t)
	fields := nullableFields()
	everyRow := []int{1, 2, 3}

	for _, pair := range []struct{ param, yes, no string }{
		{"text", "eq:x", "notEq:x"},
		{"text", "in:x", "notIn:x"},
		{"text", "isEmpty:true", "isEmpty:false"},
		{"num", "eq:1", "notEq:1"},
		{"num", "in:1", "notIn:1"},
		{"ref", "eq:10", "notEq:10"},
		{"ref", "in:10", "notIn:10"},
	} {
		yes := matching(t, db, fields, pair.param, pair.yes)
		no := matching(t, db, fields, pair.param, pair.no)

		union := append(append([]int{}, yes...), no...)
		slices.Sort(union)
		assert.Equal(t, everyRow, union,
			"%s: %s and %s must account for every row, together they are %v",
			pair.param, pair.yes, pair.no, union)
		for _, id := range yes {
			assert.NotContains(t, no, id,
				"%s: row %d answers both %s and %s", pair.param, id, pair.yes, pair.no)
		}
	}
}

// TestANotNullColumnKeepsThePlainPredicate guards the other half of the trade.
// The extra disjunct is only correct where a null is possible; on a column the
// schema forbids it from having, it is a dead branch an index cannot use, so a
// field that did not declare Nullable must not pay for it.
func TestANotNullColumnKeepsThePlainPredicate(t *testing.T) {
	db := partitionDB(t)
	plain := New(String("text", field.NewString("samples", "text")).Ops(Eq, NotEq).Empty())

	exprs, err := plain.Parse(url.Values{"text": {"notEq:x"}})
	require.NoError(t, err)
	stmt := &gorm.Statement{DB: db, Table: "samples", Clauses: map[string]clause.Clause{}}
	exprs[0].Build(stmt)
	assert.NotContains(t, stmt.SQL.String(), "IS NULL",
		"a field that did not declare Nullable should not carry a null branch")
}

// sqlOf renders one filter as SQL, for the assertions that are about the shape
// of the predicate rather than the rows it returns.
func sqlOf(t *testing.T, fields *Fields, param, value string) string {
	t.Helper()
	exprs, err := fields.Parse(url.Values{param: {value}})
	require.NoError(t, err, "%s=%s", param, value)
	require.Len(t, exprs, 1)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	stmt := &gorm.Statement{DB: db, Table: "t", Clauses: map[string]clause.Clause{}}
	exprs[0].Build(stmt)
	return stmt.SQL.String()
}
