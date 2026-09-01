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

// everyOperator declares each field type with its full operator set, so the
// table below can exercise every branch a module could reach.
func everyOperator() *Fields {
	return New(
		String("text", field.NewString("t", "text")).Ops(Eq, NotEq, Like, In, NotIn),
		Enum("choice", field.NewString("t", "choice"), []string{"a", "b"}).Ops(Eq, NotEq, In, NotIn),
		Int("number", field.NewInt("t", "number")).Ops(Eq, NotEq, Gt, Gte, Lt, Lte, In),
		Time("moment", field.NewTime("t", "moment")).Ops(Eq, Gt, Gte, Lt, Lte).Nullable(),
		ID("ref", field.NewField("t", "ref"), func(raw string) (driver.Valuer, error) {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return nil, err
			}
			return idValue(parsed), nil
		}).Ops(Eq, NotEq, In),
	)
}

// TestEveryDeclaredOperatorBuildsAPredicate walks the whole matrix: a field
// type advertising an operator it cannot build would otherwise fail only for
// the caller who first tries it.
func TestEveryDeclaredOperatorBuildsAPredicate(t *testing.T) {
	fields := everyOperator()
	for _, tc := range []struct{ param, value string }{
		{"text", "eq:hello"}, {"text", "notEq:hello"}, {"text", "like:hel"},
		{"text", "in:a,b"}, {"text", "notIn:a,b"},

		{"choice", "eq:a"}, {"choice", "notEq:b"},
		{"choice", "in:a,b"}, {"choice", "notIn:a"},

		{"number", "eq:1"}, {"number", "notEq:2"}, {"number", "gt:1"},
		{"number", "gte:1"}, {"number", "lt:9"}, {"number", "lte:9"}, {"number", "in:1,2"},

		{"moment", "eq:2026-01-02T03:04:05Z"}, {"moment", "gt:2026-01-02"},
		{"moment", "gte:2026-01-02 03:04:05"}, {"moment", "lt:2026-01-02"},
		{"moment", "lte:2026-01-02"}, {"moment", "isNull:true"}, {"moment", "isNull:false"},

		{"ref", "eq:12"}, {"ref", "notEq:12"}, {"ref", "in:12,34"},
	} {
		t.Run(tc.param+"/"+tc.value, func(t *testing.T) {
			exprs, err := fields.Parse(url.Values{tc.param: {tc.value}})
			require.NoError(t, err)
			require.Len(t, exprs, 1)
		})
	}
}

func TestEachTypeRejectsMalformedValues(t *testing.T) {
	fields := everyOperator()
	for name, raw := range map[string]url.Values{
		"int not a number":  {"number": {"eq:abc"}},
		"int list bad":      {"number": {"in:1,abc"}},
		"time unparsable":   {"moment": {"gte:31/12/2026"}},
		"isNull not a bool": {"moment": {"isNull:maybe"}},
		"id not numeric":    {"ref": {"eq:abc"}},
		"id list bad":       {"ref": {"in:12,abc"}},
		"enum outside set":  {"choice": {"eq:z"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fields.Parse(raw)
			var queryErr Error
			assert.ErrorAs(t, err, &queryErr)
		})
	}
}

func TestTimeAcceptsEveryDeclaredLayout(t *testing.T) {
	// The layouts are what exports write and what clients send; all three must
	// mean the same instant where they overlap.
	fields := everyOperator()
	for _, raw := range []string{
		"gte:2026-01-02T03:04:05Z",
		"gte:2026-01-02 03:04:05",
		"gte:2026-01-02",
	} {
		_, err := fields.Parse(url.Values{"moment": {raw}})
		require.NoError(t, err, "for %q", raw)
	}
}

func TestFieldNameIsItsParameter(t *testing.T) {
	assert.Equal(t, "text", String("text", field.NewString("t", "text")).Name())
}

func TestListValuesIgnoreBlankEntries(t *testing.T) {
	// A trailing comma is a typing artifact, not an empty value to match on.
	assert.Equal(t, []string{"a", "b"}, splitList("a, b,"))
	assert.Empty(t, splitList(" , "))
}

func TestOperatorClassification(t *testing.T) {
	assert.True(t, In.takesList())
	assert.True(t, NotIn.takesList())
	assert.False(t, Eq.takesList(), "only list operators split on commas, so a value may contain one")
	assert.True(t, IsNull.takesNoValue())
	assert.False(t, Like.takesNoValue())
}

func TestNullableOnlyAddsTheOperator(t *testing.T) {
	base := Time("moment", field.NewTime("t", "moment")).Ops(Gte)
	nullable := base.Nullable()
	assert.False(t, base.allows(IsNull))
	assert.True(t, nullable.allows(IsNull))
	assert.True(t, nullable.allows(Gte), "the declared operators survive")
}
