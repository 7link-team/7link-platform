-- +goose Up
CREATE TABLE {{.Prefix}}knowledge_search_logs (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, actor_id BIGINT NOT NULL,
 purpose VARCHAR(16) NOT NULL, request_json MEDIUMTEXT NOT NULL, result_json MEDIUMTEXT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_search_purpose CHECK (purpose IN ('SEARCH','DEBUG'))
);
CREATE INDEX {{.Prefix}}idx_knowledge_search_actor ON {{.Prefix}}knowledge_search_logs (tenant_id,actor_id,created_at,id);
CREATE TABLE {{.Prefix}}knowledge_embedding_configs (
 tenant_id BIGINT NOT NULL PRIMARY KEY, base_url VARCHAR(512) NOT NULL, model VARCHAR(128) NOT NULL,
 dims INT NOT NULL, api_key_sealed TEXT NOT NULL, batch_size INT NOT NULL, rpm INT NOT NULL,
 tpm INT NOT NULL, timeout_ms INT NOT NULL, state VARCHAR(16) NOT NULL,
 active_model_key VARCHAR(64) NOT NULL, pending_model_key VARCHAR(64) NOT NULL,
 consecutive_failures INT NOT NULL, degraded_until DATETIME(3) NULL, coverage_ppm INT NOT NULL,
 version INT NOT NULL, updated_by BIGINT NOT NULL, updated_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_embedding_dims CHECK (dims IN (384,768,1024,1536)),
 CONSTRAINT {{.Prefix}}chk_knowledge_embedding_limits CHECK (batch_size BETWEEN 1 AND 128 AND rpm > 0 AND tpm > 0 AND timeout_ms BETWEEN 100 AND 30000 AND coverage_ppm BETWEEN 0 AND 1000000 AND version > 0),
 CONSTRAINT {{.Prefix}}chk_knowledge_embedding_state CHECK (state IN ('ACTIVE','DEGRADED','DISABLED'))
);
-- Optional pgvector rows and HNSW indexes are installed by the capability probe.
-- +goose Down
DROP TABLE {{.Prefix}}knowledge_embedding_configs;
DROP TABLE {{.Prefix}}knowledge_search_logs;
