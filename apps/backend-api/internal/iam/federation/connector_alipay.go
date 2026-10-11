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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// alipayFlow implements Alipay, which does not speak OAuth 2.0 at its exchange
// at all: every call goes to one gateway, carries the operation in a `method`
// parameter, and must be signed with the application's RSA private key. The
// answer arrives wrapped in an envelope named after the method.
//
// So the tenant's "client secret" here is a PKCS#8 private key rather than a
// shared secret. Nothing else in the catalog needs that, which is why this is a
// flow instead of more columns.
type alipayFlow struct {
	// gateway defaults to Alipay's own. It is a field so a test can point one
	// instance at a local server without touching the registered connector.
	gateway string
}

const (
	alipayGateway       = "https://openapi.alipay.com/gateway.do"
	alipayTokenMethod   = "alipay.system.oauth.token"
	alipayProfileMethod = "alipay.user.info.share"
	// alipayTimestamp is the layout Alipay requires, in its own time zone.
	alipayTimestamp = "2006-01-02 15:04:05"
)

// ErrAlipaySigningKey reports a client secret that is not a usable private key.
var ErrAlipaySigningKey = errors.New("alipay requires a PKCS#8 RSA private key as the client secret")

func (f alipayFlow) endpoint() string {
	if f.gateway != "" {
		return f.gateway
	}
	return alipayGateway
}

func (f alipayFlow) Profile(ctx context.Context, upstream *upstreamHTTP, ex exchange) (map[string]any, error) {
	key, err := alipayPrivateKey(ex.cred.ClientSecret)
	if err != nil {
		return nil, err
	}
	token, err := f.call(ctx, upstream, ex.cred.ClientID, key, ErrOIDCToken, url.Values{
		"method":     {alipayTokenMethod},
		"grant_type": {"authorization_code"},
		"code":       {ex.code},
	})
	if err != nil {
		return nil, err
	}
	accessToken := claimString(token, "access_token")
	if accessToken == "" {
		return nil, fmt.Errorf("%w: gateway returned no access token", ErrOIDCToken)
	}
	profile, err := f.call(ctx, upstream, ex.cred.ClientID, key, ErrConnectorProfile, url.Values{
		"method":     {alipayProfileMethod},
		"auth_token": {accessToken},
	})
	if err != nil {
		return nil, err
	}
	// The identifier is in the token response as well, and is the one the
	// connector's Subject path names. Carrying it over keeps a profile that
	// omits it from reading as an anonymous login.
	if profile["user_id"] == nil {
		if userID := claimString(token, "user_id"); userID != "" {
			profile["user_id"] = userID
		}
	}
	return profile, nil
}

// call signs and performs one gateway operation and unwraps its envelope.
func (f alipayFlow) call(ctx context.Context, upstream *upstreamHTTP, appID string, key *rsa.PrivateKey, sentinel error, params url.Values) (map[string]any, error) {
	params.Set("app_id", appID)
	params.Set("charset", "utf-8")
	params.Set("sign_type", "RSA2")
	params.Set("version", "1.0")
	params.Set("timestamp", time.Now().Format(alipayTimestamp))
	signature, err := alipaySign(alipaySigningString(params), key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", sentinel, err)
	}
	params.Set("sign", signature)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint(), strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: build gateway request: %w", sentinel, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	document, err := upstream.Do(request, sentinel)
	if err != nil {
		return nil, err
	}
	return alipayUnwrap(document, params.Get("method"), sentinel)
}

// alipaySigningString builds the content Alipay signs: every parameter except
// the signature itself and anything empty, sorted by name and joined as
// name=value pairs. See https://opendocs.alipay.com/common/02kf5q.
func alipaySigningString(params url.Values) string {
	names := make([]string, 0, len(params))
	for name := range params {
		if name == "sign" || params.Get(name) == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, name+"="+params.Get(name))
	}
	return strings.Join(pairs, "&")
}

// alipaySign signs the content with RSA-SHA256, which is what Alipay calls RSA2.
func alipaySign(content string, key *rsa.PrivateKey) (string, error) {
	digest := sha256.Sum256([]byte(content))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign gateway request: %w", err)
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

// alipayPrivateKey reads the tenant's signing key. A bare base64 body is
// accepted as well as a PEM document, because an operator pasting a key out of
// Alipay's console gets the body without the armour.
func alipayPrivateKey(secret string) (*rsa.PrivateKey, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, ErrAlipaySigningKey
	}
	var der []byte
	if block, _ := pem.Decode([]byte(secret)); block != nil {
		der = block.Bytes
	} else {
		decoded, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(secret), ""))
		if err != nil {
			return nil, fmt.Errorf("%w: not PEM and not base64", ErrAlipaySigningKey)
		}
		der = decoded
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		// A key exported in the older format is still usable; refusing it would
		// send an operator looking for a problem in their console.
		if legacy, legacyErr := x509.ParsePKCS1PrivateKey(der); legacyErr == nil {
			return legacy, nil
		}
		return nil, fmt.Errorf("%w: %w", ErrAlipaySigningKey, err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%w: key is %T", ErrAlipaySigningKey, parsed)
	}
	return key, nil
}

// alipayUnwrap takes the payload out of the envelope named after the method, and
// turns a gateway-level refusal into an error. Alipay answers HTTP 200 for
// business failures, so a caller that only checked the status would read a
// refusal as an empty profile.
func alipayUnwrap(document map[string]any, method string, sentinel error) (map[string]any, error) {
	if failure, ok := document["error_response"].(map[string]any); ok {
		return nil, alipayError(failure, sentinel)
	}
	envelope := strings.ReplaceAll(method, ".", "_") + "_response"
	payload, ok := document[envelope].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: gateway answered no %s", sentinel, envelope)
	}
	// 10000 is Alipay's success code; anything else is a refusal reported inside
	// the envelope rather than beside it.
	if code := claimString(payload, "code"); code != "" && code != "10000" {
		return nil, alipayError(payload, sentinel)
	}
	return payload, nil
}

func alipayError(payload map[string]any, sentinel error) error {
	message := claimString(payload, "sub_msg")
	if message == "" {
		message = claimString(payload, "msg")
	}
	return fmt.Errorf("%w: alipay %s/%s: %s",
		sentinel, claimString(payload, "code"), claimString(payload, "sub_code"), message)
}
