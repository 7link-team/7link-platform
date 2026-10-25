package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"math/big"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type RuntimeObservation struct {
	Kind           string   `json:"kind" enum:"CODEX,CLAUDE_CODE"`
	Version        string   `json:"version" minLength:"1" maxLength:"64"`
	ExecutablePath string   `json:"executable_path" minLength:"1" maxLength:"1024"`
	Models         []string `json:"models" maxItems:"200"`
}
type ToolObservation struct {
	Name           string `json:"name" enum:"node,python,ffmpeg,ffprobe,whisperx,pyscenedetect,browser"`
	Version        string `json:"version" minLength:"1" maxLength:"64"`
	ExecutablePath string `json:"executable_path" minLength:"1" maxLength:"1024"`
}
type Inventory struct {
	Runtimes []RuntimeObservation `json:"runtimes" maxItems:"2"`
	Tools    []ToolObservation    `json:"tools" maxItems:"32"`
}

func boundedFact(value string, limit int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit && strings.IndexFunc(value, unicode.IsControl) < 0
}
func validateInventory(in Inventory) error {
	if len(in.Runtimes) > 2 || len(in.Tools) > 32 {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, r := range in.Runtimes {
		if !slices.Contains([]string{"CODEX", "CLAUDE_CODE"}, r.Kind) || seen[r.Kind] || !boundedFact(r.Version, 64) || !boundedFact(r.ExecutablePath, 1024) || len(r.Models) > 200 {
			return ErrInvalidArgument
		}
		seen[r.Kind] = true
		models := map[string]bool{}
		for _, model := range r.Models {
			if !boundedFact(model, 160) || models[model] {
				return ErrInvalidArgument
			}
			models[model] = true
		}
	}
	for _, tool := range in.Tools {
		if !slices.Contains([]string{"node", "python", "ffmpeg", "ffprobe", "whisperx", "pyscenedetect", "browser"}, tool.Name) || seen[tool.Name] || !boundedFact(tool.Version, 64) || !boundedFact(tool.ExecutablePath, 1024) {
			return ErrInvalidArgument
		}
		seen[tool.Name] = true
	}
	return nil
}

// recordRuntimeInventoryOn preserves model catalogs when a runner only observed
// executable versions. An explicit full inventory remains authoritative instead.
func (m *Module) recordRuntimeInventoryOn(ctx context.Context, tx *gorm.DB, tenant, machine, session guid.ID, epoch int64, in Inventory) error {
	if err := validateInventory(in); err != nil {
		return err
	}
	if err := m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); err != nil {
		return err
	}
	r := query.BindRuntimes(tx, m.names)
	previous, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.MachineID.Eq(machine)).Find()
	if err != nil {
		return err
	}
	in.Runtimes = slices.Clone(in.Runtimes)
	for i := range in.Runtimes {
		in.Runtimes[i].Models = []string{}
		for _, old := range previous {
			if old.Kind == in.Runtimes[i].Kind {
				if json.Unmarshal([]byte(old.ModelsJSON), &in.Runtimes[i].Models) != nil {
					return ErrUnavailable
				}
			}
		}
	}
	return m.RecordInventoryOn(ctx, tx, tenant, machine, session, epoch, in)
}

// RecordInventoryOn replaces only a successful full inventory report. Callers
// must not send partial detection failures as an empty successful inventory.
// Models are observed IDs, never proof that a provider will accept a request.
// The same authenticated session fence as heartbeat protects all inventory writes.
func (m *Module) RecordInventoryOn(ctx context.Context, tx *gorm.DB, tenant, machine, session guid.ID, epoch int64, in Inventory) error {
	if err := validateInventory(in); err != nil {
		return err
	}
	if err := m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); err != nil {
		return err
	}
	if m.ids == nil {
		return ErrUnavailable
	}
	runtimes := query.BindRuntimes(tx, m.names)
	tools := query.BindTools(tx, m.names)
	if _, err := runtimes.WithContext(ctx).Where(runtimes.TenantID.Eq(tenant), runtimes.MachineID.Eq(machine)).Delete(); err != nil {
		return err
	}
	if _, err := tools.WithContext(ctx).Where(tools.TenantID.Eq(tenant), tools.MachineID.Eq(machine)).Delete(); err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, r := range in.Runtimes {
		id, err := m.ids.Next()
		if err != nil {
			return err
		}
		if id <= 0 {
			return ErrUnavailable
		}
		models := r.Models
		if models == nil {
			models = []string{}
		}
		encoded, err := json.Marshal(models)
		if err != nil {
			return err
		}
		if err = runtimes.WithContext(ctx).Create(&model.MachineRuntime{ID: guid.ID(id), TenantID: tenant, MachineID: machine, Kind: r.Kind, Version: r.Version, ExecutablePath: r.ExecutablePath, ModelsJSON: string(encoded), InspectedAt: now}); err != nil {
			return err
		}
	}
	for _, tool := range in.Tools {
		id, err := m.ids.Next()
		if err != nil {
			return err
		}
		if id <= 0 {
			return ErrUnavailable
		}
		if err = tools.WithContext(ctx).Create(&model.MachineTool{ID: guid.ID(id), TenantID: tenant, MachineID: machine, Name: tool.Name, Version: tool.Version, ExecutablePath: tool.ExecutablePath, InspectedAt: now}); err != nil {
			return err
		}
	}
	return m.machineEventOn(ctx, tx, tenant, machine, "machine.inventory_updated", 0, map[string]any{"runtime_count": len(in.Runtimes), "tool_count": len(in.Tools), "conn_epoch": epoch})
}

// EstimateTokenCost computes ceil((input*inputPrice + output*outputPrice)/1e6)
// in USD micros. The boolean distinguishes an explicit zero price from an
// unknown price; missing prices must not be reported as free model usage.
func EstimateTokenCost(input, output int64, price *TokenPrice) (int64, bool, error) {
	if input < 0 || output < 0 {
		return 0, false, ErrInvalidArgument
	}
	if price == nil {
		return 0, false, nil
	}
	if price.InputMicros < 0 || price.OutputMicros < 0 {
		return 0, false, ErrInvalidArgument
	}
	value := new(big.Int).Mul(big.NewInt(input), big.NewInt(price.InputMicros))
	value.Add(value, new(big.Int).Mul(big.NewInt(output), big.NewInt(price.OutputMicros)))
	value.Add(value, big.NewInt(999999)).Quo(value, big.NewInt(1000000))
	if !value.IsInt64() {
		return 0, false, ErrInvalidArgument
	}
	return value.Int64(), true, nil
}

type TokenPrice struct{ InputMicros, OutputMicros int64 }

func (m *Module) ModelPriceOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, runtime, model string) (*TokenPrice, error) {
	if m == nil || m.names.Prices.IsZero() {
		return nil, ErrUnavailable
	}
	if err := fleetTransaction(tx, tenant, tenant); err != nil {
		return nil, err
	}
	if !slices.Contains([]string{"CODEX", "CLAUDE_CODE"}, runtime) || !boundedFact(model, 160) {
		return nil, ErrInvalidArgument
	}
	q := query.BindPrices(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Runtime.Eq(runtime), q.Model.Eq(model), q.DeletedAt.IsNull()).Limit(1).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &TokenPrice{InputMicros: rows[0].InputMicros, OutputMicros: rows[0].OutputMicros}, nil
}
