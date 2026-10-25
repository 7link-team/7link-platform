package planning

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type GoalTreeInput struct {
	PlanningTenantInput
	Cursor string `query:"cursor" maxLength:"512"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Sort   string `query:"sort" enum:"id" default:"id"`
}
type GoalTreeRow struct {
	PlanningRecord
	Origin  string  `json:"origin" doc:"Current owner identity: HUMAN, HUMAN_CREATED, AGENT_SPAWNED, or empty when unavailable."`
	Elapsed float64 `json:"elapsed" minimum:"0" maximum:"1" doc:"Elapsed fraction of configured tenant workdays in the goal period."`
}
type GoalTreePage struct {
	Items      []GoalTreeRow `json:"items"`
	HasMore    bool          `json:"has_more"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// goalTreeOn returns only currently readable goals, applying the visible limit
// after authorization. Parent IDs are already part of each authorized goal;
// hidden parent content is never hydrated or inferred.
func (m *Module) goalTreeOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in GoalTreeInput) (GoalTreePage, error) {
	out := GoalTreePage{Items: []GoalTreeRow{}}
	if tenant <= 0 || tenant != authn.TenantIDFromContext(ctx) || authn.PrincipalIDFromContext(ctx) <= 0 || in.Limit < 1 || in.Limit > 100 {
		return out, ErrInvalid
	}
	scope := fmt.Sprintf("goal-tree:%s:%s", tenant, authn.PrincipalIDFromContext(ctx))
	after, err := cursorPosition(in.Cursor, scope)
	if err != nil {
		return out, err
	}
	settings, err := m.readSettingsOn(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	q := query.BindGoals(tx, m.names)
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(after)).Order(q.ID).Limit(100).Find()
		if err != nil {
			return out, err
		}
		for _, r := range rows {
			after = r.ID
			if _, err = m.authorize(ctx, tx, tenant, r.ProjectID, r.PrincipalID, "goal_read", false); errors.Is(err, ErrForbidden) {
				continue
			} else if err != nil {
				return out, err
			}
			if len(out.Items) == in.Limit {
				out.HasMore = true
				out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(out.Items[len(out.Items)-1].ID.String() + ":" + scope))
				return out, nil
			}
			v, err := goalView(r)
			if err != nil {
				return out, err
			}
			origin := ""
			_, human, err := m.principals.ActiveHumanByID(ctx, tx, r.PrincipalID)
			if err != nil {
				return out, err
			}
			if human {
				origin = "HUMAN"
			} else if m.agents.ByPrincipalOn != nil {
				a, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, r.PrincipalID)
				if e != nil {
					return out, e
				}
				if found {
					origin = a.Origin
				}
			}
			start, end, err := settingsPeriod(r.PeriodKind, r.PeriodStart, settings.Timezone)
			if err != nil {
				return out, err
			}
			out.Items = append(out.Items, GoalTreeRow{PlanningRecord: v, Origin: origin, Elapsed: goalWorkdayElapsed(start, end, m.now(), settings.Workdays)})
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
}
func goalWorkdayElapsed(start, end, now time.Time, workdays []int) float64 {
	value := elapsedWorkdays(start, end, now, start.Location(), workdays)
	if value == nil {
		return 0
	}
	return *value
}
func (m *Module) registerGoalTreeHTTP(api huma.API) {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "planning.goal.tree", Method: http.MethodGet, Path: "/goals/tree", Summary: "Page currently authorized goals across scopes for the goal tree", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}, func(ctx context.Context, in *GoalTreeInput) (*respx.Body[GoalTreePage], error) {
		tenant, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, planningError(ctx, ErrInvalid)
		}
		db, err := m.database()
		if err != nil {
			return nil, planningError(ctx, err)
		}
		var out GoalTreePage
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = m.goalTreeOn(ctx, tx, tenant, *in); return e })
		if err != nil {
			return nil, planningError(ctx, err)
		}
		return respx.OK(ctx, out), nil
	})
}
