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
		_, err := applySCIMFilter(db.Table("iam_scim_resources AS resource"), filter, ResourceUser, "sqlite", "")
		require.NoError(t, err, filter)
	}
	_, err = applySCIMFilter(db.Table("iam_scim_resources AS resource"), `members[value eq "`+wireID("principal")+`"]`, ResourceGroup, "sqlite", "sl_")
	require.NoError(t, err)

	for _, filter := range []string{
		`unknown eq "x"`, GroupSchema + `:displayName eq "x"`, `meta.created co "2026"`,
		`active gt true`, `emails[type eq "work"]`, strings.Repeat("x", maxFilterBytes+1),
	} {
		_, err := applySCIMFilter(db.Table("iam_scim_resources AS resource"), filter, ResourceUser, "sqlite", "")
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
	condition, args, err := compileMemberFilter(scimfilter.EQ, wireID("one"), "sl_")
	require.NoError(t, err)
	assert.Contains(t, condition, "sl_iam_group_members")
	assert.Equal(t, []any{wireID("one")}, args)
	condition, _, err = compileMemberFilter(scimfilter.NE, wireID("one"), "")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(condition, "NOT "))
	_, _, err = compileMemberFilter(scimfilter.CO, wireID("one"), "")
	assert.ErrorIs(t, err, ErrInvalidFilter)
	condition, args, err = compileMemberFilter(scimfilter.PR, nil, "sl_")
	require.NoError(t, err)
	assert.Contains(t, condition, "sl_iam_group_members")
	assert.Empty(t, args)
	_, _, err = compileMemberFilter(scimfilter.EQ, nil, "")
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
		_, err := applySCIMFilter(db.Table("iam_scim_resources AS resource"), filter, ResourceUser, "sqlite", "")
		assert.ErrorIs(t, err, ErrInvalidFilter, filter)
	}
}
