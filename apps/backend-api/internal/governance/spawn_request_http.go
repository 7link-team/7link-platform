package governance

import (
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

type agentSpawnRequestCreateInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body     AgentSpawnRequest
}
type agentSpawnRequestChangeInput struct {
	TenantID  string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	RequestID string `path:"request_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key       string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body      struct {
		Version int    `json:"version" minimum:"1"`
		Action  string `json:"action" enum:"CREATE,ADOPT,REJECT,ORPHAN"`
		Reason  string `json:"reason" minLength:"10" maxLength:"2000"`
	}
}
type agentSpawnRequestListInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
}
type agentSpawnRequestOutput struct {
	Status   int
	Replayed string `header:"Idempotency-Replayed"`
	Body     respx.Envelope[model.SpawnRequest]
}

func (m *Module) registerAgentSpawnRequestHTTP(api huma.API) {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, op("governance.spawn_request.create", http.MethodPost, "/agent-spawn-requests", "Request an Agent under a current spawn grant", 201), m.createAgentSpawnRequestHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("governance.spawn_request.change", http.MethodPut, "/agent-spawn-requests/{request_id}/state", "Create, adopt or resolve an Agent request", 200), m.changeAgentSpawnRequestHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("governance.spawn_request.list", http.MethodGet, "/agent-spawn-requests", "List authorized Agent requests", 200), m.listAgentSpawnRequestsHTTP)
}
func (m *Module) agentSpawnRequestCommand(ctx context.Context, tenant guid.ID, key, operation string, body any, authorize func(*gorm.DB) error, run func(*gorm.DB) (model.SpawnRequest, error)) (*agentSpawnRequestOutput, error) {
	db, e := m.writer()
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	if m.delegations.replay == nil {
		return nil, spawnHTTPError(ctx, ErrUnavailable)
	}
	payload, e := json.Marshal(body)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	k := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: key, RequestHash: sha256.Sum256(payload)}
	if e = k.Validate(); e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	var result idempotency.Result
	var replay bool
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if authn.TenantIDFromContext(ctx) != tenant {
			return ErrForbidden
		}
		if e := authorize(tx); e != nil {
			return e
		}
		var e error
		result, replay, e = m.delegations.replay.Do(ctx, tx, k, func(work *gorm.DB) (idempotency.Result, error) {
			row, e := run(work)
			if e != nil {
				return idempotency.Result{}, e
			}
			data, e := json.Marshal(row)
			return idempotency.Result{StatusCode: 200, Body: data, ResourceKind: "spawn_request", ResourceID: row.ID}, e
		})
		return e
	})
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	var row model.SpawnRequest
	if e = json.Unmarshal(result.Body, &row); e != nil {
		return nil, spawnHTTPError(ctx, ErrUnavailable)
	}
	out := &agentSpawnRequestOutput{Status: 200, Body: respx.OK(ctx, row).Body}
	if replay {
		out.Replayed = "true"
	}
	return out, nil
}
func (m *Module) createAgentSpawnRequestHTTP(ctx context.Context, in *agentSpawnRequestCreateInput) (*agentSpawnRequestOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	out, e := m.agentSpawnRequestCommand(ctx, tenant, in.Key, "governance.spawn_request.create", in.Body, func(tx *gorm.DB) error {
		// A replay allocates no additional quota. The actual creation below still
		// performs the full checker, including all existing pending requests.
		a, e := m.checkSpawnAuthorityUsage(ctx, tx, AuthorityRequest{TenantID: tenant, PrincipalID: authn.PrincipalIDFromContext(ctx), ActorKind: "AGENT", Code: "agent_spawn", Spawn: &in.Body.Facts}, true)
		if e != nil {
			return e
		}
		if !a.Allowed {
			return ErrForbidden
		}
		return nil
	}, func(tx *gorm.DB) (model.SpawnRequest, error) {
		return m.createAgentSpawnRequestOn(ctx, tx, tenant, in.Body)
	})
	if out != nil {
		out.Status = 201
	}
	return out, e
}
func (m *Module) changeAgentSpawnRequestHTTP(ctx context.Context, in *agentSpawnRequestChangeInput) (*agentSpawnRequestOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	id, r := guid.RequireID(in.RequestID)
	if e != nil || r != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	return m.agentSpawnRequestCommand(ctx, tenant, in.Key, "governance.spawn_request.change:"+id.String(), in.Body, func(tx *gorm.DB) error { return m.spawnRequestHumanOn(ctx, tx, tenant, false) }, func(tx *gorm.DB) (model.SpawnRequest, error) {
		return m.changeAgentSpawnRequestOn(ctx, tx, tenant, id, in.Body.Version, in.Body.Action, in.Body.Reason)
	})
}
func (m *Module) listAgentSpawnRequestsHTTP(ctx context.Context, in *agentSpawnRequestListInput) (*respx.Body[[]model.SpawnRequest], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, spawnHTTPError(ctx, ErrInvalidArgument)
	}
	db, e := m.writer()
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	out := []model.SpawnRequest{}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.spawnRequestHumanOn(ctx, tx, tenant, true); e != nil {
			return e
		}
		q := query.BindSpawnRequests(tx, m.seed.names)
		var e error
		outRows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant)).Order(q.ID.Desc()).Limit(in.Limit).Offset(in.Offset).Find()
		if e != nil {
			return e
		}
		for _, row := range outRows {
			out = append(out, *row)
		}
		return nil
	})
	if e != nil {
		return nil, spawnHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
