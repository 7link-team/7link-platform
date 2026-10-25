package planning

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGoalTreePagesAuthorizedScopesAndCurrentRevocation(t *testing.T) {
	h := setup(t)
	leaf := goalTree(t, h)
	read := func(cursor string) (GoalTreePage, error) {
		var out GoalTreePage
		err := h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = h.m.goalTreeOn(claims(t, 2), tx, 1, GoalTreeInput{Cursor: cursor, Limit: 1})
			return e
		})
		return out, err
	}
	first, err := read("")
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	require.True(t, first.HasMore)
	require.Equal(t, "HUMAN", first.Items[0].Origin)
	second, err := read(first.NextCursor)
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.True(t, second.HasMore)
	last, err := read(second.NextCursor)
	require.NoError(t, err)
	require.Len(t, last.Items, 1)
	require.Equal(t, leaf.ID, last.Items[0].ID)
	require.False(t, last.HasMore)
	require.NotEqual(t, first.Items[0].ID, second.Items[0].ID)
	_, err = read("bad")
	require.ErrorIs(t, err, ErrInvalid)
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	empty, err := read(first.NextCursor)
	require.NoError(t, err)
	require.Empty(t, empty.Items)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, h.m.RegisterHTTP(t.Context(), api))
	req := httptest.NewRequest(http.MethodGet, "/tenants/1/goals/tree?limit=1&sort=id", nil).WithContext(claims(t, 2))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "HUMAN")
	req = httptest.NewRequest(http.MethodGet, "/tenants/1/goals/tree?limit=101", nil).WithContext(claims(t, 2))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, 422, rec.Code)
}
func TestGoalWorkdayElapsedUsesLocalCalendarAndCompletedDays(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 7)
	days := []int{1, 2, 3, 4, 5}
	require.Zero(t, goalWorkdayElapsed(start, end, start.Add(-time.Hour), days))
	require.Equal(t, 0.2, goalWorkdayElapsed(start, end, start.AddDate(0, 0, 1), days)) //nolint:testifylint // Exact numeric equality is frozen by this regression contract.
	require.Equal(t, 1.0, goalWorkdayElapsed(start, end, start.AddDate(0, 0, 5), days)) //nolint:testifylint // Exact numeric equality is frozen by this regression contract.
	require.Equal(t, 1.0, goalWorkdayElapsed(start, end, end.AddDate(0, 0, 8), days))   //nolint:testifylint // Exact numeric equality is frozen by this regression contract.
	require.Zero(t, goalWorkdayElapsed(start, end, end, nil))
	require.Zero(t, goalWorkdayElapsed(start, end, start.AddDate(0, 0, 5), []int{7}))
}

func TestGoalTreeRejectsUnavailableSourcesAndReportsCurrentAgentOrigin(t *testing.T) {
	for _, mode := range []string{"agent", "agent_error", "human_error", "query", "settings", "content", "period", "authority"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			g := goalTree(t, h)
			require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("principal_id", 7).Error)
			switch mode {
			case "agent", "agent_error", "human_error":
				old := h.m.principals.ActiveHumanByID
				h.m.principals.ActiveHumanByID = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					if id == 7 {
						if mode == "human_error" {
							return identity.Principal{}, false, ErrUnavailable
						}
						return identity.Principal{}, false, nil
					}
					return old(ctx, tx, id)
				}
				h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					if mode == "agent_error" {
						return fleet.AgentIdentity{}, false, ErrUnavailable
					}
					return fleet.AgentIdentity{TenantID: 1, State: "ACTIVE", Origin: "AGENT_SPAWNED", PrincipalID: 7}, true, nil
				}
			case "query", "settings":
				table := h.m.names.Goals.String()
				if mode == "settings" {
					table = h.m.names.Settings.String()
				}
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("goal_tree_read", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "content":
				require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("content", "[").Error)
			case "period":
				require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("period_start", "bad").Error)
			case "authority":
				h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrUnavailable
				}
			}
			var out GoalTreePage
			err := h.db.Transaction(func(tx *gorm.DB) error {
				var e error
				out, e = h.m.goalTreeOn(claims(t, 2), tx, 1, GoalTreeInput{Limit: 100})
				return e
			})
			require.NoError(t, h.db.Callback().Query().Remove("goal_tree_read"))
			if mode == "agent" {
				require.NoError(t, err)
				require.Len(t, out.Items, 3)
				require.Equal(t, "AGENT_SPAWNED", out.Items[2].Origin)
			} else {
				require.Error(t, err)
			}
		})
	}
	h := setup(t)
	_, err := h.m.goalTreeOn(claims(t, 2), h.db, 1, GoalTreeInput{Limit: 0})
	require.ErrorIs(t, err, ErrInvalid)
}
