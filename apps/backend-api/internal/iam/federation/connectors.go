package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Social sign-in is, for most providers, one protocol: redirect for an
// authorization code, exchange it, read a profile document. What separates
// GitHub from Discord from Feishu is a few URLs, a few parameter names and the
// JSON field names — not the protocol. So a connector is a table row, and the
// table is the only place a provider is declared.
//
// A handful of providers cannot be described that way. QQ answers its token
// endpoint in form encoding and its identity endpoint in JSONP, and needs a
// third request; Alipay signs a request envelope; WeCom authenticates the
// application separately from the person. Those get a profileFlow of their own
// rather than another column that only one row ever sets. The split is the
// design: data for what varies by value, code for what varies by shape.
//
// The table is compiled in deliberately. A tenant selects a connector by key,
// so the hosts a login reaches are never tenant-controlled: an administrator
// cannot repoint a provider's profile endpoint at something they own while
// keeping the client ID that existing identity links trust.

var (
	ErrUnknownConnector = errors.New("unknown social connector")
	ErrConnectorProfile = errors.New("social profile request failed")
)

// connector declares one social provider. Zero values mean "the standard
// thing", so a provider that follows RFC 6749 and answers JSON needs only its
// endpoints, its scopes and its claim paths.
type connector struct {
	// Authorize, Token and Profile are the three upstream endpoints. Profile is
	// unused when Flow supplies its own.
	Authorize string
	Token     string
	Profile   string

	// Scopes is the default scope string offered when a tenant registers the
	// provider without naming its own.
	Scopes string

	// ClientIDParam and ClientSecretParam name the credential parameters.
	// Empty means client_id and client_secret. WeChat wants appid and secret,
	// Douyin wants client_key, DingTalk wants clientId.
	ClientIDParam     string
	ClientSecretParam string

	// Exchange shapes the token request. nil means the RFC 6749 form post with
	// the secret in an Authorization header. TokenAccessPath names the field
	// holding the access token when it is not access_token.
	Exchange        tokenExchange
	TokenAccessPath string

	// ProfileAuth is how the access token reaches the profile endpoint. nil
	// means an Authorization header with the Bearer scheme. Each variant
	// carries whatever that way needs, so a connector cannot name a header
	// while asking for a query parameter.
	ProfileAuth profileAuth

	// Headers are sent on every profile request. Coinbase requires its API
	// version here. The deployment's own identity is not set from a connector:
	// upstreamHTTP sends it on every request, so one build serves any
	// deployment.
	Headers map[string]string

	// Carry maps a profile-request parameter to the token-response path that
	// supplies it. WeChat returns the openid only in the token response, Weibo
	// the uid, and Douyin nests its openid under a different name than the
	// parameter it then wants back.
	Carry map[string]string

	// Claim paths into the profile document, dotted for nested payloads so a
	// provider that wraps everything in "data" needs no code. Subject is
	// required; a provider that states no address leaves Email empty, and
	// just-in-time provisioning then refuses rather than invent one.
	Subject       string
	Email         string
	EmailVerified string
	Name          string
	Username      string

	// Flow replaces the standard exchange for a provider whose shape the fields
	// above cannot express. It still returns a document, so claim mapping,
	// provisioning, linking and auditing stay identical for every provider.
	Flow profileFlow
}

// tokenExchange builds the token request. The variants carry their own data,
// which is what makes an impossible combination impossible to write: only the
// form shape leaves a choice about where the secret goes, so only formPost has
// a field for it.
type tokenExchange interface {
	// request builds the token request from the shared parameters. It is handed
	// the connector for its parameter names and the credentials to prove the
	// client with, and must not retain either.
	request(ctx context.Context, conn connector, cred credentials, params url.Values) (*http.Request, error)
}

// formPost is the RFC 6749 form body. The secret travels in an Authorization
// header unless the provider refuses that, which Weibo, Douyin, Discord, Kakao,
// Coinbase and Instagram all do.
type formPost struct{ secretInBody bool }

func (f formPost) request(ctx context.Context, conn connector, cred credentials, params url.Values) (*http.Request, error) {
	params.Set(conn.clientIDParam(), cred.ClientID)
	basic := false
	if cred.ClientSecret != "" {
		if f.secretInBody {
			params.Set(conn.clientSecretParam(), cred.ClientSecret)
		} else {
			basic = true
		}
	}
	request, err := newTokenRequest(ctx, http.MethodPost, conn.Token, params.Encode(), "application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	if basic {
		request.SetBasicAuth(url.QueryEscape(cred.ClientID), url.QueryEscape(cred.ClientSecret))
	}
	return request, nil
}

// queryGet puts every parameter in the query string and issues a GET, which is
// what api.weixin.qq.com, openapi.baidu.com and graph.facebook.com document.
// It is a GET because there is no body to send: posting an empty one is what
// makes a provider answer "invalid request" for no visible reason.
type queryGet struct{}

func (queryGet) request(ctx context.Context, conn connector, cred credentials, params url.Values) (*http.Request, error) {
	params.Set(conn.clientIDParam(), cred.ClientID)
	params.Set(conn.clientSecretParam(), cred.ClientSecret)
	return newTokenRequest(ctx, http.MethodGet, appendQuery(conn.Token, params), "", "")
}

// jsonPost posts a JSON object with camelCase keys, which is what DingTalk's
// v1.0 endpoint expects.
type jsonPost struct{}

func (jsonPost) request(ctx context.Context, conn connector, cred credentials, params url.Values) (*http.Request, error) {
	object := map[string]string{
		"grantType": "authorization_code", "code": params.Get("code"),
		conn.clientIDParam(): cred.ClientID, conn.clientSecretParam(): cred.ClientSecret,
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("%w: encode token request: %w", ErrOIDCToken, err)
	}
	return newTokenRequest(ctx, http.MethodPost, conn.Token, string(encoded), "application/json")
}

func newTokenRequest(ctx context.Context, method, endpoint, body, contentType string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: build token request: %w", ErrOIDCToken, err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return request, nil
}

// profileAuth carries how the access token reaches the profile endpoint, along
// with whatever that way needs.
type profileAuth interface {
	// authorize returns the endpoint to request and the headers that carry the
	// token, so a variant that moves the token into the URL can do so before
	// the request exists.
	authorize(endpoint string, token upstreamToken) (string, http.Header)
}

// bearerToken sends an Authorization header. The scheme is Bearer unless the
// provider refuses it, which Yandex does.
type bearerToken struct{ scheme string }

func (b bearerToken) authorize(endpoint string, token upstreamToken) (string, http.Header) {
	scheme := b.scheme
	if scheme == "" {
		scheme = "Bearer"
	}
	return endpoint, http.Header{"Authorization": []string{scheme + " " + token.AccessToken}}
}

// tokenHeader sends the bare token in a header of the provider's own naming,
// which is how DingTalk accepts it.
type tokenHeader struct{ name string }

func (h tokenHeader) authorize(endpoint string, token upstreamToken) (string, http.Header) {
	return endpoint, http.Header{h.name: []string{token.AccessToken}}
}

// tokenQuery sends the token, and anything carried with it, as query parameters.
type tokenQuery struct{}

func (tokenQuery) authorize(endpoint string, token upstreamToken) (string, http.Header) {
	params := url.Values{}
	params.Set("access_token", token.AccessToken)
	for key, value := range token.Carried {
		params.Set(key, value)
	}
	return appendQuery(endpoint, params), nil
}

// profileFlow turns an authorization code into a profile document. The standard
// implementation is standardFlow; a provider supplies its own only when its
// shape cannot be expressed in connector's fields.
//
// Implementations receive the credentials already decrypted and the connector
// they belong to, and must not retain either.
type profileFlow interface {
	// Profile exchanges the code and returns the provider's profile document.
	// The returned map is read through the connector's claim paths.
	Profile(ctx context.Context, upstream *upstreamHTTP, ex exchange) (map[string]any, error)
}

// exchange is everything a flow needs to turn an authorization code into a
// profile: the connector it belongs to, the tenant's credentials, and the values
// the browser round trip produced.
type exchange struct {
	conn         connector
	cred         credentials
	code         string
	callback     string
	codeVerifier string
}

// params are the parameters every token request carries, whatever its shape.
func (ex exchange) params() url.Values {
	params := url.Values{}
	params.Set("grant_type", "authorization_code")
	params.Set("code", ex.code)
	params.Set("redirect_uri", ex.callback)
	params.Set("code_verifier", ex.codeVerifier)
	return params
}

// credentials is the tenant's registered client identity for one provider.
type credentials struct {
	ClientID     string
	ClientSecret string
}

// upstreamHTTP performs requests against a provider under the deployment's
// outbound policy and decodes their JSON. It exists so a bespoke flow gets the
// policed client and the exact-number decoding without reaching for its own.
type upstreamHTTP struct {
	client *http.Client
	// userAgent identifies this deployment to the provider. It is read from the
	// runtime identity rather than written here, because the name of the
	// software is a deployment's own and Reddit refuses an unidentified client.
	userAgent string
}

// maxUpstreamBody bounds what one provider response may cost us. A profile
// document is a few hundred bytes; anything approaching this is either broken
// or hostile, and reading it unbounded would let an upstream host decide how
// much memory a login takes.
const maxUpstreamBody = 1 << 20

// DoRaw performs the request and returns the response body. It exists for the
// providers that answer something other than JSON, which a bespoke flow then
// parses itself.
func (u *upstreamHTTP) DoRaw(request *http.Request, sentinel error) ([]byte, error) {
	request.Header.Set("Accept", "application/json")
	if u.userAgent != "" {
		request.Header.Set("User-Agent", u.userAgent)
	}
	response, err := u.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", sentinel, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%w: status %d", sentinel, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxUpstreamBody+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %w", sentinel, err)
	}
	if len(body) > maxUpstreamBody {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", sentinel, maxUpstreamBody)
	}
	return body, nil
}

// Do performs the request and decodes a JSON object, keeping numbers exact so
// an identifier wider than a float64 mantissa survives the round trip.
func (u *upstreamHTTP) Do(request *http.Request, sentinel error) (map[string]any, error) {
	body, err := u.DoRaw(request, sentinel)
	if err != nil {
		return nil, err
	}
	return decodeJSONObject(body, sentinel)
}

// decodeJSONObject decodes one JSON object with exact numbers.
func decodeJSONObject(body []byte, sentinel error) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%w: decode response: %w", sentinel, err)
	}
	return document, nil
}

// lookupConnector resolves the key stored in a provider's issuer column.
func lookupConnector(key string) (connector, error) {
	conn, ok := socialConnectors[strings.TrimSpace(key)]
	if !ok {
		return connector{}, fmt.Errorf("%w: %q", ErrUnknownConnector, key)
	}
	return conn, nil
}

// ConnectorCatalogEntry is one row of the catalog a console offers when an
// administrator registers a social provider. It carries no display label:
// naming belongs to the console, which is where the locale lives.
type ConnectorCatalogEntry struct {
	Key           string `json:"key"`
	DefaultScopes string `json:"default_scopes"`
	StatesEmail   bool   `json:"states_email" doc:"False when the provider returns no email address, so just-in-time provisioning cannot apply and the connector can only link an existing account"`
}

// ConnectorCatalog lists the compiled-in social connectors in a stable order.
func ConnectorCatalog() []ConnectorCatalogEntry {
	entries := make([]ConnectorCatalogEntry, 0, len(socialConnectors))
	for key, conn := range socialConnectors {
		entries = append(entries, ConnectorCatalogEntry{Key: key, DefaultScopes: conn.Scopes, StatesEmail: conn.Email != ""})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	return entries
}

// authorizationURL builds the redirect that starts a social login. PKCE is
// always sent, so the code stays bound to this browser flow even for a provider
// that would have accepted it without.
func (c connector) authorizationURL(clientID, callback, scopes, state, codeVerifier string) (string, error) {
	endpoint, err := url.Parse(c.Authorize)
	if err != nil {
		return "", fmt.Errorf("%w: authorization endpoint", ErrInvalidProvider)
	}
	query := endpoint.Query()
	for key, value := range map[string]string{
		"response_type": "code", c.clientIDParam(): clientID, "redirect_uri": callback,
		"scope": scopes, "state": state,
		"code_challenge": codeChallenge(codeVerifier), "code_challenge_method": "S256",
	} {
		query.Set(key, value)
	}
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (c connector) clientIDParam() string {
	if c.ClientIDParam != "" {
		return c.ClientIDParam
	}
	return "client_id"
}

func (c connector) clientSecretParam() string {
	if c.ClientSecretParam != "" {
		return c.ClientSecretParam
	}
	return "client_secret"
}

// exchangeShape resolves the token request shape, defaulting to the standard
// form post with the secret in an Authorization header.
func (c connector) exchangeShape() tokenExchange {
	if c.Exchange == nil {
		return formPost{}
	}
	return c.Exchange
}

// profileAuthOf resolves how the token reaches the profile endpoint, defaulting
// to a Bearer Authorization header.
func (c connector) profileAuthOf() profileAuth {
	if c.ProfileAuth == nil {
		return bearerToken{}
	}
	return c.ProfileAuth
}

func (c connector) tokenAccessPath() string {
	if c.TokenAccessPath != "" {
		return c.TokenAccessPath
	}
	return "access_token"
}

// profile resolves the connector's flow and runs it.
func (c connector) profile(ctx context.Context, upstream *upstreamHTTP, cred credentials, code, callback, codeVerifier string) (map[string]any, error) {
	flow := c.Flow
	if flow == nil {
		flow = standardFlow{}
	}
	return flow.Profile(ctx, upstream, exchange{
		conn: c, cred: cred, code: code, callback: callback, codeVerifier: codeVerifier,
	})
}

// standardFlow is the authorization-code exchange followed by one profile read.
// Every provider that follows OAuth 2.0 closely enough uses it.
type standardFlow struct{}

func (standardFlow) Profile(ctx context.Context, upstream *upstreamHTTP, ex exchange) (map[string]any, error) {
	token, err := standardExchange(ctx, upstream, ex)
	if err != nil {
		return nil, err
	}
	return standardProfile(ctx, upstream, ex.conn, token)
}

// upstreamToken is what an exchange yields: the access token plus the
// provider-specific fields a profile request has to carry forward.
type upstreamToken struct {
	AccessToken string
	Carried     map[string]string
}

// standardExchange redeems the authorization code. The secret travels in the
// Authorization header whenever the provider accepts it there, so it stays out
// of bodies and query strings that intermediaries log.
func standardExchange(ctx context.Context, upstream *upstreamHTTP, ex exchange) (upstreamToken, error) {
	request, err := ex.conn.exchangeShape().request(ctx, ex.conn, ex.cred, ex.params())
	if err != nil {
		return upstreamToken{}, err
	}
	document, err := upstream.Do(request, ErrOIDCToken)
	if err != nil {
		return upstreamToken{}, err
	}
	accessToken := claimString(document, ex.conn.tokenAccessPath())
	if accessToken == "" {
		// Providers disagree on where the reason goes; naming both keeps the
		// operator's message useful without parsing each provider's envelope.
		reason := claimString(document, "error_description")
		if reason == "" {
			reason = claimString(document, "error")
		}
		return upstreamToken{}, fmt.Errorf("%w: %s", ErrOIDCToken, reason)
	}
	token := upstreamToken{AccessToken: accessToken}
	if len(ex.conn.Carry) > 0 {
		token.Carried = make(map[string]string, len(ex.conn.Carry))
		for parameter, path := range ex.conn.Carry {
			value := claimString(document, path)
			if value == "" {
				return upstreamToken{}, fmt.Errorf("%w: token response is missing %s", ErrOIDCToken, path)
			}
			token.Carried[parameter] = value
		}
	}
	return token, nil
}

// standardProfile reads the provider's profile document.
func standardProfile(ctx context.Context, upstream *upstreamHTTP, conn connector, token upstreamToken) (map[string]any, error) {
	endpoint, headers := conn.profileAuthOf().authorize(conn.Profile, token)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build profile request: %w", ErrConnectorProfile, err)
	}
	for key, values := range headers {
		request.Header[key] = values
	}
	for key, value := range conn.Headers {
		request.Header.Set(key, value)
	}
	return upstream.Do(request, ErrConnectorProfile)
}

// appendQuery merges parameters into a URL that may already carry some, which
// several providers' documented profile endpoints do.
func appendQuery(endpoint string, params url.Values) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		separator := "?"
		if strings.Contains(endpoint, "?") {
			separator = "&"
		}
		return endpoint + separator + params.Encode()
	}
	existing := parsed.Query()
	for key, values := range params {
		existing[key] = values
	}
	parsed.RawQuery = existing.Encode()
	return parsed.String()
}

// claims maps a profile document onto the shape the shared authentication path
// already consumes, which is what lets a social login reuse provisioning,
// linking, auditing and session issuance unchanged.
//
// AuthTime is deliberately left zero. Plain OAuth 2.0 states nothing about when
// the person authenticated upstream, and the flows that need that proof reject
// a zero value rather than accept a fabricated one.
func (c connector) claims(document map[string]any) (idTokenClaims, error) {
	subject := claimString(document, c.Subject)
	if subject == "" || len(subject) > 128 {
		return idTokenClaims{}, fmt.Errorf("%w: profile is missing a usable subject", ErrConnectorProfile)
	}
	claims := idTokenClaims{
		Subject:           subject,
		Email:             claimString(document, c.Email),
		Name:              claimString(document, c.Name),
		PreferredUsername: claimString(document, c.Username),
	}
	if len(claims.Email) > 255 || len(claims.Name) > 128 || len(claims.PreferredUsername) > 128 {
		// The columns behind these are bounded, and a provider is free to
		// return more than fits. Refusing beats storing a silent truncation
		// that later compares unequal to the same person's real name.
		return idTokenClaims{}, fmt.Errorf("%w: profile field exceeds its stored length", ErrConnectorProfile)
	}
	if claims.Email != "" {
		// A provider with no field for it is taken at its word; one that has
		// the field has to set it.
		verified := true
		if c.EmailVerified != "" {
			verified = claimBool(document, c.EmailVerified)
		}
		claims.EmailVerified = &verified
	}
	return claims, nil
}

// claimString reads a dotted path and renders the leaf as a string. Numbers
// keep their exact literal, so an integer identifier never gains an exponent.
func claimString(document map[string]any, path string) string {
	switch value := claimValue(document, path).(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	case bool:
		if value {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func claimBool(document map[string]any, path string) bool {
	switch value := claimValue(document, path).(type) {
	case bool:
		return value
	case string:
		return value == "true"
	case json.Number:
		return value.String() == "1"
	default:
		return false
	}
}

// claimValue walks a dotted path through nested objects and arrays. A numeric
// segment indexes an array, which is how Twitch and VK return the one user they
// were asked about.
func claimValue(document map[string]any, path string) any {
	if path == "" || document == nil {
		return nil
	}
	var current any = document
	for _, segment := range strings.Split(path, ".") {
		switch container := current.(type) {
		case map[string]any:
			value, ok := container[segment]
			if !ok {
				return nil
			}
			current = value
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(container) {
				return nil
			}
			current = container[index]
		default:
			return nil
		}
	}
	return current
}
