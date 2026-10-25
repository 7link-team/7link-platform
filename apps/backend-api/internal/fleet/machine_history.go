package fleet

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/base64"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"strings"
	"time"
)

// MachineExecutionHistoryConnector is filled by the execution owner. It must
// authorize each current project and return only credential-free summaries.
type MachineExecutionHistoryConnector struct {
	Bind func(MachineExecutionHistoryReader) error
}
type MachineExecutionHistoryReader func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (MachineExecutionHistoryPage, error)
type MachineExecutionHistoryEntry struct {
	ID          guid.ID   `json:"id"`
	ProjectID   guid.ID   `json:"project_id"`
	WorkItemID  guid.ID   `json:"work_item_id"`
	State       string    `json:"state"`
	ResultState string    `json:"result_state"`
	Runtime     string    `json:"runtime"`
	Model       string    `json:"model"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type MachineExecutionHistoryPage struct {
	Items      []MachineExecutionHistoryEntry `json:"items"`
	HasMore    bool                           `json:"has_more"`
	NextCursor string                         `json:"next_cursor,omitempty"`
}
type machineHistoryInput struct {
	MachineInput
	Cursor string `query:"cursor" maxLength:"256"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}

func (m *Module) provideMachineHistory(s *module.Services) error {
	return module.Provide[MachineExecutionHistoryConnector](s, MachineExecutionHistoryConnector{Bind: func(reader MachineExecutionHistoryReader) error {
		if reader == nil || m.machineHistory != nil {
			return ErrUnavailable
		}
		m.machineHistory = reader
		return nil
	}})
}
func (m *Module) registerMachineHistoryHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.execution-history.list", Method: "GET", Path: "/machines/{machine_id}/execution-history", Summary: "Browse authorized execution history on a machine", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 422, 503}}, "machine_id", m.machineOwner, authz.Guard{Resource: "machine", Verb: "read", FilterData: true}, m.machineHistoryHTTP)
}
func (m *Module) machineHistoryHTTP(ctx context.Context, in *machineHistoryInput) (*respx.Body[MachineExecutionHistoryPage], error) {
	id, err := guid.RequireID(in.MachineID)
	if err != nil || in.Limit < 1 || in.Limit > 100 {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	tenant := authn.TenantIDFromContext(ctx)
	scope := fmt.Sprintf("%s:%s:%s:", tenant, authn.PrincipalIDFromContext(ctx), id)
	after := guid.ID(0)
	if in.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(in.Cursor)
		if e != nil || !strings.HasPrefix(string(raw), scope) {
			return nil, fleetHTTPError(ctx, ErrInvalidArgument)
		}
		after, e = guid.RequireID(strings.TrimPrefix(string(raw), scope))
		if e != nil {
			return nil, fleetHTTPError(ctx, ErrInvalidArgument)
		}
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	out := MachineExecutionHistoryPage{Items: []MachineExecutionHistoryEntry{}}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.controlAuthorityOn(ctx, tx, tenant, "machine_read"); e != nil {
			return e
		}
		if _, e := m.machineOn(ctx, tx, tenant, id, false); e != nil {
			return e
		}
		if m.machineHistory == nil {
			return ErrUnavailable
		}
		var e error
		out, e = m.machineHistory(ctx, tx, tenant, id, after, in.Limit)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	if out.HasMore {
		out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(scope + out.NextCursor))
	}
	return respx.OK(ctx, out), nil
}
