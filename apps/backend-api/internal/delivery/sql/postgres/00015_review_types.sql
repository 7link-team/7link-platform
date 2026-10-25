-- +goose Up
CREATE TABLE {{.Prefix}}delivery_review_types (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL,
 document TEXT NOT NULL, version INT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}ck_delivery_review_type_version CHECK(version>0)
);
CREATE INDEX {{.Prefix}}idx_delivery_review_type_tenant ON {{.Prefix}}delivery_review_types(tenant_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_review_types;
