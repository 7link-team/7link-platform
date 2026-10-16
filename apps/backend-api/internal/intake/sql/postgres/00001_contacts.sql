-- +goose Up
CREATE TABLE {{.Prefix}}intake_contacts (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL CHECK (tenant_id>0),
 kind VARCHAR(32) NOT NULL DEFAULT 'PERSON' CHECK (kind IN ('PERSON','SYSTEM')),
 display_name VARCHAR(120) NOT NULL,
 email VARCHAR(254) NULL,
 organization_label VARCHAR(120) NULL,
 principal_id BIGINT NULL,
 locale VARCHAR(8) NULL,
 state VARCHAR(32) NOT NULL DEFAULT 'ACTIVE' CHECK (state IN ('ACTIVE','BLOCKED')),
 note VARCHAR(2000) NULL,
 version INT NOT NULL DEFAULT 1,
 archived_at TIMESTAMPTZ(3) NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 updated_at TIMESTAMPTZ(3) NOT NULL,
 created_by BIGINT NULL,
 updated_by BIGINT NULL,
 CONSTRAINT {{.Prefix}}uq_intake_contacts_tenant_id UNIQUE (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_intake_contacts_email UNIQUE (tenant_id,email),
 CONSTRAINT {{.Prefix}}uq_intake_contacts_principal UNIQUE (tenant_id,principal_id)
);
-- +goose Down
DROP TABLE {{.Prefix}}intake_contacts;
