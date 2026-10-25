package planning

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestHeavyHTTPReplayRechecksCurrentTenantAndPrincipal(t *testing.T) {
	h, in := heavyFixture(t)
	input := &HeavyCreateInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Key: "heavy-request", Body: in}
	first, e := h.m.createHeavyHTTP(claims(t, 2), input)
	require.NoError(t, e)
	require.Equal(t, 201, first.Status)
	// The command normalizes a private copy; posted branches remain reusable.
	again, e := h.m.createHeavyHTTP(claims(t, 2), input)
	require.NoError(t, e)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, first.Body.Data.ID, again.Body.Data.ID)
	foreign := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 2, PrincipalID: 2, SubjectType: authn.SubjectTypePrincipal})
	_, e = h.m.createHeavyHTTP(foreign, input)
	require.Error(t, e)
	h.m.members = members{}
	_, e = h.m.createHeavyHTTP(claims(t, 8), input)
	require.Error(t, e)
	q := query.BindHeavyRuns(h.db, h.m.names)
	n, e := q.WithContext(t.Context()).Where(q.TenantID.Eq(guid.ID(1))).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, n)
}

func TestHeavyHTTPReadLaunchCancelAndCurrentPermissionReplay(t *testing.T) {
	h, body := heavyFixture(t)
	ctx := claims(t, 2)
	created, e := h.m.createHeavyHTTP(ctx, &HeavyCreateInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Key: "heavy-lifecycle", Body: body})
	require.NoError(t, e)
	ref := HeavyRefInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, ProjectID: "3", RunID: created.Body.Data.ID.String()}
	out, e := h.m.getHeavyHTTP(ctx, &ref)
	require.NoError(t, e)
	require.Equal(t, "DRAFT", out.Body.Data.State)
	require.Len(t, out.Body.Data.Branches, 2)
	in := &HeavyChangeInput{HeavyRefInput: ref, Key: "launch-heavy"}
	in.Body.Version = 1
	in.Body.Action = "LAUNCH"
	launched, e := h.m.changeHeavyHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "UNAVAILABLE", launched.Body.Data.State)
	replay, e := h.m.changeHeavyHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	in.Key = "cancel-heavy"
	in.Body.Version = 2
	in.Body.Action = "CANCEL"
	canceled, e := h.m.changeHeavyHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "CANCEL_REQUESTED", canceled.Body.Data.State)
	out, e = h.m.getHeavyHTTP(claims(t, 6), &ref)
	require.Error(t, e)
	require.Nil(t, out)
	ref.RunID = "999"
	out, e = h.m.getHeavyHTTP(ctx, &ref)
	require.Error(t, e)
	require.Nil(t, out)
	ref.RunID = "bad"
	out, e = h.m.getHeavyHTTP(ctx, &ref)
	require.Error(t, e)
	require.Nil(t, out)
	in.HeavyRefInput = ref
	_, e = h.m.changeHeavyHTTP(ctx, in)
	require.Error(t, e)
	ref.RunID = created.Body.Data.ID.String()
	h.m.runtime = nil
	out, e = h.m.getHeavyHTTP(ctx, &ref)
	require.Error(t, e)
	require.Nil(t, out)
}
