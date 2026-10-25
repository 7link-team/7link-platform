package governance

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func reviewerRolesHarness(t *testing.T) (*Module, *gorm.DB, context.Context) {
	t.Helper()
	m, db, ctx, _, _ := delegationHarness(t)
	seedLocks(t, m.seed, db)
	store := idempotency.NewModule(m.runtime)
	s := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](s, m.seed.ids))
	require.NoError(t, s.Seal())
	require.NoError(t, store.InjectServices(s))
	source := store.MigrationSource()
	source.Prefix = "g_"
	raw, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	m.delegations.replay = store
	return m, db, ctx
}

func TestJReviewerRolesExplicitIdempotentAndRevocationPreserving(t *testing.T) {
	m, db, ctx := reviewerRolesHarness(t)
	in := &reviewerRolesInput{TenantID: "1", IdempotencyKey: "reviewer-roles-first"}
	first, err := m.provisionReviewerRolesHTTP(ctx, in)
	require.NoError(t, err)
	require.Len(t, first.Body.Data, 2)
	require.Equal(t, "agent_tester", first.Body.Data[0].Key)
	require.Equal(t, "agent_acceptor", first.Body.Data[1].Key)
	require.EqualValues(t, 2, seedCount(t, db, "roles_probe"))
	require.Zero(t, seedCount(t, db, "grants_probe"), "eligibility roles must not grant command permissions")
	require.Zero(t, seedCount(t, db, "binding_probe"), "provisioning must not grant membership")
	audits := seedCount(t, db, "audit_probe")
	replayed, err := m.provisionReviewerRolesHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, first.Body.Data, replayed.Body.Data)
	require.Equal(t, audits, seedCount(t, db, "audit_probe"))
	require.NoError(t, db.Exec("DELETE FROM grants_probe").Error)
	in.IdempotencyKey = "reviewer-roles-after-revocation"
	_, err = m.provisionReviewerRolesHTTP(ctx, in)
	require.NoError(t, err)
	require.Zero(t, seedCount(t, db, "grants_probe"), "explicit IAM revocations stay revoked")
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: false}, nil
	}
	_, err = m.provisionReviewerRolesHTTP(ctx, in)
	require.Error(t, err, "cached receipt must not bypass revoked provisioning authority")
}

func TestJReviewerRolesFailClosedAndAtomic(t *testing.T) {
	for _, scenario := range []string{"nontransaction", "nil_transaction", "wrong_tenant", "agent", "inactive_member", "denied", "audit_failure", "missing_identity", "lock_failure", "identity_failure", "explanation_failure", "no_authority_source", "invalid_authority_source"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx := reviewerRolesHarness(t)
			tenant := guid.ID(1)
			failure := errors.New("audit failure")
			switch scenario {
			case "missing_identity":
				m.principals.Lock = nil
			case "lock_failure":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "identity_failure":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "explanation_failure":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, ErrUnavailable
				}
			case "no_authority_source":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{Allowed: true}, nil
				}
			case "invalid_authority_source":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{{Effect: "allow", PermissionCode: "agent_manage", RoleID: "bad"}}}, nil
				}
			case "wrong_tenant":
				tenant = 2
			case "agent":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "inactive_member":
				m.delegations.members = delegationMembers{false}
			case "denied":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{Allowed: false}, nil
				}
			case "audit_failure":
				m.seed.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			}
			var err error
			if scenario == "nontransaction" {
				_, err = m.provisionReviewerRolesOn(ctx, db, tenant)
			} else if scenario == "nil_transaction" {
				_, err = m.provisionReviewerRolesOn(ctx, nil, tenant)
			} else {
				err = db.Transaction(func(tx *gorm.DB) error { _, e := m.provisionReviewerRolesOn(ctx, tx, tenant); return e })
			}
			require.Error(t, err)
			require.Zero(t, seedCount(t, db, "roles_probe"))
			require.Zero(t, seedCount(t, db, "grants_probe"))
			require.Zero(t, seedCount(t, db, "audit_probe"))
		})
	}
}

func TestJReviewerRoleManifestGrantsNoCommandAuthority(t *testing.T) {
	for _, entry := range reviewerRoleManifest() {
		require.Empty(t, entry.Codes)
		require.Empty(t, entry.Conditions)
	}
}

func TestJReviewerRolesHTTPFailurePathsLeaveNoDefinitions(t *testing.T) {
	for _, scenario := range []string{"invalid_tenant", "missing_replay", "invalid_key", "missing_database", "lease_failure", "nil_lease", "stale_lease", "audit_failure", "role_conflict"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx := reviewerRolesHarness(t)
			in := &reviewerRolesInput{TenantID: "1", IdempotencyKey: "role-failure-key"}
			switch scenario {
			case "invalid_tenant":
				in.TenantID = "bad"
			case "missing_replay":
				m.delegations.replay = nil
			case "invalid_key":
				in.IdempotencyKey = ""
			case "missing_database":
				m.runtime = nil
			case "lease_failure":
				m.seed.acquire = func(context.Context, string, time.Duration) (*dlock.Lock, error) { return nil, ErrUnavailable }
			case "nil_lease":
				m.seed.acquire = func(context.Context, string, time.Duration) (*dlock.Lock, error) { return nil, nil }
			case "stale_lease":
				lock, err := m.seed.acquire(ctx, "stale", time.Minute)
				require.NoError(t, err)
				require.NoError(t, lock.Unlock(ctx))
				m.seed.acquire = func(context.Context, string, time.Duration) (*dlock.Lock, error) { return lock, nil }
			case "audit_failure":
				m.seed.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return ErrUnavailable }
			case "role_conflict":
				m.seed.roles.EnsureRoleOn = func(context.Context, *gorm.DB, guid.ID, string, string, string) (core.Role, error) {
					return core.Role{ID: 88}, core.ErrRoleNameConflict
				}
			}
			_, err := m.provisionReviewerRolesHTTP(ctx, in)
			require.Error(t, err)
			require.Zero(t, seedCount(t, db, "roles_probe"))
			require.Zero(t, seedCount(t, db, "grants_probe"))
			require.Zero(t, seedCount(t, db, "audit_probe"))
		})
	}
}
