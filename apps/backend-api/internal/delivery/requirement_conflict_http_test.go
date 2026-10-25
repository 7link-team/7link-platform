package delivery

import (
	"context"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRequirementConflictHTTPReplayAndCurrentPermission(t *testing.T) {
	m, _, ctx, body := workHTTPHarness(t)
	created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	allowed := true
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: allowed}, nil
	}
	in := &requirementConflictInput{EntityID: "99", WorkItemID: created.Body.Data.ID.String(), Key: "conflict-once", Body: requirementConflictBody{Version: created.Body.Data.Version, Action: "RAISE", Reason: "Conflicting source acceptance requirements"}}
	first, err := m.requirementConflictHTTP(ctx, in)
	require.NoError(t, err)
	replay, err := m.requirementConflictHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	allowed = false
	_, err = m.requirementConflictHTTP(ctx, in)
	require.Error(t, err)
}

func TestRequirementConflictHTTPBindingAndContract(t *testing.T) {
	m, _, ctx, _ := workHTTPHarness(t)
	registry, err := authz.NewRegistry(authz.Action{Resource: "work_item", Verb: "write", Scope: authz.ActionScopeTenant, DataScoped: true})
	require.NoError(t, err)
	guard := resultHTTPGuard{ctx: ctx}
	m.registrar = authz.NewRegistrar(registry, guard, guard, guard)
	m.entityOwner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{ScopeType: "tenant", TenantID: 1, EntityID: 99}, true, nil
	}
	_, api := humatest.New(t)
	m.registerRequirementConflictHTTP(api)
	op := api.OpenAPI().Paths[requirementConflictPath].Post
	require.Equal(t, "work_item_write", op.Extensions[authz.GuardExtensionKey])
	response := api.Post("/entities/99/work-items/81/requirement-conflicts", "Idempotency-Key: bind", strings.NewReader(`{"version":1,"action":"IGNORE","reason":"Conflicting source acceptance requirements"}`))
	require.Equal(t, 422, response.Code, response.Body.String())
}

func TestRequirementConflictHTTPRejectsOverflowAndAbsentCurrentOwner(t *testing.T) {
	h := newResultFixture(t)
	for _, in := range []requirementConflictInput{{EntityID: "9999999999999999999", WorkItemID: "1"}, {EntityID: "99", WorkItemID: "9999999999999999999"}, {EntityID: "99", WorkItemID: "999"}} {
		_, e := h.m.requirementConflictHTTP(h.ctx, &in)
		require.Error(t, e)
	}
	h.m.runtime = nil
	_, e := h.m.requirementConflictHTTP(h.ctx, &requirementConflictInput{EntityID: "99", WorkItemID: h.work.String()})
	assertWorkStatus(t, e, 503)
}
