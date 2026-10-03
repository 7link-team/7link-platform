package federation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"7link.com/platform/pkg/appenv"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeFixture parses a provider payload the way the engine does, so a test
// asserting on claim paths exercises the same exact-number decoding.
func decodeFixture(t *testing.T, body string) map[string]any {
	t.Helper()
	document, err := decodeJSONObject([]byte(body), ErrConnectorProfile)
	require.NoError(t, err)
	return document
}

func TestClaimValueWalksNestedObjectsAndArrays(t *testing.T) {
	document := decodeFixture(t, `{"response":{"id":"7"},"data":[{"id":"inner"}],"flat":"top"}`)

	assert.Equal(t, "top", claimString(document, "flat"))
	assert.Equal(t, "7", claimString(document, "response.id"))
	assert.Equal(t, "inner", claimString(document, "data.0.id"), "a numeric segment indexes an array")

	assert.Empty(t, claimString(document, ""), "an empty path resolves to nothing")
	assert.Empty(t, claimString(document, "response.missing"))
	assert.Empty(t, claimString(document, "flat.deeper"), "walking into a scalar yields nothing")
	assert.Empty(t, claimString(document, "data.9.id"), "an out-of-range index yields nothing")
	assert.Empty(t, claimString(document, "data.notanindex"))
}

func TestClaimStringKeepsLargeIntegersExact(t *testing.T) {
	// A Discord snowflake exceeds the float64 mantissa; decoding through
	// float64 would round it and bind the login to the wrong subject.
	document := decodeFixture(t, `{"id":1084342872222334977,"small":42}`)

	assert.Equal(t, "1084342872222334977", claimString(document, "id"))
	assert.Equal(t, "42", claimString(document, "small"), "a small integer gains no exponent")
}

func TestClaimBoolAcceptsTheShapesProvidersUse(t *testing.T) {
	document := decodeFixture(t, `{"native":true,"text":"true","one":1,"zero":0,"off":false,"other":"no"}`)

	assert.True(t, claimBool(document, "native"))
	assert.True(t, claimBool(document, "text"))
	assert.True(t, claimBool(document, "one"))
	assert.False(t, claimBool(document, "zero"))
	assert.False(t, claimBool(document, "off"))
	assert.False(t, claimBool(document, "other"))
	assert.False(t, claimBool(document, "absent"))
}

func TestConnectorClaimsMapsAProfileAndLeavesAuthTimeZero(t *testing.T) {
	conn := socialConnectors["x"]
	document := decodeFixture(t, `{"data":{"id":"1234","name":"Ada","username":"ada"}}`)

	claims, err := conn.claims(document)
	require.NoError(t, err)

	assert.Equal(t, "1234", claims.Subject)
	assert.Equal(t, "Ada", claims.Name)
	assert.Equal(t, "ada", claims.PreferredUsername)
	assert.Empty(t, claims.Email)
	assert.Nil(t, claims.EmailVerified, "no address means nothing to declare verified")
	assert.Zero(t, claims.AuthTime, "plain OAuth 2.0 states no upstream authentication time")
}

func TestConnectorClaimsTrustsAnAddressOnlyAsFarAsTheProviderStatesIt(t *testing.T) {
	withField := socialConnectors["discord"]
	require.NotEmpty(t, withField.EmailVerified)

	claims, err := withField.claims(decodeFixture(t, `{"id":"1","email":"a@example.com","verified":false}`))
	require.NoError(t, err)
	require.NotNil(t, claims.EmailVerified)
	assert.False(t, *claims.EmailVerified, "a provider that has the field has to set it")

	claims, err = withField.claims(decodeFixture(t, `{"id":"1","email":"a@example.com","verified":true}`))
	require.NoError(t, err)
	require.NotNil(t, claims.EmailVerified)
	assert.True(t, *claims.EmailVerified)

	// A connector with no verification field is taken at its word, which is
	// what lets GitHub's address provision an account.
	withoutField := socialConnectors["github"]
	require.Empty(t, withoutField.EmailVerified)
	claims, err = withoutField.claims(decodeFixture(t, `{"id":5,"email":"a@example.com"}`))
	require.NoError(t, err)
	require.NotNil(t, claims.EmailVerified)
	assert.True(t, *claims.EmailVerified)
}

func TestConnectorClaimsRefusesWhatWouldNotStore(t *testing.T) {
	conn := socialConnectors["github"]

	_, err := conn.claims(decodeFixture(t, `{"login":"ada"}`))
	require.ErrorIs(t, err, ErrConnectorProfile, "a profile with no subject is unusable")

	_, err = conn.claims(decodeFixture(t, `{"id":"`+strings.Repeat("s", 129)+`"}`))
	require.ErrorIs(t, err, ErrConnectorProfile, "the subject column bounds the subject")

	_, err = conn.claims(decodeFixture(t, `{"id":1,"email":"`+strings.Repeat("e", 256)+`"}`))
	require.ErrorIs(t, err, ErrConnectorProfile, "truncating an address silently would mis-link later")

	_, err = conn.claims(decodeFixture(t, `{"id":1,"name":"`+strings.Repeat("n", 129)+`"}`))
	require.ErrorIs(t, err, ErrConnectorProfile)
}

func TestConnectorAuthorizationURLCarriesStateAndPKCE(t *testing.T) {
	conn := socialConnectors["wechat"]

	raw, err := conn.authorizationURL("the-app-id", "https://console.example.com/cb", "snsapi_login", "state-nonce", "verifier")
	require.NoError(t, err)

	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	query := parsed.Query()

	assert.Equal(t, "https://open.weixin.qq.com/connect/qrconnect", parsed.Scheme+"://"+parsed.Host+parsed.Path)
	assert.Equal(t, "the-app-id", query.Get("appid"), "WeChat names the client parameter appid")
	assert.Empty(t, query.Get("client_id"))
	assert.Equal(t, "code", query.Get("response_type"))
	assert.Equal(t, "https://console.example.com/cb", query.Get("redirect_uri"))
	assert.Equal(t, "state-nonce", query.Get("state"))
	assert.Equal(t, "S256", query.Get("code_challenge_method"))
	assert.Equal(t, codeChallenge("verifier"), query.Get("code_challenge"))
	assert.NotContains(t, raw, "verifier", "the verifier itself never leaves the deployment")
}

// upstreamRecorder serves scripted provider responses and records what reached
// each endpoint, so a test can assert on the request the engine actually built.
type upstreamRecorder struct {
	server      *httptest.Server
	tokenReq    *http.Request
	tokenBody   string
	profileReq  *http.Request
	tokenJSON   string
	profileJSON string
	tokenStatus int
}

func newUpstreamRecorder(t *testing.T, tokenJSON, profileJSON string) *upstreamRecorder {
	t.Helper()
	recorder := &upstreamRecorder{tokenJSON: tokenJSON, profileJSON: profileJSON}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		recorder.tokenBody = string(body)
		recorder.tokenReq = request.Clone(context.Background())
		if recorder.tokenStatus != 0 {
			writer.WriteHeader(recorder.tokenStatus)
		}
		_, _ = writer.Write([]byte(recorder.tokenJSON))
	})
	mux.HandleFunc("/profile", func(writer http.ResponseWriter, request *http.Request) {
		recorder.profileReq = request.Clone(context.Background())
		_, _ = writer.Write([]byte(recorder.profileJSON))
	})
	recorder.server = httptest.NewServer(mux)
	t.Cleanup(recorder.server.Close)
	return recorder
}

func (r *upstreamRecorder) upstream() *upstreamHTTP {
	return &upstreamHTTP{client: r.server.Client()}
}

func (r *upstreamRecorder) bind(conn connector) connector {
	conn.Token = r.server.URL + "/token"
	if conn.Profile != "" {
		conn.Profile = r.server.URL + "/profile"
	}
	return conn
}

func TestStandardExchangeFormStyleKeepsTheSecretInTheHeader(t *testing.T) {
	recorder := newUpstreamRecorder(t, `{"access_token":"at"}`, `{}`)
	conn := recorder.bind(socialConnectors["github"])

	token, err := standardExchange(context.Background(), recorder.upstream(), conn,
		credentials{ClientID: "cid", ClientSecret: "shh"}, "the-code", "https://cb", "verifier")
	require.NoError(t, err)
	assert.Equal(t, "at", token.AccessToken)

	body, err := url.ParseQuery(recorder.tokenBody)
	require.NoError(t, err)
	assert.Equal(t, "authorization_code", body.Get("grant_type"))
	assert.Equal(t, "the-code", body.Get("code"))
	assert.Equal(t, "verifier", body.Get("code_verifier"))
	assert.Equal(t, "cid", body.Get("client_id"))
	assert.NotContains(t, recorder.tokenBody, "shh", "the secret belongs in the Authorization header")

	user, password, ok := recorder.tokenReq.BasicAuth()
	require.True(t, ok)
	assert.Equal(t, "cid", user)
	assert.Equal(t, "shh", password)
	assert.Equal(t, "application/json", recorder.tokenReq.Header.Get("Accept"))
}

func TestStandardExchangeQueryStyleIsAGetWithProviderParameterNames(t *testing.T) {
	recorder := newUpstreamRecorder(t, `{"access_token":"at","openid":"ox"}`, `{}`)
	conn := recorder.bind(socialConnectors["wechat"])

	token, err := standardExchange(context.Background(), recorder.upstream(), conn,
		credentials{ClientID: "appid-value", ClientSecret: "secret-value"}, "code", "https://cb", "verifier")
	require.NoError(t, err)

	// WeChat documents a GET. Posting an empty body to a query-parameter
	// endpoint is the kind of mistake a provider answers with an opaque
	// "invalid request", so the method is asserted rather than assumed.
	assert.Equal(t, http.MethodGet, recorder.tokenReq.Method)
	assert.Empty(t, recorder.tokenBody, "a query-style exchange sends no body")

	query := recorder.tokenReq.URL.Query()
	assert.Equal(t, "appid-value", query.Get("appid"))
	assert.Equal(t, "secret-value", query.Get("secret"))
	assert.Empty(t, query.Get("client_id"))
	_, _, hasBasic := recorder.tokenReq.BasicAuth()
	assert.False(t, hasBasic, "the credentials are already in the query")

	assert.Equal(t, map[string]string{"openid": "ox"}, token.Carried)
}

func TestStandardExchangePostsTheSecretForProvidersThatRefuseBasic(t *testing.T) {
	// Weibo and Douyin only accept client_secret_post. Sending Basic instead
	// fails authentication for a reason the response does not explain.
	recorder := newUpstreamRecorder(t, `{"access_token":"at","uid":"7"}`, `{}`)
	conn := recorder.bind(socialConnectors["weibo"])
	require.Equal(t, clientSecretPost, conn.ClientAuth)

	_, err := standardExchange(context.Background(), recorder.upstream(), conn,
		credentials{ClientID: "cid", ClientSecret: "shh"}, "code", "https://cb", "verifier")
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, recorder.tokenReq.Method)
	body, err := url.ParseQuery(recorder.tokenBody)
	require.NoError(t, err)
	assert.Equal(t, "cid", body.Get("client_id"))
	assert.Equal(t, "shh", body.Get("client_secret"))
	_, _, hasBasic := recorder.tokenReq.BasicAuth()
	assert.False(t, hasBasic, "a provider that refuses Basic must not be sent one")
}

func TestStandardExchangeSendsNoSecretWhenThereIsNone(t *testing.T) {
	// A public client has no secret, and inventing an empty one would send
	// "client_secret=" or an empty Basic header.
	recorder := newUpstreamRecorder(t, `{"access_token":"at"}`, `{}`)
	conn := recorder.bind(socialConnectors["github"])

	_, err := standardExchange(context.Background(), recorder.upstream(), conn,
		credentials{ClientID: "cid"}, "code", "https://cb", "verifier")
	require.NoError(t, err)

	_, _, hasBasic := recorder.tokenReq.BasicAuth()
	assert.False(t, hasBasic)
	body, err := url.ParseQuery(recorder.tokenBody)
	require.NoError(t, err)
	assert.Empty(t, body.Get("client_secret"))
}

func TestStandardExchangeJSONStyleSendsAJSONBody(t *testing.T) {
	recorder := newUpstreamRecorder(t, `{"accessToken":"at","expireIn":7200}`, `{}`)
	conn := recorder.bind(socialConnectors["dingtalk"])

	token, err := standardExchange(context.Background(), recorder.upstream(), conn,
		credentials{ClientID: "the-client", ClientSecret: "the-secret"}, "code", "https://cb", "verifier")
	require.NoError(t, err)
	assert.Equal(t, "at", token.AccessToken, "DingTalk names the field accessToken")

	assert.Equal(t, "application/json", recorder.tokenReq.Header.Get("Content-Type"))
	var sent map[string]string
	require.NoError(t, json.Unmarshal([]byte(recorder.tokenBody), &sent))
	assert.Equal(t, "the-client", sent["clientId"])
	assert.Equal(t, "the-secret", sent["clientSecret"])
	assert.Equal(t, "authorization_code", sent["grantType"])
}

func TestStandardExchangeReadsANestedAccessTokenAndRenamesCarriedFields(t *testing.T) {
	// Douyin posts an ordinary form but wraps its answer, and wants the
	// identifier back under a different name than the one it answered with.
	recorder := newUpstreamRecorder(t, `{"data":{"access_token":"nested","open_id":"oid"},"message":"success"}`, `{}`)
	conn := recorder.bind(socialConnectors["douyin"])

	token, err := standardExchange(context.Background(), recorder.upstream(), conn,
		credentials{ClientID: "ck", ClientSecret: "cs"}, "code", "https://cb", "verifier")
	require.NoError(t, err)
	assert.Equal(t, "nested", token.AccessToken)
	assert.Equal(t, map[string]string{"open_id": "oid"}, token.Carried)

	body, err := url.ParseQuery(recorder.tokenBody)
	require.NoError(t, err)
	assert.Equal(t, "ck", body.Get("client_key"), "Douyin names the client parameter client_key")
	assert.Empty(t, body.Get("client_id"))
}

func TestStandardExchangeReportsAProviderRefusal(t *testing.T) {
	recorder := newUpstreamRecorder(t, `{"error":"bad_verification_code","error_description":"expired"}`, `{}`)
	conn := recorder.bind(socialConnectors["github"])

	_, err := standardExchange(context.Background(), recorder.upstream(), conn,
		credentials{ClientID: "cid"}, "code", "https://cb", "verifier")
	require.ErrorIs(t, err, ErrOIDCToken)
	assert.Contains(t, err.Error(), "expired", "the operator sees the provider's own reason")
}

func TestStandardExchangeRefusesWhenACarriedFieldIsAbsent(t *testing.T) {
	// Losing the openid silently would send a profile request that identifies
	// nobody, so the exchange fails instead.
	recorder := newUpstreamRecorder(t, `{"access_token":"at"}`, `{}`)
	conn := recorder.bind(socialConnectors["wechat"])

	_, err := standardExchange(context.Background(), recorder.upstream(), conn,
		credentials{ClientID: "a", ClientSecret: "b"}, "code", "https://cb", "verifier")
	require.ErrorIs(t, err, ErrOIDCToken)
	assert.Contains(t, err.Error(), "openid")
}

func TestStandardProfileSendsTheTokenTheWayEachProviderAcceptsIt(t *testing.T) {
	for name, probe := range map[string]struct {
		conn   connector
		verify func(*testing.T, *http.Request)
	}{
		"bearer by default": {
			conn: socialConnectors["github"],
			verify: func(t *testing.T, request *http.Request) {
				assert.Equal(t, "Bearer at", request.Header.Get("Authorization"))
			},
		},
		"a provider-specific scheme": {
			conn: socialConnectors["yandex"],
			verify: func(t *testing.T, request *http.Request) {
				assert.Equal(t, "OAuth at", request.Header.Get("Authorization"))
			},
		},
		"a provider-specific header": {
			conn: socialConnectors["dingtalk"],
			verify: func(t *testing.T, request *http.Request) {
				assert.Equal(t, "at", request.Header.Get("x-acs-dingtalk-access-token"))
				assert.Empty(t, request.Header.Get("Authorization"))
			},
		},
		"the query string": {
			conn: socialConnectors["baidu"],
			verify: func(t *testing.T, request *http.Request) {
				assert.Equal(t, "at", request.URL.Query().Get("access_token"))
				assert.Empty(t, request.Header.Get("Authorization"))
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := newUpstreamRecorder(t, `{}`, `{"ok":true}`)
			conn := recorder.bind(probe.conn)

			_, err := standardProfile(context.Background(), recorder.upstream(), conn, upstreamToken{AccessToken: "at"})
			require.NoError(t, err)
			require.NotNil(t, recorder.profileReq)
			probe.verify(t, recorder.profileReq)
		})
	}
}

func TestStandardProfileSendsCarriedParameters(t *testing.T) {
	recorder := newUpstreamRecorder(t, `{}`, `{"id":"1"}`)
	conn := recorder.bind(socialConnectors["weibo"])

	_, err := standardProfile(context.Background(), recorder.upstream(), conn,
		upstreamToken{AccessToken: "at", Carried: map[string]string{"uid": "9"}})
	require.NoError(t, err)
	assert.Equal(t, "9", recorder.profileReq.URL.Query().Get("uid"))
}

func TestUpstreamHTTPIdentifiesTheDeploymentNotTheProduct(t *testing.T) {
	// Reddit refuses an unidentified client, and the identity is a deployment's
	// own: it is read from the runtime, never written into the catalog.
	recorder := newUpstreamRecorder(t, `{}`, `{"id":"1"}`)
	conn := recorder.bind(socialConnectors["reddit"])
	upstream := recorder.upstream()
	upstream.userAgent = "deployment-name/2"

	_, err := standardProfile(context.Background(), upstream, conn, upstreamToken{AccessToken: "at"})
	require.NoError(t, err)
	assert.Equal(t, "deployment-name/2", recorder.profileReq.Header.Get("User-Agent"))
}

func TestUpstreamUserAgentIsBuiltFromTheRuntimeIdentity(t *testing.T) {
	assert.Empty(t, upstreamUserAgent(nil), "no runtime means no claim about who is calling")
	assert.Equal(t, "console/3.1",
		upstreamUserAgent(appenv.NewRuntime(appenv.NewIdentity("console", "3.1"))))
	assert.Equal(t, "console",
		upstreamUserAgent(appenv.NewRuntime(appenv.NewIdentity("console", ""))),
		"a deployment with no version still names itself")
	assert.Empty(t, upstreamUserAgent(appenv.NewRuntime(appenv.NewIdentity("  ", "3.1"))))
}

func TestStandardProfileKeepsParametersTheEndpointAlreadyCarried(t *testing.T) {
	// Facebook's documented endpoint arrives with ?fields=..., which a naive
	// concatenation would overwrite or duplicate.
	merged := appendQuery("https://graph.facebook.com/v21.0/me?fields=id,name", url.Values{"access_token": {"at"}})

	parsed, err := url.Parse(merged)
	require.NoError(t, err)
	assert.Equal(t, "id,name", parsed.Query().Get("fields"))
	assert.Equal(t, "at", parsed.Query().Get("access_token"))
}

func TestStandardFlowProducesClaimsFromTwoRequests(t *testing.T) {
	recorder := newUpstreamRecorder(t,
		`{"access_token":"at"}`,
		`{"id":987,"email":"ada@example.com","name":"Ada Lovelace","login":"ada"}`)
	conn := recorder.bind(socialConnectors["github"])

	document, err := conn.profile(context.Background(), recorder.upstream(),
		credentials{ClientID: "cid", ClientSecret: "shh"}, "code", "https://cb", "verifier")
	require.NoError(t, err)

	claims, err := conn.claims(document)
	require.NoError(t, err)
	assert.Equal(t, "987", claims.Subject)
	assert.Equal(t, "ada@example.com", claims.Email)
	assert.Equal(t, "Ada Lovelace", claims.Name)
	assert.Equal(t, "ada", claims.PreferredUsername)
}

func TestUpstreamHTTPRefusesAnOversizedResponse(t *testing.T) {
	// An upstream host must not get to decide how much memory a login costs.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"padding":"` + strings.Repeat("x", maxUpstreamBody) + `"}`))
	}))
	t.Cleanup(server.Close)

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	_, err = (&upstreamHTTP{client: server.Client()}).Do(request, ErrConnectorProfile)
	require.ErrorIs(t, err, ErrConnectorProfile)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestUpstreamHTTPReportsANonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	_, err = (&upstreamHTTP{client: server.Client()}).Do(request, ErrConnectorProfile)
	require.ErrorIs(t, err, ErrConnectorProfile)
	assert.Contains(t, err.Error(), "403")
}

func TestLookupConnectorRejectsAnUnknownKey(t *testing.T) {
	_, err := lookupConnector("not-a-provider")
	require.ErrorIs(t, err, ErrUnknownConnector)

	conn, err := lookupConnector("  github  ")
	require.NoError(t, err, "a stored key is trimmed before lookup")
	assert.Equal(t, socialConnectors["github"].Profile, conn.Profile)
}

func TestConnectorCatalogIsStableAndDeclaresEmailSupport(t *testing.T) {
	catalog := ConnectorCatalog()
	require.Len(t, catalog, len(socialConnectors))
	assert.Equal(t, catalog, ConnectorCatalog(), "the catalog order is stable across calls")

	for index := 1; index < len(catalog); index++ {
		assert.Less(t, catalog[index-1].Key, catalog[index].Key, "the catalog is sorted by key")
	}

	byKey := map[string]ConnectorCatalogEntry{}
	for _, entry := range catalog {
		byKey[entry.Key] = entry
	}
	assert.True(t, byKey["github"].StatesEmail)
	assert.False(t, byKey["x"].StatesEmail, "a console must be able to warn that provisioning cannot apply")
	assert.Equal(t, socialConnectors["github"].Scopes, byKey["github"].DefaultScopes)
}

func TestErrorsStayDistinguishable(t *testing.T) {
	// federationError maps each of these to a different status, so they must not
	// collapse into one another.
	assert.NotErrorIs(t, ErrUnknownConnector, ErrConnectorProfile)
	assert.NotErrorIs(t, ErrNoUpstreamAuthTime, ErrOIDCState)
	assert.NotErrorIs(t, ErrConnectorProfile, ErrOIDCToken)
}

func TestClaimStringRendersTheLeafShapesAndRefusesTheRest(t *testing.T) {
	document := decodeFixture(t, `{"yes":true,"no":false,"object":{"a":1},"list":[1],"null":null}`)

	assert.Equal(t, "true", claimString(document, "yes"))
	assert.Equal(t, "false", claimString(document, "no"))
	// A container or a null is not a claim value; rendering one would put
	// "map[...]" or "<nil>" into an identity link.
	assert.Empty(t, claimString(document, "object"))
	assert.Empty(t, claimString(document, "list"))
	assert.Empty(t, claimString(document, "null"))
}

func TestConnectorDefaultsFillInTheStandardNames(t *testing.T) {
	// A connector that overrides nothing has to resolve to the RFC 6749 names,
	// because most rows rely on exactly that.
	plain := connector{}
	assert.Equal(t, "client_id", plain.clientIDParam())
	assert.Equal(t, "client_secret", plain.clientSecretParam())
	assert.Equal(t, "access_token", plain.tokenAccessPath())
	assert.Equal(t, "Bearer", plain.profileAuthScheme())

	overridden := connector{ClientIDParam: "appid", ClientSecretParam: "secret",
		TokenAccessPath: "accessToken", ProfileAuthScheme: "OAuth"}
	assert.Equal(t, "appid", overridden.clientIDParam())
	assert.Equal(t, "secret", overridden.clientSecretParam())
	assert.Equal(t, "accessToken", overridden.tokenAccessPath())
	assert.Equal(t, "OAuth", overridden.profileAuthScheme())
}

func TestAuthorizationURLRefusesAnUnparseableEndpoint(t *testing.T) {
	broken := connector{Authorize: "https://exa mple.com/\x7f"}

	_, err := broken.authorizationURL("cid", "https://cb", "scope", "state", "verifier")
	require.ErrorIs(t, err, ErrInvalidProvider)
}

func TestAppendQueryFallsBackWhenTheEndpointDoesNotParse(t *testing.T) {
	// The catalog is gated against this, but a bespoke flow composes URLs too,
	// and silently dropping the parameters would send an unauthenticated request.
	merged := appendQuery("://not-a-url", url.Values{"access_token": {"at"}})
	assert.Contains(t, merged, "access_token=at")

	merged = appendQuery("://not-a-url?existing=1", url.Values{"access_token": {"at"}})
	assert.Contains(t, merged, "existing=1")
	assert.Contains(t, merged, "&access_token=at", "an existing query is extended, not replaced")
}

func TestUpstreamHTTPReportsATransportFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := server.Client()
	address := server.URL
	server.Close()

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, address, nil)
	require.NoError(t, err)

	_, err = (&upstreamHTTP{client: client}).DoRaw(request, ErrConnectorProfile)
	require.ErrorIs(t, err, ErrConnectorProfile)
}

func TestStandardFlowPropagatesAFailedExchange(t *testing.T) {
	recorder := newUpstreamRecorder(t, `{"error":"invalid_grant"}`, `{}`)
	conn := recorder.bind(socialConnectors["github"])

	_, err := conn.profile(context.Background(), recorder.upstream(),
		credentials{ClientID: "cid"}, "code", "https://cb", "verifier")
	require.ErrorIs(t, err, ErrOIDCToken, "a flow must not read a profile after the exchange failed")
}
