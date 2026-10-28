package iamintegration

import (
	"7link.com/platform/internal/app"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn"
	authnmodel "7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	goosex "7link.com/platform/pkg/extension/goose"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestComposedPrincipalErasure(t *testing.T) {
	runComposedPrincipalErasure(t, nil)
}

func TestComposedPrincipalErasureExternalDatabase(t *testing.T) {
	database := app.Database{Type: os.Getenv(iamLifecycleDatabaseType), Dsn: os.Getenv(iamLifecycleDatabaseDSN), Writable: true, Readable: true, MaxOpenConns: 8, MaxIdleConns: 4}
	if database.Type == "" || database.Dsn == "" {
		t.Skip("external IAM lifecycle database is not configured")
	}
	runComposedPrincipalErasure(t, &database)
}

func runComposedPrincipalErasure(t *testing.T, external *app.Database) {
	t.Helper()
	for index, scenario := range []string{"success", "late-owner-failure", "delivery-in-flight", "platform-access", "unupgraded-owner", "legacy-notification", "disabled-owner", "live-remote-mapping"} {
		t.Run(scenario, func(t *testing.T) {
			ledger := newPrincipalErasureLedgerFixture(t)
			database := app.Database{
				Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "erasure.sqlite"), Writable: true, Readable: true,
			}
			if external != nil {
				database = *external
			}
			if database.Type == "sqlite" && scenario == "legacy-notification" {
				// This fixture temporarily removes and restores authn columns.
				// Another SQLite connection can report its old SELECT * columns
				// before the first step refreshes its schema, losing NULL keys.
				// Keep DDL and snapshots on one connection; retain full equality.
				database.MaxOpenConns = 1
			}
			prefix := ""
			if external != nil {
				prefix = fmt.Sprintf("pe%d_", index)
			}
			f := newIAMTenantFixtureWithDatabase(t, database, prefix, func(config *app.AppConfig) {
				ledger.configure(config)
				if scenario == "disabled-owner" {
					config.Federation.Enabled = false
				}
			})
			if scenario == "disabled-owner" {
				_, enabled := f.app.Modules().ByName(federation.Name)
				require.False(t, enabled, "persisted federation data must be erased without an active owner")
				source := (&federation.Module{}).MigrationSource()
				source.Prefix = f.prefix
				db, err := f.db.DB()
				require.NoError(t, err)
				require.NoError(t, goosex.MigrateUp(t.Context(), db, source, f.db.Dialector.Name()))
			}
			value, ok := f.app.Modules().ByName(identity.Name)
			require.True(t, ok)
			service := value.(*identity.Module).Service
			neighbor := f.createPrincipal(t, value.(*identity.Module), iamTenantB, "neighbor", "neighbor@example.test")
			principal := f.requesterB
			if scenario == "platform-access" {
				principal = f.adminB
			}
			id := principal.ID
			f.seedPrincipalErasureOwners(t, principal, iamTenantB)
			f.seedPrincipalErasureOwners(t, f.adminA, iamTenantA)
			f.seedPrincipalErasureOwners(t, neighbor, iamTenantB)
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
			if scenario == "platform-access" {
				// The tenant route refuses to suspend a platform administrator, so
				// the grant returns after suspension: erasure must still refuse it.
				require.NoError(t, f.db.Exec("DELETE FROM "+f.table("iam_platform_administrators")+" WHERE principal_id = ?", id).Error)
			}
			_, err = service.SetStatus(t.Context(), iamTenantB, id, "disabled")
			require.NoError(t, err)
			if scenario == "platform-access" {
				require.NoError(t, f.db.Table(f.table("iam_platform_administrators")).Create(map[string]any{
					"principal_id": id, "created_at": time.Now().UTC().Truncate(time.Millisecond),
				}).Error)
			}
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
			if scenario == "legacy-notification" {
				// Restore the pre-v13 schema to seed a genuinely legacy row, then
				// reapply the guard before exercising the production entry point.
				f.seedLegacyErasureNotification(t, notification)
			}
			if scenario == "live-remote-mapping" {
				require.NoError(t, f.db.Table(f.prefix+"iam_scim_target_resources").Where("resource_id = ?", id).Update("deleted_at", nil).Error)
			}
			lateFailure := errors.New("last owner cannot persist redaction")
			if scenario == "unupgraded-owner" {
				f.downgradePrincipalErasureOwner(t, governance.MigrationSource(), governance.PrincipalErasureVersion-1)
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
			var scimToken string
			if scenario == "success" || scenario == "disabled-owner" {
				directory, token := f.createSCIMCredential(t, iamTenantB, f.tokenB, "Erasure acceptance")
				scimToken = token
				require.NoError(t, f.db.Table(f.table("iam_scim_resources")).Where("resource_id = ?", id).Update("directory_id", directory.ID).Error)
				response := f.request(t, http.MethodGet, "/scim/v2/Users/"+id.String(), 0, scimToken, nil, nil)
				requireStatus(t, http.StatusOK, response)
			}
			before := f.principalErasureSnapshot(t, id)
			bystander := f.principalErasureSnapshot(t, f.adminA.ID)
			neighborBefore := f.principalErasureSnapshot(t, neighbor.ID)
			audits := f.erasureRows(t, "iam_audit_events", "1 = 1")
			revisions, revisionErr := core.NewRevisionStore(appenv.NewRuntime(
				appenv.NewIdentity("principallifecycle-test", "1"), appenv.WithTablePrefix(f.prefix),
			))
			require.NoError(t, revisionErr)
			revision, revisionErr := revisions.Current(t.Context(), f.db, iamTenantB)
			require.NoError(t, revisionErr)
			unrelatedRevision, revisionErr := revisions.Current(t.Context(), f.db, iamTenantA)
			require.NoError(t, revisionErr)
			result, err := service.AnonymizePrincipal(t.Context(), id, policy.Version, principal.LoginName)
			currentUnrelatedRevision, revisionErr := revisions.Current(t.Context(), f.db, iamTenantA)
			require.NoError(t, revisionErr)
			assert.Equal(t, unrelatedRevision, currentUnrelatedRevision, "an unrelated tenant's policy revision is preserved")
			assert.Equal(t, bystander, f.principalErasureSnapshot(t, f.adminA.ID), "another tenant's data is unchanged")
			assert.Equal(t, neighborBefore, f.principalErasureSnapshot(t, neighbor.ID), "another principal in the same tenant is unchanged")
			for _, row := range audits {
				assert.Equal(t, []map[string]any{row}, f.erasureRows(t, "iam_audit_events", "id = ?", row["id"]), "existing audit evidence is immutable")
			}
			if scenario != "success" && scenario != "disabled-owner" {
				switch scenario {
				case "late-owner-failure":
					require.ErrorIs(t, err, lateFailure)
				case "delivery-in-flight":
					require.ErrorIs(t, err, authn.ErrNotificationInFlight)
				case "platform-access":
					require.ErrorIs(t, err, core.ErrPrincipalHasPlatformAccess)
				case "unupgraded-owner":
					require.ErrorIs(t, err, identity.ErrUnavailable)
				case "legacy-notification":
					require.ErrorIs(t, err, authn.ErrLegacyNotifications)
				case "live-remote-mapping":
					require.ErrorIs(t, err, provisioning.ErrPrincipalRemoteProvisioned)
				}
				assert.Equal(t, before, f.principalErasureSnapshot(t, id), "all owner mutations roll back together")
				assert.Equal(t, audits, f.erasureRows(t, "iam_audit_events", "1 = 1"), "failed erasure emits no committed audit event")
				current, readErr := revisions.Current(t.Context(), f.db, iamTenantB)
				require.NoError(t, readErr)
				assert.Equal(t, revision, current)
				assert.Equal(t, credentials, count("iam_credentials"))
				assert.EqualValues(t, 1, count("iam_notification_outbox"))
				stored, readErr := service.GetPrincipalLifecycle(t.Context(), id)
				require.NoError(t, readErr)
				assert.Equal(t, policy.Version, stored.Version)
				assert.Nil(t, stored.AnonymizedAt)
				var email string
				require.NoError(t, f.db.Table(f.prefix+"iam_tenant_members").Where("principal_id = ?", id).Pluck("email", &email).Error)
				assert.Equal(t, principal.Email, email)
				requirePrincipalErasureObligationCount(t, ledger, 0)
				return
			}
			require.NoError(t, err)
			requirePrincipalErasureObligationCount(t, ledger, 1)
			require.NotNil(t, result.AnonymizedAt)
			assert.Equal(t, policy.Version+1, result.Version)
			f.assertPrincipalOwnersErased(t, principal, before)
			current, readErr := revisions.Current(t.Context(), f.db, iamTenantB)
			require.NoError(t, readErr)
			assert.Equal(t, revision+1, current, "cached authorization must observe a new policy revision")
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
			response := f.request(t, http.MethodGet, "/me", iamTenantB, f.requester, nil, nil)
			requireStatus(t, http.StatusUnauthorized, response)
			response = f.request(t, http.MethodGet, "/me", iamTenantA, f.tokenA, nil, nil)
			requireStatus(t, http.StatusOK, response)
			response = f.request(t, http.MethodPut, "/scim/v2/Users/"+id.String(), 0, scimToken, map[string]any{
				"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
				"userName": principal.LoginName, "displayName": "Restored", "active": true,
			}, nil)
			requireStatus(t, http.StatusNotFound, response)
			assert.Empty(t, f.principalErasureSnapshot(t, id)["iam_principals"][0]["email"])
		})
	}
}

// TestPrincipalErasureSkipsAnUninstalledOwner covers the default deployment:
// federation is disabled and was never migrated, so it holds no records and
// must not block erasure.
func TestPrincipalErasureSkipsAnUninstalledOwner(t *testing.T) {
	f, service := newPrincipalErasureVersionFixture(t, func(config *app.AppConfig) {
		config.Federation.Enabled = false
	})
	source := federation.MigrationSource()
	source.Prefix = f.prefix
	installed, err := goosex.CurrentVersion(t.Context(), f.db, source)
	require.NoError(t, err)
	require.Zero(t, installed, "a disabled federation module is never migrated")

	principal, version := f.suspendForPrincipalErasure(t, service)
	require.Positive(t, f.erasureCount(t, "iam_credentials", principal.ID))
	result, err := service.AnonymizePrincipal(t.Context(), principal.ID, version, principal.LoginName)
	require.NoError(t, err)
	assert.NotNil(t, result.AnonymizedAt)
	assert.Zero(t, f.erasureCount(t, "iam_credentials", principal.ID), "installed owners are still erased")
}

func newPrincipalErasureVersionFixture(t *testing.T, configure ...func(*app.AppConfig)) (*iamTenantFixture, *identity.Service) {
	t.Helper()
	ledger := newPrincipalErasureLedgerFixture(t)
	database := app.Database{
		Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "erasure.sqlite"), Writable: true, Readable: true,
	}
	f := newIAMTenantFixtureWithDatabase(t, database, "", append([]func(*app.AppConfig){ledger.configure}, configure...)...)
	return f, requireIAMModule[*identity.Module](t, f, identity.Name).Service
}

// suspendForPrincipalErasure brings requesterB to the state erasure requires:
// an expired retention policy and a suspended account.
func (f *iamTenantFixture) suspendForPrincipalErasure(t *testing.T, service *identity.Service) (identity.Principal, int64) {
	t.Helper()
	principal := f.requesterB
	policy, err := service.SetPrincipalRetention(t.Context(), principal.ID, 0, time.Now().UTC().Add(-time.Hour), false, "case-acceptance")
	require.NoError(t, err)
	_, err = service.SetStatus(t.Context(), iamTenantB, principal.ID, "disabled")
	require.NoError(t, err)
	return principal, policy.Version
}

// downgradePrincipalErasureOwner takes one owner's installed schema back to
// version, leaving it behind what its erasure needs.
func (f *iamTenantFixture) downgradePrincipalErasureOwner(t *testing.T, source goosex.Source, version int64) {
	t.Helper()
	source.Prefix = f.prefix
	db, err := f.db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, f.db.Dialector.Name(), version))
}

func (f *iamTenantFixture) erasureCount(t *testing.T, table string, id guid.ID) int64 {
	t.Helper()
	var count int64
	require.NoError(t, f.db.Table(f.table(table)).Where("principal_id = ?", id).Count(&count).Error)
	return count
}

func (f *iamTenantFixture) seedPrincipalErasureOwners(t *testing.T, principal identity.Principal, tenant guid.ID) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	id := principal.ID
	insert := func(table string, row map[string]any) {
		t.Helper()
		require.NoError(t, f.db.Table(f.table(table)).Create(row).Error, table)
	}
	insert("iam_sessions", map[string]any{"id_hash": "session-" + id.String(), "principal_id": id, "created_at": now, "auth_time": now, "last_seen_at": now, "expires_at": now.Add(time.Hour), "absolute_expires_at": now.Add(time.Hour), "ip_address": "192.0.2.1", "user_agent": "Historical client"})
	insert("iam_oauth_clients", map[string]any{"id": id, "tenant_id": tenant, "name": "Erasure client", "created_at": now, "updated_at": now})
	insert("iam_oauth_consents", map[string]any{"principal_id": id, "client_id": id, "tenant_id": tenant, "scope": "openid", "created_at": now, "last_used_at": now})
	insert("iam_refresh_tokens", map[string]any{"id_hash": "refresh-" + id.String(), "family_id": "family-" + id.String(), "principal_id": id, "client_id": id, "scope": "openid", "auth_time": now, "created_at": now, "expires_at": now.Add(time.Hour)})
	insert("iam_oauth_codes", map[string]any{"code_hash": "code-" + id.String(), "principal_id": id, "client_id": id, "redirect_uri": "https://app.example.test/callback", "scope": "openid", "code_challenge": "challenge", "auth_time": now, "created_at": now, "expires_at": now.Add(time.Hour)})
	insert("iam_oauth_consent_requests", map[string]any{"challenge_hash": "consent-" + id.String(), "principal_id": id, "client_id": id, "redirect_uri": "https://app.example.test/callback", "scope": "openid", "state": "state", "code_challenge": "challenge", "created_at": now, "expires_at": now.Add(time.Hour)})
	for offset := range 2 {
		provider := id + guid.ID(offset)
		insert("iam_identity_providers", map[string]any{"id": provider, "tenant_id": tenant, "name": "Provider " + provider.String(), "issuer": "https://idp.example.test/" + provider.String(), "client_id": "client", "created_at": now, "updated_at": now})
		insert("iam_identity_links", map[string]any{"provider_id": provider, "tenant_id": tenant, "principal_id": id, "external_subject": "upstream-" + id.String(), "email": principal.Email, "display_name": principal.DisplayName, "last_login_at": now, "created_at": now, "updated_at": now})
	}
	insert("iam_scim_directories", map[string]any{"id": id, "tenant_id": tenant, "name": "Directory " + id.String(), "name_key": "directory-" + id.String(), "status": "active", "created_at": now, "updated_at": now})
	insert("iam_scim_resources", map[string]any{"directory_id": id, "resource_type": "User", "resource_id": id, "external_id": "source-" + id.String(), "external_key": "source-" + id.String(), "created_at": now, "updated_at": now})
	insert("iam_scim_targets", map[string]any{"id": id, "tenant_id": tenant, "name": "Target " + id.String(), "name_key": "target-" + id.String(), "base_url": "https://scim.example.test", "bearer_token_ciphertext": "sealed", "status": "active", "created_at": now, "updated_at": now})
	insert("iam_scim_target_resources", map[string]any{"target_id": id, "resource_type": "User", "resource_id": id, "external_id": "remote-" + id.String(), "deleted_at": now, "created_at": now, "updated_at": now})
	insert("iam_invitations", map[string]any{"tenant_id": tenant, "id": id, "email": principal.Email, "email_key": principal.Email, "token_hmac": "invite-" + id.String(), "status": "accepted", "expires_at": now.Add(time.Hour), "accepted_by": id, "created_at": now, "updated_at": now})
	insert("iam_access_reviews", map[string]any{"tenant_id": tenant, "id": id, "name": "Quarterly review", "owner_id": f.adminA.ID, "status": "completed", "due_at": now, "completed_at": now, "created_at": now, "updated_at": now})
	insert("iam_access_review_items", map[string]any{"tenant_id": tenant, "review_id": id, "id": id, "principal_id": id, "principal_name": principal.DisplayName, "role_id": id, "role_name": "Reviewed role", "grant_type": "permanent", "grant_created_at": now, "decision": "keep", "decided_by": f.adminA.ID, "decision_note": "Still required", "decided_at": now})
}

func (f *iamTenantFixture) erasureRows(t *testing.T, table, predicate string, args ...any) []map[string]any {
	t.Helper()
	var rows []map[string]any
	require.NoError(t, f.db.Table(f.table(table)).Where(predicate, args...).Find(&rows).Error)
	for _, row := range rows {
		for key, value := range row {
			if bytes, ok := value.([]byte); ok {
				row[key] = string(bytes)
			}
		}
	}
	return rows
}

func (f *iamTenantFixture) principalErasureSnapshot(t *testing.T, id guid.ID) map[string][]map[string]any {
	t.Helper()
	result := make(map[string][]map[string]any)
	for _, table := range []string{"iam_credentials", "iam_sessions", "iam_notification_outbox", "iam_tenant_members", "iam_oauth_consents", "iam_refresh_tokens", "iam_oauth_codes", "iam_oauth_consent_requests", "iam_identity_links", "iam_access_review_items", "iam_principal_lifecycles"} {
		result[table] = f.erasureRows(t, table, "principal_id = ?", id)
	}
	result["iam_principals"] = f.erasureRows(t, "iam_principals", "id = ?", id)
	result["iam_invitations"] = f.erasureRows(t, "iam_invitations", "accepted_by = ?", id)
	for _, table := range []string{"iam_scim_resources", "iam_scim_target_resources"} {
		result[table] = f.erasureRows(t, table, "resource_type = 'User' AND resource_id = ?", id)
	}
	return result
}

func (f *iamTenantFixture) assertPrincipalOwnersErased(t *testing.T, principal identity.Principal, before map[string][]map[string]any) {
	t.Helper()
	after := f.principalErasureSnapshot(t, principal.ID)
	for _, table := range []string{"iam_credentials", "iam_sessions", "iam_notification_outbox", "iam_oauth_consents", "iam_refresh_tokens", "iam_oauth_codes", "iam_oauth_consent_requests", "iam_identity_links"} {
		require.NotEmpty(t, before[table], "%s must actually exercise deletion", table)
		assert.Empty(t, after[table], table)
	}
	for _, table := range []string{"iam_scim_resources", "iam_scim_target_resources"} {
		require.Len(t, after[table], 1)
		assert.Empty(t, after[table][0]["external_id"])
		assert.NotNil(t, after[table][0]["deleted_at"])
		assert.EqualValues(t, 2, after[table][0]["version"])
	}
	assert.Equal(t, "erased:"+principal.ID.String(), after["iam_scim_resources"][0]["external_key"])
	require.Len(t, after["iam_invitations"], 1)
	assert.Empty(t, after["iam_invitations"][0]["email"])
	assert.Empty(t, after["iam_invitations"][0]["email_key"])
	require.Len(t, after["iam_access_review_items"], 1)
	expectedReview := before["iam_access_review_items"][0]
	expectedReview["principal_name"] = "Anonymized account"
	assert.Equal(t, expectedReview, after["iam_access_review_items"][0], "only the cached name changes; the decision and its provenance survive")
	assert.Equal(t, "erased:"+principal.ID.String(), after["iam_principals"][0]["login_name"])
	assert.Empty(t, after["iam_principals"][0]["email"])
	name, email := "Restored", "restored@example.test"
	ctx := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: principal.ID})
	_, err := requireIAMModule[*identity.Module](t, f, identity.Name).Service.Update(ctx, iamTenantB, principal.ID, &name, &email, nil)
	assert.ErrorContains(t, err, "anonymized", "the identity API must not restore a tombstone")
}

func (f *iamTenantFixture) seedLegacyErasureNotification(t *testing.T, row authnmodel.NotificationOutbox) {
	t.Helper()
	source := (&authn.Module{}).MigrationSource()
	source.Prefix = f.prefix
	db, err := f.db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, f.db.Dialector.Name(), 12))
	row.ID++
	row.PrincipalID = nil
	require.NoError(t, f.db.Table(f.table("iam_notification_outbox")).Create(&row).Error)
	require.NoError(t, goosex.MigrateUp(t.Context(), db, source, f.db.Dialector.Name()))
}

func TestPrincipalErasureConcurrencyExternalDatabase(t *testing.T) {
	database := app.Database{Type: os.Getenv(iamLifecycleDatabaseType), Dsn: os.Getenv(iamLifecycleDatabaseDSN), Writable: true, Readable: true, MaxOpenConns: 8, MaxIdleConns: 4}
	if database.Type == "" || database.Dsn == "" {
		t.Skip("external IAM lifecycle database is not configured")
	}
	require.Contains(t, []string{"postgres", "mysql"}, database.Type, "concurrency requires real row locking")
	ledger := newPrincipalErasureLedgerFixture(t)
	f := newIAMTenantFixtureWithDatabase(t, database, "pc_", ledger.configure)
	identityModule := requireIAMModule[*identity.Module](t, f, identity.Name)
	for _, owner := range []string{"authn", "core", "oauth", "federation", "provisioning", "organization", "governance", "delivery-in-flight", "live-remote-mapping"} {
		for _, order := range []string{"writer-first", "erasure-first"} {
			t.Run(owner+"/"+order, func(t *testing.T) {
				login := owner + "-" + order
				principal := f.createPrincipal(t, identityModule, iamTenantB, login, login+"@example.test")
				f.seedPrincipalErasureOwners(t, principal, iamTenantB)
				service := identityModule.Service
				until := time.Now().UTC().Add(-time.Hour)
				policy, err := service.SetPrincipalRetention(t.Context(), principal.ID, 0, until, false, "concurrency-acceptance")
				require.NoError(t, err)
				_, err = service.SetStatus(t.Context(), iamTenantB, principal.ID, "disabled")
				require.NoError(t, err)
				notification := authnmodel.NotificationOutbox{ID: principal.ID, PrincipalID: &principal.ID, Kind: "email", Recipient: principal.Email, PayloadCiphertext: "sealed", Status: "pending", AvailableAt: until, CreatedAt: until, UpdatedAt: until}
				require.NoError(t, f.db.Table(f.table("iam_notification_outbox")).Create(&notification).Error)
				write := func(tx *gorm.DB) error {
					switch owner {
					case "authn":
						row := notification
						row.ID++
						return tx.Table(f.table("iam_notification_outbox")).Create(&row).Error
					case "core":
						return tx.Table(f.table("iam_tenant_members")).Where("principal_id = ?", principal.ID).Update("email", "concurrent@example.test").Error
					case "oauth":
						return tx.Table(f.table("iam_oauth_consents")).Where("principal_id = ?", principal.ID).Update("scope", "openid email").Error
					case "federation":
						return tx.Table(f.table("iam_identity_links")).Where("principal_id = ?", principal.ID).Update("email", "concurrent@example.test").Error
					case "provisioning":
						return tx.Table(f.table("iam_scim_resources")).Where("resource_id = ?", principal.ID).Update("external_id", "concurrent-external").Error
					case "organization":
						return tx.Table(f.table("iam_invitations")).Where("accepted_by = ?", principal.ID).Update("email", "concurrent@example.test").Error
					case "governance":
						return tx.Table(f.table("iam_access_review_items")).Where("principal_id = ?", principal.ID).Update("principal_name", "Concurrent name").Error
					case "delivery-in-flight":
						return tx.Table(f.table("iam_notification_outbox")).Where("principal_id = ?", principal.ID).Update("status", "delivering").Error
					default:
						return tx.Table(f.table("iam_scim_target_resources")).Where("resource_id = ?", principal.ID).Update("deleted_at", nil).Error
					}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				erased := make(chan error, 1)
				erase := func() {
					_, eraseErr := service.AnonymizePrincipal(ctx, principal.ID, policy.Version, principal.LoginName)
					erased <- eraseErr
				}
				if order == "writer-first" {
					tx := f.db.WithContext(ctx).Begin()
					require.NoError(t, tx.Error)
					defer func() { _ = tx.Rollback().Error }()
					require.NoError(t, write(tx))
					transactionFixture := *f
					transactionFixture.db = tx
					before := transactionFixture.principalErasureSnapshot(t, principal.ID)
					holder := erasureConnectionID(t, tx)
					go erase()
					f.requireErasureLockWait(t, holder)
					require.NoError(t, tx.Commit().Error)
					err = awaitErasure(t, ctx, erased)
					if owner == "delivery-in-flight" || owner == "live-remote-mapping" {
						if owner == "delivery-in-flight" {
							require.ErrorIs(t, err, authn.ErrNotificationInFlight)
						} else {
							require.ErrorIs(t, err, provisioning.ErrPrincipalRemoteProvisioned)
						}
						assert.Equal(t, before, f.principalErasureSnapshot(t, principal.ID))
						return
					}
					require.NoError(t, err)
				} else {
					// Pause the real erasure transaction after its principal lock but
					// before owner cleanup. The contender must be visible in the DB's
					// lock-wait graph; a scheduling delay is not evidence of fencing.
					locked := make(chan int64, 1)
					release := make(chan struct{})
					var once sync.Once
					unblock := func() { once.Do(func() { close(release) }) }
					defer unblock()
					const callback = "test:pause_locked_erasure"
					require.NoError(t, f.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
						if tx.Statement.Context != ctx || tx.Statement.Table != f.table("iam_principals") || tx.Error != nil {
							return
						}
						var connection int64
						query := "SELECT pg_backend_pid()"
						if database.Type == "mysql" {
							query = "SELECT CONNECTION_ID()"
						}
						if readErr := tx.Session(&gorm.Session{NewDB: true}).Raw(query).Scan(&connection).Error; readErr != nil {
							tx.AddError(readErr)
							return
						}
						locked <- connection
						select {
						case <-release:
						case <-ctx.Done():
							tx.AddError(ctx.Err())
						}
					}))
					defer func() { require.NoError(t, f.db.Callback().Query().Remove(callback)) }()
					go erase()
					var holder int64
					select {
					case holder = <-locked:
					case <-ctx.Done():
						t.Fatal("erasure never acquired its principal lock")
					}
					written := make(chan error, 1)
					go func() { written <- write(f.db.WithContext(ctx)) }()
					f.requireErasureLockWait(t, holder)
					unblock()
					eraseErr := awaitErasure(t, ctx, erased)
					writeErr := awaitErasure(t, ctx, written)
					if eraseErr != nil {
						// Erasure held the principal lock first, so being refused
						// here means it lost a lock cycle: the rollback released
						// that lock, the contender committed while it was gone, and
						// the retry then re-read a world in which a fence genuinely
						// stands. Refusing is the right answer at that point -- a
						// notification really is with a sender, a remote account
						// really is live -- and it is the writer-first outcome
						// reached by a different route, so it carries the same
						// obligation: nothing may have been erased.
						require.Contains(t, []string{"delivery-in-flight", "live-remote-mapping"}, owner,
							"only a fenced owner may overtake erasure; anything else is a refusal to explain")
						if owner == "delivery-in-flight" {
							require.ErrorIs(t, eraseErr, authn.ErrNotificationInFlight)
						} else {
							require.ErrorIs(t, eraseErr, provisioning.ErrPrincipalRemoteProvisioned)
						}
						// Whole-snapshot equality cannot be the test here: the
						// contender committed a change of its own, and that change
						// is the reason for the refusal. What must be untouched is
						// what erasure would have changed.
						after := f.principalErasureSnapshot(t, principal.ID)
						assert.Equal(t, principal.LoginName, after["iam_principals"][0]["login_name"],
							"a refused erasure leaves the principal identifiable")
						assert.NotEmpty(t, after["iam_tenant_members"][0]["email"])
						assert.NotEmpty(t, after["iam_identity_links"], "no owner may be half erased")
						assert.NotEmpty(t, after["iam_oauth_consents"])
						stored, storedErr := service.GetPrincipalLifecycle(t.Context(), principal.ID)
						require.NoError(t, storedErr)
						assert.Nil(t, stored.AnonymizedAt, "a refused erasure leaves no tombstone")
						return
					}
					if erasureTransactionConflict(writeErr) {
						// A raw child UPDATE may acquire its child lock before the
						// trigger acquires the parent. Deadlock victim rollback is
						// valid fencing, but retry must still obey the tombstone.
						writeErr = write(f.db.WithContext(ctx))
					}
					if writeErr != nil {
						assert.ErrorContains(t, writeErr, "anonymized", "retry must be rejected by the tombstone")
					}
					// The write is not required to fail. When the database picks
					// erasure as the victim of a lock cycle, rolling it back frees
					// the principal lock this writer was queued behind, so the write
					// commits before any tombstone exists and the retried erasure
					// redacts it afterwards. Which side loses a cycle is the
					// database's choice, so requiring a refusal here would be
					// requiring erasure never to lose one -- and erasure that never
					// retries is the raw deadlock an operator used to see.
					//
					// What must hold either way is that the write does not survive.
					// That is asserted below for every owner: a write landing after
					// the tombstone without being fenced would leave the contended
					// value in place and fail those assertions.
				}
				stored, err := service.GetPrincipalLifecycle(t.Context(), principal.ID)
				require.NoError(t, err)
				require.NotNil(t, stored.AnonymizedAt)
				assert.Equal(t, policy.Version+1, stored.Version)
				after := f.principalErasureSnapshot(t, principal.ID)
				for _, table := range []string{"iam_notification_outbox", "iam_oauth_consents", "iam_identity_links"} {
					assert.Empty(t, after[table], table)
				}
				assert.Empty(t, after["iam_tenant_members"][0]["email"])
				assert.Empty(t, after["iam_scim_resources"][0]["external_id"])
				assert.Empty(t, after["iam_scim_target_resources"][0]["external_id"])
				assert.Empty(t, after["iam_invitations"][0]["email"])
				assert.Equal(t, "Anonymized account", after["iam_access_review_items"][0]["principal_name"])
			})
		}
	}
}

func erasureTransactionConflict(err error) bool {
	var postgres interface{ SQLState() string }
	if errors.As(err, &postgres) {
		return postgres.SQLState() == "40P01" || postgres.SQLState() == "40001"
	}
	var mysql *mysqldriver.MySQLError
	return errors.As(err, &mysql) && mysql.Number == 1213
}

func erasureConnectionID(t *testing.T, tx *gorm.DB) int64 {
	t.Helper()
	query := "SELECT pg_backend_pid()"
	if tx.Dialector.Name() == "mysql" {
		query = "SELECT CONNECTION_ID()"
	}
	var id int64
	require.NoError(t, tx.Raw(query).Scan(&id).Error)
	return id
}

func (f *iamTenantFixture) requireErasureLockWait(t *testing.T, holder int64) {
	t.Helper()
	query := "SELECT count(*) FROM pg_stat_activity WHERE ? = ANY(pg_blocking_pids(pid))"
	if f.db.Dialector.Name() == "mysql" {
		query = "SELECT count(*) FROM performance_schema.data_lock_waits w JOIN performance_schema.threads b ON b.THREAD_ID = w.BLOCKING_THREAD_ID WHERE b.PROCESSLIST_ID = ?"
	}
	require.EventuallyWithT(t, func(check *assert.CollectT) {
		var count int64
		if assert.NoError(check, f.db.Raw(query, holder).Scan(&count).Error) {
			assert.Positive(check, count, "the second transaction must actually wait for the principal lock")
		}
	}, 5*time.Second, 10*time.Millisecond)
}

func awaitErasure(t *testing.T, ctx context.Context, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatal("concurrent erasure operation did not finish: ", ctx.Err())
		return ctx.Err()
	}
}
