-- Source: internal/modules/iam/sql/mysql/00008_password_recovery.sql:7-36
-- MySQL DDL auto-commits; these tables recover independently, with the outbox dropped first.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_password_recovery_tokens (
    token_hmac   CHAR(64)     COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    principal_id BIGINT       NOT NULL,
    created_at   DATETIME(3)  NOT NULL,
    expires_at   DATETIME(3)  NOT NULL,
    consumed_at  DATETIME(3)  NULL,
    INDEX {{.Prefix}}idx_iam_password_recovery_principal (principal_id, consumed_at, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_notification_outbox (
    id                 BIGINT        NOT NULL PRIMARY KEY,
    kind               VARCHAR(64)   COLLATE utf8mb4_bin NOT NULL,
    recipient          VARCHAR(320)  COLLATE utf8mb4_bin NOT NULL,
    payload_ciphertext TEXT          COLLATE utf8mb4_bin NOT NULL,
    status             VARCHAR(16)   COLLATE utf8mb4_bin NOT NULL,
    attempts           INT           NOT NULL DEFAULT 0,
    available_at       DATETIME(3)   NOT NULL,
    locked_at          DATETIME(3)   NULL,
    sent_at            DATETIME(3)   NULL,
    last_error         VARCHAR(512)  NOT NULL DEFAULT '',
    created_at         DATETIME(3)   NOT NULL,
    updated_at         DATETIME(3)   NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_notification_outbox_status
        CHECK (status IN ('pending', 'delivering', 'sent', 'failed')),
    INDEX {{.Prefix}}idx_iam_notification_outbox_pending (status, available_at, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_notification_outbox;
DROP TABLE {{.Prefix}}iam_password_recovery_tokens;
