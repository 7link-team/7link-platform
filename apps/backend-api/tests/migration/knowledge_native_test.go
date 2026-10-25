package migration_test

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeNativeSearchMigrationsAndEngineCapabilities(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&knowledge.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			db := openMilestoneGORM(t, raw, engine.dialect)
			names, native := query.BindNames(deploymentPrefix), query.BindNativeNames(deploymentPrefix)
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.EmbeddingConfig{}, native.Embeddings.String())
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.SearchLog{}, native.SearchLogs.String())
			at := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
			live := 1
			chunk := model.Chunk{ID: 50001, TenantID: 50000, SourceID: 50002, EntryID: 50003, Ordinal: 0, Kind: "TEXT", HeadingPath: "Evidence", Content: "signed evidence", ContentHash: "hash", CharCount: 15, TokenEstimate: 5, LiveSlot: &live, CreatedAt: at, SearchTokens: "signed evidence"}
			require.NoError(t, db.Table(names.Chunks.String()).Create(&chunk).Error)
			installErr := query.InstallNativeIndex(db, names, native)
			if engine.name == "sqlite" && installErr != nil {
				require.Contains(t, installErr.Error(), "no such module: fts5")
				t.Log("SQLite FTS5 unavailable in this build; sqlite_fts5 build exercises native search")
				return
			}
			if engine.name == "mysql" {
				require.Error(t, installErr, "in-memory MySQL schema harness has no ngram plugin; never claim native engine success")
				t.Logf("MySQL schema harness native FULLTEXT unavailable: %v", installErr)
				return
			}
			require.NoError(t, installErr)
			require.NoError(t, query.SyncNativeChunk(db, native, chunk.ID, chunk.SearchTokens))
			hits, err := query.NativeMatches(db, names, native, []guid.ID{chunk.ID}, []string{"signed", "evidence"}, "ALL", 200)
			require.NoError(t, err)
			require.Len(t, hits, 1)
			require.Positive(t, hits[0].TextRaw)
			hits, err = query.NativeMatches(db, names, native, []guid.ID{50004}, []string{"evidence"}, "ALL", 200)
			require.NoError(t, err)
			require.Empty(t, hits)
			require.Error(t, query.ProbeVector(db, native), "plain PostgreSQL/SQLite harness must explicitly report absent pgvector")
		})
	}
}
