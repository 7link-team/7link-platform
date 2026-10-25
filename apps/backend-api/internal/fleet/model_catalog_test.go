package fleet

import (
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestManualModelCatalogStorageFailuresNeverPublishPartialVersion(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, operation := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f := inspectionHarness(t)
				if existing {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
						_, e := f.m.SetManualModelsOn(f.ctx, tx, 7, 1, "CODEX", ManualModelsUpdate{ModelIDs: []string{"prior"}})
						return e
					}))
				}
				q := query.BindManualModels(f.db, f.m.names)
				before, e := q.WithContext(f.ctx).Order(q.ID).Find()
				require.NoError(t, e)
				count := 0
				injected := errors.New("model catalog storage unavailable")
				hook := func(tx *gorm.DB) {
					if !strings.Contains(tx.Statement.Table, "fleet_") {
						return
					}
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch operation {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("model_storage_failure", hook))
				case "create":
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("model_storage_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("model_storage_failure", hook))
				}
				version := 0
				if existing {
					version = 1
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					_, e := f.m.SetManualModelsOn(f.ctx, tx, 7, 1, "CODEX", ManualModelsUpdate{ExpectedVersion: version, ModelIDs: []string{"next"}})
					return e
				})
				require.NoError(t, f.db.Callback().Query().Remove("model_storage_failure"))
				require.NoError(t, f.db.Callback().Create().Remove("model_storage_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("model_storage_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				after, e := q.WithContext(f.ctx).Order(q.ID).Find()
				require.NoError(t, e)
				require.Equal(t, before, after)
				return count
			}
			count := exercise(t, 0)
			for point := 1; point <= count; point++ {
				t.Run(fmt.Sprintf("existing=%t/%s/%d", existing, operation, point), func(t *testing.T) { exercise(t, point) })
			}
		}
	}
}

func TestManualModelsSurviveInventoryRefreshWithVersionedReplayAndCurrentAuthority(t *testing.T) {
	f := inspectionHarness(t)
	record := func(models []string) {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			return f.m.RecordInventoryOn(f.ctx, tx, 7, 1, f.session.ID, f.session.ConnEpoch, Inventory{Runtimes: []RuntimeObservation{{Kind: "CODEX", Version: "1", ExecutablePath: "/bin/codex", Models: models}}})
		}))
	}
	record([]string{"observed"})
	update := ManualModelsUpdate{ModelIDs: []string{"proxy-model"}, Families: map[string]string{"proxy-model": "google", "observed": "openai"}}
	set := func(in ManualModelsUpdate) (ModelCatalog, error) {
		var out ModelCatalog
		err := f.db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = f.m.SetManualModelsOn(f.ctx, tx, 7, 1, "CODEX", in)
			return err
		})
		return out, err
	}
	out, err := set(update)
	require.NoError(t, err)
	require.Equal(t, 1, out.Version)
	var effects int64
	require.NoError(t, f.db.Table("test_fleet_effects").Count(&effects).Error)
	replay, err := set(update)
	require.NoError(t, err)
	require.Equal(t, out, replay)
	var after int64
	require.NoError(t, f.db.Table("test_fleet_effects").Count(&after).Error)
	require.Equal(t, effects, after)
	changed := update
	changed.ModelIDs = []string{"different"}
	changed.Families = nil
	_, err = set(changed)
	require.ErrorIs(t, err, ErrVersionConflict)
	record([]string{"fresh-observation"})
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		current, e := f.m.ModelCatalogOn(f.ctx, tx, 7, 1, "CODEX")
		require.NoError(t, e)
		require.Equal(t, []string{"fresh-observation"}, current.ObservedIDs)
		require.Equal(t, []string{"proxy-model"}, current.ManualIDs)
		require.Equal(t, "google", current.Families["proxy-model"])
		return e
	}))
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = set(update)
	require.ErrorIs(t, err, ErrMachineForbidden)
}

func TestManualModelsValidateLimitsFamiliesTenantAndAtomicRollback(t *testing.T) {
	f := inspectionHarness(t)
	set := func(tenant guid.ID, runtime string, in ManualModelsUpdate) error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			_, err := f.m.SetManualModelsOn(f.ctx, tx, tenant, 1, runtime, in)
			return err
		})
	}
	for _, in := range []ManualModelsUpdate{{ExpectedVersion: -1}, {ModelIDs: []string{"x", "x"}}, {ModelIDs: []string{"bad\nmodel"}}, {ModelIDs: []string{"ok"}, Families: map[string]string{"foreign": "openai"}}, {ModelIDs: []string{"ok"}, Families: map[string]string{"ok": "invalid"}}, {ModelIDs: make([]string, 51)}} {
		require.ErrorIs(t, set(7, "CODEX", in), ErrInvalidArgument)
	}
	require.ErrorIs(t, set(7, "UNKNOWN", ManualModelsUpdate{}), ErrInvalidArgument)
	require.ErrorIs(t, set(8, "CODEX", ManualModelsUpdate{}), ErrMachineForbidden)
	in := ManualModelsUpdate{ModelIDs: []string{"proxy"}}
	var before int64
	require.NoError(t, f.db.Table("test_fleet_effects").Count(&before).Error)
	f.publisher.fail = true
	require.Error(t, set(7, "CODEX", in))
	f.publisher.fail = false
	q := query.BindManualModels(f.db, f.m.names)
	count, err := q.WithContext(f.ctx).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	var after int64
	require.NoError(t, f.db.Table("test_fleet_effects").Count(&after).Error)
	require.Equal(t, before, after)
	require.NoError(t, set(7, "CODEX", in))
}

func TestCatalogFamilyResolutionUsesReportedManualPrefixAndRuntimeDefault(t *testing.T) {
	f := inspectionHarness(t)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.RecordInventoryOn(f.ctx, tx, 7, 1, f.session.ID, f.session.ConnEpoch, Inventory{Runtimes: []RuntimeObservation{{Kind: "CODEX", Version: "1", ExecutablePath: "/bin/codex", Models: []string{"gpt-x", "gemini-x", "claude-x", "proxy"}}}})
	}))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.SetManualModelsOn(f.ctx, tx, 7, 1, "CODEX", ManualModelsUpdate{ModelIDs: []string{"manual"}, Families: map[string]string{"manual": "google", "proxy": "anthropic"}})
		return err
	}))
	for id, want := range map[string]string{"gpt-x": "openai", "codex-x": "openai", "gemini-x": "google", "claude-x": "anthropic", "proxy": "anthropic", "manual": "google", "unknown": "openai"} {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			got, err := f.m.ModelFamilyOn(f.ctx, tx, 7, 1, "CODEX", id)
			require.Equal(t, want, got)
			return err
		}))
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		listed, err := f.m.spawnModelOn(f.ctx, tx, 7, 1, "CODEX", "manual")
		require.True(t, listed)
		return err
	}))
	facts := []RuntimeDiscovery{{Runtime: "CODEX", State: "READY", Source: "CLI_CACHE", Models: []string{"reported"}, Families: map[string]string{"reported": "google"}}, {Runtime: "CLAUDE_CODE", State: "UNSUPPORTED", Reason: "NO_LOCAL_MODEL_LIST_OR_CLI_LIST_COMMAND", Source: "LOCAL_CONFIG"}}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, f.session.ConnEpoch, "MODELS", facts)
		return err
	}))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		got, err := f.m.ModelFamilyOn(f.ctx, tx, 7, 1, "CODEX", "reported")
		require.Equal(t, "google", got)
		return err
	}))
	facts[0].State = "ERROR"
	facts[0].Reason = "INVALID_MODEL_LIST"
	facts[0].Models = nil
	facts[0].Families = nil
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, f.session.ConnEpoch, "MODELS", facts)
		return err
	}))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		got, err := f.m.ModelFamilyOn(f.ctx, tx, 7, 1, "CODEX", "reported")
		require.Equal(t, "google", got, "failed discovery preserves the actual successful family")
		return err
	}))
}
