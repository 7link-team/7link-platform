package delivery

import (
	"context"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkTransitionHTTPHumanCursorOptionalAndTypedBodyValidation(t *testing.T) {
	m, db, ctx, id := transitionFixture(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "transition-contract", Method: "POST", Path: workTransitionPath}, func(_ context.Context, in *workTransitionInput) (*deliveryCommandOutput[workTransitionView], error) {
		return m.applyWorkTransitionHTTP(ctx, in)
	})
	path := "/work-items/" + id.String() + "/transitions"
	for _, body := range []string{`{"to_state_key":"scheduled","seen_seq":-1}`, `{"to_state_key":"scheduled","seen_seq":"1"}`, `{"to_state_key":"not valid"}`, `{}`} {
		before := transitionSnapshot(t, m, db, id)
		require.Equal(t, 422, api.Post(path, "Idempotency-Key: invalid-transition", strings.NewReader(body)).Code)
		require.Equal(t, before, transitionSnapshot(t, m, db, id))
	}
	require.Equal(t, 422, api.Post(path, strings.NewReader(`{"to_state_key":"scheduled"}`)).Code)
	response := api.Post(path, "Idempotency-Key: human-no-cursor", strings.NewReader(`{"to_state_key":"scheduled"}`))
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"state_key":"scheduled"`)
}

func TestWorkTransitionHTTPSameKeyRefreshAndCurrentReplayAuthority(t *testing.T) {
	m, db, ctx, id := transitionFixture(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 14, SubjectType: authn.SubjectTypePrincipal})
	in := &workTransitionInput{WorkItemID: id.String(), IdempotencyKey: "display-transition-refresh", Body: workTransitionBody{ToStateKey: "scheduled", Comment: "Reviewed current facts"}}
	before := transitionSnapshot(t, m, db, id)
	_, err := m.applyWorkTransitionHTTP(ctx, in)
	assertWorkStatus(t, err, 422)
	raw, marshalErr := respx.MarshalEnvelope(ctx, err)
	require.NoError(t, marshalErr)
	require.Contains(t, string(raw), `"code":126062,`)
	require.Equal(t, before, transitionSnapshot(t, m, db, id))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.recordWorkActivityOn(ctx, tx, 1, id, WorkActivityInput{Kind: "COMMENT_ADDED", ActorKind: "HUMAN", ActorID: 12, RefKind: "artifact_comment", RefID: 900})
		return err
	}))
	before = transitionSnapshot(t, m, db, id)
	seen := int64(0)
	in.Body.SeenSeq = &seen
	_, err = m.applyWorkTransitionHTTP(ctx, in)
	assertWorkStatus(t, err, 409)
	raw, marshalErr = respx.MarshalEnvelope(ctx, err)
	require.NoError(t, marshalErr)
	for _, text := range []string{`"code":126061,`, `"state":"interrupted"`, `"current_seq":1`, `COMMENT_ADDED`, `"operation":"inbox_events.list"`, `"operation":"work_item_transition.apply"`, `RETRY_WITH_SAME_IDEMPOTENCY_KEY`} {
		require.Contains(t, string(raw), text)
	}
	require.Equal(t, before, transitionSnapshot(t, m, db, id))
	seen = 1
	first, err := m.applyWorkTransitionHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, 200, first.Status)
	require.EqualValues(t, 2, first.Body.Data.ActivitySeq)
	again, err := m.applyWorkTransitionHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, first.Body.Data, again.Body.Data)
	in.Body.Comment = "Changed business input"
	_, err = m.applyWorkTransitionHTTP(ctx, in)
	assertWorkStatus(t, err, 422)
	in.Body.Comment = "Reviewed current facts"
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.applyWorkTransitionHTTP(ctx, in)
	require.Error(t, err, "current IAM must reject stored success replay")
}
