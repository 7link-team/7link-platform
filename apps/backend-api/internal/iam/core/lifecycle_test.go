package core

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var coreErasureTime = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

func TestCoreErasePrincipalDataRefusesUnusableArguments(t *testing.T) {
	harness := newRepositoryHarness(t, "")

	assert.ErrorContains(t, ErasePrincipalData(t.Context(), nil, "", authz.DefaultRegistry(), 1, coreErasureTime), "invalid principal erasure transaction")
	assert.ErrorContains(t, ErasePrincipalData(t.Context(), harness.db, "", authz.DefaultRegistry(), 0, coreErasureTime), "invalid principal erasure transaction")

	// The prefix becomes an identifier in every statement below, so a rejected
	// one must never reach the database.
	assert.Error(t, ErasePrincipalData(t.Context(), harness.db, "bad prefix; DROP TABLE x;--", authz.DefaultRegistry(), 1, coreErasureTime))
}

func TestCoreErasePrincipalDataRedactsMembershipsAndSparesEveryoneElse(t *testing.T) {
	for _, prefix := range []string{"", "sl_"} {
		t.Run(corePrefixName(prefix), func(t *testing.T) {
			harness := newRepositoryHarness(t, prefix)
			const target, bystander = guid.ID(11), guid.ID(12)
			const tenantA, tenantB = guid.ID(101), guid.ID(102)
			seedCoreMembership(t, harness, prefix, tenantA, target, "Target Person", "target@example.test")
			seedCoreMembership(t, harness, prefix, tenantB, target, "Target Person", "target@example.test")
			seedCoreMembership(t, harness, prefix, tenantA, bystander, "Other Person", "other@example.test")

			revisions, err := newRevisionStoreForPrefix(prefix)
			require.NoError(t, err)
			before := map[guid.ID]int64{}
			for _, tenant := range []guid.ID{tenantA, tenantB} {
				before[tenant], err = revisions.Current(t.Context(), harness.db, tenant)
				require.NoError(t, err)
			}

			require.NoError(t, ErasePrincipalData(t.Context(), harness.db, prefix, authz.DefaultRegistry(), target, coreErasureTime))

			// Redacting the row is only half of it. Authorization answers are
			// cached against the tenant revision, so a membership that stops
			// counting has to move the revision or the cache keeps admitting it.
			for _, tenant := range []guid.ID{tenantA, tenantB} {
				current, currentErr := revisions.Current(t.Context(), harness.db, tenant)
				require.NoError(t, currentErr)
				assert.Greater(t, current, before[tenant], "tenant %d must see a new policy revision", tenant)
			}

			// Every membership the principal held is redacted, not deleted: the
			// row is what stops the same admission being silently recreated.
			for _, tenant := range []guid.ID{tenantA, tenantB} {
				name, email, status := readCoreMembership(t, harness, prefix, tenant, target)
				assert.Equal(t, "Anonymized account", name)
				assert.Empty(t, email)
				assert.Equal(t, "disabled", status)
			}

			name, email, status := readCoreMembership(t, harness, prefix, tenantA, bystander)
			assert.Equal(t, "Other Person", name, "an unrelated member keeps their profile")
			assert.Equal(t, "other@example.test", email)
			assert.Equal(t, "active", status)
		})
	}
}

func TestCoreErasePrincipalDataRefusesWhilePlatformAccessRemains(t *testing.T) {
	for _, table := range []string{"iam_platform_administrators", "iam_platform_grants"} {
		t.Run(table, func(t *testing.T) {
			harness := newRepositoryHarness(t, "")
			const target = guid.ID(21)
			seedCoreMembership(t, harness, "", guid.ID(201), target, "Target Person", "target@example.test")
			if table == "iam_platform_administrators" {
				require.NoError(t, harness.db.Exec(
					"INSERT INTO iam_platform_administrators (principal_id, created_at) VALUES (?, ?)",
					target, coreErasureTime).Error)
			} else {
				require.NoError(t, harness.db.Exec(
					"INSERT INTO iam_platform_grants (principal_id, permission_code, created_at) VALUES (?, ?, ?)",
					target, "iam.platform.read", coreErasureTime).Error)
			}

			// Anonymizing someone who still holds platform authority would leave
			// the grant pointing at a principal nobody can identify any more, so
			// the existing removal workflow has to run first.
			assert.ErrorIs(t, ErasePrincipalData(t.Context(), harness.db, "", authz.DefaultRegistry(), target, coreErasureTime), ErrPrincipalHasPlatformAccess)

			name, _, status := readCoreMembership(t, harness, "", guid.ID(201), target)
			assert.Equal(t, "Target Person", name, "a refused erasure must change nothing")
			assert.Equal(t, "active", status)
		})
	}
}

func TestPrincipalLifecycleRequiresSystemRoleRevocation(t *testing.T) {
	// system_file_read is not in the core catalog: a module contributes it to
	// the shared registry, and the check must see it through that registry.
	registry := authz.DefaultRegistry()
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "system_file", Verb: "read", Scope: authz.ActionScopeSystem}}, nil))
	for _, code := range []string{"system_account_view", "system_file_read"} {
		t.Run(code, func(t *testing.T) {
			h := newRepositoryHarness(t, "sl_")
			seedCoreMembership(t, h, "sl_", 201, 21, "Target", "target@example.test")
			role, err := h.repository.CreateRole(t.Context(), 201, "System reader", "")
			require.NoError(t, err)
			require.NoError(t, h.db.Exec("INSERT INTO sl_iam_role_members (tenant_id, role_id, principal_id, created_at) VALUES (201, ?, 21, ?)", role.ID, coreErasureTime).Error)
			require.NoError(t, h.db.Exec("INSERT INTO sl_iam_role_permissions (tenant_id, role_id, permission_code, created_at) VALUES (201, ?, ?, ?)", role.ID, code, coreErasureTime).Error)
			require.ErrorIs(t, requireNoPrincipalPlatformAccess(t.Context(), h.db, "sl_", 21, registry, coreErasureTime), ErrPrincipalHasPlatformAccess)
			require.ErrorIs(t, ErasePrincipalData(t.Context(), h.db, "sl_", registry, 21, coreErasureTime), ErrPrincipalHasPlatformAccess)
			require.NoError(t, h.db.Exec("DELETE FROM sl_iam_role_members WHERE principal_id = 21").Error)
			require.NoError(t, requireNoPrincipalPlatformAccess(t.Context(), h.db, "sl_", 21, registry, coreErasureTime))
		})
	}
}

func TestPrincipalLifecycleProtectsModuleDeclaredGlobalRolePermissions(t *testing.T) {
	h := newRepositoryHarness(t, "sl_")
	seedCoreMembership(t, h, "sl_", 201, 21, "Target", "target@example.test")
	role, err := h.repository.CreateRole(t.Context(), 201, "Extension operator", "")
	require.NoError(t, err)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_role_members (tenant_id, role_id, principal_id, created_at) VALUES (201, ?, 21, ?)", role.ID, coreErasureTime).Error)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_role_permissions (tenant_id, role_id, permission_code, created_at) VALUES (201, ?, 'extension_manage', ?)", role.ID, coreErasureTime).Error)
	registry := authz.DefaultRegistry()
	require.NoError(t, requireNoPrincipalPlatformAccess(t.Context(), h.db, "sl_", 21, registry, coreErasureTime),
		"a code nobody contributed is an ordinary tenant grant")
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "extension", Verb: "manage", Scope: authz.ActionScopeSystem}}, nil))
	require.ErrorIs(t, requireNoPrincipalPlatformAccess(t.Context(), h.db, "sl_", 21, registry, coreErasureTime), ErrPrincipalHasPlatformAccess)
	require.ErrorIs(t, ErasePrincipalData(t.Context(), h.db, "sl_", registry, 21, coreErasureTime), ErrPrincipalHasPlatformAccess)
}

func TestPrincipalLifecycleRequiresTheSharedRegistry(t *testing.T) {
	h := newRepositoryHarness(t, "sl_")
	require.ErrorIs(t, requireNoPrincipalPlatformAccess(t.Context(), h.db, "sl_", 21, nil, coreErasureTime), ErrUnavailable)
	require.ErrorIs(t, ErasePrincipalData(t.Context(), h.db, "sl_", nil, 21, coreErasureTime), ErrUnavailable)
}

func seedCoreMembership(t *testing.T, harness repositoryHarness, prefix string, tenant, principal guid.ID, name, email string) {
	t.Helper()
	require.NoError(t, harness.db.Exec(
		"INSERT INTO "+prefix+"iam_tenant_members (tenant_id, principal_id, display_name, email, status, created_at, updated_at)"+
			" VALUES (?, ?, ?, ?, 'active', ?, ?)",
		tenant, principal, name, email, coreErasureTime, coreErasureTime,
	).Error)
}

func readCoreMembership(t *testing.T, harness repositoryHarness, prefix string, tenant, principal guid.ID) (string, string, string) {
	t.Helper()
	var row struct {
		DisplayName string
		Email       string
		Status      string
	}
	require.NoError(t, harness.db.Table(prefix+"iam_tenant_members").
		Select("display_name, email, status").
		Where("tenant_id = ? AND principal_id = ?", tenant, principal).
		Scan(&row).Error)
	return row.DisplayName, row.Email, row.Status
}

func corePrefixName(prefix string) string {
	if prefix == "" {
		return "plain"
	}
	return "prefixed"
}

func TestCoreErasePrincipalDataLeavesUnrelatedTenantsAlone(t *testing.T) {
	harness := newRepositoryHarness(t, "")
	target := guid.ID(41)
	joined, untouched := guid.ID(401), guid.ID(402)
	seedCoreMembership(t, harness, "", joined, target, "Target Person", "target@example.test")
	seedCoreMembership(t, harness, "", untouched, guid.ID(42), "Other Person", "other@example.test")

	revisions, err := newRevisionStoreForPrefix("")
	require.NoError(t, err)
	beforeUntouched, err := revisions.Current(t.Context(), harness.db, untouched)
	require.NoError(t, err)

	require.NoError(t, ErasePrincipalData(t.Context(), harness.db, "", authz.DefaultRegistry(), target, coreErasureTime))

	// Bumping a revision invalidates every cached authorization answer for that
	// tenant, so bumping one the principal never belonged to would cost a
	// re-evaluation storm for nothing.
	afterUntouched, err := revisions.Current(t.Context(), harness.db, untouched)
	require.NoError(t, err)
	assert.Equal(t, beforeUntouched, afterUntouched, "a tenant the principal never joined keeps its revision")
}

func TestCoreErasePrincipalDataAcceptsAPrincipalWithNoMemberships(t *testing.T) {
	harness := newRepositoryHarness(t, "")

	// Someone can be suspended and erased before ever joining a tenant. That is
	// nothing to redact rather than an error, or erasure would stall on the
	// accounts with the least data to remove.
	require.NoError(t, ErasePrincipalData(t.Context(), harness.db, "", authz.DefaultRegistry(), guid.ID(51), coreErasureTime))
}
