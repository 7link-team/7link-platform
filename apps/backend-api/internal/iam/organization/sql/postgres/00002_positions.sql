-- Source: internal/modules/organization/sql/postgres/00002_positions.sql:1-33.
-- Validation: internal/modules/organization/position.go:63-120.
-- Position code is already the lower(trim) normalized key written by the source service.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_positions (
    tenant_id BIGINT         NOT NULL,
    id        BIGINT         NOT NULL,
    code      VARCHAR(64)    COLLATE "C" NOT NULL,
    name      VARCHAR(128)   NOT NULL,
    status    VARCHAR(16)    COLLATE "C" NOT NULL,
    sort_order INTEGER       NOT NULL DEFAULT 0,
    version   BIGINT         NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_positions_code UNIQUE (tenant_id, code),
    CONSTRAINT {{.Prefix}}chk_iam_positions_status
        CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_positions_sort_order
        CHECK (sort_order >= 0 AND sort_order <= 1000000),
    CONSTRAINT {{.Prefix}}chk_iam_positions_version CHECK (version >= 1)
);
CREATE INDEX {{.Prefix}}idx_iam_positions_order
    ON {{.Prefix}}iam_positions (tenant_id, sort_order, code, id);

CREATE TABLE {{.Prefix}}iam_position_members (
    tenant_id    BIGINT         NOT NULL,
    position_id  BIGINT         NOT NULL,
    principal_id BIGINT         NOT NULL,
    starts_at    TIMESTAMPTZ(3) NULL,
    ends_at      TIMESTAMPTZ(3) NULL,
    created_at   TIMESTAMPTZ(3) NOT NULL,
    updated_at   TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, position_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_position_members_position
        FOREIGN KEY (tenant_id, position_id)
        REFERENCES {{.Prefix}}iam_positions (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_position_members_window
        CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);
CREATE INDEX {{.Prefix}}idx_iam_position_members_principal
    ON {{.Prefix}}iam_position_members
        (tenant_id, principal_id, starts_at, ends_at, position_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_position_members;
DROP TABLE {{.Prefix}}iam_positions;
