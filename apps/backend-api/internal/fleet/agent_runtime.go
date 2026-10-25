package fleet

import (
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
	"time"
)

// AgentRuntimeFacts keeps machine/process identity in fleet. Callers authorize
// the exact agent before requesting these facts; connectivity is not activity.
type AgentRuntimeFacts struct {
	RetirementOn        func(context.Context, *gorm.DB, guid.ID, guid.ID) (AgentRuntimeFact, bool, error)
	PrepareResetOn      func(context.Context, *gorm.DB, guid.ID, guid.ID) error
	RequestResetOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, int, int) error
	BindResetCompletion func(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error) error
	ByPrincipalOn       func(context.Context, *gorm.DB, guid.ID, guid.ID) (AgentRuntimeFact, bool, error)
}
type AgentRuntimeFact struct {
	MachineID       *guid.ID   `json:"machine_id,omitempty"`
	MachineState    string     `json:"machine_state"`
	RuntimeKind     string     `json:"runtime_kind"`
	ProcessStatus   string     `json:"process_status"`
	ProcessStatusAt *time.Time `json:"process_status_at,omitempty"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
}

func (m *Module) provideAgentRuntime(s *module.Services) error {
	return module.Provide[AgentRuntimeFacts](s, AgentRuntimeFacts{RetirementOn: func(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) (AgentRuntimeFact, bool, error) {
		return m.agentRuntimeFactOn(ctx, tx, tenant, principal, false)
	}, ByPrincipalOn: m.agentRuntimeOn, PrepareResetOn: m.prepareAgentResetOn, RequestResetOn: m.enqueueAgentResetOn, BindResetCompletion: m.bindResetCompletion})
}
func (m *Module) agentRuntimeOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) (AgentRuntimeFact, bool, error) {
	return m.agentRuntimeFactOn(ctx, tx, tenant, principal, true)
}

// Retirement must disclose a bound machine even before private credential
// delivery. Ordinary runtime operations still require the ready binding.
func (m *Module) agentRuntimeFactOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, requireReady bool) (AgentRuntimeFact, bool, error) {
	_, found, err := m.agentIdentityOn(ctx, tx, tenant, principal, false)
	if err != nil || !found {
		return AgentRuntimeFact{}, found, err
	}
	q := query.BindAgents(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal)).First()
	if err != nil {
		return AgentRuntimeFact{}, false, err
	}
	if requireReady && !agentBindingReady(r) {
		return AgentRuntimeFact{}, false, ErrVersionConflict
	}
	out := AgentRuntimeFact{MachineID: r.MachineID, MachineState: "UNASSIGNED", RuntimeKind: r.RuntimeKind, ProcessStatus: r.ProcessStatus, ProcessStatusAt: r.ProcessStatusAt, LastSeenAt: r.LastSeenAt}
	if r.MachineID != nil {
		machine, e := m.MachineByIDOn(ctx, tx, tenant, *r.MachineID)
		if e != nil {
			return AgentRuntimeFact{}, false, e
		}
		out.MachineState = machine.State
	}
	return out, true, nil
}
