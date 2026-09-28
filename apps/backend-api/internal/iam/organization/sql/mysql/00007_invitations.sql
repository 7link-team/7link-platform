-- Source: internal/modules/organization/sql/mysql/00007_invitations.sql:1-37.
-- Validation: internal/modules/organization/invitation.go:348-404,462-474.
-- Cross-module invitation-role references are added by 00008_constraints.sql.
-- MySQL DDL auto-commits; each base version is independently recoverable.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_invitations (
    tenant_id     BIGINT       NOT NULL,
    id            BIGINT       NOT NULL,
    email         VARCHAR(320) COLLATE utf8mb4_bin NOT NULL,
    email_key     VARCHAR(320) COLLATE utf8mb4_bin NOT NULL,
    token_hmac    CHAR(64)     COLLATE utf8mb4_bin NOT NULL,
    status        VARCHAR(16)  COLLATE utf8mb4_bin NOT NULL,
    department_id BIGINT       NULL,
    expires_at    DATETIME(3)  NOT NULL,
    accepted_by   BIGINT       NULL,
    accepted_at   DATETIME(3)  NULL,
    revoked_at    DATETIME(3)  NULL,
    created_at    DATETIME(3)  NOT NULL,
    updated_at    DATETIME(3)  NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_invitations_id UNIQUE (id),
    CONSTRAINT {{.Prefix}}uq_iam_invitations_token UNIQUE (token_hmac),
    CONSTRAINT {{.Prefix}}fk_iam_invitations_tenant
        FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_invitations_status
        CHECK (status IN ('pending', 'accepted', 'revoked')),
    INDEX {{.Prefix}}idx_iam_invitations_email (tenant_id, email_key, status),
    INDEX {{.Prefix}}idx_iam_invitations_expiry (status, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_invitation_roles (
    tenant_id     BIGINT NOT NULL,
    invitation_id BIGINT NOT NULL,
    role_id       BIGINT NOT NULL,
    PRIMARY KEY (tenant_id, invitation_id, role_id),
    CONSTRAINT {{.Prefix}}fk_iam_invitation_roles_invitation
        FOREIGN KEY (tenant_id, invitation_id)
        REFERENCES {{.Prefix}}iam_invitations (tenant_id, id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_invitation_roles;
DROP TABLE {{.Prefix}}iam_invitations;
