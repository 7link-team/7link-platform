package fleet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnrollmentCancelProofReplayAndTerminalFence(t *testing.T) {
	m, db, ctx, fake, _ := enrollmentHarness(t)
	start := startTestEnrollment(t, m, ctx)
	require.ErrorIs(t, m.CancelEnrollment(ctx, 8, start.ID, start.Device.DeviceCode), ErrMachineNotFound)
	require.ErrorIs(t, m.CancelEnrollment(ctx, 7, start.ID, "wrong"), ErrMachineNotFound)
	effectCount := func() int64 {
		var count int64
		require.NoError(t, db.Table("test_fleet_effects").Count(&count).Error)
		return count
	}
	before := effectCount()
	require.NoError(t, m.CancelEnrollment(ctx, 7, start.ID, start.Device.DeviceCode))
	require.Equal(t, before+2, effectCount())
	require.NoError(t, m.CancelEnrollment(ctx, 7, start.ID, start.Device.DeviceCode))
	require.Equal(t, before+2, effectCount(), "replay must not publish again")
	require.ErrorIs(t, m.CancelEnrollment(ctx, 7, start.ID, "wrong"), ErrMachineNotFound, "replays recheck proof")
	require.ErrorIs(t, m.ConfirmEnrollment(ctx, 7, start.ID, start.Device.UserCode, true), ErrEnrollmentFailed)
	_, err := m.ExchangeEnrollment(ctx, 7, start.ID, start.Device.DeviceCode)
	require.ErrorIs(t, err, ErrEnrollmentFailed)
	require.NoError(t, m.ExpireEnrollment(ctx, 7, start.ID, time.Now().Add(time.Hour)))
	q := query.BindEnrollments(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(start.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "CANCELLED", row.State) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	require.Zero(t, fake.created)
	require.Zero(t, fake.issued)
}

func TestEnrollmentCancelAtomicFailuresAndDecidedFence(t *testing.T) {
	for _, mode := range []string{"audit", "outbox", "approved", "expired"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, fake, publisher := enrollmentHarness(t)
			start := startTestEnrollment(t, m, ctx)
			state := "PENDING"
			switch mode {
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			case "outbox":
				publisher.fail = true
			case "approved":
				require.NoError(t, m.ConfirmEnrollment(ctx, 7, start.ID, start.Device.UserCode, true))
				state = "APPROVED"
			case "expired":
				q := query.BindEnrollments(db, m.names)
				_, err := q.WithContext(ctx).Where(q.ID.Eq(start.ID)).Update(q.ExpiresAt, time.Now().Add(-time.Minute))
				require.NoError(t, err)
			}
			var before, after int64
			require.NoError(t, db.Table("test_fleet_effects").Count(&before).Error)
			require.Error(t, m.CancelEnrollment(ctx, 7, start.ID, start.Device.DeviceCode))
			require.NoError(t, db.Table("test_fleet_effects").Count(&after).Error)
			require.Equal(t, before, after, "failed cancellation must leave no audit/outbox effects")
			q := query.BindEnrollments(db, m.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(start.ID)).First()
			require.NoError(t, err)
			require.Equal(t, state, row.State)
			require.Zero(t, fake.issued)
		})
	}
}

func TestEnrollmentCancelHTTPPrivateProofAndNoStore(t *testing.T) {
	m, _, ctx, _, _ := enrollmentHarness(t)
	start := startTestEnrollment(t, m, ctx)
	router := fleetBusinessRouter(t, m)
	path := "/machine-enrollments/" + start.ID.String() + "/cancellation"
	wrong := fleetHTTPRequest(router, http.MethodPost, path, `{"tenant_id":"7","device_code":"wrong"}`, nil)
	require.Equal(t, 404, wrong.Code)
	body := fmt.Sprintf(`{"tenant_id":"7","device_code":%q}`, start.Device.DeviceCode)
	for range 2 {
		response := fleetHTTPRequest(router, http.MethodPost, path, body, nil)
		require.Equal(t, 200, response.Code, response.Body.String())
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		require.NotContains(t, response.Body.String(), start.Device.DeviceCode)
	}
}
