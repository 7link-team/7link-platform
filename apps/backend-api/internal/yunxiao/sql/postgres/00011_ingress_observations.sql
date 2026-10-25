-- +goose Up
CREATE TABLE {{.Prefix}}integration_ingress_observations (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 mapping_id BIGINT NOT NULL,
 event_id BIGINT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 source VARCHAR(16) NOT NULL,
 detail TEXT NOT NULL,
 content_hash VARCHAR(64) NOT NULL,
 CONSTRAINT {{.Prefix}}integration_observation_source_ck CHECK (source IN ('CODEUP','FLOW')),
 CONSTRAINT {{.Prefix}}integration_observation_event_uq UNIQUE (event_id,mapping_id)
);
CREATE INDEX {{.Prefix}}integration_observation_mapping ON {{.Prefix}}integration_ingress_observations (tenant_id,mapping_id,id);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}integration_observation_rollback_guard (
 populated INTEGER NOT NULL,
 CONSTRAINT {{.Prefix}}integration_observation_empty_ck CHECK (populated = 0)
);
DELETE FROM {{.Prefix}}integration_observation_rollback_guard;
INSERT INTO {{.Prefix}}integration_observation_rollback_guard (populated) SELECT COUNT(*) FROM {{.Prefix}}integration_ingress_observations;
DROP TABLE {{.Prefix}}integration_observation_rollback_guard;
DROP TABLE {{.Prefix}}integration_ingress_observations;
