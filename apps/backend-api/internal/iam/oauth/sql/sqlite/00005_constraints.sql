-- Cross-module constraints preserve the source's ON DELETE CASCADE semantics.
-- Sources: internal/modules/iam/sql/sqlite/00003_local_identity.sql:51,82,93,95.
-- SQLite cannot add or drop foreign keys in place, so this milestone rebuilds each child table.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_oauth_codes RENAME TO {{.Prefix}}iam_oauth_codes_old;
CREATE TABLE {{.Prefix}}iam_oauth_codes (
    code_hash TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    client_id BIGINT NOT NULL,
    principal_id BIGINT NOT NULL,
    redirect_uri TEXT COLLATE BINARY NOT NULL,
    scope TEXT COLLATE BINARY NOT NULL,
    code_challenge TEXT COLLATE BINARY NOT NULL,
    nonce TEXT COLLATE BINARY NOT NULL DEFAULT '',
    auth_time DATETIME NOT NULL,
    acr INTEGER NOT NULL DEFAULT 0,
    amr TEXT COLLATE BINARY NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    consumed_at DATETIME NULL,
    CONSTRAINT {{.Prefix}}fk_oauth_codes_client
        FOREIGN KEY (client_id) REFERENCES {{.Prefix}}iam_oauth_clients (id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_oauth_codes_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_oauth_codes SELECT * FROM {{.Prefix}}iam_oauth_codes_old;
DROP TABLE {{.Prefix}}iam_oauth_codes_old;

ALTER TABLE {{.Prefix}}iam_oauth_consents RENAME TO {{.Prefix}}iam_oauth_consents_old;
CREATE TABLE {{.Prefix}}iam_oauth_consents (
    principal_id BIGINT NOT NULL,
    client_id BIGINT NOT NULL,
    tenant_id BIGINT NOT NULL,
    scope TEXT COLLATE BINARY NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    last_used_at DATETIME NOT NULL,
    PRIMARY KEY (principal_id, client_id),
    CONSTRAINT {{.Prefix}}fk_oauth_consents_client
        FOREIGN KEY (tenant_id, client_id)
        REFERENCES {{.Prefix}}iam_oauth_clients (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_oauth_consents_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_oauth_consents_tenant
        FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_oauth_consents SELECT * FROM {{.Prefix}}iam_oauth_consents_old;
DROP TABLE {{.Prefix}}iam_oauth_consents_old;

ALTER TABLE {{.Prefix}}iam_refresh_tokens RENAME TO {{.Prefix}}iam_refresh_tokens_old;
CREATE TABLE {{.Prefix}}iam_refresh_tokens (
    id_hash TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    family_id TEXT COLLATE BINARY NOT NULL,
    principal_id BIGINT NOT NULL,
    client_id BIGINT NOT NULL,
    scope TEXT COLLATE BINARY NOT NULL DEFAULT '',
    auth_time DATETIME NOT NULL,
    acr INTEGER NOT NULL DEFAULT 0,
    amr TEXT COLLATE BINARY NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    used_at DATETIME NULL,
    revoked_at DATETIME NULL,
    CONSTRAINT {{.Prefix}}fk_oauth_refresh_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_refresh_tokens SELECT * FROM {{.Prefix}}iam_refresh_tokens_old;
DROP TABLE {{.Prefix}}iam_refresh_tokens_old;
CREATE INDEX {{.Prefix}}idx_iam_refresh_family
    ON {{.Prefix}}iam_refresh_tokens (family_id, revoked_at);

-- +goose Down
ALTER TABLE {{.Prefix}}iam_refresh_tokens RENAME TO {{.Prefix}}iam_refresh_tokens_old;
CREATE TABLE {{.Prefix}}iam_refresh_tokens (
    id_hash TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    family_id TEXT COLLATE BINARY NOT NULL,
    principal_id BIGINT NOT NULL,
    client_id BIGINT NOT NULL,
    scope TEXT COLLATE BINARY NOT NULL DEFAULT '',
    auth_time DATETIME NOT NULL,
    acr INTEGER NOT NULL DEFAULT 0,
    amr TEXT COLLATE BINARY NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    used_at DATETIME NULL,
    revoked_at DATETIME NULL
);
INSERT INTO {{.Prefix}}iam_refresh_tokens SELECT * FROM {{.Prefix}}iam_refresh_tokens_old;
DROP TABLE {{.Prefix}}iam_refresh_tokens_old;
CREATE INDEX {{.Prefix}}idx_iam_refresh_family
    ON {{.Prefix}}iam_refresh_tokens (family_id, revoked_at);

ALTER TABLE {{.Prefix}}iam_oauth_consents RENAME TO {{.Prefix}}iam_oauth_consents_old;
CREATE TABLE {{.Prefix}}iam_oauth_consents (
    principal_id BIGINT NOT NULL,
    client_id BIGINT NOT NULL,
    tenant_id BIGINT NOT NULL,
    scope TEXT COLLATE BINARY NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    last_used_at DATETIME NOT NULL,
    PRIMARY KEY (principal_id, client_id),
    CONSTRAINT {{.Prefix}}fk_oauth_consents_client
        FOREIGN KEY (tenant_id, client_id)
        REFERENCES {{.Prefix}}iam_oauth_clients (tenant_id, id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_oauth_consents SELECT * FROM {{.Prefix}}iam_oauth_consents_old;
DROP TABLE {{.Prefix}}iam_oauth_consents_old;

ALTER TABLE {{.Prefix}}iam_oauth_codes RENAME TO {{.Prefix}}iam_oauth_codes_old;
CREATE TABLE {{.Prefix}}iam_oauth_codes (
    code_hash TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    client_id BIGINT NOT NULL,
    principal_id BIGINT NOT NULL,
    redirect_uri TEXT COLLATE BINARY NOT NULL,
    scope TEXT COLLATE BINARY NOT NULL,
    code_challenge TEXT COLLATE BINARY NOT NULL,
    nonce TEXT COLLATE BINARY NOT NULL DEFAULT '',
    auth_time DATETIME NOT NULL,
    acr INTEGER NOT NULL DEFAULT 0,
    amr TEXT COLLATE BINARY NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    consumed_at DATETIME NULL,
    CONSTRAINT {{.Prefix}}fk_oauth_codes_client
        FOREIGN KEY (client_id) REFERENCES {{.Prefix}}iam_oauth_clients (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_oauth_codes SELECT * FROM {{.Prefix}}iam_oauth_codes_old;
DROP TABLE {{.Prefix}}iam_oauth_codes_old;
