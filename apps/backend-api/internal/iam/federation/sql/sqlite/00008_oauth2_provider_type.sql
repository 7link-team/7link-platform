-- Admits social providers, which speak plain OAuth 2.0 and are identified by a
-- connector key in the issuer column rather than by a discoverable issuer URL.
-- Validation: internal/iam/federation/service.go normalizeProviderIssuer.
--
-- SQLite cannot alter a named table CHECK, so the table is rebuilt in place. The
-- replacement is created under the real name and the previous table is the one
-- renamed, because the migration suite reads ownership from the CREATE TABLE
-- statements: a scratch name would register as a table this module owns.
-- Mirrors internal/iam/authn/sql/sqlite/00009_constraints.sql.
--
-- legacy_alter_table matters here and not in that precedent: this is a parent
-- table, and a modern RENAME rewrites the three child foreign keys to follow it.
-- They must keep pointing at the name being recreated, so reference rewriting is
-- turned off for the swap. Foreign keys are off as well, because dropping the
-- renamed table with enforcement on would be rejected.
-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;
BEGIN IMMEDIATE;
ALTER TABLE {{.Prefix}}iam_identity_providers RENAME TO {{.Prefix}}iam_identity_providers_old;
CREATE TABLE {{.Prefix}}iam_identity_providers (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    provider_type TEXT COLLATE BINARY NOT NULL DEFAULT 'oidc',
    issuer TEXT COLLATE BINARY NOT NULL,
    client_id TEXT COLLATE BINARY NOT NULL,
    client_secret_ciphertext TEXT COLLATE BINARY NOT NULL DEFAULT '',
    scopes TEXT COLLATE BINARY NOT NULL DEFAULT 'openid profile email',
    auto_provision INTEGER NOT NULL DEFAULT 1 CHECK (auto_provision IN (0, 1)),
    default_role_id BIGINT NULL,
    status TEXT COLLATE BINARY NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_identity_providers_type
        CHECK (provider_type IN ('oidc', 'saml', 'oauth2')),
    CONSTRAINT {{.Prefix}}chk_iam_identity_providers_fields
        CHECK (length(CAST(name AS BLOB)) >= 1 AND length(CAST(name AS BLOB)) <= 128
            AND length(CAST(issuer AS BLOB)) >= 1 AND length(CAST(issuer AS BLOB)) <= 255
            AND length(CAST(client_id AS BLOB)) >= 1 AND length(CAST(client_id AS BLOB)) <= 128
            AND length(CAST(scopes AS BLOB)) >= 1 AND length(CAST(scopes AS BLOB)) <= 255),
    CONSTRAINT {{.Prefix}}uq_iam_identity_providers_tenant_issuer UNIQUE (tenant_id, issuer),
    CONSTRAINT {{.Prefix}}uq_iam_identity_providers_tenant_id UNIQUE (tenant_id, id)
);
INSERT INTO {{.Prefix}}iam_identity_providers
SELECT id, tenant_id, name, provider_type, issuer, client_id, client_secret_ciphertext,
    scopes, auto_provision, default_role_id, status, created_at, updated_at
FROM {{.Prefix}}iam_identity_providers_old;
DROP TABLE {{.Prefix}}iam_identity_providers_old;
CREATE INDEX {{.Prefix}}idx_iam_identity_providers_tenant ON {{.Prefix}}iam_identity_providers (tenant_id, status, name);
COMMIT;
PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;

-- +goose Down
PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;
BEGIN IMMEDIATE;
ALTER TABLE {{.Prefix}}iam_identity_providers RENAME TO {{.Prefix}}iam_identity_providers_old;
CREATE TABLE {{.Prefix}}iam_identity_providers (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    provider_type TEXT COLLATE BINARY NOT NULL DEFAULT 'oidc',
    issuer TEXT COLLATE BINARY NOT NULL,
    client_id TEXT COLLATE BINARY NOT NULL,
    client_secret_ciphertext TEXT COLLATE BINARY NOT NULL DEFAULT '',
    scopes TEXT COLLATE BINARY NOT NULL DEFAULT 'openid profile email',
    auto_provision INTEGER NOT NULL DEFAULT 1 CHECK (auto_provision IN (0, 1)),
    default_role_id BIGINT NULL,
    status TEXT COLLATE BINARY NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_identity_providers_type
        CHECK (provider_type IN ('oidc', 'saml')),
    CONSTRAINT {{.Prefix}}chk_iam_identity_providers_fields
        CHECK (length(CAST(name AS BLOB)) >= 1 AND length(CAST(name AS BLOB)) <= 128
            AND length(CAST(issuer AS BLOB)) >= 1 AND length(CAST(issuer AS BLOB)) <= 255
            AND length(CAST(client_id AS BLOB)) >= 1 AND length(CAST(client_id AS BLOB)) <= 128
            AND length(CAST(scopes AS BLOB)) >= 1 AND length(CAST(scopes AS BLOB)) <= 255),
    CONSTRAINT {{.Prefix}}uq_iam_identity_providers_tenant_issuer UNIQUE (tenant_id, issuer),
    CONSTRAINT {{.Prefix}}uq_iam_identity_providers_tenant_id UNIQUE (tenant_id, id)
);
INSERT INTO {{.Prefix}}iam_identity_providers
SELECT id, tenant_id, name, provider_type, issuer, client_id, client_secret_ciphertext,
    scopes, auto_provision, default_role_id, status, created_at, updated_at
FROM {{.Prefix}}iam_identity_providers_old;
DROP TABLE {{.Prefix}}iam_identity_providers_old;
CREATE INDEX {{.Prefix}}idx_iam_identity_providers_tenant ON {{.Prefix}}iam_identity_providers (tenant_id, status, name);
COMMIT;
PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;
