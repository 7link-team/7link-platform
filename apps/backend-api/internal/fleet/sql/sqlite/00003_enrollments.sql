-- +goose Up
CREATE TABLE {{.Prefix}}fleet_machine_enrollments (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 client_id INTEGER NOT NULL,
 device_hash CHAR(64) NOT NULL UNIQUE,
 user_hash CHAR(64) NOT NULL UNIQUE,
 name VARCHAR(100) NOT NULL,
 os VARCHAR(32) NOT NULL CHECK(os IN ('WINDOWS','MACOS','LINUX')),
 state VARCHAR(32) NOT NULL CHECK(state IN ('PENDING','APPROVING','APPROVED','PROVISIONING','ISSUING','COMPLETE','COMPENSATING','FAILED','DENIED','EXPIRED','CANCELLED')),
 principal_id INTEGER,
 credential_id INTEGER,
 approved_by INTEGER,
 machine_id INTEGER,
 expires_at DATETIME NOT NULL,
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 UNIQUE(tenant_id,id)
);
CREATE INDEX {{.Prefix}}fleet_enrollments_expiry ON {{.Prefix}}fleet_machine_enrollments(tenant_id,state,expires_at);
-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}fleet_machine_enrollments;
