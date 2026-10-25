-- +goose Up
CREATE TABLE {{.Prefix}}agents_tool_versions (
id BIGINT NOT NULL PRIMARY KEY,
tenant_id BIGINT NOT NULL,
tool_id BIGINT NOT NULL,
version_no INT NOT NULL,
transport VARCHAR(32) NOT NULL,
endpoint VARCHAR(512) NOT NULL,
content_hash VARCHAR(64) NOT NULL,
skill_body TEXT NOT NULL,
state VARCHAR(16) NOT NULL,
source VARCHAR(16) NOT NULL,
source_ref VARCHAR(512) NOT NULL,
change_note VARCHAR(2000) NOT NULL,
published_by BIGINT NOT NULL,
created_at TIMESTAMPTZ(3) NOT NULL,
CONSTRAINT {{.Prefix}}uq_agents_tool_version UNIQUE (tenant_id, tool_id, version_no),
CONSTRAINT {{.Prefix}}chk_agents_tool_version_state CHECK (state IN ('ACTIVE','REVOKED'))
);
INSERT INTO {{.Prefix}}agents_tool_versions (id, tenant_id, tool_id, version_no, transport, endpoint, content_hash, skill_body, state, source, source_ref, change_note, published_by, created_at)
SELECT id, tenant_id, id, 1, transport, endpoint, content_hash, skill_body, 'ACTIVE', 'MANUAL', '', '', 0, created_at FROM {{.Prefix}}agents_tool_entries;
CREATE TABLE {{.Prefix}}agents_tool_upstreams (
id BIGINT NOT NULL PRIMARY KEY,
tenant_id BIGINT NOT NULL,
tool_id BIGINT NOT NULL,
content_hash VARCHAR(64) NOT NULL,
note VARCHAR(2000) NOT NULL,
reported_by BIGINT NOT NULL,
created_at TIMESTAMPTZ(3) NOT NULL
);
CREATE INDEX {{.Prefix}}idx_agents_tool_upstream ON {{.Prefix}}agents_tool_upstreams (tenant_id, tool_id, id);
-- +goose Down
DROP TABLE {{.Prefix}}agents_tool_upstreams;
DROP TABLE {{.Prefix}}agents_tool_versions;
