-- Source: internal/modules/organization/sql/postgres/00007_invitations.sql:1-35.
-- Validation: internal/modules/organization/invitation.go:348-404,462-474.
-- Cross-module invitation-role references are added by 00008_constraints.sql.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_invitations (
    tenant_id     BIGINT         NOT NULL,
    id            BIGINT         NOT NULL,
    email         VARCHAR(320)   COLLATE "C" NOT NULL,
    email_key     VARCHAR(320)   COLLATE "C" NOT NULL,
    token_hmac    CHAR(64)       COLLATE "C" NOT NULL,
    status        VARCHAR(16)    COLLATE "C" NOT NULL,
    department_id BIGINT         NULL,
    expires_at    TIMESTAMPTZ(3) NOT NULL,
    accepted_by   BIGINT         NULL,
    accepted_at   TIMESTAMPTZ(3) NULL,
    revoked_at    TIMESTAMPTZ(3) NULL,
    created_at    TIMESTAMPTZ(3) NOT NULL,
    updated_at    TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_invitations_id UNIQUE (id),
    CONSTRAINT {{.Prefix}}uq_iam_invitations_token UNIQUE (token_hmac),
    CONSTRAINT {{.Prefix}}fk_iam_invitations_tenant
        FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}chk_iam_invitations_status
        CHECK (status IN ('pending', 'accepted', 'revoked'))
);
CREATE INDEX {{.Prefix}}idx_iam_invitations_email
    ON {{.Prefix}}iam_invitations (tenant_id, email_key, status);
CREATE INDEX {{.Prefix}}idx_iam_invitations_expiry
    ON {{.Prefix}}iam_invitations (status, expires_at);

CREATE TABLE {{.Prefix}}iam_invitation_roles (
    tenant_id    BIGINT NOT NULL,
    invitation_id BIGINT NOT NULL,
    role_id      BIGINT NOT NULL,
    PRIMARY KEY (tenant_id, invitation_id, role_id),
    CONSTRAINT {{.Prefix}}fk_iam_invitation_roles_invitation
        FOREIGN KEY (tenant_id, invitation_id)
        REFERENCES {{.Prefix}}iam_invitations (tenant_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_invitation_roles;
DROP TABLE {{.Prefix}}iam_invitations;
