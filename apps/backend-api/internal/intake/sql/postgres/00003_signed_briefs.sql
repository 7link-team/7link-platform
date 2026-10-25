-- +goose Up
CREATE TABLE {{.Prefix}}intake_signed_briefs (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 intake_id BIGINT NOT NULL,
 binding_id BIGINT NOT NULL,
 items_json TEXT NOT NULL,
 sha256 VARCHAR(64) NOT NULL,
 signed_by BIGINT NOT NULL,
 signed_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}intake_signed_brief_binding UNIQUE(tenant_id,binding_id)
);
-- +goose Down
DROP TABLE {{.Prefix}}intake_signed_briefs;
