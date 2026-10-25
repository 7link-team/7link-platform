package templates

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestTemplateHTTPCommandsReplayAndCurrentAuthorization(t *testing.T) {
	m, _, ctx := templateHarness(t)
	in := &draftInput{IdempotencyKey: "draft-once"}
	in.Body.Key = "software"
	in.Body.Definition = testDefinition()
	first, e := m.draftHTTP(ctx, in)
	require.NoError(t, e)
	replay, e := m.draftHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	v := first.Body.Data
	get, e := m.getHTTP(ctx, &getInput{ID: v.ID.String()})
	require.NoError(t, e)
	require.Equal(t, v, get.Body.Data)
	node := v.Definition.Workflow.Nodes[0]
	node.Name = "New title"
	p := &patchInput{ID: v.ID.String(), IdempotencyKey: "patch-once", Body: TemplateWorkflowPatch{BaseVersion: 1, BaseHash: v.Hash, Reason: "Rename", Operations: []TemplatePatchOperation{{Kind: "update_node", ID: "work", Node: &node}}}}
	patched, e := m.patchHTTP(ctx, p)
	require.NoError(t, e)
	require.Equal(t, "New title", patched.Body.Data.Definition.Workflow.Nodes[0].Name)
	_, e = m.patchHTTP(ctx, p)
	require.NoError(t, e)
	pub := &publishInput{ID: v.ID.String(), IdempotencyKey: "publish-once"}
	pub.Body.Hash = patched.Body.Data.Hash
	published, e := m.publishHTTP(ctx, pub)
	require.NoError(t, e)
	require.Equal(t, "PUBLISHED", published.Body.Data.State)
	_, e = m.publishHTTP(ctx, pub)
	require.NoError(t, e)
	instantiate := &instantiateInput{ID: v.ID.String(), IdempotencyKey: "instantiate-once"}
	instantiate.Body.ProjectID = 99
	instantiate.Body.AccountableID = 11
	instance, e := m.instantiateHTTP(ctx, instantiate)
	require.NoError(t, e)
	require.Positive(t, instance.Body.Data.ConstitutionID)
	_, e = m.instantiateHTTP(ctx, instantiate)
	require.NoError(t, e)
	read, e := m.instanceHTTP(ctx, &instanceInput{ProjectID: "99"})
	require.NoError(t, e)
	require.Equal(t, v.ID, read.Body.Data.TemplateVersionID)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.draftHTTP(ctx, in)
	assertStatus(t, e, 403)
	_, e = m.getHTTP(ctx, &getInput{ID: v.ID.String()})
	assertStatus(t, e, 403)
	other := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11})
	_, e = m.getHTTP(other, &getInput{ID: v.ID.String()})
	assertStatus(t, e, 403)
}
func TestTemplateHTTPPaginationAndFailureContracts(t *testing.T) {
	m, db, ctx := templateHarness(t)
	a := draftTemplate(t, m, db, ctx, "a")
	b := draftTemplate(t, m, db, ctx, "b")
	page, e := m.listHTTP(ctx, &listInput{Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.True(t, page.Body.Data.HasMore)
	require.Equal(t, a.ID, page.Body.Data.Items[0].ID)
	second, e := m.listHTTP(ctx, &listInput{Limit: 1, Sort: "id", Cursor: page.Body.Data.NextCursor})
	require.NoError(t, e)
	require.False(t, second.Body.Data.HasMore)
	require.Equal(t, b.ID, second.Body.Data.Items[0].ID)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
	}
	page, e = m.listHTTP(ctx, &listInput{Limit: 100, Sort: "id"})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 2)
	for _, in := range []*listInput{{Limit: 1, Sort: "id", Cursor: "bad"}, {Limit: 1, Sort: "id", Cursor: "-1"}, {Limit: 0, Sort: "id"}, {Limit: 101, Sort: "id"}, {Limit: 1, Sort: "name"}} {
		_, e = m.listHTTP(ctx, in)
		assertStatus(t, e, 422)
	}
	_, e = m.getHTTP(ctx, &getInput{ID: "bad"})
	assertStatus(t, e, 422)
	_, e = m.instanceHTTP(ctx, &instanceInput{ProjectID: "bad"})
	assertStatus(t, e, 422)
	_, e = m.patchHTTP(ctx, &patchInput{ID: "bad"})
	assertStatus(t, e, 422)
	_, e = m.publishHTTP(ctx, &publishInput{ID: "bad"})
	assertStatus(t, e, 422)
	_, e = m.instantiateHTTP(ctx, &instantiateInput{ID: "bad"})
	assertStatus(t, e, 422)
	_, e = m.getHTTP(ctx, &getInput{ID: "999"})
	assertStatus(t, e, 404)
	_, e = m.draftHTTP(ctx, &draftInput{IdempotencyKey: "invalid"})
	assertStatus(t, e, 422)
	_, e = command(m, ctx, "json", "test", make(chan int), func(*gorm.DB) (TemplateVersionView, error) { return TemplateVersionView{}, nil })
	assertStatus(t, e, 500)
	var empty *Module
	require.ErrorIs(t, empty.transaction(ctx, func(*gorm.DB) error { return nil }), ErrUnavailable)
	for _, c := range []struct {
		err    error
		status int
	}{{ErrInvalid, 422}, {ErrConflict, 409}, {ErrNotFound, 404}, {ErrForbidden, 403}, {ErrUnavailable, 503}, {idempotency.ErrKeyReused, 409}, {errors.New("private database detail"), 500}} {
		assertStatus(t, templateError(ctx, c.err), c.status)
	}
}
func assertStatus(t *testing.T, e error, status int) {
	t.Helper()
	var h huma.StatusError
	require.ErrorAs(t, e, &h)
	require.Equal(t, status, h.GetStatus())
}
func TestTemplateHTTPDeclarationUsesCurrentPermissions(t *testing.T) {
	m, _, ctx := templateHarness(t)
	m.rubricOwner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, nil
	}
	registry, e := authz.NewRegistry()
	require.NoError(t, e)
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Read work"}, {Resource: "channel", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Read channel"}, {Resource: "project", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Manage projects"}, {Resource: "integration", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Manage integrations"}, {Resource: "template", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Read templates"}, {Resource: "template", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Manage templates"}}, nil))
	m.registrar = authz.NewDeclarationOnlyRegistrar(registry)
	_, api := humatest.New(t)
	require.NoError(t, m.RegisterHTTP(ctx, api))
	for _, path := range []string{"/tenants/{tenant_id}/templates", "/tenants/{tenant_id}/templates/{template_id}", "/tenants/{tenant_id}/projects/{project_id}/workflow"} {
		require.Contains(t, api.OpenAPI().Paths, path)
	}
	m.registrar = nil
	require.ErrorIs(t, m.RegisterHTTP(ctx, api), ErrUnavailable)
}
