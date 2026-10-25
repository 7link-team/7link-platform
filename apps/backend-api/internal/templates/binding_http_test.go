package templates

import (
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strconv"
	"testing"
)

func TestBindingHTTPUsesCASReplayAndCurrentHumanAuthority(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Workflow.Parameters = map[string]any{"method": "agile"}
	d.Metadata = map[string]any{"parameters": map[string]any{"method": map[string]any{"brief_overridable": true, "values": []any{"agile", "waterfall"}}}}
	v := publishRuntime(t, m, db, ctx, "binding-http", d)
	create := &bindingCreateInput{ProjectID: "99", IdempotencyKey: "bind"}
	create.Body.TemplateVersionID = v.ID
	create.Body.AccountableID = 11
	first, e := m.bindingCreateHTTP(ctx, create)
	require.NoError(t, e)
	require.Positive(t, first.Body.Data.Version)
	replay, e := m.bindingCreateHTTP(ctx, create)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data.ID, replay.Body.Data.ID)
	in := &bindingOverrideInput{ProjectID: "99", IdempotencyKey: "approve", IfMatch: "\"1\""}
	in.Body.Overrides = map[string]any{"method": "waterfall"}
	approved, e := m.bindingOverrideHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, 2, approved.Body.Data.Version)
	require.Equal(t, guid.ID(11), *approved.Body.Data.OverridesApprovedBy)
	replay, e = m.bindingOverrideHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, 2, replay.Body.Data.Version)
	in.IdempotencyKey = "stale"
	_, e = m.bindingOverrideHTTP(ctx, in)
	assertStatus(t, e, 409)
	in.IdempotencyKey = "approve"
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	_, e = m.bindingOverrideHTTP(ctx, in)
	assertStatus(t, e, 403)
}

func TestBindingHTTPDiffAndInvalidInputContracts(t *testing.T) {
	m, db, ctx := templateHarness(t)
	a := publishRuntime(t, m, db, ctx, "diff-a", testDefinition())
	d := testDefinition()
	d.Semver = "1.1.0"
	d.Workflow.Version = 2
	d.Workflow.Nodes[0].Name = "New name"
	b := publishRuntime(t, m, db, ctx, "diff-a", d)
	diff, e := m.bindingDiffHTTP(ctx, &bindingDiffInput{ID: b.ID.String(), Against: a.ID.String()})
	require.NoError(t, e)
	require.Len(t, diff.Body.Data.Changed, 1)
	other := testDefinition()
	other.Workflow.WorkflowID = "other"
	c := publishRuntime(t, m, db, ctx, "diff-other", other)
	_, e = m.bindingDiffHTTP(ctx, &bindingDiffInput{ID: c.ID.String(), Against: a.ID.String()})
	assertStatus(t, e, 422)
	_, e = m.bindingDiffHTTP(ctx, &bindingDiffInput{ID: "bad", Against: a.ID.String()})
	assertStatus(t, e, 422)
	for _, header := range []string{"", "0", "-1", "W/\"1\"", "\"bad\"", "*"} {
		_, e := bindingVersionHeader(header)
		require.ErrorIs(t, e, ErrInvalid)
	}
	for _, header := range []string{"1", "\"1\""} {
		version, e := bindingVersionHeader(header)
		require.NoError(t, e)
		require.Equal(t, 1, version)
	}
	for _, err := range []error{ErrOverrideRange, ErrBreakingAck, ErrDiffMismatch} {
		assertStatus(t, templateError(ctx, err), 422)
	}
}

func TestBindingHTTPUpgradeAndReverseDiffPreserveMaterializedWork(t *testing.T) {
	m, db, ctx := templateHarness(t)
	v := publishRuntime(t, m, db, ctx, "public-upgrade", testDefinition())
	var binding TemplateInstanceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; binding, e = m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	d := testDefinition()
	d.Semver = "1.1.0"
	d.Workflow.Version = 2
	d.Workflow.Nodes[0].Name = "Updated"
	target := publishRuntime(t, m, db, ctx, "public-upgrade", d)
	diff, e := m.bindingDiffHTTP(ctx, &bindingDiffInput{ID: v.ID.String(), Against: target.ID.String()})
	require.NoError(t, e)
	require.NotEmpty(t, diff.Body.Data.Changed)
	input := &bindingUpgradeInput{ProjectID: "99", IfMatch: strconv.Itoa(binding.Version), IdempotencyKey: "upgrade-once"}
	input.Body.TargetVersionID = target.ID
	upgraded, e := m.bindingUpgradeHTTP(ctx, input)
	require.NoError(t, e)
	require.Equal(t, target.ID, upgraded.Body.Data.TemplateVersionID)
	require.Equal(t, binding.Runs[0].WorkItemID, upgraded.Body.Data.Runs[0].WorkItemID)
	replay, e := m.bindingUpgradeHTTP(ctx, input)
	require.NoError(t, e)
	require.Equal(t, upgraded.Body.Data, replay.Body.Data)
	for _, f := range []func() error{
		func() error {
			_, e := m.bindingUpgradeHTTP(ctx, &bindingUpgradeInput{ProjectID: "99", IfMatch: "bad"})
			return e
		},
		func() error { _, e := m.bindingCreateHTTP(ctx, &bindingCreateInput{ProjectID: "99"}); return e },
		func() error {
			_, e := m.bindingOverrideHTTP(ctx, &bindingOverrideInput{ProjectID: "99", IfMatch: "bad"})
			return e
		},
		func() error {
			_, e := m.bindingDiffHTTP(ctx, &bindingDiffInput{ID: v.ID.String(), Against: "999"})
			return e
		},
	} {
		require.Error(t, f())
	}
	_, e = bindingVersionHeader(`"unterminated`)
	require.ErrorIs(t, e, ErrInvalid)
}
