package app

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The HTTP load harness. It drives the real stack -- router, middleware,
// session and bearer authentication, authorization, database -- through
// httptest rather than calling services directly, because the cost of a
// request is not the cost of the handler alone.
//
// It measures rather than gates. Latency on a shared machine is not a stable
// assertion, and a flaky gate gets disabled, so the only things asserted are
// the ones that are always wrong: a transport failure or a 5xx. Percentiles
// are reported for a human to read.
//
// Opt in with IAM_LOAD_DATABASE_TYPE and IAM_LOAD_DATABASE_DSN; without them
// it skips, so `make ci` is unaffected.
const (
	iamLoadDatabaseType = "IAM_LOAD_DATABASE_TYPE"
	iamLoadDatabaseDSN  = "IAM_LOAD_DATABASE_DSN"
	iamLoadConcurrency  = "IAM_LOAD_CONCURRENCY"
	iamLoadRequests     = "IAM_LOAD_REQUESTS"

	iamLoadDefaultConcurrency = 16
	iamLoadDefaultRequests    = 200
)

type iamLoadScenario struct {
	name   string
	method string
	path   string
	tenant guid.ID
	token  string
	body   map[string]any
	// want is the status a single warm-up request must return. A scenario
	// that quietly measures a 404 measures nothing, and a 404 is not a 5xx,
	// so the warm-up is what keeps a wrong path from passing as load.
	want int
}

type iamLoadSample struct {
	status  int
	latency time.Duration
	err     error
}

type iamLoadReport struct {
	scenario string
	samples  []iamLoadSample
	elapsed  time.Duration
}

func TestIAMHTTPLoadExternalDatabase(t *testing.T) {
	databaseType := os.Getenv(iamLoadDatabaseType)
	databaseDSN := os.Getenv(iamLoadDatabaseDSN)
	if databaseType == "" || databaseDSN == "" {
		t.Skip("external load database is not configured")
	}
	concurrency := iamLoadEnvInt(t, iamLoadConcurrency, iamLoadDefaultConcurrency)
	requests := iamLoadEnvInt(t, iamLoadRequests, iamLoadDefaultRequests)

	fixture := newIAMTenantFixtureWithDatabase(t, Database{
		Type: databaseType, Dsn: databaseDSN, Writable: true, Readable: true,
		MaxOpenConns: concurrency * 2, MaxIdleConns: concurrency,
	}, "")
	scenarios := fixture.loadScenarios(t)

	client := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        concurrency * 2,
			MaxIdleConnsPerHost: concurrency * 2,
			MaxConnsPerHost:     concurrency * 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	t.Cleanup(client.CloseIdleConnections)

	for _, scenario := range scenarios {
		report := runIAMLoadScenario(t, client, fixture.server.URL, scenario, concurrency, requests)
		logIAMLoadReport(t, report, concurrency)
	}
}

// loadScenarios builds the one piece of state the authorization path needs
// that the shared fixture does not create: a role holding a permission, and an
// entity to ask about. Everything else is already seeded.
func (f *iamTenantFixture) loadScenarios(t *testing.T) []iamLoadScenario {
	t.Helper()

	response := f.request(t, http.MethodPost, "/iam/roles", iamTenantA, f.tokenA, map[string]any{
		"name": "Load harness role", "description": "Role used by the HTTP load harness",
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	role := decodeEnvelope[core.APIRole](t, response)

	response = f.request(t, http.MethodPut, "/iam/roles/"+role.ID+"/permissions/entity_view", iamTenantA, f.tokenA, nil, nil)
	requireStatus(t, http.StatusOK, response)

	response = f.request(t, http.MethodPut, "/iam/roles/"+role.ID+"/members/"+f.adminA.ID.String(), iamTenantA, f.tokenA, nil, nil)
	requireStatus(t, http.StatusOK, response)

	response = f.request(t, http.MethodPost, "/iam/entities", iamTenantA, f.tokenA, map[string]any{
		"type": "company", "name": "Load harness company", "status": "active",
	}, nil)
	requireStatus(t, http.StatusCreated, response)
	entity := decodeEnvelope[core.APIEntity](t, response)

	check := map[string]any{
		"entity_id": entity.ID, "permission_code": "entity_view", "subject": f.adminA.ID.String(),
	}
	// The decision must be the allowed one. A load harness that hammers a
	// denial would measure the cheap path and report it as capacity.
	response = f.request(t, http.MethodPost, "/iam/authorization/check", iamTenantA, f.tokenA, check, nil)
	requireStatus(t, http.StatusOK, response)
	require.True(t, decodeEnvelope[core.AuthorizationDecision](t, response).Allowed,
		"the load harness subject must be allowed, or it measures the denial path")

	return []iamLoadScenario{
		{name: "authorization_check", method: http.MethodPost, path: "/iam/authorization/check", tenant: iamTenantA, token: f.tokenA, body: check, want: http.StatusOK},
		{name: "roles_list", method: http.MethodGet, path: "/iam/roles", tenant: iamTenantA, token: f.tokenA, want: http.StatusOK},
		{name: "entities_list", method: http.MethodGet, path: "/iam/entities", tenant: iamTenantA, token: f.tokenA, want: http.StatusOK},
		{name: "tenants_list", method: http.MethodGet, path: "/iam/tenants", tenant: iamTenantA, token: f.tokenA, want: http.StatusOK},
	}
}

func runIAMLoadScenario(
	t *testing.T,
	client *http.Client,
	baseURL string,
	scenario iamLoadScenario,
	concurrency, requests int,
) iamLoadReport {
	t.Helper()

	// Warm up through the same client: the first request pays TLS-less
	// connection setup, and counting that as request latency would put a
	// startup cost into p99 for the rest of the run.
	for range concurrency {
		require.Equal(t, scenario.want,
			iamLoadIssue(client, baseURL, scenario).status,
			"%s warm-up: check the path and its authorization before measuring it", scenario.name)
	}

	samples := make([][]iamLoadSample, concurrency)
	var wg sync.WaitGroup
	start := time.Now()
	for worker := range concurrency {
		wg.Go(func() {
			local := make([]iamLoadSample, 0, requests)
			for range requests {
				local = append(local, iamLoadIssue(client, baseURL, scenario))
			}
			samples[worker] = local
		})
	}
	wg.Wait()
	elapsed := time.Since(start)

	flat := make([]iamLoadSample, 0, concurrency*requests)
	for _, worker := range samples {
		flat = append(flat, worker...)
	}
	return iamLoadReport{scenario: scenario.name, samples: flat, elapsed: elapsed}
}

// iamLoadIssue takes no *testing.T: it runs on worker goroutines, and neither
// t.Helper nor a failing assertion may be called from there. Failures come back
// as errors for the test goroutine to assert on.
func iamLoadIssue(client *http.Client, baseURL string, scenario iamLoadScenario) iamLoadSample {
	var body io.Reader
	if scenario.body != nil {
		encoded, err := json.Marshal(scenario.body)
		if err != nil {
			return iamLoadSample{err: err}
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(scenario.method, baseURL+scenario.path, body)
	if err != nil {
		return iamLoadSample{err: err}
	}
	if scenario.body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if !scenario.tenant.Zero() {
		request.Header.Set(authz.TenantHeader, scenario.tenant.String())
	}
	request.Header.Set("Authorization", "Bearer "+scenario.token)

	began := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return iamLoadSample{latency: time.Since(began), err: err}
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	latency := time.Since(began)
	if readErr != nil {
		return iamLoadSample{latency: latency, err: readErr}
	}
	if closeErr != nil {
		return iamLoadSample{latency: latency, err: closeErr}
	}
	return iamLoadSample{status: response.StatusCode, latency: latency}
}

func logIAMLoadReport(t *testing.T, report iamLoadReport, concurrency int) {
	t.Helper()

	latencies := make([]time.Duration, 0, len(report.samples))
	statuses := map[int]int{}
	failures := 0
	for _, sample := range report.samples {
		if sample.err != nil {
			failures++
			continue
		}
		statuses[sample.status]++
		latencies = append(latencies, sample.latency)
		if sample.status >= http.StatusInternalServerError {
			failures++
		}
	}
	slices.Sort(latencies)

	throughput := float64(len(report.samples)) / report.elapsed.Seconds()
	t.Logf("load %s: concurrency=%d requests=%d elapsed=%s throughput=%.1f req/s failures=%d statuses=%v "+
		"p50=%s p95=%s p99=%s max=%s",
		report.scenario, concurrency, len(report.samples), report.elapsed.Round(time.Millisecond),
		throughput, failures, statuses,
		iamLoadPercentile(latencies, 0.50).Round(time.Microsecond),
		iamLoadPercentile(latencies, 0.95).Round(time.Microsecond),
		iamLoadPercentile(latencies, 0.99).Round(time.Microsecond),
		iamLoadPercentile(latencies, 1).Round(time.Microsecond),
	)

	assert.Zero(t, failures, "%s: transport failures and 5xx are always defects", report.scenario)
}

func iamLoadPercentile(sorted []time.Duration, fraction float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(fraction*float64(len(sorted)))) - 1
	return sorted[min(max(index, 0), len(sorted)-1)]
}

func iamLoadEnvInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	require.NoError(t, err, "%s must be an integer", name)
	require.Positive(t, value, "%s must be positive", name)
	return value
}
