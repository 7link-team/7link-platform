package templates

import (
	"context"
	"encoding/json"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func verifierDefinition() VerifierDefinition {
	return VerifierDefinition{Key: "unit-tests", Kind: "DETERMINISTIC", Runner: "COMMAND", Version: 1, Argv: []string{"test-command", "{{param:threshold}}"}, TimeoutSec: 10, Limitations: "Only the declared command is exercised."}
}
func TestVerifierFrozenWorkPinSurvivesUpgradeAndParameterApproval(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Workflow.Parameters = map[string]any{"threshold": float64(70)}
	d.Metadata["verifiers"] = []VerifierDefinition{verifierDefinition()}
	rubric, _ := rubricFixture()
	d.Metadata["rubric"] = rubric
	v := publishRuntime(t, m, db, ctx, "verification-pins", d)
	m.delivery.CreateWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TemplateWorkInput) (guid.ID, error) {
		return 700, nil
	}
	var instance TemplateInstanceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; instance, e = m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	read := func() VerifierBinding {
		var pin VerifierBinding
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			var found bool
			pin, found, e = m.verifiersForWorkOn(ctx, tx, 1, 99, 700)
			require.True(t, found)
			return e
		}))
		return pin
	}
	initial := read()
	require.Equal(t, v.ID, initial.TemplateVersionID)
	require.Equal(t, v.Hash, initial.DefinitionHash)
	require.Equal(t, []string{"test-command", "70"}, initial.Verifiers[0].Argv)
	require.NotNil(t, initial.Rubric)
	require.Equal(t, json.Number("3.5"), initial.Rubric.PassRule.WeightedMin)
	d.Semver = "1.1.0"
	d.Workflow.Version = 2
	d.Workflow.Parameters["threshold"] = float64(95)
	changed := verifierDefinition()
	changed.Argv = []string{"new-command"}
	d.Metadata["verifiers"] = []VerifierDefinition{changed}
	rubric.PassRule.WeightedMin = "4.5"
	d.Metadata["rubric"] = rubric
	target := publishRuntime(t, m, db, ctx, "verification-pins", d)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.upgradeBindingOn(ctx, tx, 99, target.ID, instance.Version, false)
		return e
	}))
	require.Equal(t, initial, read())
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, e := m.verifiersForWorkOn(ctx, tx, 1, 99, 701)
		require.False(t, found)
		return e
	}))
	_, _, e := m.verifiersForWorkOn(ctx, db, 1, 99, 700)
	require.ErrorIs(t, e, ErrInvalid)
}

func TestVerifierDeclarationsRejectUnsupportedAndResolveOnlyKnownParameters(t *testing.T) {
	for _, fault := range []string{"version", "kind", "runner", "duplicate", "limitations", "bounds"} {
		t.Run(fault, func(t *testing.T) {
			d := testDefinition()
			v := verifierDefinition()
			switch fault {
			case "version":
				v.Version = 2
			case "kind":
				v.Kind = "MODEL_SCORED"
			case "runner":
				v.Runner = "UNAVAILABLE"
			case "limitations":
				v.Limitations = ""
			case "bounds":
				v.TimeoutSec = 3601
			}
			defs := []VerifierDefinition{v}
			if fault == "duplicate" {
				defs = append(defs, v)
			}
			d.Metadata["verifiers"] = defs
			require.ErrorIs(t, d.Validate(nil), ErrInvalid)
		})
	}
	_, e := resolveVerifiers([]VerifierDefinition{verifierDefinition()}, nil)
	require.ErrorIs(t, e, ErrInvalid)
	_, e = resolveVerifiers([]VerifierDefinition{verifierDefinition()}, map[string]any{"threshold": []any{1}})
	require.ErrorIs(t, e, ErrInvalid)
	resolved, e := resolveVerifiers([]VerifierDefinition{verifierDefinition()}, map[string]any{"threshold": "a; secret-command"})
	require.NoError(t, e)
	require.Equal(t, []string{"test-command", "a; secret-command"}, resolved[0].Argv) // One argv value, never a shell fragment.
	b, e := json.Marshal(resolved)
	require.NoError(t, e)
	require.NotEmpty(t, b)
}
