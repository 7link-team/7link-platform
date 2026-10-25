-- +goose Up
CREATE TABLE {{.Prefix}}delivery_review_sheets (
 id INTEGER NOT NULL PRIMARY KEY, tenant_id INTEGER NOT NULL, project_id INTEGER NOT NULL,
 requirement_id INTEGER NOT NULL, open_requirement_id INTEGER, initiator_id INTEGER NOT NULL,
 document TEXT NOT NULL, state VARCHAR(16) NOT NULL, version INT NOT NULL,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}uq_delivery_open_review UNIQUE(tenant_id,open_requirement_id),
 CONSTRAINT {{.Prefix}}ck_delivery_review_state CHECK(state IN ('OPEN','PASSED','REJECTED','CANCELLED')),
 CONSTRAINT {{.Prefix}}ck_delivery_review_version CHECK(version>0)
);
CREATE INDEX {{.Prefix}}idx_delivery_review_project ON {{.Prefix}}delivery_review_sheets(tenant_id,project_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_review_sheets;
