-- +goose Up
CREATE TABLE {{.Prefix}}governance_role_seeds (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 role_key VARCHAR(48) NOT NULL CHECK (length(role_key)<=48),
 role_id INTEGER NOT NULL,
 manifest_version INT NOT NULL,
 applied_codes TEXT NOT NULL,
 skipped_codes TEXT NOT NULL,
 managed BOOLEAN NOT NULL DEFAULT true,
 seed_conflict BOOLEAN NOT NULL DEFAULT false,
 version INT NOT NULL DEFAULT 1,
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 created_by INTEGER NULL,
 updated_by INTEGER NULL,
 CONSTRAINT {{.Prefix}}governance_role_seeds_key UNIQUE (tenant_id,role_key),
 CONSTRAINT {{.Prefix}}governance_role_seeds_role UNIQUE (tenant_id,role_id),
 CHECK (json_valid(applied_codes) AND json_valid(skipped_codes)),
 CHECK (managed IN (0,1) AND seed_conflict IN (0,1))
);
-- +goose Down
DROP TABLE {{.Prefix}}governance_role_seeds;
