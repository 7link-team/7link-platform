package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/policyx"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRoleSeederUsesCallerTransaction(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			h := newCoreServiceHarness(t)
			h.seedMember(t, 1, 11, MemberActive)
			require.NoError(t, h.db.Exec("CREATE TABLE seed_audit (event_type TEXT NOT NULL)").Error)
			h.service.writes.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
				return tx.WithContext(ctx).Exec("INSERT INTO seed_audit VALUES (?)", event.EventType).Error
			}
			before, err := h.repository.revisions.Current(t.Context(), h.db, 1)
			require.NoError(t, err)
			seeder := (&Module{service: h.service}).roleSeeder()
			abort := errors.New("ledger write failed")
			var role Role
			err = h.db.Transaction(func(tx *gorm.DB) error {
				var err error
				role, err = seeder.EnsureRoleOn(t.Context(), tx, 1, "delivery lead", "Delivery lead", "Project lead")
				require.NoError(t, err)
				changed, err := seeder.GrantPermissionOn(t.Context(), tx, 1, role.ID, "role_view")
				require.NoError(t, err)
				require.True(t, changed)
				changed, err = seeder.GrantPermissionOn(t.Context(), tx, 1, role.ID, "role_view")
				require.NoError(t, err)
				require.False(t, changed)
				if rollback {
					return abort
				}
				return nil
			})
			if rollback {
				require.ErrorIs(t, err, abort)
			} else {
				require.NoError(t, err)
			}
			roles, err := h.service.ListRoles(t.Context(), 1)
			require.NoError(t, err)
			var auditCount int64
			require.NoError(t, h.db.Table("seed_audit").Count(&auditCount).Error)
			after, err := h.repository.revisions.Current(t.Context(), h.db, 1)
			require.NoError(t, err)
			if rollback {
				require.Empty(t, roles)
				require.Zero(t, auditCount)
				require.Equal(t, before, after)
			} else {
				require.Len(t, roles, 1)
				codes, err := h.service.ListPermissions(t.Context(), 1, role.ID)
				require.NoError(t, err)
				require.Equal(t, []string{"role_view"}, codes)
				require.Positive(t, auditCount)
				require.Greater(t, after, before)
			}
		})
	}
}

func TestRoleSeederRejectsUnownedNamesAndInvalidReferences(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	h.seedMember(t, 2, 22, MemberActive)
	existing, err := h.service.CreateRole(t.Context(), 1, "Delivery lead", "User managed")
	require.NoError(t, err)
	seeder := (&Module{service: h.service}).roleSeeder()
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		conflicting, err := seeder.EnsureRoleOn(t.Context(), tx, 1, "delivery lead", " DELIVERY LEAD ", "Overwrite")
		require.ErrorIs(t, err, ErrRoleNameConflict)
		require.Equal(t, existing, conflicting, "ledger needs the actual conflicting role, without claiming ownership")
		_, err = seeder.EnsureRoleOn(t.Context(), tx, 1, "governance.role.engineer", "Engineer", "")
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = seeder.GrantPermissionOn(t.Context(), tx, 2, existing.ID, "role_view")
		require.ErrorIs(t, err, ErrRoleNotFound)
		_, err = seeder.GrantPermissionOn(t.Context(), tx, 1, existing.ID, "undeclared")
		require.ErrorIs(t, err, ErrPermissionNotFound)
		return nil
	}))
	stored, err := h.service.GetRole(t.Context(), 1, existing.ID)
	require.NoError(t, err)
	require.Equal(t, existing, stored)
	codes, err := h.service.ListPermissions(t.Context(), 1, existing.ID)
	require.NoError(t, err)
	require.Empty(t, codes)
}

func TestRoleSeederRequiresTransactionAndService(t *testing.T) {
	h := newCoreServiceHarness(t)
	for _, m := range []*Module{nil, {}, {service: h.service}} {
		s := m.roleSeeder()
		_, err := s.EnsureRoleOn(t.Context(), nil, 1, "lead", "Lead", "")
		require.ErrorIs(t, err, ErrUnavailable)
		_, err = s.GrantPermissionOn(t.Context(), nil, 1, 1, "role_view")
		require.ErrorIs(t, err, ErrUnavailable)
	}
	s := (&Module{service: h.service}).roleSeeder()
	_, err := s.EnsureRoleOn(t.Context(), h.db, guid.ID(1), "lead", "Lead", "")
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = s.GrantPermissionOn(t.Context(), h.db, 1, 1, "role_view")
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestRoleSeederAuditFailureRollsBackRoleAndRevision(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	before, err := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, err)
	failure := errors.New("audit persistence failed")
	h.service.writes.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	seeder := (&Module{service: h.service}).roleSeeder()
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, err := seeder.EnsureRoleOn(t.Context(), tx, 1, "engineer", "Engineer", "")
		return err
	})
	require.ErrorIs(t, err, failure)
	roles, err := h.service.ListRoles(t.Context(), 1)
	require.NoError(t, err)
	require.Empty(t, roles)
	after, err := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestRoleSeederConditionalGrantEnforcesRiskAndPreservesEdits(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	role, err := h.service.CreateRole(t.Context(), 1, "Engineer", "")
	require.NoError(t, err)
	seeder := (&Module{service: h.service}).roleSeeder()
	condition := json.RawMessage(`{"version":1,"in":[{"context":"resource.attr.risk_level"},{"value":["LOW","MEDIUM"]}]}`)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		changed, err := seeder.GrantConditionalPermissionOn(t.Context(), tx, 1, role.ID, "role_view", condition)
		require.True(t, changed)
		return err
	}))
	grants, err := h.service.ListPermissionGrants(t.Context(), 1, role.ID)
	require.NoError(t, err)
	require.Len(t, grants, 1)
	for _, risk := range []string{"LOW", "MEDIUM", "HIGH", ""} {
		allowed, err := policyx.EvaluateCondition(grants[0].Condition, policyx.TrustedContext{Resource: policyx.ResourceContext{Attrs: map[string]string{"risk_level": risk}}})
		require.NoError(t, err)
		require.Equal(t, risk == "LOW" || risk == "MEDIUM", allowed, risk)
	}
	narrow := json.RawMessage(`{"version":1,"eq":[{"context":"resource.attr.risk_level"},{"value":"LOW"}]}`)
	_, _, err = h.service.SetPermissionCondition(t.Context(), 1, role.ID, "role_view", narrow)
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		changed, err := seeder.GrantConditionalPermissionOn(t.Context(), tx, 1, role.ID, "role_view", condition)
		require.False(t, changed)
		return err
	}))
	grants, err = h.service.ListPermissionGrants(t.Context(), 1, role.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(narrow), string(grants[0].Condition), "seeding must not overwrite administrator conditions")
}

func TestRoleSeederConditionalGrantRejectsInvalidAndRollsBackConditionFailure(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	role, err := h.service.CreateRole(t.Context(), 1, "Engineer", "")
	require.NoError(t, err)
	seeder := (&Module{service: h.service}).roleSeeder()
	condition := json.RawMessage(`{"version":1,"eq":[{"context":"resource.attr.risk_level"},{"value":"LOW"}]}`)
	_, err = seeder.GrantConditionalPermissionOn(t.Context(), nil, 1, role.ID, "role_view", condition)
	require.ErrorIs(t, err, ErrUnavailable)
	for _, raw := range []string{"", "null", "{}", "broken"} {
		err = h.db.Transaction(func(tx *gorm.DB) error {
			_, err := seeder.GrantConditionalPermissionOn(t.Context(), tx, 1, role.ID, "role_view", json.RawMessage(raw))
			return err
		})
		require.ErrorIs(t, err, ErrInvalidRolePermissionCondition)
	}
	before, err := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, err)
	failure := errors.New("condition audit failed")
	h.service.writes.audit = func(_ context.Context, _ *gorm.DB, event audit.DomainEvent) error {
		if event.EventType == "role_permission_condition_updated" {
			return failure
		}
		return nil
	}
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, err := seeder.GrantConditionalPermissionOn(t.Context(), tx, 1, role.ID, "role_view", condition)
		return err
	})
	require.ErrorIs(t, err, failure)
	grants, err := h.service.ListPermissionGrants(t.Context(), 1, role.ID)
	require.NoError(t, err)
	require.Empty(t, grants)
	after, err := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestRoleSeederCopiesCurrentAdministratorsWithExpiry(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	for _, id := range []guid.ID{12, 13, 14} {
		h.seedAdditionalMember(t, 1, id, MemberActive)
	}
	admin, err := h.service.CreateRole(t.Context(), 1, administratorRoleName, "")
	require.NoError(t, err)
	_, err = h.service.GrantPermission(t.Context(), 1, admin.ID, tenantAdministerPermission)
	require.NoError(t, err)
	target, err := h.service.CreateRole(t.Context(), 1, "Team owner", "")
	require.NoError(t, err)
	expiry := h.repository.now().Add(time.Hour)
	for _, id := range []guid.ID{11, 12, 13, 14} {
		var end *time.Time
		if id == 12 || id == 13 {
			end = &expiry
		}
		_, err = h.service.AddMember(t.Context(), 1, admin.ID, id, end)
		require.NoError(t, err)
	}
	require.NoError(t, h.db.Exec("UPDATE sl_iam_role_members SET expires_at=? WHERE tenant_id=1 AND principal_id=13", h.repository.now().Add(-time.Hour)).Error)
	require.NoError(t, h.db.Exec("UPDATE sl_iam_tenant_members SET status='disabled' WHERE tenant_id=1 AND principal_id=14").Error)
	h.seedAdditionalMember(t, 1, 15, MemberActive)
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, PrincipalID: 15, TenantID: 1})
	_, err = h.service.GrantPermission(t.Context(), 1, target.ID, "role_view")
	require.NoError(t, err)
	_, err = h.service.AddMember(actor, 1, target.ID, 11, nil)
	require.ErrorIs(t, err, ErrPrivilegeEscalation)
	seeder := (&Module{service: h.service}).roleSeeder()
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		ids, err := seeder.SeedAdministratorMembersOn(actor, tx, 1, target.ID)
		require.Equal(t, []guid.ID{11, 12}, ids)
		return err
	}))
	grants, _, err := h.service.ListRoleMemberGrantsPage(t.Context(), 1, target.ID, 0, 100)
	require.NoError(t, err)
	require.Len(t, grants, 2)
	require.Nil(t, grants[0].ExpiresAt)
	require.NotNil(t, grants[1].ExpiresAt)
	require.True(t, grants[1].ExpiresAt.Equal(expiry))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		ids, err := seeder.SeedAdministratorMembersOn(actor, tx, 1, target.ID)
		require.Empty(t, ids)
		return err
	}))
	_, err = seeder.SeedAdministratorMembersOn(t.Context(), nil, 1, target.ID)
	require.ErrorIs(t, err, ErrUnavailable)
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, err := seeder.SeedAdministratorMembersOn(t.Context(), tx, 2, target.ID)
		return err
	})
	require.ErrorIs(t, err, ErrRoleNotFound)
}

func TestRoleSeederDoesNotTrustAdministratorDisplayName(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	target, err := h.service.CreateRole(t.Context(), 1, "Team owner", "")
	require.NoError(t, err)
	seeder := (&Module{service: h.service}).roleSeeder()
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		ids, err := seeder.SeedAdministratorMembersOn(t.Context(), tx, 1, target.ID)
		require.Empty(t, ids)
		return err
	}))
	admin, err := h.service.CreateRole(t.Context(), 1, administratorRoleName, "")
	require.NoError(t, err)
	_, err = h.service.AddMember(t.Context(), 1, admin.ID, 11, nil)
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		ids, err := seeder.SeedAdministratorMembersOn(t.Context(), tx, 1, target.ID)
		require.Empty(t, ids)
		return err
	}))
	members, err := h.service.ListMembers(t.Context(), 1, target.ID)
	require.NoError(t, err)
	require.Empty(t, members)
}

func TestRoleSeederAdministratorCopyAuditFailureRollsBack(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	admin, err := h.service.CreateRole(t.Context(), 1, administratorRoleName, "")
	require.NoError(t, err)
	_, err = h.service.GrantPermission(t.Context(), 1, admin.ID, tenantAdministerPermission)
	require.NoError(t, err)
	_, err = h.service.AddMember(t.Context(), 1, admin.ID, 11, nil)
	require.NoError(t, err)
	target, err := h.service.CreateRole(t.Context(), 1, "Team owner", "")
	require.NoError(t, err)
	before, err := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, err)
	failure := errors.New("membership audit failed")
	h.service.writes.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	seeder := (&Module{service: h.service}).roleSeeder()
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, err := seeder.SeedAdministratorMembersOn(t.Context(), tx, 1, target.ID)
		return err
	})
	require.ErrorIs(t, err, failure)
	members, err := h.service.ListMembers(t.Context(), 1, target.ID)
	require.NoError(t, err)
	require.Empty(t, members)
	after, err := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestRoleSeederSystemCapabilityRetainsInitiatorWithoutElevatingUserService(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			h := newCoreServiceHarness(t)
			h.seedMember(t, 1, 11, MemberActive)
			actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, PrincipalID: 11, TenantID: 1})
			role, err := h.service.CreateRole(actor, 1, "Engineer", "")
			require.NoError(t, err)
			_, err = h.service.GrantPermission(actor, 1, role.ID, "role_view")
			require.ErrorIs(t, err, ErrPrivilegeEscalation)
			require.NoError(t, h.db.Exec("CREATE TABLE seed_actor_audit (actor INTEGER, source TEXT)").Error)
			h.service.writes.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
				require.Equal(t, guid.ID(11), authn.PrincipalIDFromContext(ctx))
				require.Equal(t, guid.ID(11), event.PrincipalID)
				require.Equal(t, "role_seed", event.Detail["operation_source"])
				return tx.Exec("INSERT INTO seed_actor_audit VALUES (?,?)", event.PrincipalID, event.Detail["operation_source"]).Error
			}
			before, err := h.repository.revisions.Current(t.Context(), h.db, 1)
			require.NoError(t, err)
			seeder := (&Module{service: h.service}).roleSeeder()
			abort := errors.New("seed ledger failed")
			condition := json.RawMessage(`{"version":1,"eq":[{"context":"resource.attr.risk_level"},{"value":"LOW"}]}`)
			err = h.db.Transaction(func(tx *gorm.DB) error {
				changed, err := seeder.GrantConditionalPermissionOn(actor, tx, 1, role.ID, "role_view", condition)
				require.NoError(t, err)
				require.True(t, changed)
				changed, err = seeder.GrantPermissionOn(actor, tx, 1, role.ID, "todo_view")
				require.NoError(t, err)
				require.True(t, changed)
				if rollback {
					return abort
				}
				return nil
			})
			if rollback {
				require.ErrorIs(t, err, abort)
			} else {
				require.NoError(t, err)
			}
			require.False(t, h.service.seedRoleWrites)
			_, err = h.service.GrantPermission(actor, 1, role.ID, "role_create")
			require.ErrorIs(t, err, ErrPrivilegeEscalation)
			grants, err := h.service.ListPermissionGrants(t.Context(), 1, role.ID)
			require.NoError(t, err)
			after, err := h.repository.revisions.Current(t.Context(), h.db, 1)
			require.NoError(t, err)
			var count int64
			require.NoError(t, h.db.Table("seed_actor_audit").Count(&count).Error)
			if rollback {
				require.Empty(t, grants)
				require.Zero(t, count)
				require.Equal(t, before, after)
			} else {
				require.Len(t, grants, 2)
				require.EqualValues(t, 3, count)
				require.Greater(t, after, before)
				_, _, err = h.service.SetPermissionCondition(actor, 1, role.ID, "role_view", nil)
				require.ErrorIs(t, err, ErrPrivilegeEscalation)
			}
		})
	}
}
