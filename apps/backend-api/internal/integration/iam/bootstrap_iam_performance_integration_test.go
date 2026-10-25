package iamintegration

import (
	"7link.com/platform/internal/app"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	iamaudit "7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	iamPerformanceDatabaseType = "IAM_PERFORMANCE_DATABASE_TYPE"
	iamPerformanceDatabaseDSN  = "IAM_PERFORMANCE_DATABASE_DSN"
	iamPerformanceUpdate       = "IAM_PERFORMANCE_UPDATE"
	iamPerformanceEnforce      = "IAM_PERFORMANCE_ENFORCE_THRESHOLDS"
	iamPerformanceArtifact     = "testdata/iam_performance_baseline.json"
	iamPerformanceSamples      = 5
)

var (
	iamPerformanceStringLiteral = regexp.MustCompile(`'(?:''|[^'])*'`)
	iamPerformanceNumberLiteral = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
)

type iamPerformanceBaseline struct {
	SchemaVersion int                                     `json:"schema_version"`
	Workload      iamPerformanceWorkload                  `json:"workload"`
	Engines       map[string]iamPerformanceEngineBaseline `json:"engines"`
}

type iamPerformanceWorkload struct {
	Tenants              int `json:"tenants"`
	MembersPerTenant     int `json:"members_per_tenant"`
	RolesPerTenant       int `json:"roles_per_tenant"`
	Sessions             int `json:"sessions"`
	ResourcesPerTenant   int `json:"resources_per_tenant"`
	AuditEventsPerTenant int `json:"audit_events_per_tenant"`
	SCIMBulkOperations   int `json:"scim_bulk_operations"`
}

type iamPerformanceEngineBaseline struct {
	DatabaseVersion string                             `json:"database_version"`
	GoVersion       string                             `json:"go_version"`
	Host            string                             `json:"host"`
	CapturedAt      string                             `json:"captured_at"`
	Plans           []iamPerformancePlan               `json:"plans"`
	Benchmarks      map[string]iamPerformanceBenchmark `json:"benchmarks"`
}

type iamPerformancePlan struct {
	Path        string          `json:"path"`
	SQLTemplate string          `json:"sql_template"`
	Fingerprint string          `json:"fingerprint"`
	Plan        json.RawMessage `json:"plan"`
}

type iamPerformanceBenchmark struct {
	SamplesNSPerOp     []int64 `json:"samples_ns_per_op"`
	SamplesBytesPerOp  []int64 `json:"samples_bytes_per_op"`
	SamplesAllocsPerOp []int64 `json:"samples_allocs_per_op"`
	MedianNSPerOp      int64   `json:"median_ns_per_op"`
	ThresholdNSPerOp   int64   `json:"threshold_ns_per_op"`
}

type iamPerformancePaths struct {
	authorization func() error
	session       func() error
	scimBulk      func() error
	auditAppend   func() error
}

type iamPerformanceSQLCapture struct {
	mu      sync.Mutex
	path    string
	queries map[string][]string
}

// Only queries originating from a measured operation belong to its plan.
// The composed app also runs scheduler, sequencer and realtime queries.
type iamPerformanceContextKey struct{}

type iamPerformanceFullScan struct {
	table     string
	condition string
}

func (capture *iamPerformanceSQLCapture) LogMode(logger.LogLevel) logger.Interface { return capture }
func (*iamPerformanceSQLCapture) Info(context.Context, string, ...any)             {}
func (*iamPerformanceSQLCapture) Warn(context.Context, string, ...any)             {}
func (*iamPerformanceSQLCapture) Error(context.Context, string, ...any)            {}

func (capture *iamPerformanceSQLCapture) Trace(
	ctx context.Context,
	_ time.Time,
	sqlAndRows func() (string, int64),
	_ error,
) {
	if ctx.Value(iamPerformanceContextKey{}) != true {
		return
	}
	capture.mu.Lock()
	path := capture.path
	capture.mu.Unlock()
	if path == "" {
		return
	}
	statement, _ := sqlAndRows()
	statement = strings.TrimSpace(statement)
	if !iamPerformanceStatement(statement) {
		return
	}
	capture.mu.Lock()
	capture.queries[path] = append(capture.queries[path], statement)
	capture.mu.Unlock()
}

func (capture *iamPerformanceSQLCapture) during(path string, operation func() error) error {
	capture.mu.Lock()
	capture.path = path
	capture.mu.Unlock()
	err := operation()
	capture.mu.Lock()
	capture.path = ""
	capture.mu.Unlock()
	return err
}

func (capture *iamPerformanceSQLCapture) snapshot() map[string][]string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	result := make(map[string][]string, len(capture.queries))
	for path, statements := range capture.queries {
		seen := map[string]bool{}
		for _, statement := range statements {
			template := iamPerformanceSQLTemplate(statement)
			if !seen[template] {
				seen[template] = true
				result[path] = append(result[path], statement)
			}
		}
	}
	return result
}

func TestIAMPerformanceCaptureExcludesConcurrentBackgroundQueries(t *testing.T) {
	capture := &iamPerformanceSQLCapture{queries: make(map[string][]string)}
	ctx := context.WithValue(t.Context(), iamPerformanceContextKey{}, true)
	trace := func(ctx context.Context, sql string) {
		capture.Trace(ctx, time.Now(), func() (string, int64) { return sql, 1 }, nil)
	}
	require.NoError(t, capture.during("scim_bulk", func() error {
		done := make(chan struct{})
		go func() {
			defer close(done)
			trace(t.Context(), `SELECT * FROM "domain_event_cursors"`)
		}()
		nested, cancel := context.WithCancel(ctx)
		defer cancel()
		trace(nested, "SELECT measured_operation")
		<-done
		return nil
	}))
	trace(ctx, "SELECT outside_capture")
	require.Equal(t, map[string][]string{"scim_bulk": {"SELECT measured_operation"}}, capture.snapshot())
}

func TestIAMPerformanceExternalDatabase(t *testing.T) {
	databaseType := strings.TrimSpace(os.Getenv(iamPerformanceDatabaseType))
	dsn := strings.TrimSpace(os.Getenv(iamPerformanceDatabaseDSN))
	if databaseType == "" || dsn == "" {
		t.Skip("external IAM performance database is not configured")
	}
	require.Contains(t, []string{"postgres", "mysql"}, databaseType)

	fixture := newIAMTenantFixtureWithDatabase(t, app.Database{
		Type: databaseType, Dsn: dsn, Writable: true, Readable: true,
		MaxOpenConns: 8, MaxIdleConns: 8,
	}, "")
	workload := iamPerformanceWorkload{
		Tenants: 64, MembersPerTenant: 64, RolesPerTenant: 16, Sessions: 4096,
		ResourcesPerTenant: 64, AuditEventsPerTenant: 64, SCIMBulkOperations: 25,
	}
	seedIAMPerformanceWorkload(t, fixture, workload)
	paths := newIAMPerformancePaths(t, fixture, workload.SCIMBulkOperations)
	plans := captureIAMPerformancePlans(t, fixture, databaseType, paths)
	validateIAMPerformancePlans(t, databaseType, plans)
	originalLogger := fixture.db.Config.Logger
	fixture.db.Config.Logger = logger.Discard
	benchmarks := benchmarkIAMPerformancePaths(t, paths)
	fixture.db.Config.Logger = originalLogger
	version := iamPerformanceDatabaseVersion(t, fixture, databaseType)
	current := iamPerformanceEngineBaseline{
		DatabaseVersion: version,
		GoVersion:       runtime.Version(),
		Host:            runtime.GOOS + "/" + runtime.GOARCH,
		CapturedAt:      time.Now().UTC().Format(time.RFC3339),
		Plans:           plans,
		Benchmarks:      benchmarks,
	}
	baseline := readIAMPerformanceBaseline(t, os.Getenv(iamPerformanceUpdate) == "1")
	if os.Getenv(iamPerformanceUpdate) == "1" {
		baseline.SchemaVersion = 1
		baseline.Workload = workload
		if baseline.Engines == nil {
			baseline.Engines = make(map[string]iamPerformanceEngineBaseline)
		}
		baseline.Engines[databaseType] = current
		writeIAMPerformanceBaseline(t, baseline)
		t.Logf("updated %s %s baseline", databaseType, version)
		return
	}

	require.Equal(t, 1, baseline.SchemaVersion)
	require.Equal(t, workload, baseline.Workload)
	expected, ok := baseline.Engines[databaseType]
	require.True(t, ok, "missing checked-in %s performance baseline", databaseType)
	updatePlans := os.Getenv(iamPerformanceUpdate) == "plans"
	if !updatePlans {
		require.Equal(t, iamPerformancePlanSignatures(expected.Plans), iamPerformancePlanSignatures(current.Plans),
			"production SQL changed; review plans and regenerate with IAM_PERFORMANCE_UPDATE=plans")
		validateIAMPerformancePlans(t, databaseType, expected.Plans)
	}
	// Plan regeneration keeps the frozen samples and thresholds, so it has no
	// latency to vouch for; judging latency stays the explicit same-machine opt-in.
	if os.Getenv(iamPerformanceEnforce) == "1" {
		enforceIAMPerformanceThresholds(t, expected.Benchmarks, current.Benchmarks)
	}
	for name, benchmark := range current.Benchmarks {
		t.Logf("%s %s median=%dns/op threshold=%dns/op samples=%v", databaseType, name,
			benchmark.MedianNSPerOp, expected.Benchmarks[name].ThresholdNSPerOp, benchmark.SamplesNSPerOp)
	}
	if updatePlans {
		// SQL evolution must not silently reset the measured latency budget.
		expected.Plans = current.Plans
		baseline.Engines[databaseType] = expected
		writeIAMPerformanceBaseline(t, baseline)
		t.Logf("updated %s SQL plans; original benchmark samples and thresholds retained", databaseType)
	}
}

func TestIAMPerformancePlanRejectsUnscopedFullScans(t *testing.T) {
	postgresPlan := iamPerformancePlan{
		Path: "authorization", SQLTemplate: "SELECT * FROM iam_role_permissions",
		Plan: json.RawMessage(`[{"Plan":{"Node Type":"Seq Scan","Relation Name":"iam_role_permissions"}}]`),
	}
	mysqlPlan := iamPerformancePlan{
		Path: "scim_bulk", SQLTemplate: "SELECT * FROM iam_scim_resources",
		Plan: json.RawMessage(`{"query_block":{"table":{"table_name":"iam_scim_resources","access_type":"ALL"}}}`),
	}
	assert.ErrorContains(t, iamPerformancePlanError("postgres", postgresPlan), "unscoped full scan")
	assert.ErrorContains(t, iamPerformancePlanError("mysql", mysqlPlan), "unscoped full scan")
	postgresDirectoryPlan := iamPerformancePlan{
		Path: "scim_bulk", SQLTemplate: "SELECT * FROM iam_scim_directories",
		Plan: json.RawMessage(`[{"Plan":{"Node Type":"Seq Scan","Relation Name":"iam_scim_directories","Plan Width":23}}]`),
	}
	assert.ErrorContains(t, iamPerformancePlanError("postgres", postgresDirectoryPlan), "unscoped full scan")
	mysqlDirectoryPlan := iamPerformancePlan{
		Path: "scim_bulk", SQLTemplate: "SELECT * FROM iam_scim_directories AS directory",
		Plan: json.RawMessage(`{"query_block":{"table":{"table_name":"directory","access_type":"ALL","used_columns":["id","tenant_id"]}}}`),
	}
	assert.ErrorContains(t, iamPerformancePlanError("mysql", mysqlDirectoryPlan), "unscoped full scan")

	postgresPlan.SQLTemplate = "SELECT * FROM iam_groups WHERE tenant_id = ?"
	postgresPlan.Plan = json.RawMessage(`[{"Plan":{"Node Type":"Seq Scan","Relation Name":"iam_groups","Filter":"tenant_id = 1"}}]`)
	mysqlPlan.SQLTemplate = "SELECT * FROM iam_scim_directories WHERE id = ?"
	mysqlPlan.Plan = json.RawMessage(`{"query_block":{"table":{"table_name":"iam_scim_directories","access_type":"ALL","attached_condition":"id = 1"}}}`)
	assert.NoError(t, iamPerformancePlanError("postgres", postgresPlan))
	assert.NoError(t, iamPerformancePlanError("mysql", mysqlPlan))
	for _, condition := range []string{"", "tenant_id = 1", "owner_tenant_id = 1"} {
		postgresActor := iamPerformancePlan{Path: "scim_bulk", SQLTemplate: "SELECT * FROM iam_service_accounts"}
		postgresActor.Plan = json.RawMessage(fmt.Sprintf(`[{"Plan":{"Node Type":"Seq Scan","Relation Name":"iam_service_accounts","Filter":%q}}]`, condition))
		mysqlActor := iamPerformancePlan{Path: "scim_bulk", SQLTemplate: "SELECT * FROM iam_service_accounts AS service_account"}
		mysqlActor.Plan = json.RawMessage(fmt.Sprintf(`{"query_block":{"table":{"table_name":"service_account","access_type":"ALL","attached_condition":%q}}}`, condition))
		for engine, plan := range map[string]iamPerformancePlan{"postgres": postgresActor, "mysql": mysqlActor} {
			if condition == "owner_tenant_id = 1" {
				assert.NoError(t, iamPerformancePlanError(engine, plan))
			} else {
				assert.ErrorContains(t, iamPerformancePlanError(engine, plan), "unscoped full scan")
			}
		}
	}
}

func TestIAMPerformanceBaselineContract(t *testing.T) {
	baseline := readIAMPerformanceBaseline(t, false)
	require.Equal(t, 1, baseline.SchemaVersion)
	require.Len(t, baseline.Engines["postgres"].Benchmarks["authorization"].SamplesNSPerOp, iamPerformanceSamples)
	require.Equal(t, []string{"mysql", "postgres"}, sortedIAMPerformanceEngineNames(baseline.Engines))
	for engine, saved := range baseline.Engines {
		require.NotEmpty(t, saved.DatabaseVersion, engine)
		require.NotEmpty(t, saved.GoVersion, engine)
		require.NotEmpty(t, saved.Host, engine)
		require.NotEmpty(t, saved.CapturedAt, engine)
		validateIAMPerformancePlans(t, engine, saved.Plans)
		require.Equal(t,
			[]string{"audit_append", "authorization", "scim_bulk_25", "session_lookup"},
			sortedIAMPerformanceBenchmarkNames(saved.Benchmarks),
		)
		for name, benchmark := range saved.Benchmarks {
			require.Len(t, benchmark.SamplesNSPerOp, iamPerformanceSamples, engine+"/"+name)
			require.Len(t, benchmark.SamplesBytesPerOp, iamPerformanceSamples, engine+"/"+name)
			require.Len(t, benchmark.SamplesAllocsPerOp, iamPerformanceSamples, engine+"/"+name)
			ordered := append([]int64(nil), benchmark.SamplesNSPerOp...)
			sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
			require.Equal(t, ordered[len(ordered)/2], benchmark.MedianNSPerOp, engine+"/"+name)
			require.GreaterOrEqual(t, benchmark.ThresholdNSPerOp, ordered[len(ordered)-1], engine+"/"+name)
		}
	}
}

func TestIAMPerformanceThresholdJudgesTheMedian(t *testing.T) {
	expected := map[string]iamPerformanceBenchmark{"path": {ThresholdNSPerOp: 1_000}}
	judge := func(samples ...int64) []string {
		return iamPerformanceThresholdViolations(expected, map[string]iamPerformanceBenchmark{"path": {SamplesNSPerOp: samples}})
	}
	assert.Empty(t, judge(300, 310, 3_000, 320, 305), "one outlier must not fail the gate")
	assert.Empty(t, judge(1_000, 900, 5_000, 4_000, 800), "a median at the threshold passes")
	violations := judge(900, 1_001, 5_000, 1_200, 800)
	require.Len(t, violations, 1)
	assert.Contains(t, violations[0], "path median 1001ns/op exceeded the same-machine frozen threshold 1000ns/op")
	assert.Equal(t, []string{"path has no frozen threshold"},
		iamPerformanceThresholdViolations(map[string]iamPerformanceBenchmark{}, map[string]iamPerformanceBenchmark{"path": {SamplesNSPerOp: []int64{1}}}))
	assert.Equal(t, []string{"path has no samples"},
		iamPerformanceThresholdViolations(expected, map[string]iamPerformanceBenchmark{"path": {}}))
}

func newIAMPerformancePaths(t *testing.T, fixture *iamTenantFixture, bulkOperations int) iamPerformancePaths {
	t.Helper()
	ctx := context.WithValue(t.Context(), iamPerformanceContextKey{}, true)
	now := time.Now().UTC().Truncate(time.Millisecond)
	roleID := guid.ID(6_500_001)
	require.NoError(t, fixture.db.Table(fixture.table("iam_roles")).Create(map[string]any{
		"tenant_id": iamTenantA, "id": roleID, "name": "Performance role", "name_key": "performance-role",
		"description": "", "created_at": now, "updated_at": now,
	}).Error)
	require.NoError(t, fixture.db.Table(fixture.table("iam_role_permissions")).Create(map[string]any{
		"tenant_id": iamTenantA, "role_id": roleID, "permission_code": "entity_view",
		"condition_json": "", "created_at": now,
	}).Error)
	require.NoError(t, fixture.db.Table(fixture.table("iam_role_members")).Create(map[string]any{
		"tenant_id": iamTenantA, "role_id": roleID, "principal_id": fixture.adminA.ID, "created_at": now,
	}).Error)

	services := fixture.app.Modules().Services()
	ids, err := module.Resolve[guid.Source](services)
	require.NoError(t, err)
	appender, err := module.Resolve[iamaudit.Appender](services)
	require.NoError(t, err)
	identities, err := module.Resolve[identity.IdentityProvisioner](services)
	require.NoError(t, err)
	groups, err := module.Resolve[organization.GroupProvisioner](services)
	require.NoError(t, err)
	actors, err := module.Resolve[identity.ServiceAccountValidator](services)
	require.NoError(t, err)
	fields, err := module.Resolve[core.AuthorizationEvaluator](services)
	require.NoError(t, err)
	scim := provisioning.NewService(fixture.app.Runtime(), appender, ids, identities, groups, actors, fields, provisioning.Config{}, nil, httpx.Policy{})
	// A service built outside the module never went through injection, so it
	// binds the owners' published names itself.
	scimPrefix := fixture.app.Runtime().TablePrefix()
	require.NoError(t, scim.BindForeignTables(
		core.PublishedAccessTableNames(scimPrefix), identity.PublishedPrincipalTableNames(scimPrefix),
		organization.PublishedDirectoryTableNames(scimPrefix),
	))
	response := fixture.request(t, http.MethodPost, "/service-accounts", iamTenantA, fixture.tokenA,
		map[string]any{"login_name": "performance-scim-actor", "display_name": "Performance SCIM actor"}, nil)
	requireStatus(t, http.StatusCreated, response)
	account := decodeEnvelope[identity.ServiceAccount](t, response)
	directory, err := scim.CreateDirectory(ctx, iamTenantA, "Performance directory", &account.ID)
	require.NoError(t, err)
	credential, err := scim.CreateCredential(ctx, iamTenantA, directory.ID, "Performance credential", nil)
	require.NoError(t, err)
	scimAuth, err := scim.Authenticate(ctx, "Bearer "+credential.Token)
	require.NoError(t, err)
	auditService := iamaudit.NewService(fixture.app.Runtime(), ids)
	authorizer := newBoundIAMAuthorizer(t, fixture.app.Runtime())
	authnModule := requireIAMModule[*iamauthn.Module](t, fixture, iamauthn.Name)
	sessionToken, err := authnModule.CreateSession(ctx, fixture.adminA.ID, now, authn.Assurance{
		AuthTime: now, Level: 1, Methods: []string{"pwd"},
	})
	require.NoError(t, err)
	sessionCookie := authnModule.SessionCookie(sessionToken)

	operations := make([]provisioning.BulkOperation, bulkOperations)
	missingID := guid.ID(8_900_000_000_000_001).String()
	for index := range operations {
		operations[index] = provisioning.BulkOperation{Method: "DELETE", Path: "/Users/" + missingID}
	}
	bulkRequest := provisioning.BulkRequest{
		Schemas: []string{provisioning.BulkSchema}, Operations: operations,
	}
	return iamPerformancePaths{
		authorization: func() error {
			allowed, err := authorizer.CheckBulk(ctx, iamTenantA, []string{"entity_view", "menu_view"}, fixture.adminA.ID)
			if err == nil && !allowed["entity_view"] {
				return errors.New("performance authorization fixture was denied")
			}
			return err
		},
		session: func() error {
			_, err := authnModule.Authenticate(ctx, "", sessionCookie)
			return err
		},
		scimBulk: func() error {
			response, err := scim.Bulk(ctx, scimAuth, bulkRequest)
			if err != nil {
				return err
			}
			if len(response.Operations) != bulkOperations {
				return fmt.Errorf("SCIM bulk returned %d operations, want %d", len(response.Operations), bulkOperations)
			}
			for _, operation := range response.Operations {
				if operation.Status != "404" {
					return fmt.Errorf("SCIM bulk operation returned %s, want 404", operation.Status)
				}
			}
			return nil
		},
		auditAppend: func() error {
			_, err := auditService.Append(ctx, iamaudit.EventInput{
				TenantID: iamTenantA, EventType: "performance_probe", Outcome: "success",
			})
			return err
		},
	}
}

func captureIAMPerformancePlans(
	t *testing.T,
	fixture *iamTenantFixture,
	databaseType string,
	paths iamPerformancePaths,
) []iamPerformancePlan {
	t.Helper()
	capture := &iamPerformanceSQLCapture{queries: make(map[string][]string)}
	database := fixture.db
	originalLogger := database.Config.Logger
	database.Config.Logger = capture
	for _, path := range []struct {
		name      string
		operation func() error
	}{
		{name: "authorization", operation: paths.authorization},
		{name: "session_lookup", operation: paths.session},
		{name: "scim_bulk", operation: paths.scimBulk},
		{name: "audit_append", operation: paths.auditAppend},
	} {
		require.NoError(t, capture.during(path.name, path.operation), path.name)
	}
	database.Config.Logger = originalLogger

	queries := capture.snapshot()
	plans := make([]iamPerformancePlan, 0)
	for _, path := range []string{"authorization", "session_lookup", "scim_bulk", "audit_append"} {
		require.NotEmpty(t, queries[path], "%s emitted no SQL", path)
		for _, statement := range queries[path] {
			template := iamPerformanceSQLTemplate(statement)
			digest := sha256.Sum256([]byte(template))
			plans = append(plans, iamPerformancePlan{
				Path: path, SQLTemplate: template, Fingerprint: hex.EncodeToString(digest[:]),
				Plan: explainIAMPerformanceSQL(t, databaseType, database, statement),
			})
		}
	}
	sort.Slice(plans, func(i, j int) bool {
		if plans[i].Path == plans[j].Path {
			return plans[i].Fingerprint < plans[j].Fingerprint
		}
		return plans[i].Path < plans[j].Path
	})
	return plans
}

func benchmarkIAMPerformancePaths(t *testing.T, paths iamPerformancePaths) map[string]iamPerformanceBenchmark {
	t.Helper()
	result := make(map[string]iamPerformanceBenchmark, 4)
	for _, path := range []struct {
		name      string
		operation func() error
	}{
		{name: "authorization", operation: paths.authorization},
		{name: "session_lookup", operation: paths.session},
		{name: "scim_bulk_25", operation: paths.scimBulk},
		{name: "audit_append", operation: paths.auditAppend},
	} {
		benchmark := iamPerformanceBenchmark{}
		for range iamPerformanceSamples {
			var operationErr error
			sample := testing.Benchmark(func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if err := path.operation(); err != nil {
						operationErr = err
						b.StopTimer()
						return
					}
				}
			})
			require.NoError(t, operationErr, path.name)
			benchmark.SamplesNSPerOp = append(benchmark.SamplesNSPerOp, sample.NsPerOp())
			benchmark.SamplesBytesPerOp = append(benchmark.SamplesBytesPerOp, sample.AllocedBytesPerOp())
			benchmark.SamplesAllocsPerOp = append(benchmark.SamplesAllocsPerOp, sample.AllocsPerOp())
		}
		ordered := append([]int64(nil), benchmark.SamplesNSPerOp...)
		sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
		benchmark.MedianNSPerOp = ordered[len(ordered)/2]
		benchmark.ThresholdNSPerOp = roundedIAMPerformanceThreshold(ordered[len(ordered)-1] * 3)
		result[path.name] = benchmark
	}
	return result
}

func seedIAMPerformanceWorkload(t *testing.T, fixture *iamTenantFixture, workload iamPerformanceWorkload) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	type tenantRow struct {
		ID                   guid.ID
		Slug, Name, Status   string
		Version              int64
		CreatedAt, UpdatedAt time.Time
	}
	type principalRow struct {
		ID                                    guid.ID
		LoginName, Email, DisplayName, Status string
		EmailVerified, ActivationRequired     bool
		CreatedAt, UpdatedAt                  time.Time
	}
	type memberRow struct {
		TenantID, PrincipalID      guid.ID
		DisplayName, Email, Status string
		CreatedAt, UpdatedAt       time.Time
	}
	type roleRow struct {
		TenantID, ID               guid.ID
		Name, NameKey, Description string
		CreatedAt, UpdatedAt       time.Time
	}
	type permissionRow struct {
		TenantID, RoleID              guid.ID
		PermissionCode, ConditionJSON string
		CreatedAt                     time.Time
	}
	type roleMemberRow struct {
		TenantID, RoleID, PrincipalID guid.ID
		CreatedAt                     time.Time
	}
	type sessionRow struct {
		IDHash                                   string
		PrincipalID                              guid.ID
		CreatedAt, AuthTime                      time.Time
		ACR                                      int
		AMR                                      string
		LastSeenAt, ExpiresAt, AbsoluteExpiresAt time.Time
		RevokedAt                                *time.Time
		IPAddress, UserAgent                     string
	}
	type directoryRow struct {
		ID, TenantID          guid.ID
		Name, NameKey, Status string
		Version               int64
		CreatedAt, UpdatedAt  time.Time
	}
	type resourceRow struct {
		DirectoryID             guid.ID
		ResourceType            string
		ResourceID              guid.ID
		ExternalID, ExternalKey string
		Version                 int64
		CreatedAt, UpdatedAt    time.Time
		DeletedAt               *time.Time
	}
	type auditEventRow struct {
		ID, TenantID                          guid.ID
		PrincipalID                           *guid.ID
		EventType, TargetType                 string
		TargetID                              *guid.ID
		Outcome, IPAddress, UserAgent, Detail string
		CreatedAt                             time.Time
		Sequence                              int64
		PreviousHash, EventHash               string
	}
	type auditHeadRow struct {
		TenantID  guid.ID
		Sequence  int64
		EventHash string
		UpdatedAt time.Time
	}

	tenants := make([]tenantRow, 0, workload.Tenants)
	principals := make([]principalRow, 0, workload.Tenants*workload.MembersPerTenant)
	members := make([]memberRow, 0, workload.Tenants*workload.MembersPerTenant)
	roles := make([]roleRow, 0, workload.Tenants*workload.RolesPerTenant)
	permissions := make([]permissionRow, 0, cap(roles))
	roleMembers := make([]roleMemberRow, 0, cap(roles))
	directories := make([]directoryRow, 0, workload.Tenants)
	resources := make([]resourceRow, 0, workload.Tenants*workload.ResourcesPerTenant)
	auditEvents := make([]auditEventRow, 0, workload.Tenants*workload.AuditEventsPerTenant)
	auditHeads := make([]auditHeadRow, 0, workload.Tenants)
	for tenantIndex := range workload.Tenants {
		tenantID := guid.ID(7_000_000 + tenantIndex)
		principalID := guid.ID(8_000_000 + tenantIndex*workload.MembersPerTenant)
		directoryID := guid.ID(9_000_000 + tenantIndex)
		tenants = append(tenants, tenantRow{ID: tenantID, Slug: fmt.Sprintf("perf-%03d", tenantIndex), Name: "Performance", Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now})
		for memberIndex := range workload.MembersPerTenant {
			memberID := guid.ID(8_000_000 + tenantIndex*workload.MembersPerTenant + memberIndex)
			login := fmt.Sprintf("perf-%03d-%03d", tenantIndex, memberIndex)
			email := login + "@example.test"
			principals = append(principals, principalRow{ID: memberID, LoginName: login, Email: email, DisplayName: "Performance", Status: "active", EmailVerified: true, CreatedAt: now, UpdatedAt: now})
			members = append(members, memberRow{TenantID: tenantID, PrincipalID: memberID, DisplayName: "Performance", Email: email, Status: "active", CreatedAt: now, UpdatedAt: now})
		}
		directories = append(directories, directoryRow{ID: directoryID, TenantID: tenantID, Name: "Performance", NameKey: "performance", Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now})
		for roleIndex := range workload.RolesPerTenant {
			roleID := guid.ID(10_000_000 + tenantIndex*workload.RolesPerTenant + roleIndex)
			roles = append(roles, roleRow{TenantID: tenantID, ID: roleID, Name: fmt.Sprintf("Role %03d", roleIndex), NameKey: fmt.Sprintf("role-%03d", roleIndex), CreatedAt: now, UpdatedAt: now})
			permissions = append(permissions, permissionRow{TenantID: tenantID, RoleID: roleID, PermissionCode: "entity_view", CreatedAt: now})
			roleMembers = append(roleMembers, roleMemberRow{TenantID: tenantID, RoleID: roleID, PrincipalID: principalID, CreatedAt: now})
		}
		for resourceIndex := range workload.ResourcesPerTenant {
			resourceID := guid.ID(20_000_000 + tenantIndex*workload.ResourcesPerTenant + resourceIndex)
			key := fmt.Sprintf("id:%d", resourceID)
			resources = append(resources, resourceRow{DirectoryID: directoryID, ResourceType: "User", ResourceID: resourceID, ExternalKey: key, Version: 1, CreatedAt: now, UpdatedAt: now})
		}
		previousHash := ""
		for eventIndex := range workload.AuditEventsPerTenant {
			eventID := guid.ID(30_000_000 + tenantIndex*workload.AuditEventsPerTenant + eventIndex)
			eventHash := fmt.Sprintf("%064x", int64(eventID))
			auditEvents = append(auditEvents, auditEventRow{ID: eventID, TenantID: tenantID, EventType: "performance_seed", Outcome: "success", Detail: "{}", CreatedAt: now, Sequence: int64(eventIndex + 1), PreviousHash: previousHash, EventHash: eventHash})
			previousHash = eventHash
		}
		auditHeads = append(auditHeads, auditHeadRow{TenantID: tenantID, Sequence: int64(workload.AuditEventsPerTenant), EventHash: previousHash, UpdatedAt: now})
	}
	sessions := make([]sessionRow, 0, workload.Sessions)
	for index := range workload.Sessions {
		sessions = append(sessions, sessionRow{
			IDHash: fmt.Sprintf("%064x", index+1), PrincipalID: fixture.adminA.ID,
			CreatedAt: now, AuthTime: now, ACR: 1, AMR: "pwd", LastSeenAt: now,
			ExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(8 * time.Hour),
		})
	}

	for _, batch := range []struct {
		table string
		rows  any
	}{
		{table: "iam_tenants", rows: &tenants},
		{table: "iam_principals", rows: &principals},
		{table: "iam_tenant_members", rows: &members},
		{table: "iam_roles", rows: &roles},
		{table: "iam_role_permissions", rows: &permissions},
		{table: "iam_role_members", rows: &roleMembers},
		{table: "iam_sessions", rows: &sessions},
		{table: "iam_scim_directories", rows: &directories},
		{table: "iam_scim_resources", rows: &resources},
		{table: "iam_audit_events", rows: &auditEvents},
		{table: "iam_audit_heads", rows: &auditHeads},
	} {
		require.NoError(t, fixture.db.Table(fixture.table(batch.table)).CreateInBatches(batch.rows, 250).Error, batch.table)
	}
	if fixture.db.Dialector.Name() == "postgres" {
		require.NoError(t, fixture.db.Exec("ANALYZE").Error)
	} else {
		for _, table := range []string{"iam_tenants", "iam_principals", "iam_tenant_members", "iam_roles", "iam_role_permissions", "iam_role_members", "iam_sessions", "iam_scim_directories", "iam_scim_resources", "iam_audit_events", "iam_audit_heads"} {
			require.NoError(t, fixture.db.Exec("ANALYZE TABLE "+fixture.table(table)).Error, table)
		}
	}
}

func explainIAMPerformanceSQL(t *testing.T, databaseType string, database interface {
	Raw(string, ...any) *gorm.DB
}, statement string) json.RawMessage {
	t.Helper()
	prefix := "EXPLAIN (FORMAT JSON) "
	if databaseType == "mysql" {
		prefix = "EXPLAIN FORMAT=JSON "
	}
	var raw []byte
	require.NoError(t, database.Raw(prefix+statement).Row().Scan(&raw), statement)
	require.True(t, json.Valid(raw), "invalid %s JSON EXPLAIN: %s", databaseType, raw)
	return append(json.RawMessage(nil), raw...)
}

func iamPerformanceDatabaseVersion(t *testing.T, fixture *iamTenantFixture, databaseType string) string {
	t.Helper()
	query := "SHOW server_version"
	if databaseType == "mysql" {
		query = "SELECT VERSION()"
	}
	var version string
	require.NoError(t, fixture.db.Raw(query).Scan(&version).Error)
	if databaseType == "mysql" {
		require.Regexp(t, `^8\.4\.`, version, "performance MySQL plans must come from the pinned real MySQL 8.4 gate")
	}
	return version
}

func validateIAMPerformancePlans(t *testing.T, databaseType string, plans []iamPerformancePlan) {
	t.Helper()
	require.NotEmpty(t, plans)
	paths := map[string]bool{}
	for _, plan := range plans {
		paths[plan.Path] = true
		require.NoError(t, iamPerformancePlanError(databaseType, plan), "%s: %s", plan.Path, plan.SQLTemplate)
	}
	require.Equal(t, map[string]bool{
		"authorization": true, "session_lookup": true, "scim_bulk": true, "audit_append": true,
	}, paths)
}

func iamPerformancePlanError(databaseType string, plan iamPerformancePlan) error {
	if !json.Valid(plan.Plan) {
		return errors.New("invalid JSON plan")
	}
	if plan.Fingerprint != "" {
		digest := sha256.Sum256([]byte(plan.SQLTemplate))
		if actual := hex.EncodeToString(digest[:]); actual != plan.Fingerprint {
			return fmt.Errorf("SQL fingerprint mismatch: got %s, want %s", plan.Fingerprint, actual)
		}
	}
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(plan.SQLTemplate)), "INSERT ") {
		return nil
	}
	fullScans := iamPerformanceFullScans(databaseType, plan.Plan)
	for _, scan := range fullScans {
		if plan.Path == "scim_bulk" {
			// MySQL reports the aliases used by the captured SCIM queries.
			switch scan.table {
			case "credential":
				scan.table = "iam_scim_credentials"
			case "directory":
				scan.table = "iam_scim_directories"
			case "tenant":
				scan.table = "iam_tenants"
			case "resource":
				scan.table = "iam_scim_resources"
			case "principal":
				scan.table = "iam_principals"
			case "member":
				scan.table = "iam_tenant_members"
			case "service_account":
				scan.table = "iam_service_accounts"
			}
		}
		scope := iamPerformanceScopeColumn(scan.table)
		if scope == "" {
			continue
		}
		// Tenant slugs have a global UNIQUE constraint on every supported engine.
		// Small root tables may still use a sequential scan for this single-row lookup.
		if strings.TrimPrefix(strings.ToLower(scan.table), "sl_") == "iam_tenants" &&
			!regexp.MustCompile(`(?i)\b(?:or|not)\b`).MatchString(scan.condition) &&
			regexp.MustCompile("(?i)(?:^|\\bAND\\s+)[\\s(]*(?:[\"`]?[a-z_][a-z0-9_]*[\"`]?\\.)?[\"`]?slug[\"`]?(?:\\)|::text)*\\s*=\\s*'[^']*'").MatchString(scan.condition) {
			continue
		}
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(scope) + `\b`).MatchString(strings.ToLower(scan.condition)) {
			return fmt.Errorf("unscoped full scan of %s in %s", scan.table, plan.Path)
		}
		if iamPerformanceRequiresIndex(plan.Path, scan.table) {
			return fmt.Errorf("critical full scan of %s in %s", scan.table, plan.Path)
		}
	}
	return nil
}

func TestIAMPerformancePlanAllowsOnlyUniqueTenantSlugEquality(t *testing.T) {
	for _, tc := range []struct {
		filter  string
		allowed bool
	}{
		{`((slug)::text = '__system__'::text)`, true},
		{`tenant.slug = '__system__'`, true},
		{`(status = 'active')`, false},
		{`(slug ~~ '%system%')`, false},
		{`(slug <> '__system__')`, false},
		{`(slug = other_slug)`, false},
		{`(display_slug = '__system__')`, false},
		{`(slug = '__system__' OR status = 'active')`, false},
		{`NOT (slug = '__system__')`, false},
		{`lower(slug) = '__system__'`, false},
		{`slug::citext = '__system__'`, false},
	} {
		encoded, err := json.Marshal([]any{map[string]any{"Plan": map[string]any{"Node Type": "Seq Scan", "Relation Name": "iam_tenants", "Filter": tc.filter}}})
		require.NoError(t, err)
		err = iamPerformancePlanError("postgres", iamPerformancePlan{Path: "authorization", SQLTemplate: "SELECT id FROM iam_tenants", Plan: encoded})
		if tc.allowed {
			require.NoError(t, err, tc.filter)
		} else {
			require.ErrorContains(t, err, "unscoped full scan", tc.filter)
		}
	}
	encoded, err := json.Marshal([]any{map[string]any{"Plan": map[string]any{"Node Type": "Seq Scan", "Relation Name": "iam_role_permissions", "Filter": `slug = '__system__'`}}})
	require.NoError(t, err)
	require.ErrorContains(t, iamPerformancePlanError("postgres", iamPerformancePlan{Path: "authorization", SQLTemplate: "SELECT * FROM iam_role_permissions", Plan: encoded}), "unscoped full scan")
}

func iamPerformanceRequiresIndex(path, table string) bool {
	table = strings.TrimPrefix(strings.ToLower(table), "sl_")
	switch path {
	case "authorization":
		return table == "iam_role_members" || table == "iam_role_permissions" || table == "iam_tenant_members"
	case "session_lookup":
		return table == "iam_sessions"
	case "scim_bulk":
		return table == "iam_scim_resources" || table == "iam_tenant_members"
	default:
		return false
	}
}

func iamPerformanceFullScans(databaseType string, raw json.RawMessage) []iamPerformanceFullScan {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	set := map[string]iamPerformanceFullScan{}
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			if databaseType == "postgres" && typed["Node Type"] == "Seq Scan" {
				if table, ok := typed["Relation Name"].(string); ok {
					filter, _ := typed["Filter"].(string)
					set[table+filter] = iamPerformanceFullScan{table: table, condition: filter}
				}
			}
			if databaseType == "mysql" {
				if table, ok := typed["table"].(map[string]any); ok && table["access_type"] == "ALL" {
					if name, ok := table["table_name"].(string); ok && !strings.HasPrefix(name, "<") {
						filter, _ := table["attached_condition"].(string)
						set[name+filter] = iamPerformanceFullScan{table: name, condition: filter}
					}
				}
			}
			for _, item := range typed {
				walk(item)
			}
		}
	}
	walk(value)
	result := make([]iamPerformanceFullScan, 0, len(set))
	for _, scan := range set {
		result = append(result, scan)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].table == result[j].table {
			return result[i].condition < result[j].condition
		}
		return result[i].table < result[j].table
	})
	return result
}

func iamPerformanceScopeColumn(table string) string {
	table = strings.TrimPrefix(strings.ToLower(table), "sl_")
	switch table {
	case "iam_sessions":
		return "id_hash"
	case "iam_principals", "iam_tenants":
		return "id"
	case "iam_credentials":
		return "principal_id"
	case "iam_service_accounts":
		return "owner_tenant_id"
	case "iam_platform_administrators", "iam_platform_grants":
		return ""
	case "iam_scim_credentials":
		return "id"
	case "iam_scim_resources":
		return "directory_id"
	case "iam_scim_directories":
		return "id"
	case "iam_audit_events", "iam_audit_heads":
		return "tenant_id"
	default:
		if strings.HasPrefix(table, "iam_") {
			return "tenant_id"
		}
		return ""
	}
}

func iamPerformanceStatement(statement string) bool {
	upper := strings.ToUpper(strings.TrimSpace(statement))
	for _, prefix := range []string{"SELECT ", "WITH ", "INSERT ", "UPDATE ", "DELETE "} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

func iamPerformanceSQLTemplate(statement string) string {
	statement = iamPerformanceStringLiteral.ReplaceAllString(statement, "?")
	statement = iamPerformanceNumberLiteral.ReplaceAllString(statement, "?")
	return strings.Join(strings.Fields(statement), " ")
}

func iamPerformancePlanSignatures(plans []iamPerformancePlan) []string {
	result := make([]string, 0, len(plans))
	for _, plan := range plans {
		result = append(result, plan.Path+"|"+plan.Fingerprint)
	}
	sort.Strings(result)
	return result
}

func roundedIAMPerformanceThreshold(value int64) int64 {
	const quantum = int64(time.Millisecond)
	if value < quantum {
		quantum := int64(100 * time.Microsecond)
		return ((value + quantum - 1) / quantum) * quantum
	}
	return ((value + quantum - 1) / quantum) * quantum
}

func enforceIAMPerformanceThresholds(
	t *testing.T,
	expected map[string]iamPerformanceBenchmark,
	current map[string]iamPerformanceBenchmark,
) {
	t.Helper()
	require.Equal(t, sortedIAMPerformanceBenchmarkNames(expected), sortedIAMPerformanceBenchmarkNames(current))
	for _, violation := range iamPerformanceThresholdViolations(expected, current) {
		assert.Fail(t, violation)
	}
}

// iamPerformanceThresholdViolations judges each path by the median of its
// samples, so one scheduler stall in five samples cannot fail the gate while a
// regression that moves the typical run still does.
func iamPerformanceThresholdViolations(
	expected map[string]iamPerformanceBenchmark,
	current map[string]iamPerformanceBenchmark,
) []string {
	violations := make([]string, 0)
	for _, name := range sortedIAMPerformanceBenchmarkNames(current) {
		threshold := expected[name].ThresholdNSPerOp
		if threshold <= 0 {
			violations = append(violations, fmt.Sprintf("%s has no frozen threshold", name))
			continue
		}
		samples := current[name].SamplesNSPerOp
		if len(samples) == 0 {
			violations = append(violations, fmt.Sprintf("%s has no samples", name))
			continue
		}
		ordered := append([]int64(nil), samples...)
		sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
		if median := ordered[len(ordered)/2]; median > threshold {
			violations = append(violations, fmt.Sprintf(
				"%s median %dns/op exceeded the same-machine frozen threshold %dns/op (samples %v)",
				name, median, threshold, samples))
		}
	}
	return violations
}

func sortedIAMPerformanceBenchmarkNames(values map[string]iamPerformanceBenchmark) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedIAMPerformanceEngineNames(values map[string]iamPerformanceEngineBaseline) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func readIAMPerformanceBaseline(t *testing.T, allowMissing bool) iamPerformanceBaseline {
	t.Helper()
	data, err := os.ReadFile(iamPerformanceArtifact)
	if allowMissing && errors.Is(err, os.ErrNotExist) {
		return iamPerformanceBaseline{}
	}
	require.NoError(t, err)
	var baseline iamPerformanceBaseline
	require.NoError(t, json.Unmarshal(data, &baseline))
	return baseline
}

func writeIAMPerformanceBaseline(t *testing.T, baseline iamPerformanceBaseline) {
	t.Helper()
	data, err := json.MarshalIndent(baseline, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')
	require.NoError(t, os.WriteFile(iamPerformanceArtifact, data, 0o644))
}
