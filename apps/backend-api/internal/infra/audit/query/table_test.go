package query

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		DryRun: true,
	})
	require.NoError(t, err)
	return db
}

// Bind is the single place this module names its table, so a prefixed binding
// must differ from an unprefixed one by exactly the prefix.
func TestBindAppliesPrefix(t *testing.T) {
	db := testDB(t)
	plain := Bind(db, "").TableName()
	prefixed := Bind(db, "wp_").TableName()

	assert.NotEqual(t, plain, prefixed)
	assert.Equal(t, "wp_"+plain, prefixed)
}

// The generated fields carry the table as a qualifier, so the rendered SQL must
// name the prefixed table in the FROM clause and in the predicate alike. A
// plain NotContains would not do: the base name is a substring of the prefixed
// one, so the prefixed occurrences are removed before looking for a bare one.
func TestBindQualifiesColumnsWithPrefixedTable(t *testing.T) {
	db := testDB(t)
	base := Bind(db, "").TableName()

	q := Bind(db, "wp_")
	statement := q.WithContext(t.Context()).Where(q.Operation.Eq("x")).UnderlyingDB().
		Find(&[]struct{}{}).Statement
	sql := statement.SQL.String()

	require.Contains(t, sql, "wp_"+base, "the prefixed table must be named")
	assert.NotContains(t, strings.ReplaceAll(sql, "wp_"+base, ""), base,
		"no reference may fall back to the unprefixed table")
}
