package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func installedVerifierFixture(t *testing.T, fault *string, kinds ...string) (*Module, *gorm.DB, context.Context, CalibrationStart) {
	t.Helper()
	kind, runner, method, count, machine := "DETERMINISTIC", "VISUAL", "PROBE", 2, guid.ID(101)
	if len(kinds) > 0 && kinds[0] == "MODEL_SCORED" {
		kind, runner, method, count, machine = "MODEL_SCORED", "AGENT_JUDGE", "AGREEMENT", 20, 0
	}
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = io.WriteString(w, `{"protocol":"verifier-v1","available":true}`)
			return
		}
		if r.URL.Path != "/evaluate" {
			http.NotFound(w, r)
			return
		}
		raw, e := io.ReadAll(r.Body)
		if !assert.NoError(t, e) {
			return
		}
		if !assert.NotContains(t, string(raw), `"expected"`) {
			return
		}
		var in verifierHTTPRequest
		if !assert.NoError(t, json.Unmarshal(raw, &in)) {
			return
		}
		if !assert.Equal(t, "POST", r.Method) {
			return
		}
		if !assert.Equal(t, "verifier-v1", in.Protocol) {
			return
		}
		out := verifierHTTPResponse{Model: in.Model, Protocol: in.Protocol, RequestSHA256: in.RequestSHA256, ProviderRef: in.ProviderRef, MachineID: in.MachineID}
		for _, s := range in.Samples {
			var input struct {
				Valid bool            `json:"valid"`
				Files []evalFileInput `json:"files"`
			}
			if !assert.NoError(t, json.Unmarshal(s.Input, &input)) {
				return
			}
			if len(input.Files) > 0 {
				var content struct {
					Valid bool `json:"valid"`
				}
				if !assert.NoError(t, json.Unmarshal(input.Files[0].Data, &content)) {
					return
				}
				if !assert.Equal(t, input.Files[0].SHA256, evalDigest(input.Files[0].Data)) {
					return
				}
				input.Valid = content.Valid
			}
			verdict := "FAIL"
			if input.Valid {
				verdict = "PASS"
			}
			if *fault == "invert" {
				if verdict == "PASS" {
					verdict = "FAIL"
				} else {
					verdict = "PASS"
				}
			}
			out.Results = append(out.Results, verifierHTTPResult{SampleID: s.SampleID, InputSHA256: s.SHA256, Verdict: verdict, EvidenceRefs: []string{"observed:" + s.SHA256}})
		}
		switch *fault {
		case "digest":
			out.RequestSHA256 = strings.Repeat("0", 64)
		case "input":
			out.Results[0].InputSHA256 = strings.Repeat("0", 64)
		case "duplicate":
			out.Results[1] = out.Results[0]
		case "provider":
			out.ProviderRef = "other"
		case "machine":
			out.MachineID++
		case "model":
			out.Model = "unattributed-model"
		case "evidence":
			out.Results[0].EvidenceRefs = nil
		case "unavailable":
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if !assert.NoError(t, json.NewEncoder(w).Encode(out)) {
			return
		}
	}))
	t.Cleanup(service.Close)
	m, db, ctx := templateHarness(t)
	m.providerAdapters = map[string]ProviderAdapter{"verifier_http_v1": verifierHTTPAdapter()}
	fingerprint := "observed-browser-141"
	m.verifierTools = fleet.VerifierTools{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) (string, error) { return fingerprint, nil }}
	var provider ProviderView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		provider, e = m.createProviderOn(ctx, tx, ProviderDraft{Kind: "VLM_JUDGE", AdapterKey: "verifier_http_v1", DisplayName: "Configured verifier", Config: map[string]json.RawMessage{"endpoint": json.RawMessage(fmt.Sprintf("%q", service.URL)), "model": json.RawMessage(`"configured-model"`)}, MachineID: machine, AcceptLicenseNotice: true})
		return e
	}))
	health, e := m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "health-once"})
	require.NoError(t, e)
	require.Equal(t, "ENABLED", health.Body.Data.State)
	d := testDefinition()
	def := VerifierDefinition{PluginKey: "http-v1", RequiredTools: []string{"browser"}, Key: "visual", Kind: kind, Runner: runner, Version: 1, Argv: []string{"probe"}, TimeoutSec: 60, Limitations: "Only the known sample inputs and configured machine/tool are evaluated"}
	if kind == "MODEL_SCORED" {
		def.RequiredTools = nil
		def.Argv = nil
	}
	d.Metadata["verifiers"] = []VerifierDefinition{def}
	v := publishRuntime(t, m, db, ctx, "installed-plugin", d)
	for i := 0; i < count; i++ {
		valid := i%2 == 0
		expected := "FAIL"
		if valid {
			expected = "PASS"
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.addCalibrationSampleOn(ctx, tx, CalibrationSampleDraft{TemplateVersionID: v.ID, VerifierKey: "visual", Input: json.RawMessage(fmt.Sprintf(`{"sample":%d,"valid":%t}`, i, valid)), Expected: expected}, "MANUAL")
			return e
		}))
	}
	m.calibrationEvaluator = m.evaluateInstalledVerifier
	return m, db, ctx, CalibrationStart{TemplateVersionID: v.ID, VerifierKey: "visual", ProviderRef: "provider:" + provider.ID.String(), MachineID: machine, Method: method}
}
func TestInstalledVerifierHTTPExecutesAnswerBlindSamplesAndBindsToolCalibration(t *testing.T) {
	fault := ""
	m, db, ctx, in := installedVerifierFixture(t, &fault)
	head := calibrationStartForTest(t, m, db, ctx, in)
	require.NoError(t, m.evaluateCalibrationRun(ctx, 1, head.RunID))
	head = calibrationGetForTest(t, m, db, ctx, head.ID)
	require.Equal(t, "COMPLETED", head.RunState)
	require.Equal(t, "CALIBRATED", head.State)
	require.Equal(t, 1000000, *head.AgreementPPM)
	read := func(machine guid.ID) string {
		var state string
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			state, e = m.toolCalibrationOn(ctx, tx, 1, in.TemplateVersionID, in.VerifierKey, machine)
			return e
		}))
		return state
	}
	require.Equal(t, "CALIBRATED", read(101))
	require.Equal(t, "UNCALIBRATED", read(102))
	m.verifierTools.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) (string, error) {
		return "observed-browser-142", nil
	}
	require.Equal(t, "DRIFTED", read(101), "a tool change invalidates an accepted probe before any scheduled review")
	require.NoError(t, m.evaluateCalibrationRun(ctx, 1, head.RunID), "terminal retry is immutable")
	require.Equal(t, head, calibrationGetForTest(t, m, db, ctx, head.ID))
}
func TestInstalledVerifierRejectsUnattributedResponsesAndUnavailableServices(t *testing.T) {
	for _, failure := range []string{"digest", "input", "duplicate", "provider", "machine", "evidence", "unavailable"} {
		t.Run(failure, func(t *testing.T) {
			fault := failure
			m, db, ctx, in := installedVerifierFixture(t, &fault)
			head := calibrationStartForTest(t, m, db, ctx, in)
			require.NoError(t, m.evaluateCalibrationRun(ctx, 1, head.RunID))
			head = calibrationGetForTest(t, m, db, ctx, head.ID)
			require.NotEqual(t, "COMPLETED", head.RunState)
			require.Equal(t, "UNCALIBRATED", head.State)
			require.Nil(t, head.AgreementPPM)
			require.Empty(t, head.Results)
		})
	}
}
func TestInstalledVerifierRechecksInitiatorAuthorityAndFrozenProvider(t *testing.T) {
	for _, failure := range []string{"authority", "configuration", "tool"} {
		t.Run(failure, func(t *testing.T) {
			fault := ""
			m, db, ctx, in := installedVerifierFixture(t, &fault)
			head := calibrationStartForTest(t, m, db, ctx, in)
			switch failure {
			case "authority":
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "configuration":
				_, e := query.BindProviders(db, m.names).WithContext(ctx).Where(query.BindProviders(db, m.names).TenantID.Eq(guid.ID(1))).UpdateSimple(query.BindProviders(db, m.names).Version.Add(1))
				require.NoError(t, e)
			case "tool":
				m.verifierTools.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) (string, error) { return "new-tool", nil }
			}
			require.NoError(t, m.evaluateCalibrationRun(ctx, 1, head.RunID))
			var state string
			require.NoError(t, db.Table(m.names.CalibrationRuns.String()).Select("state").Where("id=?", head.RunID).Scan(&state).Error)
			require.Equal(t, "UNAVAILABLE", state)
		})
	}
}
func TestVerifierPluginDeclarationsAndHTTPConfigurationAreClosed(t *testing.T) {
	v := verifierDefinition()
	require.True(t, validVerifierPlugin(v))
	v.PluginKey = "uninstalled"
	require.False(t, validVerifierPlugin(v))
	v.PluginKey = "http-v1"
	v.RequiredTools = []string{"browser", "browser"}
	require.False(t, validVerifierPlugin(v))
	v.RequiredTools = []string{"../browser"}
	require.False(t, validVerifierPlugin(v))
	for _, raw := range []string{`{}`, `{"endpoint":"file:///private"}`, `{"endpoint":"https://user:secret@example.invalid"}`, `{"endpoint":"https://example.invalid?secret=a"}`, `{"endpoint":"https://example.invalid","secret":"private"}`} {
		_, e := parseVerifierHTTPConfig(json.RawMessage(raw))
		require.Error(t, e)
	}
	plugins := installedVerifierPlugins()
	plugins[0].Runners[0] = "CHANGED"
	require.Equal(t, "COMMAND", installedVerifierPlugins()[0].Runners[0])
}

func TestInstalledProviderFamiliesUseActualGatewayTransport(t *testing.T) {
	for _, kind := range providerKinds {
		t.Run(kind, func(t *testing.T) {
			m, db, ctx := bindStudio(t, "VIDEO")
			providerFixture(t, m)
			generated, canceled := 0, 0
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !assert.Equal(t, "Bearer transport-private", r.Header.Get("Authorization")) {
					return
				}
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/health":
					_, _ = io.WriteString(w, `{"protocol":"provider-v1","available":true}`)
				case r.Method == http.MethodPost && r.URL.Path == "/generations":
					var input struct {
						Protocol string          `json:"protocol"`
						Kind     string          `json:"kind"`
						Key      string          `json:"idempotency_key"`
						Model    string          `json:"model"`
						Input    json.RawMessage `json:"input"`
					}
					if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&input)) {
						return
					}
					if !assert.Equal(t, "provider-v1", input.Protocol) {
						return
					}
					if !assert.Equal(t, kind, input.Kind) {
						return
					}
					if !assert.Equal(t, "configured-model", input.Model) {
						return
					}
					if !assert.True(t, strings.HasPrefix(input.Key, "provider-generation:")) {
						return
					}
					if !assert.JSONEq(t, `{"prompt":"actual input"}`, string(input.Input)) {
						return
					}
					generated++
					_, _ = io.WriteString(w, `{"job_ref":"remote-job"}`)
				case r.Method == http.MethodGet && r.URL.Path == "/generations/remote-job":
					if !assert.NoError(t, json.NewEncoder(w).Encode(ProviderPoll{State: "COMPLETED", Outputs: []ProviderOutput{{FileRef: "files:actual", SHA256: strings.Repeat("a", 64), Model: "configured-model", ModelVersion: "1"}}})) {
						return
					}
				case r.Method == http.MethodPost && r.URL.Path == "/generations/remote-job/cancel":
					canceled++
					w.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(w, r)
				}
			}))
			defer service.Close()
			var provider ProviderView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				endpoint, _ := json.Marshal(service.URL)
				provider, e = m.createProviderOn(ctx, tx, ProviderDraft{Kind: kind, AdapterKey: strings.ToLower(kind) + "_http_v1", DisplayName: "Transport", Config: map[string]json.RawMessage{"endpoint": endpoint, "model": json.RawMessage(`"configured-model"`)}, Secret: "transport-private", AcceptLicenseNotice: true})
				return e
			}))
			health, e := m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "health"})
			require.NoError(t, e)
			require.Equal(t, "ENABLED", health.Body.Data.State)
			instance, e := m.instanceOn(ctx, db, 99)
			require.NoError(t, e)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.bindProvidersOn(ctx, tx, 99, instance.Version, map[string]guid.ID{kind: provider.ID})
				return e
			}))
			m.providerExecution = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return nil }
			var first ProviderGenerationView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				first, e = m.requestProviderGenerationOn(ctx, tx, ProviderGenerationRequest{ProjectID: 99, ExecutionID: 123, Kind: kind, Input: json.RawMessage(`{"prompt":"actual input"}`)})
				return e
			}))
			require.NoError(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}))
			require.Equal(t, 1, generated)
			require.NoError(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				out, e := m.readProviderGenerationOn(ctx, tx, first.ID)
				require.Equal(t, "COMPLETED", out.State)
				require.NotEmpty(t, out.OutputSHA256)
				require.Equal(t, "configured-model", out.Output.Outputs[0].Model)
				return e
			}))
			// Exercise cancellation through the same persisted gateway, with a new execution.
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				first, e = m.requestProviderGenerationOn(ctx, tx, ProviderGenerationRequest{ProjectID: 99, ExecutionID: 124, Kind: kind, Input: json.RawMessage(`{"prompt":"actual input"}`)})
				return e
			}))
			require.NoError(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.cancelProviderGenerationOn(ctx, tx, first.ID); return e }))
			require.NoError(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}))
			require.Equal(t, 1, canceled)
			service.Close()
			offline, e := m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "offline-health"})
			require.NoError(t, e)
			require.Equal(t, "UNAVAILABLE", offline.Body.Data.State)
		})
	}
}

func TestInstalledProviderHTTPRejectsWrongProtocolAndCredentialRedirect(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		t.Error("credential redirect must not be followed")
	}))
	defer target.Close()
	for _, fault := range []string{"protocol", "offline", "redirect", "malformed", "trailing"} {
		t.Run(fault, func(t *testing.T) {
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch fault {
				case "protocol":
					_, _ = io.WriteString(w, `{"protocol":"another-v1","available":true}`)
				case "offline":
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, "transport-private")
				case "redirect":
					http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
				case "malformed":
					_, _ = io.WriteString(w, `{"protocol":"provider-v1","available":true,"credential":"transport-private"}`)
				case "trailing":
					_, _ = io.WriteString(w, `{"protocol":"provider-v1","available":true}{}`)
				}
			}))
			defer service.Close()
			config, _ := json.Marshal(verifierHTTPConfig{Endpoint: service.URL})
			adapter := providerHTTPAdapter("image_gen_http_v1", "IMAGE_GEN", "provider-v1")
			health, e := adapter.Health(t.Context(), ProviderConnection{Config: config, Environment: map[string]string{"VERIFIER_CREDENTIAL": "transport-private"}})
			require.ErrorIs(t, e, ErrUnavailable)
			require.False(t, health.Available)
			require.NotContains(t, e.Error(), "transport-private")
		})
	}
	require.False(t, reached)
	adapter := verifierHTTPAdapter()
	_, e := adapter.Generate(t.Context(), ProviderConnection{}, "", json.RawMessage(`{}`))
	require.ErrorIs(t, e, ErrInvalid)
	for _, raw := range []string{`{"endpoint":"https://example.invalid"}{}`, `{"endpoint":"https://example.invalid"}garbage`} {
		require.Error(t, adapter.ValidateConfig(json.RawMessage(raw)))
	}
}

func TestVerifierCurrentProviderAndToolSourcesFailClosed(t *testing.T) {
	fault := ""
	m, db, ctx, in := installedVerifierFixture(t, &fault)
	read := func(ref string, machine guid.ID, def VerifierDefinition) error {
		return db.Transaction(func(tx *gorm.DB) error { _, _, e := m.verifierProviderOn(ctx, tx, 1, ref, machine, def); return e })
	}
	def := VerifierDefinition{RequiredTools: []string{"browser"}}
	for _, ref := range []string{"other:1", "provider:bad", "provider:999"} {
		require.Error(t, read(ref, in.MachineID, def))
	}
	require.ErrorIs(t, read(in.ProviderRef, 102, def), ErrProviderAdapter)
	tools := m.verifierTools
	m.verifierTools = fleet.VerifierTools{}
	require.ErrorIs(t, read(in.ProviderRef, in.MachineID, def), ErrUnavailable)
	injected := errors.New("tool source unavailable")
	m.verifierTools = fleet.VerifierTools{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, []string) (string, error) { return "", injected }}
	require.ErrorIs(t, read(in.ProviderRef, in.MachineID, def), injected)
	m.verifierTools = tools
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("verifier-provider-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Providers.String() {
			tx.AddError(injected)
		}
	}))
	require.ErrorIs(t, read(in.ProviderRef, in.MachineID, def), injected)
	require.NoError(t, db.Callback().Query().Remove("verifier-provider-failure"))
	_, e := m.evaluateInstalledVerifier(ctx, CalibrationEvaluationRequest{})
	require.ErrorIs(t, e, ErrUnavailable)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, e := m.verifierGetOn(ctx, tx, 2, in.TemplateVersionID, in.VerifierKey)
		require.ErrorIs(t, e, ErrInvalid)
		return nil
	}))
	for _, kind := range []string{"DETERMINISTIC", "MODEL_SCORED"} {
		t.Run(kind, func(t *testing.T) {
			fault := ""
			m, db, ctx, in := installedVerifierFixture(t, &fault, kind)
			head := calibrationStartForTest(t, m, db, ctx, in)
			require.NoError(t, m.evaluateCalibrationRun(ctx, 1, head.RunID))
			head = calibrationGetForTest(t, m, db, ctx, head.ID)
			if kind == "MODEL_SCORED" {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.confirmCalibrationOn(ctx, tx, head.ID, head.Version, true, "Human approves")
					return e
				}))
			}
			state := func() string {
				var out string
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					s, e := m.verifierCalibrationStateOn(ctx, tx, 1, in.TemplateVersionID, in.VerifierKey, in.MachineID, in.Method)
					out = s
					return e
				}))
				return out
			}
			require.Equal(t, "CALIBRATED", state())
			require.NoError(t, db.Table(m.names.CalibrationRuns.String()).Where("id=?", head.RunID).Update("results", "invalid").Error)
			require.Equal(t, "DRIFTED", state())
			require.NoError(t, db.Table(m.names.Calibrations.String()).Where("id=?", head.ID).Update("next_review_at", time.Now().Add(-time.Hour)).Error)
			require.Equal(t, "DRIFTED", state())
		})
	}
}
