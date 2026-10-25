-- +goose Up
CREATE TABLE {{.Prefix}}planning_settings (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL UNIQUE,
 timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai',
 day_report_at VARCHAR(5) NOT NULL DEFAULT '18:00',
 week_report_at VARCHAR(16) NOT NULL DEFAULT 'MON 09:00',
 month_report_at VARCHAR(16) NOT NULL DEFAULT 'D1 09:00',
 quarter_report_at VARCHAR(16) NOT NULL DEFAULT 'D1 09:00',
 year_report_at VARCHAR(16) NOT NULL DEFAULT 'D2 09:00',
 grace_day_minutes INTEGER NOT NULL DEFAULT 960 CHECK (grace_day_minutes>=0),
 grace_week_minutes INTEGER NOT NULL DEFAULT 1620 CHECK (grace_week_minutes>=0),
 grace_month_minutes INTEGER NOT NULL DEFAULT 4320 CHECK (grace_month_minutes>=0),
 grace_quarter_minutes INTEGER NOT NULL DEFAULT 7200 CHECK (grace_quarter_minutes>=0),
 grace_year_minutes INTEGER NOT NULL DEFAULT 14400 CHECK (grace_year_minutes>=0),
 required_member_kinds JSONB NOT NULL,
 agent_day_report BOOLEAN NOT NULL DEFAULT true,
 workdays JSONB NOT NULL,
 at_risk_threshold NUMERIC(8,6) NOT NULL DEFAULT 0.15 CHECK (at_risk_threshold>=0 AND at_risk_threshold<=1),
 version INTEGER NOT NULL DEFAULT 1 CHECK (version>0)
);
-- +goose Down
DROP TABLE {{.Prefix}}planning_settings;
