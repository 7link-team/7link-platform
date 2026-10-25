package migration_test

import (
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/knowledge/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"database/sql"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

// MySQL uses a generated key to implement the nullable project uniqueness rule.
// It is derived by the database and intentionally absent from the writable model.
type knowledgeEntryMySQLSchema struct {
	model.Entry
	MemoryProjectKey *int64 `gorm:"column:memory_project_key"`
}

func TestKnowledgeSchemasAcrossEngines(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	engines = append(engines, realMySQLEngines(t)...)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&knowledge.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Distillation{}, deploymentPrefix+"knowledge_skill_distillations")
			if engine.dialect == "mysql" {
				assertModelMatchesNamedTable(t, raw, engine.dialect, knowledgeEntryMySQLSchema{}, deploymentPrefix+"knowledge_entries")
			} else {
				assertModelMatchesNamedTable(t, raw, engine.dialect, model.Entry{}, deploymentPrefix+"knowledge_entries")
			}
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Review{}, deploymentPrefix+"knowledge_reviews")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Settings{}, deploymentPrefix+"knowledge_settings")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Source{}, deploymentPrefix+"knowledge_sources")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Chunk{}, deploymentPrefix+"knowledge_chunks")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.UsageReceipt{}, deploymentPrefix+"knowledge_usage_receipts")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.InjectionLog{}, deploymentPrefix+"knowledge_injection_logs")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Skill{}, deploymentPrefix+"knowledge_skills")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.SkillVersion{}, deploymentPrefix+"knowledge_skill_versions")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.AgentSkill{}, deploymentPrefix+"knowledge_agent_skills")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.DiagnosticLog{}, deploymentPrefix+"knowledge_diagnostic_logs")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.RuntimeMetricSample{}, deploymentPrefix+"knowledge_runtime_metric_samples")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Skill{}, deploymentPrefix+"knowledge_skills")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.SkillVersion{}, deploymentPrefix+"knowledge_skill_versions")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.AgentSkill{}, deploymentPrefix+"knowledge_agent_skills")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.DiagnosticLog{}, deploymentPrefix+"knowledge_diagnostic_logs")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.RuntimeMetricSample{}, deploymentPrefix+"knowledge_runtime_metric_samples")
			if engine.name == "mysql" {
				return
			} // Schema harness, not real MySQL constraint validation.
			db := openMilestoneGORM(t, raw, engine.dialect)
			for _, entry := range []model.Entry{
				{ID: 1, TenantID: 1, Scope: "TEAM", Kind: "RULE", State: "PENDING_REVIEW", Title: "Rule", Body: "Body", ContentHash: "hash", Provenance: "human", CreatedBy: 2},
				{ID: 2, TenantID: 1, Scope: "PROJECT", Kind: "DOCUMENT", State: "ACTIVE"},
				{ID: 3, TenantID: 1, Scope: "TEAM", Kind: "DOCUMENT", State: "PENDING_REVIEW"},
			} {
				entry.CreatedAt = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
				entry.UpdatedAt = entry.CreatedAt
				err := db.Table(deploymentPrefix + "knowledge_entries").Create(&entry).Error
				if entry.ID == 1 {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			}
		})
	}
}

func TestKnowledgePolicyAndSourceLifecycleMigrationsPreserveExistingRows(t *testing.T) {
	engines := []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&knowledge.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 3))
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			entry := model.Entry{ID: 4101, TenantID: 4100, Scope: "TEAM", Kind: "EXPERIENCE", State: "ACTIVE", Title: "Existing memory", Body: "Retain original evidence", ContentHash: "original", Provenance: "existing", Version: 3, CreatedBy: 2, CreatedAt: at, UpdatedAt: at}
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_entries").Omit("TTLDays", "ExpiresAt", "LastUsedAt", "UseCount", "SourceExecutionID", "SupersedesID", "RiskFlags", "Origin", "LiveSlot").Create(&entry).Error)
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_settings").Create(map[string]any{"tenant_id": 4100, "candidate_limit": 23, "version": 2, "updated_by": 2, "updated_at": at}).Error)
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			var retained model.Entry
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_entries").Where("id = ?", entry.ID).First(&retained).Error)
			require.Equal(t, entry.Title, retained.Title)
			require.Equal(t, entry.Body, retained.Body)
			require.Equal(t, entry.ContentHash, retained.ContentHash)
			require.Equal(t, entry.Version, retained.Version)
			require.WithinDuration(t, at, retained.CreatedAt, time.Millisecond)
			var settings model.Settings
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_settings").Where("tenant_id = ?", 4100).First(&settings).Error)
			require.Equal(t, 23, settings.CandidateLimit)
			require.Equal(t, "LEXICAL", settings.IndexStrategy)
			require.Equal(t, "WRITERS", settings.SourcePermission)
			require.Equal(t, "RULES_ONLY", settings.MemoryApproval)
			pending := entry
			pending.ID, pending.State = 4102, "PENDING_REVIEW"
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_entries").Create(&pending).Error)
			invalid := pending
			invalid.ID, invalid.Kind = 4103, "DOCUMENT"
			require.Error(t, db.Table(deploymentPrefix+"knowledge_entries").Create(&invalid).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 3), "pending experience must prevent a lossy downgrade")
			retained = model.Entry{}
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_entries").Where("id = ?", pending.ID).First(&retained).Error)
			require.Equal(t, "PENDING_REVIEW", retained.State)
		})
	}
}

func TestKnowledgeSourceLiveChunkAndSettingsConstraintsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	engines = append(engines, realMySQLEngines(t)...)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&knowledge.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			if engine.name == "mysql" {
				return
			} // schema harness has no real constraint enforcement
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
			settings := model.Settings{TenantID: 6001, CandidateLimit: 0, Version: 1, UpdatedBy: 2, UpdatedAt: at}
			require.Error(t, db.Table(deploymentPrefix+"knowledge_settings").Create(&settings).Error)
			settings.CandidateLimit = 200
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_settings").Create(&settings).Error)
			binding := model.Source{ID: 7001, TenantID: 6001, Scope: "PROJECT", SourceKind: "UPLOAD", Title: "Document", Locator: "/documents/source", Format: "TEXT", State: "PENDING", Version: 1, ChunkerVersion: 1, CreatedBy: 2, UpdatedBy: 2, CreatedAt: at, UpdatedAt: at}
			require.Error(t, db.Table(deploymentPrefix+"knowledge_sources").Create(&binding).Error)
			binding.Scope = "TEAM"
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_sources").Create(&binding).Error)
			live := 1
			chunk := model.Chunk{ID: 8001, TenantID: 6001, SourceID: binding.ID, EntryID: 9001, Kind: "TEXT", Content: "content", ContentHash: "hash", CharCount: 7, TokenEstimate: 3, LiveSlot: &live, CreatedAt: at}
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_chunks").Create(&chunk).Error)
			duplicate := chunk
			duplicate.ID, duplicate.EntryID = 8002, 9002
			require.Error(t, db.Table(deploymentPrefix+"knowledge_chunks").Create(&duplicate).Error)
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_chunks").Where("id = ?", chunk.ID).Updates(map[string]any{"live_slot": nil, "invalidated_at": at}).Error)
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_chunks").Create(&duplicate).Error)
			invalid := duplicate
			invalid.ID, invalid.EntryID, invalid.ContentHash, invalid.LiveSlot = 8003, 9003, "other", nil
			require.Error(t, db.Table(deploymentPrefix+"knowledge_chunks").Create(&invalid).Error)
			receipt := model.UsageReceipt{ID: 9101, TenantID: 6001, SourceID: binding.ID, EntryID: 9001, ExecutionID: 9201, EvidenceID: 9301, Kind: "INCLUSION", ContentHash: "hash", CreatedAt: at}
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_usage_receipts").Create(&receipt).Error)
			receipt.ID = 9102
			require.Error(t, db.Table(deploymentPrefix+"knowledge_usage_receipts").Create(&receipt).Error)
			receipt.Kind = "CITATION"
			require.NoError(t, db.Table(deploymentPrefix+"knowledge_usage_receipts").Create(&receipt).Error)
			receipt.ID, receipt.Kind = 9103, "DEBUG"
			require.Error(t, db.Table(deploymentPrefix+"knowledge_usage_receipts").Create(&receipt).Error)
		})
	}
}

func realMySQLEngines(t *testing.T) []databaseEngine {
	t.Helper()
	dsn := os.Getenv("MYSQL8_DSN")
	if dsn == "" {
		return nil
	}
	return []databaseEngine{{name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
		db, err := sql.Open("mysql", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, db.Close()) })
		verifyRealMySQLServer(t, db)
		return db
	}}}
}
