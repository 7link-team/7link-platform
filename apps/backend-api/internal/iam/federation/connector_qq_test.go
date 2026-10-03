package federation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// qqServer stands in for QQ Connect across its three endpoints, each answering
// in the shape QQ actually uses.
type qqServer struct {
	server      *httptest.Server
	tokenBody   string
	identity    string
	profile     string
	profileReq  *http.Request
	identityReq *http.Request
}

func newQQServer(t *testing.T, tokenBody, identity, profile string) *qqServer {
	t.Helper()
	fake := &qqServer{tokenBody: tokenBody, identity: identity, profile: profile}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2.0/token", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(fake.tokenBody))
	})
	mux.HandleFunc("/oauth2.0/me", func(writer http.ResponseWriter, request *http.Request) {
		fake.identityReq = request.Clone(context.Background())
		_, _ = writer.Write([]byte(fake.identity))
	})
	mux.HandleFunc("/user/get_user_info", func(writer http.ResponseWriter, request *http.Request) {
		fake.profileReq = request.Clone(context.Background())
		_, _ = writer.Write([]byte(fake.profile))
	})
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

// run points the catalog's QQ connector at the fake, leaving the registered one
// untouched: connector is a value and the flow's endpoints are fields.
func (q *qqServer) run(t *testing.T) (map[string]any, error) {
	t.Helper()
	conn := socialConnectors["qq"]
	conn.Token = q.server.URL + "/oauth2.0/token"
	conn.Flow = qqFlow{
		identityEndpoint: q.server.URL + "/oauth2.0/me",
		profileEndpoint:  q.server.URL + "/user/get_user_info",
	}
	return conn.profile(context.Background(), &upstreamHTTP{client: q.server.Client()},
		credentials{ClientID: "the-app", ClientSecret: "the-secret"}, "code", "https://cb", "verifier")
}

func TestQQFlowReadsAFormEncodedTokenAndJSONPIdentity(t *testing.T) {
	// This is QQ's documented pair of oddities: the token endpoint answers
	// form encoding and the identity endpoint answers JSONP.
	fake := newQQServer(t,
		"access_token=AT&expires_in=7776000&refresh_token=RT",
		`callback( {"client_id":"the-app","openid":"OPENID-1"} );`,
		`{"ret":0,"nickname":"阿达","figureurl_qq_1":"https://example.com/a.png"}`)

	document, err := fake.run(t)
	require.NoError(t, err)

	conn := socialConnectors["qq"]
	claims, err := conn.claims(document)
	require.NoError(t, err)
	assert.Equal(t, "OPENID-1", claims.Subject, "the identifier comes from the identity endpoint, not the profile")
	assert.Equal(t, "阿达", claims.Name)
	assert.Empty(t, claims.Email, "QQ states no address")
	assert.Zero(t, claims.AuthTime)

	// The profile read is keyed by application and user together.
	require.NotNil(t, fake.profileReq)
	query := fake.profileReq.URL.Query()
	assert.Equal(t, "AT", query.Get("access_token"))
	assert.Equal(t, "the-app", query.Get("oauth_consumer_key"))
	assert.Equal(t, "OPENID-1", query.Get("openid"))
	assert.Equal(t, "json", fake.identityReq.URL.Query().Get("fmt"), "fmt=json is requested even though JSONP is tolerated")
}

func TestQQFlowAcceptsAJSONTokenResponse(t *testing.T) {
	// fmt=json is requested, so the honored form has to work too.
	fake := newQQServer(t,
		`{"access_token":"AT","expires_in":7776000}`,
		`{"openid":"OPENID-2"}`,
		`{"ret":0,"nickname":"Ada"}`)

	document, err := fake.run(t)
	require.NoError(t, err)
	assert.Equal(t, "OPENID-2", claimString(document, "openid"))
	assert.Equal(t, "Ada", claimString(document, "nickname"))
}

func TestQQFlowSurfacesAFailureReportedWithStatus200(t *testing.T) {
	// QQ reports profile failures in the body, so a non-zero ret has to be read
	// as an error or a failed read would look like an empty profile.
	fake := newQQServer(t,
		`{"access_token":"AT"}`,
		`{"openid":"OPENID-3"}`,
		`{"ret":100007,"msg":"param access_token is wrong or lost"}`)

	_, err := fake.run(t)
	require.ErrorIs(t, err, ErrConnectorProfile)
	assert.Contains(t, err.Error(), "100007")
	assert.Contains(t, err.Error(), "access_token")
}

func TestQQFlowRefusesAnIdentityResponseWithoutAnOpenID(t *testing.T) {
	fake := newQQServer(t, `{"access_token":"AT"}`, `{"error":100016}`, `{"ret":0}`)

	_, err := fake.run(t)
	require.ErrorIs(t, err, ErrConnectorProfile)
	assert.Contains(t, err.Error(), "openid")
}

func TestQQFlowReportsATokenRefusalInEitherEncoding(t *testing.T) {
	fake := newQQServer(t, "error=100019&error_description=code+reused", `{}`, `{}`)
	_, err := fake.run(t)
	require.ErrorIs(t, err, ErrOIDCToken)
	assert.Contains(t, err.Error(), "code reused")

	fake = newQQServer(t, `{"error":100019,"error_description":"code expired"}`, `{}`, `{}`)
	_, err = fake.run(t)
	require.ErrorIs(t, err, ErrOIDCToken)
	assert.Contains(t, err.Error(), "code expired")
}

func TestUnwrapJSONPLeavesPlainJSONAlone(t *testing.T) {
	assert.JSONEq(t, `{"a":1}`, string(unwrapJSONP([]byte(`{"a":1}`))))
	assert.JSONEq(t, `{"a":1}`, string(unwrapJSONP([]byte(`callback( {"a":1} );`))))
	assert.Equal(t, "not json", string(unwrapJSONP([]byte("not json"))), "input with no object is returned untouched")
}

func TestQQFlowDefaultsToQQsOwnEndpoints(t *testing.T) {
	// The catalog registers qqFlow{}, so the zero value has to resolve to the
	// real addresses; an empty endpoint would be requested against nothing.
	flow := qqFlow{}
	assert.Equal(t, qqIdentityEndpoint, flow.identity())
	assert.Equal(t, qqProfileEndpoint, flow.profile())

	overridden := qqFlow{identityEndpoint: "https://local/me", profileEndpoint: "https://local/info"}
	assert.Equal(t, "https://local/me", overridden.identity())
	assert.Equal(t, "https://local/info", overridden.profile())

	registered, err := lookupConnector("qq")
	require.NoError(t, err)
	assert.Equal(t, qqFlow{}, registered.Flow, "the catalog holds the zero value, so the defaults are what production uses")
}

func TestQQFlowStopsWhenTheProfileReadFails(t *testing.T) {
	fake := newQQServer(t, `{"access_token":"AT"}`, `{"openid":"OPENID"}`, `not json`)

	_, err := fake.run(t)
	require.ErrorIs(t, err, ErrConnectorProfile)
}
