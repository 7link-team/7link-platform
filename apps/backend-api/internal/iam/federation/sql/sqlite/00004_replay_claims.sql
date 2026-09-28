-- Single-use claims for federation protocol identifiers: OIDC/SAML state
-- nonces, SAML assertion IDs, and SAML logout request IDs.
--
-- This lived in a process-local map, which is single-use only for a deployment
-- running one replica. With two, a callback consumed on one of them is still
-- unclaimed on the other, and replaying it there succeeds -- so the protection
-- silently disappears at the moment a deployment scales out. The database is
-- the only state every replica shares here, and the unique primary key is what
-- makes the claim atomic between them: the insert either wins or violates.
--
-- Rows are swept once their window closes; expires_at carries an index so the
-- sweep stays a range delete rather than a scan.
-- Validation: internal/iam/federation/replay.go; internal/iam/federation/service.go.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_federation_replay_claims (
    claim_key TEXT COLLATE BINARY PRIMARY KEY,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_federation_replay_claims_key
        CHECK (length(CAST(claim_key AS BLOB)) >= 1 AND length(CAST(claim_key AS BLOB)) <= 255)
);
CREATE INDEX {{.Prefix}}idx_iam_federation_replay_claims_expiry
    ON {{.Prefix}}iam_federation_replay_claims (expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_federation_replay_claims;
