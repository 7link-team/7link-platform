package agents

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RunStateInput struct {
	AgentPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Desired string `json:"desired" enum:"ACTIVE,STOPPED"`
	}
}
type ProcessHistoryInput struct {
	AgentPath
	Limit  int       `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset int       `query:"offset" default:"0" minimum:"0"`
	Search string    `query:"search" maxLength:"200"`
	Kind   string    `query:"kind" maxLength:"200"`
	From   time.Time `query:"from"`
	To     time.Time `query:"to"`
}
type ProcessHistory struct {
	Items   []model.ProcessEvent `json:"items"`
	HasMore bool                 `json:"has_more"`
}

func (m *Module) observeProcessOn(ctx context.Context, tx *gorm.DB, in fleet.AgentProcessTransition) error {
	if m.ids == nil || in.TenantID <= 0 || in.AgentPrincipalID <= 0 || in.OccurredAt.IsZero() || !slices.Contains([]string{"START_REQUESTED", "STOP_REQUESTED", "RESTART_REQUESTED", "STARTED", "STOPPED", "CRASHED", "AUTO_RESTART_SCHEDULED", "RESTART_EXHAUSTED"}, in.Kind) {
		return ErrInvalid
	}
	id, e := m.ids.Next()
	if e != nil {
		return e
	}
	if id <= 0 {
		return ErrUnavailable
	}
	return query.BindProcessEvents(tx, m.names).WithContext(ctx).Create(&model.ProcessEvent{ID: guid.ID(id), TenantID: in.TenantID, AgentPrincipalID: in.AgentPrincipalID, Kind: in.Kind, MachineID: in.MachineID, PID: in.PID, RestartCount: in.RestartCount, Reason: in.Reason, OccurredAt: in.OccurredAt}).Error
}
func (m *Module) runStateOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, desired string) (fleet.AgentRunState, error) {
	if _, e := m.authorize(ctx, tx, tenant, agent, "agent_manage", true); e != nil {
		return fleet.AgentRunState{}, e
	}
	if m.profiles.RunStateOn == nil {
		return fleet.AgentRunState{}, ErrUnavailable
	}
	v, e := m.profiles.RunStateOn(ctx, tx, tenant, agent, desired)
	if e != nil {
		return v, resetRuntimeError(profileError(e))
	}
	if e = m.record(ctx, tx, tenant, agent, "RUN_STATE_"+desired, true); e != nil {
		return fleet.AgentRunState{}, e
	}
	return v, nil
}
func (m *Module) processHistoryOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, in ProcessHistoryInput) (ProcessHistory, error) {
	out := ProcessHistory{Items: []model.ProcessEvent{}}
	if in.Limit < 1 || in.Limit > 200 || in.Offset < 0 || !validProcessSearch(in.Search) {
		return out, ErrInvalid
	}
	if _, e := m.authorize(ctx, tx, tenant, agent, "agent_read", true); e != nil {
		return out, e
	}
	q := query.BindProcessEvents(tx, m.names).WithContext(ctx).Where(&model.ProcessEvent{TenantID: tenant, AgentPrincipalID: agent})
	if !in.From.IsZero() && !in.To.IsZero() && in.From.After(in.To) {
		return out, ErrInvalid
	}
	if !in.From.IsZero() {
		q = q.Where(clause.Gte{Column: clause.Column{Name: "occurred_at"}, Value: in.From})
	}
	if !in.To.IsZero() {
		q = q.Where(clause.Lte{Column: clause.Column{Name: "occurred_at"}, Value: in.To})
	}
	for _, kind := range strings.Split(in.Kind, ",") {
		if kind != "" && !slices.Contains([]string{"START_REQUESTED", "STOP_REQUESTED", "RESTART_REQUESTED", "STARTED", "STOPPED", "CRASHED", "AUTO_RESTART_SCHEDULED", "RESTART_EXHAUSTED"}, kind) {
			return out, ErrInvalid
		}
	}
	// Kind names are the stable, localizable searchable event vocabulary.
	kinds := []string{}
	for _, kind := range []string{"START_REQUESTED", "STOP_REQUESTED", "RESTART_REQUESTED", "STARTED", "STOPPED", "CRASHED", "AUTO_RESTART_SCHEDULED", "RESTART_EXHAUSTED"} {
		if (in.Kind == "" || slices.Contains(strings.Split(in.Kind, ","), kind)) && strings.Contains(strings.ToLower(kind), strings.ToLower(strings.TrimSpace(in.Search))) {
			kinds = append(kinds, kind)
		}
	}
	if len(kinds) == 0 {
		return out, nil
	}
	q = q.Where(map[string]any{"kind": kinds})
	if e := q.Order(clause.OrderByColumn{Column: clause.Column{Name: "occurred_at"}, Desc: true}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: true}).Offset(in.Offset).Limit(in.Limit + 1).Find(&out.Items).Error; e != nil {
		return out, e
	}
	if len(out.Items) > in.Limit {
		out.HasMore = true
		out.Items = out.Items[:in.Limit]
	}
	return out, nil
}
func validProcessSearch(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= 200
}
func (m *Module) registerProcessHTTP(api huma.API) {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "agent_run_state.update", Method: http.MethodPut, Path: agentPath + "/run-state", Summary: "Request Agent start or stop through the current fenced Runner", Tags: []string{"tenants"}, DefaultStatus: 202, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, m.runStateHTTP)
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "agent_process_events.list", Method: http.MethodGet, Path: agentPath + "/process-events", Summary: "Read actual Agent process observations and lifecycle requests", Tags: []string{"tenants"}, Errors: []int{401, 403, 404, 422, 500, 503}}, m.processHistoryHTTP)
}
func (m *Module) runStateHTTP(ctx context.Context, in *RunStateInput) (*CommandOutput[fleet.AgentRunState], error) {
	t, a, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return agentCommand(ctx, m, in.IdempotencyKey, "agent_run_state.update", agentPath+"/run-state", agentParams(in.AgentPath), in.Body, func(tx *gorm.DB) error { _, e := m.authorize(ctx, tx, t, a, "agent_manage", true); return e }, func(tx *gorm.DB) (fleet.AgentRunState, error) { return m.runStateOn(ctx, tx, t, a, in.Body.Desired) })
}
func (m *Module) processHistoryHTTP(ctx context.Context, in *ProcessHistoryInput) (*ProtocolBody[ProcessHistory], error) {
	t, a, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (ProcessHistory, error) { return m.processHistoryOn(ctx, tx, t, a, *in) })
}
