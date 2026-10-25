package governance

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDelegationExpiryIsAtomicAndPublishesOnlyActualWindowEnd(t *testing.T) {
	m, db, ctx, publisher, in := delegationHarness(t)
	granted, err := grantDelegation(t, m, db, ctx, in)
	require.NoError(t, err)
	require.NoError(t, m.expireDelegations(t.Context(), jobs.Job{}))
	require.EqualValues(t, 1, seedCount(t, db, "binding_probe"))
	require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
	at := granted.EndsAt
	m.seed.now = func() time.Time { return at }
	publisher.Fail = true
	require.ErrorContains(t, m.expireDelegations(t.Context(), jobs.Job{}), "outbox unavailable")
	q := query.BindDelegations(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(granted.ID)).Take()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
	require.Equal(t, granted.Version, row.Version)
	require.NotNil(t, row.ActiveKey)
	require.EqualValues(t, 1, seedCount(t, db, "binding_probe"))
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
	publisher.Fail = false
	require.NoError(t, m.expireDelegations(t.Context(), jobs.Job{}))
	row, err = q.WithContext(ctx).Where(q.ID.Eq(granted.ID)).Take()
	require.NoError(t, err)
	require.Equal(t, "EXPIRED", row.State)
	require.Equal(t, "WINDOW_ENDED", *row.EndReason)
	require.Nil(t, row.ActiveKey)
	require.Nil(t, row.EndedBy, "scheduled expiry must not invent a Human actor")
	require.Equal(t, granted.Version+1, row.Version)
	require.Zero(t, seedCount(t, db, "binding_probe"))
	require.EqualValues(t, 2, seedCount(t, db, "audit_probe"))
	var detail string
	require.NoError(t, db.Table("event_probe").Order("rowid DESC").Limit(1).Pluck("detail", &detail).Error)
	require.Contains(t, detail, "delegation.expired")
	require.Contains(t, detail, "SYSTEM:governance.delegation_expiry")
	require.NoError(t, m.expireDelegations(t.Context(), jobs.Job{}))
	require.EqualValues(t, 2, seedCount(t, db, "event_probe"), "repeat ticks must not repeat the terminal fact")
	require.Error(t, m.expireDelegations(t.Context(), jobs.Job{TenantID: 1}))
}

func TestDelegationExpiryPreservesAuthorityWhenBindingRemovalFails(t *testing.T) {
	m, db, ctx, _, in := delegationHarness(t)
	granted, err := grantDelegation(t, m, db, ctx, in)
	require.NoError(t, err)
	m.seed.now = func() time.Time { return granted.EndsAt }
	remove := m.delegations.grants.RemoveEntityRoleBinding
	m.delegations.grants.RemoveEntityRoleBinding = nil
	require.ErrorIs(t, m.expireDelegations(t.Context(), jobs.Job{}), ErrUnavailable)
	m.delegations.grants.RemoveEntityRoleBinding = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error) {
		return false, ErrUnavailable
	}
	require.ErrorIs(t, m.expireDelegations(t.Context(), jobs.Job{}), ErrUnavailable)
	q := query.BindDelegations(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(granted.ID)).Take()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
	require.EqualValues(t, 1, seedCount(t, db, "binding_probe"))
	require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
	m.delegations.grants.RemoveEntityRoleBinding = remove
	require.NoError(t, db.Migrator().DropTable(m.seed.names.Delegations.String()))
	require.Error(t, m.expireDelegations(t.Context(), jobs.Job{}))
	m.runtime = nil
	require.Error(t, m.expireDelegations(t.Context(), jobs.Job{}))
}

func TestDelegationProjectConnectorBindsExactlyOnce(t *testing.T) {
	m := &Module{}
	require.ErrorIs(t, m.bindDelegationProjects(DelegationProjectFacts{}), ErrInvalidArgument)
	called := false
	facts := DelegationProjectFacts{InspectOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
		called = true
		return DelegationProjectState{Stage: "ACTIVE"}, nil
	}}
	require.NoError(t, m.bindDelegationProjects(facts))
	require.ErrorIs(t, m.bindDelegationProjects(facts), ErrInvalidArgument)
	state, err := m.delegations.projects.InspectOn(t.Context(), nil, 1, 99)
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, "ACTIVE", state.Stage)
}

func TestDelegationFailedGrantNeverLeavesCarriersOrDurableFacts(t *testing.T) {
	for _, fault := range []string{"principal lock", "human source", "project source", "agent source", "id source", "invalid id", "binding write", "role source", "audit source", "insert"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, in := delegationHarness(t)
			switch fault {
			case "principal lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "human source":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "project source":
				m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
					return DelegationProjectState{}, ErrUnavailable
				}
			case "agent source":
				m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, ErrUnavailable
				}
			case "id source":
				m.seed.ids = seedTestIDs{err: ErrUnavailable}
			case "invalid id":
				m.seed.ids = seedTestIDs{}
			case "binding write":
				m.delegations.bindings.PutOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, core.BindingEffect, time.Time) (core.EntityRoleBinding, bool, error) {
					return core.EntityRoleBinding{}, false, ErrUnavailable
				}
			case "role source":
				require.NoError(t, db.Table(m.seed.names.RoleSeeds.String()).Where("tenant_id = ?", 1).Delete(nil).Error)
			case "audit source":
				m.seed.audit = nil
			case "insert":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_delegation BEFORE INSERT ON "+m.seed.names.Delegations.String()+" BEGIN SELECT RAISE(ABORT,'delegation insert failed'); END").Error)
			}
			_, err := grantDelegation(t, m, db, ctx, in)
			require.Error(t, err)
			for _, table := range []string{m.seed.names.Delegations.String(), "binding_probe", "audit_probe", "event_probe"} {
				require.Zero(t, seedCount(t, db, table), table)
			}
		})
	}
}

func TestDelegationCurrentReadRequiresAnActivePrincipal(t *testing.T) {
	m, _, _, _ := delegationHTTPHarness(t)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 2, SubjectType: authn.SubjectTypePrincipal})
	for _, fault := range []string{"lock error", "inactive", "missing source", "explain error"} {
		t.Run(fault, func(t *testing.T) {
			m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: 2}, true, nil
			}
			m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{Allowed: true}, nil
			}
			switch fault {
			case "lock error":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "inactive":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "missing source":
				m.principals.LockActiveRow = nil
			case "explain error":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, ErrUnavailable
				}
			}
			out, err := m.listDelegationsHTTP(ctx, &delegationListInput{TenantID: "1", Limit: 10})
			require.Error(t, err)
			require.Nil(t, out)
		})
	}
}
