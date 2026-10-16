-- +goose Up
CREATE TABLE {{.Prefix}}governance_role_seeds (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 role_key VARCHAR(48) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 role_id BIGINT NOT NULL,
 manifest_version INT NOT NULL,
 applied_codes JSON NOT NULL,
 skipped_codes JSON NOT NULL,
 managed BOOLEAN NOT NULL DEFAULT true,
 seed_conflict BOOLEAN NOT NULL DEFAULT false,
 version INT NOT NULL DEFAULT 1,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 created_by BIGINT NULL,
 updated_by BIGINT NULL,
 CONSTRAINT {{.Prefix}}governance_role_seeds_key UNIQUE (tenant_id,role_key),
 CONSTRAINT {{.Prefix}}governance_role_seeds_role UNIQUE (tenant_id,role_id),
 CHECK (managed IN (0,1) AND seed_conflict IN (0,1))
);
-- +goose Down
DROP TABLE {{.Prefix}}governance_role_seeds;
