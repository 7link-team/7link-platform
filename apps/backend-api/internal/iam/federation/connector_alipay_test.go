package federation

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// alipayTestKey returns a signing key and the PKCS#8 PEM an operator would paste
// into the provider's client secret.
func alipayTestKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// alipayGatewayStub answers the two gateway operations and records what was
// signed, so a test can verify the signature the way Alipay would.
type alipayGatewayStub struct {
	server   *httptest.Server
	requests []url.Values
	token    string
	profile  string
}

func newAlipayGateway(t *testing.T, token, profile string) *alipayGatewayStub {
	t.Helper()
	stub := &alipayGatewayStub{token: token, profile: profile}
	stub.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("gateway received an unparseable body: %v", err)
			return
		}
		stub.requests = append(stub.requests, form)
		if form.Get("method") == alipayTokenMethod {
			_, _ = writer.Write([]byte(stub.token))
			return
		}
		_, _ = writer.Write([]byte(stub.profile))
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *alipayGatewayStub) run(t *testing.T, secret string) (map[string]any, error) {
	t.Helper()
	conn := socialConnectors["alipay"]
	conn.Flow = alipayFlow{gateway: s.server.URL}
	return conn.profile(context.Background(), &upstreamHTTP{client: s.server.Client()},
		credentials{ClientID: "2021000000000000", ClientSecret: secret}, "the-code", "https://cb", "verifier")
}

func TestAlipayFlowSignsEveryCallWithTheApplicationKey(t *testing.T) {
	key, pemKey := alipayTestKey(t)
	stub := newAlipayGateway(t,
		`{"alipay_system_oauth_token_response":{"code":"10000","access_token":"AT","user_id":"2088111122223333"},"sign":"x"}`,
		`{"alipay_user_info_share_response":{"code":"10000","nick_name":"张三","user_id":"2088111122223333"},"sign":"x"}`)

	document, err := stub.run(t, pemKey)
	require.NoError(t, err)

	conn := socialConnectors["alipay"]
	claims, err := conn.claims(document)
	require.NoError(t, err)
	assert.Equal(t, "2088111122223333", claims.Subject)
	assert.Equal(t, "张三", claims.Name)
	assert.Empty(t, claims.Email, "Alipay states no address")
	assert.Zero(t, claims.AuthTime)

	require.Len(t, stub.requests, 2, "a token call and a profile call")
	for index, form := range stub.requests {
		// Verify the signature the way Alipay does: rebuild the content from
		// everything that was sent, and check it against the public key. A test
		// that only asserted a signature was present would pass on a wrong one.
		signature, err := base64.StdEncoding.DecodeString(form.Get("sign"))
		require.NoError(t, err, "call %d", index)
		digest := sha256.Sum256([]byte(alipaySigningString(form)))
		require.NoError(t, rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature),
			"call %d carries a signature Alipay would reject", index)

		assert.Equal(t, "RSA2", form.Get("sign_type"))
		assert.Equal(t, "utf-8", form.Get("charset"))
		assert.Equal(t, "1.0", form.Get("version"))
		assert.Equal(t, "2021000000000000", form.Get("app_id"))
		assert.NotEmpty(t, form.Get("timestamp"))
		assert.NotContains(t, form.Encode(), "BEGIN PRIVATE KEY", "the signing key never leaves the deployment")
	}
	assert.Equal(t, "authorization_code", stub.requests[0].Get("grant_type"))
	assert.Equal(t, "the-code", stub.requests[0].Get("code"))
	assert.Equal(t, "AT", stub.requests[1].Get("auth_token"), "the profile call presents the token it was issued")
}

func TestAlipaySigningStringFollowsTheDocumentedRules(t *testing.T) {
	// Sorted by name, the signature itself excluded, empty values excluded.
	// Getting any of these wrong produces a signature Alipay rejects with a
	// message that does not say which rule was broken.
	content := alipaySigningString(url.Values{
		"method":  {"alipay.system.oauth.token"},
		"app_id":  {"2021"},
		"sign":    {"must-not-appear"},
		"empty":   {""},
		"charset": {"utf-8"},
	})
	assert.Equal(t, "app_id=2021&charset=utf-8&method=alipay.system.oauth.token", content)
}

func TestAlipayFlowReportsAGatewayRefusal(t *testing.T) {
	_, pemKey := alipayTestKey(t)

	// A refusal beside the envelope.
	stub := newAlipayGateway(t,
		`{"error_response":{"code":"40002","sub_code":"isv.invalid-app-id","msg":"Invalid Arguments","sub_msg":"无效的AppID"},"sign":"x"}`, `{}`)
	_, err := stub.run(t, pemKey)
	require.ErrorIs(t, err, ErrOIDCToken)
	assert.Contains(t, err.Error(), "isv.invalid-app-id")
	assert.Contains(t, err.Error(), "无效的AppID")

	// A refusal inside it, which arrives with HTTP 200 and would otherwise read
	// as an empty profile.
	stub = newAlipayGateway(t,
		`{"alipay_system_oauth_token_response":{"code":"20000","msg":"Service Currently Unavailable"},"sign":"x"}`, `{}`)
	_, err = stub.run(t, pemKey)
	require.ErrorIs(t, err, ErrOIDCToken)
	assert.Contains(t, err.Error(), "20000")
}

func TestAlipayFlowRefusesAnUnusableSigningKey(t *testing.T) {
	stub := newAlipayGateway(t, `{}`, `{}`)

	_, err := stub.run(t, "")
	require.ErrorIs(t, err, ErrAlipaySigningKey, "an empty secret cannot sign anything")

	_, err = stub.run(t, "not-a-key")
	require.ErrorIs(t, err, ErrAlipaySigningKey)

	assert.Empty(t, stub.requests, "nothing is sent to the gateway without a usable key")
}

func TestAlipayPrivateKeyAcceptsWhatAnOperatorPastes(t *testing.T) {
	key, pemKey := alipayTestKey(t)

	parsed, err := alipayPrivateKey(pemKey)
	require.NoError(t, err)
	assert.Equal(t, key.N, parsed.N)

	// Alipay's console shows the body without the PEM armour, and an operator
	// pastes exactly that.
	block, _ := pem.Decode([]byte(pemKey))
	require.NotNil(t, block)
	bare := base64.StdEncoding.EncodeToString(block.Bytes)
	parsed, err = alipayPrivateKey(bare)
	require.NoError(t, err)
	assert.Equal(t, key.N, parsed.N)

	// A key exported in the older format still works rather than sending an
	// operator looking for a problem in their console.
	legacy := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	parsed, err = alipayPrivateKey(string(legacy))
	require.NoError(t, err)
	assert.Equal(t, key.N, parsed.N)
}

func TestAlipayFlowCarriesTheIdentifierFromTheTokenCall(t *testing.T) {
	// alipay.user.info.share may answer without the identifier; the token call
	// always states it, and losing it would make the login anonymous.
	_, pemKey := alipayTestKey(t)
	stub := newAlipayGateway(t,
		`{"alipay_system_oauth_token_response":{"code":"10000","access_token":"AT","user_id":"2088999"},"sign":"x"}`,
		`{"alipay_user_info_share_response":{"code":"10000","nick_name":"李四"},"sign":"x"}`)

	document, err := stub.run(t, pemKey)
	require.NoError(t, err)
	assert.Equal(t, "2088999", claimString(document, "user_id"))
}

func TestAlipayFlowDefaultsToAlipaysOwnGateway(t *testing.T) {
	// The catalog registers alipayFlow{}, so the zero value has to resolve to
	// the real gateway; an empty endpoint would be posted to nothing.
	assert.Equal(t, alipayGateway, alipayFlow{}.endpoint())
	assert.Equal(t, "https://local/gw", alipayFlow{gateway: "https://local/gw"}.endpoint())

	registered, err := lookupConnector("alipay")
	require.NoError(t, err)
	assert.Equal(t, alipayFlow{}, registered.Flow,
		"the catalog holds the zero value, so the default is what production uses")
}

func TestAlipayUnwrapRefusesAnAnswerWithoutItsEnvelope(t *testing.T) {
	// A gateway answer that carries neither the envelope nor an error is not
	// something to read fields out of.
	_, err := alipayUnwrap(map[string]any{"unexpected": "shape"}, alipayTokenMethod, ErrOIDCToken)
	require.ErrorIs(t, err, ErrOIDCToken)
	assert.Contains(t, err.Error(), "alipay_system_oauth_token_response")
}

func TestAlipayFlowRefusesATokenAnswerWithoutAToken(t *testing.T) {
	_, pemKey := alipayTestKey(t)
	stub := newAlipayGateway(t,
		`{"alipay_system_oauth_token_response":{"code":"10000","user_id":"2088"},"sign":"x"}`, `{}`)

	_, err := stub.run(t, pemKey)
	require.ErrorIs(t, err, ErrOIDCToken)
	assert.Contains(t, err.Error(), "no access token")
	assert.Len(t, stub.requests, 1, "the profile call is not attempted without a token")
}
