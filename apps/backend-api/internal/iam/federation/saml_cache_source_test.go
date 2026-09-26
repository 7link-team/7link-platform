package federation

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSAMLMetadataCacheBindsSecuritySource(t *testing.T) {
	env := newFederationEnvironment(t)
	var firstRequests, secondRequests atomic.Int32
	metadataServer := func(entityID string, requests *atomic.Int32) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = fmt.Fprintf(w, `<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="%s"><md:IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"/></md:EntityDescriptor>`, entityID)
		}))
		t.Cleanup(server.Close)
		return server
	}
	first := metadataServer("first-idp", &firstRequests)
	second := metadataServer("second-idp", &secondRequests)
	provider := providerRow{ID: testID("cache-source"), ProviderType: ProviderSAML, Issuer: first.URL, ClientID: "first-client"}
	descriptor, err := env.service.samlIDPMetadata(t.Context(), provider)
	require.NoError(t, err)
	require.Equal(t, "first-idp", descriptor.EntityID)
	_, err = env.service.samlIDPMetadata(t.Context(), provider)
	require.NoError(t, err)
	require.Equal(t, int32(1), firstRequests.Load(), "unchanged security source reuses metadata")
	provider.ClientID = "replacement-client"
	_, err = env.service.samlIDPMetadata(t.Context(), provider)
	require.NoError(t, err)
	require.Equal(t, int32(2), firstRequests.Load(), "client change invalidates cached metadata")
	provider.Issuer = second.URL
	descriptor, err = env.service.samlIDPMetadata(t.Context(), provider)
	require.NoError(t, err)
	require.Equal(t, "second-idp", descriptor.EntityID)
	require.Equal(t, int32(1), secondRequests.Load())
	second.Close()
	provider.ClientID = "unavailable-replacement"
	_, err = env.service.samlIDPMetadata(t.Context(), provider)
	require.ErrorIs(t, err, ErrSAMLResponse, "changed source cannot fall back to cached old trust")
}
