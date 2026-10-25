-- +goose Up
ALTER TABLE {{.Prefix}}fleet_agents DROP CONSTRAINT {{.Prefix}}fleet_agents_authorization_mode_check,
 ADD CONSTRAINT {{.Prefix}}ck_fleet_agents_authorization_mode CHECK (authorization_mode IN ('READ_ONLY','WORKSPACE_WRITE','ON_REQUEST','FULL_AUTO'));
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_agents DROP CONSTRAINT {{.Prefix}}ck_fleet_agents_authorization_mode,
 ADD CONSTRAINT {{.Prefix}}fleet_agents_authorization_mode_check CHECK (authorization_mode IN ('READ_ONLY','ON_REQUEST','FULL_AUTO'));
