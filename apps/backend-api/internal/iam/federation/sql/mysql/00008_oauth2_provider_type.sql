-- Admits social providers, which speak plain OAuth 2.0 and are identified by a
-- connector key in the issuer column rather than by a discoverable issuer URL.
-- Validation: internal/iam/federation/service.go normalizeProviderIssuer.
-- The down migration restores the narrower set and therefore fails while any
-- oauth2 provider is still registered. That is deliberate: dropping a tenant's
-- provider would orphan the identity links that reference it.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_identity_providers
    DROP CHECK {{.Prefix}}chk_iam_identity_providers_type;

ALTER TABLE {{.Prefix}}iam_identity_providers
    ADD CONSTRAINT {{.Prefix}}chk_iam_identity_providers_type
        CHECK (provider_type IN ('oidc', 'saml', 'oauth2'));

-- +goose Down
ALTER TABLE {{.Prefix}}iam_identity_providers
    DROP CHECK {{.Prefix}}chk_iam_identity_providers_type;

ALTER TABLE {{.Prefix}}iam_identity_providers
    ADD CONSTRAINT {{.Prefix}}chk_iam_identity_providers_type
        CHECK (provider_type IN ('oidc', 'saml'));
