package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"

	"github.com/stretchr/testify/require"
)

func TestProjectControlRecipientRequiresCurrentHumanIdentity(t *testing.T) {
	for _, scenario := range []string{"unbound", "inactive", "wrong_identity", "lookup_error"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx := projectModeHarness(t)
			failure := errors.New("principal lookup unavailable")
			want := error(nil)
			m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				if scenario == "lookup_error" {
					return identity.Principal{}, false, failure
				}
				id := guid.ID(11)
				if scenario == "wrong_identity" {
					id = 12
				}
				return identity.Principal{ID: id}, scenario != "inactive", nil
			}
			if scenario == "unbound" {
				m.principals.ActiveHumanByID = nil
				want = ErrUnavailable
			}
			if scenario == "lookup_error" {
				want = failure
			}
			visible, err := m.projectControlReadableOn(ctx, db, 1, 99, 11)
			require.False(t, visible)
			if want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, want)
			}
		})
	}
}

func TestProjectControlSourcesTrackOnlyCurrentPersistedEpisodes(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Updates(map[string]any{"team_mode": "AUTONOMOUS", "autonomy_version": 7, "holds": `[{"kind":"AUTONOMY_PRECONDITION"},{"kind":"MANUAL","source":"ANDON","id":"71","since":"` + now.Format(time.RFC3339Nano) + `"}]`}).Error)
	for _, held := range []bool{false, true} {
		visible, err := m.projectAutonomyFactOn(ctx, db, 1, 99, held, 7)
		require.NoError(t, err)
		require.Equal(t, held, visible)
	}
	fact, found, err := m.projectAndonFactOn(ctx, db, 1, 99)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, guid.ID(71), fact.OccurrenceID)
	require.Equal(t, now, fact.CreatedAt)
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("holds", `{}`).Error)
	_, err = m.projectAutonomyFactOn(ctx, db, 1, 99, true, 7)
	require.ErrorIs(t, err, ErrUnavailable)
	_, _, err = m.projectAndonFactOn(ctx, db, 1, 99)
	require.ErrorIs(t, err, ErrUnavailable)
	_, _, err = m.projectModeFactOn(ctx, nil, 1, 99)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.projectControlReadableOn(ctx, db, 1, 99, 0)
	require.NoError(t, err)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	visible, err := m.projectControlReadableOn(ctx, db, 1, 99, 11)
	require.NoError(t, err)
	require.False(t, visible)
	failure := errors.New("policy database unavailable")
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, failure
	}
	_, err = m.projectControlReadableOn(ctx, db, 1, 99, 11)
	require.ErrorIs(t, err, failure)
}

func TestProjectControlFactsRejectAbsentAndOtherTenantSources(t *testing.T) {
	m, db, ctx, _ := stageHarness(t)
	_, found, err := m.projectAndonFactOn(ctx, db, 2, 99)
	require.NoError(t, err)
	require.False(t, found)
	_, found, err = m.projectModeFactOn(ctx, db, 1, 999)
	require.NoError(t, err)
	require.False(t, found)
	_, _, err = m.projectAndonFactOn(ctx, nil, 1, 99)
	require.ErrorIs(t, err, ErrInvalidArgument)
	yes, err := m.projectAutonomyFactOn(ctx, db, 2, 99, false, 2)
	require.NoError(t, err)
	require.False(t, yes)
	_, err = m.projectAutonomyFactOn(ctx, nil, 1, 99, true, 2)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestAutonomyFactsRejectLegacyAndInvalidRevisions(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("team_mode", "AUTONOMOUS").Error)
	for _, version := range []int64{-1, 0, 1, 2} {
		visible, err := m.projectAutonomyFactOn(ctx, db, 1, 99, false, version)
		require.NoError(t, err)
		require.False(t, visible, "a legacy project has no invented autonomy episode")
	}
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("autonomy_version", 2).Error)
	for _, version := range []int64{-1, 0, 1, 3} {
		visible, err := m.projectAutonomyFactOn(ctx, db, 1, 99, false, version)
		require.NoError(t, err)
		require.False(t, visible)
	}
}
