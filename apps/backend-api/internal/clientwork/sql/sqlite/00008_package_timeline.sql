-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_package_timeline (
 tenant_id BIGINT NOT NULL,id BIGINT NOT NULL,project_id BIGINT NOT NULL,package_id BIGINT NOT NULL,
 kind VARCHAR(32) NOT NULL,state VARCHAR(32) NOT NULL,version INTEGER NOT NULL CHECK(version>0),
 content_hash VARCHAR(64) NOT NULL,created_at DATETIME NOT NULL,
 PRIMARY KEY(tenant_id,id),UNIQUE(tenant_id,package_id,version),
 FOREIGN KEY(tenant_id,package_id) REFERENCES {{.Prefix}}clientwork_packages (tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_package_timeline_page ON {{.Prefix}}clientwork_package_timeline(tenant_id,project_id,package_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_package_timeline;
