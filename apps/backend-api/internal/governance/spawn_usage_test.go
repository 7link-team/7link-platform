package governance

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnReservationIdempotencyQuotaDayAndRollback(t *testing.T) {
	m, db, ctx, _, in := spawnGrantHarness(t)
	grant, e := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, e)
	agentCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 3, SubjectType: authn.SubjectTypePrincipal})
	req := AuthorityRequest{TenantID: 1, PrincipalID: 3, ActorKind: "AGENT", Code: "agent_spawn", Spawn: &SpawnRequestFacts{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Role: "EXECUTOR", MachineID: 5, Runtime: "CODEX", Model: "model", Effort: "medium", EstimatedCapMicros: 1000000}}
	reserve := func(child guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.reserveSpawnOn(agentCtx, tx, 1, child, req) })
	}
	release := func(child guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.releaseSpawnOn(ctx, tx, 1, child, "RETIRED") })
	}
	require.NoError(t, reserve(44))
	require.NoError(t, reserve(44), "same Agent replay must succeed even when quota is full")
	require.EqualValues(t, 1, seedCount(t, db, m.seed.names.SpawnGrantMembers.String()))
	require.Error(t, reserve(45), "only one slot")
	require.NoError(t, release(44))
	require.NoError(t, release(44), "release cannot decrement twice")
	require.NoError(t, reserve(45))
	require.NoError(t, release(45))
	require.Error(t, reserve(46), "daily quota does not reset on release")
	q := query.BindSpawnGrants(db, m.seed.names)
	row, e := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, e)
	require.Zero(t, row.ActiveCount)
	require.Equal(t, 2, row.SpawnedToday)
	at := m.seed.now().Add(24 * time.Hour)
	m.seed.now = func() time.Time { return at }
	require.NoError(t, reserve(46))
	row, e = q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, e)
	require.Equal(t, 1, row.ActiveCount)
	require.Equal(t, 1, row.SpawnedToday)
	require.NoError(t, release(46))
	require.NoError(t, db.Exec("CREATE TRIGGER reject_spawn_member BEFORE INSERT ON "+m.seed.names.SpawnGrantMembers.String()+" BEGIN SELECT RAISE(ABORT,'member insert failed'); END").Error)
	require.Error(t, reserve(47))
	row, e = q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, e)
	require.Zero(t, row.ActiveCount)
	require.Equal(t, 1, row.SpawnedToday, "failed child transaction restores both counters")
}

func TestSpawnAuthorityRequiresCurrentScopeIdentityAndExplicitDeny(t *testing.T) {
	m, db, ctx, _, in := spawnGrantHarness(t)
	_, e := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, e)
	req := AuthorityRequest{TenantID: 1, PrincipalID: 3, ActorKind: "AGENT", Code: "agent_spawn", Spawn: &SpawnRequestFacts{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Role: "EXECUTOR", MachineID: 5, Runtime: "CODEX", Model: "model", Effort: "medium", EstimatedCapMicros: 1000000}}
	check := func() AuthorityDecision {
		var d AuthorityDecision
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; d, e = m.Check(ctx, tx, req); return e }))
		return d
	}
	require.True(t, check().Allowed)
	require.Equal(t, "POLICY:spawn_grant", check().AuthoritySource)
	req.Spawn.Role = "REVIEWER"
	require.Equal(t, "SPAWN_GRANT_SCOPE", check().ReasonCode)
	req.Spawn.Role = "EXECUTOR"
	req.Spawn.ProjectIDs = []guid.ID{98}
	require.Equal(t, "SPAWN_GRANT_SCOPE", check().ReasonCode)
	req.Spawn.ProjectIDs = []guid.ID{99}
	req.Spawn.Effort = "high"
	require.Equal(t, "SPAWN_GRANT_SCOPE", check().ReasonCode)
	req.Spawn.Effort = "medium"
	req.Spawn.EstimatedCapMicros = 1000001
	require.Equal(t, "SPAWN_GRANT_SCOPE", check().ReasonCode)
	req.Spawn.EstimatedCapMicros = 1000000
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: false, Reason: "explicit_deny"}, nil
	}
	require.Equal(t, "EXPLICIT_DENY", check().ReasonCode)
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: false, Reason: "no_match"}, nil
	}
	require.True(t, check().Allowed, "policy may allow without a role grant")
	req.PrincipalID = 4
	require.Equal(t, "SPAWN_NOT_LEAD", check().ReasonCode)
}
