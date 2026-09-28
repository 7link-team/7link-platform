-- Source: internal/modules/iam/sql/sqlite/00008_password_recovery.sql:6-34
-- +goose Up
CREATE TABLE {{.Prefix}}iam_password_recovery_tokens (
    token_hmac   TEXT     COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT   NOT NULL,
    created_at   DATETIME NOT NULL,
    expires_at   DATETIME NOT NULL,
    consumed_at  DATETIME NULL
);

CREATE INDEX {{.Prefix}}idx_iam_password_recovery_principal
    ON {{.Prefix}}iam_password_recovery_tokens (principal_id, consumed_at, expires_at);

CREATE TABLE {{.Prefix}}iam_notification_outbox (
    id                 BIGINT   NOT NULL PRIMARY KEY,
    kind               TEXT     COLLATE BINARY NOT NULL,
    recipient          TEXT     COLLATE BINARY NOT NULL,
    payload_ciphertext TEXT     COLLATE BINARY NOT NULL,
    status             TEXT     COLLATE BINARY NOT NULL,
    attempts           INTEGER  NOT NULL DEFAULT 0,
    available_at       DATETIME NOT NULL,
    locked_at          DATETIME NULL,
    sent_at            DATETIME NULL,
    last_error         TEXT     NOT NULL DEFAULT '',
    created_at         DATETIME NOT NULL,
    updated_at         DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_notification_outbox_status
        CHECK (status IN ('pending', 'delivering', 'sent', 'failed'))
);

CREATE INDEX {{.Prefix}}idx_iam_notification_outbox_pending
    ON {{.Prefix}}iam_notification_outbox (status, available_at, created_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_notification_outbox;
DROP TABLE {{.Prefix}}iam_password_recovery_tokens;
