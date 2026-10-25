package model

import (
	"7link.com/platform/internal/infra/guid"
)

type Settings struct {
	TeamOwnerID            guid.ID `gorm:"column:team_owner_id;not null;default:0"`
	WatermelonLagThreshold float64 `gorm:"column:watermelon_lag_threshold;not null"`
	ID                     guid.ID `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID               guid.ID `gorm:"column:tenant_id;not null;uniqueIndex"`
	Timezone               string  `gorm:"column:timezone;not null"`
	DayReportAt            string  `gorm:"column:day_report_at;not null"`
	WeekReportAt           string  `gorm:"column:week_report_at;not null"`
	MonthReportAt          string  `gorm:"column:month_report_at;not null"`
	QuarterReportAt        string  `gorm:"column:quarter_report_at;not null"`
	YearReportAt           string  `gorm:"column:year_report_at;not null"`
	GraceDayMinutes        int     `gorm:"column:grace_day_minutes;not null"`
	GraceWeekMinutes       int     `gorm:"column:grace_week_minutes;not null"`
	GraceMonthMinutes      int     `gorm:"column:grace_month_minutes;not null"`
	GraceQuarterMinutes    int     `gorm:"column:grace_quarter_minutes;not null"`
	GraceYearMinutes       int     `gorm:"column:grace_year_minutes;not null"`
	RequiredMemberKinds    string  `gorm:"column:required_member_kinds;not null"`
	AgentDayReport         bool    `gorm:"column:agent_day_report;not null"`
	Workdays               string  `gorm:"column:workdays;not null"`
	AtRiskThreshold        float64 `gorm:"column:at_risk_threshold;not null"`
	Version                int     `gorm:"column:version;not null"`
}

func (Settings) TableName() string { return "planning_settings" }
