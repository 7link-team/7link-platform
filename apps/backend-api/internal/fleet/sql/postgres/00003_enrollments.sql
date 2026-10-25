-- +goose Up
CREATE TABLE {{.Prefix}}fleet_machine_enrollments (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 client_id BIGINT NOT NULL,
 device_hash CHAR(64) NOT NULL UNIQUE,
 user_hash CHAR(64) NOT NULL UNIQUE,
 name VARCHAR(100) NOT NULL,
 os VARCHAR(32) NOT NULL CHECK(os IN ('WINDOWS','MACOS','LINUX')),
 state VARCHAR(32) NOT NULL CHECK(state IN ('PENDING','APPROVING','APPROVED','PROVISIONING','ISSUING','COMPLETE','COMPENSATING','FAILED','DENIED','EXPIRED','CANCELLED')),
 principal_id BIGINT,
 credential_id BIGINT,
 approved_by BIGINT,
 machine_id BIGINT,
 expires_at TIMESTAMPTZ(3) NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 UNIQUE(tenant_id,id)
);
CREATE INDEX {{.Prefix}}fleet_enrollments_expiry ON {{.Prefix}}fleet_machine_enrollments(tenant_id,state,expires_at);
-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}fleet_machine_enrollments;
