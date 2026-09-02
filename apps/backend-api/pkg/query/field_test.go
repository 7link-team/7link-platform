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

// TestOperatorsRequireAValue covers the filter that silently does nothing:
// like: with no comparand builds LIKE '%%' and matches every row, and in:
// builds an empty list that matches none.
func TestOperatorsRequireAValue(t *testing.T) {
	for _, raw := range []string{"like:", "eq:", "in:", "notEq:"} {
		_, err := everyOperator().Parse(url.Values{"text": {raw}})
		assert.ErrorContains(t, err, "needs a value", "text=%s", raw)
	}
}

// TestIntRangeAppliesToListMembers guards the bound the descriptor advertises.
// Rejecting an out-of-range value for eq while accepting it inside in: would
// make the documented range untrue for half the operators.
func TestIntRangeAppliesToListMembers(t *testing.T) {
	fields := New(Int("number", field.NewInt("t", "number")).Ops(Eq, In).Range(1, 5))

	_, err := fields.Parse(url.Values{"number": {"in:2,99"}})
	assert.ErrorContains(t, err, "must be between 1 and 5")

	_, err = fields.Parse(url.Values{"number": {"in:2,4"}})
	assert.NoError(t, err)
}

// TestNullableIsHonouredByEveryKind ties Nullable to the builders. A field that
// advertises isNull in its descriptor and then rejects it at parse time is a
// contract the client cannot use.
func TestNullableIsHonouredByEveryKind(t *testing.T) {
	fields := New(
		String("text", field.NewString("t", "text")).Ops(Eq).Nullable(),
		Enum("choice", field.NewString("t", "choice"), []string{"a"}).Ops(Eq).Nullable(),
		Int("number", field.NewInt("t", "number")).Ops(Eq).Nullable(),
		Time("moment", field.NewTime("t", "moment")).Ops(Eq).Nullable(),
		ID("ref", field.NewField("t", "ref"), func(raw string) (driver.Valuer, error) {
			return nil, nil
		}).Ops(Eq).Nullable(),
	)
	for _, name := range []string{"text", "choice", "number", "moment", "ref"} {
		for _, value := range []string{"isNull:true", "isNull:false"} {
			exprs, err := fields.Parse(url.Values{name: {value}})
			require.NoError(t, err, "%s=%s", name, value)
			assert.Len(t, exprs, 1)
		}
		_, err := fields.Parse(url.Values{name: {"isNull:maybe"}})
		assert.ErrorContains(t, err, "must be true or false", name)
	}
}

// TestEmptyIsHonouredByTextFields covers the operator that exists because an
// empty value can no longer be smuggled in as a comparand.
func TestEmptyIsHonouredByTextFields(t *testing.T) {
	fields := New(
		String("text", field.NewString("t", "text")).Ops(Eq).Empty(),
		Enum("choice", field.NewString("t", "choice"), []string{"a"}).Ops(Eq).Empty(),
	)
	for _, name := range []string{"text", "choice"} {
		for _, value := range []string{"isEmpty:true", "isEmpty:false"} {
			exprs, err := fields.Parse(url.Values{name: {value}})
			require.NoError(t, err, "%s=%s", name, value)
			assert.Len(t, exprs, 1)
		}
		_, err := fields.Parse(url.Values{name: {"isEmpty:maybe"}})
		assert.ErrorContains(t, err, "must be true or false", name)
	}
}

// TestEmptyRefusesNonTextFields keeps the declaration honest at wiring time. A
// number, a timestamp and an identifier have no empty distinct from null, so
// offering isEmpty on one would advertise an operator the field must reject.
func TestEmptyRefusesNonTextFields(t *testing.T) {
	assert.Panics(t, func() { Int("number", field.NewInt("t", "number")).Empty() })
	assert.Panics(t, func() { Time("moment", field.NewTime("t", "moment")).Empty() })
	assert.Panics(t, func() {
		ID("ref", field.NewField("t", "ref"), func(string) (driver.Valuer, error) { return nil, nil }).Empty()
	})
}

// TestEmptyIsDescribedToClients ties the operator to the descriptor a filter UI
// is built from.
func TestEmptyIsDescribedToClients(t *testing.T) {
	described := New(String("text", field.NewString("t", "text")).Ops(Eq).Empty()).Describe()
	require.Len(t, described.Filters, 1)
	assert.Contains(t, described.Filters[0].Ops, string(IsEmpty))
}

// TestValuesThatNormalizeToNothingAreRejected covers the ordering the plain
// empty-value rule cannot see. splitList trims and drops blank entries, and
// EscapeLike removes wildcards, so a value that is non-empty when it is checked
// can still be empty by the time it reaches SQL — an empty IN () that matches
// nothing, or a LIKE '%%' that matches everything.
func TestValuesThatNormalizeToNothingAreRejected(t *testing.T) {
	fields := everyOperator()

	for _, raw := range []string{"in:,,,", "in:   ", "in: , ", "notIn:,"} {
		_, err := fields.Parse(url.Values{"text": {raw}})
		assert.ErrorContains(t, err, "needs at least one value", "text=%s", raw)
	}
	for _, raw := range []string{"like:%", "like:___", "like:%_%", `like:\`} {
		_, err := fields.Parse(url.Values{"text": {raw}})
		assert.ErrorContains(t, err, "other than wildcards", "text=%s", raw)
	}

	// The surrounding forms still work: a list with one usable member, and a
	// pattern that carries a wildcard alongside real text.
	for _, raw := range []string{"in:a,,b", "in: a ", "like:a%b"} {
		exprs, err := fields.Parse(url.Values{"text": {raw}})
		require.NoError(t, err, "text=%s", raw)
		assert.Len(t, exprs, 1)
	}
}

// TestIdentifierParseMustProduceAValue covers the parse function this package
// cannot see inside. A module's parser may read "" or "null" as "no
// identifier" and return a nil value with a nil error, and a nil comparand is
// not "no rows": gorm renders Eq(nil) as IS NULL and Neq(nil) as IS NOT NULL,
// so on a non-nullable column one silently matches nothing and the other
// silently matches everything.
func TestIdentifierParseMustProduceAValue(t *testing.T) {
	fields := New(ID("ref", field.NewField("t", "ref"), func(raw string) (driver.Valuer, error) {
		if raw == "7" {
			return idValue(7), nil
		}
		return nil, nil // "not an identifier", reported as neither value nor error
	}).Ops(Eq, NotEq, In, NotIn))

	for _, raw := range []string{"eq:0", "eq:null", "notEq:null", "in:null", "in:7,null", "notIn:0"} {
		_, err := fields.Parse(url.Values{"ref": {raw}})
		assert.ErrorContains(t, err, "identifier", "ref=%s", raw)
	}
	exprs, err := fields.Parse(url.Values{"ref": {"eq:7"}})
	require.NoError(t, err)
	assert.Len(t, exprs, 1)
}

// TestAnUnimplementedOperatorIsRejectedNotSubstituted is the companion to
// TestEveryAdvertisedOperatorIsImplemented, in the other direction. Enum and ID
// used to end their single-value path with a bare "return Eq", so an operator
// with no case became equality — answering a question the caller did not ask,
// which is worse than refusing. Every builder falls through to unsupported.
func TestAnUnimplementedOperatorIsRejectedNotSubstituted(t *testing.T) {
	identifiers := func(string) (driver.Valuer, error) { return idValue(1), nil }
	for name, probe := range map[string]struct {
		fields *Fields
		param  string
		value  string
	}{
		"string": {New(String("text", field.NewString("t", "text")).Ops(Gt)), "text", "gt:x"},
		"enum":   {New(Enum("choice", field.NewString("t", "choice"), []string{"a"}).Ops(Gt)), "choice", "gt:a"},
		"int":    {New(Int("num", field.NewInt("t", "num")).Ops(Like)), "num", "like:1"},
		"time":   {New(Time("moment", field.NewTime("t", "moment")).Ops(Like)), "moment", "like:2026-01-02"},
		"id":     {New(ID("ref", field.NewField("t", "ref"), identifiers).Ops(Gt)), "ref", "gt:1"},
	} {
		_, err := probe.fields.Parse(url.Values{probe.param: {probe.value}})
		assert.ErrorContains(t, err, "is not supported", "%s: %s", name, probe.value)
	}
}

// TestListOperatorsAreNotInterchangeable guards a silent inversion: Int and ID
// routed every list operator through In, so a field declaring notIn returned
// exactly the rows it was asked to exclude.
//
// The values are pairs because gorm folds a one-element list into = or <>,
// which is correct but hides which operator produced it.
func TestListOperatorsAreNotInterchangeable(t *testing.T) {
	identifiers := func(raw string) (driver.Valuer, error) { return idValue(1), nil }
	for name, probe := range map[string]struct {
		fields *Fields
		param  string
		list   string
	}{
		"int":    {New(Int("num", field.NewInt("t", "num")).Ops(In, NotIn)), "num", "1,2"},
		"id":     {New(ID("ref", field.NewField("t", "ref"), identifiers).Ops(In, NotIn)), "ref", "1,2"},
		"string": {New(String("text", field.NewString("t", "text")).Ops(In, NotIn)), "text", "x,y"},
		"enum": {New(Enum("choice", field.NewString("t", "choice"), []string{"a", "b"}).
			Ops(In, NotIn)), "choice", "a,b"},
	} {
		in := sqlOf(t, probe.fields, probe.param, "in:"+probe.list)
		notIn := sqlOf(t, probe.fields, probe.param, "notIn:"+probe.list)
		assert.NotEqual(t, in, notIn, "%s: in and notIn build the same predicate", name)
		assert.Contains(t, notIn, "NOT IN", "%s: notIn did not build NOT IN", name)
		assert.NotContains(t, in, "NOT IN", "%s: in built an exclusion", name)
	}
}
