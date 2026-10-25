package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestProjectStageHTTPValidationAndUnavailable(t *testing.T) {
	m := &Module{}
	ctx := t.Context()
	_, err := m.changeProjectStageHTTP(ctx, &stageChangeInput{EntityID: "bad"})
	assertWorkStatus(t, err, 422)
	_, err = m.changeProjectStageHTTP(ctx, &stageChangeInput{EntityID: "99", IfMatch: `"1"`})
	assertWorkStatus(t, err, 503)
	for _, in := range []*stageListInput{{EntityID: "bad", Limit: 50}, {EntityID: "99", Limit: 201}, {EntityID: "99", Limit: 50, Offset: -1}} {
		_, err = m.listProjectStagesHTTP(ctx, in)
		assertWorkStatus(t, err, 422)
	}
	_, err = m.listProjectStagesHTTP(ctx, &stageListInput{EntityID: "99", Limit: 50})
	assertWorkStatus(t, err, 503)
	_, err = m.projectStageReadinessHTTP(ctx, &stageReadinessInput{EntityID: "bad"})
	assertWorkStatus(t, err, 422)
	_, err = m.projectStageReadinessHTTP(ctx, &stageReadinessInput{EntityID: "99"})
	assertWorkStatus(t, err, 503)
}

func TestProjectStageHTTPRejectsWithActionableErrors(t *testing.T) {
	for _, scenario := range []string{"illegal", "criteria", "reason", "candidates", "principal", "authority"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := stageHarness(t)
			in := &stageChangeInput{EntityID: "99", IfMatch: `"1"`, Body: stageChangeBody{ToStage: "PLANNING"}}
			expected := 409
			switch scenario {
			case "criteria":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "DISCOVERY").Error)
				expected = 422
			case "reason":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "PLANNING").Error)
				in.Body.ToStage, expected = "DISCOVERY", 422
			case "candidates":
				m.projectAccess = core.AccessTableNames{}
				in.Body.ToStage, expected = "ACTIVE", 503
			case "principal":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return errors.New("principal lock unavailable") }
				expected = 500
			case "authority":
				m.stageAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{}, errors.New("authority unavailable")
				}
				expected = 500
			}
			_, err := m.changeProjectStageHTTP(ctx, in)
			assertWorkStatus(t, err, expected)
			project, err := query.BindProjects(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, 1, project.Version)
			count, err := query.BindStageTransitions(db, m.service.names).WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

func TestProjectStageReadinessFailsClosedOnPrincipalFailure(t *testing.T) {
	m, _, ctx, _ := stageHarness(t)
	m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return errors.New("principal lock unavailable") }
	_, err := m.projectStageReadinessHTTP(ctx, &stageReadinessInput{EntityID: "99"})
	assertWorkStatus(t, err, 500)
	m.principals.Lock = nil
	_, err = m.projectStageReadinessHTTP(ctx, &stageReadinessInput{EntityID: "99"})
	assertWorkStatus(t, err, 503)
}

func TestProjectStageReadinessNeverInventsLaterAggregateFacts(t *testing.T) {
	for _, tc := range []struct {
		stage   string
		allowed map[string]bool
	}{
		{"ACTIVE", map[string]bool{"BLOCKED": true, "ACCEPTANCE_PENDING": false}},
		{"ACCEPTANCE_PENDING", map[string]bool{"ACTIVE": true, "DELIVERED": false}},
		{"DELIVERED", map[string]bool{"ACTIVE": false, "CLOSED": false}},
		{"CLOSED", map[string]bool{}},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			m, db, ctx, _ := stageHarness(t)
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", tc.stage).Error)
			result, err := m.projectStageReadinessHTTP(ctx, &stageReadinessInput{EntityID: "99"})
			if tc.stage == "CLOSED" {
				assertWorkStatus(t, err, 409)
				require.Empty(t, stageTargets(tc.stage))
				return
			}
			assertWorkStatus(t, err, 503)
			require.Nil(t, result, "missing actual package/execution capability must remain unavailable")
		})
	}
}

func TestProjectStageHTTPReplayHistoryAndIsolation(t *testing.T) {
	m, _, ctx, _ := stageHarness(t)
	input := &stageChangeInput{EntityID: "99", IfMatch: `"1"`, IdempotencyKey: "stage-discovery-once", Body: stageChangeBody{ToStage: "DISCOVERY"}}
	first, err := m.changeProjectStageHTTP(ctx, input)
	require.NoError(t, err)
	require.Equal(t, "DISCOVERY", first.Body.Data.Project.Stage)
	replay, err := m.changeProjectStageHTTP(ctx, input)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	changed := *input
	changed.Body.Reason = "Different request"
	_, err = m.changeProjectStageHTTP(ctx, &changed)
	assertWorkStatus(t, err, 422)
	changed = *input
	changed.IdempotencyKey = "stale-stage"
	_, err = m.changeProjectStageHTTP(ctx, &changed)
	assertWorkStatus(t, err, 409)
	page, err := m.listProjectStagesHTTP(ctx, &stageListInput{EntityID: "99", Limit: 1})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Body.Meta.Page.Total)
	require.Len(t, page.Body.Data, 1)
	expectedJSON, e := json.Marshal(first.Body.Data.Transition)
	require.NoError(t, e)
	actualJSON, e := json.Marshal(page.Body.Data[0])
	require.NoError(t, e)
	require.JSONEq(t, string(expectedJSON), string(actualJSON))
	encoded, err := json.Marshal(page.Body.Data)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "tenant_id")
	require.NotContains(t, string(encoded), "criteria_snapshot")
	page, err = m.listProjectStagesHTTP(ctx, &stageListInput{EntityID: "99", Offset: 1, Limit: 1})
	require.NoError(t, err)
	require.Empty(t, page.Body.Data)
	for _, claims := range []*authn.Claims{{TenantID: 2, EntityID: 99, PrincipalID: 11}, {TenantID: 1, EntityID: 98, PrincipalID: 11}} {
		_, err = m.listProjectStagesHTTP(authn.WithClaims(ctx, claims), &stageListInput{EntityID: claims.EntityID.String(), Limit: 50})
		assertWorkStatus(t, err, 404)
	}
	ready, err := m.projectStageReadinessHTTP(ctx, &stageReadinessInput{EntityID: "99"})
	require.NoError(t, err)
	require.Len(t, ready.Body.Data, 2)
	require.Equal(t, "CANCELLED", ready.Body.Data[1].Target) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	require.True(t, ready.Body.Data[1].HumanOnly)
	require.Equal(t, "PLANNING", ready.Body.Data[0].Target)
	require.False(t, ready.Body.Data[0].Allowed)
}
