package templates

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ProviderGenerationRequest struct {
	ProjectID   guid.ID
	ExecutionID guid.ID
	Kind        string
	Input       json.RawMessage
}
type ProviderGenerationView struct {
	ID              guid.ID      `json:"id"`
	ProjectID       guid.ID      `json:"project_id"`
	ExecutionID     guid.ID      `json:"execution_id"`
	ProviderID      guid.ID      `json:"provider_id"`
	ProviderVersion int          `json:"provider_version"`
	AdapterKey      string       `json:"adapter_key"`
	Kind            string       `json:"kind"`
	State           string       `json:"state"`
	Availability    string       `json:"availability"`
	JobRef          string       `json:"job_ref"`
	RequestSHA256   string       `json:"request_sha256"`
	OutputSHA256    string       `json:"output_sha256"`
	Output          ProviderPoll `json:"output"`
	CreatedAt       time.Time    `json:"created_at"`
	CompletedAt     *time.Time   `json:"completed_at,omitempty"`
}
type ProviderGateway struct {
	RequestOn func(context.Context, *gorm.DB, ProviderGenerationRequest) (ProviderGenerationView, error)
	ReadOn    func(context.Context, *gorm.DB, guid.ID) (ProviderGenerationView, error)
	CancelOn  func(context.Context, *gorm.DB, guid.ID) (ProviderGenerationView, error)
}
type providerConfigPin struct {
	Config        json.RawMessage `json:"config"`
	LicenseSHA256 string          `json:"license_sha256"`
}

func (m *Module) generationView(row *model.ProviderGeneration) (ProviderGenerationView, error) {
	out := ProviderGenerationView{ID: row.ID, ProjectID: row.ProjectID, ExecutionID: row.ExecutionID, ProviderID: row.ProviderID, ProviderVersion: row.ProviderVersion, AdapterKey: row.AdapterKey, Kind: row.Kind, State: row.State, Availability: "AVAILABLE", JobRef: row.JobRef, RequestSHA256: row.RequestSHA256, OutputSHA256: row.OutputSHA256, CreatedAt: row.CreatedAt, CompletedAt: row.CompletedAt}
	if _, exists := m.providerAdapters[row.AdapterKey]; !exists || m.providerExecution == nil {
		out.Availability = "UNAVAILABLE"
	}
	if err := json.Unmarshal([]byte(row.OutputSnapshot), &out.Output); err != nil {
		return ProviderGenerationView{}, err
	}
	return out, nil
}
func (m *Module) requestProviderGenerationOn(ctx context.Context, tx *gorm.DB, in ProviderGenerationRequest) (ProviderGenerationView, error) {
	if err := providerTransaction(tx); err != nil {
		return ProviderGenerationView{}, err
	}
	tenant := authn.TenantIDFromContext(ctx)
	if tenant <= 0 || in.ProjectID <= 0 || in.ExecutionID <= 0 || !slices.Contains(providerKinds, in.Kind) || len(in.Input) == 0 || len(in.Input) > 32000 || !json.Valid(in.Input) {
		return ProviderGenerationView{}, ErrInvalid
	}
	if m.providerExecution == nil || m.ids == nil {
		return ProviderGenerationView{}, ErrUnavailable
	}
	if err := m.providerExecution(ctx, tx, tenant, in.ProjectID, in.ExecutionID, "GENERATE"); err != nil {
		return ProviderGenerationView{}, err
	}
	b := query.BindBindings(tx, m.names)
	binding, err := b.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(b.TenantID.Eq(tenant), b.ProjectID.Eq(in.ProjectID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ProviderGenerationView{}, ErrNotFound
	}
	if err != nil {
		return ProviderGenerationView{}, err
	}
	q := query.BindProviderGenerations(tx, m.names)
	old, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(in.ExecutionID), q.Kind.Eq(in.Kind)).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return ProviderGenerationView{}, err
	}
	canonical, err := providerRequestBytes(in.Input)
	if err != nil {
		return ProviderGenerationView{}, err
	}
	hash := sha256.Sum256(canonical)
	requestHash := hex.EncodeToString(hash[:])
	if old != nil {
		if old.ProjectID != in.ProjectID || old.RequestSHA256 != requestHash {
			return ProviderGenerationView{}, ErrConflict
		}
		return m.generationView(old)
	}
	links := query.BindProviderBindings(tx, m.names)
	link, err := links.WithContext(ctx).Where(links.TenantID.Eq(tenant), links.BindingID.Eq(binding.ID), links.Kind.Eq(in.Kind)).Order(links.ID.Desc()).First()
	if err != nil {
		return ProviderGenerationView{}, ErrProviderBinding
	}
	p := query.BindProviders(tx, m.names)
	provider, err := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ID.Eq(link.ProviderID)).First()
	if err != nil {
		return ProviderGenerationView{}, err
	}
	if m.providerView(provider).State != "ENABLED" || provider.HealthCheckedAt == nil {
		return ProviderGenerationView{}, ErrProviderBinding
	}
	if _, exists := m.providerAdapters[provider.AdapterKey]; !exists {
		return ProviderGenerationView{}, ErrProviderAdapter
	}
	// A binding pins the chosen provider identity, while each requested execution
	// freezes the actual configuration version it will use.
	pin, err := json.Marshal(providerConfigPin{json.RawMessage(provider.Config), provider.LicenseSHA256})
	if err != nil {
		return ProviderGenerationView{}, err
	}
	id, err := m.ids.Next()
	if err != nil {
		return ProviderGenerationView{}, err
	}
	row := &model.ProviderGeneration{TenantID: tenant, ID: guid.ID(id), ProjectID: in.ProjectID, BindingID: binding.ID, ProviderID: provider.ID, ProviderVersion: provider.Version, ExecutionID: in.ExecutionID, Kind: in.Kind, AdapterKey: provider.AdapterKey, ConfigSnapshot: string(pin), SecretSnapshot: provider.SealedSecret, MachineID: provider.MachineID, Request: string(canonical), RequestSHA256: requestHash, State: "PENDING", OutputSnapshot: `{"state":"PENDING","outputs":[]}`, CreatedAt: time.Now().UTC(), Version: 1}
	if err = q.WithContext(ctx).Create(row); err != nil {
		return ProviderGenerationView{}, err
	}
	if err = m.providerEventOn(ctx, tx, tenant, row.ID, "template.provider_generation_requested", map[string]any{"project_id": row.ProjectID, "execution_id": row.ExecutionID, "provider_id": row.ProviderID, "provider_version": row.ProviderVersion, "request_sha256": row.RequestSHA256}); err != nil {
		return ProviderGenerationView{}, err
	}
	return m.generationView(row)
}
func providerRequestBytes(raw json.RawMessage) ([]byte, error) {
	if !json.Valid(raw) {
		return nil, ErrInvalid
	}
	var out any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return nil, ErrInvalid
	}
	return json.Marshal(out)
}
func (m *Module) providerGenerationOn(ctx context.Context, tx *gorm.DB, id guid.ID, operation string) (*model.ProviderGeneration, error) {
	if err := providerTransaction(tx); err != nil {
		return nil, err
	}
	if m.providerExecution == nil {
		return nil, ErrUnavailable
	}
	q := query.BindProviderGenerations(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = m.providerExecution(ctx, tx, row.TenantID, row.ProjectID, row.ExecutionID, operation); err != nil {
		return nil, err
	}
	// Acquire source principal/execution locks before the generation lock, just
	// as request and worker finalization do. Identity columns are immutable.
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(row.ID), q.ProjectID.Eq(row.ProjectID), q.ExecutionID.Eq(row.ExecutionID)).First()
}
func providerTransaction(tx *gorm.DB) error {
	if tx == nil || tx.Statement == nil {
		return ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	return nil
}
func (m *Module) readProviderGenerationOn(ctx context.Context, tx *gorm.DB, id guid.ID) (ProviderGenerationView, error) {
	row, err := m.providerGenerationOn(ctx, tx, id, "READ")
	if err != nil {
		return ProviderGenerationView{}, err
	}
	return m.generationView(row)
}
func (m *Module) cancelProviderGenerationOn(ctx context.Context, tx *gorm.DB, id guid.ID) (ProviderGenerationView, error) {
	row, err := m.providerGenerationOn(ctx, tx, id, "CANCEL")
	if err != nil {
		return ProviderGenerationView{}, err
	}
	if slices.Contains([]string{"COMPLETED", "FAILED", "CANCELLED", "CANCEL_REQUESTED"}, row.State) { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return m.generationView(row)
	}
	result := tx.WithContext(ctx).Table(m.names.ProviderGenerations.String()).Where("tenant_id=? AND id=? AND version=?", row.TenantID, row.ID, row.Version).Updates(map[string]any{"state": "CANCEL_REQUESTED", "version": row.Version + 1})
	if result.Error != nil {
		return ProviderGenerationView{}, result.Error
	}
	if result.RowsAffected != 1 {
		return ProviderGenerationView{}, ErrConflict
	}
	row.State = "CANCEL_REQUESTED"
	row.Version++
	if err = m.providerEventOn(ctx, tx, row.TenantID, row.ID, "template.provider_generation_changed", map[string]any{"execution_id": row.ExecutionID, "state": row.State, "version": row.Version}); err != nil {
		return ProviderGenerationView{}, err
	}
	return m.generationView(row)
}
func (m *Module) providerGenerationJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return ErrInvalid
	}
	var row *model.ProviderGeneration
	err := m.transaction(ctx, func(tx *gorm.DB) error {
		q := query.BindProviderGenerations(tx, m.names)
		var e error
		row, e = q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.State.In("PENDING", "RUNNING", "CANCEL_REQUESTED")).Order(q.ID).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			row = nil
			return nil
		}
		if e != nil {
			return e
		}
		if m.providerExecution == nil {
			return ErrUnavailable
		}
		return m.providerExecution(ctx, tx, row.TenantID, row.ProjectID, row.ExecutionID, "EXECUTE")
	})
	if err != nil || row == nil {
		return err
	}
	a, exists := m.providerAdapters[row.AdapterKey]
	if !exists {
		return ErrUnavailable
	}
	var pin providerConfigPin
	if json.Unmarshal([]byte(row.ConfigSnapshot), &pin) != nil || providerLicenseHash(a) != pin.LicenseSHA256 {
		return ErrUnavailable
	}
	connection, cleanup, err := m.providerConnection(&model.TemplateProvider{TenantID: row.TenantID, ID: row.ProviderID, AdapterKey: row.AdapterKey, Config: string(pin.Config), MachineID: row.MachineID, SealedSecret: row.SecretSnapshot})
	if err != nil {
		return ErrUnavailable
	}
	defer cleanup()
	state, jobRef := row.State, row.JobRef
	output := ProviderPoll{State: row.State, Outputs: []ProviderOutput{}}
	work, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	switch row.State {
	case "PENDING":
		jobRef, err = a.Generate(work, connection, "provider-generation:"+row.ID.String(), json.RawMessage(row.Request))
		if err != nil || strings.TrimSpace(jobRef) == "" || len(jobRef) > 200 {
			return ErrUnavailable
		}
		state = "RUNNING"
		output.State = state
	case "RUNNING":
		output, err = a.Poll(work, connection, jobRef)
		if err != nil {
			return ErrUnavailable
		}
		if !slices.Contains([]string{"RUNNING", "COMPLETED", "FAILED"}, output.State) {
			return ErrInvalid
		}
		state = output.State
	case "CANCEL_REQUESTED":
		if jobRef != "" {
			if err = a.Cancel(work, connection, jobRef); err != nil {
				return ErrUnavailable
			}
		}
		state = "CANCELLED" //nolint:misspell // Preserve the published domain or serialized contract spelling.
		output.State = state
	}
	if len(output.Outputs) > 100 || len(output.ReportedCost) > 80 || len(output.Currency) > 16 || state == "COMPLETED" && len(output.Outputs) == 0 {
		return ErrInvalid
	}
	for _, item := range output.Outputs {
		if strings.TrimSpace(item.FileRef) == "" || len(item.FileRef) > 2000 || !reportDigestPatternProvider.MatchString(item.SHA256) || strings.TrimSpace(item.Model) == "" || len(item.Model) > 200 || strings.TrimSpace(item.ModelVersion) == "" || len(item.ModelVersion) > 200 || len(item.Seed) > 200 {
			return ErrInvalid
		}
	}
	raw, err := json.Marshal(output)
	if err != nil || len(raw) > 256000 {
		return ErrInvalid
	}
	for _, secret := range connection.Environment {
		if secret != "" && (strings.Contains(string(raw), secret) || strings.Contains(jobRef, secret)) {
			return ErrInvalid
		}
	}
	digest := sha256.Sum256(raw)
	return m.transaction(ctx, func(tx *gorm.DB) error {
		if err = m.providerExecution(ctx, tx, row.TenantID, row.ProjectID, row.ExecutionID, "EXECUTE"); err != nil {
			return err
		}
		var finished *time.Time
		if slices.Contains([]string{"COMPLETED", "FAILED", "CANCELLED"}, state) { //nolint:misspell // Preserve the published domain or serialized contract spelling.
			now := time.Now().UTC()
			finished = &now
		}
		result := tx.WithContext(ctx).Table(m.names.ProviderGenerations.String()).Where("tenant_id=? AND id=? AND version=? AND state=?", row.TenantID, row.ID, row.Version, row.State).Updates(map[string]any{"job_ref": jobRef, "state": state, "output_snapshot": string(raw), "output_sha256": hex.EncodeToString(digest[:]), "completed_at": finished, "version": row.Version + 1})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConflict
		}
		return m.providerEventOn(ctx, tx, row.TenantID, row.ID, "template.provider_generation_changed", map[string]any{"execution_id": row.ExecutionID, "provider_id": row.ProviderID, "provider_version": row.ProviderVersion, "state": state, "output_sha256": hex.EncodeToString(digest[:])})
	})
}
