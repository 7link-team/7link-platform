package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"database/sql/driver"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"time"
)

type scheduleInput struct {
	ResultWorkInput
	Key  string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body scheduleBody
}
type calendarInput struct {
	EntityID     string    `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Start        time.Time `query:"start"`
	End          time.Time `query:"end"`
	Unscheduled  bool      `query:"unscheduled"`
	AssigneeKind string    `query:"assignee_kind" enum:"HUMAN,AGENT"`
	AssigneeID   guid.ID   `query:"assignee_id" minimum:"0"`
	Offset       int       `query:"offset" minimum:"0" default:"0"`
	Limit        int       `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

func idValues(ids []guid.ID) []driver.Valuer {
	out := make([]driver.Valuer, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}
func (m *Module) registerScheduleHTTP(api huma.API) {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.schedule", Method: http.MethodPut, Path: workPath + "/{work_item_id}/schedule", Summary: "Change scheduled dates with version and reason", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "write"}, m.scheduleWorkHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.calendar", Method: http.MethodGet, Path: workPath + "/calendar", Summary: "Read authorized scheduled or unscheduled work", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.calendarHTTP)
}
func (m *Module) scheduleWorkHTTP(ctx context.Context, in *scheduleInput) (*deliveryCommandOutput[WorkView], error) {
	tenant, project, id, actor, owner, err := m.consumptionContext(ctx, in.ResultWorkInput)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "work_item", workHTTPError, "work_item.schedule", http.MethodPut, workPath+"/{work_item_id}/schedule", in.Key, 200, in.Body, map[string]string{"entity_id": project.String(), "work_item_id": id.String()}, func(tx *gorm.DB) (WorkView, guid.ID, error) {
		v, e := m.scheduleWorkOn(ctx, tx, tenant, project, id, actor, owner, in.Body)
		return v, id, e
	}, func(tx *gorm.DB) error {
		_, _, _, e := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "work_item_write")
		return e
	})
}
func (m *Module) calendarHTTP(ctx context.Context, in *calendarInput) (*respx.Body[[]calendarRow], error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	var rows []calendarRow
	var total int64
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		rows, total, e = m.calendarRowsOn(ctx, tx, authn.TenantIDFromContext(ctx), project, authn.PrincipalIDFromContext(ctx), in)
		return e
	})
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return respx.List(ctx, rows, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(rows), Total: total}), nil
}
