package federation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wecomStub answers the three WeCom endpoints and records what reached each, so
// a test can show the application token never travels where it should not.
type wecomStub struct {
	server       *httptest.Server
	tokenReq     *http.Request
	identityReq  *http.Request
	detailReq    *http.Request
	detailBody   string
	tokenBody    string
	identityBody string
	detailAnswer string
}

func newWeComStub(t *testing.T, token, identity, detail string) *wecomStub {
	t.Helper()
	stub := &wecomStub{tokenBody: token, identityBody: identity, detailAnswer: detail}
	mux := http.NewServeMux()
	mux.HandleFunc("/cgi-bin/gettoken", func(writer http.ResponseWriter, request *http.Request) {
		stub.tokenReq = request.Clone(context.Background())
		_, _ = writer.Write([]byte(stub.tokenBody))
	})
	mux.HandleFunc("/cgi-bin/auth/getuserinfo", func(writer http.ResponseWriter, request *http.Request) {
		stub.identityReq = request.Clone(context.Background())
		_, _ = writer.Write([]byte(stub.identityBody))
	})
	mux.HandleFunc("/cgi-bin/auth/getuserdetail", func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		stub.detailBody = string(body)
		stub.detailReq = request.Clone(context.Background())
		_, _ = writer.Write([]byte(stub.detailAnswer))
	})
	stub.server = httptest.NewServer(mux)
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *wecomStub) run(t *testing.T) (map[string]any, error) {
	t.Helper()
	conn := socialConnectors["wecom"]
	conn.Flow = wecomFlow{
		tokenEndpoint:    s.server.URL + "/cgi-bin/gettoken",
		identityEndpoint: s.server.URL + "/cgi-bin/auth/getuserinfo",
		detailEndpoint:   s.server.URL + "/cgi-bin/auth/getuserdetail",
	}
	return conn.profile(context.Background(), &upstreamHTTP{client: s.server.Client()},
		credentials{ClientID: "the-corp", ClientSecret: "the-corp-secret"}, "the-code", "https://cb", "verifier")
}

func TestWeComFlowAuthenticatesTheApplicationThenThePerson(t *testing.T) {
	stub := newWeComStub(t,
		`{"errcode":0,"errmsg":"ok","access_token":"APP-TOKEN","expires_in":7200}`,
		`{"errcode":0,"errmsg":"ok","userid":"zhangsan"}`,
		`{}`)

	document, err := stub.run(t)
	require.NoError(t, err)

	conn := socialConnectors["wecom"]
	claims, err := conn.claims(document)
	require.NoError(t, err)
	assert.Equal(t, "zhangsan", claims.Subject)
	assert.Empty(t, claims.Email, "snsapi_base states no address")
	assert.Zero(t, claims.AuthTime)

	// The application proves itself with the corp credentials.
	require.NotNil(t, stub.tokenReq)
	assert.Equal(t, "the-corp", stub.tokenReq.URL.Query().Get("corpid"))
	assert.Equal(t, "the-corp-secret", stub.tokenReq.URL.Query().Get("corpsecret"))

	// The person is resolved with the application's token plus the code, and the
	// corp secret is never sent to that endpoint.
	require.NotNil(t, stub.identityReq)
	assert.Equal(t, "APP-TOKEN", stub.identityReq.URL.Query().Get("access_token"))
	assert.Equal(t, "the-code", stub.identityReq.URL.Query().Get("code"))
	assert.Empty(t, stub.identityReq.URL.Query().Get("corpsecret"))

	assert.Nil(t, stub.detailReq, "without a ticket there is nothing more to ask for")
}

func TestWeComFlowReadsADetailOnlyWhenATicketWasIssued(t *testing.T) {
	stub := newWeComStub(t,
		`{"errcode":0,"access_token":"APP-TOKEN"}`,
		`{"errcode":0,"userid":"zhangsan","user_ticket":"TICKET"}`,
		`{"errcode":0,"name":"张三","email":"zhangsan@example.com","avatar":"https://example/a.png"}`)

	document, err := stub.run(t)
	require.NoError(t, err)

	conn := socialConnectors["wecom"]
	claims, err := conn.claims(document)
	require.NoError(t, err)
	assert.Equal(t, "zhangsan", claims.Subject, "the identity step stays authoritative for the identifier")
	assert.Equal(t, "张三", claims.Name)
	assert.Equal(t, "zhangsan@example.com", claims.Email)

	require.NotNil(t, stub.detailReq)
	assert.Equal(t, "APP-TOKEN", stub.detailReq.URL.Query().Get("access_token"))
	var sent map[string]string
	require.NoError(t, json.Unmarshal([]byte(stub.detailBody), &sent))
	assert.Equal(t, "TICKET", sent["user_ticket"])
}

func TestWeComFlowRefusesSomeoneOutsideTheOrganization(t *testing.T) {
	// WeChat answers for an outside contact with an OpenId instead of a UserId.
	// Linking that identifier would bind an account to someone the directory
	// does not contain.
	stub := newWeComStub(t,
		`{"errcode":0,"access_token":"APP-TOKEN"}`,
		`{"errcode":0,"openid":"oGZUI0egBJY1zhBYw2KhdUfwVJJE"}`,
		`{}`)

	_, err := stub.run(t)
	require.ErrorIs(t, err, ErrWeComExternalUser)
}

func TestWeComFlowSurfacesARefusalReportedWithStatus200(t *testing.T) {
	// WeCom reports its own failures in the body, so a non-zero errcode has to
	// be read as an error or a failed call would look like an empty answer.
	stub := newWeComStub(t, `{"errcode":40001,"errmsg":"invalid credential"}`, `{}`, `{}`)
	_, err := stub.run(t)
	require.ErrorIs(t, err, ErrOIDCToken)
	assert.Contains(t, err.Error(), "40001")
	assert.Contains(t, err.Error(), "invalid credential")

	stub = newWeComStub(t, `{"errcode":0,"access_token":"APP-TOKEN"}`, `{"errcode":40029,"errmsg":"invalid code"}`, `{}`)
	_, err = stub.run(t)
	require.ErrorIs(t, err, ErrConnectorProfile)
	assert.Contains(t, err.Error(), "40029")
}

func TestWeComFlowRefusesAnAnswerThatIdentifiesNobody(t *testing.T) {
	stub := newWeComStub(t, `{"errcode":0,"access_token":"APP-TOKEN"}`, `{"errcode":0}`, `{}`)

	_, err := stub.run(t)
	require.ErrorIs(t, err, ErrConnectorProfile)
	assert.Contains(t, err.Error(), "identified nobody")
}

func TestWeComFlowRefusesWhenTheApplicationGetsNoToken(t *testing.T) {
	stub := newWeComStub(t, `{"errcode":0,"errmsg":"ok"}`, `{}`, `{}`)

	_, err := stub.run(t)
	require.ErrorIs(t, err, ErrOIDCToken)
	assert.Contains(t, err.Error(), "no application token")
	assert.Nil(t, stub.identityReq, "nobody is resolved without an application token")
}

func TestWeComFlowDefaultsToWeComsOwnEndpoints(t *testing.T) {
	// The catalog registers wecomFlow{}, so the zero value has to resolve to the
	// three real addresses.
	token, identity, detail := wecomFlow{}.endpoints()
	assert.Equal(t, wecomTokenEndpoint, token)
	assert.Equal(t, wecomIdentityEndpoint, identity)
	assert.Equal(t, wecomDetailEndpoint, detail)

	token, identity, detail = wecomFlow{
		tokenEndpoint: "https://a", identityEndpoint: "https://b", detailEndpoint: "https://c",
	}.endpoints()
	assert.Equal(t, "https://a", token)
	assert.Equal(t, "https://b", identity)
	assert.Equal(t, "https://c", detail)

	registered, err := lookupConnector("wecom")
	require.NoError(t, err)
	assert.Equal(t, wecomFlow{}, registered.Flow,
		"the catalog holds the zero value, so the defaults are what production uses")
}

func TestWeComFlowRefusesSomeoneOutsideUnderEitherSpelling(t *testing.T) {
	// WeCom's own documentation shows OpenId capitalised; its JSON has been seen
	// both ways, and admitting one spelling would leave the other unguarded.
	for _, body := range []string{
		`{"errcode":0,"OpenId":"oGZUI0e"}`,
		`{"errcode":0,"openid":"oGZUI0e"}`,
	} {
		stub := newWeComStub(t, `{"errcode":0,"access_token":"APP-TOKEN"}`, body, `{}`)
		_, err := stub.run(t)
		require.ErrorIs(t, err, ErrWeComExternalUser, "body %s", body)
	}
}

func TestWeComFlowAcceptsEitherSpellingOfTheIdentifier(t *testing.T) {
	// WeCom's documentation capitalises UserId. Asserting on the raw document
	// would pass while the connector's claim path found nothing, so this goes
	// through the mapping the login actually uses.
	conn := socialConnectors["wecom"]
	for _, body := range []string{
		`{"errcode":0,"UserId":"zhangsan"}`,
		`{"errcode":0,"userid":"zhangsan"}`,
	} {
		stub := newWeComStub(t, `{"errcode":0,"access_token":"APP-TOKEN"}`, body, `{}`)
		document, err := stub.run(t)
		require.NoError(t, err, "body %s", body)

		claims, err := conn.claims(document)
		require.NoError(t, err, "body %s", body)
		assert.Equal(t, "zhangsan", claims.Subject, "body %s", body)
	}
}

func TestWeComFlowReportsAFailedDetailRead(t *testing.T) {
	stub := newWeComStub(t,
		`{"errcode":0,"access_token":"APP-TOKEN"}`,
		`{"errcode":0,"userid":"zhangsan","user_ticket":"TICKET"}`,
		`{"errcode":60011,"errmsg":"no privilege"}`)

	_, err := stub.run(t)
	require.ErrorIs(t, err, ErrConnectorProfile)
	assert.Contains(t, err.Error(), "60011")
}
