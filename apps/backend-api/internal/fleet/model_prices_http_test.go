package fleet

import (
	"github.com/stretchr/testify/require"
	"net/http"
	"strings"
	"testing"
)

func TestModelPriceHTTPExactMicrosPreconditionsAndTenantFence(t *testing.T) {
	m, _, _, _, _ := enrollmentHarness(t)
	router := fleetBusinessRouter(t, m)
	path := "/tenants/7/model-prices/CODEX/proxy"
	body := `{"input_micros_per_mtok":"9007199254740993","output_micros_per_mtok":"17","cache_read_micros_per_mtok":"3","cache_write_micros_per_mtok":"7"}`
	response := fleetHTTPRequest(router, http.MethodPut, path, body, nil)
	require.Equal(t, 428, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"code":140428`)
	response = fleetHTTPRequest(router, http.MethodPut, path, body, map[string]string{"If-Match": "\"0\""})
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "\"1\"", response.Header().Get("ETag"))
	require.Contains(t, response.Body.String(), `"9007199254740993"`)
	response = fleetHTTPRequest(router, http.MethodPut, path, strings.Replace(body, "9007199254740993", "1", 1), map[string]string{"If-Match": "\"0\""})
	require.Equal(t, 409, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"code":140409`)
	response = fleetHTTPRequest(router, http.MethodPut, path, strings.Replace(body, "9007199254740993", "9223372036854775808", 1), map[string]string{"If-Match": "\"1\""})
	require.Equal(t, 422, response.Code, response.Body.String())
	response = fleetHTTPRequest(router, http.MethodGet, "/tenants/7/model-prices?limit=1", "", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "proxy")
	response = fleetHTTPRequest(router, http.MethodGet, "/tenants/8/model-prices", "", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "proxy")
	response = fleetHTTPRequest(router, http.MethodDelete, path, "", map[string]string{"If-Match": "\"1\""})
	require.Equal(t, 204, response.Code, response.Body.String())
	response = fleetHTTPRequest(router, http.MethodGet, "/tenants/7/model-prices", "", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "proxy")
}
