package planning

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PlanningSettingsValue struct {
	TeamOwnerID            guid.ID  `json:"team_owner_id"`
	WatermelonLagThreshold float64  `json:"watermelon_lag_threshold" minimum:"0.10" maximum:"0.60"`
	Timezone               string   `json:"timezone" maxLength:"64"`
	DayReportAt            string   `json:"day_report_at"`
	WeekReportAt           string   `json:"week_report_at"`
	MonthReportAt          string   `json:"month_report_at"`
	QuarterReportAt        string   `json:"quarter_report_at"`
	YearReportAt           string   `json:"year_report_at"`
	GraceDayMinutes        int      `json:"grace_day_minutes" minimum:"0"`
	GraceWeekMinutes       int      `json:"grace_week_minutes" minimum:"0"`
	GraceMonthMinutes      int      `json:"grace_month_minutes" minimum:"0"`
	GraceQuarterMinutes    int      `json:"grace_quarter_minutes" minimum:"0"`
	GraceYearMinutes       int      `json:"grace_year_minutes" minimum:"0"`
	RequiredMemberKinds    []string `json:"required_member_kinds"`
	AgentDayReport         bool     `json:"agent_day_report"`
	Workdays               []int    `json:"workdays"`
	AtRiskThreshold        float64  `json:"at_risk_threshold" minimum:"0" maximum:"1"`
}

type PlanningSettings struct {
	PlanningSettingsValue
	Version int `json:"version"`
}

// Pointer fields preserve PATCH's distinction between absent and zero/false.
type PlanningSettingsPatch struct {
	TeamOwnerID            *guid.ID  `json:"team_owner_id,omitempty"`
	WatermelonLagThreshold *float64  `json:"watermelon_lag_threshold,omitempty" minimum:"0.10" maximum:"0.60"`
	Timezone               *string   `json:"timezone,omitempty" maxLength:"64"`
	DayReportAt            *string   `json:"day_report_at,omitempty"`
	WeekReportAt           *string   `json:"week_report_at,omitempty"`
	MonthReportAt          *string   `json:"month_report_at,omitempty"`
	QuarterReportAt        *string   `json:"quarter_report_at,omitempty"`
	YearReportAt           *string   `json:"year_report_at,omitempty"`
	GraceDayMinutes        *int      `json:"grace_day_minutes,omitempty" minimum:"0"`
	GraceWeekMinutes       *int      `json:"grace_week_minutes,omitempty" minimum:"0"`
	GraceMonthMinutes      *int      `json:"grace_month_minutes,omitempty" minimum:"0"`
	GraceQuarterMinutes    *int      `json:"grace_quarter_minutes,omitempty" minimum:"0"`
	GraceYearMinutes       *int      `json:"grace_year_minutes,omitempty" minimum:"0"`
	RequiredMemberKinds    *[]string `json:"required_member_kinds,omitempty"`
	AgentDayReport         *bool     `json:"agent_day_report,omitempty"`
	Workdays               *[]int    `json:"workdays,omitempty"`
	AtRiskThreshold        *float64  `json:"at_risk_threshold,omitempty" minimum:"0" maximum:"1"`
}

func defaultPlanningSettings() PlanningSettings {
	return PlanningSettings{PlanningSettingsValue: PlanningSettingsValue{WatermelonLagThreshold: 0.30, Timezone: "Asia/Shanghai", DayReportAt: "18:00", WeekReportAt: "MON 09:00", MonthReportAt: "D1 09:00", QuarterReportAt: "D1 09:00", YearReportAt: "D2 09:00", GraceDayMinutes: 960, GraceWeekMinutes: 1620, GraceMonthMinutes: 4320, GraceQuarterMinutes: 7200, GraceYearMinutes: 14400, RequiredMemberKinds: []string{"WEEK", "MONTH", "QUARTER", "YEAR"}, AgentDayReport: true, Workdays: []int{1, 2, 3, 4, 5}, AtRiskThreshold: 0.15}, Version: 1}
}

var reportClock = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)

func validPlanningSettings(v PlanningSettingsValue) bool {
	if v.TeamOwnerID < 0 || v.Workdays == nil || v.RequiredMemberKinds == nil {
		return false
	}
	if len(v.Timezone) > 64 || v.Timezone == "" || v.Timezone == "Local" {
		return false
	}
	if _, e := time.LoadLocation(v.Timezone); e != nil {
		return false
	}
	if !reportClock.MatchString(v.DayReportAt) {
		return false
	}
	week := strings.Split(v.WeekReportAt, " ")
	if len(week) != 2 || !slices.Contains([]string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}, week[0]) || !reportClock.MatchString(week[1]) {
		return false
	}
	for _, s := range []string{v.MonthReportAt, v.QuarterReportAt, v.YearReportAt} {
		parts := strings.Split(s, " ")
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "D") || !reportClock.MatchString(parts[1]) {
			return false
		}
		n, e := strconv.Atoi(strings.TrimPrefix(parts[0], "D"))
		if e != nil || n < 1 || n > 31 || parts[0] != "D"+strconv.Itoa(n) {
			return false
		}
	}
	for _, n := range []int{v.GraceDayMinutes, v.GraceWeekMinutes, v.GraceMonthMinutes, v.GraceQuarterMinutes, v.GraceYearMinutes} {
		if n < 0 {
			return false
		}
	}
	seen := map[string]bool{}
	for _, kind := range v.RequiredMemberKinds {
		if seen[kind] || !slices.Contains([]string{"DAY", "WEEK", "MONTH", "QUARTER", "YEAR"}, kind) {
			return false
		}
		seen[kind] = true
	}
	days := map[int]bool{}
	for _, day := range v.Workdays {
		if day < 1 || day > 7 || days[day] {
			return false
		}
		days[day] = true
	}
	return !math.IsNaN(v.WatermelonLagThreshold) && v.WatermelonLagThreshold >= 0.10 && v.WatermelonLagThreshold <= 0.60 && !math.IsNaN(v.AtRiskThreshold) && v.AtRiskThreshold >= 0 && v.AtRiskThreshold <= 1
}

func (m *Module) readSettingsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) (PlanningSettings, error) {
	q := query.BindSettings(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return defaultPlanningSettings(), nil
	}
	if e != nil {
		return PlanningSettings{}, e
	}
	v := PlanningSettings{PlanningSettingsValue: PlanningSettingsValue{TeamOwnerID: r.TeamOwnerID, WatermelonLagThreshold: r.WatermelonLagThreshold, Timezone: r.Timezone, DayReportAt: r.DayReportAt, WeekReportAt: r.WeekReportAt, MonthReportAt: r.MonthReportAt, QuarterReportAt: r.QuarterReportAt, YearReportAt: r.YearReportAt, GraceDayMinutes: r.GraceDayMinutes, GraceWeekMinutes: r.GraceWeekMinutes, GraceMonthMinutes: r.GraceMonthMinutes, GraceQuarterMinutes: r.GraceQuarterMinutes, GraceYearMinutes: r.GraceYearMinutes, AgentDayReport: r.AgentDayReport, AtRiskThreshold: r.AtRiskThreshold}, Version: r.Version}
	if json.Unmarshal([]byte(r.RequiredMemberKinds), &v.RequiredMemberKinds) != nil || json.Unmarshal([]byte(r.Workdays), &v.Workdays) != nil || !validPlanningSettings(v.PlanningSettingsValue) || v.Version < 1 {
		return PlanningSettings{}, ErrUnavailable
	}
	return v, nil
}

func (m *Module) patchSettingsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, expected int, patch PlanningSettingsPatch) (PlanningSettings, error) {
	if _, e := m.authorize(ctx, tx, tenant, 0, 0, "planning_settings_manage", true); e != nil {
		return PlanningSettings{}, e
	}
	q := query.BindSettings(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		return PlanningSettings{}, e
	}
	old, e := m.readSettingsOn(ctx, tx, tenant)
	if e != nil {
		return PlanningSettings{}, e
	}
	if old.Version != expected {
		return PlanningSettings{}, ErrConflict
	}
	// JSON merges only present pointer fields, keeping explicit false and zero.
	b, e := json.Marshal(patch)
	if e != nil {
		return PlanningSettings{}, ErrInvalid
	}
	v := old.PlanningSettingsValue
	if json.Unmarshal(b, &v) != nil || !validPlanningSettings(v) {
		return PlanningSettings{}, ErrInvalid
	}
	if v.TeamOwnerID < 0 {
		return PlanningSettings{}, ErrInvalid
	}
	if v.TeamOwnerID > 0 {
		p, found, err := m.principals.LockActiveRow(ctx, tx, v.TeamOwnerID)
		if err != nil {
			return PlanningSettings{}, err
		}
		_, human, err := m.principals.ActiveHumanByID(ctx, tx, v.TeamOwnerID)
		if err != nil {
			return PlanningSettings{}, err
		}
		member, err := m.members.IsMemberActiveOn(ctx, tx, tenant, v.TeamOwnerID)
		if err != nil {
			return PlanningSettings{}, err
		}
		if !found || p.ID != v.TeamOwnerID || !human || !member {
			return PlanningSettings{}, ErrInvalid
		}
	}
	kinds, _ := json.Marshal(v.RequiredMemberKinds)
	days, _ := json.Marshal(v.Workdays)
	row := model.Settings{TeamOwnerID: v.TeamOwnerID, WatermelonLagThreshold: v.WatermelonLagThreshold, TenantID: tenant, Timezone: v.Timezone, DayReportAt: v.DayReportAt, WeekReportAt: v.WeekReportAt, MonthReportAt: v.MonthReportAt, QuarterReportAt: v.QuarterReportAt, YearReportAt: v.YearReportAt, GraceDayMinutes: v.GraceDayMinutes, GraceWeekMinutes: v.GraceWeekMinutes, GraceMonthMinutes: v.GraceMonthMinutes, GraceQuarterMinutes: v.GraceQuarterMinutes, GraceYearMinutes: v.GraceYearMinutes, RequiredMemberKinds: string(kinds), AgentDayReport: v.AgentDayReport, Workdays: string(days), AtRiskThreshold: v.AtRiskThreshold, Version: expected + 1}
	if r == nil {
		id, e := m.ids.Next()
		if e != nil {
			return PlanningSettings{}, e
		}
		if id <= 0 {
			return PlanningSettings{}, ErrUnavailable
		}
		row.ID = guid.ID(id)
		if e = q.WithContext(ctx).Create(&row); e != nil {
			return PlanningSettings{}, e
		}
	} else {
		row.ID = r.ID
		result, e := q.WithContext(ctx).Where(q.ID.Eq(r.ID), q.Version.Eq(expected)).Select(q.ALL).Updates(&row)
		if e != nil {
			return PlanningSettings{}, e
		}
		if result.RowsAffected != 1 {
			return PlanningSettings{}, ErrConflict
		}
	}
	actor := authn.PrincipalIDFromContext(ctx)
	detail := map[string]any{"before": old, "after": v, "version": row.Version, "authority_source": "permission:planning_settings_manage"}
	if e = m.audit(ctx, tx, audit.DomainEvent{TenantID: tenant, PrincipalID: actor, EventType: "planning.settings_updated", TargetType: "planning_settings", TargetID: row.ID, Detail: detail}); e != nil {
		return PlanningSettings{}, e
	}
	if _, e = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "planning.settings_updated", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "ROLE"}, Subject: events.Subject{Kind: "planning_settings", ID: row.ID}, SubjectVersion: int64(row.Version), Data: detail}); e != nil {
		return PlanningSettings{}, e
	}
	return PlanningSettings{PlanningSettingsValue: v, Version: row.Version}, nil
}

// settingsPeriod is used only while creating a new boundary snapshot. Existing
// reports must continue to use their stored boundaries after settings change.
func settingsPeriod(kind, date, timezone string) (time.Time, time.Time, error) {
	start, end, e := period(kind, date)
	if e != nil {
		return start, end, e
	}
	loc, e := time.LoadLocation(timezone)
	if e != nil {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	local := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc) }
	return local(start), local(end), nil
}

func (m *Module) periodForOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind, date string) (time.Time, time.Time, error) {
	v, err := m.readSettingsOn(ctx, tx, tenant)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return settingsPeriod(kind, date, v.Timezone)
}
