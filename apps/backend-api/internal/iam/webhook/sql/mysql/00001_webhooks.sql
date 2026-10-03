-- A tenant's standing requests to be told about its own events, and the work
-- owed against them. after_sequence is the subscription's cursor into that
-- tenant's audit chain, which is gapless and per tenant, so one column is enough
-- to know what has already been offered.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_webhook_subscriptions (
    id                BIGINT         NOT NULL PRIMARY KEY,
    tenant_id         BIGINT         NOT NULL,
    name              VARCHAR(128)   NOT NULL,
    url               VARCHAR(2048)  COLLATE utf8mb4_bin NOT NULL,
    secret_ciphertext TEXT           COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    event_types       VARCHAR(1024)  COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    status            VARCHAR(16)    COLLATE utf8mb4_bin NOT NULL DEFAULT 'active',
    after_sequence    BIGINT         NOT NULL DEFAULT 0,
    created_at        DATETIME(3) NOT NULL,
    updated_at        DATETIME(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_webhook_subscriptions_status
        CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_webhook_subscriptions_fields
        CHECK (OCTET_LENGTH(name) >= 1 AND OCTET_LENGTH(name) <= 128
            AND OCTET_LENGTH(url) >= 1 AND OCTET_LENGTH(url) <= 2048
            AND after_sequence >= 0),
    CONSTRAINT {{.Prefix}}uq_iam_webhook_subscriptions_tenant_name UNIQUE (tenant_id, name),
    CONSTRAINT {{.Prefix}}uq_iam_webhook_subscriptions_tenant_id UNIQUE (tenant_id, id)
);
CREATE INDEX {{.Prefix}}idx_iam_webhook_subscriptions_due
    ON {{.Prefix}}iam_webhook_subscriptions (status, tenant_id, id);

CREATE TABLE {{.Prefix}}iam_webhook_deliveries (
    id              BIGINT         NOT NULL PRIMARY KEY,
    subscription_id BIGINT         NOT NULL,
    tenant_id       BIGINT         NOT NULL,
    event_id        BIGINT         NOT NULL,
    event_sequence  BIGINT         NOT NULL,
    event_type      VARCHAR(64)    COLLATE utf8mb4_bin NOT NULL,
    status          VARCHAR(16)    COLLATE utf8mb4_bin NOT NULL DEFAULT 'pending',
    attempts        INTEGER        NOT NULL DEFAULT 0,
    available_at    DATETIME(3) NOT NULL,
    locked_at       DATETIME(3) NULL,
    delivered_at    DATETIME(3) NULL,
    last_error      VARCHAR(512)   NOT NULL DEFAULT '',
    created_at      DATETIME(3) NOT NULL,
    updated_at      DATETIME(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_webhook_deliveries_status
        CHECK (status IN ('pending', 'delivering', 'delivered', 'failed')),
    CONSTRAINT {{.Prefix}}chk_iam_webhook_deliveries_fields
        CHECK (attempts >= 0 AND event_sequence > 0),
    -- One event is owed to one subscription once. The uniqueness is what makes a
    -- fan-out pass that runs twice harmless.
    CONSTRAINT {{.Prefix}}uq_iam_webhook_deliveries_subscription_event
        UNIQUE (subscription_id, event_id),
    CONSTRAINT {{.Prefix}}fk_iam_webhook_deliveries_subscription
        FOREIGN KEY (tenant_id, subscription_id)
        REFERENCES {{.Prefix}}iam_webhook_subscriptions (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_iam_webhook_deliveries_pending
    ON {{.Prefix}}iam_webhook_deliveries (status, available_at, id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_webhook_deliveries;
DROP TABLE {{.Prefix}}iam_webhook_subscriptions;
