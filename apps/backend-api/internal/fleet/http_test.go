package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Every mutation must both publish its path selector and bind it before the
// business handler runs. An unexported embedded input silently loses both.
func TestMachineHTTPWriterOutageAndMalformedSelectorsFailClosed(t *testing.T) {
	m, _, ctx, _ := machineHarness(t)
	m.runtime = nil
	for _, call := range []func() error{
		func() error { _, e := m.listMachinesHTTP(ctx, &machineListInput{Limit: 50}); return e },
		func() error { _, e := m.getMachineHTTP(ctx, &MachineInput{MachineID: "1"}); return e },
		func() error {
			_, e := m.drainMachineHTTP(ctx, &machineDrainInput{MachineInput: MachineInput{MachineID: "1"}, IfMatch: "1"})
			return e
		},
		func() error {
			_, e := m.openSessionHTTP(ctx, &sessionInput{MachineInput: MachineInput{MachineID: "1"}})
			return e
		},
		func() error {
			_, e := m.heartbeatHTTP(ctx, &heartbeatInput{MachineInput: MachineInput{MachineID: "1"}})
			return e
		},
		func() error {
			_, e := m.inventoryHTTP(ctx, &inventoryInput{MachineInput: MachineInput{MachineID: "1"}})
			return e
		},
	} {
		var status huma.StatusError
		require.ErrorAs(t, call(), &status)
		require.Equal(t, 503, status.GetStatus())
	}
	_, found, e := m.machineOwner(ctx, 1)
	require.ErrorIs(t, e, ErrUnavailable)
	require.False(t, found)
	for _, id := range []string{"invalid", "0", "9999999999999999999999999999"} {
		for _, call := range []func() error{
			func() error { _, e := m.getMachineHTTP(ctx, &MachineInput{MachineID: id}); return e },
			func() error {
				_, e := m.drainMachineHTTP(ctx, &machineDrainInput{MachineInput: MachineInput{MachineID: id}})
				return e
			},
			func() error {
				_, e := m.openSessionHTTP(ctx, &sessionInput{MachineInput: MachineInput{MachineID: id}})
				return e
			},
			func() error {
				_, e := m.heartbeatHTTP(ctx, &heartbeatInput{MachineInput: MachineInput{MachineID: id}})
				return e
			},
			func() error {
				_, e := m.inventoryHTTP(ctx, &inventoryInput{MachineInput: MachineInput{MachineID: id}})
				return e
			},
		} {
			var status huma.StatusError
			require.ErrorAs(t, call(), &status)
			require.Equal(t, 422, status.GetStatus())
		}
	}
}

func TestMachineHTTPCurrentPolicyAndOwnerStorageErrorsExposeNoPartialView(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	injected := errors.New("machine current policy unavailable")
	for _, mode := range []string{"missing", "source", "denied", "storage"} {
		switch mode {
		case "missing":
			m.policy = nil
		case "source":
			m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, injected
			}
		case "denied":
			m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, nil
			}
		case "storage":
			m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("machine_view_storage_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == m.names.Machines.String() {
					tx.AddError(injected)
				}
			}))
		}
		read, e := m.getMachineHTTP(ctx, &MachineInput{MachineID: "1"})
		require.Error(t, e)
		require.Nil(t, read)
		list, e := m.listMachinesHTTP(ctx, &machineListInput{Limit: 50})
		require.Error(t, e)
		require.Nil(t, list)
		if mode == "storage" {
			_, found, e := m.machineOwner(ctx, 1)
			require.ErrorIs(t, e, injected)
			require.False(t, found)
		}
	}
	require.NoError(t, db.Callback().Query().Remove("machine_view_storage_failure"))
	_, found, e := m.machineOwner(ctx, 999999)
	require.NoError(t, e)
	require.False(t, found)
}

func TestMachineMutationPathBinding(t *testing.T) {
	for _, tc := range []struct {
		name, method, suffix, body string
		register                   func(huma.API, huma.Operation, func(string))
	}{
		{"drain", http.MethodPut, "/drain", `{"drain":true}`, func(api huma.API, op huma.Operation, record func(string)) {
			huma.Register(api, op, func(ctx context.Context, in *machineDrainInput) (*respx.Body[bool], error) {
				record(in.MachineID)
				return respx.OK(ctx, true), nil
			})
		}},
		{"session", http.MethodPost, "/runner-sessions", `{"incarnation_id":"process-one"}`, func(api huma.API, op huma.Operation, record func(string)) {
			huma.Register(api, op, func(ctx context.Context, in *sessionInput) (*respx.Body[bool], error) {
				record(in.MachineID)
				return respx.OK(ctx, true), nil
			})
		}},
		{"heartbeat", http.MethodPut, "/heartbeat", `{"session_id":"2","conn_epoch":1}`, func(api huma.API, op huma.Operation, record func(string)) {
			huma.Register(api, op, func(ctx context.Context, in *heartbeatInput) (*respx.Body[bool], error) {
				record(in.MachineID)
				return respx.OK(ctx, true), nil
			})
		}},
		{"inventory", http.MethodPut, "/inventory", `{"session_id":"2","conn_epoch":1,"inventory":{"Runtimes":[],"Tools":[]}}`, func(api huma.API, op huma.Operation, record func(string)) {
			huma.Register(api, op, func(ctx context.Context, in *inventoryInput) (*respx.Body[bool], error) {
				record(in.MachineID)
				return respx.OK(ctx, true), nil
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := chi.NewRouter()
			api := humax.New(router, humax.Config{Name: "test", Version: "1"})
			path := "/machines/{machine_id}" + tc.suffix
			bound := ""
			tc.register(api, huma.Operation{OperationID: "test." + tc.name, Method: tc.method, Path: path}, func(id string) { bound = id })
			op := api.OpenAPI().Paths[path].Put
			if tc.method == http.MethodPost {
				op = api.OpenAPI().Paths[path].Post
			}
			require.NotNil(t, op)
			var selector *huma.Param
			for _, parameter := range op.Parameters {
				if parameter.Name == "machine_id" && parameter.In == "path" {
					selector = parameter
				}
			}
			require.NotNil(t, selector, "the generated operation must declare machine_id")
			require.True(t, selector.Required)
			require.Equal(t, `^[1-9][0-9]*$`, selector.Schema.Pattern)
			require.NotNil(t, selector.Schema.MaxLength)
			require.Equal(t, 19, *selector.Schema.MaxLength)

			request := httptest.NewRequest(tc.method, "/machines/123"+tc.suffix, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("If-Match", `"1"`)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, "123", bound, "the actual Huma request must reach the handler with the path identifier")
		})
	}
}

type fleetHTTPAuthority struct{ enrollmentHTTPAuthority }

func (a fleetHTTPAuthority) Authenticate(ctx context.Context, authorization, cookie string) (*authn.Claims, error) {
	if authorization == "Bearer runner" {
		// Real IAM access-token verification deliberately leaves the route's
		// tenant unbound; organization_id is an external claim, not a selector.
		return &authn.Claims{PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount, OrganizationID: "external-not-tenant"}, nil
	}
	return a.enrollmentHTTPAuthority.Authenticate(ctx, authorization, cookie)
}

func TestRunnerHTTPBindsUnscopedVerifiedMachineIdentity(t *testing.T) {
	m, _, _, _, _ := enrollmentHarness(t)
	router := fleetBusinessRouter(t, m)
	headers := map[string]string{"Authorization": "Bearer runner"}
	response := fleetHTTPRequest(router, http.MethodPost, "/machines/1/runner-sessions", `{"incarnation_id":"actual-claims-shape"}`, headers)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var session struct {
		Data sessionView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &session))
	body := fmt.Sprintf(`{"session_id":%q,"conn_epoch":%d}`, session.Data.ID.String(), session.Data.ConnEpoch)
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/heartbeat", body, headers)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	body = fmt.Sprintf(`{"session_id":%q,"conn_epoch":%d,"observation_mode":"RUNTIME_VERSIONS","inventory":{"runtimes":[],"tools":[]}}`, session.Data.ID.String(), session.Data.ConnEpoch)
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/inventory", body, headers)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

func (fleetHTTPAuthority) EntityConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{AllowAll: true}, nil
}

func fleetBusinessRouter(t *testing.T, m *Module) http.Handler {
	t.Helper()
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "gateway", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	require.NoError(t, registry.Contribute([]authz.Action{
		{Resource: "machine_access", Verb: "manage", Scope: authz.ActionScopeTenant},
		{Resource: "terminal", Verb: "interact", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "agent", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "agent", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "work_item", Verb: "assign", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "execution", Verb: "stop", Scope: authz.ActionScopeTenant, DataScoped: true},
	}, nil))
	for _, verb := range []string{"read", "manage", "revoke"} {
		require.NoError(t, registry.Contribute([]authz.Action{{Resource: "machine", Verb: verb, Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	}
	authority := fleetHTTPAuthority{}
	m.registrar = authz.NewRegistrar(registry, authority, authority, authority)
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	return router
}

func fleetHTTPRequest(router http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func seedFleetMachine(t *testing.T, m *Module, db *gorm.DB, id, tenant, principal guid.ID, state string, seen *time.Time) {
	t.Helper()
	key := "machine-" + id.String()
	now := time.Now().UTC()
	require.NoError(t, query.BindMachines(db, m.names).WithContext(t.Context()).Create(&model.Machine{ID: id, TenantID: tenant, PrincipalID: principal, Name: key, NameKey: &key, State: state, OS: "LINUX", SlotCount: 1, LastSeenAt: seen, Version: 1, CreatedAt: now, UpdatedAt: now}))
}

func TestMachineHTTPListGetAndCurrentTenantPolicy(t *testing.T) {
	m, db, _, _, _ := enrollmentHarness(t)
	seedFleetMachine(t, m, db, 2, 7, 102, "OFFLINE", nil)
	seedFleetMachine(t, m, db, 3, 8, 103, "OFFLINE", nil)
	m.policy = func(_ context.Context, _ *gorm.DB, tenant guid.ID, _ string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: tenant == 7}, nil
	}
	router := fleetBusinessRouter(t, m)
	response := fleetHTTPRequest(router, http.MethodGet, "/tenants/7/machines?limit=1", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page struct {
		Data machinePage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Data.Items, 1)
	require.Equal(t, guid.ID(1), page.Data.Items[0].ID)
	require.True(t, page.Data.HasMore)
	require.Equal(t, "1", page.Data.NextCursor)
	response = fleetHTTPRequest(router, http.MethodGet, "/tenants/7/machines?limit=1&cursor="+page.Data.NextCursor, "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Data.Items, 1)
	require.Equal(t, guid.ID(2), page.Data.Items[0].ID)
	require.False(t, page.Data.HasMore)
	response = fleetHTTPRequest(router, http.MethodGet, "/machines/1", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var machine struct {
		Data machineView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &machine))
	require.Equal(t, "OFFLINE", machine.Data.State)
	require.Equal(t, "UNAVAILABLE", machine.Data.RemoteAccess)
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/machines/999", http.StatusNotFound},
		{"/machines/3", http.StatusForbidden},
		{"/tenants/7/machines?cursor=not-an-id", http.StatusUnprocessableEntity},
		{"/tenants/7/machines?limit=101", http.StatusUnprocessableEntity},
	} {
		response = fleetHTTPRequest(router, http.MethodGet, tc.path, "", nil)
		require.Equal(t, tc.status, response.Code, response.Body.String())
	}
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	for _, path := range []string{"/tenants/7/machines", "/machines/1"} {
		response = fleetHTTPRequest(router, http.MethodGet, path, "", nil)
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
}

func TestMachineHTTPFiltersBeforePaginationAndPreservesTenant(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	now, stale := time.Now().UTC(), time.Now().UTC().Add(-time.Minute)
	for _, row := range []struct {
		id, tenant guid.ID
		state      string
		seen       *time.Time
	}{
		{2, 7, "ONLINE", &now}, {3, 7, "ONLINE", &stale}, {4, 7, "DRAINING", &now}, {5, 7, "ONLINE", &now}, {6, 8, "ONLINE", &now}, {7, 7, "ONLINE", &now},
	} {
		seedFleetMachine(t, m, db, row.id, row.tenant, row.id+100, row.state, row.seen)
	}
	for _, id := range []guid.ID{2, 3, 4, 5, 6, 7} {
		tenant := guid.ID(7)
		if id == 6 {
			tenant = 8
		}
		require.NoError(t, query.BindRuntimes(db, m.names).WithContext(ctx).Create(&model.MachineRuntime{ID: id + 20, TenantID: tenant, MachineID: id, Kind: "CODEX", Version: "1", ExecutablePath: "/bin/codex", ModelsJSON: "[]", InspectedAt: now}))
	}
	active := 1
	require.NoError(t, query.BindSlots(db, m.names).WithContext(ctx).Create(&model.SlotReservation{ID: 90, TenantID: 7, MachineID: 2, ExecutionID: 99, SlotNo: 1, ActiveSlot: &active, CreatedAt: now}))
	// Released reservations are history, not occupied capacity.
	require.NoError(t, query.BindSlots(db, m.names).WithContext(ctx).Create(&model.SlotReservation{ID: 91, TenantID: 7, MachineID: 5, ExecutionID: 98, SlotNo: 1, ReleasedAt: &now, CreatedAt: now}))
	router := fleetBusinessRouter(t, m)
	check := func(queryString string, ids []guid.ID, more bool) machinePage {
		t.Helper()
		response := fleetHTTPRequest(router, http.MethodGet, "/tenants/7/machines?"+queryString, "", nil)
		require.Equal(t, 200, response.Code, response.Body.String())
		var envelope struct {
			Data machinePage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		actual := []guid.ID{}
		for _, row := range envelope.Data.Items {
			actual = append(actual, row.ID)
		}
		require.Equal(t, ids, actual)
		require.Equal(t, more, envelope.Data.HasMore)
		return envelope.Data
	}
	page := check("state=ONLINE&runtime=CODEX&free_slots=true&limit=1", []guid.ID{5}, true)
	require.Equal(t, "5", page.NextCursor)
	check("state=ONLINE&runtime=CODEX&free_slots=true&limit=1&cursor=5", []guid.ID{7}, false)
	check("state=OFFLINE&runtime=CODEX", []guid.ID{3}, false)
	check("state=DRAINING&runtime=CODEX", []guid.ID{4}, false)
	check("state=DRAINING&free_slots=true", []guid.ID{}, false)
	check("runtime=CLAUDE_CODE", []guid.ID{}, false)
	for _, invalid := range []string{"state=UNKNOWN", "runtime=UNKNOWN", "free_slots=invalid"} {
		require.Equal(t, 422, fleetHTTPRequest(router, http.MethodGet, "/tenants/7/machines?"+invalid, "", nil).Code)
	}
}

func TestMachineHTTPRunnerDrainAndInventoryFlow(t *testing.T) {
	m, db, _, _, publisher := enrollmentHarness(t)
	router := fleetBusinessRouter(t, m)
	response := fleetHTTPRequest(router, http.MethodPost, "/machines/1/runner-sessions", `{"incarnation_id":"process-one"}`, map[string]string{"Authorization": ""})
	require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	response = fleetHTTPRequest(router, http.MethodPost, "/machines/1/runner-sessions", `{"incarnation_id":"process-one"}`, nil)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var session struct {
		Data sessionView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &session))
	heartbeat := fmt.Sprintf(`{"session_id":"%s","conn_epoch":%d}`, session.Data.ID, session.Data.ConnEpoch)
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/heartbeat", heartbeat, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/heartbeat", fmt.Sprintf(`{"session_id":"%s","conn_epoch":%d}`, session.Data.ID, session.Data.ConnEpoch+1), nil)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	inventory := fmt.Sprintf(`{"session_id":"%s","conn_epoch":%d,"inventory":{"runtimes":[{"kind":"CODEX","version":"1","executable_path":"/bin/codex","models":["observed-model"]}],"tools":[{"name":"node","version":"24","executable_path":"/bin/node"}]}}`, session.Data.ID, session.Data.ConnEpoch)
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/inventory", inventory, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/drain", `{"drain":true}`, map[string]string{"If-Match": "2"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var drained struct {
		Data machineView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &drained))
	require.Equal(t, "DRAINING", drained.Data.State)
	require.True(t, drained.Data.DrainRequested)
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/drain", `{"drain":false}`, map[string]string{"If-Match": "2"})
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	publisher.fail = true
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/drain", `{"drain":false}`, map[string]string{"If-Match": fmt.Sprint(drained.Data.Version)})
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	row, err := query.BindMachines(db, m.names).WithContext(t.Context()).First()
	require.NoError(t, err)
	require.True(t, row.DrainRequested, "HTTP transaction rolls back a failed outbox write")
	publisher.fail = false
	m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, nil }
	for _, request := range []struct{ method, path, body string }{
		{http.MethodPut, "/machines/1/heartbeat", heartbeat},
		{http.MethodPut, "/machines/1/inventory", inventory},
		{http.MethodPost, "/machines/1/runner-sessions", `{"incarnation_id":"process-two"}`},
	} {
		response = fleetHTTPRequest(router, request.method, request.path, request.body, nil)
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
}

func TestMachineHTTPServiceErrorsRemainOpaque(t *testing.T) {
	m, _, _, _, _ := enrollmentHarness(t)
	router := fleetBusinessRouter(t, m)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, errors.New("test private SQL detail")
	}
	response := fleetHTTPRequest(router, http.MethodGet, "/tenants/7/machines", "", nil)
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "private SQL detail")
	for _, match := range []string{"invalid", "0", "-1"} {
		response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/drain", `{"drain":true}`, map[string]string{"If-Match": match})
		require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	}
}

func TestMachineHTTPRegisteredRoutesFailClosedWhenDatabaseBecomesUnavailable(t *testing.T) {
	m, _, _, _, _ := enrollmentHarness(t)
	router := fleetBusinessRouter(t, m)
	m.runtime = nil
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/tenants/7/machines", ""},
		{http.MethodGet, "/machines/1", ""},
		{http.MethodGet, "/machines/1/capabilities", ""},
		{http.MethodPut, "/machines/1/drain", `{"drain":true}`},
		{http.MethodPost, "/machines/1/runner-sessions", `{"incarnation_id":"process"}`},
		{http.MethodPut, "/machines/1/heartbeat", `{"session_id":"2","conn_epoch":1}`},
		{http.MethodPut, "/machines/1/inventory", `{"session_id":"2","conn_epoch":1,"inventory":{"runtimes":[],"tools":[]}}`},
		{http.MethodPost, "/machine-enrollments", `{"tenant_id":"7","client_id":"20","name":"runner","os":"LINUX"}`},
		{http.MethodPost, "/machine-enrollments/1/credential", `{"tenant_id":"7","device_code":"proof"}`},
	} {
		response := fleetHTTPRequest(router, request.method, request.path, request.body, map[string]string{"If-Match": "1"})
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), "sqlite")
	}
}

func TestMachineHTTPRunnerConflictAndOwnershipDoNotMutateSession(t *testing.T) {
	m, db, _, _, _ := enrollmentHarness(t)
	seedFleetMachine(t, m, db, 2, 7, 102, "OFFLINE", nil)
	router := fleetBusinessRouter(t, m)
	response := fleetHTTPRequest(router, http.MethodPost, "/machines/1/runner-sessions", `{"incarnation_id":"one"}`, nil)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	for _, request := range []struct {
		path, body string
		status     int
	}{
		{"/machines/1/runner-sessions", `{"incarnation_id":"two"}`, http.StatusConflict},
		{"/machines/2/runner-sessions", `{"incarnation_id":"one"}`, http.StatusForbidden},
		{"/machines/999/runner-sessions", `{"incarnation_id":"one"}`, http.StatusNotFound},
		{"/machines/9223372036854775808/runner-sessions", `{"incarnation_id":"one"}`, http.StatusUnprocessableEntity},
	} {
		response = fleetHTTPRequest(router, http.MethodPost, request.path, request.body, nil)
		require.Equal(t, request.status, response.Code, response.Body.String())
	}
	var count int64
	require.NoError(t, db.Table(m.names.Sessions.String()).Count(&count).Error)
	require.Equal(t, int64(1), count)
}
