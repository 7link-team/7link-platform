package federation

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// qqFlow implements QQ Connect, which is the reason profileFlow exists. Three
// things put it outside the declarative table: the token endpoint answers a
// form-encoded body, the endpoint that reveals the user's identifier answers
// JSONP, and the profile read needs both that identifier and the application's
// own key — so it is three requests, not two.
//
// Both of the first two endpoints accept fmt=json, which is asked for here. QQ
// has nonetheless been observed answering JSONP regardless, so the wrapper is
// stripped rather than trusted away.
type qqFlow struct {
	// identityEndpoint and profileEndpoint default to QQ's own addresses. They
	// are fields rather than constants so a test can point one instance at a
	// local server without mutating anything the other instances read.
	identityEndpoint string
	profileEndpoint  string
}

const (
	qqIdentityEndpoint = "https://graph.qq.com/oauth2.0/me"
	qqProfileEndpoint  = "https://graph.qq.com/user/get_user_info"
)

func (f qqFlow) identity() string {
	if f.identityEndpoint != "" {
		return f.identityEndpoint
	}
	return qqIdentityEndpoint
}

func (f qqFlow) profile() string {
	if f.profileEndpoint != "" {
		return f.profileEndpoint
	}
	return qqProfileEndpoint
}

func (f qqFlow) Profile(ctx context.Context, upstream *upstreamHTTP, conn connector, cred credentials, code, callback, codeVerifier string) (map[string]any, error) {
	accessToken, err := qqAccessToken(ctx, upstream, conn, cred, code, callback)
	if err != nil {
		return nil, err
	}
	openID, err := f.openID(ctx, upstream, accessToken)
	if err != nil {
		return nil, err
	}
	profile, err := f.userInfo(ctx, upstream, cred.ClientID, accessToken, openID)
	if err != nil {
		return nil, err
	}
	// The identifier is not in the profile document, so it is merged in under
	// the key the connector's Subject path names.
	profile["openid"] = openID
	return profile, nil
}

// qqAccessToken redeems the code. The response is form encoded unless QQ honors
// fmt=json, so both shapes are accepted.
func qqAccessToken(ctx context.Context, upstream *upstreamHTTP, conn connector, cred credentials, code, callback string) (string, error) {
	params := url.Values{}
	params.Set("grant_type", "authorization_code")
	params.Set("client_id", cred.ClientID)
	params.Set("client_secret", cred.ClientSecret)
	params.Set("code", code)
	params.Set("redirect_uri", callback)
	params.Set("fmt", "json")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, appendQuery(conn.Token, params), nil)
	if err != nil {
		return "", fmt.Errorf("%w: build token request: %w", ErrOIDCToken, err)
	}
	body, err := upstream.DoRaw(request, ErrOIDCToken)
	if err != nil {
		return "", err
	}
	if document, err := decodeJSONObject(unwrapJSONP(body), ErrOIDCToken); err == nil {
		if token := claimString(document, "access_token"); token != "" {
			return token, nil
		}
		return "", fmt.Errorf("%w: %s", ErrOIDCToken, claimString(document, "error_description"))
	}
	// Form encoding: access_token=...&expires_in=...&refresh_token=...
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return "", fmt.Errorf("%w: decode token response", ErrOIDCToken)
	}
	token := values.Get("access_token")
	if token == "" {
		return "", fmt.Errorf("%w: %s", ErrOIDCToken, values.Get("error_description"))
	}
	return token, nil
}

// openID resolves the per-application user identifier.
func (f qqFlow) openID(ctx context.Context, upstream *upstreamHTTP, accessToken string) (string, error) {
	params := url.Values{}
	params.Set("access_token", accessToken)
	params.Set("fmt", "json")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, appendQuery(f.identity(), params), nil)
	if err != nil {
		return "", fmt.Errorf("%w: build identity request: %w", ErrConnectorProfile, err)
	}
	body, err := upstream.DoRaw(request, ErrConnectorProfile)
	if err != nil {
		return "", err
	}
	document, err := decodeJSONObject(unwrapJSONP(body), ErrConnectorProfile)
	if err != nil {
		return "", err
	}
	openID := claimString(document, "openid")
	if openID == "" {
		return "", fmt.Errorf("%w: identity response carries no openid", ErrConnectorProfile)
	}
	return openID, nil
}

// userInfo reads the display profile, which QQ keys by application and user
// together rather than by the access token alone.
func (f qqFlow) userInfo(ctx context.Context, upstream *upstreamHTTP, clientID, accessToken, openID string) (map[string]any, error) {
	params := url.Values{}
	params.Set("access_token", accessToken)
	params.Set("oauth_consumer_key", clientID)
	params.Set("openid", openID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, appendQuery(f.profile(), params), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build profile request: %w", ErrConnectorProfile, err)
	}
	document, err := upstream.Do(request, ErrConnectorProfile)
	if err != nil {
		return nil, err
	}
	// QQ reports failure in the body with HTTP 200, so a non-zero ret has to be
	// read as an error or a failed read would look like an empty profile.
	if ret := claimString(document, "ret"); ret != "" && ret != "0" {
		return nil, fmt.Errorf("%w: qq ret %s: %s", ErrConnectorProfile, ret, claimString(document, "msg"))
	}
	return document, nil
}

// unwrapJSONP strips a "callback( ... );" wrapper when one is present and
// returns the input untouched otherwise.
func unwrapJSONP(body []byte) []byte {
	start := bytes.IndexByte(body, '{')
	end := bytes.LastIndexByte(body, '}')
	if start < 0 || end < start {
		return body
	}
	return body[start : end+1]
}
