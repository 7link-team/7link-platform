-- +goose Up
CREATE TABLE worker_ids (
    id         INT         NOT NULL PRIMARY KEY,
    token      VARCHAR(64) NOT NULL,
    expires_at BIGINT      NOT NULL,
    os         VARCHAR(64),
    host       VARCHAR(255),
    ipv4_lan   VARCHAR(255),
    mac        VARCHAR(255),
    disk       TEXT,
    container  TINYINT(1),
    kvm        TINYINT(1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE worker_ids;
