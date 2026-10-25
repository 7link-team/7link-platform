package governance

import (
	"context"
	"testing"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnAuthorityCurrentSourceFailuresNeverAuthorize(t *testing.T) {
	for _, fault := range []string{"invalid request", "dependency", "principal error", "inactive principal", "inactive member", "agent error", "missing agent", "explain error", "no grant", "corrupt grant", "grant source", "project error", "model error", "unlisted model", "own transaction", "outside transaction", "missing writer"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, in := spawnGrantHarness(t)
			grant, err := grantSpawn(t, m, db, ctx, in)
			require.NoError(t, err)
			req := AuthorityRequest{TenantID: 1, PrincipalID: 3, ActorKind: "AGENT", Code: "agent_spawn", Spawn: &SpawnRequestFacts{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Role: "EXECUTOR", MachineID: 5, Runtime: "CODEX", Model: "model", Effort: "medium", EstimatedCapMicros: 1000000}}
			wantReason := ""
			switch fault {
			case "invalid request":
				req.Environment = "PRODUCTION"
				wantReason = "INVALID_REQUEST"
			case "dependency":
				m.spawnDirectory.AgentOn = nil
			case "principal error":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "inactive principal":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				wantReason = "PRINCIPAL_INACTIVE"
			case "inactive member":
				m.delegations.members = delegationMembers{false}
				wantReason = "PRINCIPAL_INACTIVE"
			case "agent error":
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, ErrUnavailable
				}
			case "missing agent":
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, nil
				}
				wantReason = "SPAWN_NOT_LEAD"
			case "explain error":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, ErrUnavailable
				}
			case "no grant":
				require.NoError(t, db.Exec("DELETE FROM "+m.seed.names.SpawnGrants.String()+" WHERE id = ?", grant.ID).Error)
				wantReason = "SPAWN_GRANT_MISSING"
			case "corrupt grant":
				require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("id = ?", grant.ID).Update("scope_json", "broken").Error)
			case "grant source":
				require.NoError(t, db.Migrator().DropTable(m.seed.names.SpawnGrants.String()))
			case "project error":
				m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
					return DelegationProjectState{}, ErrUnavailable
				}
			case "model error":
				m.spawnDirectory.ModelOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string) (bool, error) {
					return false, ErrUnavailable
				}
			case "unlisted model":
				calls := 0
				m.spawnDirectory.ModelOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string) (bool, error) {
					calls++
					return calls == 1, nil
				}
				wantReason = "SPAWN_GRANT_SCOPE"
			case "missing writer":
				m.runtime = nil
			}
			var decision AuthorityDecision
			if fault == "outside transaction" {
				decision, err = m.checkSpawnAuthority(ctx, db, req)
			} else if fault == "own transaction" || fault == "missing writer" {
				decision, err = m.checkSpawnAuthority(ctx, nil, req)
			} else {
				err = db.Transaction(func(tx *gorm.DB) error { var e error; decision, e = m.checkSpawnAuthority(ctx, tx, req); return e })
			}
			if fault == "own transaction" {
				require.NoError(t, err)
				require.True(t, decision.Allowed)
			} else {
				require.False(t, decision.Allowed)
				if wantReason != "" {
					require.NoError(t, err)
					require.Equal(t, wantReason, decision.ReasonCode)
				} else {
					require.Error(t, err)
				}
			}
		})
	}
}

func TestSpawnUsageRejectsInvalidTransactionsAndReleaseRollback(t *testing.T) {
	m, db, ctx, _, in := spawnGrantHarness(t)
	grant, err := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, err)
	agentCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 3, SubjectType: authn.SubjectTypePrincipal})
	req := AuthorityRequest{TenantID: 1, PrincipalID: 3, ActorKind: "AGENT", Code: "agent_spawn", Spawn: &SpawnRequestFacts{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Role: "EXECUTOR", MachineID: 5, Runtime: "CODEX", Model: "model", Effort: "medium", EstimatedCapMicros: 1000000}}
	require.ErrorIs(t, m.reserveSpawnOn(agentCtx, nil, 1, 44, req), ErrInvalidArgument)
	require.ErrorIs(t, m.reserveSpawnOn(agentCtx, db, 1, 44, req), ErrInvalidArgument)
	require.ErrorIs(t, m.releaseSpawnOn(ctx, nil, 1, 44, "RETIRED"), ErrInvalidArgument)
	require.ErrorIs(t, m.releaseSpawnOn(ctx, db, 1, 44, "RETIRED"), ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.releaseSpawnOn(ctx, tx, 1, 999, "RETIRED") }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.reserveSpawnOn(agentCtx, tx, 1, 44, req) }))
	require.NoError(t, db.Exec("CREATE TRIGGER reject_release BEFORE UPDATE ON "+m.seed.names.SpawnGrantMembers.String()+" BEGIN SELECT RAISE(ABORT,'release unavailable'); END").Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.releaseSpawnOn(ctx, tx, 1, 44, "RETIRED") }))
	q := query.BindSpawnGrants(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, err)
	require.Equal(t, 1, row.ActiveCount)
	members := query.BindSpawnGrantMembers(db, m.seed.names)
	member, err := members.WithContext(ctx).Where(members.AgentID.Eq(guid.ID(44))).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", member.State)
	require.Nil(t, member.ReleasedAt)
}
