package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"gorm.io/gen"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentWorkspaceMachine publishes current transport fences, never host paths.
type AgentWorkspaceMachine struct {
	BindContext func(context.Context, guid.ID) (context.Context, error)
	BindingOn   func(context.Context, *gorm.DB, guid.ID, guid.ID) (AgentWorkspaceFence, error)
	ValidateOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64) error
}
type AgentCatalogPolicy struct {
	AllowedOn func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (bool, error)
}

// ToolSourceFacts exposes observed machine capabilities to tool imports. Reads
// recheck current Human machine_read authority inside the caller's transaction.
type ToolSourceFacts struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (MachineCapabilities, error)
}

func (m *Module) provideToolSources(s *module.Services) error {
	return module.Provide(s, ToolSourceFacts{ReadOn: m.toolSourceFactsOn})
}

func (m *Module) toolSourceFactsOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID) (MachineCapabilities, error) {
	if err := fleetTransaction(tx, tenant, machine); err != nil {
		return MachineCapabilities{}, err
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "machine_read"); err != nil {
		return MachineCapabilities{}, err
	}

	facts, err := m.capabilitiesOn(ctx, tx, tenant, machine)
	if err != nil {
		return MachineCapabilities{}, err
	}
	return facts, nil
}

// AgentRetirementObserverConnector binds transactional cleanup owned by consumers.
type AgentRetirementObserverConnector struct {
	Bind func(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error) error
}

func (m *Module) provideRetirementObservers(s *module.Services) error {
	return module.Provide(s, AgentRetirementObserverConnector{Bind: func(observer func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error) error {
		if observer == nil || m.retirementObserver != nil {
			return ErrUnavailable
		}
		m.retirementObserver = observer
		return nil
	}})
}

func (m *Module) notifyRetirementOn(ctx context.Context, tx *gorm.DB, tenant, principal, eventID guid.ID) error {
	if m.retirementObserver == nil {
		return nil
	}
	return m.retirementObserver(ctx, tx, tenant, principal, eventID)
}

func (m *Module) agentCatalogAllowedOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, key string) (bool, error) {
	if e := fleetTransaction(tx, tenant, agent); e != nil {
		return false, e
	}
	q := query.BindAgents(tx, m.names)
	a, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(agent), q.State.Eq("ACTIVE")).First()
	if e != nil {
		return false, e
	}
	var allowed []string
	if json.Unmarshal([]byte(a.MCPAllowlist), &allowed) != nil {
		return false, ErrUnavailable
	}
	return slices.Contains(allowed, "catalog:"+key), nil
}

type AgentWorkspaceFence struct {
	MachineID      guid.ID `json:"machine_id"`
	SessionID      guid.ID `json:"session_id"`
	ConnEpoch      int64   `json:"conn_epoch"`
	ConfigRevision int     `json:"config_revision"`
}

func (m *Module) provideWorkspace(s *module.Services) error {
	revokers := map[string]func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error{}
	if err := module.Provide(s, AgentRetirementRevocationConnector{Bind: func(owner string, f func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error) error {
		if owner == "" || f == nil || revokers[owner] != nil {
			return ErrInvalidArgument
		}
		revokers[owner] = f
		return nil
	}}); err != nil {
		return err
	}
	revoke := func(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID) error {
		// Both owners are mandatory: a partial module composition may not
		// silently turn off delegation, capacity or channel revocation.
		if revokers["business_governance"] == nil || revokers["collab"] == nil {
			return ErrUnavailable
		}
		for _, owner := range []string{"business_governance", "collab"} {
			if err := revokers[owner](ctx, tx, tenant, principal, retirement); err != nil {
				return err
			}
		}
		return nil
	}
	if err := module.Provide(s, AgentRetirementRuntime{BeginOn: m.beginAgentRetirementOn, CancelOn: m.cancelAgentRetirementOn, CleanupOn: m.cleanupAgentRetirementOn, RequestCleanupOn: func(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int) (AgentRetirementBinding, error) {
		return m.requestAgentRetirementCleanupOn(ctx, tx, tenant, principal, retirement, version, true)
	}, CompleteTimedOutOn: func(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int) (AgentRetirementBinding, error) {
		return m.completeTimedOutRetirementOn(ctx, tx, tenant, principal, retirement, version, revoke)
	}, CleanupStateOn: m.agentRetirementCleanupStateOn, CompleteOn: func(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int) (AgentRetirementBinding, error) {
		return m.completeAgentRetirementRevocationsOn(ctx, tx, tenant, principal, retirement, version, revoke)
	}, ForceOn: func(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int, confirmName string, loss RetirementLoss) (AgentRetirementBinding, error) {
		return m.forceAgentRetirementOn(ctx, tx, tenant, principal, retirement, version, confirmName, loss, revoke)
	}}); err != nil {
		return err
	}
	if err := module.Provide(s, AgentProvisioner{CreateOn: m.provisionAgentOn}); err != nil {
		return err
	}
	if e := module.Provide(s, AgentProfiles{ReadOn: m.agentProfileOn, UpdateOn: m.updateAgentProfileOn, RunStateOn: m.agentRunStateOn, DirectoryOn: m.fleetRosterOn, RearmOn: m.rearmAgentOn, BindProcessObserver: func(f func(context.Context, *gorm.DB, AgentProcessTransition) error) error {
		if f == nil || m.processObserver != nil {
			return ErrUnavailable
		}
		m.processObserver = f
		return nil
	}}); e != nil {
		return e
	}
	if e := module.Provide(s, AgentCatalogPolicy{AllowedOn: m.agentCatalogAllowedOn}); e != nil {
		return e
	}
	return module.Provide(s, AgentWorkspaceMachine{BindContext: m.BindRunnerContext, BindingOn: m.agentWorkspaceBindingOn, ValidateOn: m.HeartbeatOn})
}

// AgentRetirementRuntime owns Fleet's runtime portion of the retirement
// protocol. The caller persists the retirement steps in the same transaction;
// handover, reassignment and final revocation remain source-owner commands.
// None of these functions asserts that a machine has cleaned its private data.
type AgentRetirementRuntime struct {
	RequestCleanupOn   func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (AgentRetirementBinding, error)
	CompleteTimedOutOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (AgentRetirementBinding, error)
	BeginOn            func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (AgentRetirementBinding, error)
	CancelOn           func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (AgentRetirementBinding, error)
	CleanupOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (AgentRetirementBinding, error)
	CleanupStateOn     func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error)
	CompleteOn         func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (AgentRetirementBinding, error)
	ForceOn            func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, string, RetirementLoss) (AgentRetirementBinding, error)
}

// RetirementLoss is frozen by the coordinator from source-owned manifests.
// It records disclosed loss, never a claim that executions or cleanup succeeded.
type RetirementLoss struct {
	OpenItems         int `json:"open_items"`
	RunningExecutions int `json:"running_executions"`
}

var ErrRetirementNameMismatch = errors.New("fleet: retirement confirmation name mismatch")

// AgentRetirementRevocationConnector lets Governance revoke delegations and
// spawn capacity in Fleet's final transaction without a reverse import edge.
type AgentRetirementRevocationConnector struct {
	Bind func(string, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error) error
}

func (m *Module) injectRetirement(s *module.Services) error {
	registrar, err := module.Resolve[*authz.Registrar](s)
	if err != nil {
		return err
	}
	if registrar == nil || registrar.Registry() == nil {
		return ErrUnavailable
	}
	if err = registrar.Registry().Contribute([]authz.Action{{Resource: "agent", Verb: "retire", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "retire agents with handover and credential revocation"}}, nil); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.Catalog](s)
	if err != nil {
		return err
	}
	return catalog.AddTypes(Name, events.TypeSpec{Name: "agent.state_changed", SchemaVersion: 1, SubjectKind: "agent", Realtime: events.RealtimeSignal})
}

type AgentRetirementBinding struct {
	AgentID          guid.ID  `json:"agent_id"`
	PrincipalID      guid.ID  `json:"principal_id"`
	RetirementID     guid.ID  `json:"retirement_id"`
	MachineID        *guid.ID `json:"machine_id,omitempty"`
	CleanupCommandID *guid.ID `json:"cleanup_command_id,omitempty"`
	State            string   `json:"state"`
	ProcessStatus    string   `json:"process_status"`
	Version          int      `json:"version"`
}

func (m *Module) retirementAgentOn(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID) (*model.Agent, error) {
	if fleetTransaction(tx, tenant, principal) != nil || retirement <= 0 {
		return nil, ErrInvalidArgument
	}
	if err := m.lockAgentCommandPrincipals(ctx, tx, principal); err != nil {
		return nil, err
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "agent_retire"); err != nil {
		return nil, err
	}
	q := query.BindAgents(tx, m.names)
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal)).First()
}

func retirementBinding(a *model.Agent, retirement guid.ID) AgentRetirementBinding {
	return AgentRetirementBinding{AgentID: a.ID, PrincipalID: a.PrincipalID, RetirementID: retirement, MachineID: a.MachineID, State: a.State, ProcessStatus: a.ProcessStatus, Version: a.Version}
}

// Begin moves an online Agent out of all new-dispatch candidate sets. It does
// not claim that existing executions have stopped or a handover was produced.
func (m *Module) beginAgentRetirementOn(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int) (AgentRetirementBinding, error) {
	a, err := m.retirementAgentOn(ctx, tx, tenant, principal, retirement)
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if version < 1 || a.Version != version || !slices.Contains([]string{"DRAFT", "ACTIVE", "STOPPED", "CRASHED"}, a.State) || a.RetirementID != nil {
		return AgentRetirementBinding{}, ErrVersionConflict
	}
	if a.MachineID == nil {
		return AgentRetirementBinding{}, ErrMachineOffline
	}
	machine, err := m.machineOn(ctx, tx, tenant, *a.MachineID, true)
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if machineState(machine, time.Now().UTC()) != "ONLINE" || machine.CurrentSessionID == nil {
		return AgentRetirementBinding{}, ErrMachineOffline
	}
	if err = m.retirementTransitionOn(ctx, tx, a, retirement, "RETIRING", "fleet.agent_retirement_started", map[string]any{"retirement_id": retirement}); err != nil {
		return AgentRetirementBinding{}, err
	}
	return retirementBinding(a, retirement), nil
}

// Cancel is called only while the durable protocol is still cancellable. A
// detached binding is an additional irreversible-boundary fence here.
func (m *Module) cancelAgentRetirementOn(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int) (AgentRetirementBinding, error) {
	a, err := m.retirementAgentOn(ctx, tx, tenant, principal, retirement)
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if version < 1 || a.Version != version || a.State != "RETIRING" || a.RetirementID == nil || *a.RetirementID != retirement || a.MachineID == nil || a.PendingCleanupMachineID != nil {
		return AgentRetirementBinding{}, ErrVersionConflict
	}
	// Stopping a process is a real runtime operation. Do not relabel a running
	// process STOPPED when cancellation of the protocol has not stopped it.
	if slices.Contains([]string{"STARTING", "RUNNING", "STOPPING", "UNREACHABLE"}, a.ProcessStatus) {
		return AgentRetirementBinding{}, ErrAgentRebindState
	}
	if err = m.retirementTransitionOn(ctx, tx, a, retirement, "STOPPED", "fleet.agent_retirement_cancelled", map[string]any{"retirement_id": nil}); err != nil { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return AgentRetirementBinding{}, err
	}
	return retirementBinding(a, retirement), nil
}

// Cleanup is safe only after the source owners have stopped executions and the
// machine has reported that the Agent process exited. Detach and the command
// commit together; the existing machine/session protocol alone can ACK it.
func (m *Module) cleanupAgentRetirementOn(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int) (AgentRetirementBinding, error) {
	return m.requestAgentRetirementCleanupOn(ctx, tx, tenant, principal, retirement, version, false)
}

// The protocol command may ask the existing Runner CLEANUP command to stop the
// supervised process itself. STOPPING is intent; only its fenced ACK proves exit
// and private-directory removal. The legacy exited-process command stays strict.
func (m *Module) requestAgentRetirementCleanupOn(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int, stopProcess bool) (AgentRetirementBinding, error) {
	a, err := m.retirementAgentOn(ctx, tx, tenant, principal, retirement)
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if version < 1 || a.Version != version || a.State != "RETIRING" || a.RetirementID == nil || *a.RetirementID != retirement {
		return AgentRetirementBinding{}, ErrVersionConflict
	}
	if a.MachineID == nil || a.PendingCleanupMachineID != nil || (!stopProcess && !slices.Contains([]string{"NONE", "EXITED"}, a.ProcessStatus)) {
		return AgentRetirementBinding{}, ErrAgentRebindState
	}
	if m.agentRebindQuiet == nil || m.ids == nil {
		return AgentRetirementBinding{}, ErrUnavailable
	}
	quiet, err := m.agentRebindQuiet(ctx, tx, tenant, principal)
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if !quiet {
		return AgentRetirementBinding{}, ErrAgentRunningExecutions
	}
	id, err := m.ids.Next()
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if id <= 0 {
		return AgentRetirementBinding{}, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	command := model.AgentMachineCommand{ID: guid.ID(id), TenantID: tenant, AgentPrincipalID: principal, MachineID: *a.MachineID, ConfigRevision: a.ConfigRevision, Kind: "CLEANUP", State: "QUEUED", CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	if err = query.BindAgentMachineCommands(tx, m.names).WithContext(ctx).Create(&command); err != nil {
		return AgentRetirementBinding{}, err
	}
	changes := map[string]any{"machine_id": nil, "pending_cleanup_machine_id": a.MachineID}
	if !slices.Contains([]string{"NONE", "EXITED"}, a.ProcessStatus) {
		changes["process_status"], changes["process_status_at"] = "STOPPING", now
	}
	if err = m.retirementTransitionOn(ctx, tx, a, retirement, "RETIRING", "fleet.agent_retirement_cleanup_requested", changes); err != nil {
		return AgentRetirementBinding{}, err
	}
	a.PendingCleanupMachineID, a.MachineID = a.MachineID, nil
	if stopProcess && !slices.Contains([]string{"NONE", "EXITED"}, a.ProcessStatus) {
		a.ProcessStatus = "STOPPING"
	}
	out := retirementBinding(a, retirement)
	out.CleanupCommandID = &command.ID
	return out, nil
}

// A timeout leaves the actual cleanup command and pending machine intact. Its
// source-owned timestamp, not a caller-provided elapsed duration, gates revoke.
func (m *Module) completeTimedOutRetirementOn(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int, revoke func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error) (AgentRetirementBinding, error) {
	a, err := m.retirementAgentOn(ctx, tx, tenant, principal, retirement)
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if version < 1 || a.Version != version || a.State != "RETIRING" || a.RetirementID == nil || *a.RetirementID != retirement || a.MachineID != nil || a.PendingCleanupMachineID == nil {
		return AgentRetirementBinding{}, ErrVersionConflict
	}
	q := query.BindAgentMachineCommands(tx, m.names)
	command, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(principal), q.Kind.Eq("CLEANUP"), q.MachineID.Eq(*a.PendingCleanupMachineID)).Order(q.ID.Desc()).First()
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if command.CreatedAt.Add(5*time.Minute).After(time.Now().UTC()) || command.State == "DONE" {
		return AgentRetirementBinding{}, ErrAgentRebindState
	}
	if m.agentRebindQuiet == nil || m.revokeAccount == nil || m.retirementObserver == nil || revoke == nil || m.audit == nil || m.events == nil {
		return AgentRetirementBinding{}, ErrUnavailable
	}
	quiet, err := m.agentRebindQuiet(ctx, tx, tenant, principal)
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if !quiet {
		return AgentRetirementBinding{}, ErrAgentRunningExecutions
	}
	return m.finalizeAgentRetirementOn(ctx, tx, a, retirement, "NORMAL", "TIMED_OUT", RetirementLoss{}, revoke)
}

func (m *Module) agentRetirementCleanupStateOn(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID) (string, error) {
	a, err := m.retirementAgentOn(ctx, tx, tenant, principal, retirement)
	if err != nil {
		return "", err
	}
	if a.RetirementID == nil || *a.RetirementID != retirement || a.State != "RETIRING" || a.MachineID != nil {
		return "", ErrVersionConflict
	}
	q := query.BindAgentMachineCommands(tx, m.names)
	c, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(principal), q.Kind.Eq("CLEANUP")).Order(q.ID.Desc()).First()
	if err != nil {
		return "", err
	}
	if c.State == "DONE" && c.CompletedAt != nil && c.SessionID != nil && c.ConnEpoch > 0 && a.PendingCleanupMachineID == nil {
		return "ACKED", nil
	}
	if c.State == "FAILED" {
		return "FAILED", nil
	}
	return "PENDING", nil
}

// Complete owns Fleet's final transition after the protocol owner has completed
// handover/reassignment and removed project bindings, delegations and channel
// memberships in this same transaction. It disables IAM credentials through
// their owner and freezes Knowledge through the existing retirement observer.
// This normal-flow command requires a genuine cleanup ACK; forced/offline and
// timeout completion require the separate forced-retirement protocol.
func (m *Module) completeAgentRetirementOn(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int) (AgentRetirementBinding, error) {
	return m.completeAgentRetirementRevocationsOn(ctx, tx, tenant, principal, retirement, version, nil)
}

func (m *Module) completeAgentRetirementRevocationsOn(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int, revoke func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error) (AgentRetirementBinding, error) {
	a, err := m.retirementAgentOn(ctx, tx, tenant, principal, retirement)
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if version < 1 || a.Version != version || a.State != "RETIRING" || a.RetirementID == nil || *a.RetirementID != retirement || a.MachineID != nil || a.PendingCleanupMachineID != nil {
		return AgentRetirementBinding{}, ErrVersionConflict
	}
	state, err := m.agentRetirementCleanupStateOn(ctx, tx, tenant, principal, retirement)
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if state != "ACKED" {
		return AgentRetirementBinding{}, ErrAgentRebindState
	}
	if m.revokeAccount == nil || m.retirementObserver == nil || m.agentRebindQuiet == nil || m.audit == nil || m.events == nil {
		return AgentRetirementBinding{}, ErrUnavailable
	}
	if a.Origin == "AGENT_SPAWNED" && a.AdoptedAt == nil && revoke == nil {
		return AgentRetirementBinding{}, ErrUnavailable
	}
	quiet, err := m.agentRebindQuiet(ctx, tx, tenant, principal)
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if !quiet {
		return AgentRetirementBinding{}, ErrAgentRunningExecutions
	}
	return m.finalizeAgentRetirementOn(ctx, tx, a, retirement, "NORMAL", "ACKED", RetirementLoss{}, revoke)
}

// ForceOn is internal to the retirement coordinator, which consumes the
// purpose-bound proof before invoking it. Current Human IAM authority is still
// checked here. Existing machine cleanup stops the process and removes only its
// private directory when the machine next polls, including after reconnect.
func (m *Module) forceAgentRetirementOn(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID, version int, confirmName string, loss RetirementLoss, revoke func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error) (AgentRetirementBinding, error) {
	a, err := m.retirementAgentOn(ctx, tx, tenant, principal, retirement)
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if a.Name != confirmName {
		return AgentRetirementBinding{}, ErrRetirementNameMismatch
	}
	if version < 1 || a.Version != version || a.State == "RETIRED" || (a.RetirementID != nil && *a.RetirementID != retirement) || loss.OpenItems < 0 || loss.RunningExecutions < 0 {
		return AgentRetirementBinding{}, ErrVersionConflict
	}
	if m.revokeAccount == nil || m.retirementObserver == nil || m.audit == nil || m.events == nil || m.ids == nil || revoke == nil {
		return AgentRetirementBinding{}, ErrUnavailable
	}
	// Retired bindings must never keep a private credential delivery ahead of
	// cleanup in the machine's queue. Erase it in the revocation transaction.
	commands := query.BindAgentMachineCommands(tx, m.names)
	if _, err = commands.WithContext(ctx).Where(commands.TenantID.Eq(tenant), commands.AgentPrincipalID.Eq(principal), commands.Kind.Eq("CREDENTIAL"), commands.State.In("QUEUED", "RUNNING")).Updates(map[string]any{"state": "CANCELLED", "sealed_credential": "", "reason_code": "BINDING_CHANGED"}); err != nil { //nolint:misspell // Frozen persisted protocol state.
		return AgentRetirementBinding{}, err
	}
	var commandID *guid.ID
	if a.MachineID != nil {
		id, e := m.ids.Next()
		if e != nil {
			return AgentRetirementBinding{}, e
		}
		if id <= 0 {
			return AgentRetirementBinding{}, ErrUnavailable
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		command := model.AgentMachineCommand{ID: guid.ID(id), TenantID: tenant, AgentPrincipalID: principal, MachineID: *a.MachineID, ConfigRevision: a.ConfigRevision, Kind: "CLEANUP", State: "QUEUED", CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
		if e = query.BindAgentMachineCommands(tx, m.names).WithContext(ctx).Create(&command); e != nil {
			return AgentRetirementBinding{}, e
		}
		commandID = &command.ID
	}
	// Revocation owners require the real runtime retirement state. Establish it
	// in this transaction before their commands; a later failure rolls it back.
	if a.State != "RETIRING" {
		if err = m.retirementTransitionOn(ctx, tx, a, retirement, "RETIRING", "fleet.agent_retirement_started", map[string]any{"retirement_id": retirement}); err != nil {
			return AgentRetirementBinding{}, err
		}
	}
	out, err := m.finalizeAgentRetirementOn(ctx, tx, a, retirement, "FORCED", "PENDING", loss, revoke)
	out.CleanupCommandID = commandID
	return out, err
}

func (m *Module) finalizeAgentRetirementOn(ctx context.Context, tx *gorm.DB, a *model.Agent, retirement guid.ID, mode, cleanup string, loss RetirementLoss, revoke func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error) (AgentRetirementBinding, error) {
	tenant, principal, version := a.TenantID, a.PrincipalID, a.Version
	var err error
	if revoke != nil {
		if err = revoke(ctx, tx, tenant, principal, retirement); err != nil {
			return AgentRetirementBinding{}, err
		}
	}
	if err = m.revokeAccount(ctx, tx, tenant, principal); err != nil {
		return AgentRetirementBinding{}, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	roles := query.BindAgentRoles(tx, m.names)
	if _, err = roles.WithContext(ctx).Where(roles.TenantID.Eq(tenant), roles.AgentID.Eq(a.ID), roles.RevokedAt.IsNull()).Updates(map[string]any{"revoked_at": now, "updated_at": now}); err != nil {
		return AgentRetirementBinding{}, err
	}
	q := query.BindAgents(tx, m.names)
	changes := map[string]any{"state": "RETIRED", "retired_at": now, "retirement_id": retirement, "credential_id": nil, "credential_delivery": "FAILED", "auto_restart": false, "version": version + 1, "updated_at": now, "updated_by": authn.PrincipalIDFromContext(ctx)}
	if mode == "FORCED" {
		changes["machine_id"] = nil
		if a.MachineID != nil {
			changes["pending_cleanup_machine_id"] = a.MachineID
		}
	}
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(a.ID), q.Version.Eq(version)).Updates(changes)
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if changed.RowsAffected != 1 {
		return AgentRetirementBinding{}, ErrVersionConflict
	}
	e := audit.NewDomainEvent(ctx, tenant, "fleet.agent_retired", "agent", a.ID)
	e.Detail["retirement_id"], e.Detail["mode"], e.Detail["cleanup_status"], e.Detail["actor_kind"], e.Detail["authority_source"] = retirement, mode, cleanup, "HUMAN", "IAM:agent_retire"
	if mode == "FORCED" {
		e.Detail["lost"] = loss
		e.Detail["handover_source"] = nil
	}
	if err = m.audit(ctx, tx, e); err != nil {
		return AgentRetirementBinding{}, err
	}
	event, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "agent.retired", Subject: events.Subject{Kind: "agent", ID: principal}, SubjectVersion: int64(version + 1), Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM:agent_retire"}, Data: map[string]any{"agent_id": a.ID, "principal_id": principal, "retirement_id": retirement, "mode": mode, "cleanup_status": cleanup}})
	if err != nil {
		return AgentRetirementBinding{}, err
	}
	if err = m.notifyRetirementOn(ctx, tx, tenant, principal, event); err != nil {
		return AgentRetirementBinding{}, err
	}
	a.State, a.Version = "RETIRED", version+1
	if mode == "FORCED" {
		a.MachineID = nil
	}
	return retirementBinding(a, retirement), nil
}

func (m *Module) retirementTransitionOn(ctx context.Context, tx *gorm.DB, a *model.Agent, retirement guid.ID, state, action string, changes map[string]any) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	now, actor := time.Now().UTC().Truncate(time.Millisecond), authn.PrincipalIDFromContext(ctx)
	changes["state"], changes["version"], changes["updated_at"], changes["updated_by"] = state, a.Version+1, now, actor
	q := query.BindAgents(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(a.TenantID), q.ID.Eq(a.ID), q.Version.Eq(a.Version)).Updates(changes)
	if err != nil {
		return err
	}
	if r.RowsAffected != 1 {
		return ErrVersionConflict
	}
	e := audit.NewDomainEvent(ctx, a.TenantID, action, "agent", a.ID)
	e.Detail["retirement_id"], e.Detail["state"], e.Detail["authority_source"], e.Detail["actor_kind"] = retirement, state, "IAM:agent_retire", "HUMAN"
	if err = m.audit(ctx, tx, e); err != nil {
		return err
	}
	if _, err = m.events.Publish(ctx, tx, events.Event{TenantID: a.TenantID, Type: "agent.state_changed", Subject: events.Subject{Kind: "agent", ID: a.PrincipalID}, SubjectVersion: int64(a.Version + 1), Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "IAM:agent_retire"}, Data: map[string]any{"agent_id": a.ID, "principal_id": a.PrincipalID, "state": state, "retirement_id": retirement}}); err != nil {
		return err
	}
	a.State, a.Version = state, a.Version+1
	if state == "STOPPED" {
		a.RetirementID = nil
	} else {
		a.RetirementID = &retirement
	}
	return nil
}

// AgentProvisioner is the atomic creation path for a newly provisioned IAM
// account. Responsibility roles are not tenant IAM grants: project permissions
// are still assigned exclusively by the project owner.
type AgentProvisioner struct {
	CreateOn func(context.Context, *gorm.DB, guid.ID, AgentRegistration, AgentProfilePatch, identity.ServiceAccountCredentialSecret) (RegisteredAgent, error)
}

func (m *Module) provisionAgentOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in AgentRegistration, profile AgentProfilePatch, secret identity.ServiceAccountCredentialSecret) (RegisteredAgent, error) {
	var empty RegisteredAgent
	if m == nil || secret.Credential.ID <= 0 || secret.Credential.ServiceAccountID != in.PrincipalID || secret.Secret == "" {
		return empty, ErrInvalidArgument
	}
	ring, err := m.agentCredentialRing()
	if err != nil {
		return empty, err
	}
	// Provisioning bypasses only legacy pre-grant validation for this call.
	// The shared module retains its original registration role checks.
	out, err := m.registerAgentOn(ctx, tx, tenant, in, nil, func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) error { return nil })
	if err != nil {
		return empty, err
	}
	profile.Version = out.Version
	configured, err := m.updateAgentProfileOn(ctx, tx, tenant, in.PrincipalID, profile)
	if err != nil {
		return empty, err
	}
	plain, err := json.Marshal(secret)
	if err != nil {
		return empty, err
	}
	sealed, err := ring.Seal("binding", []byte(tenant.String()+":"+in.PrincipalID.String()+":"+secret.Credential.ID.String()), plain)
	if err != nil {
		return empty, ErrUnavailable
	}
	value, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	if value <= 0 {
		return empty, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	command := &model.AgentMachineCommand{ID: guid.ID(value), TenantID: tenant, AgentPrincipalID: in.PrincipalID, MachineID: in.MachineID, CredentialID: &secret.Credential.ID, ConfigRevision: configured.ConfigRevision, Kind: "CREDENTIAL", State: "QUEUED", SealedCredential: sealed, CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	if err = query.BindAgentMachineCommands(tx, m.names).WithContext(ctx).Create(command); err != nil {
		return empty, err
	}
	q := query.BindAgents(tx, m.names)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(out.ID)).Updates(map[string]any{"credential_id": secret.Credential.ID, "credential_delivery": "PENDING"}); err != nil {
		return empty, err
	}
	out.Version = configured.Version
	return out, nil
}

// AgentProfiles keeps persisted runtime configuration owned by Fleet. The
// consumer must check current Human or SELF authority in the same transaction.
type AgentProfiles struct {
	ReadOn              func(context.Context, *gorm.DB, guid.ID, guid.ID) (AgentProfile, error)
	UpdateOn            func(context.Context, *gorm.DB, guid.ID, guid.ID, AgentProfilePatch) (AgentProfile, error)
	RunStateOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (AgentRunState, error)
	BindProcessObserver func(func(context.Context, *gorm.DB, AgentProcessTransition) error) error
	RearmOn             func(context.Context, *gorm.DB, guid.ID, guid.ID) error
	DirectoryOn         func(context.Context, *gorm.DB, guid.ID, FleetRosterFilter, int, int) (FleetRosterPage, error)
}
type FleetRosterFilter struct {
	Search, Role, State, Runtime, Model, Origin, Lineage string
	MachineID, ParentAgentID, SpawnGrantID               guid.ID
}
type FleetRosterAgent struct {
	OwnerID       guid.ID    `json:"owner_id"`
	ID            guid.ID    `json:"id"`
	PrincipalID   guid.ID    `json:"principal_id"`
	Name          string     `json:"name"`
	Handle        string     `json:"handle"`
	State         string     `json:"state"`
	ProcessStatus string     `json:"process_status"`
	MachineOnline bool       `json:"machine_online"`
	Roles         []string   `json:"roles"`
	Runtime       string     `json:"runtime"`
	Model         string     `json:"model"`
	MachineID     *guid.ID   `json:"machine_id"`
	Origin        string     `json:"origin"`
	Lineage       string     `json:"lineage_status"`
	ParentAgentID *guid.ID   `json:"parent_agent_id"`
	SpawnGrantID  *guid.ID   `json:"spawn_grant_id"`
	ExpiresAt     *time.Time `json:"expires_at"`
	LastActiveAt  *time.Time `json:"last_active_at"`
	CreatedAt     time.Time  `json:"created_at"`
	MaxParallel   int        `json:"max_parallel"`
	AvatarStyle   string     `json:"avatar_style"`
	AvatarSeed    int64      `json:"avatar_seed"`
}
type FleetRosterPage struct {
	Items   []FleetRosterAgent
	HasMore bool
}

func (m *Module) fleetRosterOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in FleetRosterFilter, limit, offset int) (FleetRosterPage, error) {
	out := FleetRosterPage{Items: []FleetRosterAgent{}}
	if e := fleetTransaction(tx, tenant, tenant); e != nil {
		return out, e
	}
	if limit < 1 || limit > 200 || offset < 0 {
		return out, ErrInvalidArgument
	}
	q := query.BindAgents(tx, m.names)
	r := q.WithContext(ctx).Where(q.TenantID.Eq(tenant))
	if in.Search != "" {
		r = r.Where(q.WithContext(ctx).Where(q.Name.Like("%" + in.Search + "%")).Or(q.Handle.Like("%" + in.Search + "%")))
	}
	if in.State != "" {
		r = r.Where(q.State.In(strings.Split(in.State, ",")...))
	}
	if in.Runtime != "" {
		r = r.Where(q.RuntimeKind.Eq(in.Runtime))
	}
	if in.Model != "" {
		r = r.Where(q.ModelID.Eq(in.Model))
	}
	if in.Origin != "" {
		r = r.Where(q.Origin.Eq(in.Origin))
	}
	if in.MachineID > 0 {
		r = r.Where(q.MachineID.Eq(in.MachineID))
	}
	if in.ParentAgentID > 0 {
		r = r.Where(q.ParentAgentID.Eq(in.ParentAgentID))
	}
	if in.SpawnGrantID > 0 {
		r = r.Where(q.SpawnGrantID.Eq(in.SpawnGrantID))
	}
	if in.Role != "" {
		ar := query.BindAgentRoles(tx, m.names)
		r = r.Where(gen.Exists(ar.WithContext(ctx).Where(ar.TenantID.Eq(tenant), ar.AgentID.EqCol(q.ID), ar.Role.Eq(in.Role), ar.RevokedAt.IsNull())))
	}
	switch in.Lineage {
	case "HUMAN_CREATED":
		r = r.Where(q.Origin.Eq("HUMAN_CREATED"))
	case "SPAWNED":
		r = r.Where(q.Origin.Eq("AGENT_SPAWNED"), q.AdoptedAt.IsNull(), q.OrphanedAt.IsNull())
	case "ADOPTED":
		r = r.Where(q.AdoptedAt.IsNotNull())
	case "ORPHANED":
		r = r.Where(q.OrphanedAt.IsNotNull())
	case "":
	default:
		return out, ErrInvalidArgument
	}
	rows, e := r.Order(q.ID).Offset(offset).Limit(limit + 1).Find()
	if e != nil {
		return out, e
	}
	if len(rows) > limit {
		out.HasMore = true
		rows = rows[:limit]
	}
	for _, a := range rows {
		identity, found, e := m.ByPrincipalOn(ctx, tx, tenant, a.PrincipalID)
		if e != nil {
			return out, e
		}
		if !found {
			return out, ErrUnavailable
		}
		lineage := "HUMAN_CREATED"
		if a.Origin == "AGENT_SPAWNED" {
			lineage = "SPAWNED"
		}
		if a.AdoptedAt != nil {
			lineage = "ADOPTED"
		}
		if a.OrphanedAt != nil {
			lineage = "ORPHANED"
		}
		online := false
		if a.MachineID != nil {
			machine, err := m.machineOn(ctx, tx, tenant, *a.MachineID, false)
			if err != nil {
				return out, err
			}
			online = machine.CurrentSessionID != nil && machineState(machine, time.Now().UTC()) == "ONLINE"
		}
		out.Items = append(out.Items, FleetRosterAgent{MachineOnline: online, OwnerID: a.OwnerPrincipalID, ID: a.ID, PrincipalID: a.PrincipalID, Name: a.Name, Handle: a.Handle, State: a.State, ProcessStatus: a.ProcessStatus, Roles: identity.Roles, Runtime: a.RuntimeKind, Model: a.ModelID, MachineID: a.MachineID, Origin: a.Origin, Lineage: lineage, ParentAgentID: a.ParentAgentID, SpawnGrantID: a.SpawnGrantID, ExpiresAt: a.ExpiresAt, LastActiveAt: a.LastSeenAt, CreatedAt: a.CreatedAt, MaxParallel: a.MaxParallelItems, AvatarStyle: a.AvatarStyle, AvatarSeed: a.AvatarSeed})
	}
	return out, nil
}

type AgentRunState struct {
	State            string `json:"state"`
	ProcessStatus    string `json:"process_status"`
	RestartCount     int    `json:"restart_count"`
	RestartExhausted bool   `json:"restart_exhausted"`
}
type AgentProcessTransition struct {
	TenantID, AgentPrincipalID guid.ID
	MachineID                  *guid.ID
	Kind                       string
	PID, RestartCount          int
	Reason                     string
	OccurredAt                 time.Time
}

func (m *Module) agentRunStateOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, desired string) (AgentRunState, error) {
	var out AgentRunState
	if desired != "ACTIVE" && desired != "STOPPED" {
		return out, ErrInvalidArgument
	}
	if e := fleetTransaction(tx, tenant, principal); e != nil {
		return out, e
	}
	q := query.BindAgents(tx, m.names)
	before, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal)).First()
	if e != nil {
		return out, e
	}
	if before.MachineID == nil {
		return out, ErrMachineOffline
	}
	machine, e := m.machineOn(ctx, tx, tenant, *before.MachineID, true)
	if e != nil {
		return out, e
	}
	if machineState(machine, time.Now().UTC()) != "ONLINE" || machine.CurrentSessionID == nil {
		return out, ErrMachineOffline
	}
	if m.agentRebindQuiet == nil {
		return out, ErrUnavailable
	}
	quiet, e := m.agentRebindQuiet(ctx, tx, tenant, principal)
	if e != nil {
		return out, e
	}
	if !quiet {
		return out, ErrVersionConflict
	}
	a, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal)).First()
	if e != nil {
		return out, e
	}
	if !slices.Contains([]string{"DRAFT", "ACTIVE", "STOPPED", "CRASHED"}, a.State) {
		return out, ErrVersionConflict
	}
	if desired == "ACTIVE" && !agentBindingReady(a) {
		return out, ErrVersionConflict
	}
	out = AgentRunState{State: a.State, ProcessStatus: a.ProcessStatus, RestartCount: a.RestartCountInWindow, RestartExhausted: a.RestartExhausted}
	if a.State == desired {
		return out, nil
	}
	status, kind := "STARTING", "START_REQUESTED"
	if desired == "STOPPED" {
		status, kind = "STOPPING", "STOP_REQUESTED"
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	changes := map[string]any{"state": desired, "process_status": status, "process_status_at": now, "updated_at": now, "version": a.Version + 1}
	if desired == "ACTIVE" {
		changes["running_config_revision"] = nil
		changes["restart_count_in_window"] = 0
		changes["restart_exhausted"] = false
		changes["restart_window_started_at"] = nil
	}
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(a.ID), q.Version.Eq(a.Version)).Updates(changes); e != nil {
		return out, e
	}
	if m.processObserver != nil {
		if e = m.processObserver(ctx, tx, AgentProcessTransition{TenantID: tenant, AgentPrincipalID: principal, MachineID: a.MachineID, Kind: kind, OccurredAt: now}); e != nil {
			return out, e
		}
	}
	return AgentRunState{State: desired, ProcessStatus: status}, nil
}

type AgentProfile struct {
	ID                    guid.ID         `json:"id"`
	PrincipalID           guid.ID         `json:"principal_id"`
	Name                  string          `json:"name"`
	Handle                string          `json:"handle"`
	Persona               *string         `json:"persona"`
	AvatarStyle           string          `json:"avatar_style"`
	AvatarSeed            int64           `json:"avatar_seed"`
	AvatarURL             string          `json:"avatar_url,omitempty"`
	SystemPrompt          string          `json:"system_prompt"`
	ReasoningEffort       *string         `json:"reasoning_effort"`
	Runtime               string          `json:"runtime"`
	Model                 string          `json:"model"`
	AuthorizationMode     string          `json:"authorization_mode"`
	MaxContextTokens      int             `json:"max_context_tokens"`
	MCPAllowlist          []string        `json:"mcp_allowlist"`
	CapabilityTags        []string        `json:"capability_tags"`
	DefaultChannelNames   []string        `json:"default_channel_names"`
	MemoryPolicy          json.RawMessage `json:"memory_policy"`
	MaxParallelItems      int             `json:"max_parallel_items"`
	MaxRisk               string          `json:"max_risk"`
	AutoRestart           bool            `json:"auto_restart"`
	ConfigRevision        int             `json:"config_revision"`
	RunningConfigRevision *int            `json:"running_config_revision"`
	Version               int             `json:"version"`
}
type AgentProfilePatch struct {
	Version             int              `json:"version" minimum:"1"`
	Name                *string          `json:"name,omitempty" minLength:"1" maxLength:"64"`
	Persona             *string          `json:"persona,omitempty" maxLength:"2000"`
	AvatarStyle         *string          `json:"avatar_style,omitempty" enum:"ROBOT,PIXEL,MONSTER,GEOMETRIC"`
	AvatarSeed          *int64           `json:"avatar_seed,omitempty" minimum:"0" maximum:"9007199254740991"`
	SystemPrompt        *string          `json:"system_prompt,omitempty" minLength:"1" maxLength:"20000"`
	ReasoningEffort     *string          `json:"reasoning_effort,omitempty" maxLength:"16"`
	MaxContextTokens    *int             `json:"max_context_tokens,omitempty" minimum:"4000" maximum:"200000"`
	MCPAllowlist        *[]string        `json:"mcp_allowlist,omitempty" maxItems:"32"`
	CapabilityTags      *[]string        `json:"capability_tags,omitempty" maxItems:"20"`
	DefaultChannelNames *[]string        `json:"default_channel_names,omitempty" maxItems:"10"`
	MemoryPolicy        *json.RawMessage `json:"memory_policy,omitempty"`
	MaxParallelItems    *int             `json:"max_parallel_items,omitempty" minimum:"1" maximum:"4"`
	MaxRisk             *string          `json:"max_risk,omitempty" enum:"LOW,MEDIUM,HIGH"`
	AutoRestart         *bool            `json:"auto_restart,omitempty"`
}

func (m *Module) agentProfileOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) (AgentProfile, error) {
	var out AgentProfile
	if e := fleetTransaction(tx, tenant, principal); e != nil {
		return out, e
	}
	q := query.BindAgents(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal)).First()
	if e != nil {
		return out, e
	}
	out = AgentProfile{ID: r.ID, PrincipalID: r.PrincipalID, Name: r.Name, Handle: r.Handle, Persona: r.Persona, AvatarStyle: r.AvatarStyle, AvatarSeed: r.AvatarSeed, SystemPrompt: r.SystemPrompt, ReasoningEffort: r.ReasoningEffort, Runtime: r.RuntimeKind, Model: r.ModelID, AuthorizationMode: r.AuthorizationMode, MaxContextTokens: r.MaxContextTokens, MemoryPolicy: json.RawMessage(r.MemoryPolicy), MaxParallelItems: r.MaxParallelItems, MaxRisk: r.MaxRisk, AutoRestart: r.AutoRestart, ConfigRevision: r.ConfigRevision, RunningConfigRevision: r.RunningConfigRevision, Version: r.Version}
	for _, value := range []struct {
		raw  string
		dest *[]string
	}{{r.MCPAllowlist, &out.MCPAllowlist}, {r.CapabilityTags, &out.CapabilityTags}, {r.DefaultChannelNames, &out.DefaultChannelNames}} {
		if e = json.Unmarshal([]byte(value.raw), value.dest); e != nil {
			return AgentProfile{}, ErrUnavailable
		}
	}
	if !json.Valid(out.MemoryPolicy) {
		return AgentProfile{}, ErrUnavailable
	}
	return out, nil
}

func (m *Module) updateAgentProfileOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, in AgentProfilePatch) (AgentProfile, error) {
	var empty AgentProfile
	if e := fleetTransaction(tx, tenant, principal); e != nil {
		return empty, e
	}
	if in.Version < 1 {
		return empty, ErrInvalidArgument
	}
	q := query.BindAgents(tx, m.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal)).First()
	if e != nil {
		return empty, e
	}
	if r.Version != in.Version || slices.Contains([]string{"RETIRING", "RETIRED"}, r.State) {
		return empty, ErrVersionConflict
	}
	if r.CredentialID != nil && r.CredentialDelivery != "DELIVERED" {
		return empty, ErrVersionConflict
	}
	changes := map[string]any{}
	for _, f := range []struct {
		key      string
		value    *string
		max      int
		required bool
	}{{"name", in.Name, 64, true}, {"persona", in.Persona, 2000, false}, {"system_prompt", in.SystemPrompt, 20000, true}, {"reasoning_effort", in.ReasoningEffort, 16, true}, {"avatar_style", in.AvatarStyle, 32, true}, {"max_risk", in.MaxRisk, 32, true}} {
		if f.value == nil {
			continue
		}
		if !validProfileText(*f.value, f.max, f.required) {
			return empty, ErrInvalidArgument
		}
		changes[f.key] = *f.value
	}
	if in.Name != nil {
		others, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.Neq("RETIRED"), q.ID.Neq(r.ID)).Find()
		if err != nil {
			return empty, err
		}
		for _, other := range others {
			if strings.EqualFold(other.Name, *in.Name) {
				return empty, ErrVersionConflict
			}
		}
	}
	if in.AvatarStyle != nil && !slices.Contains([]string{"ROBOT", "PIXEL", "MONSTER", "GEOMETRIC"}, *in.AvatarStyle) {
		return empty, ErrInvalidArgument
	}
	if in.MaxRisk != nil && !slices.Contains([]string{"LOW", "MEDIUM", "HIGH"}, *in.MaxRisk) {
		return empty, ErrInvalidArgument
	}
	if in.AvatarSeed != nil {
		if *in.AvatarSeed < 0 || *in.AvatarSeed > 9007199254740991 {
			return empty, ErrInvalidArgument
		}
		changes["avatar_seed"] = *in.AvatarSeed
	}
	if in.MaxContextTokens != nil {
		if *in.MaxContextTokens < 4000 || *in.MaxContextTokens > 200000 {
			return empty, ErrInvalidArgument
		}
		changes["max_context_tokens"] = *in.MaxContextTokens
	}
	if in.MaxParallelItems != nil {
		if *in.MaxParallelItems < 1 || *in.MaxParallelItems > 4 {
			return empty, ErrInvalidArgument
		}
		changes["max_parallel_items"] = *in.MaxParallelItems
	}
	if in.MemoryPolicy != nil {
		if !json.Valid(*in.MemoryPolicy) || len(*in.MemoryPolicy) > 8192 {
			return empty, ErrInvalidArgument
		}
		changes["memory_policy"] = string(*in.MemoryPolicy)
	}
	if in.AutoRestart != nil {
		changes["auto_restart"] = *in.AutoRestart
	}
	for _, f := range []struct {
		key    string
		values *[]string
		max    int
	}{{"mcp_allowlist", in.MCPAllowlist, 32}, {"capability_tags", in.CapabilityTags, 20}, {"default_channel_names", in.DefaultChannelNames, 10}} {
		if f.values == nil {
			continue
		}
		if len(*f.values) > f.max {
			return empty, ErrInvalidArgument
		}
		seen := map[string]bool{}
		for _, value := range *f.values {
			if !validProfileText(value, 128, true) || seen[value] {
				return empty, ErrInvalidArgument
			}
			if f.key == "capability_tags" && !profileTag.MatchString(value) {
				return empty, ErrInvalidArgument
			}
			seen[value] = true
		}
		values := *f.values
		if values == nil {
			values = []string{}
		}
		raw, err := json.Marshal(values)
		if err != nil {
			return empty, err
		}
		changes[f.key] = string(raw)
	}
	if in.ReasoningEffort != nil || in.MCPAllowlist != nil && len(*in.MCPAllowlist) > 0 {
		if r.MachineID == nil {
			return empty, ErrMachineOffline
		}
		machine, err := m.machineOn(ctx, tx, tenant, *r.MachineID, true)
		if err != nil {
			return empty, err
		}
		if machine.CurrentSessionID == nil {
			return empty, ErrMachineOffline
		}
		iq := query.BindInspections(tx, m.names)
		for _, kind := range []string{"MODELS", "CAPABILITIES"} {
			if kind == "MODELS" && in.ReasoningEffort == nil || kind == "CAPABILITIES" && (in.MCPAllowlist == nil || len(*in.MCPAllowlist) == 0) {
				continue
			}
			reports, err := iq.WithContext(ctx).Where(iq.TenantID.Eq(tenant), iq.MachineID.Eq(machine.ID), iq.SessionID.Eq(*machine.CurrentSessionID), iq.ConnEpoch.Eq(machine.ConnEpoch), iq.Kind.Eq(kind), iq.ScopeKey.Eq("home")).Order(iq.ID.Desc()).Limit(1).Find()
			if err != nil {
				return empty, err
			}
			if len(reports) != 1 || reports[0].State != "READY" {
				return empty, ErrUnavailable
			}
			var facts []RuntimeDiscovery
			if json.Unmarshal([]byte(reports[0].ResultJSON), &facts) != nil {
				return empty, ErrUnavailable
			}
			valid := false
			for _, fact := range facts {
				if fact.Runtime != r.RuntimeKind || fact.State != "READY" {
					continue
				}
				if kind == "MODELS" {
					for _, model := range fact.Reasoning {
						if model.Model == r.ModelID && slices.Contains(model.Efforts, *in.ReasoningEffort) {
							valid = true
						}
					}
				} else {
					valid = true
					for _, server := range *in.MCPAllowlist {
						if !slices.Contains(fact.MCP, server) {
							valid = false
						}
					}
				}
			}
			if !valid {
				return empty, ErrInvalidArgument
			}
		}
	}
	if len(changes) == 0 {
		return m.agentProfileOn(ctx, tx, tenant, principal)
	}
	changes["version"], changes["config_revision"], changes["updated_at"] = r.Version+1, r.ConfigRevision+1, time.Now().UTC().Truncate(time.Millisecond)
	res, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(r.ID), q.Version.Eq(in.Version)).Updates(changes)
	if e != nil {
		return empty, e
	}
	if res.RowsAffected != 1 {
		return empty, ErrVersionConflict
	}
	return m.agentProfileOn(ctx, tx, tenant, principal)
}

func validProfileText(value string, limit int, required bool) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit && (!required || strings.TrimSpace(value) != "")
}

var profileTag = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func (m *Module) agentWorkspaceBindingOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (AgentWorkspaceFence, error) {
	var out AgentWorkspaceFence
	if e := fleetTransaction(tx, tenant, agent); e != nil {
		return out, e
	}
	q := query.BindAgents(tx, m.names)
	a, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(agent)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, ErrMachineNotFound
	}
	if e != nil {
		return out, e
	}
	if a.MachineID == nil {
		return out, ErrMachineOffline
	}
	machine, e := m.machineOn(ctx, tx, tenant, *a.MachineID, true)
	if e != nil {
		return out, e
	}
	if machineState(machine, time.Now().UTC()) != "ONLINE" || machine.CurrentSessionID == nil {
		return out, ErrMachineOffline
	}
	return AgentWorkspaceFence{MachineID: machine.ID, SessionID: *machine.CurrentSessionID, ConnEpoch: machine.ConnEpoch, ConfigRevision: a.ConfigRevision}, nil
}

func (m *Module) rearmAgentOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) error {
	if e := fleetTransaction(tx, tenant, principal); e != nil {
		return e
	}
	q := query.BindAgents(tx, m.names)
	row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal)).First()
	if e != nil {
		return e
	}
	_, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal)).Updates(map[string]any{"state": "ACTIVE", "process_status": "STARTING", "process_status_at": time.Now().UTC().Truncate(time.Millisecond), "running_config_revision": nil, "version": row.Version + 1, "restart_count_in_window": 0, "restart_exhausted": false, "restart_window_started_at": nil})
	return e
}
