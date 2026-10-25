package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type inspectionFixture struct {
	m         *Module
	db        *gorm.DB
	ctx       context.Context
	session   SessionFact
	publisher *fleetPublisher
}

func inspectionHarness(t *testing.T) *inspectionFixture {
	t.Helper()
	m, db, ctx, _, publisher := enrollmentHarness(t)
	f := &inspectionFixture{m: m, db: db, ctx: ctx, publisher: publisher}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		f.session, err = m.OpenSessionOn(ctx, tx, 7, 1, "inspection")
		return err
	}))
	return f
}
func (f *inspectionFixture) request(kind string) (InspectionView, error) {
	var out InspectionView
	err := f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = f.m.RequestInspectionOn(f.ctx, tx, 7, 1, kind)
		return err
	})
	return out, err
}
func (f *inspectionFixture) pull() ([]InspectionView, error) {
	var out []InspectionView
	err := f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = f.m.PullInspectionOn(f.ctx, tx, 7, 1, f.session.ID, f.session.ConnEpoch)
		return err
	})
	return out, err
}
func (f *inspectionFixture) report(id guid.ID, in InspectionResult) (InspectionView, error) {
	var out InspectionView
	err := f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = f.m.ReportInspectionOn(f.ctx, tx, 7, 1, id, f.session.ID, f.session.ConnEpoch, in)
		return err
	})
	return out, err
}
func (f *inspectionFixture) read(t *testing.T, id guid.ID) *model.Inspection {
	t.Helper()
	q := query.BindInspections(f.db, f.m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	return row
}
func inspectionInventory() Inventory {
	return Inventory{Runtimes: []RuntimeObservation{{Kind: "CODEX", Version: "2", ExecutablePath: "/actual/codex", Models: []string{}}}, Tools: []ToolObservation{{Name: "node", Version: "24", ExecutablePath: "/actual/node"}}}
}

func TestInspectionLifecycleCoalescesSerializesAndPreservesModelFacts(t *testing.T) {
	f := inspectionHarness(t)
	old := inspectionInventory()
	old.Runtimes[0].Version, old.Runtimes[0].Models = "1", []string{"prior-observed-model"}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.RecordInventoryOn(f.ctx, tx, 7, 1, f.session.ID, 1, old) }))
	request, err := f.request("RUNTIMES")
	require.NoError(t, err)
	require.Equal(t, "QUEUED", request.State)
	require.Equal(t, 65*time.Second, request.ExpiresAt.Sub(request.CreatedAt))
	replay, err := f.request("RUNTIMES")
	require.NoError(t, err)
	require.Equal(t, request, replay)
	models, err := f.request("MODELS")
	require.NoError(t, err)
	rows, err := f.pull()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, request.ID, rows[0].ID)
	require.Equal(t, "RUNNING", rows[0].State)
	rows, err = f.pull()
	require.NoError(t, err)
	require.Empty(t, rows, "a second pull cannot redeliver or start a parallel request")
	in := InspectionResult{Succeeded: true, Inventory: inspectionInventory()}
	in.Inventory.Runtimes[0].Models = []string{"forged-model"}
	ready, err := f.report(request.ID, in)
	require.NoError(t, err)
	require.Equal(t, "READY", ready.State)
	require.Nil(t, f.read(t, request.ID).ActiveKey)
	before := f.read(t, request.ID)
	var effects int64
	require.NoError(t, f.db.Table("test_fleet_effects").Count(&effects).Error)
	replayed, err := f.report(request.ID, in)
	require.NoError(t, err)
	require.Equal(t, ready, replayed)
	require.Equal(t, before, f.read(t, request.ID))
	var after int64
	require.NoError(t, f.db.Table("test_fleet_effects").Count(&after).Error)
	require.Equal(t, effects, after)
	in.Inventory.Runtimes[0].Version = "changed"
	_, err = f.report(request.ID, in)
	require.ErrorIs(t, err, ErrVersionConflict)
	var runtime model.MachineRuntime
	require.NoError(t, f.db.Table(f.m.names.Runtimes.String()).Take(&runtime).Error)
	require.Equal(t, "2", runtime.Version)
	require.JSONEq(t, `["prior-observed-model"]`, runtime.ModelsJSON)
	rows, err = f.pull()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, models.ID, rows[0].ID)
	_, err = f.report(models.ID, InspectionResult{Succeeded: true, Inventory: inspectionInventory()})
	require.ErrorIs(t, err, ErrInvalidArgument, "version facts cannot prove model discovery")
	failed, err := f.report(models.ID, InspectionResult{ReasonCode: "UNSUPPORTED_INSPECTION_KIND"})
	require.NoError(t, err)
	require.Equal(t, "ERROR", failed.State)
	require.Equal(t, "UNSUPPORTED_INSPECTION_KIND", failed.ReasonCode)
	failedReplay, err := f.report(models.ID, InspectionResult{ReasonCode: "UNSUPPORTED_INSPECTION_KIND"})
	require.NoError(t, err)
	require.Equal(t, failed, failedReplay)
	rows, err = f.pull()
	require.NoError(t, err)
	require.Empty(t, rows)
	newRequest, err := f.request("RUNTIMES")
	require.NoError(t, err)
	require.NotEqual(t, request.ID, newRequest.ID)
}

func TestInspectionRequestsRequireCurrentHumanAndOnlineMachine(t *testing.T) {
	for _, fault := range []string{"invalid_kind", "no_transaction", "denied", "missing_machine", "offline", "draining", "revoked", "no_session", "ids_missing", "ids_zero", "ids_error", "insert", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			f := inspectionHarness(t)
			kind := "RUNTIMES"
			switch fault {
			case "invalid_kind":
				kind = "SHELL"
			case "denied":
				f.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "missing_machine":
			case "offline":
				require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 1).Update("last_seen_at", time.Now().UTC().Add(-time.Minute)).Error)
			case "draining":
				require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 1).Update("state", "DRAINING").Error)
			case "revoked":
				require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 1).Updates(map[string]any{"state": "REVOKED", "name_key": nil}).Error)
			case "no_session":
				require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 1).Update("current_session_id", nil).Error)
			case "ids_missing":
				f.m.ids = nil
			case "ids_zero":
				f.m.ids = failedFleetIDs{}
			case "ids_error":
				f.m.ids = failedFleetIDs{err: errors.New("ids failed")}
			case "insert":
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_inspection BEFORE INSERT ON dir_fleet_inspections BEGIN SELECT RAISE(ABORT,'insert failed'); END").Error)
			case "outbox":
				f.publisher.fail = true
			}
			var err error
			if fault == "no_transaction" {
				_, err = f.m.RequestInspectionOn(f.ctx, f.db, 7, 1, kind)
			} else if fault == "missing_machine" {
				err = f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.RequestInspectionOn(f.ctx, tx, 7, 999, kind); return e })
			} else {
				_, err = f.request(kind)
			}
			require.Error(t, err)
			var count int64
			require.NoError(t, f.db.Table(f.m.names.Inspections.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestInspectionFailuresRollbackClaimReportAndLastGoodInventory(t *testing.T) {
	for _, fault := range []string{"claim_update", "claim_outbox", "report_update", "report_outbox", "inventory_insert", "inventory_ids", "malformed_models"} {
		t.Run(fault, func(t *testing.T) {
			f := inspectionHarness(t)
			old := inspectionInventory()
			old.Runtimes[0].Version = "last-good"
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.RecordInventoryOn(f.ctx, tx, 7, 1, f.session.ID, 1, old) }))
			request, err := f.request("RUNTIMES")
			require.NoError(t, err)
			claim := fault == "claim_update" || fault == "claim_outbox"
			if !claim {
				_, err = f.pull()
				require.NoError(t, err)
			}
			if fault == "malformed_models" {
				require.NoError(t, f.db.Table(f.m.names.Runtimes.String()).Where("1=1").Update("models_json", "invalid").Error)
			}
			before := f.read(t, request.ID)
			var effects int64
			require.NoError(t, f.db.Table("test_fleet_effects").Count(&effects).Error)
			switch fault {
			case "claim_update", "report_update":
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_inspection_update BEFORE UPDATE ON dir_fleet_inspections BEGIN SELECT RAISE(ABORT,'update failed'); END").Error)
			case "claim_outbox", "report_outbox":
				f.publisher.fail = true
			case "inventory_insert":
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_runtime BEFORE INSERT ON dir_fleet_machine_runtimes BEGIN SELECT RAISE(ABORT,'inventory failed'); END").Error)
			case "inventory_ids":
				f.m.ids = failedFleetIDs{}
			}
			if claim {
				_, err = f.pull()
			} else {
				_, err = f.report(request.ID, InspectionResult{Succeeded: true, Inventory: inspectionInventory()})
			}
			require.Error(t, err)
			require.Equal(t, before, f.read(t, request.ID))
			var after int64
			require.NoError(t, f.db.Table("test_fleet_effects").Count(&after).Error)
			require.Equal(t, effects, after)
			var runtime model.MachineRuntime
			require.NoError(t, f.db.Table(f.m.names.Runtimes.String()).Take(&runtime).Error)
			require.Equal(t, "last-good", runtime.Version)
		})
	}
}

func TestInspectionWatchdogUnknownAndNoLateResultOrAutomaticReplay(t *testing.T) {
	for _, fault := range []string{"timeout", "offline", "epoch", "revoked", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			f := inspectionHarness(t)
			request, err := f.request("RUNTIMES")
			require.NoError(t, err)
			_, err = f.pull()
			require.NoError(t, err)
			now := time.Now().UTC()
			if fault == "timeout" || fault == "outbox" {
				require.NoError(t, f.db.Table(f.m.names.Inspections.String()).Where("id=?", request.ID).Update("expires_at", now.Add(-time.Second)).Error)
			}
			if fault == "offline" {
				require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 1).Update("last_seen_at", now.Add(-time.Minute)).Error)
			}
			if fault == "epoch" {
				require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 1).Update("conn_epoch", 2).Error)
			}
			if fault == "revoked" {
				require.NoError(t, f.db.Table(f.m.names.Machines.String()).Where("id=?", 1).Updates(map[string]any{"state": "REVOKED", "name_key": nil}).Error)
			}
			before := f.read(t, request.ID)
			if fault == "outbox" {
				f.publisher.fail = true
			}
			err = f.m.expireInspections(f.ctx, f.db, 7, now)
			if fault == "outbox" {
				require.Error(t, err)
				require.Equal(t, before, f.read(t, request.ID))
				f.publisher.fail = false
				require.NoError(t, f.m.expireInspections(f.ctx, f.db, 7, now))
			} else {
				require.NoError(t, err)
			}
			row := f.read(t, request.ID)
			require.Equal(t, "ERROR", row.State)
			require.Equal(t, "RESULT_UNKNOWN", row.ReasonCode)
			require.Nil(t, row.ActiveKey)
			require.NoError(t, f.m.expireInspections(f.ctx, f.db, 7, now.Add(time.Minute)))
			require.Equal(t, row, f.read(t, request.ID))
			_, err = f.report(request.ID, InspectionResult{Succeeded: true, Inventory: inspectionInventory()})
			require.Error(t, err)
		})
	}
}

func TestInspectionHTTPUsesRealLifecycleAndCursor(t *testing.T) {
	f := inspectionHarness(t)
	router := fleetBusinessRouter(t, f.m)
	response := fleetHTTPRequest(router, http.MethodPost, "/machines/1/inspections", `{"kind":"RUNTIMES"}`, nil)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var created struct {
		Data InspectionView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
	response = fleetHTTPRequest(router, http.MethodPost, "/machines/1/inspections", `{"kind":"CAPABILITIES"}`, nil)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	response = fleetHTTPRequest(router, http.MethodGet, "/machines/1/inspections?limit=1", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page struct {
		Data inspectionPage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Data.Items, 1)
	require.True(t, page.Data.HasMore)
	response = fleetHTTPRequest(router, http.MethodGet, "/machines/1/inspections?limit=1&cursor="+page.Data.NextCursor, "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fence := fmt.Sprintf(`"session_id":"%s","conn_epoch":1`, f.session.ID)
	response = fleetHTTPRequest(router, http.MethodPost, "/machines/1/inspection-pulls", "{"+fence+"}", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "RUNNING")
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/inspections/"+created.Data.ID.String()+"/result", "{"+fence+`,"succeeded":true,"inventory":{"runtimes":[],"tools":[]}}`, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "READY")
	response = fleetHTTPRequest(router, http.MethodPost, "/machines/1/inspection-pulls", "{"+fence+"}", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var unsupported struct {
		Data []InspectionView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &unsupported))
	require.Len(t, unsupported.Data, 1)
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/inspections/"+unsupported.Data[0].ID.String()+"/result", "{"+fence+`,"succeeded":false,"reason_code":"UNSUPPORTED_INSPECTION_KIND"}`, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "ERROR")
	require.Contains(t, response.Body.String(), "UNSUPPORTED_INSPECTION_KIND")
	for _, path := range []string{"/machines/1/inspections?cursor=bad", "/machines/9999999999999999999/inspections"} {
		response = fleetHTTPRequest(router, http.MethodGet, path, "", nil)
		require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	}
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	response = fleetHTTPRequest(router, http.MethodGet, "/machines/1/inspections", "", nil)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	response = fleetHTTPRequest(router, http.MethodPost, "/machines/1/inspections", `{"kind":"MODELS"}`, nil)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
}

func TestInspectionWatchdogUsesTenantBoundedPagesAndPreservesFreshRequests(t *testing.T) {
	f := inspectionHarness(t)
	request, err := f.request("RUNTIMES")
	require.NoError(t, err)
	require.NoError(t, f.m.expireInspections(f.ctx, f.db, 7, time.Now().UTC()))
	require.Equal(t, "QUEUED", f.read(t, request.ID).State)
	// Terminal records and other tenants are never reprocessed by this tenant job.
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.expireInspectionOn(f.ctx, tx, 7, 1, request.ID, time.Now().UTC().Add(66*time.Second))
	}))
	seedFleetMachine(t, f.m, f.db, 2, 8, 102, "OFFLINE", nil)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 101; i++ {
		active := 1
		machine := guid.ID(100 + i)
		seedFleetMachine(t, f.m, f.db, machine, 7, guid.ID(10000+i), "OFFLINE", nil)
		require.NoError(t, query.BindInspections(f.db, f.m.names).WithContext(f.ctx).Create(&model.Inspection{ID: guid.ID(1000 + i), TenantID: 7, MachineID: machine, RequestedBy: 101, Kind: "RUNTIMES", State: "QUEUED", ActiveKey: &active, SessionID: f.session.ID, ConnEpoch: 1, ExpiresAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now}))
	}
	active := 1
	require.NoError(t, query.BindInspections(f.db, f.m.names).WithContext(f.ctx).Create(&model.Inspection{ID: 2000, TenantID: 8, MachineID: 2, RequestedBy: 102, Kind: "RUNTIMES", State: "QUEUED", ActiveKey: &active, SessionID: 2, ConnEpoch: 1, ExpiresAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, f.m.JobHandlers()[0].Run(f.ctx, jobs.Job{TenantID: 7}))
	require.Equal(t, "QUEUED", f.read(t, 2000).State)
	for i := 0; i < 101; i++ {
		require.Equal(t, "ERROR", f.read(t, guid.ID(1000+i)).State)
	}
}

func TestInspectionResultsRejectUntrustedFactsAndSessions(t *testing.T) {
	for _, fault := range []string{"zero_id", "failed_inventory", "unknown_reason", "success_reason", "invalid_inventory", "pending", "missing", "wrong_session", "stale_epoch", "expired"} {
		t.Run(fault, func(t *testing.T) {
			f := inspectionHarness(t)
			request, err := f.request("RUNTIMES")
			require.NoError(t, err)
			if fault != "pending" {
				_, err = f.pull()
				require.NoError(t, err)
			}
			in := InspectionResult{Succeeded: true, Inventory: inspectionInventory()}
			id := request.ID
			switch fault {
			case "zero_id":
				id = 0
			case "failed_inventory":
				in.Succeeded = false
				in.ReasonCode = "INSPECTION_FAILED"
			case "unknown_reason":
				in = InspectionResult{ReasonCode: "raw secret failure"}
			case "success_reason":
				in.ReasonCode = "INSPECTION_FAILED"
			case "invalid_inventory":
				in.Inventory.Runtimes[0].Version = ""
			case "missing":
				id = 999
			case "wrong_session":
				f.session.ID++
			case "stale_epoch":
				f.session.ConnEpoch++
			case "expired":
				require.NoError(t, f.db.Table(f.m.names.Inspections.String()).Where("id=?", id).Update("expires_at", time.Now().UTC().Add(-time.Second)).Error)
			}
			before := f.read(t, request.ID)
			_, err = f.report(id, in)
			require.Error(t, err)
			require.Equal(t, before, f.read(t, request.ID))
		})
	}
}

func TestInspectionHTTPFailureContractsAndIsolation(t *testing.T) {
	for _, fault := range []string{"create_overflow", "pull_overflow", "report_machine_overflow", "report_id_overflow", "pull_stale", "report_pending", "unavailable", "create_query", "pull_query", "report_query", "list_query", "list_unauthorized", "create_unauthorized"} {
		t.Run(fault, func(t *testing.T) {
			f := inspectionHarness(t)
			request, err := f.request("RUNTIMES")
			require.NoError(t, err)
			router := fleetBusinessRouter(t, f.m)
			method, path, body := http.MethodGet, "/machines/1/inspections", ""
			status := http.StatusServiceUnavailable
			headers := map[string]string{}
			fence := fmt.Sprintf(`"session_id":"%s","conn_epoch":1`, f.session.ID)
			switch fault {
			case "create_overflow":
				method, path, body, status = http.MethodPost, "/machines/9999999999999999999/inspections", `{"kind":"RUNTIMES"}`, 422
			case "pull_overflow":
				method, path, body, status = http.MethodPost, "/machines/9999999999999999999/inspection-pulls", "{"+fence+"}", 422
			case "report_machine_overflow", "report_id_overflow":
				method, path, body, status = http.MethodPut, "/machines/9999999999999999999/inspections/1/result", "{"+fence+`,"succeeded":false,"reason_code":"INSPECTION_FAILED"}`, 422
				if fault == "report_id_overflow" {
					path = "/machines/1/inspections/9999999999999999999/result"
				}
			case "pull_stale":
				method, path, body, status = http.MethodPost, "/machines/1/inspection-pulls", `{"session_id":"999","conn_epoch":1}`, 409
			case "report_pending":
				method, path, body, status = http.MethodPut, "/machines/1/inspections/"+request.ID.String()+"/result", "{"+fence+`,"succeeded":false,"reason_code":"INSPECTION_FAILED"}`, 409
			case "unavailable":
				f.m.runtime = nil
			case "create_query", "pull_query", "report_query", "list_query":
				require.NoError(t, f.db.Exec("DROP TABLE "+f.m.names.Inspections.String()).Error)
				if fault == "create_query" {
					method, body = http.MethodPost, `{"kind":"RUNTIMES"}`
				}
				if fault == "pull_query" {
					method, path, body = http.MethodPost, "/machines/1/inspection-pulls", "{"+fence+"}"
				}
				if fault == "report_query" {
					method, path, body = http.MethodPut, "/machines/1/inspections/"+request.ID.String()+"/result", "{"+fence+`,"succeeded":false,"reason_code":"INSPECTION_FAILED"}`
				}
			case "list_unauthorized", "create_unauthorized":
				headers["Authorization"], status = "", 401
				if fault == "create_unauthorized" {
					method, body = http.MethodPost, `{"kind":"MODELS"}`
				}
			}
			response := fleetHTTPRequest(router, method, path, body, headers)
			require.Equal(t, status, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), "no such table")
		})
	}
}

func TestInspectionExpiryStorageFailuresAndTerminalNoop(t *testing.T) {
	for _, fault := range []string{"missing_machine", "missing_request", "query", "update", "terminal"} {
		t.Run(fault, func(t *testing.T) {
			f := inspectionHarness(t)
			request, err := f.request("RUNTIMES")
			require.NoError(t, err)
			machine, id := guid.ID(1), request.ID
			if fault == "missing_machine" {
				machine = 999
			}
			if fault == "missing_request" {
				id = 999
			}
			if fault == "query" {
				require.NoError(t, f.db.Exec("DROP TABLE "+f.m.names.Inspections.String()).Error)
				require.Error(t, f.m.expireInspections(f.ctx, f.db, 7, time.Now().UTC()))
				return
			}
			if fault == "update" {
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_expiry BEFORE UPDATE ON dir_fleet_inspections BEGIN SELECT RAISE(ABORT,'expiry failed'); END").Error)
			}
			if fault == "terminal" {
				_, err = f.pull()
				require.NoError(t, err)
				_, err = f.report(id, InspectionResult{ReasonCode: "INSPECTION_FAILED"})
				require.NoError(t, err)
			}
			before := f.read(t, request.ID)
			err = f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.expireInspectionOn(f.ctx, tx, 7, machine, id, time.Now().UTC().Add(66*time.Second))
			})
			if fault == "terminal" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, before, f.read(t, request.ID))
		})
	}
}
