package guid

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func httpCounter() func() (int64, error) {
	var value atomic.Int64
	return func() (int64, error) { return value.Add(1), nil }
}

func TestHTTPEndpoints(t *testing.T) {
	_, api := humatest.New(t)
	registerHTTP(api, httpCounter())

	response := api.Get("/guid")
	require.Equal(t, http.StatusOK, response.Code)
	var one struct {
		Data string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &one))
	assert.Equal(t, "1", one.Data)

	response = api.Get("/guid/3")
	require.Equal(t, http.StatusOK, response.Code)
	var batch struct {
		Data []string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &batch))
	assert.Equal(t, []string{"2", "3", "4"}, batch.Data)

	response = api.Get("/guid/10000")
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &batch))
	assert.Len(t, batch.Data, 10000)

	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/guid/0").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/guid/10001").Code)
}

func TestHTTPEndpointsHideGeneratorFailures(t *testing.T) {
	_, api := humatest.New(t)
	registerHTTP(api, func() (int64, error) { return 0, assert.AnError })
	assert.Equal(t, http.StatusServiceUnavailable, api.Get("/guid").Code)
	assert.Equal(t, http.StatusServiceUnavailable, api.Get("/guid/2").Code)
}
