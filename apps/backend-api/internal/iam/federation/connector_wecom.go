package federation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// wecomFlow implements WeCom, where the application authenticates separately
// from the person: the authorization code is redeemed with a token the
// application obtains for itself from its corp credentials, not with the code
// plus a client secret. There is no OAuth 2.0 token endpoint to point at, which
// is why this is a flow rather than a table row.
type wecomFlow struct {
	// tokenEndpoint and identityEndpoint and detailEndpoint default to WeCom's
	// own. They are fields so a test can point one instance at a local server.
	tokenEndpoint    string
	identityEndpoint string
	detailEndpoint   string
}

const (
	wecomTokenEndpoint    = "https://qyapi.weixin.qq.com/cgi-bin/gettoken"
	wecomIdentityEndpoint = "https://qyapi.weixin.qq.com/cgi-bin/auth/getuserinfo"
	wecomDetailEndpoint   = "https://qyapi.weixin.qq.com/cgi-bin/auth/getuserdetail"
)

// ErrWeComExternalUser reports a sign-in by someone who is not a member of the
// organization. WeCom answers for them with an OpenId instead of a UserId, and
// that identifier means nothing to the directory this connector links against.
var ErrWeComExternalUser = errors.New("wecom sign-in is limited to members of the organization")

func (f wecomFlow) endpoints() (token, identity, detail string) {
	token, identity, detail = f.tokenEndpoint, f.identityEndpoint, f.detailEndpoint
	if token == "" {
		token = wecomTokenEndpoint
	}
	if identity == "" {
		identity = wecomIdentityEndpoint
	}
	if detail == "" {
		detail = wecomDetailEndpoint
	}
	return token, identity, detail
}

func (f wecomFlow) Profile(ctx context.Context, upstream *upstreamHTTP, ex exchange) (map[string]any, error) {
	tokenEndpoint, identityEndpoint, detailEndpoint := f.endpoints()

	// The application proves itself first. This token belongs to the deployment,
	// not to the person signing in, and is never put in a session.
	token, err := wecomGet(ctx, upstream, tokenEndpoint, url.Values{
		"corpid":     {ex.cred.ClientID},
		"corpsecret": {ex.cred.ClientSecret},
	}, ErrOIDCToken)
	if err != nil {
		return nil, err
	}
	appToken := claimString(token, "access_token")
	if appToken == "" {
		return nil, fmt.Errorf("%w: wecom returned no application token", ErrOIDCToken)
	}

	identity, err := wecomGet(ctx, upstream, identityEndpoint, url.Values{
		"access_token": {appToken},
		"code":         {ex.code},
	}, ErrConnectorProfile)
	if err != nil {
		return nil, err
	}
	// An OpenId in place of a UserId means WeChat answered for an outside
	// contact. Linking that identifier would bind an account to someone the
	// directory does not contain.
	if claimString(identity, "openid") != "" || claimString(identity, "OpenId") != "" {
		return nil, ErrWeComExternalUser
	}
	// WeCom's documentation capitalises UserId and its JSON has been seen both
	// ways, so the member's identifier is normalized here onto the one key the
	// connector's Subject path reads. Tolerating both spellings in this check
	// while the claim path read only one is how a login that WeCom answered
	// correctly would still have failed.
	member := claimString(identity, "UserId")
	if member == "" {
		member = claimString(identity, "userid")
	}
	if member == "" {
		return nil, fmt.Errorf("%w: wecom identified nobody", ErrConnectorProfile)
	}
	identity["userid"] = member

	// A ticket is only issued under the private-information scope. Without it
	// the member's identifier is all WeCom states, which is enough to link an
	// existing account and not enough to create one.
	ticket := claimString(identity, "user_ticket")
	if ticket == "" {
		return identity, nil
	}
	detail, err := wecomPostJSON(ctx, upstream, detailEndpoint, url.Values{"access_token": {appToken}},
		map[string]string{"user_ticket": ticket}, ErrConnectorProfile)
	if err != nil {
		return nil, err
	}
	// The identifier stays authoritative from the identity step; the detail call
	// only adds what the scope permitted.
	for key, value := range detail {
		if _, taken := identity[key]; !taken {
			identity[key] = value
		}
	}
	return identity, nil
}

func wecomGet(ctx context.Context, upstream *upstreamHTTP, endpoint string, params url.Values, sentinel error) (map[string]any, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, appendQuery(endpoint, params), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build wecom request: %w", sentinel, err)
	}
	return wecomDo(ctx, upstream, request, sentinel)
}

func wecomPostJSON(ctx context.Context, upstream *upstreamHTTP, endpoint string, params url.Values, body map[string]string, sentinel error) (map[string]any, error) {
	encoded, err := jsonBody(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", sentinel, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, appendQuery(endpoint, params), encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: build wecom request: %w", sentinel, err)
	}
	request.Header.Set("Content-Type", "application/json;charset=UTF-8")
	return wecomDo(ctx, upstream, request, sentinel)
}

// wecomDo performs the request and reports a refusal. WeCom answers HTTP 200
// for its own failures, so a non-zero errcode has to be read as an error or a
// failed call would look like an empty answer.
func wecomDo(ctx context.Context, upstream *upstreamHTTP, request *http.Request, sentinel error) (map[string]any, error) {
	document, err := upstream.Do(request, sentinel)
	if err != nil {
		return nil, err
	}
	if code := claimString(document, "errcode"); code != "" && code != "0" {
		return nil, fmt.Errorf("%w: wecom errcode %s: %s", sentinel, code, claimString(document, "errmsg"))
	}
	return document, nil
}
