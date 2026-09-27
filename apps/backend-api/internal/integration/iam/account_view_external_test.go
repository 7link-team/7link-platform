package iamintegration

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/app"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestAccountViewExternalDatabase(t *testing.T) {
	databaseType, dsn := strings.TrimSpace(os.Getenv(iamLifecycleDatabaseType)), strings.TrimSpace(os.Getenv(iamLifecycleDatabaseDSN))
	if databaseType == "" || dsn == "" {
		t.Skip("external IAM lifecycle database is not configured")
	}
	require.Contains(t, []string{"mysql", "postgres"}, databaseType, "row-lock acceptance requires a real transactional engine")
	f := newIAMTenantFixtureWithDatabase(t, app.Database{
		Type: databaseType, Dsn: dsn, Writable: true, Readable: true, MaxOpenConns: 8, MaxIdleConns: 4,
	}, os.Getenv(iamLifecycleTablePrefix))
	service := requireIAMModule[*iamauthn.Module](t, f, iamauthn.Name).Service
	cookie := f.closureCookie(t, f.adminA)
	headers := map[string]string{"Cookie": cookie, "Origin": "https://app.example.test"}
	targetSessions := f.count(t, "iam_sessions", "principal_id = ?", f.requesterB.ID)
	operatorSessions := f.count(t, "iam_sessions", "principal_id = ?", f.adminA.ID)
	organization := f.request(t, http.MethodPost, "/account-views", 0, "", map[string]any{
		"target_type": "tenant", "scope_type": "tenant", "scope_id": iamTenantB.String(), "mode": "read_write",
	}, headers)
	requireStatus(t, http.StatusOK, organization)
	organizationView := decodeEnvelope[iamauthn.AccountViewCreated](t, organization)
	organizationClaims, err := service.Authenticate(t.Context(), "Bearer "+organizationView.Token, "")
	require.NoError(t, err)
	require.Equal(t, f.adminA.ID, organizationClaims.PrincipalID)
	require.Equal(t, "tenant", organizationClaims.AccountView.TargetType)
	requireStatus(t, http.StatusOK, f.request(t, http.MethodGet, "/tenants/"+iamTenantB.String()+"/roles", 0, organizationView.Token, nil, nil))
	requireStatus(t, http.StatusForbidden, f.request(t, http.MethodGet, "/tenants/"+iamTenantA.String()+"/roles", 0, organizationView.Token, nil, nil))
	require.Error(t, f.db.Table(f.table("iam_account_view_tokens")).Where("id = ?", organizationView.ID).Update("target_type", "account").Error)
	create := func() iamauthn.AccountViewCreated {
		response := f.request(t, http.MethodPost, "/account-views", 0, "", map[string]any{
			"principal_id": f.requesterB.ID.String(), "scope_type": "tenant", "scope_id": iamTenantB.String(),
		}, headers)
		requireStatus(t, http.StatusOK, response)
		created := decodeEnvelope[iamauthn.AccountViewCreated](t, response)
		require.Equal(t, "read_only", created.Mode)
		require.True(t, strings.HasPrefix(created.Token, "avt_"))
		require.Empty(t, response.Cookies)
		return created
	}
	created := create()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	claims, err := service.Authenticate(ctx, "Bearer "+created.Token, "")
	require.NoError(t, err, "actual authority callback must execute its locked queries on this dialect")
	require.Equal(t, f.requesterB.ID, claims.PrincipalID)
	require.Equal(t, f.adminA.ID, claims.AccountView.PrincipalID)
	require.Zero(t, claims.ACR)
	require.Empty(t, claims.AMR)
	require.True(t, claims.AuthTime.IsZero())
	response := f.request(t, http.MethodDelete, "/account-views/"+created.ID.String(), 0, "", nil, headers)
	requireStatus(t, http.StatusOK, response)
	_, err = service.Authenticate(ctx, "Bearer "+created.Token, "")
	require.Error(t, err)
	for _, event := range []string{"account_view_created", "account_view_used", "account_view_revoked"} {
		require.Equal(t, int64(1), f.count(t, "iam_audit_events", "event_type = ? AND target_id = ?", event, created.ID))
	}

	active := create()
	require.True(t, t.Run("membership-revoked-while-use-waits", func(t *testing.T) {
		f.accountViewUseAfterLockedMutation(t, service, active.Token, func(tx *gorm.DB) error {
			return tx.Table(f.table("iam_tenant_members")).Where("tenant_id = ? AND principal_id = ?", iamTenantB, f.requesterB.ID).
				Update("status", "disabled").Error
		})
	}))
	require.NoError(t, f.db.Table(f.table("iam_tenant_members")).Where("tenant_id = ? AND principal_id = ?", iamTenantB, f.requesterB.ID).
		Update("status", "active").Error)
	// Restricted authority is a direct role assignment on the real system root.
	require.NoError(t, f.db.Table(f.table("iam_platform_administrators")).Where("principal_id = ?", f.adminA.ID).Delete(&struct{}{}).Error)
	_, err = service.Authenticate(t.Context(), "Bearer "+organizationView.Token, "")
	require.Error(t, err, "organization view must lose nonmember authority immediately when protected system authority is removed")
	var root struct{ ID guid.ID }
	require.NoError(t, f.db.Table(f.table("iam_tenants")).Select("id").Where("slug = ? AND scope_kind = ?", core.SystemRootSlug, "system").Take(&root).Error)
	roleID := guid.ID(987654321)
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, f.db.Table(f.table("iam_tenant_members")).Create(map[string]any{
		"tenant_id": root.ID, "principal_id": f.adminA.ID, "display_name": "System operator", "email": "", "status": "active", "created_at": now, "updated_at": now,
	}).Error)
	require.NoError(t, f.db.Table(f.table("iam_roles")).Create(map[string]any{"tenant_id": root.ID, "id": roleID, "name": "Account view operator", "name_key": "account view operator", "description": "", "created_at": now, "updated_at": now}).Error)
	permission := map[string]any{"tenant_id": root.ID, "role_id": roleID, "permission_code": "account_view_create", "condition_json": "", "created_at": now}
	member := map[string]any{"tenant_id": root.ID, "role_id": roleID, "principal_id": f.adminA.ID, "created_at": now}
	require.NoError(t, f.db.Table(f.table("iam_role_permissions")).Create(permission).Error)
	require.NoError(t, f.db.Table(f.table("iam_role_members")).Create(member).Error)
	_, err = service.Authenticate(ctx, "Bearer "+active.Token, "")
	require.NoError(t, err)
	require.True(t, t.Run("operator-role-permission-revoked-while-use-waits", func(t *testing.T) {
		f.accountViewUseAfterLockedMutation(t, service, active.Token, func(tx *gorm.DB) error {
			return tx.Table(f.table("iam_role_permissions")).Where("tenant_id = ? AND role_id = ? AND permission_code = ?", root.ID, roleID, "account_view_create").
				Delete(&struct{}{}).Error
		})
	}))
	require.NoError(t, f.db.Table(f.table("iam_role_permissions")).Create(permission).Error)
	_, err = service.Authenticate(ctx, "Bearer "+active.Token, "")
	require.NoError(t, err)
	require.True(t, t.Run("operator-role-member-revoked-while-use-waits", func(t *testing.T) {
		f.accountViewUseAfterLockedMutation(t, service, active.Token, func(tx *gorm.DB) error {
			return tx.Table(f.table("iam_role_members")).Where("tenant_id = ? AND role_id = ? AND principal_id = ?", root.ID, roleID, f.adminA.ID).Delete(&struct{}{}).Error
		})
	}))
	require.Equal(t, targetSessions, f.count(t, "iam_sessions", "principal_id = ?", f.requesterB.ID))
	require.Equal(t, operatorSessions, f.count(t, "iam_sessions", "principal_id = ?", f.adminA.ID))
	require.Equal(t, int64(1), f.count(t, "iam_account_view_tokens", "id = ? AND revoked_at IS NOT NULL", created.ID))
}

type accountViewLockProbeKey struct{}

func (f *iamTenantFixture) accountViewUseAfterLockedMutation(t *testing.T, service *iamauthn.Service, token string, mutate func(*gorm.DB) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	tx := f.db.WithContext(ctx).Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()
	var principal struct{ ID guid.ID }
	require.NoError(t, tx.Table(f.table("iam_principals")).Select("id").Where("id = ?", f.adminA.ID).
		Clauses(clause.Locking{Strength: "UPDATE"}).Take(&principal).Error)
	lockAttempted := make(chan struct{}, 1)
	const callback = "account_view_lock_probe"
	require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register(callback, func(db *gorm.DB) {
		if db.Statement.Context.Value(accountViewLockProbeKey{}) == true && db.Statement.Table == f.table("iam_principals") {
			if _, locked := db.Statement.Clauses["FOR"]; locked {
				select {
				case lockAttempted <- struct{}{}:
				default:
				}
			}
		}
	}))
	defer f.db.Callback().Query().Remove(callback)
	result := make(chan error, 1)
	go func() {
		_, err := service.Authenticate(context.WithValue(ctx, accountViewLockProbeKey{}, true), "Bearer "+token, "")
		result <- err
	}()
	select {
	case <-lockAttempted:
	case <-ctx.Done():
		t.Fatal("authentication did not attempt the principal row lock")
	}
	select {
	case err := <-result:
		t.Fatalf("authentication passed the held principal lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	require.NoError(t, mutate(tx))
	require.NoError(t, tx.Commit().Error)
	select {
	case err := <-result:
		require.ErrorIs(t, err, authnext.ErrInvalidCredentials, "committed revocation must invalidate the waiting use")
	case <-ctx.Done():
		t.Fatal("authentication did not resume after lock release")
	}
}
