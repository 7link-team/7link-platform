package iamintegration

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type grantabilityFenceProbeKey struct{}

func TestGrantabilityExternalDatabase(t *testing.T) {
	kind, dsn := strings.TrimSpace(os.Getenv(iamLifecycleDatabaseType)), strings.TrimSpace(os.Getenv(iamLifecycleDatabaseDSN))
	if kind == "" || dsn == "" {
		t.Skip("external IAM lifecycle database is not configured")
	}
	require.Contains(t, []string{"mysql", "postgres"}, kind)
	f := newIAMTenantFixtureWithDatabase(t, app.Database{Type: kind, Dsn: dsn, Writable: true, Readable: true, MaxOpenConns: 8, MaxIdleConns: 4}, os.Getenv(iamLifecycleTablePrefix))
	guard, err := module.Resolve[core.AdministratorGuard](f.app.Modules().Services())
	require.NoError(t, err)
	grantability := guard.(interface {
		RequireGrantableRoles(context.Context, *gorm.DB, guid.ID, []guid.ID) error
	})
	revisions, err := core.NewRevisionStore(f.app.Runtime())
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	actorRole, targetRole := guid.ID(987654310), guid.ID(987654311)
	for _, role := range []guid.ID{actorRole, targetRole} {
		require.NoError(t, f.db.Table(f.table("iam_roles")).Create(map[string]any{"tenant_id": iamTenantB, "id": role, "name": role.String(), "name_key": role.String(), "description": "", "created_at": now, "updated_at": now}).Error)
		require.NoError(t, f.db.Table(f.table("iam_role_permissions")).Create(map[string]any{"tenant_id": iamTenantB, "role_id": role, "permission_code": "menu_create", "condition_json": "", "created_at": now}).Error)
	}
	assignment := map[string]any{"tenant_id": iamTenantB, "role_id": actorRole, "principal_id": f.requesterB.ID, "created_at": now}
	require.NoError(t, f.db.Table(f.table("iam_role_members")).Create(assignment).Error)
	actorContext := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: f.requesterB.ID})
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return grantability.RequireGrantableRoles(actorContext, tx, iamTenantB, []guid.ID{targetRole})
	}))

	for _, revoke := range []string{"permission", "assignment", "membership"} {
		t.Run("actor-"+revoke+"-revoked-while-fence-waits", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			mutation := f.db.WithContext(ctx).Begin()
			require.NoError(t, mutation.Error)
			defer mutation.Rollback()
			if revoke == "membership" {
				var actor struct{ ID guid.ID }
				require.NoError(t, mutation.Table(f.table("iam_principals")).Select("id").Where("id = ?", f.requesterB.ID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&actor).Error)
			}
			require.NoError(t, revisions.Lock(ctx, mutation, iamTenantB))
			attempted := make(chan struct{}, 1)
			callback := "grantability_fence_probe"
			processor, before := f.db.Callback().Create(), "gorm:create"
			if revoke == "membership" {
				processor, before = f.db.Callback().Query(), "gorm:query"
			}
			require.NoError(t, processor.Before(before).Register(callback, func(db *gorm.DB) {
				matches := db.Statement.Table == f.table("iam_policy_revisions")
				if revoke == "membership" {
					_, locked := db.Statement.Clauses["FOR"]
					matches = locked && db.Statement.Table == f.table("iam_principals")
				}
				if db.Statement.Context.Value(grantabilityFenceProbeKey{}) == true && matches {
					select {
					case attempted <- struct{}{}:
					default:
					}
				}
			}))
			defer processor.Remove(callback)
			result := make(chan error, 1)
			go func() {
				readCtx := authn.WithClaims(context.WithValue(ctx, grantabilityFenceProbeKey{}, true), &authn.Claims{PrincipalID: f.requesterB.ID})
				result <- f.db.WithContext(readCtx).Transaction(func(tx *gorm.DB) error {
					var root struct{ ID guid.ID }
					if err := tx.Table(f.table("iam_tenants")).Select("id").Where("id = ?", iamTenantB).Take(&root).Error; err != nil {
						return err
					}
					return grantability.RequireGrantableRoles(readCtx, tx, iamTenantB, []guid.ID{targetRole})
				})
			}()
			waitForLocalFenceAttempt(t, ctx, attempted, result)
			switch revoke {
			case "permission":
				require.NoError(t, mutation.Table(f.table("iam_role_permissions")).Where("tenant_id = ? AND role_id = ? AND permission_code = ?", iamTenantB, actorRole, "menu_create").Delete(&struct{}{}).Error)
			case "assignment":
				require.NoError(t, mutation.Table(f.table("iam_role_members")).Where("tenant_id = ? AND role_id = ? AND principal_id = ?", iamTenantB, actorRole, f.requesterB.ID).Delete(&struct{}{}).Error)
			case "membership":
				require.NoError(t, mutation.Table(f.table("iam_tenant_members")).Where("tenant_id = ? AND principal_id = ?", iamTenantB, f.requesterB.ID).Update("status", "disabled").Error)
			}
			require.NoError(t, mutation.Commit().Error)
			select {
			case err := <-result:
				require.ErrorIs(t, err, core.ErrPrivilegeEscalation)
			case <-ctx.Done():
				t.Fatal("grantability did not resume after owner fence release")
			}
		})
		switch revoke {
		case "permission":
			require.NoError(t, f.db.Table(f.table("iam_role_permissions")).Create(map[string]any{"tenant_id": iamTenantB, "role_id": actorRole, "permission_code": "menu_create", "condition_json": "", "created_at": now}).Error)
		case "assignment":
			require.NoError(t, f.db.Table(f.table("iam_role_members")).Create(assignment).Error)
		case "membership":
			require.NoError(t, f.db.Table(f.table("iam_tenant_members")).Where("tenant_id = ? AND principal_id = ?", iamTenantB, f.requesterB.ID).Update("status", "active").Error)
		}
	}

	t.Run("system-administrator-guard-preserves-principal-lock-order", func(t *testing.T) {
		verifySystemAdministratorLockOrder(t, f)
	})
	t.Run("local-and-global-member-mutations-use-same-lock-order", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		headers := map[string]string{"Cookie": f.closureCookie(t, f.adminB), "Origin": "https://app.example.test"}
		mutation := f.db.WithContext(ctx).Begin()
		require.NoError(t, mutation.Error)
		defer mutation.Rollback()
		require.NoError(t, revisions.Lock(ctx, mutation, iamTenantB))
		attempted := make(chan struct{}, 1)
		callback := "grantability_lock_order_probe"
		require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register(callback, func(db *gorm.DB) {
			if db.Statement.Context.Value(grantabilityFenceProbeKey{}) == true && db.Statement.Table == f.table("iam_policy_revisions") {
				select {
				case attempted <- struct{}{}:
				default:
				}
			}
		}))
		defer f.db.Callback().Create().Remove(callback)
		localResult := make(chan error, 1)
		go func() {
			localCtx := authn.WithClaims(context.WithValue(ctx, grantabilityFenceProbeKey{}, true), &authn.Claims{PrincipalID: f.adminB.ID})
			localResult <- f.db.WithContext(localCtx).Transaction(func(tx *gorm.DB) error {
				return grantability.RequireGrantableRoles(localCtx, tx, iamTenantB, []guid.ID{targetRole})
			})
		}()
		waitForLocalFenceAttempt(t, ctx, attempted, localResult)
		globalResult := make(chan iamHTTPResponse, 1)
		go func() {
			globalResult <- f.request(t, http.MethodPatch, "/system/tenants/"+iamTenantB.String()+"/members/"+f.requesterB.ID.String(), 0, "", map[string]any{"display_name": "Managed requester"}, headers)
		}()
		select {
		case response := <-globalResult:
			t.Fatalf("global mutation passed actor principal fence: %d", response.StatusCode)
		case <-time.After(150 * time.Millisecond):
		}
		require.NoError(t, mutation.Commit().Error)
		select {
		case err := <-localResult:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal("local grantability deadlocked")
		}
		select {
		case response := <-globalResult:
			requireStatus(t, http.StatusOK, response)
		case <-ctx.Done():
			t.Fatal("global member mutation deadlocked")
		}
	})
}

func waitForLocalFenceAttempt(t *testing.T, ctx context.Context, attempted <-chan struct{}, result <-chan error) {
	t.Helper()
	select {
	case <-attempted:
	case <-ctx.Done():
		t.Fatal("grantability did not attempt the policy fence")
	}
	select {
	case err := <-result:
		t.Fatalf("grantability passed held owner fence: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
}
