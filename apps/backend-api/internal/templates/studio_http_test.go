package templates

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestStudioHTTPReplayAuthorizationPagingAndTenantIsolation(t *testing.T) {
	m, _, ctx := bindStudio(t, "VIDEO")
	in := &studioCreateInput{ProjectID: "99", IdempotencyKey: "studio-once", Body: StudioDraft{Domain: "VIDEO", Kind: "SHOT", Content: StudioContent{Title: "First"}}}
	first, e := m.studioCreateHTTP(ctx, in)
	require.NoError(t, e)
	replay, e := m.studioCreateHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data.ID, replay.Body.Data.ID)
	in.Body.Content.Title = "Changed"
	_, e = m.studioCreateHTTP(ctx, in)
	assertStatus(t, e, 409)
	in.IdempotencyKey = "second"
	_, e = m.studioCreateHTTP(ctx, in)
	require.NoError(t, e)
	page, e := m.studioListHTTP(ctx, &studioListInput{ProjectID: "99", Domain: "VIDEO", Sort: "id", Limit: 1})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.True(t, page.Body.Data.HasMore)
	next, e := m.studioListHTTP(ctx, &studioListInput{ProjectID: "99", Domain: "VIDEO", Sort: "id", Limit: 1, Cursor: page.Body.Data.NextCursor})
	require.NoError(t, e)
	require.Len(t, next.Body.Data.Items, 1)
	require.NotEqual(t, page.Body.Data.Items[0].ID, next.Body.Data.Items[0].ID)
	other := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	_, e = m.studioListHTTP(other, &studioListInput{ProjectID: "99", Domain: "VIDEO", Sort: "id", Limit: 1})
	assertStatus(t, e, 404)
	in.Body.Content.Title = "First"
	in.IdempotencyKey = "studio-once"
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.studioCreateHTTP(ctx, in)
	assertStatus(t, e, 403)
}

func TestStudioDecisionReplayNeverAdoptsWithoutCurrentHumanAuthority(t *testing.T) {
	m, db, ctx := bindStudio(t, "TEAM_TASKS")
	draft := createStudio(t, m, db, ctx, StudioDraft{Domain: "TEAM_TASKS", Kind: "ASSISTANT_DRAFT", Content: StudioContent{Title: "Triage", Text: "Suggested action", Source: "message:1"}})
	in := &studioDecisionInput{ProjectID: "99", ID: draft.ID.String(), IdempotencyKey: "reject-once", Body: StudioDecision{Version: 1, Action: "REJECT", Reason: "Human rejected"}}
	first, e := m.studioDecisionHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "REJECTED", first.Body.Data.State)
	second, e := m.studioDecisionHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data.ID, second.Body.Data.ID)
	nonhuman := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 11, SubjectType: authn.SubjectTypeServiceAccount})
	_, e = m.studioDecisionHTTP(nonhuman, in)
	assertStatus(t, e, 403)
	in.Body.Reason = "Changed request"
	_, e = m.studioDecisionHTTP(ctx, in)
	assertStatus(t, e, 409)
	_, e = m.studioListHTTP(ctx, &studioListInput{ProjectID: "99", Domain: "TEAM_TASKS", Limit: 50, Sort: "id", Cursor: "-1"})
	assertStatus(t, e, 422)
	_, e = m.studioCreateHTTP(ctx, &studioCreateInput{ProjectID: "invalid"})
	assertStatus(t, e, 422)
}
