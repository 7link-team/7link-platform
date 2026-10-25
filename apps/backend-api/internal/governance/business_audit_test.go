package governance

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBusinessAuditCurrentAuthorityAndVisibilityBeforePagination(t *testing.T) {
	m, _, ctx, _, _ := escapeHarness(t)
	full, project, read := false, true, true
	m.explain = func(_ context.Context, tx *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.NotNil(t, tx)
		require.EqualValues(t, 1, r.TenantID)
		require.EqualValues(t, 2, r.PrincipalID)
		switch r.Permission {
		case "audit_read":
			require.Zero(t, r.EntityID)
			return authz.Explanation{Allowed: read}, nil
		case "audit_event_view":
			return authz.Explanation{Allowed: full}, nil
		case "project_read":
			return authz.Explanation{Allowed: project && r.EntityID == 99}, nil
		}
		t.Fatalf("unexpected permission: %s", r.Permission)
		return authz.Explanation{}, nil
	}
	m.businessAudit = func(_ context.Context, tx *gorm.DB, q audit.BusinessQuery, visible func(audit.BusinessEvent) (bool, error)) ([]audit.BusinessEvent, int64, error) {
		require.NotNil(t, tx)
		rows := []audit.BusinessEvent{
			{AuditEventID: 1, ProjectID: 98, Actor: audit.BusinessActor{ID: 2}},
			{AuditEventID: 2, ProjectID: 99, Actor: audit.BusinessActor{ID: 3}},
			{AuditEventID: 3, Actor: audit.BusinessActor{ID: 3}},
			{AuditEventID: 4, Actor: audit.BusinessActor{ID: 2}},
		}
		out := []audit.BusinessEvent{}
		for _, e := range rows {
			ok, err := visible(e)
			if err != nil {
				return nil, 0, err
			}
			if ok {
				out = append(out, e)
			}
		}
		return out, int64(len(out)), nil
	}
	in := &businessAuditInput{TenantID: "1", Limit: 50}
	out, err := m.businessAuditHTTP(ctx, in)
	require.NoError(t, err)
	require.Len(t, out.Body.Data, 2)
	require.EqualValues(t, 2, out.Body.Data[0].AuditEventID)
	require.EqualValues(t, 4, out.Body.Data[1].AuditEventID)
	require.EqualValues(t, 2, out.Body.Meta.Page.Total)
	require.Zero(t, out.Body.Meta.IndexLagSeconds)
	require.Equal(t, "no-store", out.CacheControl)
	project = false
	out, err = m.businessAuditHTTP(ctx, in)
	require.NoError(t, err)
	require.Len(t, out.Body.Data, 1)
	require.EqualValues(t, 4, out.Body.Data[0].AuditEventID)
	full = true
	out, err = m.businessAuditHTTP(ctx, in)
	require.NoError(t, err)
	require.Len(t, out.Body.Data, 4)
	read = false
	_, err = m.businessAuditHTTP(ctx, in)
	require.Error(t, err, "audit_event_view cannot replace audit_read")
	read = true
	in.TenantID = "2"
	_, err = m.businessAuditHTTP(ctx, in)
	require.Error(t, err)
	in.TenantID = "1"
	m.delegations.members = delegationMembers{false}
	_, err = m.businessAuditHTTP(ctx, in)
	require.Error(t, err)
}

func TestBusinessAuditInputExactFiltersAndValidation(t *testing.T) {
	in := businessAuditInput{TenantID: "1", Limit: 50, ProjectID: "9007199254740993", ActorID: "8", TargetID: "9", SpawnGrantID: "10", EventType: "fleet.agent_spawned,fleet.agent_adopted", AuthorityKind: "POLICY", AuthorityRef: "spawn_grant", From: "2026-10-23T01:00:00Z", To: "2026-10-23T02:00:00Z"}
	q, err := in.query()
	require.NoError(t, err)
	require.EqualValues(t, 9007199254740993, q.ProjectID)
	require.Equal(t, []string{"fleet.agent_spawned", "fleet.agent_adopted"}, q.EventTypes)
	for _, mutate := range []func(*businessAuditInput){
		func(i *businessAuditInput) { i.Limit = 0 }, func(i *businessAuditInput) { i.Limit = 201 }, func(i *businessAuditInput) { i.Offset = -1 },
		func(i *businessAuditInput) { i.ProjectID = "bad" }, func(i *businessAuditInput) { i.ActorID = "bad" }, func(i *businessAuditInput) { i.TargetID = "bad" }, func(i *businessAuditInput) { i.SpawnGrantID = "bad" },
		func(i *businessAuditInput) { i.From = "bad" }, func(i *businessAuditInput) { i.To = i.From }, func(i *businessAuditInput) { i.EventType = "one,,two" }, func(i *businessAuditInput) { i.EventType = "one, two" },
	} {
		bad := in
		mutate(&bad)
		_, err := bad.query()
		require.Error(t, err)
	}
}
