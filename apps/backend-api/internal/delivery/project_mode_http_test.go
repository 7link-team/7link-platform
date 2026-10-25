package delivery

import (
	"context"
	"encoding/json"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkTakeoverHTTPPreservesExecutionAndRechecksAuthorityOnReplay(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	w := query.BindWorkItems(db, m.service.names)
	work, err := w.WithContext(ctx).First()
	require.NoError(t, err)
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Updates(map[string]any{"current_execution_id": 901, "state": "IN_PROGRESS"}).Error)
	in := &workTakeoverInput{EntityID: "99", WorkItemID: work.ID.String(), Key: "work-takeover-once", Cookie: "cookie", Body: workTakeoverBody{Version: work.Version, Reason: "Human takes over this running work item", ReauthProof: "proof"}}
	first, err := m.takeoverWorkHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "IN_PROGRESS", first.Body.Data.State)
	stored, err := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
	require.NoError(t, err)
	require.True(t, stored.RequireHumanDecision)
	require.Equal(t, guid.ID(901), *stored.CurrentExecutionID)
	replay, err := m.takeoverWorkHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	var proofs int64
	require.NoError(t, db.Table("mode_probe").Where("kind=?", "proof").Count(&proofs).Error)
	require.EqualValues(t, 1, proofs)
	m.modeGovernance.AuthorityOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (string, error) {
		return "", governance.ErrForbidden
	}
	_, err = m.takeoverWorkHTTP(ctx, in)
	assertWorkStatus(t, err, 403)
}

func TestProjectModeHTTPReplayRechecksCurrentAuthorityAndHistory(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	in := &projectModeInput{EntityID: "99", IfMatch: `"1"`, Key: "mode-once", Cookie: "cookie", Body: projectModeBody{ToMode: "AUTONOMOUS", Reason: "Current project prerequisites are verified", ReauthProof: "proof"}}
	first, err := m.changeProjectModeHTTP(ctx, in)
	require.NoError(t, err)
	replay, err := m.changeProjectModeHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	var proofs int64
	require.NoError(t, db.Table("mode_probe").Where("kind=?", "proof").Count(&proofs).Error)
	require.EqualValues(t, 1, proofs)
	history, err := m.projectModeHistoryHTTP(ctx, &stageListInput{EntityID: "99", Limit: 20})
	require.NoError(t, err)
	require.Len(t, history.Body.Data, 1)
	require.Empty(t, history.Body.Data[0].RevokedSpawnIDs)
	stale := *in
	stale.Key = "stale-mode"
	stale.Body.ToMode = "HYBRID"
	_, err = m.changeProjectModeHTTP(ctx, &stale)
	assertWorkStatus(t, err, 409)
	m.modeGovernance.AuthorityOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (string, error) {
		return "", governance.ErrForbidden
	}
	_, err = m.changeProjectModeHTTP(ctx, in)
	assertWorkStatus(t, err, 403)
}

func TestProjectTakeoverHTTPNoModeChangeStillReplaysCommittedReceipt(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	in := &projectTakeoverInput{EntityID: "99", IfMatch: `"1"`, Key: "takeover-once", Cookie: "cookie"}
	in.Body.Reason, in.Body.ReauthProof = "Human assumes control over delegated authority", "proof"
	first, err := m.takeoverProjectHTTP(ctx, in)
	require.NoError(t, err)
	require.Nil(t, first.Body.Data.ModeChangeID)
	require.NotNil(t, first.Body.Data.TakeoverID)
	require.Nil(t, first.Body.Data.Project.TeamModeChangedAt, "a control takeover does not change mode history timestamps")
	require.Equal(t, []guid.ID{61}, first.Body.Data.ModeChange.RevokedSpawnIDs)
	replay, err := m.takeoverProjectHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	history, err := m.projectModeHistoryHTTP(ctx, &stageListInput{EntityID: "99", Limit: 20})
	require.NoError(t, err)
	require.Len(t, history.Body.Data, 1)
	require.Equal(t, []guid.ID{61}, history.Body.Data[0].RevokedSpawnIDs)
	var count int64
	require.NoError(t, db.Table("mode_probe").Where("kind=?", "spawn").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestModeErrorsPreservePublicStatusCodeAndFirstFailedPrerequisite(t *testing.T) {
	for _, tc := range []struct {
		err          error
		status, code int
	}{{governance.ErrForbidden, 403, 0}, {governance.ErrUnavailable, 503, 0}, {governance.ErrVersion, 409, 0}, {ErrModeSame, 409, 127020}, {ErrModeIllegal, 409, 127051}, {ErrModePreconditions, 422, 127021}, {stageFailure{err: ErrModePreconditions, meta: map[string]any{"preconditions": []stageCriterion{{Passed: true}, {Passed: true}, {Passed: false}, {Passed: false}}}}, 422, 127023}} {
		err := modeHTTPError(context.Background(), tc.err)
		assertWorkStatus(t, err, tc.status)
		if tc.code > 0 {
			raw, e := json.Marshal(err)
			require.NoError(t, e)
			var body struct {
				Code int             `json:"code"`
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(raw, &body))
			require.Equal(t, tc.code, body.Code)
			if tc.code == 127023 {
				require.Contains(t, string(body.Data), "preconditions")
			}
		}
	}
}
