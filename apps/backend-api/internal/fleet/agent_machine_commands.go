package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"time"
)

type AgentMachineCommandView struct {
	ID               guid.ID   `json:"id"`
	AgentPrincipalID guid.ID   `json:"agent_principal_id"`
	MachineID        guid.ID   `json:"machine_id"`
	CredentialID     *guid.ID  `json:"credential_id,omitempty"`
	ConfigRevision   int       `json:"config_revision"`
	Kind             string    `json:"kind" enum:"CLEANUP,CREDENTIAL"`
	State            string    `json:"state" enum:"QUEUED,RUNNING,DONE,FAILED,CANCELLED"` //nolint:misspell // Frozen persisted protocol state.
	ReasonCode       string    `json:"reason_code,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
}
type AgentMachineDelivery struct {
	AgentMachineCommandView
	ClientID *guid.ID `json:"client_id,omitempty"`
	Secret   string   `json:"client_secret,omitempty" doc:"Private credential delivery; never log or include in a receipt."`
}

func machineCommandView(r *model.AgentMachineCommand) AgentMachineCommandView {
	return AgentMachineCommandView{ID: r.ID, AgentPrincipalID: r.AgentPrincipalID, MachineID: r.MachineID, CredentialID: r.CredentialID, ConfigRevision: r.ConfigRevision, Kind: r.Kind, State: r.State, ReasonCode: r.ReasonCode, ExpiresAt: r.ExpiresAt}
}
func (m *Module) agentCommandStore(tx *gorm.DB) *gorm.DB {
	return query.BindAgentMachineCommands(tx, m.names).WithContext(tx.Statement.Context).UnderlyingDB()
}
func (m *Module) lockAgentCommandPrincipals(ctx context.Context, tx *gorm.DB, agent guid.ID) error {
	if m.principals.Lock == nil {
		return ErrUnavailable
	}
	ids := []guid.ID{authn.PrincipalIDFromContext(ctx), agent}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if id <= 0 {
			return ErrMachineForbidden
		}
		if err := m.principals.Lock(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) pullAgentMachineCommandsOn(ctx context.Context, tx *gorm.DB, tenant, machine, session guid.ID, epoch int64) ([]AgentMachineDelivery, error) {
	if fleetTransaction(tx, tenant, machine) != nil {
		return nil, ErrInvalidArgument
	}
	var snapshot model.AgentMachineCommand
	err := m.agentCommandStore(tx.WithContext(ctx)).Where(map[string]any{"tenant_id": tenant, "machine_id": machine}).Where(clause.IN{Column: clause.Column{Name: "state"}, Values: []any{"QUEUED", "RUNNING"}}).Where(clause.Gt{Column: clause.Column{Name: "expires_at"}, Value: time.Now().UTC()}).Order("id").Take(&snapshot).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err = m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); err != nil {
			return nil, err
		}
		return []AgentMachineDelivery{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err = m.lockAgentCommandPrincipals(ctx, tx, snapshot.AgentPrincipalID); err != nil {
		return nil, err
	}
	if err = m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); err != nil {
		return nil, err
	}
	var row model.AgentMachineCommand
	if err = m.agentCommandStore(tx.WithContext(ctx)).Where(map[string]any{"tenant_id": tenant, "machine_id": machine, "id": snapshot.ID}).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&row).Error; err != nil {
		return nil, err
	}
	if row.State != "QUEUED" && row.State != "RUNNING" {
		return []AgentMachineDelivery{}, nil
	}
	q := query.BindAgents(tx, m.names)
	a, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(row.AgentPrincipalID)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil {
		return nil, err
	}
	stale := row.Kind == "CREDENTIAL" && (a.MachineID == nil || *a.MachineID != machine || a.ConfigRevision != row.ConfigRevision || a.CredentialID == nil || row.CredentialID == nil || *a.CredentialID != *row.CredentialID) || row.Kind == "CLEANUP" && a.MachineID != nil && *a.MachineID == machine
	if stale {
		err = m.agentCommandStore(tx).Where(map[string]any{"id": row.ID, "tenant_id": tenant}).Updates(map[string]any{"state": "CANCELLED", "sealed_credential": "", "reason_code": "BINDING_CHANGED"}).Error //nolint:misspell // Frozen persisted protocol state.
		return []AgentMachineDelivery{}, err
	}
	out := AgentMachineDelivery{AgentMachineCommandView: machineCommandView(&row)}
	if row.Kind == "CREDENTIAL" {
		if row.CredentialID == nil {
			return nil, ErrUnavailable
		}
		ring, e := m.agentCredentialRing()
		if e != nil {
			return nil, e
		}
		plain, e := ring.Open("binding", []byte(tenant.String()+":"+row.AgentPrincipalID.String()+":"+row.CredentialID.String()), row.SealedCredential)
		if e != nil {
			return nil, ErrUnavailable
		}
		var secret identity.ServiceAccountCredentialSecret
		if json.Unmarshal(plain, &secret) != nil || secret.Credential.ID != *row.CredentialID || secret.Secret == "" {
			return nil, ErrUnavailable
		}
		out.ClientID = row.CredentialID
		out.Secret = secret.Secret
	}
	if err = m.agentCommandStore(tx).Where(map[string]any{"id": row.ID, "tenant_id": tenant}).Updates(map[string]any{"state": "RUNNING", "session_id": session, "conn_epoch": epoch}).Error; err != nil {
		return nil, err
	}
	out.State = "RUNNING"
	return []AgentMachineDelivery{out}, nil
}
func (m *Module) reportAgentMachineCommandOn(ctx context.Context, tx *gorm.DB, tenant, machine, id, session guid.ID, epoch int64, state, reason string) (AgentMachineCommandView, error) {
	var out AgentMachineCommandView
	if fleetTransaction(tx, tenant, machine) != nil {
		return out, ErrInvalidArgument
	}
	if state != "DONE" && state != "FAILED" || state == "DONE" && reason != "" || state == "FAILED" && !slices.Contains([]string{"CREDENTIAL_STORE_FAILED", "CLEANUP_FAILED", "INVALID_PRIVATE_DIRECTORY"}, reason) {
		return out, ErrInvalidArgument
	}
	var row model.AgentMachineCommand
	err := m.agentCommandStore(tx.WithContext(ctx)).Where(map[string]any{"tenant_id": tenant, "machine_id": machine, "id": id}).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrMachineNotFound
	}
	if err != nil {
		return out, err
	}
	if err = m.lockAgentCommandPrincipals(ctx, tx, row.AgentPrincipalID); err != nil {
		return out, err
	}
	if err = m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); err != nil {
		return out, err
	}
	if err = m.agentCommandStore(tx).Where(map[string]any{"tenant_id": tenant, "id": id}).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&row).Error; err != nil {
		return out, err
	}
	if row.SessionID == nil || *row.SessionID != session || row.ConnEpoch != epoch {
		return out, ErrSessionStale
	}
	if row.State == state && row.ReasonCode == reason {
		return machineCommandView(&row), nil
	}
	if row.State != "RUNNING" || !row.ExpiresAt.After(time.Now().UTC()) {
		return out, ErrVersionConflict
	}
	q := query.BindAgents(tx, m.names)
	a, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(row.AgentPrincipalID)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil {
		return out, err
	}
	values := map[string]any{}
	if row.Kind == "CREDENTIAL" {
		if a.MachineID == nil || *a.MachineID != machine || a.ConfigRevision != row.ConfigRevision || a.CredentialID == nil || row.CredentialID == nil || *a.CredentialID != *row.CredentialID {
			return out, ErrVersionConflict
		}
		values["credential_delivery"] = map[string]string{"DONE": "DELIVERED", "FAILED": "FAILED"}[state]
	}
	if row.Kind == "CLEANUP" && state == "DONE" {
		if a.MachineID != nil && *a.MachineID == machine {
			return out, ErrVersionConflict
		}
		if a.PendingCleanupMachineID != nil && *a.PendingCleanupMachineID == machine {
			values["pending_cleanup_machine_id"] = nil
			values["process_status"], values["process_status_at"] = "EXITED", time.Now().UTC()
		}
	}
	if len(values) > 0 {
		values["updated_at"] = time.Now().UTC()
		values["version"] = a.Version + 1
		if _, err = q.WithContext(ctx).Where(q.ID.Eq(a.ID)).Updates(values); err != nil {
			return out, err
		}
	}
	now := time.Now().UTC()
	if err = m.agentCommandStore(tx).Where(map[string]any{"id": id, "tenant_id": tenant}).Updates(map[string]any{"state": state, "reason_code": reason, "sealed_credential": "", "completed_at": now}).Error; err != nil {
		return out, err
	}
	if err = m.machineEventOn(ctx, tx, tenant, machine, "machine.agent_command_completed", 0, map[string]any{"command_id": id, "agent_principal_id": row.AgentPrincipalID, "kind": row.Kind, "state": state, "reason_code": reason}); err != nil {
		return out, err
	}
	row.State, row.ReasonCode = state, reason
	return machineCommandView(&row), nil
}

// Expiry erases abandoned encrypted deliveries and records failure; it never
// substitutes for a successful old-machine cleanup acknowledgement.
func (m *Module) expireAgentMachineCommands(ctx context.Context, db *gorm.DB, tenant guid.ID, now time.Time) error {
	if tenant <= 0 || now.IsZero() {
		return ErrInvalidArgument
	}
	q := query.BindAgentMachineCommands(db, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.In("QUEUED", "RUNNING"), q.ExpiresAt.Lte(now)).Order(q.ID).Limit(200).Find()
	if err != nil {
		return err
	}
	for _, row := range rows {
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if m.principals.Lock == nil {
				return ErrUnavailable
			}
			if e := m.principals.Lock(ctx, tx, row.AgentPrincipalID); e != nil {
				return e
			}
			commands := query.BindAgentMachineCommands(tx, m.names)
			current, e := commands.WithContext(ctx).Where(commands.ID.Eq(row.ID), commands.TenantID.Eq(tenant)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
			if e != nil {
				return e
			}
			if !slices.Contains([]string{"QUEUED", "RUNNING"}, current.State) || current.ExpiresAt.After(now) {
				return nil
			}
			agents := query.BindAgents(tx, m.names)
			a, e := agents.WithContext(ctx).Where(agents.TenantID.Eq(tenant), agents.PrincipalID.Eq(current.AgentPrincipalID)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
			if e != nil {
				return e
			}
			if current.Kind == "CREDENTIAL" && a.CredentialID != nil && current.CredentialID != nil && *a.CredentialID == *current.CredentialID && a.ConfigRevision == current.ConfigRevision {
				if _, e = agents.WithContext(ctx).Where(agents.ID.Eq(a.ID)).Updates(map[string]any{"credential_delivery": "FAILED", "version": a.Version + 1, "updated_at": now}); e != nil {
					return e
				}
			}
			if _, e = commands.WithContext(ctx).Where(commands.ID.Eq(current.ID), commands.TenantID.Eq(tenant)).Updates(map[string]any{"state": "FAILED", "sealed_credential": "", "reason_code": "COMMAND_EXPIRED", "completed_at": now}); e != nil {
				return e
			}
			return m.machineEventOn(ctx, tx, tenant, current.MachineID, "machine.agent_command_completed", 0, map[string]any{"command_id": current.ID, "agent_principal_id": current.AgentPrincipalID, "kind": current.Kind, "state": "FAILED", "reason_code": "COMMAND_EXPIRED"})
		})
		if err != nil {
			return err
		}
	}
	return nil
}
