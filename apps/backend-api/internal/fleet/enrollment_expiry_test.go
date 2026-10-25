package fleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnrollmentExpiryTerminatesAbandonedCoordinationAndCleansOrphans(t *testing.T) {
	for _, phase := range []string{"PENDING", "APPROVING", "PROVISIONING", "COMPENSATING"} {
		t.Run(phase, func(t *testing.T) {
			m, db, ctx, fake, _ := enrollmentHarness(t)
			started := startTestEnrollment(t, m, ctx)
			deadline := time.Now().UTC().Add(-time.Minute)
			if phase == "COMPENSATING" {
				deadline = time.Now().UTC().Add(time.Minute)
			}
			require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Updates(map[string]any{"state": phase, "expires_at": deadline}).Error)
			if phase == "PROVISIONING" || phase == "COMPENSATING" {
				fake.account = identity.ServiceAccount{ID: 202, LoginName: "machine-" + started.ID.String(), Version: 1}
			}
			require.NoError(t, m.ExpireEnrollment(t.Context(), 7, started.ID, time.Now().UTC()))
			var row model.Enrollment
			require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
			require.Equal(t, "FAILED", row.State)
			if phase == "PROVISIONING" || phase == "COMPENSATING" {
				require.Equal(t, 1, fake.deleted)
			} else {
				require.Zero(t, fake.deleted)
			}
			require.NoError(t, m.ExpireEnrollment(t.Context(), 7, started.ID, time.Now().UTC().Add(time.Hour)))
			require.Zero(t, fake.issued)
		})
	}
}

func completeEnrollmentForExpiry(t *testing.T, m *Module, ctx context.Context) EnrollmentStarted {
	t.Helper()
	started := startTestEnrollment(t, m, ctx)
	require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
	_, err := m.ExchangeEnrollment(t.Context(), 7, started.ID, started.Device.DeviceCode)
	require.NoError(t, err)
	return started
}

func TestEnrollmentExpiryRevokesUnusedMachineAndRetriesIAMCleanup(t *testing.T) {
	m, db, ctx, fake, _ := enrollmentHarness(t)
	started := completeEnrollmentForExpiry(t, m, ctx)
	failure := errors.New("test IAM cleanup unavailable")
	m.enrollmentIAM.DeleteAccount = func(context.Context, guid.ID, guid.ID, int64) error { return failure }
	now := time.Now().UTC().Add(61 * time.Second)
	require.ErrorIs(t, m.ExpireEnrollment(t.Context(), 7, started.ID, now), failure)
	var machine model.Machine
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", started.ID).Take(&machine).Error)
	require.Equal(t, "REVOKED", machine.State)
	require.Nil(t, machine.NameKey)
	var enrollment model.Enrollment
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&enrollment).Error)
	require.Equal(t, "COMPENSATING", enrollment.State)
	m.accounts = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID, _ time.Time) (bool, error) {
		return tenant == 7 && principal == 202, nil
	}
	runner := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 202, SubjectType: authn.SubjectTypePrincipal})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.OpenSessionOn(runner, tx, 7, started.ID, "late-session")
		return err
	}), ErrMachineRevoked)
	m.enrollmentIAM.DeleteAccount = fake.port().DeleteAccount
	require.NoError(t, m.ExpireEnrollment(t.Context(), 7, started.ID, now.Add(time.Second)))
	require.Equal(t, 1, fake.deleted)
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&enrollment).Error)
	require.Equal(t, "FAILED", enrollment.State)
}

func TestEnrollmentExpiryAcknowledgedDeliveryPreservesMachine(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "session_ack", true: "legacy_last_seen"}[fallback], func(t *testing.T) {
			m, db, ctx, fake, _ := enrollmentHarness(t)
			started := completeEnrollmentForExpiry(t, m, ctx)
			m.accounts = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID, _ time.Time) (bool, error) {
				return tenant == 7 && principal == 202, nil
			}
			runner := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 202, SubjectType: authn.SubjectTypePrincipal})
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.OpenSessionOn(runner, tx, 7, started.ID, "first-session")
				return err
			}))
			var row model.Enrollment
			require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
			require.NotNil(t, row.AcknowledgedAt, "first authenticated session acknowledges delivery in its transaction")
			if fallback {
				require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Update("acknowledged_at", nil).Error)
			}
			require.NoError(t, m.ExpireEnrollment(t.Context(), 7, started.ID, time.Now().UTC().Add(time.Hour)))
			require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
			require.Equal(t, "COMPLETE", row.State)
			require.NotNil(t, row.AcknowledgedAt)
			require.Zero(t, fake.deleted)
		})
	}
}

func TestEnrollmentExpiryOutboxFailureRollsBackDenialAndDoesNotDeleteIAM(t *testing.T) {
	m, db, ctx, fake, publisher := enrollmentHarness(t)
	started := completeEnrollmentForExpiry(t, m, ctx)
	now := time.Now().UTC().Add(61 * time.Second)
	publisher.fail = true
	require.Error(t, m.ExpireEnrollment(t.Context(), 7, started.ID, now))
	var machine model.Machine
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", started.ID).Take(&machine).Error)
	require.Equal(t, "OFFLINE", machine.State)
	var row model.Enrollment
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
	require.Equal(t, "COMPLETE", row.State)
	require.Zero(t, fake.deleted)
	publisher.fail = false
	require.NoError(t, m.ExpireEnrollment(t.Context(), 7, started.ID, now))
	require.Equal(t, 1, fake.deleted)
}

func TestWatchdogEnrollmentExpiryTraversesBoundedPagesAndTenantFilters(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	now := time.Now().UTC()
	rows := make([]*model.Enrollment, 0, 103)
	for id := guid.ID(100); id < 203; id++ {
		tenant := guid.ID(7)
		expiry := now.Add(-time.Minute)
		if id == 201 {
			tenant = 8
		}
		if id == 202 {
			expiry = now.Add(time.Hour)
		}
		rows = append(rows, &model.Enrollment{ID: id, TenantID: tenant, ClientID: 20, Name: "pending", OS: "LINUX", State: "PENDING", DeviceHash: enrollmentHash("device-" + id.String()), UserHash: userCodeHash("user-" + id.String()), ExpiresAt: expiry, CreatedAt: now, UpdatedAt: now})
	}
	require.NoError(t, query.BindEnrollments(db, m.names).WithContext(ctx).Create(rows...))
	require.NoError(t, m.JobHandlers()[0].Run(t.Context(), jobs.Job{TenantID: 7}))
	var count int64
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("tenant_id = ? AND state = ?", 7, "FAILED").Count(&count).Error)
	require.Equal(t, int64(101), count)
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("state = ?", "PENDING").Count(&count).Error)
	require.Equal(t, int64(2), count, "future and foreign-tenant records stay pending")
}
