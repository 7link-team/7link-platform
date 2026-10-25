package clientwork

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAcceptanceCalendarAndMetricsFailClosedOnUnavailableOrInvalidCalendar(t *testing.T) {
	m, db, _, _ := harness(t)
	s := module.NewServices()
	require.NoError(t, m.provideAcceptanceCalendar(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[AcceptanceCalendarConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, c.Bind(nil), ErrUnavailable)
	for _, fault := range []string{"missing", "source", "zone", "local", "version", "date", "first_count", "accepted_count"} {
		t.Run(fault, func(t *testing.T) {
			zone, version := "UTC", 1
			if fault == "zone" {
				zone = "not/a/timezone"
			}
			if fault == "local" {
				zone = "Local"
			}
			if fault == "version" {
				version = 0
			}
			require.NoError(t, c.Bind(func(context.Context, *gorm.DB, guid.ID) (string, int, error) {
				if fault == "source" {
					return "", 0, ErrUnavailable
				}
				return zone, version, nil
			}))
			if fault == "missing" {
				m.acceptanceCalendar = nil
			}
			date := "2026-10-24"
			if fault == "date" {
				date = "2026-99-99"
			}
			if fault == "first_count" || fault == "accepted_count" {
				calls := 0
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("metric_count_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.PackageReceipt.String() {
						calls++
						if fault == "first_count" || calls == 2 {
							tx.AddError(ErrUnavailable)
						}
					}
				}))
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				out, e := m.acceptanceMetricsOn(t.Context(), tx, 1, 3, date, 1)
				require.Empty(t, out.Days)
				return e
			})
			require.Error(t, err)
			require.NoError(t, db.Callback().Query().Remove("metric_count_failure"))
		})
	}
	for _, in := range []*acceptanceMetricsInput{{EntityID: "bad", Days: 1}, {EntityID: "3", Days: 0}, {EntityID: "3", Days: 94}} {
		out, err := m.acceptanceMetricsHTTP(testContext(t, 2), in)
		require.Error(t, err)
		require.Nil(t, out)
	}
	_, err = m.acceptanceMetricsOn(t.Context(), db, 1, 3, "", 1)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestAcceptanceMetricsUsesTenantDSTCalendarActualReceiptsAndNullDenominator(t *testing.T) {
	m, db, _, _ := harness(t)
	m.acceptanceCalendar = func(context.Context, *gorm.DB, guid.ID) (string, int, error) { return "America/Los_Angeles", 7, nil }
	for i, v := range []struct {
		at       string
		decision string
		project  guid.ID
	}{{"2026-11-01T06:59:59Z", "ACCEPT", 3}, {"2026-11-01T07:00:00Z", "ACCEPT", 3}, {"2026-11-02T07:59:59Z", "PARTIALLY_ACCEPTED", 3}, {"2026-11-02T08:00:00Z", "REJECT", 3}, {"2026-11-01T08:00:00Z", "ACCEPT", 99}} {
		at, e := time.Parse(time.RFC3339, v.at)
		require.NoError(t, e)
		r := model.PackageReceipt{TenantID: 1, ID: guid.ID(100 + i), ProjectID: v.project, PackageID: guid.ID(10 + i), ContentHash: "hash", Decision: v.decision, Reason: "Actual client", ActorID: 4, Source: "PORTAL", CreatedAt: at}
		require.NoError(t, query.BindPackageReceipt(db, m.names).WithContext(t.Context()).Create(&r))
	}
	later := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	duplicate := model.PackageReceipt{TenantID: 1, ID: 200, ProjectID: 3, PackageID: 11, ContentHash: "later hash", Decision: "ACCEPT", Reason: "Later receipt", ActorID: 4, Source: "PORTAL", CreatedAt: later}
	require.ErrorIs(t, query.BindPackageReceipt(db, m.names).WithContext(t.Context()).Create(&duplicate), gorm.ErrDuplicatedKey)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.acceptanceMetricsOn(t.Context(), tx, 1, 3, "2026-11-03", 4)
		require.NoError(t, e)
		require.Equal(t, 7, out.CalendarVersion)
		require.EqualValues(t, 1, out.Days[0].Receipts)
		require.EqualValues(t, 2, out.Days[1].Receipts)
		require.Equal(t, .5, *out.Days[1].AcceptanceRate) //nolint:testifylint // Exact numeric equality is frozen by this regression contract.
		require.EqualValues(t, 1, out.Days[2].Receipts)
		require.Zero(t, *out.Days[2].AcceptanceRate)
		require.Nil(t, out.Days[3].AcceptanceRate)
		return nil
	}))
	_, e := m.acceptanceMetricsHTTP(testContext(t, 4), &acceptanceMetricsInput{EntityID: "3", Date: "2026-11-01", Days: 1})
	require.Error(t, e)
}

func TestAcceptanceMetricsHTTPReturnsActualCalendarDayAndFailsWithoutWriter(t *testing.T) {
	m, _, _, _ := harness(t)
	m.acceptanceCalendar = func(context.Context, *gorm.DB, guid.ID) (string, int, error) { return "UTC", 3, nil }
	out, e := m.acceptanceMetricsHTTP(testContext(t, 2), &acceptanceMetricsInput{EntityID: "3", Days: 1})
	require.NoError(t, e)
	require.Equal(t, 3, out.Body.Data.CalendarVersion)
	require.Len(t, out.Body.Data.Days, 1)
	require.Equal(t, now().UTC().Format("2006-01-02"), out.Body.Data.Days[0].Date)
	require.Zero(t, out.Body.Data.Days[0].Receipts)
	require.Nil(t, out.Body.Data.Days[0].AcceptanceRate)
	m.runtime = nil
	out, e = m.acceptanceMetricsHTTP(testContext(t, 2), &acceptanceMetricsInput{EntityID: "3", Days: 1})
	require.Error(t, e)
	require.Nil(t, out)
}
