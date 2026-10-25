package delivery

import (
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"strconv"
	"strings"
	"testing"
)

func TestCommentDefectHTTPContractAndOriginalSource(t *testing.T) {
	m, _, ctx, _, _ := commentDefectHarness(t)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "comment-defect-contract", Method: "POST", Path: commentDefectPath, DefaultStatus: 201}, func(_ context.Context, in *commentDefectInput) (*deliveryCommandOutput[CommentDefectView], error) {
		return m.createCommentDefectHTTP(ctx, in)
	})
	valid := `{"severity":"CRITICAL","expected":"Expected result","actual":"Caller override"}`
	for i, missing := range []string{`{"severity":"CRITICAL"}`, `{"expected":"Expected result"}`, `{"severity":"CRITICAL","expected":""}`} {
		response := api.Post("/comment-threads/80/defects", "Idempotency-Key: missing-"+strconv.Itoa(i), strings.NewReader(missing))
		require.Equal(t, 422, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "422126020")
	}
	response := api.Post("/comment-threads/80/defects", "Idempotency-Key: contract", strings.NewReader(valid))
	require.Equal(t, 201, response.Code, response.Body.String())
	var out respx.Envelope[CommentDefectView]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &out))
	require.Equal(t, "Wrong return value", out.Data.SourceFirstBody)
	require.Equal(t, "Caller override", out.Data.Actual)
	require.EqualValues(t, 71, out.Data.ExecutionID)
	require.NotEmpty(t, out.Meta.Next)
	response = api.Post("/comment-threads/80/defects", "Idempotency-Key: other", strings.NewReader(valid))
	require.Equal(t, 422, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "422126063")
	require.Contains(t, response.Body.String(), out.Data.ID.String())
	require.Equal(t, 422, api.Post("/comment-threads/80/defects", strings.NewReader(valid)).Code)
}
