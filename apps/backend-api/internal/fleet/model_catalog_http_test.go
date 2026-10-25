package fleet

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestManualModelsHTTPBoundedContractAndTenantIsolation(t *testing.T) {
	m, _, _, _, _ := enrollmentHarness(t)
	router := fleetBusinessRouter(t, m)
	base := "/machines/1/model-catalogs/CODEX"
	response := fleetHTTPRequest(router, http.MethodGet, base, "", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = fleetHTTPRequest(router, http.MethodPut, base+"/manual-models", `{"expected_version":0,"model_ids":["proxy"],"families":{"proxy":"google"}}`, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	var out struct {
		Data ModelCatalog `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &out))
	require.Equal(t, 1, out.Data.Version)
	require.Equal(t, []string{"proxy"}, out.Data.ManualIDs)
	for _, body := range []string{`{"expected_version":0,"model_ids":["changed"]}`, `{"expected_version":1,"model_ids":["proxy"],"families":{"foreign":"openai"}}`} {
		response = fleetHTTPRequest(router, http.MethodPut, base+"/manual-models", body, nil)
		require.Contains(t, []int{409, 422}, response.Code, response.Body.String())
	}
	response = fleetHTTPRequest(router, http.MethodGet, "/tenants/8/machines/1/model-catalogs/CODEX", "", nil)
	require.Contains(t, []int{403, 404}, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "proxy")
	response = fleetHTTPRequest(router, http.MethodGet, "/machines/1/model-catalogs/UNKNOWN", "", nil)
	require.Equal(t, 422, response.Code, response.Body.String())
}
