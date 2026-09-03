-- Source: internal/modules/iam/sql/sqlite/00003_local_identity.sql:85-96
-- The composite client key enforces the source tenant_id on the referenced client (R12).
-- +goose Up
CREATE TABLE {{.Prefix}}iam_oauth_consents (
    principal_id BIGINT   NOT NULL,
    client_id    BIGINT   NOT NULL,
    tenant_id    BIGINT   NOT NULL,
    scope        TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL,
    last_used_at DATETIME NOT NULL,
    PRIMARY KEY (principal_id, client_id),
    CONSTRAINT {{.Prefix}}fk_oauth_consents_client
        FOREIGN KEY (tenant_id, client_id)
        REFERENCES {{.Prefix}}iam_oauth_clients (tenant_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_oauth_consents;
