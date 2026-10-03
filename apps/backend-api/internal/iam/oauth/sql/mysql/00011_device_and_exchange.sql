-- The device authorization grant (RFC 8628) and token exchange (RFC 8693).
--
-- A device with no browser shows a short user code and polls; both identifiers
-- are stored hashed, as the authorization code is, so a database read cannot be
-- redeemed. An exchange grant is the written-down permission for one client to
-- act as a subject: without a row a client cannot exchange at all, which is what
-- keeps impersonation from being open-ended.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_oauth_device_authorizations (
    device_code_hash CHAR(64)       COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    user_code_hash   CHAR(64)       COLLATE utf8mb4_bin NOT NULL,
    client_id        BIGINT         NOT NULL,
    tenant_id        BIGINT         NOT NULL,
    scope            TEXT           COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    status           VARCHAR(16)    COLLATE utf8mb4_bin NOT NULL DEFAULT 'pending',
    principal_id     BIGINT         NULL,
    interval_seconds INTEGER        NOT NULL DEFAULT 5,
    last_polled_at   DATETIME(3) NULL,
    auth_time        DATETIME(3) NULL,
    acr              INTEGER        NOT NULL DEFAULT 0,
    amr              TEXT           COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    created_at       DATETIME(3) NOT NULL,
    expires_at       DATETIME(3) NOT NULL,
    consumed_at      DATETIME(3) NULL,
    CONSTRAINT {{.Prefix}}chk_iam_oauth_device_status
        CHECK (status IN ('pending', 'approved', 'denied')),
    CONSTRAINT {{.Prefix}}chk_iam_oauth_device_fields
        CHECK (interval_seconds > 0 AND acr >= 0
            AND (status <> 'approved' OR (principal_id IS NOT NULL AND auth_time IS NOT NULL))),
    -- A person types the user code, so finding a request by it has to be exact
    -- and unique; two pending requests sharing one code would let an approval
    -- land on the wrong device.
    CONSTRAINT {{.Prefix}}uq_iam_oauth_device_user_code UNIQUE (user_code_hash)
);
CREATE INDEX {{.Prefix}}idx_iam_oauth_device_expiry
    ON {{.Prefix}}iam_oauth_device_authorizations (expires_at, status);

CREATE TABLE {{.Prefix}}iam_oauth_exchange_grants (
    id             BIGINT         NOT NULL PRIMARY KEY,
    tenant_id      BIGINT         NOT NULL,
    client_id      BIGINT         NOT NULL,
    subject_prefix VARCHAR(128)   COLLATE utf8mb4_bin NOT NULL,
    scope          TEXT           COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    status         VARCHAR(16)    COLLATE utf8mb4_bin NOT NULL DEFAULT 'active',
    created_at     DATETIME(3) NOT NULL,
    updated_at     DATETIME(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_oauth_exchange_status
        CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_oauth_exchange_fields
        CHECK (OCTET_LENGTH(subject_prefix) >= 1 AND OCTET_LENGTH(subject_prefix) <= 128),
    CONSTRAINT {{.Prefix}}uq_iam_oauth_exchange_pair UNIQUE (tenant_id, client_id, subject_prefix),
    CONSTRAINT {{.Prefix}}fk_iam_oauth_exchange_client
        FOREIGN KEY (tenant_id, client_id)
        REFERENCES {{.Prefix}}iam_oauth_clients (tenant_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_oauth_exchange_grants;
DROP TABLE {{.Prefix}}iam_oauth_device_authorizations;
