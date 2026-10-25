package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// AgentRegistrationRolesConnector lets the business role owner validate stable
// role keys against actual current IAM grants without reversing dependencies.
type AgentRegistrationRolesConnector struct {
	Bind func(func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) error) error
}

func (m *Module) provideAgentRegistration(s *module.Services) error {
	return module.Provide[AgentRegistrationRolesConnector](s, AgentRegistrationRolesConnector{Bind: func(check func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) error) error {
		if check == nil || m.agentRoles != nil {
			return ErrUnavailable
		}
		m.agentRoles = check
		return nil
	}})
}

type AgentRegistration struct {
	AuthorizationMode string   `json:"authorization_mode,omitempty" enum:"READ_ONLY,WORKSPACE_WRITE" default:"READ_ONLY"`
	PrincipalID       guid.ID  `json:"principal_id" doc:"An existing active service account owned by this tenant, with the requested Agent IAM roles."`
	MachineID         guid.ID  `json:"machine_id"`
	Name              string   `json:"name" minLength:"1" maxLength:"64"`
	Handle            string   `json:"handle" minLength:"2" maxLength:"40" pattern:"^[a-z][a-z0-9-]{1,39}$"`
	Runtime           string   `json:"runtime" enum:"CODEX,CLAUDE_CODE"`
	Model             string   `json:"model" minLength:"1" maxLength:"160"`
	Roles             []string `json:"roles" minItems:"1" maxItems:"7"`
	SystemPrompt      string   `json:"system_prompt" minLength:"1" maxLength:"20000"`
}
type RegisteredAgent struct {
	ID            guid.ID `json:"id"`
	PrincipalID   guid.ID `json:"principal_id"`
	MachineID     guid.ID `json:"machine_id"`
	State         string  `json:"state"`
	ProcessStatus string  `json:"process_status"`
	ModelFamily   string  `json:"model_family"`
	Version       int     `json:"version"`
}

var agentHandle = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)

func (m *Module) agentRegistrationAuthorityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in AgentRegistration) error {
	if in.PrincipalID <= 0 || in.MachineID <= 0 || m.principals.Lock == nil {
		return ErrInvalidArgument
	}
	machine, err := m.MachineByIDOn(ctx, tx, tenant, in.MachineID)
	if err != nil {
		return err
	}
	ids := []guid.ID{authn.PrincipalIDFromContext(ctx), in.PrincipalID, machine.PrincipalID}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if id <= 0 {
			return ErrMachineForbidden
		}
		if err = m.principals.Lock(ctx, tx, id); err != nil {
			return err
		}
	}
	return m.controlAuthorityOn(ctx, tx, tenant, "agent_manage")
}

// Native model validation is separate from catalog family resolution.
func nativeModelFamily(runtime, name string) string {
	if !discoveryID(name, 160) {
		return ""
	}
	return inferredModelFamily(runtime, name)
}

func (m *Module) RegisterAgentOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in AgentRegistration) (RegisteredAgent, error) {
	return m.registerAgentOn(ctx, tx, tenant, in, nil, m.agentRoles)
}

func (m *Module) registerAgentOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in AgentRegistration, spawn *agentSpawnRegistration, checkRoles func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) error) (RegisteredAgent, error) {
	var empty RegisteredAgent
	if in.AuthorizationMode == "" {
		in.AuthorizationMode = "READ_ONLY"
	}
	if (spawn == nil && !slices.Contains([]string{"READ_ONLY", "WORKSPACE_WRITE"}, in.AuthorizationMode)) || (spawn != nil && in.AuthorizationMode != "ON_REQUEST") {
		return empty, ErrInvalidArgument
	}
	if fleetTransaction(tx, tenant, in.PrincipalID) != nil || in.MachineID <= 0 || !boundedFact(in.Name, 64) || !agentHandle.MatchString(in.Handle) || !boundedFact(in.Model, 160) || !utf8.ValidString(in.SystemPrompt) || strings.TrimSpace(in.SystemPrompt) == "" || len(in.SystemPrompt) > 20000 || len(in.Roles) < 1 || len(in.Roles) > 7 {
		return empty, ErrInvalidArgument
	}
	family := nativeModelFamily(in.Runtime, in.Model)
	if family == "" {
		return empty, ErrInvalidArgument
	}
	roles := slices.Clone(in.Roles)
	slices.Sort(roles)
	roles = slices.Compact(roles)
	keys := make([]string, len(roles))
	for i, role := range roles {
		var ok bool
		if keys[i], ok = AgentIAMRoleKey(role); !ok {
			return empty, ErrInvalidRoles
		}
	}
	if slices.Contains(roles, "EXECUTOR") && slices.Contains(roles, "ACCEPTOR") || slices.Contains(roles, "ASSISTANT") && (slices.Contains(roles, "EXECUTOR") || slices.Contains(roles, "TESTER") || slices.Contains(roles, "ACCEPTOR")) {
		return empty, ErrInvalidRoles
	}
	if m.principals.Lock == nil || m.accounts == nil || checkRoles == nil || m.ids == nil || m.audit == nil || m.events == nil {
		return empty, ErrUnavailable
	}
	if spawn == nil {
		if err := m.agentRegistrationAuthorityOn(ctx, tx, tenant, in); err != nil {
			return empty, err
		}
	} else {
		grant, err := m.agentSpawnAuthorityOn(ctx, tx, tenant, spawn.Input, false)
		if err != nil {
			return empty, err
		}
		spawn.Grant = grant
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	valid, err := m.accounts(ctx, tx, tenant, in.PrincipalID, now)
	if err != nil {
		return empty, err
	}
	if !valid {
		return empty, ErrMachineForbidden
	}
	if err = checkRoles(ctx, tx, tenant, in.PrincipalID, keys); err != nil {
		return empty, err
	}
	// Machine credentials and Agent credentials are distinct principals.
	mq := query.BindMachines(tx, m.names)
	count, err := mq.WithContext(ctx).Where(mq.PrincipalID.Eq(in.PrincipalID)).Count()
	if err != nil {
		return empty, err
	}
	if count > 0 {
		return empty, ErrMachineForbidden
	}
	q := query.BindAgents(tx, m.names)
	count, err = q.WithContext(ctx).Where(q.PrincipalID.Eq(in.PrincipalID)).Count()
	if err != nil {
		return empty, err
	}
	if count > 0 {
		return empty, ErrVersionConflict
	}
	machine, err := m.LockMachineOn(ctx, tx, tenant, in.MachineID)
	if err != nil {
		return empty, err
	}
	if machine.State != "ONLINE" {
		return empty, ErrMachineOffline
	}
	runtimes := query.BindRuntimes(tx, m.names)
	installed, err := runtimes.WithContext(ctx).Where(runtimes.TenantID.Eq(tenant), runtimes.MachineID.Eq(in.MachineID), runtimes.Kind.Eq(in.Runtime)).Count()
	if err != nil {
		return empty, err
	}
	// A configured native model ID is allowed even when the CLI has no model
	// catalog. Registration asserts installed runtime, never catalog discovery.
	if installed != 1 {
		return empty, ErrInvalidArgument
	}
	family, err = m.ModelFamilyOn(ctx, tx, tenant, in.MachineID, in.Runtime, in.Model)
	if err != nil {
		return empty, err
	}
	value, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	if value <= 0 {
		return empty, ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	id := guid.ID(value)
	row := &model.Agent{ID: id, TenantID: tenant, PrincipalID: in.PrincipalID, MachineID: &in.MachineID, Name: in.Name, Handle: in.Handle, AvatarStyle: "PIXEL", AvatarSeed: value % 9007199254740992, SystemPrompt: in.SystemPrompt, RuntimeKind: in.Runtime, ModelID: in.Model, ModelFamily: family, AuthorizationMode: in.AuthorizationMode, MaxContextTokens: 24000, MCPAllowlist: "[]", CapabilityTags: "[]", DefaultChannelNames: "[]", MemoryPolicy: `{"profile":"STANDARD"}`, MaxParallelItems: 1, MaxRisk: "MEDIUM", State: "ACTIVE", ProcessStatus: "NONE", ConfigRevision: 1, AutoRestart: true, CredentialDelivery: "PENDING", Version: 1, Origin: "HUMAN_CREATED", CreatedByPrincipalID: actor, OwnerPrincipalID: actor, CreatedBy: &actor, UpdatedBy: &actor, CreatedAt: now, UpdatedAt: now}
	if spawn != nil {
		row.Origin, row.AuthorizationMode = "AGENT_SPAWNED", "ON_REQUEST"
		row.ParentAgentID, row.SpawnGrantID = &spawn.Input.SupervisorAgentID, &spawn.Grant.GrantID
		row.SpawnReason, row.ExpiresAt, row.ReasoningEffort = &spawn.Input.Reason, &spawn.Grant.ExpiresAt, &spawn.Input.Effort
		row.OwnerPrincipalID = spawn.Grant.OwnerPrincipalID
	}
	if err = q.WithContext(ctx).Create(row); err != nil {
		return empty, err
	}
	r := query.BindAgentRoles(tx, m.names)
	for i, role := range roles {
		if err = r.WithContext(ctx).Create(&model.AgentRole{TenantID: tenant, AgentID: id, Role: role, IAMRoleKey: keys[i], CreatedBy: &actor, UpdatedBy: &actor, CreatedAt: now, UpdatedAt: now}); err != nil {
			return empty, err
		}
	}
	kind, authority, typ := "HUMAN", "IAM:agent_manage", "fleet.agent_registered"
	if spawn != nil {
		if err = m.spawnPolicy.ReserveOn(ctx, tx, tenant, id, spawn.Input); err != nil {
			return empty, err
		}
		kind, authority, typ = "AGENT", "POLICY:spawn_grant", "agent.spawned"
	}
	event := audit.NewDomainEvent(ctx, tenant, "fleet.agent_registered", "agent", id)
	if spawn != nil {
		event.EventType = "fleet.agent_spawned"
		event.Detail["spawn_grant_id"], event.Detail["supervisor_agent_id"], event.Detail["reason"] = spawn.Grant.GrantID.String(), spawn.Input.SupervisorAgentID.String(), spawn.Input.Reason
	}
	event.Detail["principal_id"], event.Detail["machine_id"], event.Detail["roles"] = in.PrincipalID.String(), in.MachineID.String(), keys
	event.Detail["authority_source"], event.Detail["actor_kind"] = authority, kind
	if err = m.audit(ctx, tx, event); err != nil {
		return empty, err
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: typ, Subject: events.Subject{Kind: "agent", ID: in.PrincipalID}, SubjectVersion: 1, Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: authority}, Data: map[string]any{"state": "ACTIVE", "process_status": "NONE", "agent_id": id, "principal_id": in.PrincipalID, "parent_agent_id": row.ParentAgentID, "spawn_grant_id": row.SpawnGrantID}})
	return RegisteredAgent{ID: id, PrincipalID: in.PrincipalID, MachineID: in.MachineID, State: "ACTIVE", ProcessStatus: "NONE", ModelFamily: family, Version: 1}, err
}

type agentRegistrationInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           AgentRegistration
}

func (m *Module) registerAgentRegistrationHTTP(api huma.API) error {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "agent.registration.create", Method: http.MethodPost, Path: "/agents", DefaultStatus: 201, Summary: "Register a real service account as a machine-bound Agent", Tags: []string{"agents"}, Errors: []int{401, 403, 404, 409, 422, 503}}, authz.Guard{Resource: "agent", Verb: "manage", FilterData: true}, m.registerAgentHTTP)
	return authz.RegisterRequestContract(api, "/tenants/{tenant_id}/agents")
}

func (m *Module) registerAgentHTTP(ctx context.Context, in *agentRegistrationInput) (*respx.Body[RegisteredAgent], error) {
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out RegisteredAgent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if err := m.agentRegistrationAuthorityOn(ctx, tx, tenant, in.Body); err != nil {
			return err
		}
		if m.idempotency == nil {
			return ErrUnavailable
		}
		digest, err := idempotency.HashRequest(http.MethodPost, "agent.registration.create", nil, in.Body)
		if err != nil {
			return err
		}
		receipt, _, err := m.idempotency.Do(ctx, tx, idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "agent.registration.create", Value: in.IdempotencyKey, RequestHash: digest}, func(tx *gorm.DB) (idempotency.Result, error) {
			v, err := m.RegisterAgentOn(ctx, tx, tenant, in.Body)
			if err != nil {
				return idempotency.Result{}, err
			}
			body, err := json.Marshal(v)
			return idempotency.Result{StatusCode: 201, Body: body}, err
		})
		if err != nil {
			return err
		}
		return json.Unmarshal(receipt.Body, &out)
	})
	if errors.Is(err, idempotency.ErrKeyReused) || errors.Is(err, idempotency.ErrInProgress) {
		err = ErrVersionConflict
	}
	if errors.Is(err, idempotency.ErrInvalidKey) || errors.Is(err, ErrInvalidRoles) {
		err = ErrInvalidArgument
	}
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
