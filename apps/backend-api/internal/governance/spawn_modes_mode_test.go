package governance

import (
	"context"
	"encoding/json"
	"testing"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModeTakeoverRevokesScopedSpawnGrantsAtomically(t *testing.T) {
	m, db, ctx, pub, input := spawnGrantHarness(t)
	grant, err := grantSpawn(t, m, db, ctx, input)
	require.NoError(t, err)
	q := query.BindSpawnGrants(db, m.seed.names)
	original, err := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, err)
	ids := []guid.ID{grant.ID}
	for i, projects := range [][]guid.ID{{99, 100}, {}, {100}} {
		row := *original
		row.ID += guid.ID(i + 1)
		row.GranteeAgentID += guid.ID(i + 1)
		row.GranteePrincipalID += guid.ID(i + 1)
		key := row.GranteeAgentID.String()
		row.ActiveKey = &key
		scope := input.SpawnGrantScope
		scope.ProjectIDs = projects
		encoded, err := json.Marshal(scope)
		require.NoError(t, err)
		row.ScopeJSON = string(encoded)
		if i == 0 {
			row.State = "EXHAUSTED"
		}
		require.NoError(t, q.WithContext(ctx).Create(&row))
		ids = append(ids, row.ID)
	}
	members := query.BindSpawnGrantMembers(db, m.seed.names)
	require.NoError(t, members.WithContext(ctx).Create(&model.SpawnGrantMember{ID: 80, TenantID: 1, GrantID: grant.ID, AgentID: 60, State: "ACTIVE", PerAgentCapMicros: 1000000, ReservedAt: m.seed.now()}))
	require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("id=?", grant.ID).Update("active_count", 1).Error)
	// The actor has takeover authority but explicitly lacks grant-management
	// authority. Revocation must use the already-checked takeover source.
	m.explain = func(_ context.Context, _ *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: req.Permission == "project_takeover", Reason: "administrator_grant"}, nil
	}
	principals, err := m.modeDelegationPrincipalsOn(ctx, db, 1, 99)
	require.NoError(t, err)
	require.Equal(t, []guid.ID{3, 4}, principals)
	var revoked []guid.ID
	call := func(locked []guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var err error
			revoked, err = m.revokeModeSpawnGrantsOn(ctx, tx, 1, 99, "project_takeover", "Human takes over project authority", locked)
			return err
		})
	}
	require.ErrorIs(t, call([]guid.ID{3}), ErrVersion)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
	pub.Fail = true
	require.Error(t, call(principals))
	pub.Fail = false
	row, err = q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
	member, err := members.WithContext(ctx).Where(members.AgentID.Eq(guid.ID(60))).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", member.State)
	require.NoError(t, call(principals))
	require.Equal(t, ids[:2], revoked)
	rows, err := q.WithContext(ctx).Order(q.ID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 4)
	for i, row := range rows {
		if i < 2 {
			require.Equal(t, "REVOKED", row.State)
			require.Equal(t, "TAKEOVER", *row.EndReason)
			require.Nil(t, row.ActiveKey)
			require.Zero(t, row.ActiveCount)
		} else {
			require.Equal(t, "ACTIVE", row.State)
		}
	}
	member, err = members.WithContext(ctx).Where(members.AgentID.Eq(guid.ID(60))).First()
	require.NoError(t, err)
	require.Equal(t, "RELEASED", member.State)
	require.Equal(t, "GRANT_ENDED", *member.ReleaseReason)
}

func TestModeAuthorityNeverSubstitutesModeChangeForTakeover(t *testing.T) {
	m, db, ctx, _, _ := spawnGrantHarness(t)
	m.explain = func(_ context.Context, _ *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: req.Permission == "project_mode_change", Reason: "administrator_grant"}, nil
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := m.projectModeAuthorityOn(ctx, tx, 1, 99, "project_takeover")
		return err
	})
	require.ErrorIs(t, err, ErrForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.projectModeAuthorityOn(ctx, tx, 1, 99, "project_mode_change")
		return err
	}))
}
