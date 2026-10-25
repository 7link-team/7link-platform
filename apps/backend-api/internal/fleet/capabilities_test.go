package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCapabilitiesQueryFailureDoesNotExposePartialObservations(t *testing.T) {
	for _, table := range []string{"fleet_machine_runtimes", "fleet_machine_tools", "fleet_workdirs", "fleet_slot_reservations"} {
		t.Run(table, func(t *testing.T) {
			m, db, ctx, _, _ := enrollmentHarness(t)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				session, err := m.OpenSessionOn(ctx, tx, 7, 1, "fault")
				if err != nil {
					return err
				}
				return m.RecordInventoryOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch, Inventory{Runtimes: []RuntimeObservation{{Kind: "CODEX", Version: "1", ExecutablePath: "/private/runtime", Models: []string{"private-model"}}}})
			}))
			router := fleetBusinessRouter(t, m)
			require.NoError(t, db.Exec(fmt.Sprintf("DROP TABLE dir_%s", table)).Error)
			response := fleetHTTPRequest(router, http.MethodGet, "/machines/1/capabilities", "", nil)
			require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), "/private/runtime")
			require.NotContains(t, response.Body.String(), "private-model")
			require.NotContains(t, response.Body.String(), "no such table")
		})
	}
}

func TestCapabilitiesHTTPProjectsOnlyOwnedObservationsAndActiveSlots(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	seedFleetMachine(t, m, db, 2, 8, 102, "OFFLINE", nil)
	var session SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		session, err = m.OpenSessionOn(ctx, tx, 7, 1, "capabilities")
		if err != nil {
			return err
		}
		return m.RecordInventoryOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch, Inventory{Runtimes: []RuntimeObservation{{Kind: "CODEX", Version: "1.2", ExecutablePath: "/bin/codex", Models: []string{"observed-model"}}}, Tools: []ToolObservation{{Name: "node", Version: "24", ExecutablePath: "/bin/node"}}})
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.ReserveSlotOn(ctx, tx, 7, 1, 900)
		if err != nil {
			return err
		}
		if err = m.ReleaseSlotOn(ctx, tx, 7, 1, 900); err != nil {
			return err
		}
		_, err = m.ReserveSlotOn(ctx, tx, 7, 1, 901)
		return err
	}))
	now := time.Now().UTC()
	require.NoError(t, query.BindWorkdirs(db, m.names).WithContext(ctx).Create(&model.Workdir{ID: 50, TenantID: 7, MachineID: 1, ProjectID: 10, RepositoryID: 11, Path: "/work/owned", PathKey: strings64(), CreatedAt: now}))
	require.NoError(t, query.BindRuntimes(db, m.names).WithContext(ctx).Create(&model.MachineRuntime{ID: 51, TenantID: 8, MachineID: 2, Kind: "CLAUDE_CODE", Version: "foreign", ExecutablePath: "/private/foreign", ModelsJSON: `["foreign-model"]`, InspectedAt: now}))
	router := fleetBusinessRouter(t, m)
	response := fleetHTTPRequest(router, http.MethodGet, "/machines/1/capabilities", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var capabilities struct {
		Data MachineCapabilities `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &capabilities))
	require.Len(t, capabilities.Data.Runtimes, 1)
	require.Equal(t, []string{"observed-model"}, capabilities.Data.Runtimes[0].Models)
	require.Equal(t, "/bin/codex", capabilities.Data.Runtimes[0].Path)
	require.Len(t, capabilities.Data.Tools, 1)
	require.Equal(t, "node", capabilities.Data.Tools[0].Name)
	require.Len(t, capabilities.Data.Workdirs, 1)
	require.Equal(t, guid.ID(11), capabilities.Data.Workdirs[0].RepositoryID)
	require.Equal(t, "/work/owned", capabilities.Data.Workdirs[0].Path)
	require.Equal(t, []MachineSlotView{{ExecutionID: 901, SlotNo: 1}}, capabilities.Data.ActiveSlots)
	require.Equal(t, "UNAVAILABLE", capabilities.Data.RemoteAccess)
	require.NotContains(t, response.Body.String(), "foreign")
	response = fleetHTTPRequest(router, http.MethodGet, "/tenants/7/model-gateway", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var gateway struct {
		Data ModelGatewayStatus `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &gateway))
	require.False(t, gateway.Data.Available)
	require.Equal(t, "RUNNER_GATEWAY_UNAVAILABLE", gateway.Data.Reason)
	require.Empty(t, gateway.Data.Models, "observed model names cannot imply a functioning gateway")

	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	response = fleetHTTPRequest(router, http.MethodGet, "/machines/1/capabilities", "", nil)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "/work/owned")
}

func TestCapabilitiesHTTPMalformedObservationFailsWithoutLeakingPartialData(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	now := time.Now().UTC()
	require.NoError(t, query.BindRuntimes(db, m.names).WithContext(ctx).Create(&model.MachineRuntime{ID: 50, TenantID: 7, MachineID: 1, Kind: "CODEX", Version: "1", ExecutablePath: "/private/path", ModelsJSON: `invalid-json`, InspectedAt: now}))
	router := fleetBusinessRouter(t, m)
	response := fleetHTTPRequest(router, http.MethodGet, "/machines/1/capabilities", "", nil)
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "/private/path")
	response = fleetHTTPRequest(router, http.MethodGet, "/machines/999/capabilities", "", nil)
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.capabilitiesOn(ctx, tx, 8, 1)
		require.ErrorIs(t, err, ErrMachineNotFound)
		return nil
	}))
}

func TestCapabilitiesRequireCurrentHumanNotOnlyCachedRoutePermission(t *testing.T) {
	m, _, _, _, _ := enrollmentHarness(t)
	router := fleetBusinessRouter(t, m)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	response := fleetHTTPRequest(router, http.MethodGet, "/machines/1/capabilities", "", nil)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "runtimes")
}
