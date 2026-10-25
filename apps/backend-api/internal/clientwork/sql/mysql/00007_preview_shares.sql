-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_preview_shares (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 preview_id BIGINT NOT NULL, active_slot BIGINT NULL, recipient_id BIGINT NOT NULL,
 requested_by BIGINT NOT NULL, decided_by BIGINT NULL, binding_id BIGINT NULL,
 state VARCHAR(32) NOT NULL CHECK (state IN ('PENDING_APPROVAL','ACTIVE','REJECTED','REVOKED','EXPIRED')),
 note TEXT NOT NULL, ttl_hours INTEGER NOT NULL CHECK (ttl_hours BETWEEN 1 AND 168),
 expires_at DATETIME(3) NULL, created_at DATETIME(3) NOT NULL,
 decided_at DATETIME(3) NULL, reason TEXT NULL, version INTEGER NOT NULL CHECK (version>0),
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,active_slot),
 FOREIGN KEY (tenant_id,preview_id) REFERENCES {{.Prefix}}clientwork_previews (tenant_id,id)
);
CREATE INDEX {{.Prefix}}clientwork_preview_shares_project ON {{.Prefix}}clientwork_preview_shares (tenant_id,project_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_preview_shares;
