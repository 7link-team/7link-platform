package planning

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGoalAlignmentHTTPUsesCurrentPermissionAndAssignedFacts(t *testing.T) {
	h := setup(t)
	parent, _ := contributorParent(t, h)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeGoalOn(claims(t, 2), tx, 1, parent.ID, PlanningGoalChange{Version: parent.Version, ExpectedContentHash: parent.ContentHash, Action: "ASSIGN_CONTRIBUTOR", PrincipalID: 3, Key: "delivery"})
		return e
	}))
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, h.m.RegisterHTTP(t.Context(), api))
	path := "/tenants/1/goals/" + parent.ID.String() + "/alignment"
	call := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil).WithContext(claims(t, 2)))
		return r
	}
	response := call(path)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var data struct{ Data GoalAlignment }
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &data))
	require.Contains(t, data.Data.Contributors, GoalContributor{KR: "delivery", PrincipalID: 3, Source: "ASSIGNED"})
	require.Equal(t, http.StatusUnprocessableEntity, call("/tenants/1/goals/0/alignment").Code)
	require.Equal(t, http.StatusNotFound, call("/tenants/1/goals/999/alignment").Code)
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Equal(t, http.StatusForbidden, call(path).Code)
	h.m.runtime = nil
	require.Equal(t, http.StatusServiceUnavailable, call(path).Code)
}

func TestContributorSourceFailuresLeaveNoNewGrant(t *testing.T) {
	for _, scenario := range []string{"missing_lock", "lock", "member", "policy", "denied", "missing_ids", "id", "zero_id", "delete_missing", "insert"} {
		t.Run(scenario, func(t *testing.T) {
			h := setup(t)
			parent, _ := contributorParent(t, h)
			q := query.BindGoals(h.db, h.m.names)
			r, e := q.WithContext(claims(t, 2)).Where(q.ID.Eq(parent.ID)).First()
			require.NoError(t, e)
			var body PlanningGoalInput
			require.NoError(t, json.Unmarshal(parent.Content, &body))
			in := PlanningGoalChange{Action: "ASSIGN_CONTRIBUTOR", PrincipalID: 3, Key: "delivery"}
			failure := errors.New("source unavailable")
			switch scenario {
			case "missing_lock":
				h.m.principals.LockActiveRow = nil
			case "lock":
				h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			case "member":
				h.m.members = failingMemberChecker{err: failure}
			case "policy":
				h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "denied":
				h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "missing_ids":
				h.m.ids = nil
			case "id":
				h.m.ids = failingPlanningIDs{err: failure}
			case "zero_id":
				h.m.ids = failingPlanningIDs{}
			case "delete_missing":
				in.Action = "REMOVE_CONTRIBUTOR"
			case "insert":
				require.NoError(t, h.db.Exec("CREATE TRIGGER refuse_contributor_insert BEFORE INSERT ON p_planning_goal_contributors BEGIN SELECT RAISE(ABORT, 'insert unavailable'); END").Error)
			}
			e = h.db.Transaction(func(tx *gorm.DB) error { return h.m.changeContributorOn(claims(t, 2), tx, 1, r, body, in) })
			require.Error(t, e)
			count, e := query.BindContributors(h.db, h.m.names).WithContext(claims(t, 2)).Count()
			require.NoError(t, e)
			require.Zero(t, count)
		})
	}
}

func TestChildGoalGrantsContributionOnlyWhileBothGoalsAreLive(t *testing.T) {
	h := setup(t)
	parent, child := contributorParent(t, h)
	ctx := claims(t, 2)
	q := query.BindGoals(h.db, h.m.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(child.ID)).Update(q.PrincipalID, guid.ID(3))
	require.NoError(t, err)
	r, err := q.WithContext(ctx).Where(q.ID.Eq(parent.ID)).First()
	require.NoError(t, err)
	var body PlanningGoalInput
	require.NoError(t, json.Unmarshal(parent.Content, &body))
	check := func(kr string, want bool) {
		t.Helper()
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			yes, e := h.m.contributesOn(ctx, tx, 1, r, body, 3, kr)
			require.NoError(t, e)
			require.Equal(t, want, yes)
			return nil
		}))
	}
	check("delivery", true)
	check("another-kr", false)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(child.ID)).Update(q.State, goalCancelled)
	require.NoError(t, err)
	check("delivery", false)
	r.State = goalCancelled
	check("delivery", false)
}
