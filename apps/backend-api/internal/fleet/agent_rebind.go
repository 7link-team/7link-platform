package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/extension/secretx"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
)

var ErrAgentRunningExecutions = errors.New("fleet: agent has nonterminal executions")
var (
	ErrAgentRebindState      = errors.New("fleet: agent state cannot be rebound")
	ErrAgentRebindSameTarget = errors.New("fleet: agent already bound to target")
	ErrAgentRebindRuntime    = errors.New("fleet: target runtime unavailable")
	ErrAgentRebindMCP        = errors.New("fleet: target MCP capabilities unproven")
	ErrAgentRebindEffort     = errors.New("fleet: target reasoning effort unsupported")
)

// Existing registrations have no recorded rotated credential. Rebound profiles
// cannot resume or dispatch until the new credential is ACKed. Old offline
// machine cleanup remains deferred and visible independently.
func agentBindingReady(a *model.Agent) bool {
	return a.CredentialID == nil || a.CredentialDelivery == "DELIVERED"
}

type AgentRebindConnector struct {
	Bind func(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)) error
}

func (m *Module) provideAgentRebind(s *module.Services) error {
	return module.Provide[AgentRebindConnector](s, AgentRebindConnector{Bind: func(f func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)) error {
		if f == nil || m.agentRebindQuiet != nil {
			return ErrInvalidArgument
		}
		m.agentRebindQuiet = f
		return nil
	}})
}

type AgentMachineBindingInput struct {
	MachineID       guid.ID `json:"machine_id"`
	RuntimeKind     string  `json:"runtime_kind" enum:"CODEX,CLAUDE_CODE"`
	ModelID         string  `json:"model_id" minLength:"1" maxLength:"160"`
	ReasoningEffort *string `json:"reasoning_effort,omitempty" maxLength:"16"`
}
type AgentMachineBinding struct {
	RetirementID            *guid.ID `json:"retirement_id,omitempty"`
	ReasoningEffort         *string  `json:"reasoning_effort,omitempty"`
	CapabilityStale         bool     `json:"capability_stale"`
	AgentPrincipalID        guid.ID  `json:"agent_principal_id"`
	MachineID               guid.ID  `json:"machine_id"`
	RuntimeKind             string   `json:"runtime_kind"`
	ModelID                 string   `json:"model_id"`
	State                   string   `json:"state"`
	CredentialDelivery      string   `json:"credential_delivery"`
	PendingCleanupMachineID *guid.ID `json:"pending_cleanup_machine_id,omitempty"`
	ConfigRevision          int      `json:"config_revision"`
	Version                 int      `json:"version"`
}

func bindingView(a *model.Agent) AgentMachineBinding {
	out := AgentMachineBinding{RetirementID: a.RetirementID, AgentPrincipalID: a.PrincipalID, RuntimeKind: a.RuntimeKind, ModelID: a.ModelID, State: a.State, CredentialDelivery: a.CredentialDelivery, PendingCleanupMachineID: a.PendingCleanupMachineID, ConfigRevision: a.ConfigRevision, Version: a.Version}
	if a.MachineID != nil {
		out.MachineID = *a.MachineID
	}
	out.ReasoningEffort = a.ReasoningEffort
	return out
}
func (m *Module) agentCredentialRing() (*keyringx.Keyring, error) {
	key, err := secretx.Resolve("gateway_encryption_key", m.config.GatewayEncryptionKey, m.config.GatewayEncryptionKeyFile, secretx.DefaultMaxBytes)
	if err != nil {
		return nil, ErrUnavailable
	}
	return keyringx.New("fleet:agent-credentials:", key, m.config.PreviousGatewayEncryptionKeys...)
}
func (m *Module) rebindAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, principal, target guid.ID) error {
	if fleetTransaction(tx, tenant, principal) != nil || target <= 0 {
		return ErrInvalidArgument
	}
	a := query.BindAgents(tx, m.names)
	snapshot, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.PrincipalID.Eq(principal)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrMachineNotFound
	}
	if err != nil {
		return err
	}
	ids := []guid.ID{authn.PrincipalIDFromContext(ctx), principal}
	machines := []guid.ID{target}
	if snapshot.MachineID != nil {
		machines = append(machines, *snapshot.MachineID)
	}
	for _, id := range machines {
		row, e := m.machineOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		ids = append(ids, row.PrincipalID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if m.principals.Lock == nil {
		return ErrUnavailable
	}
	for _, id := range ids {
		if id <= 0 {
			return ErrMachineForbidden
		}
		if err = m.principals.Lock(ctx, tx, id); err != nil {
			return err
		}
	}
	if err = m.controlAuthorityOn(ctx, tx, tenant, "agent_manage"); err != nil {
		return err
	}
	now := time.Now().UTC()
	if m.accounts == nil {
		return ErrUnavailable
	}
	targetRow, e := m.machineOn(ctx, tx, tenant, target, false)
	if e != nil {
		return e
	}
	for _, id := range []guid.ID{principal, targetRow.PrincipalID} {
		active, e := m.accounts(ctx, tx, tenant, id, now)
		if e != nil {
			return e
		}
		if !active {
			return ErrMachineForbidden
		}
	}
	slices.Sort(machines)
	for _, id := range slices.Compact(machines) {
		if _, err = m.machineOn(humanMachineContext(ctx), tx, tenant, id, true); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) RebindAgentOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, version int, in AgentMachineBindingInput) (AgentMachineBinding, error) {
	var out AgentMachineBinding
	if version < 1 || in.MachineID <= 0 || strings.TrimSpace(in.ModelID) == "" || len(in.ModelID) > 160 || nativeModelFamily(in.RuntimeKind, in.ModelID) == "" {
		return out, ErrInvalidArgument
	}
	if err := m.rebindAuthorityOn(ctx, tx, tenant, principal, in.MachineID); err != nil {
		return out, err
	}
	q := query.BindAgents(tx, m.names)
	a, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil {
		return out, err
	}
	if a.Version != version {
		return out, ErrVersionConflict
	}
	if a.PendingCleanupMachineID != nil {
		return out, ErrVersionConflict
	}
	if !slices.Contains([]string{"DRAFT", "STOPPED", "CRASHED"}, a.State) || slices.Contains([]string{"STARTING", "RUNNING", "STOPPING"}, a.ProcessStatus) {
		return out, ErrAgentRebindState
	}
	if a.MachineID != nil && *a.MachineID == in.MachineID {
		return out, ErrAgentRebindSameTarget
	}
	if m.agentRebindQuiet == nil || m.agentCredentialRotate == nil || m.ids == nil || m.audit == nil || m.events == nil {
		return out, ErrUnavailable
	}
	quiet, err := m.agentRebindQuiet(ctx, tx, tenant, principal)
	if err != nil {
		return out, err
	}
	if !quiet {
		return out, ErrAgentRunningExecutions
	}
	target, err := m.machineOn(humanMachineContext(ctx), tx, tenant, in.MachineID, true)
	if err != nil {
		return out, err
	}
	if target.State == "REVOKED" {
		return out, ErrMachineRevoked
	}
	runtimes := query.BindRuntimes(tx, m.names)
	runtime, err := runtimes.WithContext(ctx).Where(runtimes.TenantID.Eq(tenant), runtimes.MachineID.Eq(in.MachineID), runtimes.Kind.Eq(in.RuntimeKind)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrAgentRebindRuntime
	}
	if err != nil {
		return out, err
	}
	if runtime.ExecutablePath == "" {
		return out, ErrAgentRebindRuntime
	}
	family, err := m.ModelFamilyOn(ctx, tx, tenant, in.MachineID, in.RuntimeKind, in.ModelID)
	if err != nil {
		return out, err
	}
	var allowlist []string
	if json.Unmarshal([]byte(a.MCPAllowlist), &allowlist) != nil {
		return out, ErrUnavailable
	}
	if in.ReasoningEffort == nil {
		in.ReasoningEffort = a.ReasoningEffort
	}
	stale, err := m.rebindCapabilitiesOn(ctx, tx, tenant, in, allowlist)
	if err != nil {
		return out, err
	}
	ring, err := m.agentCredentialRing()
	if err != nil {
		return out, err
	}
	secret, err := m.agentCredentialRotate(ctx, tx, tenant, principal, a.CredentialID)
	if err != nil {
		return out, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	revision := a.ConfigRevision + 1
	create := func(machine guid.ID, kind, sealed string, credential *guid.ID) error {
		value, e := m.ids.Next()
		if e != nil {
			return e
		}
		if value <= 0 {
			return ErrUnavailable
		}
		return query.BindAgentMachineCommands(tx, m.names).WithContext(ctx).Create(&model.AgentMachineCommand{ID: guid.ID(value), TenantID: tenant, AgentPrincipalID: principal, MachineID: machine, CredentialID: credential, ConfigRevision: revision, Kind: kind, State: "QUEUED", SealedCredential: sealed, CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)})
	}
	plain, err := json.Marshal(secret)
	if err != nil {
		return out, err
	}
	sealed, err := ring.Seal("binding", []byte(tenant.String()+":"+principal.String()+":"+secret.Credential.ID.String()), plain)
	if err != nil {
		return out, ErrUnavailable
	}
	if err = create(in.MachineID, "CREDENTIAL", sealed, &secret.Credential.ID); err != nil {
		return out, err
	}
	old := a.MachineID
	if old != nil {
		if err = create(*old, "CLEANUP", "", nil); err != nil {
			return out, err
		}
	}
	values := map[string]any{"machine_id": in.MachineID, "runtime_kind": in.RuntimeKind, "model_id": in.ModelID, "model_family": family, "reasoning_effort": in.ReasoningEffort, "credential_id": secret.Credential.ID, "credential_delivery": "PENDING", "pending_cleanup_machine_id": old, "config_revision": revision, "running_config_revision": nil, "process_status": "NONE", "version": version + 1, "updated_at": now, "updated_by": authn.PrincipalIDFromContext(ctx)}
	if _, err = q.WithContext(ctx).Where(q.ID.Eq(a.ID), q.Version.Eq(version)).Updates(values); err != nil {
		return out, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "fleet.agent_rebound", "agent", a.ID)
	event.Detail["old_machine_id"], event.Detail["machine_id"] = old, in.MachineID
	if err = m.audit(ctx, tx, event); err != nil {
		return out, err
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "fleet.agent_rebound", Subject: events.Subject{Kind: "agent", ID: principal}, SubjectVersion: int64(version + 1), Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM:agent_manage"}, Data: map[string]any{"machine_id": in.MachineID, "old_machine_id": old, "credential_delivery": "PENDING"}})
	if err != nil {
		return out, err
	}
	a.MachineID = &in.MachineID
	a.RuntimeKind = in.RuntimeKind
	a.ModelID = in.ModelID
	a.ReasoningEffort = in.ReasoningEffort
	a.PendingCleanupMachineID = old
	a.CredentialDelivery = "PENDING"
	a.ConfigRevision = revision
	a.Version++
	out = bindingView(a)
	out.CapabilityStale = stale
	return out, nil
}

// RebindCapabilityError carries only allowlisted identifiers, never local MCP configuration.
type RebindCapabilityError struct {
	Cause     error    `json:"-"`
	Unknown   []string `json:"unknown,omitempty"`
	Model     string   `json:"model,omitempty"`
	Effort    string   `json:"effort,omitempty"`
	Supported []string `json:"supported,omitempty"`
	Stale     bool     `json:"stale"`
}

func (e *RebindCapabilityError) Error() string { return e.Cause.Error() }
func (e *RebindCapabilityError) Unwrap() error { return e.Cause }

func (m *Module) rebindCapabilitiesOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in AgentMachineBindingInput, allowlist []string) (bool, error) {
	stale := false
	read := func(kind string) (*RuntimeDiscovery, error) {
		q := query.BindInspections(tx, m.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(in.MachineID), q.Kind.Eq(kind), q.ScopeKey.Eq("home"), q.State.Eq("READY")).Order(q.ID.Desc()).Limit(1).Find()
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, nil
		}
		var facts []RuntimeDiscovery
		if json.Unmarshal([]byte(rows[0].ResultJSON), &facts) != nil {
			return nil, ErrUnavailable
		}
		for _, d := range facts {
			if d.Runtime == in.RuntimeKind && d.LastSuccessAt != nil {
				// BR-05-05 permits historical successful observations with an explicit warning.
				stale = stale || d.State != "READY" || time.Since(*d.LastSuccessAt) > 24*time.Hour
				return &d, nil
			}
		}
		return nil, nil
	}
	if len(allowlist) > 0 {
		d, err := read("CAPABILITIES")
		if err != nil {
			return false, err
		}
		unknown := []string{}
		for _, name := range allowlist {
			if d == nil || !slices.Contains(d.MCP, name) {
				unknown = append(unknown, name)
			}
		}
		if len(unknown) > 0 {
			return stale, &RebindCapabilityError{Cause: ErrAgentRebindMCP, Unknown: unknown, Stale: stale}
		}
	}
	if in.ReasoningEffort != nil && *in.ReasoningEffort != "" {
		d, err := read("MODELS")
		if err != nil {
			return false, err
		}
		supported := []string{}
		if d != nil {
			for _, model := range d.Reasoning {
				if model.Model == in.ModelID {
					supported = model.Efforts
					break
				}
			}
		}
		if !slices.Contains(supported, *in.ReasoningEffort) {
			return stale, &RebindCapabilityError{Cause: ErrAgentRebindEffort, Model: in.ModelID, Effort: *in.ReasoningEffort, Supported: supported, Stale: stale}
		}
	}
	return stale, nil
}

func (m *Module) injectAgentRebind(s *module.Services) error {
	var err error
	m.agentCredentialRotate, err = module.Resolve[identity.AgentCredentialRotator](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	return err
}
