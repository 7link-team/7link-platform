package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAssignmentReleaseBodyRemainsOptionalAndValidatesCursor(t *testing.T) {
	_, api := humatest.New(t)
	var seen *int64
	huma.Register(api, huma.Operation{OperationID: "assignment-release-body-test", Method: http.MethodPost, Path: assignmentPath + "/release"}, func(_ context.Context, in *assignmentReleaseInput) (*struct{ Body bool }, error) {
		seen = nil
		if in.Body != nil {
			seen = in.Body.SeenSeq
		}
		return &struct{ Body bool }{true}, nil
	})
	require.Equal(t, 200, api.Post("/assignments/1/release", "If-Match: 1").Code)
	require.Nil(t, seen)
	require.Equal(t, 200, api.Post("/assignments/1/release", "If-Match: 1", map[string]any{"seen_seq": 7}).Code)
	require.NotNil(t, seen)
	require.EqualValues(t, 7, *seen)
	require.Equal(t, 422, api.Post("/assignments/1/release", "If-Match: 1", map[string]any{"seen_seq": -1}).Code)
}

func TestAssignmentReleaseStaleContextIsAtomicAndSameKeyRefreshes(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	claimed, err := m.createAssignmentHTTP(ctx, &assignmentCreateInput{WorkItemID: work.ID.String(), IfMatch: fmtETag(work.Version), IdempotencyKey: "context-claim", Body: assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11}})
	require.NoError(t, err)
	seen := claimed.Body.Data.WorkItem.ActivitySeq
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.recordWorkActivityOn(ctx, tx, 1, work.ID, WorkActivityInput{Kind: "COMMENT_ADDED", ActorKind: "HUMAN", ActorID: 12, RefKind: "artifact_comment", RefID: 88, Title: "Changed context"})
		return err
	}))
	in := &assignmentReleaseInput{AssignmentID: claimed.Body.Data.Assignment.ID.String(), IfMatch: fmtETag(claimed.Body.Data.Assignment.Version), IdempotencyKey: "context-release", Body: &assignmentReleaseBody{SeenSeq: &seen}}
	_, err = m.releaseAssignmentHTTP(ctx, in)
	assertWorkStatus(t, err, 409)
	stored, err := query.BindAssignments(db, m.service.names).WithContext(ctx).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", stored.Status)
	seen++
	first, err := m.releaseAssignmentHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "RELEASED", first.Body.Data.Assignment.Status)
	again, err := m.releaseAssignmentHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, first.Body.Data, again.Body.Data)
}

func TestAssignmentHTTPReplayOwnershipHistoryAndIsolation(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	input := &assignmentCreateInput{WorkItemID: work.ID.String(), IfMatch: fmtETag(work.Version), IdempotencyKey: "claim-once", Body: assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11}}
	created, err := m.createAssignmentHTTP(ctx, input)
	require.NoError(t, err)
	replay, err := m.createAssignmentHTTP(ctx, input)
	require.NoError(t, err)
	require.Equal(t, created.Body.Data, replay.Body.Data)
	id := created.Body.Data.Assignment.ID
	trusted, found, err := m.assignmentOwner(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, authz.ResourceOwner{ScopeType: "tenant", TenantID: 1, EntityID: 99}, trusted)
	trusted, found, err = m.workOwner(ctx, work.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, guid.ID(99), trusted.EntityID)
	detail, err := m.getAssignmentHTTP(ctx, &assignmentGetInput{AssignmentID: id.String()})
	require.NoError(t, err)
	require.Equal(t, fmtETag(1), detail.ETag)
	ttl := 3600
	renew := &assignmentLeaseInput{AssignmentID: id.String(), IfMatch: fmtETag(1), IdempotencyKey: "renew-once", Body: assignmentLeaseBody{TTLSeconds: &ttl}}
	changed, err := m.renewAssignmentHTTP(ctx, renew)
	require.NoError(t, err)
	replay, err = m.renewAssignmentHTTP(ctx, renew)
	require.NoError(t, err)
	require.Equal(t, changed.Body.Data, replay.Body.Data)
	release := &assignmentReleaseInput{AssignmentID: id.String(), IfMatch: fmtETag(2), IdempotencyKey: "release-once"}
	changed, err = m.releaseAssignmentHTTP(ctx, release)
	require.NoError(t, err)
	require.Equal(t, "RELEASED", changed.Body.Data.Assignment.Status)
	replay, err = m.releaseAssignmentHTTP(ctx, release)
	require.NoError(t, err)
	require.Equal(t, changed.Body.Data, replay.Body.Data)
	history, err := m.listAssignmentsHTTP(ctx, &assignmentListInput{WorkItemID: work.ID.String(), Limit: 1, Status: "RELEASED"})
	require.NoError(t, err)
	require.Len(t, history.Body.Data, 1)
	history, err = m.listAssignmentsHTTP(ctx, &assignmentListInput{WorkItemID: work.ID.String(), Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Empty(t, history.Body.Data)
	for _, claims := range []*authn.Claims{{TenantID: 2, EntityID: 99, PrincipalID: 11}, {TenantID: 1, EntityID: 98, PrincipalID: 11}} {
		other := authn.WithClaims(ctx, claims)
		_, err = m.getAssignmentHTTP(other, &assignmentGetInput{AssignmentID: id.String()})
		require.Error(t, err)
		_, err = m.listAssignmentsHTTP(other, &assignmentListInput{WorkItemID: work.ID.String(), Limit: 50})
		require.Error(t, err)
	}
	count, err := query.BindAssignments(db, m.service.names).WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestAssignmentActualHTTPContractsAndErrors(t *testing.T) {
	m, _, ctx, work := assignmentHarness(t, true)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "assignment-contract-create", Method: http.MethodPost, Path: workAssignmentsPath, DefaultStatus: 201}, func(_ context.Context, in *assignmentCreateInput) (*deliveryCommandOutput[assignmentReceipt], error) {
		return m.createAssignmentHTTP(ctx, in)
	})
	path := "/work-items/" + work.ID.String() + "/assignments"
	body := map[string]any{"role": "EXECUTOR", "assignee_principal_id": "11"}
	require.Equal(t, 422, api.Post(path, body).Code)
	require.Equal(t, 422, api.Post(path, "If-Match: 1", body).Code)
	for _, invalid := range []map[string]any{{"role": "REVIEWER", "assignee_principal_id": "11"}, {"role": "EXECUTOR", "assignee_principal_id": "11", "lease_ttl_seconds": 899}, {"role": "EXECUTOR", "assignee_principal_id": "11", "lease_ttl_seconds": 86401}} {
		require.Equal(t, 422, api.Post(path, "If-Match: "+fmtETag(work.Version), invalid).Code)
	}
	response := api.Post(path, "If-Match: "+fmtETag(work.Version), "Idempotency-Key: actual-http-claim", body)
	require.Equal(t, 201, response.Code, response.Body.String())
	response = api.Post(path, "If-Match: "+fmtETag(work.Version), body)
	require.Equal(t, 409, response.Code, response.Body.String())
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.EqualValues(t, 120426, envelope["code"])
}

func TestAssignmentReadDataScopeUsesWorkCreator(t *testing.T) {
	m, _, ctx, work := assignmentHarness(t, true)
	claimed, err := m.createAssignmentHTTP(authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal}), &assignmentCreateInput{WorkItemID: work.ID.String(), IfMatch: fmtETag(work.Version), Body: assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11}})
	require.NoError(t, err)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
	}
	_, err = m.getAssignmentHTTP(ctx, &assignmentGetInput{AssignmentID: claimed.Body.Data.Assignment.ID.String()})
	require.Error(t, err)
	_, err = m.listAssignmentsHTTP(ctx, &assignmentListInput{WorkItemID: work.ID.String(), Limit: 50})
	require.Error(t, err)
}

func TestAssignmentReplayRequiresCurrentExecuteScope(t *testing.T) {
	m, _, ctx, work := assignmentHarness(t, true)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	input := &assignmentCreateInput{WorkItemID: work.ID.String(), IfMatch: fmtETag(work.Version), IdempotencyKey: "revoked-replay", Body: assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11}}
	_, err := m.createAssignmentHTTP(ctx, input)
	require.NoError(t, err)
	m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "work_item_read"}, nil
	}
	_, err = m.createAssignmentHTTP(ctx, input)
	require.Error(t, err)
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 404, status.GetStatus()) // Scoped-out resources retain the existing non-disclosure response.
}

func TestAssignmentHTTPValidationAndUnavailable(t *testing.T) {
	m := &Module{}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11})
	for _, id := range []string{"bad", "1"} {
		want := 422
		if id == "1" {
			want = 503
		}
		_, err := m.getAssignmentHTTP(ctx, &assignmentGetInput{AssignmentID: id})
		assertWorkStatus(t, err, want)
		_, err = m.releaseAssignmentHTTP(ctx, &assignmentReleaseInput{AssignmentID: id, IfMatch: `"1"`})
		assertWorkStatus(t, err, want)
		_, err = m.createAssignmentHTTP(ctx, &assignmentCreateInput{WorkItemID: id, IfMatch: `"1"`})
		assertWorkStatus(t, err, want)
		_, err = m.listAssignmentsHTTP(ctx, &assignmentListInput{WorkItemID: id, Limit: 50})
		assertWorkStatus(t, err, want)
	}
	for _, in := range []*assignmentListInput{{WorkItemID: "1", Limit: 101}, {WorkItemID: "1", Limit: 50, Status: "UNKNOWN"}, {WorkItemID: "1", Limit: 50, Role: "UNKNOWN"}, {WorkItemID: "1", Limit: 50, Offset: -1}} {
		_, err := m.listAssignmentsHTTP(ctx, in)
		assertWorkStatus(t, err, 422)
	}
	for _, resolve := range []func(context.Context, guid.ID) (authz.ResourceOwner, bool, error){m.workOwner, m.assignmentOwner} {
		_, found, err := resolve(ctx, 1)
		require.ErrorIs(t, err, ErrUnavailable)
		require.False(t, found)
	}
	m, _, ctx, _ = assignmentHarness(t, true)
	for _, resolve := range []func(context.Context, guid.ID) (authz.ResourceOwner, bool, error){m.workOwner, m.assignmentOwner} {
		_, found, err := resolve(ctx, 0)
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.False(t, found)
		_, found, err = resolve(ctx, 999)
		require.NoError(t, err)
		require.False(t, found)
	}
}
