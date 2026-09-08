package app

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn"
	authnmodel "7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestComposedPrincipalErasure(t *testing.T) {
	for _, scenario := range []string{"success", "late-owner-failure", "delivery-in-flight", "platform-access", "unupgraded-owner"} {
		t.Run(scenario, func(t *testing.T) {
			f := newIAMTenantFixture(t)
			value, ok := f.app.modules.ByName(identity.Name)
			require.True(t, ok)
			service := value.(*identity.Module).Service
			principal := f.requesterB
			if scenario == "platform-access" {
				principal = f.adminB
			}
			id := principal.ID
			count := func(table string) int64 {
				t.Helper()
				var result int64
				require.NoError(t, f.db.Table(f.prefix+table).Where("principal_id = ?", id).Count(&result).Error)
				return result
			}
			credentials := count("iam_credentials")
			require.Positive(t, credentials)
			until := time.Now().UTC().Add(-time.Hour)
			policy, err := service.SetPrincipalRetention(t.Context(), id, 0, until, false, "case-acceptance")
			require.NoError(t, err)
			_, err = service.SetStatus(t.Context(), iamTenantB, id, "disabled")
			require.NoError(t, err)
			status := "pending"
			if scenario == "delivery-in-flight" {
				status = "delivering"
			}
			notification := authnmodel.NotificationOutbox{
				ID: 999001, PrincipalID: &id, Kind: "email", Recipient: principal.Email,
				PayloadCiphertext: "test-sealed-payload", Status: status,
				AvailableAt: until, CreatedAt: until, UpdatedAt: until,
			}
			require.NoError(t, f.db.Table(f.prefix+"iam_notification_outbox").Create(&notification).Error)
			lateFailure := errors.New("last owner cannot persist redaction")
			if scenario == "unupgraded-owner" {
				require.NoError(t, f.db.Table(f.prefix+"goose_governance").Where("version_id = ?", 4).Update("is_applied", false).Error)
			}
			if scenario == "late-owner-failure" {
				const callback = "test:fail_erasure_last_owner"
				require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register(callback, func(db *gorm.DB) {
					if db.Statement.Table == f.prefix+"iam_access_review_items" {
						db.AddError(lateFailure)
					}
				}))
				t.Cleanup(func() { require.NoError(t, f.db.Callback().Update().Remove(callback)) })
			}
			result, err := service.AnonymizePrincipal(t.Context(), id, policy.Version, principal.LoginName)
			if scenario != "success" {
				switch scenario {
				case "late-owner-failure":
					require.ErrorIs(t, err, lateFailure)
				case "delivery-in-flight":
					require.ErrorIs(t, err, authn.ErrNotificationInFlight)
				case "platform-access":
					require.ErrorIs(t, err, core.ErrPrincipalHasPlatformAccess)
				case "unupgraded-owner":
					require.ErrorIs(t, err, identity.ErrUnavailable)
				}
				assert.Equal(t, credentials, count("iam_credentials"))
				assert.EqualValues(t, 1, count("iam_notification_outbox"))
				stored, readErr := service.GetPrincipalLifecycle(t.Context(), id)
				require.NoError(t, readErr)
				assert.Equal(t, policy.Version, stored.Version)
				assert.Nil(t, stored.AnonymizedAt)
				var email string
				require.NoError(t, f.db.Table(f.prefix+"iam_tenant_members").Where("principal_id = ?", id).Pluck("email", &email).Error)
				assert.Equal(t, principal.Email, email)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result.AnonymizedAt)
			assert.Equal(t, policy.Version+1, result.Version)
			assert.Zero(t, count("iam_credentials"))
			assert.Zero(t, count("iam_notification_outbox"))
			assert.Zero(t, count("iam_sessions"))
			var member struct{ Email, DisplayName, Status string }
			require.NoError(t, f.db.Table(f.prefix+"iam_tenant_members").Where("principal_id = ?", id).Take(&member).Error)
			assert.Empty(t, member.Email)
			assert.Equal(t, "Anonymized account", member.DisplayName)
			assert.Equal(t, "disabled", member.Status)
			var auditCount int64
			require.NoError(t, f.db.Table(f.prefix+"iam_audit_events").Where("target_id = ? AND event_type = ?", id, "principal_anonymized").Count(&auditCount).Error)
			assert.EqualValues(t, 1, auditCount)
			assert.Error(t, f.db.Table(f.prefix+"iam_tenant_members").Where("principal_id = ?", id).Update("email", "restored@example.test").Error)
			assert.Error(t, f.db.Table(f.prefix+"iam_principals").Where("id = ?", id).Update("status", "active").Error)
			assert.Error(t, f.db.Table(f.prefix+"iam_notification_outbox").Create(&notification).Error)
			response := f.request(t, http.MethodGet, "/authn/me", iamTenantB, f.requester, nil, nil)
			requireStatus(t, http.StatusUnauthorized, response)
			response = f.request(t, http.MethodGet, "/authn/me", iamTenantA, f.tokenA, nil, nil)
			requireStatus(t, http.StatusOK, response)
		})
	}
}
