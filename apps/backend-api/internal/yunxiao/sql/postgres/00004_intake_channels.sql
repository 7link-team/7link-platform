-- +goose Up
CREATE TABLE {{.Prefix}}intake_channel_settings (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 kind VARCHAR(16) NOT NULL, document TEXT NOT NULL, version INT NOT NULL, updated_by BIGINT NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_intake_channel_kind UNIQUE(tenant_id,project_id,kind),
 CONSTRAINT {{.Prefix}}ck_intake_channel_kind CHECK(kind IN ('PORTAL','EMAIL','API')),
 CONSTRAINT {{.Prefix}}ck_intake_channel_version CHECK(version>0)
);
-- +goose Down
DROP TABLE {{.Prefix}}intake_channel_settings;
