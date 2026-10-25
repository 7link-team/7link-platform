package fleet

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type discoveryReportInput struct {
	MachineInput
	Body struct {
		InspectionSession
		Kind      string             `json:"kind" enum:"MODELS,CAPABILITIES"`
		Discovery []RuntimeDiscovery `json:"discovery" maxItems:"2"`
	}
}

func (m *Module) registerDiscoveryHTTP(api huma.API) error {
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.discovery.report", Method: http.MethodPut, Path: "/machines/{machine_id}/discovery", Summary: "Refresh local discovery at session opening and every five minutes", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, m.discoveryHTTP)
	return authz.RegisterRequestContract(api, "/machines/{machine_id}/discovery")
}
func (m *Module) discoveryHTTP(ctx context.Context, in *discoveryReportInput) (*respx.Body[InspectionView], error) {
	machine, e := guid.RequireID(in.MachineID)
	if e != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	ctx, e = m.BindRunnerContext(ctx, machine)
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	db, e := m.writer()
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	var out InspectionView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.RecordDiscoveryOn(ctx, tx, authn.TenantIDFromContext(ctx), machine, in.Body.SessionID, in.Body.ConnEpoch, in.Body.Kind, in.Body.Discovery)
		return err
	})
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) RecordDiscoveryOn(ctx context.Context, tx *gorm.DB, tenant, machine, session guid.ID, epoch int64, kind string, facts []RuntimeDiscovery) (InspectionView, error) {
	if kind != "MODELS" && kind != "CAPABILITIES" {
		return InspectionView{}, ErrInvalidArgument
	}
	if e := m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); e != nil {
		return InspectionView{}, e
	}
	if m.ids == nil {
		return InspectionView{}, ErrUnavailable
	}
	id, e := m.ids.Next()
	if e != nil {
		return InspectionView{}, e
	}
	if id <= 0 {
		return InspectionView{}, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	r := &model.Inspection{ID: guid.ID(id), TenantID: tenant, MachineID: machine, Kind: kind, ScopeKey: "home", State: "READY", SessionID: session, ConnEpoch: epoch, ExpiresAt: now, CreatedAt: now, UpdatedAt: now}
	merged, e := m.discoveryOn(ctx, tx, r, facts)
	if e != nil {
		return InspectionView{}, e
	}
	b, _ := json.Marshal(merged)
	r.ResultJSON = string(b)
	r.ResultHash = enrollmentHash(string(b))
	if e = query.BindInspections(tx, m.names).WithContext(ctx).Create(r); e != nil {
		return InspectionView{}, e
	}
	// Periodic reports keep one latest snapshot. Explicit inspection jobs retain
	// their receipts; neither prompts nor ever-growing periodic snapshots persist.
	q := query.BindInspections(tx, m.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.Kind.Eq(kind), q.ScopeKey.Eq("home"), q.RequestedBy.Eq(guid.ID(0)), q.State.Eq("READY"), q.ID.Lt(r.ID)).Delete(); e != nil {
		return InspectionView{}, e
	}
	if e = m.inspectionEventOn(ctx, tx, r); e != nil {
		return InspectionView{}, e
	}
	return inspectionView(r), nil
}

// The authenticated pull serializes first-session capability scans across every
// registered workdir. No arbitrary client path is accepted, and an old session
// cannot create a job. Manual refresh still uses current Human machine_read.
func (m *Module) nextWorkdirInspectionOn(ctx context.Context, tx *gorm.DB, tenant, machine, session guid.ID, epoch int64) (*model.Inspection, error) {
	w := query.BindWorkdirs(tx, m.names)
	workdirs, e := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.MachineID.Eq(machine), w.ArchivedAt.IsNull()).Order(w.ID).Find()
	if e != nil {
		return nil, e
	}
	q := query.BindInspections(tx, m.names)
	for _, workdir := range workdirs {
		n, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.Kind.Eq("CAPABILITIES"), q.ScopeKey.Eq(workdir.ID.String()), q.WorkdirPath.Eq(workdir.Path), q.SessionID.Eq(session)).Count()
		if e != nil {
			return nil, e
		}
		if n > 0 {
			continue
		}
		if m.ids == nil {
			return nil, ErrUnavailable
		}
		id, e := m.ids.Next()
		if e != nil {
			return nil, e
		}
		if id <= 0 {
			return nil, ErrUnavailable
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		active := 1
		r := &model.Inspection{ID: guid.ID(id), TenantID: tenant, MachineID: machine, Kind: "CAPABILITIES", ScopeKey: workdir.ID.String(), WorkdirPath: workdir.Path, State: "QUEUED", ActiveKey: &active, SessionID: session, ConnEpoch: epoch, ExpiresAt: now.Add(65 * time.Second), CreatedAt: now, UpdatedAt: now}
		if e = q.WithContext(ctx).Create(r); e != nil {
			return nil, e
		}
		return r, nil
	}
	return nil, nil
}

type DiscoveredSkill struct {
	Name   string `json:"name" maxLength:"200"`
	SHA256 string `json:"sha256" maxLength:"64"`
}
type RuntimeDiscovery struct {
	Families      map[string]string `json:"families,omitempty"`
	Reasoning     []ModelReasoning  `json:"reasoning,omitempty" maxItems:"1000"`
	Runtime       string            `json:"runtime" enum:"CODEX,CLAUDE_CODE"`
	State         string            `json:"state" enum:"READY,ERROR,UNSUPPORTED"`
	Reason        string            `json:"reason,omitempty" maxLength:"64"`
	Source        string            `json:"source" enum:"LOCAL_CONFIG,CLI_CACHE,CACHED"`
	Models        []string          `json:"models" maxItems:"1000"`
	Skills        []DiscoveredSkill `json:"skills" maxItems:"2000"`
	MCP           []string          `json:"mcp" maxItems:"2000"`
	CheckedAt     *time.Time        `json:"checked_at,omitempty"`
	LastSuccessAt *time.Time        `json:"last_success_at,omitempty"`
}

// ModelReasoning is a model-scoped CLI catalog observation, never a runtime default.
type ModelReasoning struct {
	Model   string   `json:"model" maxLength:"160"`
	Efforts []string `json:"efforts" maxItems:"16"`
}

func discoveryID(id string, limit int) bool {
	return boundedFact(id, limit) && strings.IndexFunc(id, unicode.IsSpace) < 0
}
func validateDiscovery(kind string, facts []RuntimeDiscovery) error {
	if len(facts) != 2 {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, d := range facts {
		if !slices.Contains([]string{"CODEX", "CLAUDE_CODE"}, d.Runtime) || seen[d.Runtime] || !slices.Contains([]string{"READY", "ERROR", "UNSUPPORTED"}, d.State) || !slices.Contains([]string{"LOCAL_CONFIG", "CLI_CACHE"}, d.Source) || d.CheckedAt != nil || d.LastSuccessAt != nil {
			return ErrInvalidArgument
		}
		seen[d.Runtime] = true
		if len(d.Families) > 1000 || kind != "MODELS" && len(d.Families) > 0 || d.State != "READY" && len(d.Families) > 0 {
			return ErrInvalidArgument
		}
		for id, family := range d.Families {
			if !slices.Contains(d.Models, id) || !slices.Contains([]string{"openai", "anthropic", "google"}, family) {
				return ErrInvalidArgument
			}
		}
		if len(d.Reasoning) > 1000 || kind != "MODELS" && len(d.Reasoning) > 0 || d.State != "READY" && len(d.Reasoning) > 0 {
			return ErrInvalidArgument
		}
		reasoningModels := map[string]bool{}
		for _, model := range d.Reasoning {
			if !slices.Contains(d.Models, model.Model) || reasoningModels[model.Model] || len(model.Efforts) > 16 {
				return ErrInvalidArgument
			}
			reasoningModels[model.Model] = true
			efforts := map[string]bool{}
			for _, effort := range model.Efforts {
				if !discoveryID(effort, 16) || efforts[effort] {
					return ErrInvalidArgument
				}
				efforts[effort] = true
			}
		}
		if d.State == "READY" && d.Reason != "" || d.State != "READY" && !slices.Contains([]string{"NO_LOCAL_MODEL_LIST_OR_CLI_LIST_COMMAND", "LOCAL_CONFIG_UNREADABLE", "INVALID_MODEL_LIST", "MODEL_LIST_TOO_LARGE", "LOCAL_SKILL_UNREADABLE", "CAPABILITY_LIST_TOO_LARGE"}, d.Reason) {
			return ErrInvalidArgument
		}
		if len(d.Models) > 1000 || len(d.Skills) > 2000 || len(d.MCP) > 2000 || kind == "MODELS" && len(d.Skills)+len(d.MCP) > 0 || kind == "CAPABILITIES" && len(d.Models) > 0 {
			return ErrInvalidArgument
		}
		if d.State != "READY" && kind == "MODELS" && len(d.Models) > 0 {
			return ErrInvalidArgument
		}
		ids := map[string]bool{}
		for _, id := range d.Models {
			if !discoveryID(id, 160) || ids[id] {
				return ErrInvalidArgument
			}
			ids[id] = true
		}
		for _, id := range d.MCP {
			if !discoveryID(id, 200) {
				return ErrInvalidArgument
			}
		}
		for _, s := range d.Skills {
			b, e := hex.DecodeString(s.SHA256)
			if !discoveryID(s.Name, 200) || e != nil || len(b) != 32 {
				return ErrInvalidArgument
			}
		}
	}
	b, _ := json.Marshal(facts)
	if len(b) > 900*1024 || kind == "MODELS" && len(b) > 512*1024 {
		return ErrInvalidArgument
	}
	return nil
}

// Keep the last successful catalog on a per-runtime failure. The latest receipt
// carries both the failure fact and its checked/last-success times; an unsupported
// runtime does not fail the other runtime or erase prior observations.
func (m *Module) discoveryOn(ctx context.Context, tx *gorm.DB, row *model.Inspection, facts []RuntimeDiscovery) ([]RuntimeDiscovery, error) {
	if err := validateDiscovery(row.Kind, facts); err != nil {
		return nil, err
	}
	q := query.BindInspections(tx, m.names)
	prior, e := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.MachineID.Eq(row.MachineID), q.Kind.Eq(row.Kind), q.ScopeKey.Eq(row.ScopeKey), q.ID.Lt(row.ID), q.State.Eq("READY")).Order(q.ID.Desc()).Limit(1).Find()
	if e != nil {
		return nil, e
	}
	old := []RuntimeDiscovery{}
	if len(prior) > 0 && prior[0].ResultJSON != "" {
		if json.Unmarshal([]byte(prior[0].ResultJSON), &old) != nil {
			return nil, ErrUnavailable
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	facts = slices.Clone(facts)
	for i := range facts {
		d := &facts[i]
		d.Models = slices.Clone(d.Models)
		d.CheckedAt = &now
		if d.State == "READY" {
			d.LastSuccessAt = &now
			slices.SortFunc(d.Models, modelVersionOrder)
		} else {
			for _, p := range old {
				if p.Runtime == d.Runtime && p.LastSuccessAt != nil {
					d.Source = "CACHED"
					d.LastSuccessAt = p.LastSuccessAt
					d.Models = p.Models
					d.Reasoning = p.Reasoning
					d.Families = p.Families
					if row.Kind == "CAPABILITIES" {
						d.Skills = p.Skills
						d.MCP = p.MCP
					}
				}
			}
		}
		if row.Kind == "MODELS" && d.State == "READY" {
			r := query.BindRuntimes(tx, m.names)
			b, _ := json.Marshal(d.Models)
			if _, e = r.WithContext(ctx).Where(r.TenantID.Eq(row.TenantID), r.MachineID.Eq(row.MachineID), r.Kind.Eq(d.Runtime)).Updates(map[string]any{"models_json": string(b)}); e != nil {
				return nil, e
			}
		}
	}
	return facts, nil
}

// Compare numeric runs as versions, so 5.10 sorts before 5.9 without interpreting
// provider-specific model names as semantic versions.
func modelVersionOrder(a, b string) int {
	for len(a) > 0 && len(b) > 0 {
		if a[0] >= '0' && a[0] <= '9' && b[0] >= '0' && b[0] <= '9' {
			i, j := 0, 0
			for i < len(a) && a[i] >= '0' && a[i] <= '9' {
				i++
			}
			for j < len(b) && b[j] >= '0' && b[j] <= '9' {
				j++
			}
			x, y := strings.TrimLeft(a[:i], "0"), strings.TrimLeft(b[:j], "0")
			if len(x) != len(y) {
				if len(x) > len(y) {
					return -1
				}
				return 1
			}
			if c := strings.Compare(x, y); c != 0 {
				return -c
			}
			a, b = a[i:], b[j:]
			continue
		}
		if a[0] != b[0] {
			return -strings.Compare(a, b)
		}
		a, b = a[1:], b[1:]
	}
	return -strings.Compare(a, b)
}
