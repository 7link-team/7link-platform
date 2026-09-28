package provisioning

import (
	"strings"
	"testing"
	"time"

	scimfilter "github.com/scim2/filter-parser/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSCIMFilterASTCompilation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{DryRun: true})
	require.NoError(t, err)
	for _, filter := range []string{
		`userName sw "a" and (active eq true or emails[value co "@example.test"])`,
		`not (displayName eq "blocked")`,
		UserSchema + `:name.formatted pr`,
		`meta.created ge "2026-08-03T00:00:00Z"`,
	} {
		_, err := applySCIMFilter(db.Table("iam_scim_resources AS resource"), filter, ResourceUser, "sqlite", testForeignTables(t, ""))
		require.NoError(t, err, filter)
	}
	_, err = applySCIMFilter(db.Table("iam_scim_resources AS resource"), `members[value eq "`+wireID("principal")+`"]`, ResourceGroup, "sqlite", testForeignTables(t, "sl_"))
	require.NoError(t, err)

	for _, filter := range []string{
		`unknown eq "x"`, GroupSchema + `:displayName eq "x"`, `meta.created co "2026"`,
		`active gt true`, `emails[type eq "work"]`, strings.Repeat("x", maxFilterBytes+1),
	} {
		_, err := applySCIMFilter(db.Table("iam_scim_resources AS resource"), filter, ResourceUser, "sqlite", testForeignTables(t, ""))
		assert.ErrorIs(t, err, ErrInvalidFilter, filter)
	}
}

func TestSCIMComparisonUsesDialectAndTimestamp(t *testing.T) {
	for dialect, expected := range map[string]string{"sqlite": "INSTR", "mysql": "LOCATE", "postgres": "STRPOS"} {
		condition, args, err := compileComparison(filterTarget{expression: "principal.login_name"}, scimfilter.CO, "ALICE", dialect)
		require.NoError(t, err)
		assert.Contains(t, condition, expected)
		assert.Equal(t, []any{"alice"}, args)
	}
	condition, args, err := compileComparison(filterTarget{expression: "resource.created_at", timestamp: true}, scimfilter.GE, "2026-08-03T00:00:00Z", "sqlite")
	require.NoError(t, err)
	assert.Equal(t, "resource.created_at >= ?", condition)
	assert.Equal(t, []any{time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)}, args)
	_, _, err = compileComparison(filterTarget{expression: "resource.created_at", timestamp: true}, scimfilter.EQ, "not-time", "sqlite")
	assert.ErrorIs(t, err, ErrInvalidFilter)

	condition, args, err = compileComparison(filterTarget{expression: "principal.email", caseExact: true}, scimfilter.EQ, nil, "sqlite")
	require.NoError(t, err)
	assert.Equal(t, "principal.email IS NULL", condition)
	assert.Empty(t, args)
	for operator, expected := range map[scimfilter.CompareOperator]string{
		scimfilter.EQ: "column = ?", scimfilter.NE: "column <> ?", scimfilter.GT: "column > ?",
		scimfilter.GE: "column >= ?", scimfilter.LT: "column < ?", scimfilter.LE: "column <= ?",
		scimfilter.SW: "SUBSTR(column, 1, LENGTH(?)) = ?", scimfilter.EW: "SUBSTR(column, -LENGTH(?)) = ?",
	} {
		condition, args, err = comparison("column", operator, "value", "sqlite")
		require.NoError(t, err)
		assert.Equal(t, expected, condition)
		assert.NotEmpty(t, args)
	}
	_, _, err = comparison("column", scimfilter.PR, "value", "sqlite")
	assert.ErrorIs(t, err, ErrInvalidFilter)
}

func TestMemberFilterAndDialectHelpers(t *testing.T) {
	prefixed, plain := testForeignTables(t, "sl_"), testForeignTables(t, "")
	condition, args, err := compileMemberFilter(scimfilter.EQ, wireID("one"), prefixed)
	require.NoError(t, err)
	assert.Contains(t, condition, "sl_iam_group_members")
	assert.Equal(t, []any{wireID("one")}, args)
	condition, _, err = compileMemberFilter(scimfilter.NE, wireID("one"), plain)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(condition, "NOT "))
	_, _, err = compileMemberFilter(scimfilter.CO, wireID("one"), plain)
	assert.ErrorIs(t, err, ErrInvalidFilter)
	condition, args, err = compileMemberFilter(scimfilter.PR, nil, prefixed)
	require.NoError(t, err)
	assert.Contains(t, condition, "sl_iam_group_members")
	assert.Empty(t, args)
	_, _, err = compileMemberFilter(scimfilter.EQ, nil, plain)
	assert.ErrorIs(t, err, ErrInvalidFilter)
	assert.Empty(t, normalizeDialect(nil))
	condition, _, err = compileComparison(filterTarget{expression: "active", boolean: true}, scimfilter.PR, nil, "sqlite")
	require.NoError(t, err)
	assert.Equal(t, "1 = 1", condition)
	condition, _, err = compileComparison(filterTarget{expression: "active", boolean: true}, scimfilter.EQ, false, "sqlite")
	require.NoError(t, err)
	assert.Equal(t, "NOT active", condition)
	condition, _, err = compileComparison(filterTarget{expression: "email", caseExact: true}, scimfilter.NE, nil, "sqlite")
	require.NoError(t, err)
	assert.Equal(t, "email IS NOT NULL", condition)
	_, _, err = compileComparison(filterTarget{expression: "active", boolean: true}, scimfilter.GT, true, "sqlite")
	assert.ErrorIs(t, err, ErrInvalidFilter)
}

// TestSCIMSubstringOperatorsRejectEmptyValues stops a caller from turning a
// scoped SCIM query into an unfiltered listing: an empty substring matches
// every row under co and sw, and SUBSTR(column, -0) is dialect-defined for ew.
func TestSCIMSubstringOperatorsRejectEmptyValues(t *testing.T) {
	target := filterTarget{expression: "principal.login_name"}
	for _, operator := range []scimfilter.CompareOperator{scimfilter.CO, scimfilter.SW, scimfilter.EW} {
		for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
			_, _, err := compileComparison(target, operator, "", dialect)
			assert.ErrorIs(t, err, ErrInvalidFilter, "%s on %s", operator, dialect)
		}
	}

	// Exact and ordering comparisons keep accepting the empty string: matching
	// a stored empty value is a legitimate query, not a bypass.
	condition, args, err := compileComparison(target, scimfilter.EQ, "", "sqlite")
	require.NoError(t, err)
	assert.Equal(t, "LOWER(principal.login_name) = ?", condition)
	assert.Equal(t, []any{""}, args)

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{DryRun: true})
	require.NoError(t, err)
	for _, filter := range []string{`userName co ""`, `userName sw ""`, `userName ew ""`} {
		_, err := applySCIMFilter(db.Table("iam_scim_resources AS resource"), filter, ResourceUser, "sqlite", testForeignTables(t, ""))
		assert.ErrorIs(t, err, ErrInvalidFilter, filter)
	}
}

// TestSCIMFilterComplexityLimitsRejectDoSExpressions proves both halves of the
// compiler's complexity guard on their own. A deeply nested negation trips the
// depth cap while staying far under the node cap, and a wide balanced
// conjunction trips the node cap at depth six; expressions one step inside
// each limit still compile, so neither cap can be satisfied by rejecting
// everything.
func TestSCIMFilterComplexityLimitsRejectDoSExpressions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{DryRun: true})
	require.NoError(t, err)
	compile := func(filter string) error {
		_, err := applySCIMFilter(db.Table("iam_scim_resources AS resource"), filter, ResourceUser, "sqlite", testForeignTables(t, ""))
		return err
	}
	nested := func(depth int) string {
		return strings.Repeat("not (", depth) + "userName pr" + strings.Repeat(")", depth)
	}
	var balanced func(height int) string
	balanced = func(height int) string {
		if height == 0 {
			return "userName pr"
		}
		return "(" + balanced(height-1) + " and " + balanced(height-1) + ")"
	}

	// 16 negations put the attribute at depth 16 with 17 nodes; 17 negations
	// push it to depth 17, still only 18 nodes, so only the depth cap can fire.
	require.NoError(t, compile(nested(16)))
	assert.ErrorIs(t, compile(nested(17)), ErrInvalidFilter, "the depth cap must reject a deeper expression")

	// Height 5 is 63 nodes, height 6 is 127 nodes, both far inside the depth
	// cap, so only the node cap can reject the wider one.
	require.NoError(t, compile(balanced(5)))
	assert.ErrorIs(t, compile(balanced(6)), ErrInvalidFilter, "the node cap must reject a wider expression")
}

// TestSCIMFilterRejectsMalformedLogicalAndValuePathNodes keeps a malformed part
// of a filter from being silently dropped: a bad operand on either side of a
// logical node, an operator the compiler does not implement, a value path on an
// attribute this module cannot join, and a compound value filter all have to
// fail the whole filter rather than widen the result set.
func TestSCIMFilterRejectsMalformedLogicalAndValuePathNodes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{DryRun: true})
	require.NoError(t, err)
	for _, filter := range []string{
		`unknown eq "x" and userName pr`,
		`userName pr and unknown eq "x"`,
		`groups[value eq "x"]`,
		`emails[value eq "a" and type eq "work"]`,
	} {
		_, err := applySCIMFilter(db.Table("iam_scim_resources AS resource"), filter, ResourceUser, "sqlite", testForeignTables(t, ""))
		assert.ErrorIs(t, err, ErrInvalidFilter, filter)
	}

	present := func() *scimfilter.AttributeExpression {
		return &scimfilter.AttributeExpression{AttributePath: scimfilter.AttributePath{AttributeName: "userName"}, Operator: scimfilter.PR}
	}
	nodes := 0
	_, _, err = compileFilterExpression(&scimfilter.LogicalExpression{Operator: scimfilter.LogicalOperator("xor"), Left: present(), Right: present()},
		ResourceUser, "sqlite", testForeignTables(t, ""), false, 0, &nodes)
	assert.ErrorIs(t, err, ErrInvalidFilter, "an unimplemented logical operator must not fall through to AND")

	nodes = 0
	_, _, err = compileFilterExpression(nil, ResourceUser, "sqlite", testForeignTables(t, ""), false, 0, &nodes)
	assert.ErrorIs(t, err, ErrInvalidFilter, "an unrecognized node must fail closed, not compile to nothing")
}

// TestSCIMComparisonRejectsUncomparableValues stops a null or non-string
// compare value from reaching SQL: ordering against null is never satisfiable
// and a numeric literal would be bound against a text column.
func TestSCIMComparisonRejectsUncomparableValues(t *testing.T) {
	target := filterTarget{expression: "resource.external_id", caseExact: true}
	for _, operator := range []scimfilter.CompareOperator{scimfilter.GT, scimfilter.GE, scimfilter.LT, scimfilter.LE, scimfilter.CO} {
		_, _, err := compileComparison(target, operator, nil, "sqlite")
		assert.ErrorIs(t, err, ErrInvalidFilter, "null is only comparable with eq and ne: %s", operator)
	}
	for _, value := range []any{42, 3.5, []any{"x"}} {
		_, _, err := compileComparison(target, scimfilter.EQ, value, "sqlite")
		assert.ErrorIs(t, err, ErrInvalidFilter)
	}

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{DryRun: true})
	require.NoError(t, err)
	_, err = applySCIMFilter(db.Table("iam_scim_resources AS resource"), `userName eq 42`, ResourceUser, "sqlite", testForeignTables(t, ""))
	assert.ErrorIs(t, err, ErrInvalidFilter)
}

// TestSCIMGroupFilterResolvesOnlyGroupSchemaAttributes proves a Group listing
// compiles displayName against the group table and accepts only the Group
// schema URI, so a User-schema prefix or a user-only attribute cannot be
// smuggled into a group query.
func TestSCIMGroupFilterResolvesOnlyGroupSchemaAttributes(t *testing.T) {
	compile := func(raw string) (string, []any, error) {
		expression, err := scimfilter.ParseFilter([]byte(raw))
		require.NoError(t, err, raw)
		nodes := 0
		return compileFilterExpression(expression, ResourceGroup, "sqlite", testForeignTables(t, ""), false, 0, &nodes)
	}
	condition, args, err := compile(GroupSchema + `:displayName eq "Team"`)
	require.NoError(t, err)
	assert.Equal(t, "LOWER(scim_group.name) = ?", condition)
	assert.Equal(t, []any{"team"}, args)

	for _, raw := range []string{UserSchema + `:displayName eq "Team"`, `userName eq "team"`, `emails.value pr`} {
		_, _, err := compile(raw)
		assert.ErrorIs(t, err, ErrInvalidFilter, raw)
	}
}
