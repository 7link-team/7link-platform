package templates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// These are installed implementations, not claims that a remote service or
// machine tool is available. Runner receipts retain their existing protocol.
type VerifierPlugin struct {
	Key        string   `json:"key"`
	Version    int      `json:"version"`
	Runners    []string `json:"runners"`
	Evaluation string   `json:"evaluation"`
}

func installedVerifierPlugins() []VerifierPlugin {
	return []VerifierPlugin{
		{Key: "runner-v1", Version: 1, Runners: []string{"COMMAND", "TEST", "VISUAL", "AGENT_JUDGE"}, Evaluation: "RUNNER_RECEIPT"},
		{Key: "http-v1", Version: 1, Runners: []string{"COMMAND", "TEST", "VISUAL", "AGENT_JUDGE"}, Evaluation: "HTTP_PROVIDER"},
	}
}
func validVerifierPlugin(v VerifierDefinition) bool {
	key := v.PluginKey
	if key == "" {
		key = "runner-v1"
	}
	found := false
	for _, p := range installedVerifierPlugins() {
		if p.Key == key && slices.Contains(p.Runners, v.Runner) {
			found = true
		}
	}
	if !found || len(v.RequiredTools) > 20 {
		return false
	}
	seen := map[string]bool{}
	for _, name := range v.RequiredTools {
		if !providerKeyPattern.MatchString(name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

type verifierHTTPConfig struct {
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
}

func parseVerifierHTTPConfig(raw json.RawMessage) (verifierHTTPConfig, error) {
	var c verifierHTTPConfig
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || len(c.Endpoint) > 2000 || len(c.Model) > 200 {
		return c, ErrInvalid
	}
	u, e := url.Parse(c.Endpoint)
	// A deployment administrator configures the service, never sample input.
	// HTTP supports private Runner services; redirecting credentials is forbidden.
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, ErrInvalid
	}
	return c, nil
}
func verifierHTTPCall(ctx context.Context, c ProviderConnection, method, path string, input, output any) error {
	cfg, e := parseVerifierHTTPConfig(c.Config)
	if e != nil {
		return e
	}
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return ErrInvalid
		}
		if len(b) > 4<<20 {
			return ErrInvalid
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(cfg.Endpoint, "/")+path, body)
	if e != nil {
		return ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	if secret := c.Environment["VERIFIER_CREDENTIAL"]; secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Do(req)
	if e != nil {
		return ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrUnavailable
	}
	if output == nil {
		return nil
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if e != nil || len(b) > 4<<20 {
		return ErrUnavailable
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if dec.Decode(output) != nil || dec.Decode(new(any)) != io.EOF {
		return ErrUnavailable
	}
	return nil
}

// The configured service implements this versioned protocol. Its health response
// must identify it explicitly; an unrelated endpoint returning 200 is insufficient.
func verifierHTTPAdapter() ProviderAdapter {
	return providerHTTPAdapter("verifier_http_v1", "VLM_JUDGE", "verifier-v1")
}

// Each family uses a separately configured service and license. Installing the
// protocol never marks that service available without a successful health check.
func providerHTTPAdapter(key, kind, protocol string) ProviderAdapter {
	return ProviderAdapter{
		Definition:           ProviderAdapterView{Key: key, Kind: kind, LicenseNotice: "The administrator must accept the configured verifier service and model licences before use.", RegionNotice: "Frozen evaluation inputs are sent to the configured service; configure its region and retention policy.", PublicConfigFields: []string{"endpoint", "model"}}, //nolint:misspell // Preserve the published domain or serialized contract spelling.
		SecretEnvironmentKey: "VERIFIER_CREDENTIAL",
		ValidateConfig:       func(raw json.RawMessage) error { _, e := parseVerifierHTTPConfig(raw); return e },
		Health: func(ctx context.Context, c ProviderConnection) (ProviderHealth, error) {
			var r struct {
				Protocol  string `json:"protocol"`
				Available bool   `json:"available"`
			}
			e := verifierHTTPCall(ctx, c, "GET", "/health", nil, &r)
			if e != nil || r.Protocol != protocol || !r.Available {
				return ProviderHealth{Limitations: "Verifier service unavailable or protocol not established"}, ErrUnavailable
			}
			return ProviderHealth{Available: true, Limitations: "Service reachable; individual verdicts and calibration remain unproven"}, nil
		},
		Generate: func(ctx context.Context, c ProviderConnection, idempotencyKey string, input json.RawMessage) (string, error) {
			if strings.TrimSpace(idempotencyKey) == "" || len(idempotencyKey) > 200 {
				return "", ErrInvalid
			}
			cfg, e := parseVerifierHTTPConfig(c.Config)
			if e != nil {
				return "", e
			}
			var out struct {
				JobRef string `json:"job_ref"`
			}
			e = verifierHTTPCall(ctx, c, "POST", "/generations", struct {
				IdempotencyKey string          `json:"idempotency_key"`
				Protocol       string          `json:"protocol"`
				Kind           string          `json:"kind"`
				Model          string          `json:"model"`
				Input          json.RawMessage `json:"input"`
			}{idempotencyKey, protocol, kind, cfg.Model, input}, &out)
			if e != nil {
				return "", e
			}
			if strings.TrimSpace(out.JobRef) == "" || len(out.JobRef) > 200 {
				return "", ErrInvalid
			}
			return out.JobRef, nil
		},
		Poll: func(ctx context.Context, c ProviderConnection, job string) (ProviderPoll, error) {
			var out ProviderPoll
			if strings.TrimSpace(job) == "" || len(job) > 200 {
				return out, ErrInvalid
			}
			e := verifierHTTPCall(ctx, c, "GET", "/generations/"+url.PathEscape(job), nil, &out)
			return out, e
		},
		Cancel: func(ctx context.Context, c ProviderConnection, job string) error {
			if strings.TrimSpace(job) == "" || len(job) > 200 {
				return ErrInvalid
			}
			return verifierHTTPCall(ctx, c, "POST", "/generations/"+url.PathEscape(job)+"/cancel", struct {
				Protocol string `json:"protocol"`
			}{protocol}, nil)
		},
	}
}

func (m *Module) verifierProviderOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, ref string, machine guid.ID, v VerifierDefinition) (*model.TemplateProvider, string, error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 2 && len(parts) != 4 || parts[0] != "provider" {
		return nil, "", ErrProviderAdapter
	}
	id, e := guid.RequireID(parts[1])
	if e != nil {
		return nil, "", ErrInvalid
	}
	q := query.BindProviders(tx, m.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, "", ErrProviderAdapter
	}
	if e != nil {
		return nil, "", e
	}
	if row.AdapterKey != "verifier_http_v1" || m.providerView(row).State != "ENABLED" || row.HealthCheckedAt == nil || row.MachineID != machine {
		return nil, "", ErrProviderAdapter
	}
	tools := ""
	if len(requiredVerifierTools(v)) > 0 {
		if machine <= 0 || m.verifierTools.ReadOn == nil {
			return nil, "", ErrUnavailable
		}
		tools, e = m.verifierTools.ReadOn(ctx, tx, tenant, machine, requiredVerifierTools(v))
		if e != nil {
			return nil, "", e
		}
	}
	pin := fmt.Sprintf("provider:%s:%d:%s", row.ID, row.Version, evalDigest([]byte(row.Config+"\n"+tools)))
	if len(parts) == 4 && ref != pin {
		return nil, "", ErrConflict
	}
	return row, pin, nil
}

// HTTP payload intentionally has no Human labels. Both request digest and every
// input digest must be echoed, so another attempt's response cannot be reused.
type verifierHTTPRequest struct {
	Agent             *verifierHTTPAgent `json:"agent,omitempty"`
	Protocol          string             `json:"protocol"`
	RunID             guid.ID            `json:"run_id"`
	TemplateVersionID guid.ID            `json:"template_version_id"`
	DefinitionSHA256  string             `json:"definition_sha256"`
	ProviderRef       string             `json:"provider_ref"`
	MachineID         guid.ID            `json:"machine_id"`
	Model             string             `json:"model"`
	Verifier          VerifierDefinition `json:"verifier"`
	Samples           []CalibrationInput `json:"samples"`
	RequestSHA256     string             `json:"request_sha256"`
}
type verifierHTTPAgent struct {
	AgentID             guid.ID `json:"agent_id"`
	Role                string  `json:"role"`
	Model               string  `json:"model"`
	Family              string  `json:"family"`
	ConfigurationSHA256 string  `json:"configuration_sha256"`
	Instructions        string  `json:"instructions"`
}
type verifierHTTPResult struct {
	SampleID     guid.ID  `json:"sample_id"`
	InputSHA256  string   `json:"input_sha256"`
	Verdict      string   `json:"verdict"`
	EvidenceRefs []string `json:"evidence_refs"`
}
type verifierHTTPResponse struct {
	Model         string               `json:"model,omitempty"`
	Protocol      string               `json:"protocol"`
	RequestSHA256 string               `json:"request_sha256"`
	ProviderRef   string               `json:"provider_ref"`
	MachineID     guid.ID              `json:"machine_id"`
	Results       []verifierHTTPResult `json:"results"`
}

func (m *Module) evaluateInstalledVerifier(ctx context.Context, in CalibrationEvaluationRequest) (CalibrationEvaluation, error) {
	out := CalibrationEvaluation{ProviderRef: in.ProviderRef, MachineID: in.MachineID}
	if in.Verifier.PluginKey != "http-v1" || in.InitiatorID <= 0 || len(in.Samples) == 0 || len(in.Samples) > 1000 {
		return out, ErrUnavailable
	}
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: in.TenantID, PrincipalID: in.InitiatorID, SubjectType: authn.SubjectTypePrincipal})
	var row *model.TemplateProvider
	var pinned string
	var agent *verifierHTTPAgent
	e := m.transaction(ctx, func(tx *gorm.DB) error {
		version, e := m.versionOn(ctx, tx, in.TemplateVersionID, "template_manage")
		if e != nil {
			return e
		}
		if version.SHA256 != in.DefinitionSHA256 {
			return ErrConflict
		}
		actual, e := calibrationVerifier(version, in.Verifier.Key)
		if e != nil {
			return e
		}
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(in.Verifier)
		if !bytes.Equal(a, b) {
			return ErrConflict
		}
		if in.Target.Role != "" {
			a, e := m.evalAgentOn(ctx, tx, in.TenantID, in.Target)
			if e != nil {
				return e
			}
			agent = &verifierHTTPAgent{AgentID: a.AgentID, Role: a.Role, Model: a.Model, Family: a.Family, ConfigurationSHA256: a.SHA256, Instructions: a.Instructions}
		}
		row, pinned, e = m.verifierProviderOn(ctx, tx, in.TenantID, in.ProviderRef, in.MachineID, in.Verifier)
		return e
	})
	if e != nil {
		return out, e
	}
	if pinned != in.ProviderRef {
		return out, ErrConflict
	}
	conn, cleanup, e := m.providerConnection(row)
	if e != nil {
		return out, e
	}
	defer cleanup()
	cfg, e := parseVerifierHTTPConfig(conn.Config)
	if e != nil {
		return out, e
	}
	if in.Verifier.Kind == "MODEL_SCORED" && strings.TrimSpace(cfg.Model) == "" {
		return out, ErrUnavailable
	}
	req := verifierHTTPRequest{Protocol: "verifier-v1", RunID: in.RunID, TemplateVersionID: in.TemplateVersionID, DefinitionSHA256: in.DefinitionSHA256, ProviderRef: pinned, MachineID: in.MachineID, Model: cfg.Model, Verifier: in.Verifier, Samples: in.Samples}
	req.Agent = agent
	if agent != nil && agent.Model != cfg.Model {
		return out, ErrConflict
	}
	seen := map[guid.ID]string{}
	for _, s := range in.Samples {
		if s.SampleID <= 0 || seen[s.SampleID] != "" || evalDigest(s.Input) != s.SHA256 {
			return out, ErrInvalid
		}
		seen[s.SampleID] = s.SHA256
	}
	raw, _ := json.Marshal(req)
	req.RequestSHA256 = evalDigest(raw)
	var resp verifierHTTPResponse
	if e = verifierHTTPCall(ctx, conn, "POST", "/evaluate", req, &resp); e != nil {
		return out, e
	}
	if resp.Protocol != req.Protocol || resp.RequestSHA256 != req.RequestSHA256 || resp.ProviderRef != pinned || resp.MachineID != in.MachineID || len(resp.Results) != len(in.Samples) {
		return out, ErrInvalid
	}
	if in.Verifier.Kind == "MODEL_SCORED" && resp.Model != cfg.Model {
		return out, ErrInvalid
	}
	for _, r := range resp.Results {
		if seen[r.SampleID] == "" || seen[r.SampleID] != r.InputSHA256 || !slices.Contains([]string{"PASS", "WARN", "FAIL", "INCONCLUSIVE"}, r.Verdict) || len(r.EvidenceRefs) == 0 || len(r.EvidenceRefs) > 20 {
			return out, ErrInvalid
		}
		delete(seen, r.SampleID)
		for _, ref := range r.EvidenceRefs {
			if strings.TrimSpace(ref) == "" || len(ref) > 2000 {
				return out, ErrInvalid
			}
		}
		out.Results = append(out.Results, CalibrationResult{SampleID: r.SampleID, Verdict: r.Verdict, EvidenceRefs: r.EvidenceRefs})
	}
	// A revocation or provider/tool change during the network call invalidates it.
	e = m.transaction(ctx, func(tx *gorm.DB) error {
		if _, e := m.versionOn(ctx, tx, in.TemplateVersionID, "template_manage"); e != nil {
			return e
		}
		_, _, e := m.verifierProviderOn(ctx, tx, in.TenantID, pinned, in.MachineID, in.Verifier)
		if e == nil && agent != nil {
			_, e = m.evalAgentOn(ctx, tx, in.TenantID, in.Target)
		}
		return e
	})
	if e == nil {
		for _, c := range in.EvalCases {
			if _, check := m.evalSampleInput(ctx, in.TenantID, in.InitiatorID, c, false); check != nil {
				return out, check
			}
		}
	}
	return out, e
}

func requiresToolCalibration(v VerifierDefinition) bool {
	return v.Kind == "DETERMINISTIC" && (v.Runner == "VISUAL" || len(v.RequiredTools) > 0)
}
func requiredVerifierTools(v VerifierDefinition) []string {
	if len(v.RequiredTools) == 0 && v.Runner == "VISUAL" {
		return []string{"browser"}
	}
	return v.RequiredTools
}
func (m *Module) toolCalibrationOn(ctx context.Context, tx *gorm.DB, tenant, version guid.ID, key string, machine guid.ID) (string, error) {
	return m.verifierCalibrationStateOn(ctx, tx, tenant, version, key, machine, "PROBE")
}
func (m *Module) verifierGetOn(ctx context.Context, tx *gorm.DB, tenant, version guid.ID, key string) (VerifierDefinition, string, error) {
	if providerTransaction(tx) != nil || authn.TenantIDFromContext(ctx) != tenant {
		return VerifierDefinition{}, "", ErrInvalid
	}
	q := query.BindVersions(tx, m.names)
	row, e := q.WithContext(ctx).Where(q.ID.Eq(version), q.TenantID.In(guid.ID(0), tenant)).First()
	if e != nil {
		return VerifierDefinition{}, "", e
	}
	def, e := calibrationVerifier(row, key)
	if e != nil {
		return def, "", e
	}
	if def.Kind != "MODEL_SCORED" {
		if requiresToolCalibration(def) {
			return def, "UNCALIBRATED", nil
		}
		return def, "CALIBRATED", nil
	}
	state, e := m.verifierCalibrationStateOn(ctx, tx, tenant, version, key, 0, "AGREEMENT")
	return def, state, e
}
func (m *Module) verifierCalibrationStateOn(ctx context.Context, tx *gorm.DB, tenant, version guid.ID, key string, machine guid.ID, method string) (string, error) {
	if tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant || providerTransaction(tx) != nil {
		return "", ErrInvalid
	}
	q := query.BindVersions(tx, m.names)
	row, e := q.WithContext(ctx).Where(q.ID.Eq(version), q.TenantID.In(guid.ID(0), tenant)).First()
	if e != nil {
		return "", e
	}
	def, e := calibrationVerifier(row, key)
	if e != nil {
		return "", e
	}
	if method == "PROBE" && !requiresToolCalibration(def) {
		return "CALIBRATED", nil
	}
	if method == "PROBE" && machine <= 0 {
		return "UNCALIBRATED", nil
	}
	c := query.BindCalibrations(tx, m.names)
	entries, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.TemplateVersionID.Eq(version), c.VerifierKey.Eq(key), c.MachineID.Eq(machine), c.Method.Eq(method)).Order(c.ID.Desc()).Limit(1).Find()
	if e != nil {
		return "", e
	}
	if len(entries) == 0 {
		return "UNCALIBRATED", nil
	}
	head := entries[0]
	if head.State != "CALIBRATED" {
		return head.State, nil
	}
	if !head.NextReviewAt.After(time.Now()) || method == "AGREEMENT" && head.CalibratedBy == nil {
		return "DRIFTED", nil
	}
	_, _, e = m.verifierProviderOn(ctx, tx, tenant, head.ProviderRef, machine, def)
	if e != nil {
		return "DRIFTED", nil //nolint:nilerr // Invalid calibration facts are represented by the explicit DRIFTED status.
	}
	r := query.BindCalibrationRuns(tx, m.names)
	run, e := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ID.Eq(head.CurrentRunID), r.CalibrationID.Eq(head.ID)).First()
	if e != nil {
		return "", e
	}
	var snap calibrationSnapshot
	if run.State != "COMPLETED" || run.AgreementPPM == nil || *run.AgreementPPM < head.TargetPPM || evalDigest([]byte(run.Snapshot)) != run.SHA256 || json.Unmarshal([]byte(run.Snapshot), &snap) != nil || snap.DefinitionSHA256 != row.SHA256 {
		return "DRIFTED", nil //nolint:nilerr // Invalid calibration facts are represented by the explicit DRIFTED status.
	}
	var results []CalibrationResult
	if json.Unmarshal([]byte(run.Results), &results) != nil {
		return "DRIFTED", nil //nolint:nilerr // Invalid calibration facts are represented by the explicit DRIFTED status.
	}
	agreement, e := calibrationAgreement(snap, CalibrationEvaluation{ProviderRef: head.ProviderRef, MachineID: machine, Results: results}, head.ProviderRef, machine)
	if e != nil || agreement < head.TargetPPM || agreement != *run.AgreementPPM {
		return "DRIFTED", nil //nolint:nilerr // Invalid calibration facts are represented by the explicit DRIFTED status.
	}
	return "CALIBRATED", nil
}
