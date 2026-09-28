-- Source: internal/modules/iam/sql/postgres/00008_password_recovery.sql:6-33
-- +goose Up
CREATE TABLE {{.Prefix}}iam_password_recovery_tokens (
    token_hmac   CHAR(64)       COLLATE "C" NOT NULL PRIMARY KEY,
    principal_id BIGINT         NOT NULL,
    created_at   TIMESTAMPTZ(3) NOT NULL,
    expires_at   TIMESTAMPTZ(3) NOT NULL,
    consumed_at  TIMESTAMPTZ(3) NULL
);

CREATE INDEX {{.Prefix}}idx_iam_password_recovery_principal
    ON {{.Prefix}}iam_password_recovery_tokens (principal_id, consumed_at, expires_at);

CREATE TABLE {{.Prefix}}iam_notification_outbox (
    id                 BIGINT         NOT NULL PRIMARY KEY,
    kind               VARCHAR(64)    COLLATE "C" NOT NULL,
    recipient          VARCHAR(320)   COLLATE "C" NOT NULL,
    payload_ciphertext TEXT           COLLATE "C" NOT NULL,
    status             VARCHAR(16)    COLLATE "C" NOT NULL,
    attempts           INTEGER        NOT NULL DEFAULT 0,
    available_at       TIMESTAMPTZ(3) NOT NULL,
    locked_at          TIMESTAMPTZ(3) NULL,
    sent_at            TIMESTAMPTZ(3) NULL,
    last_error         VARCHAR(512)   NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ(3) NOT NULL,
    updated_at         TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_notification_outbox_status
        CHECK (status IN ('pending', 'delivering', 'sent', 'failed'))
);

CREATE INDEX {{.Prefix}}idx_iam_notification_outbox_pending
    ON {{.Prefix}}iam_notification_outbox (status, available_at, created_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_notification_outbox;
DROP TABLE {{.Prefix}}iam_password_recovery_tokens;
