package execution

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVisualReviewHTTPValidatesExecutionAndCurrentAuthority(t *testing.T) {
	f, execution, review := visualReviewFixture(t)
	page, err := f.m.visualReviewsHTTP(f.human, &visualReviewsInput{ExecutionInput: ExecutionInput{ExecutionID: execution.String()}, Limit: 1})
	require.NoError(t, err)
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	require.Contains(t, string(encoded), review.PlanHash)
	require.Contains(t, string(encoded), review.CaptureReceiptHash)
	require.NotContains(t, string(encoded), "nonce")
	require.NotContains(t, string(encoded), "source_workdir")
	require.NotContains(t, string(encoded), "preparedVisual")
	_, err = f.m.visualReviewsHTTP(f.human, &visualReviewsInput{ExecutionInput: ExecutionInput{ExecutionID: execution.String()}, Limit: 101})
	require.Error(t, err)
	in := &visualReviewInput{ExecutionInput: ExecutionInput{ExecutionID: execution.String()}, Body: review}
	out, err := f.m.visualReviewHTTP(f.human, in)
	require.NoError(t, err)
	require.NotNil(t, out)
	f.authorized = false
	_, err = f.m.visualReviewsHTTP(f.human, &visualReviewsInput{ExecutionInput: ExecutionInput{ExecutionID: execution.String()}, Limit: 50})
	require.Error(t, err)
	_, err = f.m.visualReviewHTTP(f.human, in)
	require.Error(t, err)
	in.ExecutionID = "invalid"
	_, err = f.m.visualReviewHTTP(f.human, in)
	require.Error(t, err)
	in.ExecutionID = execution.String()
	f.m.runtime = nil
	_, err = f.m.visualReviewHTTP(f.human, in)
	require.Error(t, err)
}
