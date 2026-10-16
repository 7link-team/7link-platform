package governance

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedHarness(t *testing.T) (*seedService, *gorm.DB) {
	t.Helper()
	s, db, _ := seedHarnessRuntime(t)
	return s, db
}

func seedHarnessRuntime(t *testing.T) (*seedService, *gorm.DB, *appenv.Runtime) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "seed.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	db := router.Write()
	raw, err := db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = "g_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	for _, sql := range []string{
		"CREATE TABLE roles_probe (id INTEGER PRIMARY KEY, tenant_id INTEGER, name TEXT, UNIQUE(tenant_id,name))",
		"CREATE TABLE grants_probe (tenant_id INTEGER, role_id INTEGER, code TEXT, UNIQUE(tenant_id,role_id,code))",
		"CREATE TABLE audit_probe (detail TEXT)",
	} {
		require.NoError(t, db.Exec(sql).Error)
	}
	ids, err := guid.New(1)
	require.NoError(t, err)
	s := &seedService{names: query.BindNames("g_"), ids: ids, registry: authz.DefaultRegistry(), now: func() time.Time { return time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC) }}
	s.roles = core.RoleSeeder{
		EnsureRoleOn: func(ctx context.Context, tx *gorm.DB, tenant guid.ID, key, name, description string) (core.Role, error) {
			var old struct{ ID guid.ID }
			result := tx.WithContext(ctx).Table("roles_probe").Select("id").Where("tenant_id = ? AND name = ?", tenant, key).Take(&old)
			if result.Error == nil {
				return core.Role{ID: old.ID}, core.ErrRoleNameConflict
			}
			if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return core.Role{}, result.Error
			}
			id, err := ids.Next()
			if err != nil {
				return core.Role{}, err
			}
			return core.Role{ID: guid.ID(id)}, tx.WithContext(ctx).Exec("INSERT INTO roles_probe VALUES (?,?,?)", id, tenant, key).Error
		},
		GrantPermissionOn: func(ctx context.Context, tx *gorm.DB, tenant, role guid.ID, code string) (bool, error) {
			result := tx.WithContext(ctx).Exec("INSERT OR IGNORE INTO grants_probe VALUES (?,?,?)", tenant, role, code)
			return result.RowsAffected > 0, result.Error
		},
	}
	s.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		require.Equal(t, "governance.role_seed_applied", event.EventType)
		data, err := json.Marshal(event.Detail)
		require.NoError(t, err)
		return tx.WithContext(ctx).Exec("INSERT INTO audit_probe VALUES (?)", string(data)).Error
	}
	return s, db, appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix("g_"))
}
func seedCount(t *testing.T, db *gorm.DB, table string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Table(table).Count(&count).Error)
	return count
}
func applySeed(t *testing.T, s *seedService, db *gorm.DB, version int, manifest []roleManifestEntry) {
	t.Helper()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return s.applyOn(t.Context(), tx, 1, version, manifest) }))
}

func TestSeedRepeatedRunAndRevocationPreserved(t *testing.T) {
	s, db := seedHarness(t)
	manifest := []roleManifestEntry{{Key: "delivery_lead", Name: "Delivery lead", Codes: []string{"role_view", "user_view", "role_view"}}}
	applySeed(t, s, db, 1, manifest)
	require.EqualValues(t, 1, seedCount(t, db, "roles_probe"))
	require.EqualValues(t, 2, seedCount(t, db, "grants_probe"))
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
	applySeed(t, s, db, 1, manifest)
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
	require.NoError(t, db.Exec("DELETE FROM grants_probe WHERE code='role_view'").Error)
	applySeed(t, s, db, 1, manifest)
	require.EqualValues(t, 1, seedCount(t, db, "grants_probe"), "manual revocations must not be restored")
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
	manifest[0].Codes = []string{"role_view", "user_view", "role_create"}
	applySeed(t, s, db, 2, manifest)
	require.EqualValues(t, 2, seedCount(t, db, "grants_probe"))
	require.EqualValues(t, 2, seedCount(t, db, "audit_probe"))
	var row model.RoleSeed
	require.NoError(t, db.Table(s.names.RoleSeeds.String()).Take(&row).Error)
	require.JSONEq(t, `["role_create","role_view","user_view"]`, row.AppliedCodes)
	require.Equal(t, 2, row.ManifestVersion)
	require.Equal(t, 2, row.Version)
	// Removing a manifest permission does not revoke previously applied grants.
	manifest[0].Codes = []string{"role_view"}
	applySeed(t, s, db, 3, manifest)
	require.EqualValues(t, 2, seedCount(t, db, "grants_probe"))
}

func TestSeedUnavailablePermissionsAddedWhenEnabled(t *testing.T) {
	s, db := seedHarness(t)
	manifest := []roleManifestEntry{{Key: "engineer", Name: "Engineer", Codes: []string{"role_view", "future_read"}}}
	applySeed(t, s, db, 1, manifest)
	var row model.RoleSeed
	require.NoError(t, db.Table(s.names.RoleSeeds.String()).Take(&row).Error)
	require.JSONEq(t, `["future_read"]`, row.SkippedCodes)
	require.NoError(t, s.registry.Contribute([]authz.Action{{Code: "future_read", Resource: "future", Verb: "read", Scope: authz.ActionScopeTenant}}, nil))
	applySeed(t, s, db, 1, manifest)
	require.EqualValues(t, 2, seedCount(t, db, "grants_probe"))
	require.NoError(t, db.Table(s.names.RoleSeeds.String()).Take(&row).Error)
	require.JSONEq(t, `[]`, row.SkippedCodes)
	require.JSONEq(t, `["future_read","role_view"]`, row.AppliedCodes)
}

func TestSeedUnmanagedAndConflictingRolesNotChanged(t *testing.T) {
	s, db := seedHarness(t)
	require.NoError(t, db.Exec("INSERT INTO roles_probe VALUES (42,1,'engineer')").Error)
	manifest := []roleManifestEntry{{Key: "engineer", Name: "Engineer", Codes: []string{"role_view"}}}
	applySeed(t, s, db, 1, manifest)
	var row model.RoleSeed
	require.NoError(t, db.Table(s.names.RoleSeeds.String()).Take(&row).Error)
	require.True(t, row.SeedConflict)
	require.False(t, row.Managed)
	require.EqualValues(t, 42, row.RoleID)
	require.Zero(t, seedCount(t, db, "grants_probe"))
	applySeed(t, s, db, 2, manifest)
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
	require.NoError(t, db.Exec("UPDATE g_governance_role_seeds SET seed_conflict=false").Error)
	applySeed(t, s, db, 2, manifest)
	require.Zero(t, seedCount(t, db, "grants_probe"), "detached roles must not upgrade")
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
}

func TestSeedAuditFailureRollsBackAllWrites(t *testing.T) {
	s, db := seedHarness(t)
	failure := errors.New("audit failed")
	s.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	err := db.Transaction(func(tx *gorm.DB) error {
		return s.applyOn(t.Context(), tx, 1, 1, []roleManifestEntry{{Key: "engineer", Name: "Engineer", Codes: []string{"role_view"}}})
	})
	require.ErrorIs(t, err, failure)
	for _, table := range []string{"roles_probe", "grants_probe", "g_governance_role_seeds", "audit_probe"} {
		require.Zero(t, seedCount(t, db, table))
	}
}

func TestSeedRejectsInvalidTransactionAndLedger(t *testing.T) {
	s, db := seedHarness(t)
	require.ErrorIs(t, s.applyOn(t.Context(), db, 1, 1, nil), ErrInvalidArgument)
	require.ErrorIs(t, s.applyOn(t.Context(), nil, 1, 1, nil), ErrInvalidArgument)
	require.ErrorIs(t, (*seedService)(nil).applyOn(t.Context(), db, 1, 1, nil), ErrUnavailable)
	for _, raw := range []string{"null", "{}", "not-json", "[42]"} {
		_, err := decodeSeedCodes(raw)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, s.applyOn(t.Context(), tx, 0, 1, nil), ErrInvalidArgument)
		require.ErrorIs(t, s.applyOn(t.Context(), tx, 1, 0, nil), ErrInvalidArgument)
		require.ErrorIs(t, s.applyOn(t.Context(), tx, 1, 1, []roleManifestEntry{{}}), ErrInvalidArgument)
		return nil
	}))
}

type seedTestIDs struct {
	id  int64
	err error
}

func (s seedTestIDs) Next() (int64, error) { return s.id, s.err }

func TestSeedDependencyFailuresRollbackWithoutLedger(t *testing.T) {
	for _, scenario := range []string{"role", "empty_role", "collision_during_insert", "id", "zero_id", "grant"} {
		t.Run(scenario, func(t *testing.T) {
			s, db := seedHarness(t)
			failure := errors.New("dependency failed")
			expected := failure
			switch scenario {
			case "role":
				s.roles.EnsureRoleOn = func(context.Context, *gorm.DB, guid.ID, string, string, string) (core.Role, error) {
					return core.Role{}, failure
				}
			case "empty_role":
				s.roles.EnsureRoleOn = func(context.Context, *gorm.DB, guid.ID, string, string, string) (core.Role, error) {
					return core.Role{}, nil
				}
				expected = ErrUnavailable
			case "collision_during_insert":
				s.roles.EnsureRoleOn = func(context.Context, *gorm.DB, guid.ID, string, string, string) (core.Role, error) {
					return core.Role{}, core.ErrRoleNameConflict
				}
				expected = core.ErrRoleNameConflict
			case "id":
				s.ids = seedTestIDs{err: failure}
			case "zero_id":
				s.ids = seedTestIDs{}
				expected = ErrUnavailable
			case "grant":
				s.roles.GrantPermissionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (bool, error) { return false, failure }
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				return s.applyOn(t.Context(), tx, 1, 1, []roleManifestEntry{{Key: "engineer", Name: "Engineer", Codes: []string{"role_view"}}})
			})
			require.ErrorIs(t, err, expected)
			for _, table := range []string{"roles_probe", "grants_probe", "g_governance_role_seeds", "audit_probe"} {
				require.Zero(t, seedCount(t, db, table))
			}
		})
	}
}

func TestSeedCorruptLedgerAndMissingTableFailClosed(t *testing.T) {
	s, db := seedHarness(t)
	manifest := []roleManifestEntry{{Key: "engineer", Name: "Engineer", Codes: []string{"role_view"}}}
	applySeed(t, s, db, 1, manifest)
	require.NoError(t, db.Exec("UPDATE g_governance_role_seeds SET applied_codes='[42]'").Error)
	err := db.Transaction(func(tx *gorm.DB) error { return s.applyOn(t.Context(), tx, 1, 2, manifest) })
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
	require.NoError(t, db.Exec("DROP TABLE g_governance_role_seeds").Error)
	err = db.Transaction(func(tx *gorm.DB) error { return s.applyOn(t.Context(), tx, 1, 2, manifest) })
	require.Error(t, err)
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
}

func TestSeedTenantIsolation(t *testing.T) {
	s, db := seedHarness(t)
	manifest := []roleManifestEntry{{Key: "engineer", Name: "Engineer", Codes: []string{"role_view"}}}
	applySeed(t, s, db, 1, manifest)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return s.applyOn(t.Context(), tx, 2, 1, manifest) }))
	require.EqualValues(t, 2, seedCount(t, db, "roles_probe"))
	require.EqualValues(t, 2, seedCount(t, db, "g_governance_role_seeds"))
	var rows []model.RoleSeed
	require.NoError(t, db.Table(s.names.RoleSeeds.String()).Order("tenant_id").Find(&rows).Error)
	require.NotEqual(t, rows[0].RoleID, rows[1].RoleID)
	require.NoError(t, db.Exec("DELETE FROM grants_probe WHERE tenant_id=1").Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return s.applyOn(t.Context(), tx, 2, 1, manifest) }))
	require.EqualValues(t, 1, seedCount(t, db, "grants_probe"))
	require.EqualValues(t, 2, seedCount(t, db, "audit_probe"))
}

func TestSeedLaterRoleFailureRollsBackEarlierRole(t *testing.T) {
	s, db := seedHarness(t)
	original := s.roles.EnsureRoleOn
	failure := errors.New("second role creation failed")
	s.roles.EnsureRoleOn = func(ctx context.Context, tx *gorm.DB, tenant guid.ID, key, name, description string) (core.Role, error) {
		if key == "engineer" {
			return core.Role{}, failure
		}
		return original(ctx, tx, tenant, key, name, description)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return s.applyOn(t.Context(), tx, 1, 1, []roleManifestEntry{
			{Key: "delivery_lead", Name: "Delivery lead", Codes: []string{"role_view"}},
			{Key: "engineer", Name: "Engineer", Codes: []string{"user_view"}},
		})
	})
	require.ErrorIs(t, err, failure)
	for _, table := range []string{"roles_probe", "grants_probe", "g_governance_role_seeds", "audit_probe"} {
		require.Zero(t, seedCount(t, db, table))
	}
}

func seedLocks(t *testing.T, s *seedService, db *gorm.DB) *dlock.Locker {
	t.Helper()
	raw, err := db.DB()
	require.NoError(t, err)
	source := (&dlock.Module{}).MigrationSource()
	source.Prefix = "g_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	locker := dlock.NewLocker(db, dlock.WithTablePrefix("g_"), dlock.WithTTL(time.Minute))
	s.acquire = func(ctx context.Context, key string, ttl time.Duration) (*dlock.Lock, error) {
		require.Equal(t, time.Minute, ttl)
		return locker.TryLock(ctx, key)
	}
	return locker
}

func TestSeedLockedEntrySerializesAndReleases(t *testing.T) {
	s, db := seedHarness(t)
	locker := seedLocks(t, s, db)
	manifest := []roleManifestEntry{{Key: "engineer", Name: "Engineer", Codes: []string{"role_view"}}}
	held, err := locker.TryLock(t.Context(), "governance:seed:1")
	require.NoError(t, err)
	require.ErrorIs(t, s.apply(t.Context(), db, 1, 1, manifest), dlock.ErrNotAcquired)
	require.Zero(t, seedCount(t, db, "roles_probe"))
	require.NoError(t, held.Unlock(t.Context()))
	require.NoError(t, s.apply(t.Context(), db, 1, 1, manifest))
	require.NoError(t, s.apply(t.Context(), db, 1, 1, manifest))
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
	next, err := locker.TryLock(t.Context(), "governance:seed:1")
	require.NoError(t, err)
	require.NoError(t, next.Unlock(t.Context()))
	failure := errors.New("audit failed")
	s.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, s.apply(t.Context(), db, 2, 1, manifest), failure)
	require.EqualValues(t, 1, seedCount(t, db, "roles_probe"))
	next, err = locker.TryLock(t.Context(), "governance:seed:2")
	require.NoError(t, err)
	require.NoError(t, next.Unlock(t.Context()))
}

func TestSeedLockedEntryRejectsMissingOrLostLease(t *testing.T) {
	s, db := seedHarness(t)
	require.ErrorIs(t, s.apply(t.Context(), db, 1, 1, nil), ErrUnavailable)
	require.ErrorIs(t, (*seedService)(nil).apply(t.Context(), db, 1, 1, nil), ErrUnavailable)
	locker := seedLocks(t, s, db)
	require.ErrorIs(t, s.apply(t.Context(), nil, 1, 1, nil), ErrUnavailable)
	require.ErrorIs(t, s.apply(t.Context(), db, 0, 1, nil), ErrInvalidArgument)
	s.acquire = func(context.Context, string, time.Duration) (*dlock.Lock, error) { return nil, nil }
	require.ErrorIs(t, s.apply(t.Context(), db, 1, 1, nil), ErrUnavailable)
	stale, err := locker.TryLock(t.Context(), "governance:seed:1")
	require.NoError(t, err)
	require.NoError(t, stale.Unlock(t.Context()))
	s.acquire = func(context.Context, string, time.Duration) (*dlock.Lock, error) { return stale, nil }
	require.ErrorIs(t, s.apply(t.Context(), db, 1, 1, []roleManifestEntry{{Key: "engineer", Name: "Engineer"}}), dlock.ErrNotAcquired)
	require.Zero(t, seedCount(t, db, "roles_probe"))
}

func TestSeedConditionalGrantNeverFallsBackToUnrestricted(t *testing.T) {
	s, db := seedHarness(t)
	condition := json.RawMessage(`{"version":1,"in":[{"context":"resource.attr.risk_level"},{"value":["LOW","MEDIUM"]}]}`)
	manifest := []roleManifestEntry{{Key: "engineer", Name: "Engineer", Codes: []string{"role_view"}, Conditions: map[string]json.RawMessage{"role_view": condition}}}
	err := db.Transaction(func(tx *gorm.DB) error { return s.applyOn(t.Context(), tx, 1, 1, manifest) })
	require.ErrorIs(t, err, ErrUnavailable)
	require.Zero(t, seedCount(t, db, "roles_probe"))
	require.Zero(t, seedCount(t, db, "grants_probe"))
	calls := 0
	grant := s.roles.GrantPermissionOn
	s.roles.GrantPermissionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (bool, error) {
		t.Fatal("conditional grant used unrestricted path")
		return false, nil
	}
	s.roles.GrantConditionalPermissionOn = func(ctx context.Context, tx *gorm.DB, tenant, role guid.ID, code string, raw json.RawMessage) (bool, error) {
		calls++
		require.JSONEq(t, string(condition), string(raw))
		return grant(ctx, tx, tenant, role, code)
	}
	applySeed(t, s, db, 1, manifest)
	applySeed(t, s, db, 1, manifest)
	require.Equal(t, 1, calls)
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
}

func TestSeedTeamOwnerMembersAreCopiedOnlyAtFirstCreation(t *testing.T) {
	s, db := seedHarness(t)
	require.NoError(t, db.Exec("CREATE TABLE owner_members_probe (role_id INTEGER, principal_id INTEGER)").Error)
	calls := 0
	s.roles.SeedAdministratorMembersOn = func(ctx context.Context, tx *gorm.DB, tenant, role guid.ID) ([]guid.ID, error) {
		calls++
		return []guid.ID{11}, tx.WithContext(ctx).Exec("INSERT INTO owner_members_probe VALUES (?,11)", role).Error
	}
	manifest := []roleManifestEntry{{Key: "team_owner", Name: "Team owner", Codes: []string{"role_view"}}}
	applySeed(t, s, db, 1, manifest)
	require.EqualValues(t, 1, seedCount(t, db, "owner_members_probe"))
	require.NoError(t, db.Exec("DELETE FROM owner_members_probe").Error)
	applySeed(t, s, db, 2, manifest)
	require.Equal(t, 1, calls)
	require.Zero(t, seedCount(t, db, "owner_members_probe"), "later seed upgrades must not re-add removed owners")
}

func TestSeedTeamOwnerMembershipFailureRollsBack(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing", false: "failed"}[missing], func(t *testing.T) {
			s, db := seedHarness(t)
			failure := errors.New("member initialization failed")
			expected := ErrUnavailable
			if !missing {
				expected = failure
				s.roles.SeedAdministratorMembersOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, failure }
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				return s.applyOn(t.Context(), tx, 1, 1, []roleManifestEntry{{Key: "team_owner", Name: "Team owner", Codes: []string{"role_view"}}})
			})
			require.ErrorIs(t, err, expected)
			for _, table := range []string{"roles_probe", "grants_probe", "g_governance_role_seeds", "audit_probe"} {
				require.Zero(t, seedCount(t, db, table))
			}
		})
	}
}
