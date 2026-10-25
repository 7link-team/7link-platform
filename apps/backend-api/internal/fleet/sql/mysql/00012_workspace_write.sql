-- +goose Up
ALTER TABLE {{.Prefix}}fleet_agents DROP CHECK {{.Prefix}}fleet_agents_chk_3,
 ADD CONSTRAINT {{.Prefix}}ck_fleet_agents_authorization_mode CHECK (authorization_mode IN ('READ_ONLY','WORKSPACE_WRITE','ON_REQUEST','FULL_AUTO'));
-- +goose Down
ALTER TABLE {{.Prefix}}fleet_agents DROP CHECK {{.Prefix}}ck_fleet_agents_authorization_mode,
 ADD CONSTRAINT {{.Prefix}}fleet_agents_chk_3 CHECK (authorization_mode IN ('READ_ONLY','ON_REQUEST','FULL_AUTO'));
