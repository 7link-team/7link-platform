package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestWorkActivitiesHTTPRequiresCurrentWorkVisibility(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	assignmentClaim(t, m, db, ctx, work, false)
	page, err := m.listWorkActivitiesHTTP(ctx, &workActivityListInput{WorkItemID: work.ID.String(), Limit: 50})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Body.Data.CurrentSeq)
	require.Len(t, page.Body.Data.Items, 1)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.listWorkActivitiesHTTP(ctx, &workActivityListInput{WorkItemID: work.ID.String(), Limit: 50})
	require.Error(t, err)
}

func TestWorkActivitiesHTTPPaginatesAfterSequenceWithOffset(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < 5; i++ {
			if _, err := m.recordWorkActivityOn(ctx, tx, 1, work.ID, WorkActivityInput{Kind: "STATE_CHANGED", ActorKind: "SYSTEM", RefKind: "work_item", RefID: work.ID}); err != nil {
				return err
			}
		}
		return nil
	}))
	page, err := m.listWorkActivitiesHTTP(ctx, &workActivityListInput{WorkItemID: work.ID.String(), AfterSeq: 1, Limit: 2, Offset: 1})
	require.NoError(t, err)
	require.EqualValues(t, 5, page.Body.Data.CurrentSeq)
	require.Len(t, page.Body.Data.Items, 2)
	require.EqualValues(t, 3, page.Body.Data.Items[0].Seq)
	require.EqualValues(t, 4, page.Body.Data.Items[1].Seq)
	require.True(t, page.Body.Data.HasMore)
	page, err = m.listWorkActivitiesHTTP(ctx, &workActivityListInput{WorkItemID: work.ID.String(), AfterSeq: 1, Limit: 2, Offset: 3})
	require.NoError(t, err)
	require.Len(t, page.Body.Data.Items, 1)
	require.EqualValues(t, 5, page.Body.Data.Items[0].Seq)
	require.False(t, page.Body.Data.HasMore)
	page, err = m.listWorkActivitiesHTTP(ctx, &workActivityListInput{WorkItemID: work.ID.String(), AfterSeq: 1, Limit: 2, Offset: 4})
	require.NoError(t, err)
	require.Empty(t, page.Body.Data.Items)
	require.False(t, page.Body.Data.HasMore)
}

func TestWorkActivityHTTPRejectsOverflowAndUnavailableCurrentStorage(t *testing.T) {
	h := newResultFixture(t)
	_, e := h.m.listWorkActivitiesHTTP(h.ctx, &workActivityListInput{WorkItemID: "9999999999999999999", Limit: 20})
	assertWorkStatus(t, e, 422)
	require.NoError(t, h.db.Exec("ALTER TABLE "+h.m.service.names.WorkActivities.String()+" RENAME TO unavailable_work_activities").Error)
	out, e := h.m.listWorkActivitiesHTTP(h.ctx, &workActivityListInput{WorkItemID: h.work.String(), Limit: 20})
	require.Nil(t, out)
	assertWorkStatus(t, e, 500)
	h.m.runtime = nil
	_, e = h.m.listWorkActivitiesHTTP(h.ctx, &workActivityListInput{WorkItemID: h.work.String(), Limit: 20})
	assertWorkStatus(t, e, 503)
}
