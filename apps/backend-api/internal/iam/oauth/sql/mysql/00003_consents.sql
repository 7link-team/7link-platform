-- Source: internal/modules/iam/sql/mysql/00003_local_identity.sql:65-76
-- The composite client key enforces the source tenant_id on the referenced client (R12).
-- MySQL DDL auto-commits; this file owns exactly one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_oauth_consents (
    principal_id BIGINT       NOT NULL,
    client_id    BIGINT       NOT NULL,
    tenant_id    BIGINT       NOT NULL,
    scope        TEXT         COLLATE utf8mb4_bin NOT NULL DEFAULT (''),
    created_at   DATETIME(3)  NOT NULL,
    last_used_at DATETIME(3)  NOT NULL,
    PRIMARY KEY (principal_id, client_id),
    CONSTRAINT {{.Prefix}}fk_oauth_consents_client
        FOREIGN KEY (tenant_id, client_id)
        REFERENCES {{.Prefix}}iam_oauth_clients (tenant_id, id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_oauth_consents;
