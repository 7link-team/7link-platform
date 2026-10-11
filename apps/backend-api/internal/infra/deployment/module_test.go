package deployment

import (
	"encoding/json"
	"net/http"
	"testing"

	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func deploymentIdentityFrom(t *testing.T, body []byte) DeploymentIdentity {
	t.Helper()
	var envelope struct {
		Data DeploymentIdentity `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	return envelope.Data
}

func TestModuleName(t *testing.T) {
	assert.Equal(t, Name, NewModule(nil, Config{}).ModuleName())
}

// The console asks for this before it has a session, so the operation has to be
// reachable without one. Losing that turns the sign-in page into a page that
// cannot name itself.
func TestIdentityIsServedWithoutASession(t *testing.T) {
	respx.Install()
	_, api := humatest.New(t)
	require.NoError(t, NewModule(nil, Config{
		Name: "Acme DeploymentIdentity", Organization: "Acme",
	}).RegisterHTTP(t.Context(), api))

	response := api.Get(Path)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	identity := deploymentIdentityFrom(t, response.Body.Bytes())
	assert.Equal(t, "Acme DeploymentIdentity", identity.Name)
	assert.Equal(t, "Acme", identity.Organization)

	operation := api.OpenAPI().Paths[Path].Get
	require.NotNil(t, operation)
	assert.Equal(t, "deployment-identity", operation.OperationID)
	assert.True(t, operation.Metadata["public"] == true || len(operation.Security) == 0,
		"the sign-in page reads this before it can authenticate: %v", operation.Metadata)
}

// A deployment that names nothing is answered, not left waiting: the client
// owns the neutral wording, and it can only apply it once it has an answer.
func TestUnconfiguredDeploymentIsStillAnswered(t *testing.T) {
	respx.Install()
	_, api := humatest.New(t)
	require.NoError(t, NewModule(nil, Config{}).RegisterHTTP(t.Context(), api))

	response := api.Get(Path)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	identity := deploymentIdentityFrom(t, response.Body.Bytes())
	assert.Empty(t, identity.Name)
	assert.Empty(t, identity.Organization)
}

// Either name may be configured without the other.
func TestEitherNameMayStandAlone(t *testing.T) {
	respx.Install()
	for _, test := range []struct{ name, organization string }{
		{name: "Acme DeploymentIdentity"},
		{organization: "Acme"},
	} {
		_, api := humatest.New(t)
		require.NoError(t, NewModule(nil, Config{
			Name: test.name, Organization: test.organization,
		}).RegisterHTTP(t.Context(), api))
		identity := deploymentIdentityFrom(t, api.Get(Path).Body.Bytes())
		assert.Equal(t, test.name, identity.Name)
		assert.Equal(t, test.organization, identity.Organization)
	}
}
