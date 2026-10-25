package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type WakeHTTPInput struct {
	AgentPath
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted configuration version returned by GET wake-config."`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           WakeConfigInput
}

func (m *Module) registerWakeHTTP(api huma.API) {
	op := func(id, method, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: agentPath + "/wake-config", Summary: summary, Tags: []string{"tenants"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, op("agent_wake.read", http.MethodGet, "Read the current content-free wake configuration"), m.wakeReadHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_wake.configure", http.MethodPut, "Configure signed content-free Agent webhook wake hints"), m.wakeConfigureHTTP)
}
func (m *Module) wakeReadHTTP(ctx context.Context, in *AgentPath) (*ProtocolBody[WakeConfigView], error) {
	tenant, agent, err := pathIDs(ctx, *in)
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (WakeConfigView, error) { return m.wakeConfigOn(ctx, tx, tenant, agent) })
}
func (m *Module) wakeConfigureHTTP(ctx context.Context, in *WakeHTTPInput) (*CommandOutput[WakeConfigView], error) {
	tenant, agent, err := pathIDs(ctx, in.AgentPath)
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	version, err := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if err != nil || version < 1 {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	db, err := m.database()
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	if m.replay == nil {
		return nil, agentHTTPError(ctx, ErrUnavailable)
	}
	digest, err := idempotency.HashRequest(http.MethodPut, "agent_wake.configure", map[string]string{"agent": agent.String(), "version": strconv.Itoa(version)}, in.Body)
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: actorID(ctx), Operation: "agent_wake.configure", Value: in.IdempotencyKey, RequestHash: digest}
	var value WakeConfigView
	var secret string
	var replayed bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Replays are still gated by current Human authority, without rotating or
		// revealing a secret for the second time.
		if _, e := m.authorize(ctx, tx, tenant, agent, "agent_manage", true); e != nil {
			return e
		}
		receipt, replay, e := m.replay.Do(ctx, tx, key, func(tx *gorm.DB) (idempotency.Result, error) {
			v, s, e := m.setWakeConfigOn(ctx, tx, tenant, agent, version, in.Body)
			if e != nil {
				return idempotency.Result{}, e
			}
			secret = s
			body, e := json.Marshal(v)
			return idempotency.Result{StatusCode: 200, Body: body}, e
		})
		if e != nil {
			return e
		}
		replayed = replay
		return json.Unmarshal(receipt.Body, &value)
	})
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	if !replayed {
		value.Secret = secret
	}
	out := &CommandOutput[WakeConfigView]{Body: protocolOK(ctx, value).Body}
	if replayed {
		out.Replayed = "true"
	}
	return out, nil
}
