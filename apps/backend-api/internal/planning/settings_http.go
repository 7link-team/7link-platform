package planning

import (
	"7link.com/platform/internal/infra/guid"
	p "7link.com/platform/internal/planning/i18n"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"strings"
)

type PlanningSettingsOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[PlanningSettings]
}
type PlanningSettingsPatchInput struct {
	PlanningTenantInput
	IfMatch string `header:"If-Match" required:"true" doc:"Quoted current settings version, initially 1."`
	Body    PlanningSettingsPatch
}

func (m *Module) registerSettingsHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "planning.settings.get", Method: http.MethodGet, Path: "/planning-settings", Summary: "Get team planning calendar and report settings", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}, authz.Guard{Resource: "goal", Verb: "read"}, m.getPlanningSettingsHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "planning.settings.patch", Method: http.MethodPatch, Path: "/planning-settings", Summary: "Update versioned team planning settings", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 409, 422, 500, 503}}, authz.Guard{Resource: "planning_settings", Verb: "manage"}, m.patchPlanningSettingsHTTP)
}
func (m *Module) getPlanningSettingsHTTP(ctx context.Context, in *PlanningTenantInput) (*PlanningSettingsOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	db, e := m.database()
	if e != nil {
		return nil, planningError(ctx, e)
	}
	var value PlanningSettings
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.authorize(ctx, tx, tenant, 0, 0, "goal_read", false); e != nil {
			return e
		}
		var e error
		value, e = m.readSettingsOn(ctx, tx, tenant)
		return e
	})
	if e != nil {
		return nil, planningError(ctx, e)
	}
	return &PlanningSettingsOutput{ETag: fmt.Sprintf("\"%d\"", value.Version), Body: respx.OK(ctx, value).Body}, nil
}
func (m *Module) patchPlanningSettingsHTTP(ctx context.Context, in *PlanningSettingsPatchInput) (*PlanningSettingsOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	version, e := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if e != nil || version < 1 || in.IfMatch != fmt.Sprintf("\"%d\"", version) {
		return nil, planningError(ctx, ErrInvalid)
	}
	db, e := m.database()
	if e != nil {
		return nil, planningError(ctx, e)
	}
	var value PlanningSettings
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		value, e = m.patchSettingsOn(ctx, tx, tenant, version, in.Body)
		return e
	})
	if e != nil {
		if errors.Is(e, ErrInvalid) {
			return nil, respx.ErrStatus(ctx, 422, 180440, p.Invalid, nil)
		}
		return nil, planningError(ctx, e)
	}
	return &PlanningSettingsOutput{ETag: fmt.Sprintf("\"%d\"", value.Version), Body: respx.OK(ctx, value).Body}, nil
}
