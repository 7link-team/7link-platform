-- +goose Up
CREATE TABLE {{.Prefix}}intake_signed_briefs (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 project_id INTEGER NOT NULL,
 intake_id INTEGER NOT NULL,
 binding_id INTEGER NOT NULL,
 items_json TEXT NOT NULL,
 sha256 VARCHAR(64) NOT NULL,
 signed_by INTEGER NOT NULL,
 signed_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}intake_signed_brief_binding UNIQUE(tenant_id,binding_id)
);
-- +goose Down
DROP TABLE {{.Prefix}}intake_signed_briefs;
