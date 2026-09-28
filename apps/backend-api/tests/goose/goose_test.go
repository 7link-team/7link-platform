package goose_test

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"testing"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/sql
var migrations embed.FS

type databaseFactory func(*testing.T) *sql.DB

func migrationsRoot(t *testing.T) fs.FS {
	t.Helper()
	root, err := fs.Sub(migrations, "testdata")
	require.NoError(t, err)
	return root
}

func TestMigrationsMySQLAndPostgres(t *testing.T) {
	tests := []struct {
		name    string
		dialect string
		open    databaseFactory
	}{
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			db := tt.open(t)
			root := migrationsRoot(t)
			versionTable := "goose_widgets_" + tt.name
			source := goosex.Source{FS: root, TableName: versionTable}
			upToDate, err := goosex.UpToDate(ctx, db, source, tt.dialect)
			require.NoError(t, err)
			assert.False(t, upToDate)
			require.NoError(t, goosex.Migrate(ctx, db, source, tt.dialect))
			_, err = db.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES (1, 'first')`)
			require.NoError(t, err)
			assert.Equal(t, 1, widgetCount(t, ctx, db))
			require.NoError(t, goosex.Migrate(ctx, db, source, tt.dialect))
			require.NoError(t, goosex.MigrateDown(ctx, db, source, tt.dialect))
			assertWidgetsTableMissing(t, ctx, db)
			require.NoError(t, goosex.MigrateUp(ctx, db, source, tt.dialect))
			require.NoError(t, goosex.MigrateDownTo(ctx, db, source, tt.dialect, 0))
			assertWidgetsTableMissing(t, ctx, db)
		})
	}
}

func widgetCount(t *testing.T, ctx context.Context, db *sql.DB) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM widgets`).Scan(&count))
	return count
}

func assertWidgetsTableMissing(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES (2, 'missing')`)
	assert.Error(t, err)
}
