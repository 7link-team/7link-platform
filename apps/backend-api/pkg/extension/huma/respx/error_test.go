package respx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsStatusErrorRecognizesAChosenResponseAnywhereInTheChain(t *testing.T) {
	chosen := huma.Error422UnprocessableEntity(InvalidID)
	assert.True(t, IsStatusError(chosen))
	assert.True(t, IsStatusError(fmt.Errorf("handler: %w", chosen)))
	assert.True(t, IsStatusError(huma.ErrorWithHeaders(chosen, http.Header{"Retry-After": {"1"}})))
	assert.True(t, IsStatusError(Err(t.Context(), 100001, InvalidArgument)))
	assert.False(t, IsStatusError(errors.New("domain failure")))
	assert.False(t, IsStatusError(nil))
}

func TestErrStatusCarriesItsStatusBusinessCodeAndData(t *testing.T) {
	detail := map[string]int{"revision": 7}
	err := ErrStatus(t.Context(), http.StatusConflict, 102003, "files.file_operation_conflict", detail)

	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, http.StatusConflict, status.GetStatus(), "the transport status is the caller's")
	raw, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)
	var body struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]int `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Equal(t, 102003, body.Code, "the business code, not the status, is the discriminator")
	assert.Equal(t, "files.file_operation_conflict", body.Message)
	assert.Equal(t, detail, body.Data)

	var plain *errorEnvelope
	require.ErrorAs(t, Err(t.Context(), 100001, InvalidArgument), &plain)
	assert.Nil(t, plain.Data, "Err keeps data null")
	assert.Equal(t, http.StatusBadRequest, plain.GetStatus())
}
