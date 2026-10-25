package templates

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestRubricScoreHTTPReplayCurrentAuthorityCASAndReadProjection(t *testing.T) {
	m, db, ctx, _, input := rubricScoreFixture(t)
	in := &rubricWorkCreateInput{RubricWorkPath: RubricWorkPath{EntityID: "99", WorkID: "700"}, IdempotencyKey: "rubric-once", Body: input}
	first, e := m.rubricWorkCreateHTTP(ctx, in)
	require.NoError(t, e)
	second, e := m.rubricWorkCreateHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data.ID, second.Body.Data.ID)
	board, e := m.rubricWorkReadHTTP(ctx, &in.RubricWorkPath)
	require.NoError(t, e)
	require.Len(t, board.Body.Data.Receipts, 1)
	require.Equal(t, []string{"HUMAN_REVIEW"}, board.Body.Data.AllowedStages)
	in.Body.Scores[0].Note = "Changed"
	_, e = m.rubricWorkCreateHTTP(ctx, in)
	assertStatus(t, e, 409)
	in.Body = input
	in.Body.WorkVersion = 2
	_, e = m.rubricWorkCreateHTTP(ctx, in)
	assertStatus(t, e, 409)
	m.rubricSubjects.WorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, string) (delivery.RubricSubject, error) {
		return delivery.RubricSubject{}, delivery.ErrWorkForbidden
	}
	in.Body = input
	_, e = m.rubricWorkCreateHTTP(ctx, in)
	assertStatus(t, e, 403)
	q := query.BindLifecycleReceipts(db, m.names)
	n, e := q.WithContext(ctx).Where(q.EventType.Eq("rubric.score_recorded")).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, n)
}

func TestRubricPackageFormRequiresCurrentWriteAuthority(t *testing.T) {
	m, _, ctx, target, _ := rubricScoreFixture(t)
	target.ActorKind = "CLIENT"
	target.PackageHash = "published"
	target.Version = 4
	denied := true
	m.rubricSubjects.PackageOn = func(_ context.Context, _ *gorm.DB, in delivery.RubricClientRequest) (delivery.RubricSubject, error) {
		if in.Write && denied {
			return delivery.RubricSubject{}, delivery.ErrWorkForbidden
		}
		return target, nil
	}
	in := &RubricPackagePath{EntityID: "99", PackageID: "900", ItemID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	out, err := m.rubricPackageReadHTTP(ctx, in)
	require.NoError(t, err)
	require.Empty(t, out.Body.Data.AllowedStages)
	denied = false
	out, err = m.rubricPackageReadHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, []string{"CLIENT_ACCEPTANCE"}, out.Body.Data.AllowedStages)
}

func TestRubricPackageHTTPAcceptsNullResultAndRejectsFabricatedZeroID(t *testing.T) {
	m, _, ctx, target, input := rubricScoreFixture(t)
	target.ActorKind = "CLIENT"
	target.PackageHash = strings.Repeat("a", 64)
	target.Version = 4
	m.rubricSubjects.PackageOn = func(_ context.Context, _ *gorm.DB, in delivery.RubricClientRequest) (delivery.RubricSubject, error) {
		require.Equal(t, "CLIENT_ACCEPTANCE", in.Stage)
		require.Equal(t, 4, in.Version)
		return target, nil
	}
	input.Stage = "CLIENT_ACCEPTANCE"
	input.ResultID = 0
	input.WorkVersion = 4
	_, api := humatest.New(t)
	path := "/entities/{entity_id}/delivery-packages/{package_id}/rubric-scores"
	huma.Register(api, huma.Operation{OperationID: "rubric-package-wire", Method: "POST", Path: path, DefaultStatus: 201}, func(_ context.Context, in *rubricPackageCreateInput) (*respx.Body[RubricScoreReceipt], error) {
		require.Equal(t, "99", in.EntityID)
		require.Equal(t, "900", in.PackageID)
		require.Equal(t, strings.Repeat("a", 64), in.ItemID)
		require.Equal(t, "actual-client", in.IdempotencyKey)
		return m.rubricPackageCreateHTTP(ctx, in)
	})
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	url := "/entities/99/delivery-packages/900/rubric-scores?item_id=" + strings.Repeat("a", 64)
	bad := api.Post(url, "Idempotency-Key: bad-zero", strings.NewReader(strings.Replace(string(raw), `"result_id":null`, `"result_id":"0"`, 1)))
	require.Equal(t, 422, bad.Code)
	response := api.Post(url, "Idempotency-Key: actual-client", strings.NewReader(string(raw)))
	require.Equal(t, 201, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), `"result_id"`)
	require.NotContains(t, response.Body.String(), `"work_item_id"`)
	require.NotContains(t, response.Body.String(), `"authority"`)
}

func TestRubricWorkHTTPBindsCurrentResultAndPreservesDecimalWire(t *testing.T) {
	m, _, ctx, _, input := rubricScoreFixture(t)
	precise := json.Number("4.000000000000000001")
	input.Scores[0].Score = &precise
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "rubric-work-wire", Method: "POST", Path: "/entities/{entity_id}/work-items/{work_item_id}/rubric-scores", DefaultStatus: 201}, func(_ context.Context, in *rubricWorkCreateInput) (*respx.Body[RubricScoreReceipt], error) {
		require.Equal(t, "99", in.EntityID)
		require.Equal(t, "700", in.WorkID)
		require.Equal(t, guid.ID(701), in.Body.ResultID)
		return m.rubricWorkCreateHTTP(ctx, in)
	})
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	response := api.Post("/entities/99/work-items/700/rubric-scores", "Idempotency-Key: actual-human", strings.NewReader(string(raw)))
	require.Equal(t, 201, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "4.000000000000000001")
}

func TestRubricHTTPRejectsInvalidResourcePathsAndSourceUnavailable(t *testing.T) {
	m, _, ctx, _, _ := rubricScoreFixture(t)
	for _, path := range []RubricWorkPath{{EntityID: "bad", WorkID: "700"}, {EntityID: "99", WorkID: "bad"}} {
		_, e := m.rubricWorkReadHTTP(ctx, &path)
		assertStatus(t, e, 422)
		_, e = m.rubricWorkCreateHTTP(ctx, &rubricWorkCreateInput{RubricWorkPath: path})
		assertStatus(t, e, 422)
	}
	for _, path := range []RubricPackagePath{{EntityID: "bad", PackageID: "1"}, {EntityID: "99", PackageID: "bad"}} {
		_, e := m.rubricPackageReadHTTP(ctx, &path)
		assertStatus(t, e, 422)
		_, e = m.rubricPackageCreateHTTP(ctx, &rubricPackageCreateInput{RubricPackagePath: path})
		assertStatus(t, e, 422)
	}
	m.rubricSubjects.WorkOn = nil
	_, e := m.rubricWorkReadHTTP(ctx, &RubricWorkPath{EntityID: "99", WorkID: "700"})
	assertStatus(t, e, 503)
}
