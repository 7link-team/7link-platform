-- +goose Up
CREATE TABLE {{.Prefix}}fleet_gateway_configs (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL UNIQUE,
 enabled BOOLEAN NOT NULL, machine_id BIGINT NULL, default_model VARCHAR(160) NOT NULL,
 records_json MEDIUMTEXT NOT NULL, version INTEGER NOT NULL CHECK(version>0), revision BIGINT NOT NULL CHECK(revision>0), updated_at DATETIME(3) NOT NULL
);
CREATE TABLE {{.Prefix}}fleet_gateway_keys (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL,
 sealed TEXT NOT NULL, key_hash VARCHAR(64) NOT NULL UNIQUE, last4 VARCHAR(4) NOT NULL, created_at DATETIME(3) NOT NULL
);
CREATE INDEX {{.Prefix}}fleet_gateway_keys_tenant ON {{.Prefix}}fleet_gateway_keys(tenant_id,id);
CREATE TABLE {{.Prefix}}fleet_gateway_requests (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, revision BIGINT NOT NULL,
 model VARCHAR(160) NOT NULL, state VARCHAR(32) NOT NULL CHECK(state IN ('QUEUED','RUNNING','COMPLETED','FAILED','CANCELLED')),
 reason VARCHAR(64) NOT NULL, created_at DATETIME(3) NOT NULL, started_at DATETIME(3) NULL, finished_at DATETIME(3) NULL
);
CREATE INDEX {{.Prefix}}fleet_gateway_requests_recent ON {{.Prefix}}fleet_gateway_requests(tenant_id,id);
-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}fleet_gateway_requests;
DROP TABLE IF EXISTS {{.Prefix}}fleet_gateway_keys;
DROP TABLE IF EXISTS {{.Prefix}}fleet_gateway_configs;
