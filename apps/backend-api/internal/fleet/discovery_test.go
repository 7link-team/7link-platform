package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"testing"
	"time"
)

func discoveryFacts() []RuntimeDiscovery {
	return []RuntimeDiscovery{{Runtime: "CODEX", State: "READY", Source: "CLI_CACHE", Models: []string{"gpt-5.2", "gpt-5.3"}}, {Runtime: "CLAUDE_CODE", State: "UNSUPPORTED", Source: "LOCAL_CONFIG", Reason: "NO_LOCAL_MODEL_LIST_OR_CLI_LIST_COMMAND"}}
}

func TestDiscoveryCapabilitiesExposeLatestRegisteredScopesWithCurrentAuthority(t *testing.T) {
	f := inspectionHarness(t)
	for i := 1; i <= 2; i++ {
		require.NoError(t, f.db.Table(f.m.names.Workdirs.String()).Create(&model.Workdir{ID: guid.ID(100 + i), TenantID: 7, MachineID: 1, ProjectID: 20, RepositoryID: guid.ID(30 + i), Path: fmt.Sprintf("/repo%d", i), PathKey: strings.Repeat(fmt.Sprint(i), 64)}).Error)
	}
	facts := discoveryFacts()
	facts[0].Models = nil
	facts[0].MCP = []string{"home-reader"}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, 1, "CAPABILITIES", facts)
		return err
	}))
	for i := 1; i <= 2; i++ {
		jobs, err := f.pull()
		require.NoError(t, err)
		require.Len(t, jobs, 1)
		facts[0].MCP = []string{fmt.Sprintf("repo%d-reader", i)}
		_, err = f.report(jobs[0].ID, InspectionResult{Succeeded: true, Discovery: facts})
		require.NoError(t, err)
	}
	var latest InspectionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		latest, err = f.m.requestScopedInspectionOn(f.ctx, tx, 7, 1, "CAPABILITIES", 101)
		return err
	}))
	_, err := f.pull()
	require.NoError(t, err)
	facts[0].MCP = []string{"repo1-new-reader"}
	_, err = f.report(latest.ID, InspectionResult{Succeeded: true, Discovery: facts})
	require.NoError(t, err)
	var got MachineCapabilities
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		got, err = f.m.capabilitiesOn(f.ctx, tx, 7, 1)
		return err
	}))
	require.Len(t, got.Discoveries, 3)
	want := map[string]string{"home": "home-reader", "101": "repo1-new-reader", "102": "repo2-reader"}
	for _, receipt := range got.Discoveries {
		require.Equal(t, []string{want[receipt.ScopeKey]}, receipt.Discovery[0].MCP)
		delete(want, receipt.ScopeKey)
		if receipt.ScopeKey == "101" {
			require.Equal(t, latest.ID, receipt.ID)
			require.Equal(t, "/repo1", receipt.WorkdirPath)
		}
	}
	require.Empty(t, want)
	f.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	r := fleetHTTPRequest(fleetBusinessRouter(t, f.m), http.MethodGet, "/machines/1/capabilities", "", nil)
	require.Equal(t, http.StatusForbidden, r.Code)
	require.NotContains(t, r.Body.String(), "repo1-new-reader")
}

func TestDiscoveryAutomaticWorkdirFailuresDoNotCreatePartialJobs(t *testing.T) {
	for _, fault := range []string{"workdir_query", "inspection_count", "ids_missing", "ids_error", "ids_zero", "insert"} {
		t.Run(fault, func(t *testing.T) {
			f := inspectionHarness(t)
			require.NoError(t, f.db.Table(f.m.names.Workdirs.String()).Create(&model.Workdir{ID: 101, TenantID: 7, MachineID: 1, ProjectID: 20, RepositoryID: 30, Path: "/repo", PathKey: strings.Repeat("a", 64)}).Error)
			switch fault {
			case "ids_missing":
				f.m.ids = nil
			case "ids_error":
				f.m.ids = failedFleetIDs{err: errors.New("source failed")}
			case "ids_zero":
				f.m.ids = failedFleetIDs{}
			case "insert":
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_auto_inspection BEFORE INSERT ON "+f.m.names.Inspections.String()+" BEGIN SELECT RAISE(ABORT,'insert failed'); END").Error)
			default:
				target := f.m.names.Workdirs.String()
				if fault == "inspection_count" {
					target = f.m.names.Inspections.String()
				}
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("reject_auto_query", func(db *gorm.DB) {
					if db.Statement.Table == target {
						db.AddError(errors.New("query failed"))
					}
				}))
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				job, err := f.m.nextWorkdirInspectionOn(f.ctx, tx, 7, 1, f.session.ID, 1)
				require.Nil(t, job)
				return err
			})
			require.Error(t, err)
			if fault == "workdir_query" || fault == "inspection_count" {
				require.NoError(t, f.db.Callback().Query().Remove("reject_auto_query"))
			}
			var count int64
			require.NoError(t, f.db.Table(f.m.names.Inspections.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestDiscoveryValidationRejectsUntrustedAndOversizedFactsWithoutReplacingReceipt(t *testing.T) {
	f := inspectionHarness(t)
	var original InspectionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		original, err = f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, 1, "MODELS", discoveryFacts())
		return err
	}))
	for _, kind := range []string{"MODELS", "CAPABILITIES"} {
		for _, fault := range []string{"checked_at", "last_success_at", "error_payload", "invalid_mcp", "invalid_skill", "oversized_payload"} {
			t.Run(kind+"/"+fault, func(t *testing.T) {
				facts := discoveryFacts()
				if kind == "CAPABILITIES" {
					facts[0].Models = nil
				}
				now := time.Now().UTC()
				switch fault {
				case "checked_at":
					facts[0].CheckedAt = &now
				case "last_success_at":
					facts[0].LastSuccessAt = &now
				case "error_payload":
					facts[0].State = "ERROR"
					facts[0].Reason = "untrusted diagnostics"
				case "invalid_mcp":
					facts[0].MCP = []string{"contains whitespace"}
				case "invalid_skill":
					facts[0].Skills = []DiscoveredSkill{{Name: "skill", SHA256: "not-a-hash"}}
				case "oversized_payload":
					facts[0].Models = nil
					for i := 0; i < 2000; i++ {
						facts[0].Skills = append(facts[0].Skills, DiscoveredSkill{Name: strings.Repeat("x", 200), SHA256: strings.Repeat("a", 64)})
						facts[0].MCP = append(facts[0].MCP, strings.Repeat("m", 200))
					}
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					_, err := f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, 1, kind, facts)
					return err
				})
				require.ErrorIs(t, err, ErrInvalidArgument)
				var receipts []model.Inspection
				require.NoError(t, f.db.Table(f.m.names.Inspections.String()).Find(&receipts).Error)
				require.Len(t, receipts, 1)
				require.Equal(t, original, inspectionView(&receipts[0]))
			})
		}
	}
}

func TestDiscoveryAutomaticReportsFencingRetentionAndHTTP(t *testing.T) {
	f := inspectionHarness(t)
	router := fleetBusinessRouter(t, f.m)
	body := map[string]any{"session_id": f.session.ID, "conn_epoch": f.session.ConnEpoch, "kind": "MODELS", "discovery": discoveryFacts()}
	b, e := json.Marshal(body)
	require.NoError(t, e)
	for i := 0; i < 3; i++ {
		r := fleetHTTPRequest(router, http.MethodPut, "/machines/1/discovery", string(b), map[string]string{"Authorization": "Bearer runner"})
		require.Equal(t, 200, r.Code, r.Body.String())
	}
	q := query.BindInspections(f.db, f.m.names)
	n, e := q.WithContext(f.ctx).Count()
	require.NoError(t, e)
	require.Equal(t, int64(1), n, "periodic reports must not grow without bound")
	r := fleetHTTPRequest(router, http.MethodGet, "/machines/1/capabilities", "", nil)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), "gpt-5.3")
	require.Contains(t, r.Body.String(), "UNSUPPORTED")
	body["conn_epoch"] = 2
	b, e = json.Marshal(body)
	require.NoError(t, e)
	r = fleetHTTPRequest(router, http.MethodPut, "/machines/1/discovery", string(b), map[string]string{"Authorization": "Bearer runner"})
	require.Equal(t, 409, r.Code)
	r = fleetHTTPRequest(router, http.MethodPut, "/machines/9999999999999999999/discovery", string(b), map[string]string{"Authorization": "Bearer runner"})
	require.Equal(t, 422, r.Code)
	r = fleetHTTPRequest(router, http.MethodPut, "/machines/99/discovery", string(b), map[string]string{"Authorization": "Bearer runner"})
	require.Equal(t, 404, r.Code)
	for _, kind := range []string{"OTHER", "MODELS"} {
		require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
			_, e := f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, 1, kind, nil)
			return e
		}))
	}
}
func TestDiscoveryRegisteredScopeFirstSessionAndManualCoalescing(t *testing.T) {
	f := inspectionHarness(t)
	q := query.BindWorkdirs(f.db, f.m.names)
	for i := 1; i <= 2; i++ {
		require.NoError(t, q.WithContext(f.ctx).Create(&model.Workdir{ID: guid.ID(100 + i), TenantID: 7, MachineID: 1, ProjectID: 20, RepositoryID: guid.ID(30 + i), Path: fmt.Sprintf("C:/repo%d", i), PathKey: strings.Repeat(fmt.Sprint(i), 64)}))
	}
	for i := 1; i <= 2; i++ {
		rows, e := f.pull()
		require.NoError(t, e)
		require.Len(t, rows, 1)
		require.Equal(t, fmt.Sprint(100+i), rows[0].ScopeKey)
		require.Equal(t, fmt.Sprintf("C:/repo%d", i), rows[0].WorkdirPath)
		facts := discoveryFacts()
		facts[0].Models = nil
		facts[0].Skills = []DiscoveredSkill{{Name: "test", SHA256: strings.Repeat("a", 64)}}
		facts[0].MCP = []string{"reader"}
		_, e = f.report(rows[0].ID, InspectionResult{Succeeded: true, Discovery: facts})
		require.NoError(t, e)
	}
	rows, e := f.pull()
	require.NoError(t, e)
	require.Empty(t, rows, "each scope gets only one first-session attempt")
	var out InspectionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = f.m.requestScopedInspectionOn(f.ctx, tx, 7, 1, "CAPABILITIES", 101)
		return e
	}))
	require.Equal(t, "101", out.ScopeKey)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		again, e := f.m.requestScopedInspectionOn(f.ctx, tx, 7, 1, "CAPABILITIES", 101)
		require.Equal(t, out, again)
		return e
	}))
	for _, scope := range []guid.ID{102, 999, -1} {
		require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
			_, e := f.m.requestScopedInspectionOn(f.ctx, tx, 7, 1, "CAPABILITIES", scope)
			return e
		}))
	}
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.requestScopedInspectionOn(f.ctx, tx, 7, 1, "MODELS", 101)
		return e
	}))
}
func TestDiscoveryValidationBoundsAndCapabilityCache(t *testing.T) {
	for _, change := range []func([]RuntimeDiscovery){
		func(v []RuntimeDiscovery) { v[0].Runtime = "OTHER" }, func(v []RuntimeDiscovery) { v[0].State = "UNKNOWN" }, func(v []RuntimeDiscovery) { v[0].Source = "CACHED" },
		func(v []RuntimeDiscovery) { v[0].Models = []string{"same", "same"} }, func(v []RuntimeDiscovery) { v[0].MCP = []string{"bad name"} },
		func(v []RuntimeDiscovery) {
			v[0].Models = nil
			v[0].Skills = []DiscoveredSkill{{Name: "skill", SHA256: "invalid"}}
		},
		func(v []RuntimeDiscovery) { v[0].Models = nil; v[0].MCP = make([]string, 2001) },
	} {
		facts := discoveryFacts()
		change(facts)
		require.Error(t, validateDiscovery("MODELS", facts))
	}
	f := inspectionHarness(t)
	facts := discoveryFacts()
	facts[0].Models = nil
	facts[0].MCP = []string{"reader"}
	facts[0].Skills = []DiscoveredSkill{{Name: "test", SHA256: strings.Repeat("a", 64)}}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, 1, "CAPABILITIES", facts)
		return e
	}))
	facts[0].State = "ERROR"
	facts[0].Reason = "LOCAL_CONFIG_UNREADABLE"
	facts[0].MCP = nil
	facts[0].Skills = nil
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		out, e := f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, 1, "CAPABILITIES", facts)
		require.NoError(t, e)
		require.Equal(t, "CACHED", out.Discovery[0].Source)
		require.Equal(t, []string{"reader"}, out.Discovery[0].MCP)
		return nil
	}))
}

func TestDiscoveryReasoningEvidenceIsValidatedAndCached(t *testing.T) {
	facts := discoveryFacts()
	facts[0].Reasoning = []ModelReasoning{{Model: facts[0].Models[0], Efforts: []string{"low", "high"}}}
	require.NoError(t, validateDiscovery("MODELS", facts))
	facts[0].Reasoning[0].Model = "unreported-model"
	require.Error(t, validateDiscovery("MODELS", facts))
	facts[0].Reasoning[0].Model = facts[0].Models[0]
	facts[0].Reasoning[0].Efforts = []string{"high", "high"}
	require.Error(t, validateDiscovery("MODELS", facts))
	facts[0].Reasoning[0].Efforts = []string{"high"}
	f := inspectionHarness(t)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, 1, "MODELS", facts)
		return e
	}))
	facts[0].Reasoning = nil
	facts[0].Models = nil
	facts[0].State, facts[0].Reason = "ERROR", "LOCAL_CONFIG_UNREADABLE"
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		out, e := f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, 1, "MODELS", facts)
		if e == nil {
			require.Equal(t, "CACHED", out.Discovery[0].Source)
			require.Equal(t, []string{"high"}, out.Discovery[0].Reasoning[0].Efforts)
			require.NotNil(t, out.Discovery[0].LastSuccessAt)
		}
		return e
	}))
}
func TestDiscoveryReceiptPerRuntimeCacheAndReplay(t *testing.T) {
	f := inspectionHarness(t)
	r, e := f.request("MODELS")
	require.NoError(t, e)
	_, e = f.pull()
	require.NoError(t, e)
	in := InspectionResult{Succeeded: true, Discovery: discoveryFacts()}
	ready, e := f.report(r.ID, in)
	require.NoError(t, e)
	require.Equal(t, "READY", ready.State)
	require.Equal(t, []string{"gpt-5.3", "gpt-5.2"}, ready.Discovery[0].Models)
	require.Equal(t, "UNSUPPORTED", ready.Discovery[1].State)
	replay, e := f.report(r.ID, in)
	require.NoError(t, e)
	require.Equal(t, ready, replay)
	r, e = f.request("MODELS")
	require.NoError(t, e)
	_, e = f.pull()
	require.NoError(t, e)
	in.Discovery[0].Models = nil
	in.Discovery[0].State = "UNSUPPORTED"
	in.Discovery[0].Reason = "NO_LOCAL_MODEL_LIST_OR_CLI_LIST_COMMAND"
	cached, e := f.report(r.ID, in)
	require.NoError(t, e)
	require.Equal(t, "CACHED", cached.Discovery[0].Source)
	require.Equal(t, ready.Discovery[0].Models, cached.Discovery[0].Models)
	require.Equal(t, ready.Discovery[0].LastSuccessAt, cached.Discovery[0].LastSuccessAt)
	require.Equal(t, "UNSUPPORTED", cached.Discovery[0].State)
	bad := discoveryFacts()
	bad[1].Runtime = "CODEX"
	require.ErrorIs(t, validateDiscovery("MODELS", bad), ErrInvalidArgument)
	bad = discoveryFacts()
	bad[0].Models = []string{"model with spaces"}
	require.ErrorIs(t, validateDiscovery("MODELS", bad), ErrInvalidArgument)
	bad = discoveryFacts()
	bad[0].Reason = "secret body"
	require.ErrorIs(t, validateDiscovery("MODELS", bad), ErrInvalidArgument)
	bad = discoveryFacts()
	bad[0].Skills = []DiscoveredSkill{{Name: "x", SHA256: "invalid"}}
	require.ErrorIs(t, validateDiscovery("CAPABILITIES", bad), ErrInvalidArgument)
}

func TestDiscoveryModelCatalogUsesNumericVersionOrder(t *testing.T) {
	f := inspectionHarness(t)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.RecordInventoryOn(f.ctx, tx, 7, 1, f.session.ID, 1, inspectionInventory())
	}))
	facts := discoveryFacts()
	facts[0].Models = []string{"gpt-5.9", "gpt-5.10", "gpt-5.2", "gpt-15.1", "gpt-5.10-mini", "gpt-5.010"}
	var receipt InspectionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		receipt, err = f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, 1, "MODELS", facts)
		return err
	}))
	expected := []string{"gpt-15.1", "gpt-5.10-mini", "gpt-5.10", "gpt-5.010", "gpt-5.9", "gpt-5.2"}
	require.Equal(t, expected, receipt.Discovery[0].Models)
	var capabilities MachineCapabilities
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		capabilities, err = f.m.capabilitiesOn(f.ctx, tx, 7, 1)
		return err
	}))
	for _, runtime := range capabilities.Runtimes {
		if runtime.Kind == "CODEX" {
			require.Equal(t, expected, runtime.Models, "the UI inventory must retain the same numeric catalog ordering")
			return
		}
	}
	t.Fatal("CODEX runtime is missing")
}

func TestDiscoveryFailuresPreserveCatalogSnapshotAndEffects(t *testing.T) {
	for _, fault := range []string{"ids_missing", "ids_error", "ids_zero", "prior_query", "prior_corrupt", "runtime_update", "snapshot_insert", "snapshot_delete", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			f := inspectionHarness(t)
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.RecordInventoryOn(f.ctx, tx, 7, 1, f.session.ID, 1, inspectionInventory())
			}))
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, err := f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, 1, "MODELS", discoveryFacts())
				return err
			}))
			switch fault {
			case "ids_missing":
				f.m.ids = nil
			case "ids_error":
				f.m.ids = failedFleetIDs{err: errors.New("source unavailable")}
			case "ids_zero":
				f.m.ids = failedFleetIDs{}
			case "prior_corrupt":
				require.NoError(t, f.db.Table(f.m.names.Inspections.String()).Where("1=1").Update("result_json", "invalid").Error)
			case "runtime_update":
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_discovery_runtime BEFORE UPDATE ON "+f.m.names.Runtimes.String()+" BEGIN SELECT RAISE(ABORT,'runtime update failed'); END").Error)
			case "snapshot_insert":
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_discovery_insert BEFORE INSERT ON "+f.m.names.Inspections.String()+" BEGIN SELECT RAISE(ABORT,'snapshot insert failed'); END").Error)
			case "snapshot_delete":
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_discovery_delete BEFORE DELETE ON "+f.m.names.Inspections.String()+" BEGIN SELECT RAISE(ABORT,'snapshot delete failed'); END").Error)
			case "outbox":
				f.publisher.fail = true
			}
			// Capture after corruption injection; a failed refresh must not erase
			// even the existing receipt needed to diagnose corrupt storage.
			var before []model.Inspection
			require.NoError(t, f.db.Table(f.m.names.Inspections.String()).Find(&before).Error)
			var effects int64
			require.NoError(t, f.db.Table("test_fleet_effects").Count(&effects).Error)
			if fault == "prior_query" {
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("reject_discovery_prior", func(db *gorm.DB) {
					if db.Statement.Table == f.m.names.Inspections.String() {
						db.AddError(errors.New("snapshot query failed"))
					}
				}))
			}
			facts := discoveryFacts()
			facts[0].Models = []string{"gpt-new"}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				_, err := f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, 1, "MODELS", facts)
				return err
			})
			require.Error(t, err)
			if fault == "prior_query" {
				require.NoError(t, f.db.Callback().Query().Remove("reject_discovery_prior"))
			}
			var after []model.Inspection
			require.NoError(t, f.db.Table(f.m.names.Inspections.String()).Find(&after).Error)
			require.Equal(t, before, after, "refresh failure must preserve the prior snapshot")
			var actualEffects int64
			require.NoError(t, f.db.Table("test_fleet_effects").Count(&actualEffects).Error)
			require.Equal(t, effects, actualEffects, "audit and outbox must roll back with the failed refresh")
			var runtime model.MachineRuntime
			require.NoError(t, f.db.Table(f.m.names.Runtimes.String()).Where("kind = ?", "CODEX").Take(&runtime).Error)
			require.JSONEq(t, `["gpt-5.3","gpt-5.2"]`, runtime.ModelsJSON)
		})
	}
}
