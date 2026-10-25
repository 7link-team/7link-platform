-- +goose Up
CREATE TABLE {{.Prefix}}delivery_review_types (
 id INTEGER NOT NULL PRIMARY KEY, tenant_id INTEGER NOT NULL,
 document TEXT NOT NULL, version INT NOT NULL,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}ck_delivery_review_type_version CHECK(version>0)
);
CREATE INDEX {{.Prefix}}idx_delivery_review_type_tenant ON {{.Prefix}}delivery_review_types(tenant_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_review_types;
