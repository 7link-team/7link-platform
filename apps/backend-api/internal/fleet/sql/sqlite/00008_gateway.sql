-- +goose Up
CREATE TABLE {{.Prefix}}fleet_gateway_configs (
 id INTEGER NOT NULL PRIMARY KEY, tenant_id INTEGER NOT NULL UNIQUE,
 enabled BOOLEAN NOT NULL, machine_id INTEGER NULL, default_model VARCHAR(160) NOT NULL,
 records_json TEXT NOT NULL, version INTEGER NOT NULL CHECK(version>0), revision BIGINT NOT NULL CHECK(revision>0), updated_at DATETIME NOT NULL
);
CREATE TABLE {{.Prefix}}fleet_gateway_keys (
 id INTEGER NOT NULL PRIMARY KEY, tenant_id INTEGER NOT NULL,
 sealed TEXT NOT NULL, key_hash VARCHAR(64) NOT NULL UNIQUE, last4 VARCHAR(4) NOT NULL, created_at DATETIME NOT NULL
);
CREATE INDEX {{.Prefix}}fleet_gateway_keys_tenant ON {{.Prefix}}fleet_gateway_keys(tenant_id,id);
CREATE TABLE {{.Prefix}}fleet_gateway_requests (
 id INTEGER NOT NULL PRIMARY KEY, tenant_id INTEGER NOT NULL, revision BIGINT NOT NULL,
 model VARCHAR(160) NOT NULL, state VARCHAR(32) NOT NULL CHECK(state IN ('QUEUED','RUNNING','COMPLETED','FAILED','CANCELLED')),
 reason VARCHAR(64) NOT NULL, created_at DATETIME NOT NULL, started_at DATETIME NULL, finished_at DATETIME NULL
);
CREATE INDEX {{.Prefix}}fleet_gateway_requests_recent ON {{.Prefix}}fleet_gateway_requests(tenant_id,id);
-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}fleet_gateway_requests;
DROP TABLE IF EXISTS {{.Prefix}}fleet_gateway_keys;
DROP TABLE IF EXISTS {{.Prefix}}fleet_gateway_configs;
