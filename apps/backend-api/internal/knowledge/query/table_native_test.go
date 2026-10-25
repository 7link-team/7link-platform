package query

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestNativeSQLiteFTS5ActuallyRanksAndBindsRestrictedIDs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	names, native := BindNames("safe_"), BindNativeNames("safe_")
	if err = EnsureNativeIndex(db, names, native); err != nil {
		require.Contains(t, err.Error(), "no such module: fts5")
		t.Log("SQLite explicitly reports FTS5 unavailable; native path requires sqlite_fts5 build")
		return
	}

	require.NoError(t, SyncNativeChunk(db, native, 1, "signed evidence evidence 数据 据库"))
	require.NoError(t, SyncNativeChunk(db, native, 2, "foreign evidence"))
	hits, err := NativeMatches(db, names, native, []guid.ID{1}, []string{"evidence"}, "ALL", 200)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, guid.ID(1), hits[0].ID)
	require.Positive(t, hits[0].TextRaw)
	hits, err = NativeMatches(db, names, native, []guid.ID{1}, []string{"数据", "据库"}, "ALL", 200)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.NoError(t, SyncNativeChunk(db, native, 1, "replaced"))
	hits, err = NativeMatches(db, names, native, []guid.ID{1}, []string{"evidence"}, "ALL", 200)
	require.NoError(t, err)
	require.Empty(t, hits)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, SyncNativeChunk(tx, native, 1, "rollback"))
		return gorm.ErrInvalidTransaction
	}), gorm.ErrInvalidTransaction)
	hits, err = NativeMatches(db, names, native, []guid.ID{1}, []string{"replaced"}, "ALL", 200)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	hits, err = NativeMatches(db, names, native, []guid.ID{1}, []string{"rollback"}, "ALL", 200)
	require.NoError(t, err)
	require.Empty(t, hits)
}
func TestNativeDialectExpressionsAndSQLAreBoundAndDeterministic(t *testing.T) {
	for _, tc := range []struct {
		name, all, any, predicate string
		dialect                   gorm.Dialector
	}{
		{"postgres", "'alpha' & '数据'", "'alpha' | '数据'", "ts_rank_cd", postgres.New(postgres.Config{DSN: "host=localhost user=test dbname=test", PreferSimpleProtocol: true})},
		{"mysql", "+alpha +数据", "alpha 数据", "MATCH(search_tokens)", mysql.New(mysql.Config{DSN: "test:test@tcp(localhost)/test", SkipInitializeWithVersion: true})},
		{"sqlite", "\"alpha\" AND \"数据\"", "\"alpha\" OR \"数据\"", "search_tokens MATCH", sqlite.Open(":memory:")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.all, NativeExpression(tc.name, []string{"alpha", "数据"}, "ALL"))
			require.Equal(t, tc.any, NativeExpression(tc.name, []string{"alpha", "数据"}, "ANY"))
			prefix := map[string]string{"postgres": "'库':*", "mysql": "+库*", "sqlite": "\"库\"*"}
			require.Equal(t, prefix[tc.name], NativeExpression(tc.name, []string{"库"}, "ALL"))
			db, err := gorm.Open(tc.dialect, &gorm.Config{DryRun: true, DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			q, err := nativeMatchQuery(db, BindNames("prefix_"), BindNativeNames("prefix_"), []guid.ID{7}, []string{"alpha"}, "ALL", 200)
			require.NoError(t, err)
			var out []NativeHit
			built := q.Find(&out).Statement
			require.Contains(t, built.SQL.String(), tc.predicate)
			require.Contains(t, built.SQL.String(), "text_raw DESC, id ASC")
			require.Contains(t, built.SQL.String(), "prefix_knowledge_chunk")
			require.NotContains(t, built.SQL.String(), "alpha")
			require.Contains(t, built.Vars, NativeExpression(tc.name, []string{"alpha"}, "ALL"))

		})
	}
}

func TestNativeSQLiteLargeAuthorizedSetKeepsGlobalRankingAndAllBatches(t *testing.T) {
	db, e := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, e)
	names, native := BindNames("bounded_"), BindNativeNames("bounded_")
	if e = EnsureNativeIndex(db, names, native); e != nil {
		require.Contains(t, e.Error(), "no such module: fts5")
		t.Log("requires tagged SQLite FTS5 for actual large authorized-set SQL")
		return
	}
	for _, id := range []guid.ID{1, 600, 70000} {
		require.NoError(t, SyncNativeChunk(db, native, id, "signed evidence"))
	}
	baseline, e := NativeMatches(db, names, native, []guid.ID{1, 600, 70000}, []string{"evidence"}, "ALL", 2)
	require.NoError(t, e)
	ids := make([]guid.ID, 0, 140000)
	for id := guid.ID(1); id <= 70000; id++ {
		ids = append(ids, id, id)
	}
	hits, e := NativeMatches(db, names, native, ids, []string{"evidence"}, "ALL", 2)
	require.NoError(t, e)
	require.Equal(t, baseline, hits)
	all, e := NativeMatches(db, names, native, ids, []string{"evidence"}, "ALL", 200)
	require.NoError(t, e)
	require.Len(t, all, 3)
	require.Equal(t, guid.ID(70000), all[2].ID)
}
