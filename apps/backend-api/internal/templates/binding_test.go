package templates

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/templates/model"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestBindingOverridesRequireExplicitPublishedPolicy(t *testing.T) {
	d := testDefinition()
	d.Workflow.Parameters = map[string]any{"budget": float64(10), "secret": "fixed"}
	d.Metadata = map[string]any{"parameters": map[string]any{"budget": map[string]any{"brief_overridable": true, "min": float64(10), "max": float64(30), "only_increase": true}}}
	actor := guid.ID(11)
	now := time.Now().UTC()
	for _, raw := range []string{`{"budget":9}`, `{"budget":31}`, `{"secret":"changed"}`, `{"budget":"20"}`} {
		_, _, e := resolveBindingParameters(d, &model.Binding{BriefOverrides: raw, OverridesApprovedBy: &actor, OverridesApprovedAt: &now})
		require.ErrorIs(t, e, ErrOverrideRange)
	}
	_, _, e := resolveBindingParameters(d, &model.Binding{BriefOverrides: `{"budget":20}`})
	require.ErrorIs(t, e, ErrOverrideRange)
	effective, _, e := resolveBindingParameters(d, &model.Binding{BriefOverrides: `{"budget":20}`, OverridesApprovedBy: &actor, OverridesApprovedAt: &now})
	require.NoError(t, e)
	require.Equal(t, float64(20), effective["budget"])             //nolint:testifylint // Exact numeric equality is frozen by this regression contract.
	require.Equal(t, float64(10), d.Workflow.Parameters["budget"]) //nolint:testifylint // Exact numeric equality is frozen by this regression contract.
}

func TestBindingUpgradeKeepsWorkAndRequiresMajorAcknowledgement(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Workflow.Parameters = map[string]any{"budget": float64(10)}
	old := publishRuntime(t, m, db, ctx, "upgrade", d)
	var initial TemplateInstanceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; initial, e = m.instantiateOn(ctx, tx, old.ID, 99, 11); return e }))
	d.Semver = "2.0.0"
	d.Workflow.Version = 2
	d.Workflow.Nodes[0].Name = "New pending definition"
	d.Workflow.Parameters["budget"] = float64(20)
	target := publishRuntime(t, m, db, ctx, "upgrade", d)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.upgradeBindingOn(ctx, tx, 99, target.ID, initial.Version, false)
		return e
	}), ErrBreakingAck)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.upgradeBindingOn(ctx, tx, 99, target.ID, initial.Version, true)
		if e != nil {
			return e
		}
		require.Equal(t, target.ID, out.TemplateVersionID)
		require.Equal(t, initial.Runs[0].WorkItemID, out.Runs[0].WorkItemID)
		require.Equal(t, old.ID, out.Runs[0].TemplateVersionID)
		require.Equal(t, old.Hash, out.Runs[0].DefinitionHash)
		var frozen map[string]any
		require.NoError(t, json.Unmarshal(out.Runs[0].Parameters, &frozen))
		require.Equal(t, float64(10), frozen["budget"])                  //nolint:testifylint // Exact numeric equality is frozen by this regression contract.
		require.Equal(t, float64(20), out.EffectiveParameters["budget"]) //nolint:testifylint // Exact numeric equality is frozen by this regression contract.
		return nil
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.upgradeBindingOn(ctx, tx, 99, target.ID, initial.Version, true)
		return e
	}), ErrConflict)
}

func TestBindingOverrideChangesUnresolvedRuntimeButNotMaterializedWork(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Workflow.Parameters = map[string]any{"method": "agile"}
	d.Metadata = map[string]any{"parameters": map[string]any{"method": map[string]any{"brief_overridable": true, "values": []any{"agile", "waterfall"}}}}
	d.Workflow.Nodes[0] = TemplateNode{ID: "work", Name: "Wait", Kind: "CONDITION", Condition: json.RawMessage(`{"and":[{"var":"signed_brief.approved"},{"==":[{"var":"parameters.method"},"waterfall"]}]}`)}
	d.Workflow.Edges[0].Branch = "true"
	v := publishRuntime(t, m, db, ctx, "overrides", d)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.instantiateOn(ctx, tx, v.ID, 99, 11)
		if e != nil {
			return e
		}
		require.Empty(t, out.Runs[0].DefinitionHash)
		updated, e := m.approveBindingOverridesOn(ctx, tx, 99, out.Version, map[string]any{"method": "waterfall"})
		if e != nil {
			return e
		}
		require.Equal(t, "waterfall", updated.EffectiveParameters["method"])
		require.Empty(t, updated.Runs[0].DefinitionHash)
		return nil
	}))
	m.briefs.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.SignedBriefFact, bool, error) {
		return intake.SignedBriefFact{}, true, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.advanceOn(ctx, tx, 99)
		if e != nil {
			return e
		}
		require.Equal(t, "COMPLETED", out.Runs[0].State)
		require.NotNil(t, out.Runs[0].Value)
		require.True(t, *out.Runs[0].Value)
		var frozen map[string]any
		require.NoError(t, json.Unmarshal(out.Runs[0].Parameters, &frozen))
		require.Equal(t, "waterfall", frozen["method"])
		return nil
	}))
}

func TestTemplateDiffSortedAndDoesNotHideMetadata(t *testing.T) {
	a := testDefinition()
	b := testDefinition()
	b.Workflow.Nodes[0].Name = "Changed"
	b.Workflow.Parameters = map[string]any{"z": true, "a": false}
	b.Metadata = map[string]any{"policy": "new"}
	diff, e := diffDefinitions(a, b)
	require.NoError(t, e)
	require.Len(t, diff.Changed, 1)
	require.Equal(t, "nodes/work", diff.Changed[0].Path)
	require.Equal(t, []string{"metadata/policy", "parameters/a", "parameters/z"}, []string{diff.Added[0].Path, diff.Added[1].Path, diff.Added[2].Path})
	b.Workflow.WorkflowID = "other"
	_, e = diffDefinitions(a, b)
	require.ErrorIs(t, e, ErrDiffMismatch)
}

func TestBindingUpgradeTransfersMetadataAndRollsBackEventFailure(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Workflow.Nodes[0] = TemplateNode{ID: "work", Name: "Wait for Brief", Kind: "CONDITION", Condition: json.RawMessage(`{"var":"signed_brief.approved"}`)}
	d.Workflow.Edges[0].Branch = "true"
	old := publishRuntime(t, m, db, ctx, "pending-upgrade", d)
	var initial TemplateInstanceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; initial, e = m.instantiateOn(ctx, tx, old.ID, 99, 11); return e }))
	d.Semver = "1.1.0"
	d.Workflow.Version = 2
	d.Workflow.Nodes = d.Workflow.Nodes[1:]
	d.Workflow.Edges = d.Workflow.Edges[1:]
	d.Workflow.EntryNodeID = "team"
	target := publishRuntime(t, m, db, ctx, "pending-upgrade", d)
	injected := errors.New("publisher failed")
	m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, injected })
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.upgradeBindingOn(ctx, tx, 99, target.ID, initial.Version, false)
		return e
	}), injected)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.instanceOn(ctx, tx, 99)
		if e != nil {
			return e
		}
		require.Equal(t, old.ID, out.TemplateVersionID)
		require.Equal(t, initial.Version, out.Version)
		return nil
	}))
	m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 1, nil })
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.upgradeBindingOn(ctx, tx, 99, target.ID, initial.Version, false)
		if e != nil {
			return e
		}
		require.Equal(t, "team", out.Runs[0].NodeID)
		require.Equal(t, guid.ID(11), out.Runs[0].AccountableID)
		_, e = m.advanceOn(ctx, tx, 99)
		return e
	}))
}

func TestBindingOverrideAndUpgradeStorageFailuresPreservePin(t *testing.T) {
	for _, operation := range []string{"override", "upgrade"} {
		t.Run(operation, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			d := testDefinition()
			v := publishRuntime(t, m, db, ctx, "binding-fault", d)
			var original TemplateInstanceView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; original, e = m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
			d.Semver = "1.1.0"
			d.Workflow.Version = 2
			target := publishRuntime(t, m, db, ctx, "binding-fault", d)
			injected := errors.New("binding update unavailable")
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("binding-update-failure", func(tx *gorm.DB) {
				if tx.Statement.Table == m.names.Bindings.String() {
					tx.AddError(injected)
				}
			}))
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				if operation == "override" {
					_, e := m.approveBindingOverridesOn(ctx, tx, 99, original.Version, nil)
					return e
				}
				_, e := m.upgradeBindingOn(ctx, tx, 99, target.ID, original.Version, false)
				return e
			}), injected)
			require.NoError(t, db.Callback().Update().Remove("binding-update-failure"))
			got, e := m.instanceOn(ctx, db, 99)
			require.NoError(t, e)
			require.Equal(t, original, got)
			if operation == "override" {
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.approveBindingOverridesOn(ctx, tx, 99, original.Version+1, nil)
					return e
				}), ErrConflict)
			} else {
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.upgradeBindingOn(ctx, tx, 99, 999, original.Version, false)
					return e
				}), ErrNotFound)
			}
		})
	}
	d := testDefinition()
	actor := guid.ID(11)
	now := time.Now().UTC()
	_, _, e := resolveBindingParameters(d, &model.Binding{BriefOverrides: "invalid"})
	require.ErrorIs(t, e, ErrUnavailable)
	_, overrides, e := resolveBindingParameters(d, &model.Binding{BriefOverrides: "null"})
	require.NoError(t, e)
	require.Empty(t, overrides)
	d.Workflow.Parameters = map[string]any{"choice": "a"}
	d.Metadata["parameters"] = map[string]any{"choice": map[string]any{"brief_overridable": true, "values": []any{"a", "b"}}}
	_, _, e = resolveBindingParameters(d, &model.Binding{BriefOverrides: `{"choice":"c"}`, OverridesApprovedBy: &actor, OverridesApprovedAt: &now})
	require.ErrorIs(t, e, ErrOverrideRange)
}
