package execution

import (
	"7link.com/platform/internal/infra/idempotency"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	gormx "7link.com/platform/pkg/extension/gorm"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExecutionHTTPReadKeepsPendingInteractionAndRejectsCorruptedPlan(t *testing.T) {
	f, v, input := approvalFixture(t)
	a := requestApproval(t, f, v, input)
	out, e := f.m.getHTTP(f.human, &ExecutionInput{ExecutionID: v.ID.String()})
	require.NoError(t, e)
	require.Len(t, out.Body.Data.Approvals, 1)
	require.Equal(t, a.ID, out.Body.Data.Approvals[0].ID)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("plan_json", "{").Error)
	_, e = f.m.getHTTP(f.human, &ExecutionInput{ExecutionID: v.ID.String()})
	require.Error(t, e)
	_, e = f.m.listHTTP(f.human, &listInput{ProjectID: "9", Limit: 50})
	require.Error(t, e)
}

func TestExecutionListAndControlRejectInvalidCursorLimitsAndVersions(t *testing.T) {
	f := executionHarness(t)
	for _, in := range []listInput{{ProjectID: "9", Cursor: "bad", Limit: 50}, {ProjectID: "9", Limit: 0}, {ProjectID: "9", Limit: 101}} {
		_, e := f.m.listHTTP(f.human, &in)
		require.Error(t, e)
	}
	for _, version := range []string{"bad", "0", "-1"} {
		_, e := f.m.cancelHTTP(f.human, &controlInput{ExecutionInput: ExecutionInput{ExecutionID: "1"}, IfMatch: version})
		require.Error(t, e)
	}
	f.m.runtime = nil
	_, e := f.m.createHTTP(f.human, &createInput{Body: validCreate()})
	require.Error(t, e)
	_, e = f.m.pullHTTP(f.human, &pullInput{})
	require.Error(t, e)
}

type executionHTTPAuthority struct{}

func TestExecutionRunnerHTTPRequiresTrustedMachineTenantBinding(t *testing.T) {
	for _, endpoint := range []string{"/runner/execution-pulls", "/executions/100/receipts"} {
		for _, unavailable := range []bool{false, true} {
			t.Run(endpoint+strconv.FormatBool(unavailable), func(t *testing.T) {
				f, handler := executionRoutesFixture(t)
				f.m.machines.BindRunnerContext = nil
				status := http.StatusServiceUnavailable
				if !unavailable {
					f.m.machines.BindRunnerContext = func(context.Context, guid.ID) (context.Context, error) { return nil, fleet.ErrMachineForbidden }
					status = http.StatusForbidden
				}
				var body any = f.fence
				if strings.Contains(endpoint, "receipts") {
					body = map[string]any{"fence": f.fence, "receipt": ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"}}
				}
				response := executionRouteRequest(t, handler, http.MethodPost, endpoint, body, 0, true)
				require.Equal(t, status, response.Code, response.Body.String())
			})
		}
	}
}

func (executionHTTPAuthority) Authenticate(_ context.Context, authorization, _ string) (*authn.Claims, error) {
	principal := guid.ID(11)
	if authorization == "Bearer runner" {
		return &authn.Claims{PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount, OrganizationID: "external-not-a-tenant"}, nil
	}
	return &authn.Claims{TenantID: 7, PrincipalID: principal, SubjectType: authn.SubjectTypePrincipal}, nil
}

func executionRoutesFixture(t *testing.T) (*executionFixture, http.Handler) {
	t.Helper()
	f := executionHarness(t)
	var database struct{ File string }
	require.NoError(t, f.db.Raw("PRAGMA database_list").Scan(&database).Error)
	datasources, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: database.File, Readable: true, Writable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, datasources.Close()) })
	f.m.runtime = appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("ex_"), appenv.WithDatabase(datasources))
	f.m.registrar = executionHTTPRegistrar(t)
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	require.NoError(t, f.m.RegisterHTTP(t.Context(), api))
	return f, router
}

func executionRouteRequest(t *testing.T, handler http.Handler, method, path string, body any, version int, runner bool) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, strings.NewReader(string(encoded)))
	request.Header.Set("Content-Type", "application/json")
	if version > 0 {
		request.Header.Set("If-Match", `"`+strconv.Itoa(version)+`"`)
	}
	if runner {
		request.Header.Set("Authorization", "Bearer runner")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func executionRouteData[T any](t *testing.T, response *httptest.ResponseRecorder, status int) T {
	t.Helper()
	require.Equal(t, status, response.Code, response.Body.String())
	var envelope struct {
		Data T `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	return envelope.Data
}

func TestExecutionHTTPRunnerLifecycleAndAtomicRollback(t *testing.T) {
	f, handler := executionRoutesFixture(t)
	in := validCreate()
	in.Plan.WorkdirID = 5
	f.pub.fail = true
	response := executionRouteRequest(t, handler, http.MethodPost, "/tenants/7/executions", in, 0, false)
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	for _, table := range []string{f.m.names.Executions.String(), "slots", "effects"} {
		var count int64
		require.NoError(t, f.db.Table(table).Count(&count).Error)
		require.Zero(t, count, "failed creation must roll back budget, slot, audit and outbox")
	}
	f.pub.fail = false
	v := executionRouteData[ExecutionView](t, executionRouteRequest(t, handler, http.MethodPost, "/tenants/7/executions", in, 0, false), http.StatusCreated)
	require.Equal(t, "/registered/workspace", v.Plan.Workdir)
	require.Equal(t, 1, v.Version)
	path := "/executions/" + v.ID.String()
	leased := executionRouteData[[]ExecutionView](t, executionRouteRequest(t, handler, http.MethodPost, "/runner/execution-pulls", f.fence, 0, true), http.StatusOK)
	require.Len(t, leased, 1)
	require.NotNil(t, leased[0].LeaseExpiresAt)
	receipt := func(in ReceiptInput) *httptest.ResponseRecorder {
		return executionRouteRequest(t, handler, http.MethodPost, path+"/receipts", map[string]any{"fence": f.fence, "receipt": in}, 0, true)
	}
	accepted := ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"}
	f.pub.fail = true
	require.Equal(t, http.StatusServiceUnavailable, receipt(accepted).Code)
	require.Equal(t, "CREATED", f.read(t, v.ID).State)
	require.Zero(t, f.read(t, v.ID).LastSeq)
	var started int64
	require.NoError(t, f.db.Table("effects").Where("kind = ?", "work.started").Count(&started).Error)
	require.Zero(t, started, "runner failure must also roll back the delivery transition")
	f.pub.fail = false
	ack := executionRouteData[ReceiptView](t, receipt(accepted), http.StatusOK)
	require.Equal(t, "RUNNING", ack.State)
	replay := executionRouteData[ReceiptView](t, receipt(accepted), http.StatusOK)
	require.True(t, replay.Replayed)
	require.Equal(t, http.StatusConflict, receipt(ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "HEARTBEAT"}).Code)
	executionRouteData[ReceiptView](t, receipt(ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "LOG", Content: "actual runner log"}), http.StatusOK)
	head := strings.Repeat("b", 40)
	executionRouteData[ReceiptView](t, receipt(ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "EVIDENCE", Content: head, EvidenceKind: "COMMIT", EvidenceTitle: "Runner commit"}), http.StatusOK)
	detail := executionRouteData[ExecutionDetail](t, executionRouteRequest(t, handler, http.MethodGet, path, nil, 0, false), http.StatusOK)
	require.Len(t, detail.Logs, 1)
	require.Equal(t, "actual runner log", detail.Logs[0].Content)
	require.Equal(t, hash([]byte("actual runner log")), detail.Logs[0].ContentHash)
	require.Len(t, detail.Evidence, 1)
	require.Equal(t, "COMMIT", detail.Evidence[0].Kind)
	finished := executionRouteData[ReceiptView](t, receipt(ReceiptInput{DispatchEpoch: 1, Seq: 4, Kind: "COMPLETED", HeadCommit: head}), http.StatusOK)
	require.Equal(t, "SUCCEEDED", finished.State)
	require.Equal(t, "CONFIRMED", f.read(t, v.ID).ResultState)
	var active int
	require.NoError(t, f.db.Table("slots").Select("active").Where("execution_id = ?", v.ID).Scan(&active).Error)
	require.Zero(t, active)
	var submitted int64
	require.NoError(t, f.db.Table("effects").Where("kind = ?", "work.SUCCEEDED").Count(&submitted).Error)
	require.EqualValues(t, 1, submitted)
	page := executionRouteData[ExecutionPage](t, executionRouteRequest(t, handler, http.MethodGet, "/tenants/7/executions?project_id=9&limit=1", nil, 0, false), http.StatusOK)
	require.Len(t, page.Items, 1)
	require.Equal(t, v.ID, page.Items[0].ID)
	require.False(t, page.HasMore)
	require.Equal(t, http.StatusNotFound, executionRouteRequest(t, handler, http.MethodGet, "/executions/999", nil, 0, false).Code)
}

func TestExecutionHTTPCancelTimeoutRecoveryAndOldEpochFencing(t *testing.T) {
	f, handler := executionRoutesFixture(t)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	path := "/executions/" + v.ID.String()
	control := map[string]any{"acknowledge_unknown_result": false}
	current := f.read(t, v.ID)
	require.Equal(t, http.StatusUnprocessableEntity, executionRouteRequest(t, handler, http.MethodPut, path+"/cancellation", control, 0, false).Code)
	require.Equal(t, http.StatusConflict, executionRouteRequest(t, handler, http.MethodPut, path+"/cancellation", control, 1, false).Code)
	f.authorized = false
	require.Equal(t, http.StatusForbidden, executionRouteRequest(t, handler, http.MethodPut, path+"/cancellation", control, current.Version, false).Code)
	f.authorized = true
	f.pub.fail = true
	require.Equal(t, http.StatusServiceUnavailable, executionRouteRequest(t, handler, http.MethodPut, path+"/cancellation", control, current.Version, false).Code)
	require.Nil(t, f.read(t, v.ID).CancelRequestedAt)
	require.Equal(t, current.Version, f.read(t, v.ID).Version)
	f.pub.fail = false
	canceled := executionRouteData[ExecutionView](t, executionRouteRequest(t, handler, http.MethodPut, path+"/cancellation", control, current.Version, false), http.StatusOK)
	require.Equal(t, "RUNNING", canceled.State, "recording intent must not claim cancellation has completed")
	require.NotNil(t, canceled.CancelRequestedAt)
	commands := executionRouteData[[]ExecutionView](t, executionRouteRequest(t, handler, http.MethodPost, "/runner/execution-pulls", f.fence, 0, true), http.StatusOK)
	require.Len(t, commands, 1)
	require.NotNil(t, commands[0].CancelRequestedAt)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Update("cancel_requested_at", time.Now().Add(-31*time.Second)).Error)
	require.NoError(t, f.m.JobHandlers()[0].Run(t.Context(), jobs.Job{TenantID: 7}))
	interrupted := f.read(t, v.ID)
	require.Equal(t, "INTERRUPTED", interrupted.State)
	require.Equal(t, "UNKNOWN", interrupted.ResultState)
	require.Equal(t, "CANCEL_TIMEOUT", interrupted.StateReason)
	require.Equal(t, http.StatusUnprocessableEntity, executionRouteRequest(t, handler, http.MethodPut, path+"/recovery", control, interrupted.Version, false).Code)
	control["acknowledge_unknown_result"] = true
	f.pub.fail = true
	require.Equal(t, http.StatusServiceUnavailable, executionRouteRequest(t, handler, http.MethodPut, path+"/recovery", control, interrupted.Version, false).Code)
	require.Equal(t, interrupted.Version, f.read(t, v.ID).Version)
	var active int
	require.NoError(t, f.db.Table("slots").Select("active").Where("execution_id = ?", v.ID).Scan(&active).Error)
	require.Zero(t, active, "failed recovery must roll back slot reacquisition")
	f.pub.fail = false
	recovered := executionRouteData[ExecutionView](t, executionRouteRequest(t, handler, http.MethodPut, path+"/recovery", control, interrupted.Version, false), http.StatusOK)
	require.EqualValues(t, 2, recovered.DispatchEpoch)
	require.Equal(t, v.PlanHash, recovered.PlanHash)
	require.Equal(t, "CREATED", recovered.State)
	require.Nil(t, recovered.CancelRequestedAt)
	response := executionRouteRequest(t, handler, http.MethodPost, path+"/receipts", map[string]any{"fence": f.fence, "receipt": ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "HEARTBEAT"}}, 0, true)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Zero(t, f.read(t, v.ID).LastSeq)
}

func TestExecutionHTTPDetailAndListAreBounded(t *testing.T) {
	f, handler := executionRoutesFixture(t)
	v := f.create(t)
	logs := make([]*model.Log, 201)
	evidence := make([]*model.Evidence, 201)
	for i := range logs {
		id := guid.ID(i + 1)
		content := strconv.Itoa(i)
		logs[i] = &model.Log{ID: id, TenantID: 7, ExecutionID: v.ID, DispatchEpoch: 1, Seq: int64(i + 1), Content: content, ContentHash: hash([]byte(content)), CreatedAt: time.Now()}
		evidence[i] = &model.Evidence{ID: id, TenantID: 7, ProjectID: 9, ExecutionID: v.ID, Kind: "LOG", Title: content, Content: content, ContentHash: hash([]byte(content)), CreatedAt: time.Now()}
	}
	require.NoError(t, f.db.Table(f.m.names.Logs.String()).Create(&logs).Error)
	require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Create(&evidence).Error)
	detail := executionRouteData[ExecutionDetail](t, executionRouteRequest(t, handler, http.MethodGet, "/executions/"+v.ID.String(), nil, 0, false), http.StatusOK)
	require.Len(t, detail.Logs, 200)
	require.True(t, detail.HasMoreLogs)
	require.EqualValues(t, 2, detail.Logs[0].Seq)
	require.EqualValues(t, 201, detail.Logs[199].Seq)
	require.Len(t, detail.Evidence, 200)
	require.True(t, detail.HasMoreEvidence)
	require.Equal(t, guid.ID(1), detail.Evidence[0].ID)
	var historical model.Execution
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Take(&historical).Error)
	nextID, err := f.m.nextID()
	require.NoError(t, err)
	historical.ID = nextID
	historical.State, historical.ResultState = "SUCCEEDED", "CONFIRMED"
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Create(&historical).Error)
	first := executionRouteData[ExecutionPage](t, executionRouteRequest(t, handler, http.MethodGet, "/tenants/7/executions?project_id=9&limit=1", nil, 0, false), http.StatusOK)
	require.Len(t, first.Items, 1)
	require.True(t, first.HasMore)
	require.Equal(t, v.ID.String(), first.NextCursor)
	last := executionRouteData[ExecutionPage](t, executionRouteRequest(t, handler, http.MethodGet, "/tenants/7/executions?project_id=9&limit=1&cursor="+first.NextCursor, nil, 0, false), http.StatusOK)
	require.Len(t, last.Items, 1)
	require.Equal(t, historical.ID, last.Items[0].ID)
	require.False(t, last.HasMore)
	last = executionRouteData[ExecutionPage](t, executionRouteRequest(t, handler, http.MethodGet, "/tenants/7/executions?project_id=9&limit=1&cursor="+historical.ID.String(), nil, 0, false), http.StatusOK)
	require.Empty(t, last.Items)
	require.False(t, last.HasMore)
}

func TestExecutionRunnerHTTPMapsFleetFailures(t *testing.T) {
	f := executionHarness(t)
	var database struct{ File string }
	require.NoError(t, f.db.Raw("PRAGMA database_list").Scan(&database).Error)
	datasources, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: database.File, Readable: true, Writable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, datasources.Close()) })
	f.m.runtime = appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("ex_"), appenv.WithDatabase(datasources))
	f.m.registrar = executionHTTPRegistrar(t)
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	require.NoError(t, f.m.RegisterHTTP(t.Context(), api))
	for _, tc := range []struct {
		name    string
		failure error
		status  int
	}{
		{"stale_session", fleet.ErrSessionStale, http.StatusConflict},
		{"revoked_machine", fleet.ErrMachineRevoked, http.StatusConflict},
		{"disabled_account", fleet.ErrMachineForbidden, http.StatusForbidden},
		{"unknown_machine", fleet.ErrMachineNotFound, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.m.machines.ValidateSessionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64) error { return tc.failure }
			request := httptest.NewRequest(http.MethodPost, "/runner/execution-pulls", strings.NewReader(`{"machine_id":"1","session_id":"2","conn_epoch":1}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code, response.Body.String())
		})
	}
}

func TestExecutionHumanHTTPRechecksProjectAuthority(t *testing.T) {
	f := executionHarness(t)
	var database struct{ File string }
	require.NoError(t, f.db.Raw("PRAGMA database_list").Scan(&database).Error)
	datasources, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: database.File, Readable: true, Writable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, datasources.Close()) })
	f.m.runtime = appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("ex_"), appenv.WithDatabase(datasources))
	f.m.registrar = executionHTTPRegistrar(t)
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	require.NoError(t, f.m.RegisterHTTP(t.Context(), api))
	work := f.m.work
	f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return ErrCapabilityUnavailable }
	in := validCreate()
	in.Plan.WorkdirID = 5
	body, err := json.Marshal(in)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/tenants/7/executions", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	var count int64
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Count(&count).Error)
	require.Zero(t, count, "an unavailable delivery adapter must not fabricate an execution")
	f.m.work = work
	v := f.create(t)
	for _, condition := range []string{"allowed", "inactive_human", "project_denied"} {
		t.Run(condition, func(t *testing.T) {
			f.humanActive, f.authorized = condition != "inactive_human", condition != "project_denied"
			for _, path := range []string{"/executions/" + v.ID.String(), "/tenants/7/executions?project_id=9"} {
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				want := http.StatusForbidden
				if condition == "allowed" {
					want = http.StatusOK
				}
				require.Equal(t, want, response.Code, response.Body.String())
			}
		})
	}
}
func (executionHTTPAuthority) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}
func (executionHTTPAuthority) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return false, nil
}
func (executionHTTPAuthority) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}
func (executionHTTPAuthority) EntityConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{AllowAll: true}, nil
}

func executionHTTPRegistrar(t *testing.T) *authz.Registrar {
	t.Helper()
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	for _, verb := range []string{"read", "start", "stop"} {
		require.NoError(t, registry.Contribute([]authz.Action{{Resource: "execution", Verb: verb, Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	}
	for _, verb := range []string{"read", "write"} {
		require.NoError(t, registry.Contribute([]authz.Action{{Resource: "work_item", Verb: verb, Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	}
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "evidence", Verb: "submit", Scope: authz.ActionScopeTenant, DataScoped: true}, {Resource: "artifact_comment", Verb: "write", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "interaction", Verb: "respond", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "verification", Verb: "run", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "project", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "verifier", Verb: "manage", Scope: authz.ActionScopeTenant}, {Resource: "policy", Verb: "read", Scope: authz.ActionScopeTenant}}, nil))
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "code", Verb: "merge", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	authority := executionHTTPAuthority{}
	return authz.NewRegistrar(registry, authority, authority, authority)
}

func TestExecutionRegisteredHTTPContract(t *testing.T) {
	m := &Module{registrar: executionHTTPRegistrar(t)}
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	encoded, err := json.Marshal(api.OpenAPI())
	require.NoError(t, err)
	var document struct {
		Paths map[string]map[string]struct {
			OperationID string                `json:"operationId"`
			Security    []map[string][]string `json:"security"`
			Parameters  []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
				Schema   struct {
					Type, Pattern string
					MaxLength     int `json:"maxLength"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(encoded, &document))
	ids := map[string]bool{}
	count := 0
	for path, methods := range document.Paths {
		for method, operation := range methods {
			count++
			require.NotEmpty(t, operation.OperationID, "%s %s", method, path)
			require.False(t, ids[operation.OperationID], "operation IDs must be unique")
			ids[operation.OperationID] = true
			require.NotEmpty(t, operation.Security, "%s %s must require authentication", method, path)
			for _, match := range regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(path, -1) {
				found := false
				for _, parameter := range operation.Parameters {
					if parameter.Name == match[1] && parameter.In == "path" {
						found = true
						require.True(t, parameter.Required)
						require.Equal(t, "string", parameter.Schema.Type)
						require.Equal(t, `^[1-9][0-9]*$`, parameter.Schema.Pattern)
						require.Equal(t, 19, parameter.Schema.MaxLength)
					}
				}
				require.True(t, found, "%s %s must declare %s", method, path, match[1])
			}
		}
	}
	expectedIDs := []string{
		"execution.list", "execution.create", "execution.get", "execution.cancel", "execution.recover", "execution.runner.pull", "execution.runner.receipt",
		"execution.sessions.list", "execution.evidence-provenance.list",
		"execution.branch.list", "execution.branch.strategy", "execution.trace.export", "verifier.list", "verifier.create", "verifier.get", "verifier.update", "verifier.version.list",
		"execution.full_auto.grant", "execution.full_auto.policy", "execution.full_auto.policy.get",
		"artifact_comment.create", "artifact_comment.list", "execution.visual.reviews", "execution.visual.review",
		"artifact_comment.source", "artifact_comment.state", "artifact_comment.reanchor", "artifact_comment.edit", "artifact_comment.withdraw",
		"artifact_comment.reply", "artifact_comment.messages", "evidence.create", "evidence.update",
		"execution.merge.create", "execution.merge.list", "execution.merge.get", "execution.merge.pull", "execution.merge.receipt",
		"execution.approval.request", "execution.approval.poll", "execution.approval.decide",
		"execution.verification.pull", "execution.verification.receipt", "template.verifier-result.list",
		"execution.preview-ports.list", "execution.previews.list", "execution.previews.ensure",
		"preview.read", "preview.terminate", "preview.access-grants.create", "preview.label-access.create",
	}
	for _, path := range []string{
		"/executions/{execution_id}/full-auto-grant", "/executions/{execution_id}/project-execution-policy",
		"/executions/{execution_id}/artifact-comments", "/executions/{execution_id}/visual-reviews",
		"/comment-threads/{thread_id}", "/comment-threads/{thread_id}/reanchor", "/comments/{comment_id}",
		"/comment-threads/{thread_id}/comments", "/executions/{execution_id}/evidence", "/evidence/{evidence_id}",
		"/executions/{execution_id}/merges", "/runner/merges/{merge_id}/receipts",
		"/executions/{execution_id}/branches", "/verifiers/{verifier_id}",
		"/executions/{execution_id}/approval-requests", "/executions/{execution_id}/approval-requests/{approval_id}/polls", "/executions/{execution_id}/approval-requests/{approval_id}/decision",
		"/runner/verifications/{task_id}/receipts", "/executions/{execution_id}/cancellation", "/executions/{execution_id}/recovery", "/executions/{execution_id}/receipts",
	} {
		expectedIDs = append(expectedIDs, "request-contract:"+path)
	}
	actualIDs := make([]string, 0, len(ids))
	for id := range ids {
		actualIDs = append(actualIDs, id)
	}
	require.ElementsMatch(t, expectedIDs, actualIDs, "exact business and request-contract inventory")
	require.Equal(t, 72, count, "fifty-one business operations plus twenty-one request contracts")
	comments := api.OpenAPI().Paths["/executions/{execution_id}/artifact-comments"]
	require.Equal(t, "artifact_comment.create", comments.Post.OperationID)
	require.Equal(t, "artifact_comment.list", comments.Get.OperationID)
	require.Equal(t, "request-contract:/executions/{execution_id}/artifact-comments", comments.Options.OperationID)
	create := api.OpenAPI().Paths["/tenants/{tenant_id}/executions"].Post
	require.Contains(t, create.Responses, "201")
	for _, path := range []string{"/runner/execution-pulls", "/executions/{execution_id}/receipts", "/runner/verification-pulls", "/runner/verifications/{task_id}/receipts"} {
		require.True(t, authz.IsAuthenticatedOperation(api.OpenAPI().Paths[path].Post))
	}
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/executions/invalid/receipts", `{}`},
		{http.MethodPost, "/executions/1/receipts", `{}`},
		{http.MethodPost, "/runner/execution-pulls", `{}`},
		{http.MethodPost, "/tenants/7/executions", `{}`},
		{http.MethodGet, "/tenants/7/executions?project_id=invalid", ""},
	} {
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	}
}

func TestExecutionMutationPathAndHeaderBinding(t *testing.T) {
	for _, receipt := range []bool{false, true} {
		name := "control"
		if receipt {
			name = "receipt"
		}
		t.Run(name, func(t *testing.T) {
			router := chi.NewRouter()
			api := humax.New(router, humax.Config{Name: "test", Version: "1"})
			path, method, body := "/executions/{execution_id}/cancellation", http.MethodPut, `{"acknowledge_unknown_result":false}`
			id, version := "", ""
			if receipt {
				path, method, body = "/executions/{execution_id}/receipts", http.MethodPost, `{"fence":{"machine_id":"2","session_id":"3","conn_epoch":1},"receipt":{"dispatch_epoch":1,"seq":1,"kind":"ACCEPTED","content":"","head_commit":"","evidence_kind":"","evidence_title":""}}`
				huma.Register(api, huma.Operation{OperationID: name, Method: method, Path: path}, func(ctx context.Context, in *receiptHTTPInput) (*respx.Body[bool], error) {
					id = in.ExecutionID
					return respx.OK(ctx, true), nil
				})
			} else {
				huma.Register(api, huma.Operation{OperationID: name, Method: method, Path: path}, func(ctx context.Context, in *controlInput) (*respx.Body[bool], error) {
					id, version = in.ExecutionID, in.IfMatch
					return respx.OK(ctx, true), nil
				})
			}
			request := httptest.NewRequest(method, strings.ReplaceAll(path, "{execution_id}", "123"), strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("If-Match", `"5"`)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, "123", id)
			if !receipt {
				require.Equal(t, `"5"`, version)
			}
		})
	}
}

func TestHttpRejectsMalformedIdentityAndUnavailableWriter(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		id := "invalid"
		expected := 422
		f := executionHarness(t)
		if unavailable {
			id = "1"
			expected = 503
			f.m.runtime = nil
		}
		t.Run("getHTTP"+id, func(t *testing.T) {
			_, err := f.m.getHTTP(f.human, &ExecutionInput{ExecutionID: id})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("cancelHTTP"+id, func(t *testing.T) {
			_, err := f.m.cancelHTTP(f.human, &controlInput{ExecutionInput: ExecutionInput{ExecutionID: id}, IfMatch: "1"})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("recoverHTTP"+id, func(t *testing.T) {
			_, err := f.m.recoverHTTP(f.human, &controlInput{ExecutionInput: ExecutionInput{ExecutionID: id}, IfMatch: "1"})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("listHTTP"+id, func(t *testing.T) {
			_, err := f.m.listHTTP(f.human, &listInput{ProjectID: id, Limit: 10})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
	}
}

func TestExecutionHTTPErrorClassesKeepPublishedStatuses(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{&ClientAnchorError{Field: "kind", Unavailable: true}, 422}, {ErrCommentLimit, 422}, {ErrClientThreadReadOnly, 409}, {ErrFullAutoForbidden, 403}, {idempotency.ErrInProgress, 409}, {ErrApprovalUnavailable, 422}} {
		var status huma.StatusError
		require.ErrorAs(t, httpError(t.Context(), tc.err), &status)
		require.Equal(t, tc.status, status.GetStatus())
	}
}

func TestExecutionHTTPReadStorageFailuresDoNotExposePartialDetails(t *testing.T) {
	for _, command := range []string{"list", "get"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f := executionHarness(t)
				v := f.create(t)
				return f, func(*gorm.DB) error {
					if command == "list" {
						out, e := f.m.listHTTP(f.human, &listInput{ProjectID: "9", Limit: 50})
						if e == nil {
							require.Len(t, out.Body.Data.Items, 1)
						}
						return e
					}
					out, e := f.m.getHTTP(f.human, &ExecutionInput{ExecutionID: v.ID.String()})
					if e == nil {
						require.Equal(t, v.ID, out.Body.Data.Execution.ID)
					}
					return e
				}
			}, true)
		})
	}
}

func TestOwnerRejectsMissingWriterAndRecord(t *testing.T) {
	for _, missingWriter := range []bool{false, true} {
		f := executionHarness(t)
		if missingWriter {
			f.m.runtime = nil
		}
		_, found, e := f.m.owner(f.human, 999)
		if missingWriter {
			require.ErrorIs(t, e, ErrUnavailable)
		} else {
			require.NoError(t, e)
		}
		require.False(t, found)
	}
}
