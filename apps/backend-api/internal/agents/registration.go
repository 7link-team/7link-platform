package agents

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type registrationServices struct {
	identity identity.AgentIdentityRegistration
	fleet    fleet.AgentProvisioner
	skills   knowledge.SkillMounter
}

// AgentCreation never accepts an existing principal or credential. Both are
// created by their owners and committed with the complete Agent configuration.
type AgentCreation struct {
	MachineID         guid.ID   `json:"machine_id"`
	Name              string    `json:"name" minLength:"1" maxLength:"64"`
	Handle            string    `json:"handle" minLength:"2" maxLength:"40" pattern:"^[a-z][a-z0-9-]{1,39}$"`
	Persona           string    `json:"persona" maxLength:"2000"`
	Runtime           string    `json:"runtime" enum:"CODEX,CLAUDE_CODE"`
	Model             string    `json:"model" minLength:"1" maxLength:"160"`
	ReasoningEffort   *string   `json:"reasoning_effort,omitempty" enum:"low,medium,high,xhigh"`
	Roles             []string  `json:"roles" minItems:"1" maxItems:"7"`
	SystemPrompt      string    `json:"system_prompt" minLength:"1" maxLength:"20000"`
	AuthorizationMode string    `json:"authorization_mode" enum:"READ_ONLY,WORKSPACE_WRITE" default:"READ_ONLY"`
	AvatarStyle       string    `json:"avatar_style" enum:"PIXEL,ROBOT,MONSTER,GEOMETRIC" default:"PIXEL"`
	AvatarSeed        int64     `json:"avatar_seed" minimum:"0" maximum:"9007199254740991"`
	SkillIDs          []guid.ID `json:"skill_ids" maxItems:"30"`
	MemoryPolicy      string    `json:"memory_policy" minLength:"2" maxLength:"4096" doc:"Knowledge memory policy as a JSON object encoded in a string."`
	DefaultChannels   []string  `json:"default_channels" maxItems:"10"`
}

func (m *Module) injectRegistration(s *module.Services) error {
	var err error
	if m.registration.identity, err = module.Resolve[identity.AgentIdentityRegistration](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.registration.fleet, err = module.Resolve[fleet.AgentProvisioner](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if m.registration.skills, err = module.Resolve[knowledge.SkillMounter](s); err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if !m.registrationReady() {
		slog.Warn("atomic Agent creation unavailable: IAM identity, Fleet provisioner, Knowledge mounter or memory policy capability missing")
	}
	return nil
}

func (m *Module) registrationReady() bool {
	return m.registration.identity.Prepare != nil && m.registration.identity.CreateOn != nil && m.registration.fleet.CreateOn != nil && m.registration.skills != nil && m.memoryPolicy.Validate != nil
}

func (m *Module) registrationAuthorityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) error {
	if tx == nil || tx.Statement == nil {
		return ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	actor := actorID(ctx)
	if tenant <= 0 || actor <= 0 || tenant != tenantID(ctx) {
		return ErrForbidden
	}
	if m.principals.LockActiveRow == nil || m.principals.HumanByLogin == nil || m.members == nil || m.tenantPolicy == nil {
		return ErrUnavailable
	}
	principal, active, err := m.principals.LockActiveRow(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active || principal.ID != actor {
		return ErrForbidden
	}
	human, ok, err := m.principals.HumanByLogin(ctx, tx, principal.LoginName)
	if err != nil {
		return err
	}
	if !ok || human.ID != actor {
		return ErrForbidden
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	constraint, err := m.tenantPolicy(ctx, tx, tenant, "agent_manage", actor)
	if err != nil {
		return err
	}
	if !constraint.AllowAll {
		return ErrForbidden
	}
	return nil
}

func (m *Module) createAgentOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in AgentCreation, prepared *identity.PreparedAgentIdentity) (fleet.RegisteredAgent, error) {
	var empty fleet.RegisteredAgent
	if err := m.registrationAuthorityOn(ctx, tx, tenant); err != nil {
		return empty, err
	}
	if !m.registrationReady() {
		return empty, ErrUnavailable
	}
	if len(in.SkillIDs) > 30 {
		return empty, ErrInvalid
	}
	if err := m.memoryPolicy.Validate(ctx, tenant, json.RawMessage(in.MemoryPolicy)); err != nil {
		return empty, ErrInvalid
	}
	credential, err := m.registration.identity.CreateOn(ctx, tx, tenant, in.Handle, in.Name, prepared)
	if err != nil {
		return empty, registrationError(err)
	}
	memoryPolicy := json.RawMessage(in.MemoryPolicy)
	profile := fleet.AgentProfilePatch{Persona: &in.Persona, AvatarStyle: &in.AvatarStyle, AvatarSeed: &in.AvatarSeed, ReasoningEffort: in.ReasoningEffort, MemoryPolicy: &memoryPolicy, DefaultChannelNames: &in.DefaultChannels}
	out, err := m.registration.fleet.CreateOn(ctx, tx, tenant, fleet.AgentRegistration{PrincipalID: credential.Credential.ServiceAccountID, MachineID: in.MachineID, Name: in.Name, Handle: in.Handle, Runtime: in.Runtime, Model: in.Model, Roles: in.Roles, SystemPrompt: in.SystemPrompt, AuthorizationMode: in.AuthorizationMode}, profile, credential)
	if err != nil {
		return empty, registrationError(err)
	}
	if err = m.registration.skills.MountOn(ctx, tx, tenant, out.PrincipalID, in.SkillIDs); err != nil {
		return empty, registrationError(err)
	}
	if _, err = m.lockState(ctx, tx, tenant, out.PrincipalID); err != nil {
		return empty, err
	}
	return out, nil
}

func registrationError(err error) error {
	switch {
	case errors.Is(err, identity.ErrLoginConflict), errors.Is(err, identity.ErrServiceAccountConflict):
		return ErrConflict
	case errors.Is(err, identity.ErrInvalid), errors.Is(err, identity.ErrServiceAccountInvalid), errors.Is(err, knowledge.ErrInvalid), errors.Is(err, knowledge.ErrSkillNotMountable), errors.Is(err, fleet.ErrInvalidRoles):
		return ErrInvalid
	case errors.Is(err, identity.ErrUnavailable):
		return ErrUnavailable
	case errors.Is(err, fleet.ErrMachineForbidden):
		return ErrForbidden
	default:
		return profileError(err)
	}
}

type AgentCreationInput struct {
	TeamPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           AgentCreation
}

type CreationAvatarsInput struct {
	TeamPath
	Style string `query:"style" enum:"PIXEL,ROBOT,MONSTER,GEOMETRIC" default:"PIXEL"`
	Batch int    `query:"batch" minimum:"0" maximum:"1000000" default:"0"`
}

func (m *Module) creationAvatarsHTTP(ctx context.Context, in *CreationAvatarsInput) (*ProtocolBody[[]AvatarCandidate], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) ([]AvatarCandidate, error) {
		if err := m.registrationAuthorityOn(ctx, tx, tenant); err != nil {
			return nil, err
		}
		return m.avatarCandidates(ctx, tenant, actorID(ctx), in.Style, in.Batch)
	})
}

func (m *Module) registerCreationHTTP(api huma.API) error {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "agent_creation.avatar_candidates", Method: http.MethodGet, Path: "/agent-registrations/avatar-candidates", Summary: "Generate six deterministic avatar candidates before Agent creation", Tags: []string{"tenants"}, Errors: []int{401, 403, 422, 500, 503}}, m.creationAvatarsHTTP)
	// The existing /agents operation retains existing-account compatibility.
	// This resource represents the atomic complete-registration command.
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "agent_creation.create", Method: http.MethodPost, Path: "/agent-registrations", Summary: "Create an Agent identity, configuration and encrypted credential delivery atomically", Tags: []string{"tenants"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, m.createAgentHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	return authz.RegisterRequestContract(api, "/tenants/{tenant_id}/agent-registrations")
}

func (m *Module) createAgentHTTP(ctx context.Context, in *AgentCreationInput) (*CommandOutput[fleet.RegisteredAgent], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	if !m.registrationReady() {
		return nil, agentHTTPError(ctx, ErrUnavailable)
	}
	db, err := m.database()
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	check := func(tx *gorm.DB) error { return m.registrationAuthorityOn(ctx, tx, tenant) }
	if err = db.WithContext(ctx).Transaction(check); err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	if err = m.memoryPolicy.Validate(ctx, tenant, json.RawMessage(in.Body.MemoryPolicy)); err != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	prepared, err := m.registration.identity.Prepare(ctx, in.Body.AvatarStyle, in.Body.AvatarSeed)
	if err != nil {
		return nil, agentHTTPError(ctx, registrationError(err))
	}
	return agentCommand(ctx, m, in.IdempotencyKey, "agent_creation.create", "/agent-registrations", map[string]string{"tenant_id": in.TenantID}, in.Body, check, func(tx *gorm.DB) (fleet.RegisteredAgent, error) {
		return m.createAgentOn(ctx, tx, tenant, in.Body, prepared)
	})
}
