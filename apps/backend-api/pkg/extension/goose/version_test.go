package goosex

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var versionMigrations = fstest.MapFS{
	"sql/sqlite/00001_widgets.sql": {Data: []byte(`-- +goose Up
CREATE TABLE widgets (id INTEGER NOT NULL PRIMARY KEY);

-- +goose Down
DROP TABLE widgets;
`)},
	"sql/sqlite/00002_gadgets.sql": {Data: []byte(`-- +goose Up
CREATE TABLE gadgets (id INTEGER NOT NULL PRIMARY KEY);

-- +goose Down
DROP TABLE gadgets;
`)},
}

func openVersionDB(t *testing.T) *gorm.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	db, err := gormDB.DB()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return gormDB
}

func TestCurrentVersionFollowsThePrefixedStream(t *testing.T) {
	gormDB := openVersionDB(t)
	db, err := gormDB.DB()
	require.NoError(t, err)
	source := Source{FS: versionMigrations, TableName: "goose_widgets", Prefix: "test_"}

	version, err := CurrentVersion(t.Context(), gormDB, source)
	require.NoError(t, err)
	assert.Zero(t, version, "a stream without a version table was never installed")

	require.NoError(t, MigrateUpTo(t.Context(), db, source, "sqlite", 1))
	version, err = CurrentVersion(t.Context(), gormDB, source)
	require.NoError(t, err)
	assert.Equal(t, int64(1), version)

	require.NoError(t, Migrate(t.Context(), db, source, "sqlite"))
	version, err = CurrentVersion(t.Context(), gormDB, source)
	require.NoError(t, err)
	assert.Equal(t, int64(2), version)

	require.NoError(t, MigrateDownTo(t.Context(), db, source, "sqlite", 0))
	version, err = CurrentVersion(t.Context(), gormDB, source)
	require.NoError(t, err)
	assert.Zero(t, version, "an installed stream rolled back to nothing reports 0")

	other, err := CurrentVersion(t.Context(), gormDB, Source{TableName: "goose_widgets"})
	require.NoError(t, err)
	assert.Zero(t, other, "the unprefixed stream is a different table")
}

func TestCurrentVersionSkipsVersionsWhoseLatestRowIsARollback(t *testing.T) {
	gormDB := openVersionDB(t)
	db, err := gormDB.DB()
	require.NoError(t, err)
	source := Source{FS: versionMigrations, TableName: "goose_widgets"}
	require.NoError(t, Migrate(t.Context(), db, source, "sqlite"))

	// Older goose recorded a rollback as a new row rather than deleting one.
	require.NoError(t, gormDB.Exec("INSERT INTO goose_widgets (version_id, is_applied) VALUES (?, ?)", 2, false).Error)
	version, err := CurrentVersion(t.Context(), gormDB, source)
	require.NoError(t, err)
	assert.Equal(t, int64(1), version)
}

func TestCurrentVersionReadsInsideTheCallersTransaction(t *testing.T) {
	gormDB := openVersionDB(t)
	source := Source{FS: versionMigrations, TableName: "goose_widgets"}
	require.NoError(t, gormDB.Transaction(func(tx *gorm.DB) error {
		version, err := CurrentVersion(t.Context(), tx, source)
		require.NoError(t, err)
		assert.Zero(t, version)
		// The transaction must still be usable after probing a missing table.
		return tx.Exec("CREATE TABLE after_probe (id INTEGER)").Error
	}))
}

func TestCurrentVersionPropagatesFailures(t *testing.T) {
	source := Source{TableName: "goose_widgets"}
	_, err := CurrentVersion(t.Context(), nil, source)
	assert.ErrorContains(t, err, "nil database")
	_, err = CurrentVersion(t.Context(), openVersionDB(t), Source{TableName: "goose;drop"})
	assert.ErrorContains(t, err, "invalid version table")
	_, err = CurrentVersion(t.Context(), openVersionDB(t), Source{TableName: "goose_widgets", Prefix: "Bad-"})
	assert.Error(t, err)

	closed := openVersionDB(t)
	db, err := closed.DB()
	require.NoError(t, err)
	require.NoError(t, db.Close())
	_, err = CurrentVersion(t.Context(), closed, source)
	assert.ErrorContains(t, err, "list tables", "a failed lookup must not read as a never-installed stream")
}
