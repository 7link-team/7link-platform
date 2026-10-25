package fleet

import (
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
)

// ProjectArchiveFact exposes actual terminal release and ensemble decision facts.
type ProjectArchiveFact struct {
	Kind    string
	Version int
	ID      guid.ID
	State   string
	Ready   bool
}
type ProjectArchiveFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveFact, error)

func (m *Module) provideArchiveVerifiers(s *module.Services) error {
	if err := module.Provide[VerifierAgents](s, VerifierAgents{ReadOn: m.verifierAgentOn}); err != nil {
		return err
	}
	if err := module.Provide[VerifierTools](s, VerifierTools{ReadOn: m.verifierToolsOn}); err != nil {
		return err
	}
	return module.Provide[ProjectArchiveFacts](s, m.projectArchiveFactsOn)
}

type VerifierAgentConfiguration struct {
	AgentID      guid.ID `json:"agent_id"`
	Role         string  `json:"role"`
	Model        string  `json:"model"`
	Family       string  `json:"family"`
	SHA256       string  `json:"configuration_sha256"`
	Instructions string  `json:"-"`
}
type VerifierAgents struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string) (VerifierAgentConfiguration, error)
}

func (m *Module) verifierAgentOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, role, model string) (VerifierAgentConfiguration, error) {
	var out VerifierAgentConfiguration
	if fleetTransaction(tx, tenant, id) != nil || role != "ACCEPTOR" || strings.TrimSpace(model) == "" {
		return out, ErrInvalidArgument
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "agent_read"); err != nil {
		return out, err
	}
	q := query.BindAgents(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return out, err
	}
	if err = m.principals.Lock(ctx, tx, row.PrincipalID); err != nil {
		return out, err
	}
	if m.accounts == nil {
		return out, ErrUnavailable
	}
	active, err := m.accounts(ctx, tx, tenant, row.PrincipalID, time.Now().UTC())
	if err != nil {
		return out, err
	}
	if !active {
		return out, ErrUnavailable
	}
	identity, found, err := m.ByPrincipalOn(ctx, tx, tenant, row.PrincipalID)
	if err != nil {
		return out, err
	}
	if !found || identity.State != "ACTIVE" || !slices.Contains(identity.Roles, role) || identity.ModelID != model || strings.TrimSpace(identity.SystemPrompt) == "" {
		return out, ErrUnavailable
	}
	raw, _ := json.Marshal(struct {
		Revision                          int
		Role, Model, Family, Instructions string
	}{row.ConfigRevision, role, model, identity.ModelFamily, identity.SystemPrompt})
	digest := sha256.Sum256(raw)
	return VerifierAgentConfiguration{AgentID: id, Role: role, Model: model, Family: identity.ModelFamily, SHA256: hex.EncodeToString(digest[:]), Instructions: identity.SystemPrompt}, nil
}

// VerifierTools publishes observed installation fingerprints, never probe
// results or calibration. The caller must still run known-good/bad samples.
type VerifierTools struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) (string, error)
}

func (m *Module) verifierToolsOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, required []string) (string, error) {
	if fleetTransaction(tx, tenant, machine) != nil || len(required) == 0 || len(required) > 20 {
		return "", ErrInvalidArgument
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "machine_read"); err != nil {
		return "", err
	}
	row, err := m.machineOn(ctx, tx, tenant, machine, true)
	if err != nil {
		return "", err
	}
	if machineState(row, time.Now().UTC()) != "ONLINE" {
		return "", ErrMachineOffline
	}
	names := slices.Clone(required)
	slices.Sort(names)
	q := query.BindTools(tx, m.names)
	facts := []struct{ Name, Version, Path string }{}
	for i, name := range names {
		if name == "" || i > 0 && name == names[i-1] {
			return "", ErrInvalidArgument
		}
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.Name.Eq(name)).Limit(2).Find()
		if err != nil {
			return "", err
		}
		if len(rows) != 1 || strings.TrimSpace(rows[0].Version) == "" || rows[0].ExecutablePath == "" || rows[0].InspectedAt.IsZero() {
			return "", ErrUnavailable
		}
		facts = append(facts, struct{ Name, Version, Path string }{name, rows[0].Version, rows[0].ExecutablePath})
	}
	raw, _ := json.Marshal(facts)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
func (m *Module) projectArchiveFactsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]ProjectArchiveFact, error) {
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	q := query.BindTerminalSessions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	out := make([]ProjectArchiveFact, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProjectArchiveFact{ID: r.ID, State: r.State, Kind: "TERMINAL", Version: 1, Ready: r.State == "CLOSED" && r.ClosedAt != nil && r.ActiveExecution == nil})
	}
	plan := query.BindEnsemblePlans(tx, m.names)
	plans, err := plan.WithContext(ctx).Where(plan.TenantID.Eq(tenant), plan.ProjectID.Eq(project), plan.State.In("DRAFT", "COMPARING")).Clauses(clause.Locking{Strength: "UPDATE"}).Order(plan.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range plans {
		if (row.State == "DRAFT" && row.ApprovalRequestedAt != nil && row.ApprovedBy == nil) || (row.State == "COMPARING" && row.SelectedBy == nil) {
			out = append(out, ProjectArchiveFact{Kind: "ENSEMBLE_DECISION", ID: row.ID, State: row.State, Version: row.Version})
		}
	}
	return out, nil
}
