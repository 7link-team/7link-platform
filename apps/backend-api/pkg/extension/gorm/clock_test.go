package gormx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNowMillisExpr(t *testing.T) {
	for _, dialect := range []string{"sqlite", "sqlite3", "mysql", "postgres", "postgresql", "pgsql", "pg", " PGSQL "} {
		assert.NotEmpty(t, NowMillisExpr(dialect), dialect)
	}
	assert.Panics(t, func() { NowMillisExpr("oracle") })
}
