-- +goose Up
CREATE TABLE {{.Prefix}}knowledge_usage_receipts (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, source_id BIGINT NOT NULL,
 entry_id BIGINT NOT NULL, execution_id BIGINT NOT NULL, evidence_id BIGINT NOT NULL,
 kind VARCHAR(16) NOT NULL, content_hash VARCHAR(64) NOT NULL, created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_usage_kind CHECK (kind IN ('INCLUSION','CITATION')),
 CONSTRAINT {{.Prefix}}uq_knowledge_usage_fact UNIQUE (tenant_id,execution_id,entry_id,kind,evidence_id)
);
CREATE INDEX {{.Prefix}}idx_knowledge_usage_source ON {{.Prefix}}knowledge_usage_receipts (tenant_id,source_id,kind);
-- +goose Down
DROP TABLE {{.Prefix}}knowledge_usage_receipts;
