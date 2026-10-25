package fleet

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/infra/guid"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnrollmentConsoleStatusAuthorityTerminalExpiryAndNoSecrets(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	status, err := m.EnrollmentStatus(ctx, 7, started.ID)
	require.NoError(t, err)
	require.Equal(t, "PENDING", status.State)
	payload, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(payload), started.Device.DeviceCode)
	require.NotContains(t, string(payload), started.Device.UserCode)
	require.NotContains(t, string(payload), "device_hash")
	_, err = m.EnrollmentStatus(ctx, 8, started.ID)
	require.ErrorIs(t, err, ErrMachineForbidden)
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error)
	status, err = m.EnrollmentStatus(ctx, 7, started.ID)
	require.NoError(t, err)
	require.Equal(t, "EXPIRED", status.State)
	for _, state := range []string{"DENIED", "CANCELLED", "COMPLETE", "FAILED"} { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Update("state", state).Error)
		status, err = m.EnrollmentStatus(ctx, 7, started.ID)
		require.NoError(t, err)
		require.Equal(t, state, status.State)
	}
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	_, err = m.EnrollmentStatus(ctx, 7, started.ID)
	require.ErrorIs(t, err, ErrMachineForbidden)
}

func TestEnrollmentCodeLookupReportsFrozenFactsAndHidesNonpendingRequests(t *testing.T) {
	m, db, ctx, fake, _ := enrollmentHarness(t)
	facts := &EnrollmentFacts{Hostname: "build-host", Arch: "amd64", Runtimes: []EnrollmentRuntime{{Kind: "CODEX", Available: true, Version: "1.2"}, {Kind: "CLAUDE_CODE", Error: "NOT_FOUND"}}}
	started, err := m.StartEnrollment(ctx, EnrollmentStart{TenantID: 7, ClientID: 20, Name: "Original", OS: "WINDOWS", Facts: facts})
	require.NoError(t, err)
	facts.Hostname = "changed caller object"
	result, err := m.LookupEnrollment(ctx, 7, " abcd-efgh ")
	require.NoError(t, err)
	require.Equal(t, started.ID, result.ID)
	require.Equal(t, "build-host", result.Facts.Hostname)
	require.Len(t, result.Facts.Runtimes, 2)
	require.False(t, result.Facts.Runtimes[1].Available)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	for _, secret := range []string{started.Device.DeviceCode, started.Device.UserCode, "device_hash", "user_hash", "client_secret"} {
		require.NotContains(t, string(raw), secret)
	}
	for _, code := range []string{"wrong-code", "", "ABCD-EFG!"} {
		_, err = m.LookupEnrollment(ctx, 7, code)
		require.ErrorIs(t, err, ErrMachineNotFound)
	}
	_, err = m.LookupEnrollment(ctx, 8, "ABCD-EFGH")
	require.ErrorIs(t, err, ErrMachineForbidden)
	table := m.names.Enrollments.String()
	require.NoError(t, db.Table(table).Where("id=?", started.ID).Update("tenant_id", 8).Error)
	_, err = m.LookupEnrollment(ctx, 7, "ABCD-EFGH")
	require.ErrorIs(t, err, ErrMachineNotFound)
	require.NoError(t, db.Table(table).Where("id=?", started.ID).Update("tenant_id", 7).Error)
	require.NoError(t, m.CancelEnrollment(ctx, 7, started.ID, started.Device.DeviceCode))
	canceled, err := m.EnrollmentStatus(ctx, 7, started.ID)
	require.NoError(t, err)
	_, err = m.LookupEnrollment(ctx, 7, "ABCD-EFGH")
	require.ErrorIs(t, err, ErrMachineNotFound)
	for _, state := range []string{"APPROVED", "COMPLETE", "DENIED", canceled.State, "FAILED", "EXPIRED", "PROVISIONING", "ISSUING"} {
		require.NoError(t, db.Table(table).Where("id=?", started.ID).Update("state", state).Error)
		_, err = m.LookupEnrollment(ctx, 7, "ABCD-EFGH")
		require.ErrorIs(t, err, ErrMachineNotFound, state)
	}
	require.NoError(t, db.Table(table).Where("id=?", started.ID).Updates(map[string]any{"state": "PENDING", "expires_at": time.Now().Add(-time.Minute)}).Error)
	_, err = m.LookupEnrollment(ctx, 7, "ABCD-EFGH")
	require.ErrorIs(t, err, ErrMachineNotFound)
	require.NoError(t, db.Table(table).Where("id=?", started.ID).Update("expires_at", time.Now().Add(time.Minute)).Error)
	lookup := m.enrollmentIAM.LookupDevice
	m.enrollmentIAM.LookupDevice = func(ctx context.Context, tenant guid.ID, code string) (oauth.DeviceRequest, error) {
		v, e := lookup(ctx, tenant, code)
		v.ClientID = 999
		return v, e
	}
	_, err = m.LookupEnrollment(ctx, 7, "ABCD-EFGH")
	require.ErrorIs(t, err, ErrMachineNotFound)
	m.enrollmentIAM.LookupDevice = lookup
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	_, err = m.LookupEnrollment(ctx, 7, "ABCD-EFGH")
	require.ErrorIs(t, err, ErrMachineForbidden)
	require.Zero(t, fake.created)
	require.Zero(t, fake.issued)
}

func TestEnrollmentConfirmationNameIsFrozenAndCollisionDoesNotDecideIAM(t *testing.T) {
	m, db, ctx, fake, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	table := m.names.Enrollments.String()
	for _, name := range []string{strings.Repeat("x", 101), "bad\nname", "   "} {
		require.ErrorIs(t, m.DecideEnrollment(ctx, 7, started.ID, "ABCD-EFGH", true, name), ErrInvalidArgument)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	key := "build-01"
	machine := model.Machine{ID: 990, TenantID: 7, PrincipalID: 991, Name: "Build-01", NameKey: &key, OS: "LINUX", State: "OFFLINE", SlotCount: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, db.Table(m.names.Machines.String()).Create(&machine).Error)
	require.ErrorIs(t, m.DecideEnrollment(ctx, 7, started.ID, "ABCD-EFGH", true, "BUILD-01"), ErrInvalidArgument)
	require.False(t, fake.approved)
	require.Zero(t, fake.created)
	var before model.Enrollment
	require.NoError(t, db.Table(table).Where("id=?", started.ID).Take(&before).Error)
	require.Equal(t, "PENDING", before.State)
	require.Equal(t, "New runner", before.Name)
	require.NoError(t, m.DecideEnrollment(ctx, 7, started.ID, "ABCD-EFGH", true, "build-02"))
	require.NoError(t, m.DecideEnrollment(ctx, 7, started.ID, "ABCD-EFGH", true, "build-02"))
	require.ErrorIs(t, m.DecideEnrollment(ctx, 7, started.ID, "ABCD-EFGH", true, "changed-after-approval"), ErrVersionConflict)
	var stored model.Machine
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id=?", started.ID).Take(&stored).Error)
	require.Equal(t, "build-02", stored.Name)
	require.Equal(t, "build-02", *stored.NameKey)
	require.Equal(t, 1, fake.created)
	status, err := m.EnrollmentStatus(ctx, 7, started.ID)
	require.NoError(t, err)
	require.Equal(t, "OFFLINE", status.MachineState)
}

func TestEnrollmentFactsRejectMalformedObservationsBeforeCreatingDeviceRequest(t *testing.T) {
	for _, facts := range []*EnrollmentFacts{
		{Hostname: "host", Arch: "amd64", Runtimes: nil},
		{Hostname: "", Arch: "amd64"}, {Hostname: "host", Arch: ""},
		{Hostname: "host", Arch: "amd64", Runtimes: []EnrollmentRuntime{{Kind: "CODEX", Available: true}}},
		{Hostname: "host", Arch: "amd64", Runtimes: []EnrollmentRuntime{{Kind: "OTHER", Error: "NOT_FOUND"}}},
		{Hostname: "host", Arch: "amd64", Runtimes: []EnrollmentRuntime{{Kind: "CODEX", Error: "NOT_FOUND"}, {Kind: "CODEX", Error: "NOT_FOUND"}}},
		{Hostname: "host", Arch: "amd64", Runtimes: []EnrollmentRuntime{{Kind: "CODEX", Version: "1", Error: "NOT_FOUND"}}},
		{Hostname: "host", Arch: "amd64", Runtimes: []EnrollmentRuntime{{Kind: "CODEX", Available: true, Version: "1", Error: "TIMEOUT"}}},
	} {
		require.False(t, validateEnrollmentFacts(facts))
	}
	m, db, ctx, _, _ := enrollmentHarness(t)
	started, err := m.StartEnrollment(ctx, EnrollmentStart{TenantID: 7, ClientID: 20, Name: "Machine", OS: "LINUX", Facts: &EnrollmentFacts{Hostname: "", Arch: "amd64"}})
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.Zero(t, started.ID)
	var count int64
	require.NoError(t, db.Table(m.names.Enrollments.String()).Count(&count).Error)
	require.Zero(t, count)
}

func TestEnrollmentConsoleInstallationUsesOnlyConfiguredRelease(t *testing.T) {
	m, _, ctx, _, _ := enrollmentHarness(t)
	settings, err := m.EnrollmentSettings(ctx, 7, "LINUX")
	require.NoError(t, err)
	require.False(t, settings.Available)
	require.Empty(t, settings.Install)
	m.config.Runner = RunnerInstallConfig{Package: "@example/machine-runner", Version: "1.2.3", Command: "machine-runner", Server: "https://example.test/api", ClientIDs: map[string]string{"7": "20"}}
	for _, os := range []string{"WINDOWS", "MACOS", "LINUX"} {
		settings, err = m.EnrollmentSettings(ctx, 7, os)
		require.NoError(t, err)
		require.True(t, settings.Available)
		require.Equal(t, "npm install -g '@example/machine-runner@1.2.3'", settings.Install)
		require.Contains(t, settings.Login, "--tenant 7 --client 20")
		require.Contains(t, settings.Login, "--server 'https://example.test/api'")
		require.Contains(t, settings.Login, "--profile")
		require.Contains(t, settings.Login, "--name")
		require.Contains(t, settings.Start, " daemon --profile ")
	}
	m.config.Runner.Package = "example;bad"
	settings, err = m.EnrollmentSettings(ctx, 7, "LINUX")
	require.NoError(t, err)
	require.False(t, settings.Available)
	_, err = m.EnrollmentSettings(ctx, 7, "UNKNOWN")
	require.ErrorIs(t, err, ErrInvalidArgument)
}
