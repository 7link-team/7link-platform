package planning

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func settingPtr[T any](v T) *T { return &v }

func TestPlanningSettingsPatchCASZeroValuesAndAtomicAudit(t *testing.T) {
	h := setup(t)
	v, e := h.m.readSettingsOn(t.Context(), h.db, 1)
	require.NoError(t, e)
	require.Equal(t, "Asia/Shanghai", v.Timezone)
	require.Equal(t, 1, v.Version)
	require.Equal(t, []int{1, 2, 3, 4, 5}, v.Workdays)
	patch := PlanningSettingsPatch{Timezone: settingPtr("America/Los_Angeles"), AgentDayReport: settingPtr(false), GraceDayMinutes: settingPtr(0), RequiredMemberKinds: settingPtr([]string{"WEEK"})}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.patchSettingsOn(claims(t, 3), tx, 1, 2, patch); return e }), ErrConflict, "initial defaults also require an exact version")
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, patch)
		return e
	}))
	require.Equal(t, 2, v.Version)
	require.False(t, v.AgentDayReport)
	require.Zero(t, v.GraceDayMinutes)
	require.Equal(t, "18:00", v.DayReportAt)
	_, e = h.m.getPlanningSettingsHTTP(claims(t, 3), &PlanningTenantInput{TenantID: "1"})
	require.NoError(t, e)
	for _, actor := range []guid.ID{6, 7, 8, 9} {
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.patchSettingsOn(claims(t, actor), tx, 1, 2, patch); return e }), ErrForbidden)
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, patch); return e }), ErrConflict)
	patch.GraceDayMinutes = settingPtr(10)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = h.m.patchSettingsOn(claims(t, 3), tx, 1, 2, patch)
		return e
	}))
	require.Equal(t, 3, v.Version)
	oldAudit := h.m.audit
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit failed") }
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.patchSettingsOn(claims(t, 3), tx, 1, 3, PlanningSettingsPatch{Timezone: settingPtr("UTC")})
		return e
	}))
	h.m.audit = oldAudit
	oldEvents := h.m.events
	h.m.events = failingPlanningPublisher{}
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.patchSettingsOn(claims(t, 3), tx, 1, 3, PlanningSettingsPatch{Timezone: settingPtr("UTC")})
		return e
	}))
	h.m.events = oldEvents
	v, e = h.m.readSettingsOn(t.Context(), h.db, 1)
	require.NoError(t, e)
	require.Equal(t, 3, v.Version)
	require.Equal(t, "America/Los_Angeles", v.Timezone)
	v, e = h.m.readSettingsOn(t.Context(), h.db, 2)
	require.NoError(t, e)
	require.Equal(t, "Asia/Shanghai", v.Timezone)
}

func TestPlanningSettingsCorruptionAndStorageFailureCannotBecomeDefaults(t *testing.T) {
	for _, change := range []map[string]any{{"workdays": "not-json"}, {"required_member_kinds": "not-json"}, {"workdays": "null"}, {"timezone": "Missing/Zone"}} {
		h := setup(t)
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, PlanningSettingsPatch{})
			return e
		}))
		require.NoError(t, h.db.Table(h.m.names.Settings.String()).Where("tenant_id=?", 1).Updates(change).Error)
		_, e := h.m.readSettingsOn(t.Context(), h.db, 1)
		require.ErrorIs(t, e, ErrUnavailable)
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.patchSettingsOn(claims(t, 3), tx, 1, 2, PlanningSettingsPatch{})
			return e
		}), ErrUnavailable)
	}
	h := setup(t)
	require.NoError(t, h.db.Exec("DROP TABLE "+h.m.names.Settings.String()).Error)
	_, e := h.m.readSettingsOn(t.Context(), h.db, 1)
	require.Error(t, e)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, PlanningSettingsPatch{})
		return e
	}))
}

func TestPlanningSettingsValidationAndCalendarBoundaries(t *testing.T) {
	for _, patch := range []PlanningSettingsPatch{
		{Timezone: settingPtr("Local")}, {Timezone: settingPtr("Missing/Zone")}, {DayReportAt: settingPtr("24:00")}, {WeekReportAt: settingPtr("MON 9:00")}, {MonthReportAt: settingPtr("D0 09:00")}, {GraceYearMinutes: settingPtr(-1)}, {Workdays: settingPtr([]int{1, 1})}, {Workdays: settingPtr([]int{0, 8})}, {RequiredMemberKinds: settingPtr([]string{"WEEK", "WEEK"})}, {RequiredMemberKinds: settingPtr([]string{"UNKNOWN"})}, {AtRiskThreshold: settingPtr(1.1)},
	} {
		h := setup(t)
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, patch); return e }), ErrInvalid)
	}
	start, end, e := settingsPeriod("DAY", "2026-03-08", "America/Los_Angeles")
	require.NoError(t, e)
	require.Equal(t, 23*time.Hour, end.Sub(start))
	require.Equal(t, "2026-03-08T08:00:00Z", start.UTC().Format(time.RFC3339))
	start, end, e = settingsPeriod("DAY", "2026-11-01", "America/Los_Angeles")
	require.NoError(t, e)
	require.Equal(t, 25*time.Hour, end.Sub(start))
	_, _, e = settingsPeriod("WEEK", "2026-10-20", "UTC")
	require.ErrorIs(t, e, ErrInvalid)
}

func TestPlanningSettingsNeedsCurrentManageGrantBeyondRead(t *testing.T) {
	h := setup(t)
	h.m.policy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "goal_read"}, nil
	}
	_, err := h.m.getPlanningSettingsHTTP(claims(t, 3), &PlanningTenantInput{TenantID: "1"})
	require.NoError(t, err)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, PlanningSettingsPatch{Timezone: settingPtr("UTC")})
		return err
	}), ErrForbidden)
}
