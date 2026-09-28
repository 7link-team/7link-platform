-- +goose Up
CREATE TABLE {{.Prefix}}iam_principal_lifecycles (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    version BIGINT NOT NULL CHECK (version >= 1),
    retain_until DATETIME NOT NULL,
    legal_hold INTEGER NOT NULL DEFAULT 0 CHECK (legal_hold IN (0, 1)),
    reference TEXT COLLATE BINARY NOT NULL,
    anonymized_at DATETIME NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}fk_iam_principal_lifecycle_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE RESTRICT
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_principal_lifecycles;
