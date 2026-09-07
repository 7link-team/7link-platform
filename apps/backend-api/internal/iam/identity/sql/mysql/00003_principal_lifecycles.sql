-- +goose Up
CREATE TABLE {{.Prefix}}iam_principal_lifecycles (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    version BIGINT NOT NULL CHECK (version >= 1),
    retain_until DATETIME(3) NOT NULL,
    legal_hold INTEGER NOT NULL DEFAULT 0 CHECK (legal_hold IN (0, 1)),
    reference VARCHAR(200) COLLATE utf8mb4_bin NOT NULL,
    anonymized_at DATETIME(3) NULL,
    updated_at DATETIME(3) NOT NULL,
    CONSTRAINT {{.Prefix}}fk_iam_principal_lifecycle_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_principal_lifecycles;
