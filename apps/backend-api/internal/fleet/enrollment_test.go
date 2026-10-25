package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type enrollmentIAMFake struct {
	account                                     identity.ServiceAccount
	created, deleted, issued, revoked, consumed int
	approved                                    bool
	afterCreate                                 func()
	beforeIssue                                 func()
}

func (f *enrollmentIAMFake) port() EnrollmentIAM {
	return EnrollmentIAM{
		StartDevice: func(context.Context, string, string) (oauth.DeviceAuthorization, error) {
			return oauth.DeviceAuthorization{DeviceCode: "test-device-proof", UserCode: "ABCD-EFGH", ExpiresIn: 300, Interval: 5}, nil
		},
		LookupDevice: func(context.Context, guid.ID, string) (oauth.DeviceRequest, error) {
			return oauth.DeviceRequest{ClientID: 20, Status: "pending", ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}, nil
		},
		DecideDevice: func(_ context.Context, _ guid.ID, _ string, _ guid.ID, approve bool, _ time.Time, _ int, _ string) error {
			f.approved = approve
			return nil
		},
		FindAccount: func(context.Context, guid.ID, string) (identity.ServiceAccount, bool, error) {
			return f.account, f.account.ID > 0, nil
		},
		CreateAccount: func(_ context.Context, _ guid.ID, login, _, _ string, _ *time.Time) (identity.ServiceAccount, error) {
			f.created++
			f.account = identity.ServiceAccount{ID: 202, LoginName: login, Version: 1, Status: "ACTIVE"}
			if f.afterCreate != nil {
				f.afterCreate()
			}
			return f.account, nil
		},
		GetAccount: func(context.Context, guid.ID, guid.ID) (identity.ServiceAccount, error) { return f.account, nil },
		DeleteAccount: func(context.Context, guid.ID, guid.ID, int64) error {
			f.deleted++
			f.account = identity.ServiceAccount{}
			return nil
		},
		Token: func(_ context.Context, form url.Values, _ string) (oauth.TokenResponse, error) {
			if form.Get("device_code") != "test-device-proof" || !f.approved {
				return oauth.TokenResponse{}, oauth.ErrAuthorizationPending
			}
			f.consumed++
			return oauth.TokenResponse{}, nil
		},
		IssueCredential: func(_ context.Context, _, _ guid.ID, _ string, scopes []string, _ *time.Time) (identity.ServiceAccountCredentialSecret, error) {
			if len(scopes) != 1 || scopes[0] != "openid" {
				return identity.ServiceAccountCredentialSecret{}, errors.New("machine credential requires minimal openid scope")
			}
			f.issued++
			if f.beforeIssue != nil {
				f.beforeIssue()
			}
			return identity.ServiceAccountCredentialSecret{Credential: identity.ServiceAccountCredential{ID: 303, ServiceAccountID: 202}, Secret: "test-one-time-secret"}, nil
		},
		RevokeCredential: func(context.Context, guid.ID, guid.ID, guid.ID) error { f.revoked++; return nil },
		ListCredentials:  func(context.Context, guid.ID, guid.ID) ([]identity.ServiceAccountCredential, error) { return nil, nil },
	}
}

func enrollmentHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *enrollmentIAMFake, *fleetPublisher) {
	t.Helper()
	m, db, ctx, publisher := machineHarness(t)
	var database struct{ File string }
	require.NoError(t, db.Raw("PRAGMA database_list").Scan(&database).Error)
	router, err := gormx.NewDatasourceRouter(ctx, false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: database.File, Readable: true, Writable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	m.runtime = appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("dir_"), appenv.WithDatabase(router))
	dlocks := dlock.NewModule(m.runtime)
	raw, err := db.DB()
	require.NoError(t, err)
	source := dlocks.MigrationSource()
	source.Prefix = "dir_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	services := module.NewServices()
	require.NoError(t, dlocks.ProvideServices(services))
	require.NoError(t, services.Seal())
	m.enrollmentLease, err = module.Resolve[dlock.RenewingLease](services)
	require.NoError(t, err)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, true, nil
	}
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	fake := &enrollmentIAMFake{}
	m.enrollmentIAM = fake.port()
	return m, db, ctx, fake, publisher
}

func startTestEnrollment(t *testing.T, m *Module, ctx context.Context) EnrollmentStarted {
	t.Helper()
	started, err := m.StartEnrollment(ctx, EnrollmentStart{TenantID: 7, ClientID: 20, Name: "New runner", OS: "WINDOWS"})
	require.NoError(t, err)
	return started
}

func TestEnrollmentFullFlowProofsAndSecretOnce(t *testing.T) {
	m, db, ctx, fake, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	_, err := m.ExchangeEnrollment(ctx, 7, started.ID, "wrong-proof")
	require.ErrorIs(t, err, ErrMachineNotFound)
	_, err = m.ExchangeEnrollment(ctx, 8, started.ID, started.Device.DeviceCode)
	require.ErrorIs(t, err, ErrMachineNotFound)
	_, err = m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.ErrorIs(t, err, ErrEnrollmentPending)
	require.ErrorIs(t, m.ConfirmEnrollment(ctx, 7, started.ID, "wrong-code", true), ErrMachineNotFound)
	require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Update("last_polled_at", time.Now().UTC().Add(-time.Minute)).Error)
	credential, err := m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.NoError(t, err)
	require.Equal(t, started.ID, credential.MachineID)
	require.Equal(t, "test-one-time-secret", credential.Credential.Secret)
	replay, err := m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.ErrorIs(t, err, ErrEnrollmentConsumed)
	require.Empty(t, replay.Credential.Secret)
	require.Equal(t, 1, fake.created)
	require.Equal(t, 1, fake.issued)
	require.Zero(t, fake.consumed, "enrollment must never mint the approving Human token")
	require.NotEqual(t, "test-device-proof", started.Device.DeviceCode, "IAM device proof must never reach the runner")
	row, err := query.BindEnrollments(db, m.names).WithContext(ctx).Where(query.BindEnrollments(db, m.names).ID.Eq(started.ID)).First()
	require.NoError(t, err)
	stored, err := json.Marshal(row)
	require.NoError(t, err)
	for _, secret := range []string{started.Device.DeviceCode, started.Device.UserCode, credential.Credential.Secret} {
		require.NotContains(t, string(stored), secret)
	}
}

func TestEnrollmentConfirmationRollbackAndCurrentAuthorityCompensate(t *testing.T) {
	for _, failure := range []string{"event", "authority"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx, fake, _ := enrollmentHarness(t)
			started := startTestEnrollment(t, m, ctx)
			if failure == "event" {
				appendAudit := m.audit
				m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
					if event.EventType == "fleet.machine.state_changed" {
						return errors.New("test enrolled event failure")
					}
					return appendAudit(ctx, tx, event)
				}
			} else {
				fake.afterCreate = func() {
					m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
						return authz.DataConstraint{}, nil
					}
				}
			}
			require.Error(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
			require.Equal(t, 1, fake.created)
			require.Equal(t, 1, fake.deleted)
			var count int64
			require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", started.ID).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestEnrollmentExchangeCommitFailureReturnsNoSecretAndRevokes(t *testing.T) {
	m, db, ctx, fake, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
	fake.beforeIssue = func() {
		require.NoError(t, db.Exec("CREATE TRIGGER reject_enrollment_complete BEFORE UPDATE ON dir_fleet_machine_enrollments WHEN NEW.state = 'COMPLETE' BEGIN SELECT RAISE(ABORT, 'test complete failure'); END").Error)
	}
	credential, err := m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.Error(t, err)
	require.Empty(t, credential.Credential.Secret)
	require.Equal(t, 1, fake.issued)
	require.Equal(t, 1, fake.revoked)
}

func TestEnrollmentDeniedNeverProvisions(t *testing.T) {
	m, _, ctx, fake, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, false))
	credential, err := m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.ErrorIs(t, err, ErrEnrollmentFailed)
	require.Empty(t, credential.Credential.Secret)
	require.Zero(t, fake.created)
	require.Zero(t, fake.issued)
}

func TestEnrollmentProvisioningRecoveryReusesAccount(t *testing.T) {
	m, db, ctx, fake, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	fake.account = identity.ServiceAccount{ID: 202, LoginName: "machine-" + started.ID.String(), Version: 1, Status: "ACTIVE"}
	fake.approved = true
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Updates(map[string]any{"state": "PROVISIONING", "approved_by": guid.ID(101)}).Error)
	require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
	require.Zero(t, fake.created, "recover an IAM commit whose fleet principal-id checkpoint was lost")
	credential, err := m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.NoError(t, err)
	require.Equal(t, guid.ID(202), credential.Credential.Credential.ServiceAccountID)
}

func TestEnrollmentAmbiguousApprovalAndCompensationRecovery(t *testing.T) {
	for _, phase := range []string{"APPROVING", "COMPENSATING", "PROVISIONING_EXPIRED"} {
		t.Run(phase, func(t *testing.T) {
			m, db, ctx, fake, _ := enrollmentHarness(t)
			started := startTestEnrollment(t, m, ctx)
			state := phase
			values := map[string]any{"state": state, "approved_by": guid.ID(101)}
			if phase == "PROVISIONING_EXPIRED" {
				values["state"] = "PROVISIONING"
				values["expires_at"] = time.Now().UTC().Add(-time.Minute)
			}
			if phase != "APPROVING" {
				fake.account = identity.ServiceAccount{ID: 202, LoginName: "machine-" + started.ID.String(), Version: 1, Status: "ACTIVE"}
			}
			require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Updates(values).Error)
			require.ErrorIs(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true), ErrEnrollmentFailed)
			var row struct{ State string }
			require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
			require.Equal(t, "FAILED", row.State)
			require.Zero(t, fake.created)
			require.Zero(t, fake.issued)
			if phase != "APPROVING" {
				require.Equal(t, 1, fake.deleted)
			}
		})
	}
}

func TestEnrollmentExchangeRevalidatesApproverBeforeIssuance(t *testing.T) {
	m, _, ctx, fake, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	credential, err := m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.ErrorIs(t, err, ErrMachineForbidden)
	require.Empty(t, credential.Credential.Secret)
	require.Zero(t, fake.issued)
}

func TestEnrollmentExchangeRevalidatesBeforeSecretExposure(t *testing.T) {
	m, _, ctx, fake, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
	fake.beforeIssue = func() {
		m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{}, nil
		}
	}
	credential, err := m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.ErrorIs(t, err, ErrMachineForbidden)
	require.Empty(t, credential.Credential.Secret)
	require.Equal(t, 1, fake.issued)
	require.Equal(t, 1, fake.revoked)
}

func TestEnrollmentInterruptedIssuanceRevokesOnlyMatchingSecretsAndRetries(t *testing.T) {
	m, db, ctx, fake, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Update("state", "ISSUING").Error)
	revokedAt := time.Now().UTC()
	credentials := []identity.ServiceAccountCredential{
		{ID: 301, ServiceAccountID: 202, Name: "runner-" + started.ID.String()},
		{ID: 302, ServiceAccountID: 202, Name: "runner-" + started.ID.String()},
		{ID: 303, ServiceAccountID: 202, Name: "runner-" + started.ID.String(), RevokedAt: &revokedAt},
		{ID: 304, ServiceAccountID: 202, Name: "other-purpose"},
	}
	m.enrollmentIAM.ListCredentials = func(_ context.Context, tenant, account guid.ID) ([]identity.ServiceAccountCredential, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(202), account)
		return credentials, nil
	}
	cleanupUnavailable := errors.New("test revoke unavailable")
	failSecond := true
	revoked := []guid.ID{}
	m.enrollmentIAM.RevokeCredential = func(_ context.Context, tenant, account, id guid.ID) error {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(202), account)
		if id == 302 && failSecond {
			return cleanupUnavailable
		}
		for index := range credentials {
			if credentials[index].ID == id {
				credentials[index].RevokedAt = &revokedAt
			}
		}
		revoked = append(revoked, id)
		return nil
	}
	credential, err := m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.ErrorIs(t, err, cleanupUnavailable)
	require.Empty(t, credential.Credential.Secret)
	var row struct{ State string }
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
	require.Equal(t, "ISSUING", row.State, "failed cleanup remains retryable")
	failSecond = false
	credential, err = m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.ErrorIs(t, err, ErrEnrollmentFailed)
	require.Empty(t, credential.Credential.Secret)
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
	require.Equal(t, "FAILED", row.State)
	require.Equal(t, []guid.ID{301, 302}, revoked)
	require.Nil(t, credentials[3].RevokedAt, "unrelated credentials are preserved")
	require.Zero(t, fake.issued)
	require.Zero(t, fake.consumed)
}

func TestEnrollmentEventsAlwaysDescribePersistedStateAndHumanFacts(t *testing.T) {
	m, db, ctx, _, publisher := enrollmentHarness(t)
	recorded := &recordingFleetEvents{delegate: publisher}
	m.events = recorded
	started := startTestEnrollment(t, m, t.Context())
	require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
	_, err := m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, event := range recorded.published {
		data := event.Data.(map[string]any)
		serialized, err := json.Marshal(data)
		require.NoError(t, err)
		for _, secret := range []string{started.Device.DeviceCode, started.Device.UserCode, "test-one-time-secret"} {
			require.NotContains(t, string(serialized), secret)
		}
		if event.Type == "machine.enrollment_changed" {
			state, ok := data["state"].(string)
			require.True(t, ok)
			require.NotEmpty(t, state, "principal-id checkpoint must not emit an empty enrollment state")
			seen[state] = true
			if state == "PENDING" {
				require.Equal(t, "SYSTEM", event.Actor.Kind)
				require.Zero(t, event.Actor.ID)
			}
		}
		if event.Type == "machine.state_changed" && data["reason"] == "ENROLLED" {
			require.Equal(t, "HUMAN", event.Actor.Kind)
			require.Equal(t, guid.ID(101), event.Actor.ID)
			require.NotEmpty(t, event.Actor.AuthoritySource)
		}
	}
	for _, state := range []string{"PENDING", "APPROVING", "PROVISIONING", "APPROVED", "ISSUING", "COMPLETE"} {
		require.True(t, seen[state], "missing event for %s", state)
	}
	var row struct{ State string }
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
	require.Equal(t, "COMPLETE", row.State)
}

func TestEnrollmentApprovedEventFailureRollsBackMachineAndCompensatesAccount(t *testing.T) {
	m, db, ctx, fake, publisher := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	m.events = &recordingFleetEvents{delegate: publisher, rejectState: "APPROVED"}
	require.Error(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
	require.Equal(t, 1, fake.created)
	require.Equal(t, 1, fake.deleted)
	var machines int64
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", started.ID).Count(&machines).Error)
	require.Zero(t, machines)
	var row struct{ State string }
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
	require.Equal(t, "FAILED", row.State)
}

func TestEnrollmentStartFailureNeverProvisionsOrPersistsUnusableGrant(t *testing.T) {
	for _, fault := range []string{"start_device", "lookup_device", "foreign_client", "invalid_expiry", "id_failure", "id_zero", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, fake, publisher := enrollmentHarness(t)
			failure := errors.New("test IAM start dependency unavailable")
			switch fault {
			case "start_device":
				m.enrollmentIAM.StartDevice = func(context.Context, string, string) (oauth.DeviceAuthorization, error) {
					return oauth.DeviceAuthorization{}, failure
				}
			case "lookup_device":
				m.enrollmentIAM.LookupDevice = func(context.Context, guid.ID, string) (oauth.DeviceRequest, error) {
					return oauth.DeviceRequest{}, failure
				}
			case "foreign_client":
				m.enrollmentIAM.LookupDevice = func(context.Context, guid.ID, string) (oauth.DeviceRequest, error) {
					return oauth.DeviceRequest{ClientID: 21}, nil
				}
			case "invalid_expiry":
				m.enrollmentIAM.StartDevice = func(context.Context, string, string) (oauth.DeviceAuthorization, error) {
					return oauth.DeviceAuthorization{DeviceCode: "device", UserCode: "code", ExpiresIn: 0}, nil
				}
			case "id_failure":
				m.ids = failedFleetIDs{err: failure}
			case "id_zero":
				m.ids = failedFleetIDs{}
			case "outbox":
				publisher.fail = true
			}
			started, err := m.StartEnrollment(ctx, EnrollmentStart{TenantID: 7, ClientID: 20, Name: "Runner", OS: "LINUX"})
			require.Error(t, err)
			require.Zero(t, started.ID)
			require.Empty(t, started.Device.DeviceCode)
			var count int64
			require.NoError(t, db.Table(m.names.Enrollments.String()).Count(&count).Error)
			require.Zero(t, count)
			require.Zero(t, fake.created)
			require.Zero(t, fake.issued)
		})
	}
}

func TestEnrollmentProvisioningDependencyFailureResumesWithoutDuplicateAccount(t *testing.T) {
	for _, fault := range []string{"lookup", "decision", "find", "create"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, fake, _ := enrollmentHarness(t)
			started := startTestEnrollment(t, m, ctx)
			failure := errors.New("test provisioning dependency unavailable")
			port := fake.port()
			switch fault {
			case "lookup":
				m.enrollmentIAM.LookupDevice = func(context.Context, guid.ID, string) (oauth.DeviceRequest, error) {
					return oauth.DeviceRequest{}, failure
				}
			case "decision":
				m.enrollmentIAM.DecideDevice = func(context.Context, guid.ID, string, guid.ID, bool, time.Time, int, string) error { return failure }
			case "find":
				m.enrollmentIAM.FindAccount = func(context.Context, guid.ID, string) (identity.ServiceAccount, bool, error) {
					return identity.ServiceAccount{}, false, failure
				}
			case "create":
				m.enrollmentIAM.CreateAccount = func(context.Context, guid.ID, string, string, string, *time.Time) (identity.ServiceAccount, error) {
					return identity.ServiceAccount{}, failure
				}
			}
			require.ErrorIs(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true), failure)
			var row struct{ State string }
			require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
			m.enrollmentIAM = port
			if fault == "decision" {
				require.Equal(t, "FAILED", row.State)
				require.ErrorIs(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true), ErrEnrollmentFailed)
				require.Zero(t, fake.created)
			} else {
				require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
				require.Equal(t, 1, fake.created)
			}
		})
	}
}

func TestEnrollmentCompensationFailureRemainsRetryable(t *testing.T) {
	m, db, ctx, fake, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	failure := errors.New("test IAM deletion unavailable")
	m.enrollmentIAM.DeleteAccount = func(context.Context, guid.ID, guid.ID, int64) error { return failure }
	appendAudit := m.audit
	m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		if event.EventType == "fleet.machine.state_changed" {
			return errors.New("test machine event failure")
		}
		return appendAudit(ctx, tx, event)
	}
	require.ErrorIs(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true), failure)
	var row struct{ State string }
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
	require.Equal(t, "COMPENSATING", row.State)
	require.Positive(t, int64(fake.account.ID))
	m.enrollmentIAM.DeleteAccount = fake.port().DeleteAccount
	require.ErrorIs(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true), ErrEnrollmentFailed)
	require.Equal(t, 1, fake.deleted)
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
	require.Equal(t, "FAILED", row.State)
}

func TestEnrollmentExchangeIAMFailuresReturnNoSecret(t *testing.T) {
	for _, fault := range []string{"token", "issue"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, fake, _ := enrollmentHarness(t)
			started := startTestEnrollment(t, m, ctx)
			require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
			failure := errors.New("test IAM exchange unavailable")
			if fault == "token" {
				m.enrollmentIAM.Token = func(context.Context, url.Values, string) (oauth.TokenResponse, error) {
					return oauth.TokenResponse{}, failure
				}
			} else {
				m.enrollmentIAM.IssueCredential = func(context.Context, guid.ID, guid.ID, string, []string, *time.Time) (identity.ServiceAccountCredentialSecret, error) {
					return identity.ServiceAccountCredentialSecret{}, failure
				}
			}
			credential, err := m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
			var row struct{ State string }
			require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
			if fault == "token" {
				require.NoError(t, err, "generic Human token issuance is deliberately not called")
				require.NotEmpty(t, credential.Credential.Secret)
				require.Equal(t, 1, fake.issued)
				require.Equal(t, "COMPLETE", row.State)
			} else {
				require.ErrorIs(t, err, failure)
				require.Empty(t, credential.Credential.Secret)
				require.Zero(t, fake.issued)
				require.Equal(t, "ISSUING", row.State)
			}
		})
	}
}

func TestEnrollmentLeaseLossFencesProvisioningAndReturnsNoCredentials(t *testing.T) {
	for _, stage := range []string{"acquisition", "before_transaction"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, fake, _ := enrollmentHarness(t)
			started := startTestEnrollment(t, m, ctx)
			acquire := m.enrollmentLease
			m.enrollmentLease = func(ctx context.Context, name string, ttl time.Duration, onLost func(string, error)) (*dlock.Lock, error) {
				if stage == "acquisition" {
					return nil, dlock.ErrNotAcquired
				}
				lock, err := acquire(ctx, name, ttl, onLost)
				if err != nil {
					return nil, err
				}
				require.NoError(t, db.Table("dir_dlocks").Where("name = ?", name).Update("owner", "replacement-worker").Error)
				return lock, nil
			}
			require.ErrorIs(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true), dlock.ErrNotAcquired)
			require.Zero(t, fake.created)
			var row struct{ State string }
			require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
			require.Equal(t, "PENDING", row.State)
		})
	}
}

func TestEnrollmentMissingCapabilitiesAndInvalidStartFailBeforeIAM(t *testing.T) {
	m, _, ctx, _, _ := enrollmentHarness(t)
	for _, input := range []EnrollmentStart{
		{TenantID: 0, ClientID: 20, Name: "runner", OS: "LINUX"},
		{TenantID: 7, ClientID: 0, Name: "runner", OS: "LINUX"},
		{TenantID: 7, ClientID: 20, Name: "", OS: "LINUX"},
		{TenantID: 7, ClientID: 20, Name: "runner", OS: "INVALID"},
	} {
		_, err := m.StartEnrollment(ctx, input)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	input := EnrollmentStart{TenantID: 7, ClientID: 20, Name: "runner", OS: "LINUX"}
	_, err := (*Module)(nil).StartEnrollment(ctx, input)
	require.ErrorIs(t, err, ErrUnavailable)
	m.ids = nil
	_, err = m.StartEnrollment(ctx, input)
	require.ErrorIs(t, err, ErrUnavailable)
	m.enrollmentLease = nil
	require.ErrorIs(t, m.ConfirmEnrollment(ctx, 7, 1, "ABCD-EFGH", true), ErrUnavailable)
}

func TestEnrollmentStartCapsIAMLifetimeAndPersistsEarlierDeadline(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	m.enrollmentIAM.StartDevice = func(context.Context, string, string) (oauth.DeviceAuthorization, error) {
		return oauth.DeviceAuthorization{DeviceCode: "device", UserCode: "ABCD-EFGH", ExpiresIn: 600}, nil
	}
	m.enrollmentIAM.LookupDevice = func(context.Context, guid.ID, string) (oauth.DeviceRequest, error) {
		return oauth.DeviceRequest{ClientID: 20, ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
	}
	before := time.Now().UTC()
	started := startTestEnrollment(t, m, ctx)
	require.Equal(t, int64(300), started.Device.ExpiresIn)
	var row struct{ ExpiresAt time.Time }
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Take(&row).Error)
	require.WithinDuration(t, before.Add(5*time.Minute), row.ExpiresAt, time.Second)
}

func TestEnrollmentPollingPersistsAcrossModuleCopiesAndRejectsEarlyCollection(t *testing.T) {
	m, db, ctx, fake, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	_, err := m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.ErrorIs(t, err, ErrEnrollmentPending)
	replica := &Module{runtime: m.runtime, config: m.config, names: m.names, enrollmentIAM: m.enrollmentIAM, enrollmentLease: m.enrollmentLease, policy: m.policy, principals: m.principals, ids: m.ids, audit: m.audit, events: m.events}
	_, err = replica.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.ErrorIs(t, err, ErrEnrollmentSlowDown)
	q := query.BindEnrollments(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(started.ID)).First()
	require.NoError(t, err)
	require.EqualValues(t, 10, row.PollIntervalSeconds)
	require.NotNil(t, row.LastPolledAt)
	before := *row.LastPolledAt
	_, err = m.ExchangeEnrollment(ctx, 7, started.ID, "wrong")
	require.ErrorIs(t, err, ErrMachineNotFound)
	row, err = q.WithContext(ctx).Where(q.ID.Eq(started.ID)).First()
	require.NoError(t, err)
	require.EqualValues(t, 10, row.PollIntervalSeconds)
	require.True(t, before.Equal(*row.LastPolledAt))
	require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
	_, err = m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.ErrorIs(t, err, ErrEnrollmentSlowDown)
	require.Zero(t, fake.issued)
	require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Update("last_polled_at", time.Now().UTC().Add(-time.Minute)).Error)
	credential, err := replica.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.NoError(t, err)
	require.NotEmpty(t, credential.Credential.Secret)
	require.Equal(t, 1, fake.issued)
	_, err = m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.ErrorIs(t, err, ErrEnrollmentConsumed)
}
func TestEnrollmentPollingWriteFailureRollsBack(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	require.NoError(t, db.Exec("CREATE TRIGGER reject_poll BEFORE UPDATE ON dir_fleet_machine_enrollments WHEN NEW.last_polled_at IS NOT NULL BEGIN SELECT RAISE(ABORT, 'poll write failure'); END").Error)
	_, err := m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
	require.ErrorContains(t, err, "poll write failure")
	q := query.BindEnrollments(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(started.ID)).First()
	require.NoError(t, err)
	require.Nil(t, row.LastPolledAt)
	require.EqualValues(t, 5, row.PollIntervalSeconds)
}

func TestEnrollmentStorageFailuresNeverExposeUncommittedMachineCredentials(t *testing.T) {
	for _, operation := range []string{"start", "approve", "exchange"} {
		for _, kind := range []string{"query", "create", "update"} {
			total := 0
			run := func(t *testing.T, at int) {
				m, db, ctx, fake, _ := enrollmentHarness(t)
				var started EnrollmentStarted
				if operation != "start" {
					started = startTestEnrollment(t, m, ctx)
				}
				if operation == "exchange" {
					require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
				}
				faultDB, writerErr := m.writer()
				require.NoError(t, writerErr)
				count := 0
				injected := errors.New("enrollment storage unavailable")
				hook := func(tx *gorm.DB) {
					if tx.Statement.Table != m.names.Enrollments.String() && tx.Statement.Table != m.names.Machines.String() {
						return
					}
					count++
					if at > 0 && count == at {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, faultDB.Callback().Query().Before("gorm:query").Register("enrollment_failure", hook))
				case "create":
					require.NoError(t, faultDB.Callback().Create().Before("gorm:create").Register("enrollment_failure", hook))
				case "update":
					require.NoError(t, faultDB.Callback().Update().Before("gorm:update").Register("enrollment_failure", hook))
				}
				var err error
				var result EnrollmentCredential
				switch operation {
				case "start":
					started, err = m.StartEnrollment(ctx, EnrollmentStart{TenantID: 7, ClientID: 20, Name: "Storage runner", OS: "LINUX"})
				case "approve":
					err = m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true)
				case "exchange":
					result, err = m.ExchangeEnrollment(ctx, 7, started.ID, started.Device.DeviceCode)
				}
				switch kind {
				case "query":
					require.NoError(t, faultDB.Callback().Query().Remove("enrollment_failure"))
				case "create":
					require.NoError(t, faultDB.Callback().Create().Remove("enrollment_failure"))
				case "update":
					require.NoError(t, faultDB.Callback().Update().Remove("enrollment_failure"))
				}
				if at == 0 {
					require.NoError(t, err)
					if operation == "approve" || kind == "create" && operation == "start" || kind != "create" && operation == "exchange" {
						require.Positive(t, count)
					}
					total = count
					return
				}
				require.GreaterOrEqual(t, count, at)
				require.ErrorIs(t, err, injected)
				require.Empty(t, result.Credential.Secret)
				require.Zero(t, fake.consumed)
				if operation == "start" {
					require.Zero(t, started.ID)
					require.Zero(t, fake.created)
				}
				if operation == "exchange" {
					require.Equal(t, fake.issued, fake.revoked, "every issued secret on a failed exchange must be revoked")
				}
				if operation == "approve" {
					var rows int64
					require.NoError(t, db.Table(m.names.Machines.String()).Where("name = ?", "New runner").Count(&rows).Error)
					require.Zero(t, rows, "failed approval must not expose the machine")
				}
			}
			t.Run(operation+"/"+kind+"/success", func(t *testing.T) { run(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(operation+"/"+kind+"/failure_"+guid.ID(at).String(), func(t *testing.T) { run(t, at) })
			}
		}
	}
}

func TestEnrollmentStartSourceFailuresNeverPersistRequestOrCreateAccount(t *testing.T) {
	for _, stage := range []string{"start", "lookup", "client", "expiry", "id_error", "id_zero", "audit_missing", "events_missing"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, fake, _ := enrollmentHarness(t)
			injected := errors.New("device authority unavailable")
			switch stage {
			case "start":
				m.enrollmentIAM.StartDevice = func(context.Context, string, string) (oauth.DeviceAuthorization, error) {
					return oauth.DeviceAuthorization{}, injected
				}
			case "lookup":
				m.enrollmentIAM.LookupDevice = func(context.Context, guid.ID, string) (oauth.DeviceRequest, error) {
					return oauth.DeviceRequest{}, injected
				}
			case "client":
				m.enrollmentIAM.LookupDevice = func(context.Context, guid.ID, string) (oauth.DeviceRequest, error) {
					return oauth.DeviceRequest{ClientID: 99}, nil
				}
			case "expiry":
				m.enrollmentIAM.StartDevice = func(context.Context, string, string) (oauth.DeviceAuthorization, error) {
					return oauth.DeviceAuthorization{DeviceCode: "expired", UserCode: "ABCD-EFGH"}, nil
				}
			case "id_error":
				m.ids = failedFleetIDs{err: injected}
			case "id_zero":
				m.ids = failedFleetIDs{}
			case "audit_missing":
				m.audit = nil
			case "events_missing":
				m.events = nil
			}
			started, err := m.StartEnrollment(ctx, EnrollmentStart{TenantID: 7, ClientID: 20, Name: "Unavailable runner", OS: "LINUX"})
			require.Error(t, err)
			require.Zero(t, started.ID)
			require.Empty(t, started.Device.DeviceCode)
			var rows int64
			require.NoError(t, db.Table(m.names.Enrollments.String()).Count(&rows).Error)
			require.Zero(t, rows)
			require.Zero(t, fake.created)
			require.Zero(t, fake.issued)
		})
	}
}

func TestEnrollmentCurrentIAMDecisionAndProvisioningFailuresNeverExposeMachine(t *testing.T) {
	for _, fault := range []string{"writer", "lookup", "client", "decision", "find", "create", "rename_after_approval", "deny_after_approval", "invalid_name"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, fake, _ := enrollmentHarness(t)
			started := startTestEnrollment(t, m, ctx)
			if fault == "rename_after_approval" || fault == "deny_after_approval" {
				require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, started.Device.UserCode, true))
			}
			injected := errors.New("enrollment current IAM unavailable")
			switch fault {
			case "writer":
				m.runtime = nil
			case "lookup":
				m.enrollmentIAM.LookupDevice = func(context.Context, guid.ID, string) (oauth.DeviceRequest, error) {
					return oauth.DeviceRequest{}, injected
				}
			case "client":
				m.enrollmentIAM.LookupDevice = func(context.Context, guid.ID, string) (oauth.DeviceRequest, error) {
					return oauth.DeviceRequest{ClientID: 99}, nil
				}
			case "decision":
				m.enrollmentIAM.DecideDevice = func(context.Context, guid.ID, string, guid.ID, bool, time.Time, int, string) error { return injected }
			case "find":
				m.enrollmentIAM.FindAccount = func(context.Context, guid.ID, string) (identity.ServiceAccount, bool, error) {
					return identity.ServiceAccount{}, false, injected
				}
			case "create":
				m.enrollmentIAM.CreateAccount = func(context.Context, guid.ID, string, string, string, *time.Time) (identity.ServiceAccount, error) {
					return identity.ServiceAccount{}, injected
				}
			}
			name := ""
			if fault == "rename_after_approval" {
				name = "Different approved name"
			}
			if fault == "invalid_name" {
				name = "   "
			}
			require.Error(t, m.DecideEnrollment(ctx, 7, started.ID, started.Device.UserCode, fault != "deny_after_approval", name))
			var machines int64
			require.NoError(t, db.Table(m.names.Machines.String()).Where("id=?", started.ID).Count(&machines).Error)
			if fault == "rename_after_approval" || fault == "deny_after_approval" {
				require.EqualValues(t, 1, machines)
			} else {
				require.Zero(t, machines)
				require.Zero(t, fake.created)
			}
			require.Zero(t, fake.issued)
		})
	}
}
