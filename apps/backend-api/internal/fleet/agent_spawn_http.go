package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type agentSpawnHTTPInput struct {
	Key  string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body AgentSpawnInput
}
type agentSpawnRetireHTTPInput struct {
	AgentID string `path:"agent_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key     string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body    struct {
		Version int    `json:"version" minimum:"1"`
		Reason  string `json:"reason" minLength:"10" maxLength:"2000"`
	}
}
type agentSpawnHTTPOutput struct {
	Status   int
	Replayed string `header:"Idempotency-Replayed"`
	Body     respx.Envelope[RegisteredAgent]
}

func (m *Module) registerAgentSpawnHTTP(api huma.API) {
	// Policy-only spawning has no role-granted endpoint guard. Current IAM,
	// source account and bounded grant checks run inside the command transaction.
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "agent.spawn", Method: http.MethodPost, Path: "/agent-spawns", DefaultStatus: 201, Summary: "Create an Agent using a current automatic spawn grant", Tags: []string{"agents"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, m.agentSpawnHTTP)
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "agent.spawn.retire", Method: http.MethodPost, Path: "/agent-spawns/{agent_id}/retirements", Summary: "Retire an idle child owned by the current supervisor", Tags: []string{"agents"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, m.agentSpawnRetireHTTP)
}

func (m *Module) agentSpawnCommand(ctx context.Context, key, operation string, status int, body any, params map[string]string, authorize func(*gorm.DB) error, run func(*gorm.DB) (RegisteredAgent, error)) (*agentSpawnHTTPOutput, error) {
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	if m.idempotency == nil {
		return nil, fleetHTTPError(ctx, ErrUnavailable)
	}
	digest, err := idempotency.HashRequest(http.MethodPost, operation, params, body)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	var result idempotency.Result
	var replay bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := authorize(tx); err != nil {
			return err
		}
		var err error
		result, replay, err = m.idempotency.Do(ctx, tx, idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: actor, Operation: operation, Value: key, RequestHash: digest}, func(tx *gorm.DB) (idempotency.Result, error) {
			v, err := run(tx)
			if err != nil {
				return idempotency.Result{}, err
			}
			data, err := json.Marshal(v)
			return idempotency.Result{StatusCode: status, Body: data, ResourceKind: "agent", ResourceID: v.ID}, err
		})
		return err
	})
	if errors.Is(err, idempotency.ErrKeyReused) || errors.Is(err, idempotency.ErrInProgress) {
		err = ErrVersionConflict
	}
	if errors.Is(err, idempotency.ErrInvalidKey) {
		err = ErrInvalidArgument
	}
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var value RegisteredAgent
	if json.Unmarshal(result.Body, &value) != nil {
		return nil, fleetHTTPError(ctx, ErrUnavailable)
	}
	out := &agentSpawnHTTPOutput{Status: status, Body: respx.OK(ctx, value).Body}
	if replay {
		out.Replayed = "true"
	}
	return out, nil
}

func (m *Module) agentSpawnHTTP(ctx context.Context, in *agentSpawnHTTPInput) (*agentSpawnHTTPOutput, error) {
	tenant := authn.TenantIDFromContext(ctx)
	return m.agentSpawnCommand(ctx, in.Key, "agent.spawn", 201, in.Body, nil, func(tx *gorm.DB) error {
		_, err := m.agentSpawnAuthorityOn(ctx, tx, tenant, in.Body, true)
		return err
	}, func(tx *gorm.DB) (RegisteredAgent, error) { return m.SpawnAgentOn(ctx, tx, tenant, in.Body) })
}

func (m *Module) agentSpawnRetireHTTP(ctx context.Context, in *agentSpawnRetireHTTPInput) (*agentSpawnHTTPOutput, error) {
	id, err := guid.RequireID(in.AgentID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	tenant := authn.TenantIDFromContext(ctx)
	return m.agentSpawnCommand(ctx, in.Key, "agent.spawn.retire", 200, in.Body, map[string]string{"agent_id": id.String()}, func(tx *gorm.DB) error {
		_, err := m.agentSpawnRetirementAuthorityOn(ctx, tx, tenant, id)
		return err
	}, func(tx *gorm.DB) (RegisteredAgent, error) {
		return m.RetireSpawnedAgentOn(ctx, tx, tenant, id, in.Body.Version, in.Body.Reason)
	})
}
