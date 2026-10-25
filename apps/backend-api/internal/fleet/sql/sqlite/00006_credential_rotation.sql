-- +goose Up
CREATE TABLE {{.Prefix}}fleet_credential_rotations (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 machine_id INTEGER NOT NULL,
 principal_id INTEGER NOT NULL,
 requested_by INTEGER NOT NULL,
 state VARCHAR(32) NOT NULL CHECK(state IN ('PENDING_DELIVERY','ACTIVE','FAILED')),
 phase VARCHAR(32) NOT NULL CHECK(phase IN ('QUEUED','ISSUING','DELIVERED','COMPENSATING','DONE')),
 reason_code VARCHAR(32) NOT NULL,
 active_key INTEGER NULL CHECK(active_key=1),
 attempts INTEGER NOT NULL CHECK(attempts BETWEEN 0 AND 5),
 credential_id INTEGER NULL,
 issued_at DATETIME NULL,
 retire_old_at DATETIME NOT NULL,
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 UNIQUE(tenant_id,machine_id,active_key),
 FOREIGN KEY(tenant_id,machine_id) REFERENCES {{.Prefix}}fleet_machines(tenant_id,id)
);
CREATE INDEX {{.Prefix}}fleet_rotations_pending ON {{.Prefix}}fleet_credential_rotations(tenant_id,state,id);
CREATE TABLE {{.Prefix}}fleet_machine_credentials (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 machine_id INTEGER NOT NULL,
 rotation_id INTEGER NULL,
 state VARCHAR(32) NOT NULL CHECK(state IN ('PENDING_DELIVERY','ACTIVE','RETIRING','REVOKED')),
 reason_code VARCHAR(32) NOT NULL,
 retire_at DATETIME NULL,
 delivered_at DATETIME NULL,
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 FOREIGN KEY(tenant_id,machine_id) REFERENCES {{.Prefix}}fleet_machines(tenant_id,id)
);
CREATE INDEX {{.Prefix}}fleet_credentials_retire ON {{.Prefix}}fleet_machine_credentials(tenant_id,state,retire_at,id);
-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}fleet_machine_credentials;
DROP TABLE IF EXISTS {{.Prefix}}fleet_credential_rotations;
