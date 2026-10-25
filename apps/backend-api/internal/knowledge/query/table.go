package query

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"cmp"
	"errors"
	"gorm.io/gorm"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// NativeNames contains only resolver-owned derived index names.
type NativeNames struct{ FTS, TextIndex, Embeddings, Vectors, SearchLogs, Vector384, Vector768, Vector1024, Vector1536 gormx.TableName }

func BindNativeNames(prefix string) NativeNames { return BindNames(prefix).Native }

// ProbeVector never creates an extension or claims an absent deployment plugin.
func ProbeVector(db *gorm.DB, native NativeNames) error {
	if db.Dialector.Name() != "postgres" {
		return ErrNativeUnavailable
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var version string
		if err := tx.Raw("SELECT extversion FROM pg_extension WHERE extname = 'vector'").Scan(&version).Error; err != nil {
			return err
		}
		parts := strings.Split(version, ".")
		if len(parts) < 2 {
			return ErrNativeUnavailable
		}
		major, _ := strconv.Atoi(parts[0])
		minor, _ := strconv.Atoi(parts[1])
		if major == 0 && minor < 8 {
			return ErrNativeUnavailable
		}
		if err := tx.Exec("CREATE TABLE IF NOT EXISTS " + quoted(tx, native.Vectors) + " (tenant_id BIGINT NOT NULL, chunk_id BIGINT NOT NULL, model_key VARCHAR(64) NOT NULL, dims INT NOT NULL, content_hash VARCHAR(64) NOT NULL, embedding vector NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL, PRIMARY KEY (tenant_id,chunk_id,model_key))").Error; err != nil {
			return err
		}
		for _, index := range []struct {
			name gormx.TableName
			dims string
		}{{native.Vector384, "384"}, {native.Vector768, "768"}, {native.Vector1024, "1024"}, {native.Vector1536, "1536"}} {
			if err := tx.Exec("CREATE INDEX IF NOT EXISTS " + quoted(tx, index.name) + " ON " + quoted(tx, native.Vectors) + " USING hnsw ((embedding::vector(" + index.dims + ")) vector_cosine_ops) WITH (m=16,ef_construction=64) WHERE dims=" + index.dims).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
func PutVector(db *gorm.DB, native NativeNames, tenant, chunk guid.ID, key, hash string, dims int, vector string, at time.Time) error {
	return db.Exec("INSERT INTO "+quoted(db, native.Vectors)+" (tenant_id,chunk_id,model_key,dims,content_hash,embedding,created_at) VALUES (?,?,?,?,?,?::vector,?) ON CONFLICT (tenant_id,chunk_id,model_key) DO UPDATE SET content_hash=EXCLUDED.content_hash, embedding=EXCLUDED.embedding,dims=EXCLUDED.dims,created_at=EXCLUDED.created_at", tenant, chunk, key, dims, hash, vector, at).Error
}

type VectorFact struct {
	ChunkID     guid.ID
	ContentHash string
}

func VectorFacts(db *gorm.DB, native NativeNames, tenant guid.ID, key string) ([]VectorFact, error) {
	out := []VectorFact{}
	err := db.Transaction(func(tx *gorm.DB) error {
		return tx.Table(native.Vectors.String()).Select("chunk_id,content_hash").Where("tenant_id = ? AND model_key = ?", tenant, key).Scan(&out).Error
	})
	return out, err
}
func VectorMatches(db *gorm.DB, native NativeNames, tenant guid.ID, key string, dims int, allowed []guid.ID, vector string) ([]NativeHit, error) {
	out := []NativeHit{}
	if len(allowed) == 0 {
		return out, nil
	}
	if err := db.Exec("SET LOCAL hnsw.ef_search = 200").Error; err != nil {
		return out, err
	}
	if err := db.Exec("SET LOCAL hnsw.iterative_scan = 'relaxed_order'").Error; err != nil {
		return out, err
	}
	for _, batch := range NativeIDBatches(allowed) {
		q := db.Table(native.Vectors.String()).Where("tenant_id = ? AND model_key = ? AND chunk_id IN ?", tenant, key, batch)
		switch dims {
		case 384:
			q = q.Select("chunk_id AS id, embedding::vector(384) <=> ?::vector(384) AS text_raw", vector).Where("dims = 384")
		case 768:
			q = q.Select("chunk_id AS id, embedding::vector(768) <=> ?::vector(768) AS text_raw", vector).Where("dims = 768")
		case 1024:
			q = q.Select("chunk_id AS id, embedding::vector(1024) <=> ?::vector(1024) AS text_raw", vector).Where("dims = 1024")
		case 1536:
			q = q.Select("chunk_id AS id, embedding::vector(1536) <=> ?::vector(1536) AS text_raw", vector).Where("dims = 1536")
		default:
			return out, ErrNativeUnavailable
		}
		var hits []NativeHit
		if err := q.Order("text_raw ASC, chunk_id ASC").Limit(200).Scan(&hits).Error; err != nil {
			return nil, err
		}
		out = append(out, hits...)
	}
	return rankedNativeHits(out, 200, true), nil
}

var ErrNativeUnavailable = errors.New("knowledge: native index unavailable")

func quoted(db *gorm.DB, name gormx.TableName) string { return db.Statement.Quote(name.String()) }

// EnsureNativeIndex is an optional infrastructure operation. A failed probe is
// isolated in a savepoint, so a missing extension cannot poison a PG transaction.
func EnsureNativeIndex(db *gorm.DB, names TableNames, native NativeNames) error {
	return db.Transaction(func(tx *gorm.DB) error {
		switch tx.Dialector.Name() {
		case "postgres":
			return tx.Exec("CREATE INDEX IF NOT EXISTS " + quoted(tx, native.TextIndex) + " ON " + quoted(tx, names.Chunks) + " USING gin (to_tsvector('simple',search_tokens))").Error
		case "mysql":
			var size int
			if err := tx.Raw("SELECT @@ngram_token_size").Scan(&size).Error; err != nil {
				return err
			}
			if size != 2 {
				return ErrNativeUnavailable
			}
			if tx.Migrator().HasIndex(names.Chunks.String(), native.TextIndex.String()) {
				return nil
			}
			return ErrNativeUnavailable
		case "sqlite":
			return tx.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS " + quoted(tx, native.FTS) + " USING fts5(search_tokens, tokenize='unicode61 remove_diacritics 2')").Error
		default:
			return ErrNativeUnavailable
		}
	})
}
func InstallNativeIndex(db *gorm.DB, names TableNames, native NativeNames) error {
	if db.Dialector.Name() == "mysql" {
		if _, inside := db.Statement.ConnPool.(gorm.TxCommitter); inside {
			return ErrNativeUnavailable
		}
		if db.Migrator().HasIndex(names.Chunks.String(), native.TextIndex.String()) {
			return nil
		}
		// This DDL must use the infrastructure connection, never a command tx:
		// MySQL FULLTEXT installation implicitly commits the current transaction.
		return db.Connection(func(conn *gorm.DB) error {
			var size, stopwords int
			if err := conn.Raw("SELECT @@ngram_token_size").Scan(&size).Error; err != nil {
				return err
			}
			if size != 2 {
				return ErrNativeUnavailable
			}
			if err := conn.Raw("SELECT @@session.innodb_ft_enable_stopword").Scan(&stopwords).Error; err != nil {
				return err
			}
			if err := conn.Exec("SET SESSION innodb_ft_enable_stopword = OFF").Error; err != nil {
				return err
			}
			indexErr := conn.Exec("CREATE FULLTEXT INDEX " + quoted(conn, native.TextIndex) + " ON " + quoted(conn, names.Chunks) + " (search_tokens) WITH PARSER ngram").Error
			if stopwords != 0 {
				return errors.Join(indexErr, conn.Exec("SET SESSION innodb_ft_enable_stopword = ON").Error)
			}
			return indexErr
		})
	}
	return EnsureNativeIndex(db, names, native)
}
func BindSearchLogs(db *gorm.DB, native NativeNames) *gorm.DB {
	return db.Model(&model.SearchLog{}).Table(native.SearchLogs.String())
}
func BindEmbeddingConfig(db *gorm.DB, native NativeNames) *gorm.DB {
	return db.Model(&model.EmbeddingConfig{}).Table(native.Embeddings.String())
}

// SyncNativeChunk keeps SQLite's derived index transactional. PG and MySQL
// index search_tokens on the canonical chunk row directly.
func SyncNativeChunk(db *gorm.DB, native NativeNames, id guid.ID, tokens string) error {
	if db.Dialector.Name() != "sqlite" {
		return nil
	}
	if err := db.Table(native.FTS.String()).Where("rowid = ?", id).Delete(&struct{}{}).Error; err != nil {
		return err
	}
	return db.Table(native.FTS.String()).Create(map[string]any{"rowid": id, "search_tokens": tokens}).Error
}

type NativeHit struct {
	ID      guid.ID
	TextRaw float64
}

func NativeExpression(dialect string, tokens []string, mode string) string {
	parts := make([]string, len(tokens))
	for i, t := range tokens {
		runes := []rune(t)
		prefix := len(runes) == 1 && (unicode.Is(unicode.Han, runes[0]) || unicode.Is(unicode.Hiragana, runes[0]) || unicode.Is(unicode.Katakana, runes[0]) || unicode.Is(unicode.Hangul, runes[0]))
		switch dialect {
		case "postgres":
			parts[i] = "'" + strings.ReplaceAll(t, "'", "''") + "'"
			if prefix {
				parts[i] += ":*"
			}
		case "mysql":
			if mode == "ALL" {
				parts[i] = "+" + t
			} else {
				parts[i] = t
			}
			if prefix {
				parts[i] += "*"
			}
		default:
			parts[i] = "\"" + strings.ReplaceAll(t, "\"", "\"\"") + "\""
			if prefix {
				parts[i] += "*"
			}
		}
	}
	separator := " OR "
	if mode == "ALL" {
		separator = " AND "
	}
	if dialect == "postgres" {
		separator = " | "
		if mode == "ALL" {
			separator = " & "
		}
	}
	if dialect == "mysql" {
		separator = " "
	}
	return strings.Join(parts, separator)
}
func NativeMatches(db *gorm.DB, names TableNames, native NativeNames, allowed []guid.ID, tokens []string, mode string, limit int) ([]NativeHit, error) {
	out := []NativeHit{}
	if len(allowed) == 0 {
		return out, nil
	}
	for _, batch := range NativeIDBatches(allowed) {
		q, err := nativeMatchQuery(db, names, native, batch, tokens, mode, limit)
		if err != nil {
			return nil, err
		}
		var hits []NativeHit
		if err = q.Scan(&hits).Error; err != nil {
			return nil, err
		}
		out = append(out, hits...)
	}
	return rankedNativeHits(out, limit, false), nil
}
func nativeMatchQuery(db *gorm.DB, names TableNames, native NativeNames, allowed []guid.ID, tokens []string, mode string, limit int) (*gorm.DB, error) {
	expression := NativeExpression(db.Dialector.Name(), tokens, mode)
	var q *gorm.DB
	switch db.Dialector.Name() {
	case "postgres":
		q = db.Table(names.Chunks.String()).Select("id, ts_rank_cd(to_tsvector('simple',search_tokens),to_tsquery('simple',?),32) AS text_raw", expression).Where("to_tsvector('simple',search_tokens) @@ to_tsquery('simple',?)", expression).Where("id IN ?", allowed)
	case "mysql":
		q = db.Table(names.Chunks.String()).Select("id, MATCH(search_tokens) AGAINST(? IN BOOLEAN MODE) AS text_raw", expression).Where("MATCH(search_tokens) AGAINST(? IN BOOLEAN MODE)", expression).Where("id IN ?", allowed)
	case "sqlite":
		q = db.Table(native.FTS.String()).Select("rowid AS id, -rank AS text_raw").Where("search_tokens MATCH ?", expression).Where("rowid IN ?", allowed)
	default:
		return nil, ErrNativeUnavailable
	}
	return q.Order("text_raw DESC, id ASC").Limit(limit), nil
}

type TableNames struct {
	Native                                                                                                                                                             NativeNames
	Entries, Reviews, Settings, Sources, Chunks, UsageReceipts, Skills, SkillVersions, AgentSkills, DiagnosticLogs, RuntimeMetricSamples, InjectionLogs, Distillations gormx.TableName
}

func BindNames(prefix string) TableNames {
	return TableNames{Native: NativeNames{gormx.NewTableName(prefix, "knowledge_chunk_fts"), gormx.NewTableName(prefix, "idx_knowledge_chunk_text"), gormx.NewTableName(prefix, "knowledge_embedding_configs"), gormx.NewTableName(prefix, "knowledge_chunk_vectors"), gormx.NewTableName(prefix, "knowledge_search_logs"), gormx.NewTableName(prefix, "idx_knowledge_vector_384"), gormx.NewTableName(prefix, "idx_knowledge_vector_768"), gormx.NewTableName(prefix, "idx_knowledge_vector_1024"), gormx.NewTableName(prefix, "idx_knowledge_vector_1536")}, Distillations: gormx.NewTableName(prefix, model.Distillation{}.TableName()), Entries: gormx.NewTableName(prefix, model.Entry{}.TableName()), Reviews: gormx.NewTableName(prefix, model.Review{}.TableName()), Settings: gormx.NewTableName(prefix, model.Settings{}.TableName()), Sources: gormx.NewTableName(prefix, model.Source{}.TableName()), Chunks: gormx.NewTableName(prefix, model.Chunk{}.TableName()), UsageReceipts: gormx.NewTableName(prefix, model.UsageReceipt{}.TableName()), Skills: gormx.NewTableName(prefix, model.Skill{}.TableName()), SkillVersions: gormx.NewTableName(prefix, model.SkillVersion{}.TableName()), AgentSkills: gormx.NewTableName(prefix, model.AgentSkill{}.TableName()), DiagnosticLogs: gormx.NewTableName(prefix, model.DiagnosticLog{}.TableName()), RuntimeMetricSamples: gormx.NewTableName(prefix, model.RuntimeMetricSample{}.TableName()), InjectionLogs: gormx.NewTableName(prefix, model.InjectionLog{}.TableName())}
}
func BindInjectionLogs(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Model(&model.InjectionLog{}).Table(names.InjectionLogs.String())
}
func BindSkills(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Model(&model.Skill{}).Table(names.Skills.String())
}
func BindSkillVersions(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Model(&model.SkillVersion{}).Table(names.SkillVersions.String())
}
func BindAgentSkills(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Model(&model.AgentSkill{}).Table(names.AgentSkills.String())
}
func BindDiagnosticLogs(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Model(&model.DiagnosticLog{}).Table(names.DiagnosticLogs.String())
}
func BindRuntimeMetricSamples(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Model(&model.RuntimeMetricSample{}).Table(names.RuntimeMetricSamples.String())
}
func BindUsageReceipts(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Model(&model.UsageReceipt{}).Table(names.UsageReceipts.String())
}
func BindEntries(db *gorm.DB, names TableNames) *entry {
	return Use(db).Entry.Table(names.Entries.String())
}
func BindReviews(db *gorm.DB, names TableNames) *review {
	return Use(db).Review.Table(names.Reviews.String())
}
func BindSettings(db *gorm.DB, names TableNames) *settings {
	return Use(db).Settings.Table(names.Settings.String())
}
func BindSources(db *gorm.DB, names TableNames) *source {
	return Use(db).Source.Table(names.Sources.String())
}
func BindChunks(db *gorm.DB, names TableNames) *chunk {
	return Use(db).Chunk.Table(names.Chunks.String())
}

func BindDistillations(db *gorm.DB, names TableNames) *gorm.DB {
	return db.Model(&model.Distillation{}).Table(names.Distillations.String())
}

// NativeIDBatches deduplicates the complete authorized set and bounds SQL bind
// counts below even SQLite's historical 999-variable limit. No IDs are dropped.
func NativeIDBatches(ids []guid.ID) [][]guid.ID {
	seen := make(map[guid.ID]bool, len(ids))
	unique := make([]guid.ID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	batches := [][]guid.ID{}
	for len(unique) > 0 {
		n := min(500, len(unique))
		batches = append(batches, unique[:n])
		unique = unique[n:]
	}
	return batches
}
func rankedNativeHits(hits []NativeHit, limit int, ascending bool) []NativeHit {
	slices.SortFunc(hits, func(a, b NativeHit) int {
		order := cmp.Compare(a.TextRaw, b.TextRaw)
		if !ascending {
			order = -order
		}
		if order == 0 {
			return cmp.Compare(a.ID, b.ID)
		}
		return order
	})
	if limit >= 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// EligibleChunkFacts reads tenant-wide metadata using the same entry/source
// activity and expiry predicates as retrieval, independent of operator scope.
func EligibleChunkFacts(db *gorm.DB, names TableNames, tenant guid.ID, now time.Time) ([]VectorFact, error) {
	out := []VectorFact{}
	sql := "SELECT c.id AS chunk_id,c.content_hash FROM " + quoted(db, names.Chunks) + " c JOIN " + quoted(db, names.Sources) + " s ON s.id=c.source_id AND s.tenant_id=c.tenant_id JOIN " + quoted(db, names.Entries) + " e ON e.id=c.entry_id AND e.tenant_id=c.tenant_id WHERE c.tenant_id=? AND c.invalidated_at IS NULL AND s.state='INDEXED' AND s.archived_at IS NULL AND e.state='ACTIVE' AND (e.kind<>'EXPERIENCE' OR e.expires_at IS NULL OR e.expires_at>?)"
	err := db.Raw(sql, tenant, now).Scan(&out).Error
	return out, err
}
