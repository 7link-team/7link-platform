-- +goose Up
CREATE INDEX {{.Prefix}}clientwork_dispatch_expiry ON {{.Prefix}}clientwork_dispatch_proposals (tenant_id,state,expires_at,id);
CREATE INDEX {{.Prefix}}clientwork_share_expiry ON {{.Prefix}}clientwork_preview_shares (tenant_id,state,expires_at,id);
-- +goose Down
DROP INDEX {{.Prefix}}clientwork_share_expiry;
DROP INDEX {{.Prefix}}clientwork_dispatch_expiry;
