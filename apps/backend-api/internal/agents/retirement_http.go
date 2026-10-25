package agents

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type RetirementCreateInput struct {
	AgentPath
	IfMatch        string `header:"If-Match" required:"true" maxLength:"24" doc:"Current Fleet Agent version, quoted or decimal."`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Cookie         string `header:"Cookie" maxLength:"8192" doc:"Browser session used by forced-retirement reauthentication."`
	Body           RetirementStart
}

type RetirementPath struct {
	TeamPath
	RetirementID string `path:"retirement_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type RetirementCancelInput struct {
	RetirementPath
	IfMatch        string `header:"If-Match" required:"true" maxLength:"24" doc:"Current retirement protocol version, quoted or decimal."`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		State                string   `json:"state,omitempty" enum:"CANCELLED"` //nolint:misspell // Preserve the published domain or serialized contract spelling.
		Advance              bool     `json:"advance,omitempty"`
		HandoverRetry        bool     `json:"handover_retry,omitempty"`
		SuccessorKind        string   `json:"successor_kind,omitempty" enum:"HUMAN,AGENT,LATER"`
		SuccessorPrincipalID *guid.ID `json:"successor_principal_id,omitempty"`
		AgentVersion         int      `json:"agent_version" minimum:"1" doc:"Current Fleet Agent version; machine ACKs can advance it independently of retirement progress."`
	}
}

func (m *Module) registerRetirementHTTP(api huma.API) error {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{401, 403, 404, 409, 422, 500, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, op("agent_retirement.create", http.MethodPost, agentPath+"/retirements", "Start a durable Agent retirement protocol", 201), m.createRetirementHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_retirement.get", http.MethodGet, "/agent-retirements/{retirement_id}", "Read authorized durable Agent retirement progress", 200), m.getRetirementHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_retirement.handover", http.MethodGet, "/agent-retirements/{retirement_id}/handover", "Read the immutable retirement handover document", 200), m.getRetirementHandoverHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_retirement.cancel", http.MethodPatch, "/agent-retirements/{retirement_id}", "Cancel retirement before work reassignment and leave the Agent stopped", 200), m.cancelRetirementHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	for _, path := range []string{agentPath + "/retirements", "/agent-retirements/{retirement_id}", "/agent-retirements/{retirement_id}/handover"} {
		if err := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) getRetirementHandoverHTTP(ctx context.Context, in *RetirementPath) (*ProtocolBody[HandoverArtifact], error) {
	tenant, id, err := in.ids()
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (HandoverArtifact, error) {
		r, err := m.retirementOn(ctx, tx, tenant, id, "agent_retire", false)
		if err != nil {
			return HandoverArtifact{}, err
		}
		if m.retirementHandover.ReadOn == nil {
			return HandoverArtifact{}, ErrUnavailable
		}
		if err := m.checkRetirementSourceReadsOn(ctx, tx, r); err != nil {
			return HandoverArtifact{}, err
		}
		return m.retirementHandover.ReadOn(ctx, tx, tenant, r.AgentPrincipalID, id)
	})
}

func (m *Module) createRetirementHTTP(ctx context.Context, in *RetirementCreateInput) (*CommandOutput[model.Retirement], error) {
	ctx = context.WithValue(ctx, proofCookieKey{}, in.Cookie)
	tenant, agent, err := pathIDs(ctx, in.AgentPath)
	version, parseErr := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if err != nil || parseErr != nil || version < 1 {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	check := func(tx *gorm.DB) error {
		_, err := m.authorize(ctx, tx, tenant, agent, "agent_retire", true)
		return err
	}
	body := in.Body
	body.ReauthProof = "" // Never persist a proof in the idempotency fingerprint.
	return agentCommand(ctx, m, in.IdempotencyKey, "agent_retirement.create", agentPath+"/retirements", map[string]string{"tenant_id": tenant.String(), "agent_principal_id": agent.String()}, struct {
		RetirementStart
		Version int
	}{body, version}, check, func(tx *gorm.DB) (model.Retirement, error) {
		return m.startRetirementOn(ctx, tx, tenant, agent, version, in.Body)
	})
}

func (in RetirementPath) ids() (guid.ID, guid.ID, error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return 0, 0, ErrInvalid
	}
	id, err := guid.RequireID(in.RetirementID)
	if err != nil {
		return 0, 0, ErrInvalid
	}
	return tenant, id, nil
}

func (m *Module) getRetirementHTTP(ctx context.Context, in *RetirementPath) (*ProtocolBody[model.Retirement], error) {
	tenant, id, err := in.ids()
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (model.Retirement, error) {
		r, err := m.retirementOn(ctx, tx, tenant, id, "agent_read", false)
		if err != nil {
			return model.Retirement{}, err
		}
		if err := m.checkRetirementSourceReadsOn(ctx, tx, r); err != nil {
			return model.Retirement{}, err
		}
		return *r, nil
	})
}

func (m *Module) cancelRetirementHTTP(ctx context.Context, in *RetirementCancelInput) (*CommandOutput[model.Retirement], error) {
	tenant, id, err := in.ids()
	version, parseErr := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	actions := 0
	if in.Body.State == "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		actions++
	}
	if in.Body.Advance {
		actions++
	}
	if in.Body.HandoverRetry {
		actions++
	}
	if in.Body.SuccessorKind != "" {
		actions++
	}
	if err != nil || parseErr != nil || version < 1 || actions != 1 || (in.Body.SuccessorKind == "" && in.Body.SuccessorPrincipalID != nil) || (in.Body.State != "" && in.Body.State != "CANCELLED") || in.Body.AgentVersion < 1 { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	check := func(tx *gorm.DB) error {
		if in.Body.SuccessorKind != "" {
			target := guid.ID(0)
			if in.Body.SuccessorPrincipalID != nil {
				target = *in.Body.SuccessorPrincipalID
			}
			if err := m.prepareRetirementTransferOn(ctx, tx, tenant, id, target); err != nil {
				return err
			}
		} else if in.Body.Advance {
			if err := m.prepareRetirementAdvanceOn(ctx, tx, tenant, id); err != nil {
				return err
			}
		} else if in.Body.HandoverRetry {
			if err := m.prepareRetirementHandoverOn(ctx, tx, tenant, id); err != nil {
				return err
			}
		}
		r, err := m.retirementOn(ctx, tx, tenant, id, "agent_retire", false)
		if err != nil {
			return err
		}
		return m.checkRetirementSourceReadsOn(ctx, tx, r)
	}
	return agentCommand(ctx, m, in.IdempotencyKey, "agent_retirement.cancel", "/agent-retirements/{retirement_id}", map[string]string{"tenant_id": tenant.String(), "retirement_id": id.String()}, struct {
		Version int
		Body    any
	}{version, in.Body}, check, func(tx *gorm.DB) (model.Retirement, error) {
		if in.Body.SuccessorKind != "" {
			return m.selectRetirementSuccessorOn(ctx, tx, tenant, id, version, in.Body.SuccessorKind, in.Body.SuccessorPrincipalID)
		}
		if in.Body.Advance {
			return m.advanceRetirementOn(ctx, tx, tenant, id, version, in.Body.AgentVersion)
		}
		if in.Body.HandoverRetry {
			return m.advanceRetirementHandoverOn(ctx, tx, tenant, id, version, in.Body.HandoverRetry)
		}
		return m.cancelRetirementOn(ctx, tx, tenant, id, version, in.Body.AgentVersion)
	})
}
