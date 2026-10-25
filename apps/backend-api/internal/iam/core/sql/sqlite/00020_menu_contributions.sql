-- +goose Up
CREATE TABLE {{.Prefix}}iam_menus_next (
    tenant_id       BIGINT   NOT NULL,
    id              BIGINT   NOT NULL,
    parent_id       BIGINT   NULL,
    translations TEXT NOT NULL DEFAULT '{}',
    app TEXT COLLATE BINARY NOT NULL DEFAULT 'iam',
    domain TEXT COLLATE BINARY NOT NULL DEFAULT '',
    space TEXT COLLATE BINARY NOT NULL DEFAULT '',
    label           TEXT     NOT NULL,
    route           TEXT     COLLATE BINARY NULL,
    icon            TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    sort_order      INTEGER  NOT NULL DEFAULT 0,
    permission_code TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    status          TEXT     COLLATE BINARY NOT NULL,
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_menus_route UNIQUE (tenant_id, app, route),
    CONSTRAINT {{.Prefix}}fk_iam_menus_parent
        FOREIGN KEY (tenant_id, parent_id)
        REFERENCES {{.Prefix}}iam_menus_next (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_menus_status
        CHECK (status IN ('active', 'disabled'))
);
INSERT INTO {{.Prefix}}iam_menus_next (tenant_id,id,parent_id,label,route,icon,sort_order,permission_code,status,created_at,updated_at,translations,app,domain,space) SELECT tenant_id,id,parent_id,label,route,icon,sort_order,permission_code,status,created_at,updated_at,translations, 'iam', '', '' FROM {{.Prefix}}iam_menus;
UPDATE {{.Prefix}}iam_menus SET parent_id = NULL;
DROP TABLE {{.Prefix}}iam_menus;
-- Core v13 may be exercised alone by schema probes; do not reparse unrelated
-- identity-owned triggers while renaming this self-referencing replacement.
PRAGMA legacy_alter_table = ON;
ALTER TABLE {{.Prefix}}iam_menus_next RENAME TO {{.Prefix}}iam_menus;
PRAGMA legacy_alter_table = OFF;
CREATE INDEX {{.Prefix}}idx_iam_menus_tree ON {{.Prefix}}iam_menus (tenant_id,status,parent_id,sort_order,id);
CREATE TABLE {{.Prefix}}iam_menu_contributions (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL,
 module VARCHAR(64) COLLATE BINARY NOT NULL, page_key VARCHAR(128) COLLATE BINARY NOT NULL,
 menu_id BIGINT NULL, version BIGINT NOT NULL,
 seeded_hash VARCHAR(64) COLLATE BINARY NOT NULL, customized BOOLEAN NOT NULL DEFAULT FALSE,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}pk_menu_contributions PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_menu_contribution_key UNIQUE (tenant_id,module,page_key),
 CONSTRAINT {{.Prefix}}ck_menu_contribution_version CHECK (version > 0)
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_menu_contributions;
CREATE TABLE {{.Prefix}}iam_menus_next (
    tenant_id       BIGINT   NOT NULL,
    id              BIGINT   NOT NULL,
    parent_id       BIGINT   NULL,
    translations TEXT NOT NULL DEFAULT '{}',
    label           TEXT     NOT NULL,
    route           TEXT     COLLATE BINARY NULL,
    icon            TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    sort_order      INTEGER  NOT NULL DEFAULT 0,
    permission_code TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    status          TEXT     COLLATE BINARY NOT NULL,
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_menus_route UNIQUE (tenant_id, route),
    CONSTRAINT {{.Prefix}}fk_iam_menus_parent
        FOREIGN KEY (tenant_id, parent_id)
        REFERENCES {{.Prefix}}iam_menus_next (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_menus_status
        CHECK (status IN ('active', 'disabled'))
);
INSERT INTO {{.Prefix}}iam_menus_next (tenant_id,id,parent_id,label,route,icon,sort_order,permission_code,status,created_at,updated_at,translations) SELECT tenant_id,id,parent_id,label,route,icon,sort_order,permission_code,status,created_at,updated_at,translations FROM {{.Prefix}}iam_menus;
UPDATE {{.Prefix}}iam_menus SET parent_id = NULL;
DROP TABLE {{.Prefix}}iam_menus;
-- Core v13 may be exercised alone by schema probes; do not reparse unrelated
-- identity-owned triggers while renaming this self-referencing replacement.
PRAGMA legacy_alter_table = ON;
ALTER TABLE {{.Prefix}}iam_menus_next RENAME TO {{.Prefix}}iam_menus;
PRAGMA legacy_alter_table = OFF;
CREATE INDEX {{.Prefix}}idx_iam_menus_tree ON {{.Prefix}}iam_menus (tenant_id,status,parent_id,sort_order,id);
