package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"math"
	"net/http"
	"testing"
	"time"
)

func TestVersionOnlyInventoryHTTPPreservesModelsAndFullInventoryCanReplaceThem(t *testing.T) {
	f := inspectionHarness(t)
	old := inspectionInventory()
	old.Runtimes[0].Models = []string{"last-confirmed-model"}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.RecordInventoryOn(f.ctx, tx, 7, 1, f.session.ID, 1, old) }))
	router := fleetBusinessRouter(t, f.m)
	body := fmt.Sprintf(`{"session_id":"%s","conn_epoch":1,"observation_mode":"RUNTIME_VERSIONS","inventory":{"runtimes":[{"kind":"CODEX","version":"new-version","executable_path":"/actual/codex","models":["unproven-model"]}],"tools":[]}}`, f.session.ID)
	response := fleetHTTPRequest(router, http.MethodPut, "/machines/1/inventory", body, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var runtime model.MachineRuntime
	require.NoError(t, f.db.Table(f.m.names.Runtimes.String()).Take(&runtime).Error)
	require.Equal(t, "new-version", runtime.Version)
	require.JSONEq(t, `["last-confirmed-model"]`, runtime.ModelsJSON)
	body = fmt.Sprintf(`{"session_id":"%s","conn_epoch":1,"observation_mode":"FULL","inventory":{"runtimes":[{"kind":"CODEX","version":"new-version","executable_path":"/actual/codex","models":["new-observed-model"]}],"tools":[]}}`, f.session.ID)
	response = fleetHTTPRequest(router, http.MethodPut, "/machines/1/inventory", body, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	runtime = model.MachineRuntime{}
	require.NoError(t, f.db.Table(f.m.names.Runtimes.String()).Take(&runtime).Error)
	require.JSONEq(t, `["new-observed-model"]`, runtime.ModelsJSON)
}

func TestInventoryStorageAndIDFailuresPreserveLastGoodFactsAndHeartbeat(t *testing.T) {
	for _, fault := range []string{"missing_ids", "runtime_id_error", "runtime_id_zero", "tool_id_error", "tool_id_zero", "runtime_delete", "tool_delete", "runtime_insert", "tool_insert"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _ := machineHarness(t)
			var session SessionFact
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				session, err = m.OpenSessionOn(ctx, tx, 7, 1, "inventory-fault")
				return err
			}))
			lastGood := Inventory{Runtimes: []RuntimeObservation{{Kind: "CODEX", Version: "old", ExecutablePath: "/old/codex", Models: []string{"old-model"}}}, Tools: []ToolObservation{{Name: "node", Version: "old", ExecutablePath: "/old/node"}}}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				return m.RecordInventoryOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch, lastGood)
			}))
			old := time.Now().UTC().Add(-2 * time.Second).Truncate(time.Millisecond)
			require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Update("last_seen_at", old).Error)
			require.NoError(t, db.Table(m.names.Sessions.String()).Where("id = ?", session.ID).Update("last_seen_at", old).Error)
			next := Inventory{Runtimes: []RuntimeObservation{{Kind: "CODEX", Version: "new", ExecutablePath: "/new/codex", Models: []string{"new-model"}}}, Tools: []ToolObservation{{Name: "node", Version: "new", ExecutablePath: "/new/node"}}}
			switch fault {
			case "missing_ids":
				m.ids = nil
			case "runtime_id_error", "tool_id_error":
				m.ids = failedFleetIDs{err: errors.New("test identifier allocation failed")}
			case "runtime_id_zero", "tool_id_zero":
				m.ids = failedFleetIDs{}
			case "runtime_delete":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_inventory BEFORE DELETE ON dir_fleet_machine_runtimes BEGIN SELECT RAISE(ABORT, 'test delete failed'); END").Error)
			case "tool_delete":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_inventory BEFORE DELETE ON dir_fleet_machine_tools BEGIN SELECT RAISE(ABORT, 'test delete failed'); END").Error)
			case "runtime_insert":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_inventory BEFORE INSERT ON dir_fleet_machine_runtimes BEGIN SELECT RAISE(ABORT, 'test insert failed'); END").Error)
			case "tool_insert":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_inventory BEFORE INSERT ON dir_fleet_machine_tools BEGIN SELECT RAISE(ABORT, 'test insert failed'); END").Error)
			}
			if fault == "tool_id_error" || fault == "tool_id_zero" {
				next.Runtimes = nil
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				return m.RecordInventoryOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch, next)
			}))
			runtimes, err := query.BindRuntimes(db, m.names).WithContext(ctx).Find()
			require.NoError(t, err)
			require.Len(t, runtimes, 1)
			require.Equal(t, "old", runtimes[0].Version)
			require.Equal(t, `["old-model"]`, runtimes[0].ModelsJSON)
			tools, err := query.BindTools(db, m.names).WithContext(ctx).Find()
			require.NoError(t, err)
			require.Len(t, tools, 1)
			require.Equal(t, "old", tools[0].Version)
			machine, err := query.BindMachines(db, m.names).WithContext(ctx).First()
			require.NoError(t, err)
			require.NotNil(t, machine.LastSeenAt)
			require.True(t, machine.LastSeenAt.Equal(old), "heartbeat renewal is rolled back with failed inventory")
		})
	}
}

func TestInventoryRequiresCurrentSessionAndPreservesLastGoodOnFailure(t *testing.T) {
	m, db, ctx, p := machineHarness(t)
	var session SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		session, err = m.OpenSessionOn(ctx, tx, 7, 1, "process")
		return err
	}))
	in := Inventory{Runtimes: []RuntimeObservation{{Kind: "CODEX", Version: "1.2.3", ExecutablePath: "D:/tools/codex", Models: []string{"observed-model"}}}, Tools: []ToolObservation{{Name: "node", Version: "22.0", ExecutablePath: "D:/node.exe"}}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.RecordInventoryOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch, in) }))
	read := func() {
		t.Helper()
		rows, err := query.BindRuntimes(db, m.names).WithContext(ctx).Find()
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, `["observed-model"]`, rows[0].ModelsJSON)
	}
	read()
	p.fail = true
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		return m.RecordInventoryOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch, Inventory{})
	}))
	read()
	p.fail = false
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.RecordInventoryOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch+1, Inventory{})
	}), ErrSessionStale)
	read()
	in.Runtimes[0].Models = []string{"duplicate", "duplicate"}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.RecordInventoryOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch, in) }), ErrInvalidArgument)
	read()
}
func TestTokenCostsAreExactAndUnknownIsNotFree(t *testing.T) {
	for _, tc := range []struct {
		name           string
		input, output  int64
		price          *TokenPrice
		want           int64
		known, invalid bool
	}{
		{"unknown", 1, 1, nil, 0, false, false},
		{"explicit_free", 10, 20, &TokenPrice{}, 0, true, false},
		{"ceil_combined_once", 1, 1, &TokenPrice{InputMicros: 1, OutputMicros: 1}, 1, true, false},
		{"one_million_tokens", 1000000, 1000000, &TokenPrice{InputMicros: 1200000, OutputMicros: 4000000}, 5200000, true, false},
		{"large_intermediate", math.MaxInt64, 0, &TokenPrice{InputMicros: 1}, 9223372036855, true, false},
		{"overflow", math.MaxInt64, math.MaxInt64, &TokenPrice{InputMicros: math.MaxInt64, OutputMicros: math.MaxInt64}, 0, false, true},
		{"negative_tokens", -1, 1, nil, 0, false, true},
		{"negative_price", 0, 0, &TokenPrice{InputMicros: -1}, 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, known, err := EstimateTokenCost(tc.input, tc.output, tc.price)
			if tc.invalid {
				require.ErrorIs(t, err, ErrInvalidArgument)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, value)
			require.Equal(t, tc.known, known)
		})
	}
}
func TestPriceLookupTenantScopeAndUnknown(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	require.NoError(t, query.BindPrices(db, m.names).WithContext(ctx).Create(&model.ModelPrice{ID: 10, TenantID: 7, Runtime: "CODEX", Model: "model", InputMicros: 100, OutputMicros: 200, UpdatedAt: time.Now().UTC()}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		price, err := m.ModelPriceOn(ctx, tx, 7, "CODEX", "model")
		require.NoError(t, err)
		require.Equal(t, &TokenPrice{100, 200}, price)
		price, err = m.ModelPriceOn(ctx, tx, 8, "CODEX", "model")
		require.NoError(t, err)
		require.Nil(t, price)
		_, err = m.ModelPriceOn(ctx, tx, 7, "unknown", "model")
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
}

func TestInventoryRejectsUnboundedOrDuplicateFacts(t *testing.T) {
	for _, in := range []Inventory{
		{Runtimes: make([]RuntimeObservation, 3)},
		{Tools: make([]ToolObservation, 33)},
		{Runtimes: []RuntimeObservation{{Kind: "UNSUPPORTED", Version: "1", ExecutablePath: "/tool"}}},
		{Tools: []ToolObservation{{Name: "arbitrary-command", Version: "1", ExecutablePath: "/tool"}}},
		{Tools: []ToolObservation{{Name: "node", Version: "1", ExecutablePath: "/node"}, {Name: "node", Version: "2", ExecutablePath: "/other-node"}}},
		{Runtimes: []RuntimeObservation{{Kind: "CODEX", Version: "1", ExecutablePath: "/tool", Models: []string{"bad\nmodel"}}}},
	} {
		require.ErrorIs(t, validateInventory(in), ErrInvalidArgument)
	}
	m, db, ctx, _ := machineHarness(t)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := (*Module)(nil).ModelPriceOn(ctx, tx, 7, "CODEX", "m"); return err }), ErrUnavailable)
	_, err := m.ModelPriceOn(ctx, db, 7, "CODEX", "m")
	require.ErrorIs(t, err, ErrInvalidArgument)
}
