package fleet

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

var (
	ErrUnavailable     = errors.New("fleet: directory unavailable")
	ErrInvalidArgument = errors.New("fleet: invalid directory request")
	ErrInvalidRoles    = errors.New("fleet: invalid Agent roles")
)

// AgentDirectory exposes identity facts, not IAM permissions. ByPrincipalOn
// uses the caller transaction; tenant membership must be checked by the caller.
// Other directory operations (load, runtime profile, statistics) remain separate
// work and are not supplied by this identity capability.
type AgentDirectory struct {
	ByPrincipalOn  func(context.Context, *gorm.DB, guid.ID, guid.ID) (AgentIdentity, bool, error)
	ActiveByRoleOn func(context.Context, *gorm.DB, guid.ID, string) ([]AgentIdentity, error)
}

// ActiveByRoleOn enumerates at most 100 actual active identities for bounded
// participant locking. Overflow is unavailable, never proof of no candidates.
// Callers recheck current identity and IAM after acquiring principal locks.
func (m *Module) ActiveByRoleOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, role string) ([]AgentIdentity, error) {
	if m == nil || m.names.Agents.IsZero() || m.names.AgentRoles.IsZero() {
		return nil, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	if _, valid := AgentIAMRoleKey(role); !valid {
		return nil, ErrInvalidRoles
	}
	a, r := query.BindAgents(tx, m.names), query.BindAgentRoles(tx, m.names)
	rows, err := a.WithContext(ctx).Select(a.ALL).Join(r, a.ID.EqCol(r.AgentID), a.TenantID.EqCol(r.TenantID)).Where(a.TenantID.Eq(tenant), a.State.Eq("ACTIVE"), r.Role.Eq(role), r.RevokedAt.IsNull()).Order(a.PrincipalID).Limit(101).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 100 {
		return nil, ErrUnavailable
	}
	out := make([]AgentIdentity, 0, len(rows))
	for _, row := range rows {
		identity, found, err := m.ByPrincipalOn(ctx, tx, tenant, row.PrincipalID)
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, identity)
		}
	}
	return out, nil
}

// AgentIdentity exposes internal identity/configuration facts; the prompt is never serialized.
type AgentIdentity struct {
	AuthorizationMode                           string
	SystemPrompt                                string `json:"-"`
	ID, TenantID, PrincipalID, OwnerPrincipalID guid.ID
	Name, State, Origin, ModelFamily            string
	RuntimeKind, ModelID                        string
	ParentAgentID                               *guid.ID
	AdoptedAt                                   *time.Time
	Roles                                       []string
	IAMRoleKeys                                 []string
}

// AcceptsInboxEvents describes lifecycle eligibility for durable receipts only.
// It grants neither source access nor authority to wake or execute a runtime.
func (a AgentIdentity) AcceptsInboxEvents() bool {
	switch a.State {
	case "DRAFT", "STOPPED", "ACTIVE", "CRASHED", "RETIRING":
		return true
	default:
		return false
	}
}

// AgentIAMRoleKey returns the frozen role mapping used by Agent configuration
// and project binding validation. Prefix matching alone is not authorization.
func AgentIAMRoleKey(role string) (string, bool) {
	switch role {
	case "EXECUTOR":
		return "agent_executor", true
	case "REVIEWER":
		return "agent_reviewer", true
	case "COORDINATOR":
		return "agent_coordinator", true
	case "LEAD":
		return "agent_lead", true
	case "TESTER":
		return "agent_tester", true
	case "ACCEPTOR":
		return "agent_acceptor", true
	case "ASSISTANT":
		return "agent_assistant", true
	default:
		return "", false
	}
}

func (m *Module) ByPrincipalOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) (AgentIdentity, bool, error) {
	return m.agentIdentityOn(ctx, tx, tenant, principal, true)
}

func (m *Module) agentIdentityOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, resolveFamily bool) (AgentIdentity, bool, error) {
	if m == nil || m.names.Agents.IsZero() || m.names.AgentRoles.IsZero() {
		return AgentIdentity{}, false, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 || principal <= 0 {
		return AgentIdentity{}, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return AgentIdentity{}, false, ErrInvalidArgument
	}
	agents := query.BindAgents(tx, m.names)
	row, err := agents.WithContext(ctx).Where(agents.TenantID.Eq(tenant), agents.PrincipalID.Eq(principal)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentIdentity{}, false, nil
	}
	if err != nil {
		return AgentIdentity{}, false, err
	}
	roles := query.BindAgentRoles(tx, m.names)
	rows, err := roles.WithContext(ctx).Where(roles.TenantID.Eq(tenant), roles.AgentID.Eq(row.ID), roles.RevokedAt.IsNull()).Order(roles.Role).Find()
	if err != nil {
		return AgentIdentity{}, false, err
	}
	result := AgentIdentity{AuthorizationMode: row.AuthorizationMode, SystemPrompt: row.SystemPrompt, ID: row.ID, TenantID: row.TenantID, PrincipalID: row.PrincipalID, OwnerPrincipalID: row.OwnerPrincipalID, Name: row.Name, State: row.State, Origin: row.Origin, ModelFamily: row.ModelFamily, RuntimeKind: row.RuntimeKind, ModelID: row.ModelID, ParentAgentID: row.ParentAgentID, AdoptedAt: row.AdoptedAt, Roles: []string{}, IAMRoleKeys: []string{}}
	if resolveFamily && row.MachineID != nil {
		result.ModelFamily, err = m.ModelFamilyOn(ctx, tx, tenant, *row.MachineID, row.RuntimeKind, row.ModelID)
		if err != nil {
			return AgentIdentity{}, false, err
		}
	}
	seen := make(map[string]bool, len(rows))
	for _, role := range rows {
		key, valid := AgentIAMRoleKey(role.Role)
		if !valid || key != role.IAMRoleKey {
			return AgentIdentity{}, false, ErrInvalidRoles
		}
		seen[role.Role] = true
		result.Roles = append(result.Roles, role.Role)
		result.IAMRoleKeys = append(result.IAMRoleKeys, key)
	}
	if (seen["ACCEPTOR"] && seen["EXECUTOR"]) || (seen["ASSISTANT"] && (seen["EXECUTOR"] || seen["TESTER"] || seen["ACCEPTOR"])) {
		return AgentIdentity{}, false, ErrInvalidRoles
	}
	return result, true, nil
}
