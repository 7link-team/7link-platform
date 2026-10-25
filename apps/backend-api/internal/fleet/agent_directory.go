package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AgentDirectoryInput struct {
	MachineID string `query:"machine_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Cursor    string `query:"cursor" maxLength:"512"`
	Limit     int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Search    string `query:"search" maxLength:"200"`
}

// AgentDirectoryView deliberately omits prompts, tools, secrets and host paths.
type AgentDirectoryView struct {
	ID            guid.ID `json:"id"`
	PrincipalID   guid.ID `json:"principal_id"`
	Name          string  `json:"name"`
	Handle        string  `json:"handle"`
	State         string  `json:"state"`
	ProcessStatus string  `json:"process_status"`
	Runtime       string  `json:"runtime"`
	Model         string  `json:"model"`
	OwnerID       guid.ID `json:"owner_id"`
	Version       int     `json:"version"`
}

type AgentDirectoryPage struct {
	Items      []AgentDirectoryView `json:"items"`
	HasMore    bool                 `json:"has_more"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

func (m *Module) registerDirectoryHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "agent.directory.list", Method: "GET", Path: "/agents", Summary: "Browse the current internal Agent directory without private configuration", Tags: []string{"agents"}, Errors: []int{401, 403, 422, 500, 503}}, authz.Guard{Resource: "agent", Verb: "read", FilterData: true}, m.directoryHTTP)
}

func (m *Module) directoryHTTP(ctx context.Context, in *AgentDirectoryInput) (*respx.Body[AgentDirectoryPage], error) {
	if in.Limit < 1 || in.Limit > 100 || !utf8.ValidString(in.Search) || utf8.RuneCountInString(in.Search) > 200 {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	machine := guid.ID(0)
	if in.MachineID != "" {
		var e error
		machine, e = guid.RequireID(in.MachineID)
		if e != nil {
			return nil, fleetHTTPError(ctx, ErrInvalidArgument)
		}
	}
	search := strings.TrimSpace(in.Search)
	scope := fmt.Sprintf("%s:%s:%x:", tenant, actor, sha256.Sum256([]byte(search+":"+in.MachineID)))
	before := guid.ID(0)
	if in.Cursor != "" {
		decoded, e := base64.RawURLEncoding.DecodeString(in.Cursor)
		if e != nil || !strings.HasPrefix(string(decoded), scope) {
			return nil, fleetHTTPError(ctx, ErrInvalidArgument)
		}
		before, e = guid.RequireID(strings.TrimPrefix(string(decoded), scope))
		if e != nil {
			return nil, fleetHTTPError(ctx, ErrInvalidArgument)
		}
	}
	db, e := m.writer()
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	out := AgentDirectoryPage{Items: []AgentDirectoryView{}}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.controlAuthorityOn(ctx, tx, tenant, "agent_read"); e != nil {
			return e
		}
		q := query.BindAgents(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(before))
		if machine > 0 {
			r = r.Where(q.MachineID.Eq(machine))
		}
		if search != "" {
			r = r.Where(q.Name.Like("%" + search + "%"))
		}
		rows, e := r.Order(q.ID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
			out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(scope + rows[len(rows)-1].ID.String()))
		}
		for _, row := range rows {
			out.Items = append(out.Items, AgentDirectoryView{ID: row.ID, PrincipalID: row.PrincipalID, Name: row.Name, Handle: row.Handle, State: row.State, ProcessStatus: row.ProcessStatus, Runtime: row.RuntimeKind, Model: row.ModelID, OwnerID: row.OwnerPrincipalID, Version: row.Version})
		}
		return nil
	})
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

// DispatchFleetFacts returns bounded, credential-free observed facts. All rows
// are locked in deterministic order so application can recheck capacity safely.
type DispatchFleetFacts struct {
	PrincipalsOn func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error)
	ListOn       func(context.Context, *gorm.DB, guid.ID) ([]DispatchAgentFact, error)
}
type DispatchAgentFact struct {
	Version               int                     `json:"agent_version"`
	ConfigRevision        int                     `json:"config_revision"`
	MachineID             *guid.ID                `json:"machine_id"`
	MachineVersion        *int                    `json:"machine_version"`
	MachineEpoch          *int64                  `json:"machine_epoch"`
	ModelID               string                  `json:"model_id"`
	RuntimeInspectedAt    *time.Time              `json:"runtime_inspected_at"`
	ObservedSkills        *DispatchObservedSkills `json:"observed_skills"`
	ID                    guid.ID                 `json:"agent_id"`
	PrincipalID           guid.ID                 `json:"principal_id"`
	Name                  string                  `json:"name"`
	State                 string                  `json:"state"`
	ProcessStatus         string                  `json:"process_status"`
	Roles                 []string                `json:"roles"`
	Runtime               string                  `json:"runtime"`
	ModelListed           bool                    `json:"model_listed"`
	Tags                  []string                `json:"tags"`
	MaxRisk               string                  `json:"max_risk"`
	MaxParallel           int                     `json:"max_parallel"`
	MachineOnline         bool                    `json:"machine_online"`
	MachineDraining       bool                    `json:"machine_draining"`
	FreeSlots             int                     `json:"free_slots"`
	SpawnBudgetUnverified bool                    `json:"spawn_budget_unverified"`
}

// DispatchObservedSkills is local runtime discovery, not the approved Knowledge
// skill catalog. Names/hashes preserve provenance without exposing host paths.
type DispatchObservedSkills struct {
	InspectionID  guid.ID           `json:"inspection_id"`
	ResultHash    string            `json:"result_hash"`
	State         string            `json:"state"`
	Source        string            `json:"source"`
	CheckedAt     *time.Time        `json:"checked_at"`
	LastSuccessAt *time.Time        `json:"last_success_at"`
	Skills        []DiscoveredSkill `json:"skills"`
}

func (m *Module) provideDispatch(s *module.Services) error {
	return module.Provide[DispatchFleetFacts](s, DispatchFleetFacts{PrincipalsOn: m.dispatchPrincipalsOn, ListOn: m.dispatchFactsOn})
}

func (m *Module) dispatchPrincipalsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) ([]guid.ID, error) {
	if tx == nil || tenant <= 0 {
		return nil, ErrInvalidArgument
	}
	a := query.BindAgents(tx, m.names)
	rows, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.State.Neq("RETIRED")).Order(a.PrincipalID).Limit(101).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 100 {
		return nil, ErrUnavailable
	}
	ids := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.PrincipalID)
		if row.MachineID != nil {
			machine, e := m.machineOn(ctx, tx, tenant, *row.MachineID, false)
			if e != nil {
				return nil, e
			}
			ids = append(ids, machine.PrincipalID)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}
func (m *Module) dispatchFactsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) ([]DispatchAgentFact, error) {
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	a := query.BindAgents(tx, m.names)
	rows, e := a.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(a.TenantID.Eq(tenant), a.State.Neq("RETIRED")).Order(a.ID).Limit(101).Find()
	if e != nil {
		return nil, e
	}
	if len(rows) > 100 {
		return nil, ErrUnavailable
	}
	out := make([]DispatchAgentFact, 0, len(rows))
	for _, row := range rows {
		identity, found, e := m.ByPrincipalOn(ctx, tx, tenant, row.PrincipalID)
		if e != nil {
			return nil, e
		}
		if !found {
			return nil, ErrUnavailable
		}
		f := DispatchAgentFact{ID: row.ID, PrincipalID: row.PrincipalID, Name: row.Name, State: row.State, ProcessStatus: row.ProcessStatus, Roles: identity.Roles, Runtime: row.RuntimeKind, MaxRisk: row.MaxRisk, MaxParallel: row.MaxParallelItems, Tags: []string{}, SpawnBudgetUnverified: row.Origin == "AGENT_SPAWNED" && row.AdoptedAt == nil}
		f.Version, f.ConfigRevision, f.MachineID, f.ModelID = row.Version, row.ConfigRevision, row.MachineID, row.ModelID
		if e = json.Unmarshal([]byte(row.CapabilityTags), &f.Tags); e != nil {
			return nil, e
		}
		if row.MachineID != nil {
			mq := query.BindMachines(tx, m.names)
			machine, e := mq.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(mq.TenantID.Eq(tenant), mq.ID.Eq(*row.MachineID)).First()
			if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
				return nil, e
			}
			if machine != nil {
				f.MachineVersion, f.MachineEpoch = &machine.Version, &machine.ConnEpoch
				f.MachineOnline = machine.State == "ONLINE"
				f.MachineDraining = machine.DrainRequested
				sq := query.BindSlots(tx, m.names)
				used, e := sq.WithContext(ctx).Where(sq.TenantID.Eq(tenant), sq.MachineID.Eq(machine.ID), sq.ReleasedAt.IsNull()).Count()
				if e != nil {
					return nil, e
				}
				f.FreeSlots = max(0, machine.SlotCount-int(used))
				rq := query.BindRuntimes(tx, m.names)
				runtime, e := rq.WithContext(ctx).Where(rq.TenantID.Eq(tenant), rq.MachineID.Eq(machine.ID), rq.Kind.Eq(row.RuntimeKind)).First()
				if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
					return nil, e
				}
				if runtime != nil {
					f.RuntimeInspectedAt = &runtime.InspectedAt
					var models []string
					if e = json.Unmarshal([]byte(runtime.ModelsJSON), &models); e != nil {
						return nil, e
					}
					catalog, _, err := m.modelCatalogOn(ctx, tx, tenant, machine.ID, row.RuntimeKind)
					if err != nil {
						return nil, err
					}
					f.ModelListed = slices.Contains(catalog.ObservedIDs, row.ModelID) || slices.Contains(catalog.ManualIDs, row.ModelID)
				}
				if machine.CurrentSessionID != nil {
					iq := query.BindInspections(tx, m.names)
					reports, err := iq.WithContext(ctx).Where(iq.TenantID.Eq(tenant), iq.MachineID.Eq(machine.ID), iq.SessionID.Eq(*machine.CurrentSessionID), iq.ConnEpoch.Eq(machine.ConnEpoch), iq.Kind.Eq("CAPABILITIES"), iq.ScopeKey.Eq("home"), iq.State.Eq("READY")).Order(iq.ID.Desc()).Limit(1).Find()
					if err != nil {
						return nil, err
					}
					if len(reports) == 1 {
						var discoveries []RuntimeDiscovery
						if err = json.Unmarshal([]byte(reports[0].ResultJSON), &discoveries); err != nil {
							return nil, err
						}
						for _, discovery := range discoveries {
							if discovery.Runtime == row.RuntimeKind {
								f.ObservedSkills = &DispatchObservedSkills{InspectionID: reports[0].ID, ResultHash: reports[0].ResultHash, State: discovery.State, Source: discovery.Source, CheckedAt: discovery.CheckedAt, LastSuccessAt: discovery.LastSuccessAt, Skills: slices.Clone(discovery.Skills)}
								break
							}
						}
					}
				}
			}
		}
		out = append(out, f)
	}
	return out, nil
}
