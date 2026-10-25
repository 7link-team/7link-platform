package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestScopeChangeCreatesImmutableBaselineAndReplayRechecksAuthority(t *testing.T) {
	m, db, ctx, row, body := scopeHarness(t)
	in := &scopeChangeInput{EntityID: "99", RequirementID: row.ID.String(), IfMatch: fmtETag(row.Version), IdempotencyKey: "scope-change", Body: body}
	created, err := m.scopeChangeHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, 2, created.Body.Data.ToVersionNo)
	in.Body.ReauthProof = "already-consumed" // proof secrets do not change receipt identity.
	replayed, err := m.scopeChangeHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, created.Body.Data, replayed.Body.Data)
	q := query.BindRequirementVersions(db, m.service.names)
	versions, err := q.WithContext(ctx).Where(q.RequirementID.Eq(row.ID)).Order(q.VersionNo).Find()
	require.NoError(t, err)
	require.Len(t, versions, 2)
	require.Equal(t, "Requirement", versions[0].Title)
	require.Equal(t, *body.Title, versions[1].Title)
	require.Equal(t, &created.Body.Data.ID, versions[1].ChangeRecordID)
	m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{}, nil
	}
	_, err = m.scopeChangeHTTP(ctx, in)
	assertWorkStatus(t, err, 403)
}

func TestScopeHTTPRejectsInvalidPathsMissingRuntimeAndStepupFailures(t *testing.T) {
	m, _, ctx, row, body := scopeHarness(t)
	_, err := m.scopeChangeHTTP(ctx, &scopeChangeInput{EntityID: "invalid", RequirementID: row.ID.String(), IfMatch: fmtETag(row.Version), Body: body})
	assertWorkStatus(t, err, 422)
	_, err = m.changeHistoryHTTP(ctx, &changeHistoryInput{EntityID: "99", RequirementID: "invalid", Limit: 50})
	assertWorkStatus(t, err, 422)
	_, err = m.changeImpactsHTTP(ctx, &changeImpactsInput{EntityID: "99", RequirementID: row.ID.String(), ChangeRecordID: "invalid", Limit: 50})
	assertWorkStatus(t, err, 422)
	for _, failure := range []error{authn.ErrExpiredToken, authn.ErrUnavailable} {
		m.scopeReauthentication = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return failure }
		_, err = m.scopeChangeHTTP(ctx, &scopeChangeInput{EntityID: "99", RequirementID: row.ID.String(), IfMatch: fmtETag(row.Version), Body: body})
		if errors.Is(failure, authn.ErrExpiredToken) {
			assertWorkStatus(t, err, 403)
		} else {
			assertWorkStatus(t, err, 503)
		}
	}
	m.runtime = nil
	_, err = m.changeHistoryHTTP(ctx, &changeHistoryInput{EntityID: "99", RequirementID: row.ID.String(), Limit: 50})
	assertWorkStatus(t, err, 503)
	_, err = m.changeImpactsHTTP(ctx, &changeImpactsInput{EntityID: "99", RequirementID: row.ID.String(), ChangeRecordID: "333", Limit: 50})
	assertWorkStatus(t, err, 503)
}
