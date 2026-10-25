package agents

import (
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestCatalogUsesActiveHumanCatalogGrantsAndVerifiedHash(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		tool, err := m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "go-review", Kind: "SKILL", Title: "Go review", SkillBody: "Review database transactions"})
		require.NoError(t, err)
		before, err := m.skillCatalogOn(agentContext(t, 7), tx, 1, 7)
		require.NoError(t, err)
		require.Empty(t, before.Skills)
		grant, err := m.grantOn(ctx, tx, 1, tool.ID, 7, "ACTIVE")
		require.NoError(t, err)
		got, err := m.skillCatalogOn(agentContext(t, 7), tx, 1, 7)
		require.NoError(t, err)
		require.Len(t, got.Skills, 1)
		require.Equal(t, tool.ContentHash, got.Skills[0].SHA256)
		require.Equal(t, grant.ID, got.Skills[0].GrantID)
		require.False(t, got.TagsAvailable)
		require.False(t, got.MountsAvailable)
		q := query.BindToolEntry(tx, m.names)
		_, err = q.WithContext(ctx).Where(q.ID.Eq(tool.ID)).Update(q.ContentHash, "corrupt")
		require.NoError(t, err)
		_, err = m.skillCatalogOn(agentContext(t, 7), tx, 1, 7)
		require.ErrorIs(t, err, ErrUnavailable)
		_, err = q.WithContext(ctx).Where(q.ID.Eq(tool.ID)).Update(q.ContentHash, tool.ContentHash)
		require.NoError(t, err)
		_, err = m.grantOn(ctx, tx, 1, tool.ID, 7, "REVOKED")
		require.NoError(t, err)
		got, err = m.skillCatalogOn(agentContext(t, 7), tx, 1, 7)
		require.NoError(t, err)
		require.Empty(t, got.Skills)
		_, err = m.skillCatalogOn(agentContext(t, 7), tx, 2, 7)
		require.Error(t, err)
		return nil
	}))
	_, err := m.skillCatalogOn(ctx, db, 1, 7)
	require.ErrorIs(t, err, ErrInvalid)
	s := module.NewServices()
	require.NoError(t, m.provideSkillCatalog(s))
	require.NoError(t, s.Seal())
	p, err := module.Resolve[SkillCatalog](s)
	require.NoError(t, err)
	require.NotNil(t, p.ForAgentOn)
}

func TestMountedOperationsPublishOnlyAfterRoutesAndRemainSealed(t *testing.T) {
	m, _, _, api := agentHTTPHarness(t)
	services := module.NewServices()
	require.NoError(t, m.provideSkillCatalog(services))
	require.NoError(t, services.Seal())
	catalog, err := module.Resolve[MountedOperationCatalog](services)
	require.NoError(t, err)
	require.NoError(t, catalog.Register(MountedOperation{Name: "triage.create", HTTPID: "clientwork.intake_suggestion.create", Permission: "agent_read"}))
	before := m.registry.List()
	require.ErrorIs(t, catalog.Finalize(api), ErrUnavailable)
	require.Equal(t, before, m.registry.List(), "a missing route must not partly publish a manifest")
	type body struct {
		Body struct {
			Reason string `json:"reason"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "clientwork.intake_suggestion.create", Method: "POST", Path: "/entities/{entity_id}/intake-suggestions", Summary: "Record actual suggestion", DefaultStatus: 201}, func(ctx context.Context, _ *body) (*respx.Body[string], error) { return respx.OK(ctx, "recorded"), nil })
	require.NoError(t, catalog.Finalize(api))
	require.NoError(t, catalog.Finalize(api), "identical router republication is idempotent")
	require.ErrorIs(t, catalog.Register(MountedOperation{Name: "late.create", HTTPID: "late", Permission: "agent_read"}), ErrConflict)
	after := m.registry.List()
	require.Len(t, after, len(before)+1)
	for _, op := range after {
		if op.Name == "triage.create" {
			require.Equal(t, "/entities/{entity_id}/intake-suggestions", op.Path)
			require.Contains(t, string(op.InputSchema), "reason")
			require.Equal(t, "agent_read", op.RequiredPermission)
			require.True(t, op.IdempotencyRequired)
			require.ErrorIs(t, m.registry.Register(op), ErrConflict)
		}
	}
	// Changing a previously published contract cannot mutate any manifest entry.
	api.OpenAPI().Paths["/entities/{entity_id}/intake-suggestions"].Post.Summary = "Changed contract"
	require.ErrorIs(t, catalog.Finalize(api), ErrConflict)
	require.Equal(t, after, m.registry.List())
}
