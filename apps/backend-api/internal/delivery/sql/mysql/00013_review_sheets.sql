-- +goose Up
CREATE TABLE {{.Prefix}}delivery_review_sheets (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 requirement_id BIGINT NOT NULL, open_requirement_id BIGINT, initiator_id BIGINT NOT NULL,
 document MEDIUMTEXT NOT NULL, state VARCHAR(16) NOT NULL, version INT NOT NULL,
 created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_delivery_open_review UNIQUE(tenant_id,open_requirement_id),
 CONSTRAINT {{.Prefix}}ck_delivery_review_state CHECK(state IN ('OPEN','PASSED','REJECTED','CANCELLED')),
 CONSTRAINT {{.Prefix}}ck_delivery_review_version CHECK(version>0)
);
CREATE INDEX {{.Prefix}}idx_delivery_review_project ON {{.Prefix}}delivery_review_sheets(tenant_id,project_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_review_sheets;
