-- +goose Up
ALTER TABLE {{.Prefix}}iam_menus ADD COLUMN app VARCHAR(32) COLLATE "C" NOT NULL DEFAULT 'iam';
ALTER TABLE {{.Prefix}}iam_menus ADD COLUMN domain VARCHAR(16) COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}iam_menus ADD COLUMN space VARCHAR(16) COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}iam_menus DROP CONSTRAINT {{.Prefix}}uq_iam_menus_route, ADD CONSTRAINT {{.Prefix}}uq_iam_menus_route UNIQUE (tenant_id,app,route);
CREATE TABLE {{.Prefix}}iam_menu_contributions (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL,
 module VARCHAR(64) COLLATE "C" NOT NULL, page_key VARCHAR(128) COLLATE "C" NOT NULL,
 menu_id BIGINT NULL, version BIGINT NOT NULL,
 seeded_hash VARCHAR(64) COLLATE "C" NOT NULL, customized BOOLEAN NOT NULL DEFAULT FALSE,
 created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}pk_menu_contributions PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_menu_contribution_key UNIQUE (tenant_id,module,page_key),
 CONSTRAINT {{.Prefix}}ck_menu_contribution_version CHECK (version > 0)
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_menu_contributions;
ALTER TABLE {{.Prefix}}iam_menus DROP CONSTRAINT {{.Prefix}}uq_iam_menus_route, ADD CONSTRAINT {{.Prefix}}uq_iam_menus_route UNIQUE (tenant_id,route);
ALTER TABLE {{.Prefix}}iam_menus DROP COLUMN space;
ALTER TABLE {{.Prefix}}iam_menus DROP COLUMN domain;
ALTER TABLE {{.Prefix}}iam_menus DROP COLUMN app;
