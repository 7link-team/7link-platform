package oauth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deviceHTTPClient registers a public client permitted the device grant, which
// is what a television is: no secret, a declared grant.
func deviceHTTPClient(t *testing.T, h oauthHTTPHarness, grants []string) string {
	t.Helper()
	created, _, err := h.service.CreateClient(
		t.Context(), 7, "Television", nil, grants, []string{"openid", "profile"}, true, ConsentImplicit,
	)
	require.NoError(t, err)
	return created.ID
}

// approverHeaders names a caller whose session carries an assurance, so a test
// can watch that assurance travel to the token the device collects.
func approverHeaders(t *testing.T, h oauthHTTPHarness, principalID guid.ID) map[string]string {
	t.Helper()
	token := "approver-" + principalID.String()
	h.web.mu.Lock()
	h.web.tokens[token] = &authnext.Claims{
		Issuer: "https://iam.example", Subject: principalID.String(),
		SubjectType: authnext.SubjectTypePrincipal, PrincipalID: principalID,
		AuthTime: oauthTestNow, ACR: 2, AMR: []string{"pwd", "otp"},
	}
	h.web.mu.Unlock()
	return map[string]string{"Authorization": "Bearer " + token}
}

func TestDeviceAuthorizationEndpointNeedsNoCredentialButDoesNeedTheGrant(t *testing.T) {
	// A device has nothing to authenticate with, so this endpoint is public and
	// the declared grant is the whole of its protection.
	harness := newOAuthHTTPHarness(t)
	permitted := deviceHTTPClient(t, harness, []string{grantDeviceCode})

	response := harness.form(http.MethodPost, "/oauth/device_authorization",
		url.Values{"client_id": {permitted}, "scope": {"openid"}}, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var answer DeviceAuthorization
	decodeJSON(t, response, &answer)
	assert.NotEmpty(t, answer.DeviceCode)
	assert.NotEmpty(t, answer.UserCode)
	assert.Equal(t, "https://console.example.test/device", answer.VerificationURI)
	assert.Equal(t, answer.VerificationURI+"?user_code="+answer.UserCode, answer.VerificationURIComplete,
		"the complete URI saves the person typing the code")
	assert.Positive(t, answer.ExpiresIn)
	assert.Positive(t, answer.Interval)

	withoutGrant := deviceHTTPClient(t, harness, []string{"refresh_token"})
	refused := harness.form(http.MethodPost, "/oauth/device_authorization",
		url.Values{"client_id": {withoutGrant}}, nil)
	assert.Equal(t, http.StatusUnauthorized, refused.Code)
	assert.Equal(t, "invalid_client", decodeProtocolError(t, refused).Code)

	tooWide := harness.form(http.MethodPost, "/oauth/device_authorization",
		url.Values{"client_id": {permitted}, "scope": {"openid admin"}}, nil)
	assert.Equal(t, http.StatusBadRequest, tooWide.Code)
	assert.Equal(t, "invalid_scope", decodeProtocolError(t, tooWide).Code)

	malformed := harness.request(http.MethodPost, "/oauth/device_authorization",
		"application/x-www-form-urlencoded", stringReader("%zz"), nil)
	assert.Equal(t, http.StatusBadRequest, malformed.Code)
	assert.Equal(t, "invalid_request", decodeProtocolError(t, malformed).Code)
}

func TestDeviceFlowOverHTTPIssuesForTheApprover(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	clock := oauthTestNow
	harness.service.now = func() time.Time { return clock }
	clientID := deviceHTTPClient(t, harness, []string{grantDeviceCode, "refresh_token"})

	started := harness.form(http.MethodPost, "/oauth/device_authorization",
		url.Values{"client_id": {clientID}, "scope": {"openid"}}, nil)
	require.Equal(t, http.StatusOK, started.Code, started.Body.String())
	var answer DeviceAuthorization
	decodeJSON(t, started, &answer)

	poll := func() *protocolErrorRecorder {
		return &protocolErrorRecorder{harness.form(http.MethodPost, "/oauth/token", url.Values{
			"grant_type": {grantDeviceCode}, "client_id": {clientID}, "device_code": {answer.DeviceCode},
		}, nil)}
	}

	pending := poll()
	require.Equal(t, http.StatusBadRequest, pending.Code, pending.Body.String())
	assert.Equal(t, "authorization_pending", decodeProtocolError(t, pending.ResponseRecorder).Code,
		"the device is told to keep waiting, not that something went wrong")

	// Polling again before the interval elapses is slowed down rather than
	// answered, so a device cannot turn the endpoint into a loop.
	hurried := poll()
	assert.Equal(t, "slow_down", decodeProtocolError(t, hurried.ResponseRecorder).Code)

	// The person approving reads what they are approving.
	headers := approverHeaders(t, harness, 101)
	lookup := harness.request(http.MethodGet,
		"/tenants/7/oauth/device-requests?user_code="+url.QueryEscape(answer.UserCode), "", nil, headers)
	require.Equal(t, http.StatusOK, lookup.Code, lookup.Body.String())
	request := decodeOAuthEnvelope[DeviceRequest](t, lookup).Data
	assert.Equal(t, "Television", request.ClientName, "the page names the client, not the device")
	assert.Equal(t, "openid", request.Scope)
	assert.Equal(t, DeviceStatusPending, request.Status)

	decision := harness.json(http.MethodPost, "/tenants/7/oauth/device-requests/decisions",
		map[string]any{"user_code": answer.UserCode, "approve": true}, headers)
	require.Equal(t, http.StatusOK, decision.Code, decision.Body.String())
	assert.Equal(t, "approved", decodeOAuthEnvelope[map[string]string](t, decision).Data["status"])

	// An approved request is no longer pending, so the page cannot be read again.
	reread := harness.request(http.MethodGet,
		"/tenants/7/oauth/device-requests?user_code="+url.QueryEscape(answer.UserCode), "", nil, headers)
	assert.Equal(t, http.StatusNotFound, reread.Code)

	clock = clock.Add(time.Minute)
	issued := poll()
	require.Equal(t, http.StatusOK, issued.Code, issued.Body.String())
	var tokens TokenResponse
	decodeJSON(t, issued.ResponseRecorder, &tokens)
	require.NotEmpty(t, tokens.AccessToken)
	assert.NotEmpty(t, tokens.RefreshToken, "the client declared refresh_token")
	claims := harness.web.claimsFor(tokens.AccessToken)
	require.NotNil(t, claims)
	assert.Equal(t, guid.ID(101), claims.PrincipalID, "the token is the approver's, not the poller's")
	assert.Equal(t, 2, claims.ACR, "and carries the assurance they actually had")

	// The code is spent: a second device holding it collects nothing.
	clock = clock.Add(time.Minute)
	spent := poll()
	assert.Equal(t, http.StatusBadRequest, spent.Code)
	assert.Equal(t, "invalid_grant", decodeProtocolError(t, spent.ResponseRecorder).Code)
}

func TestRefusedDeviceRequestTellsTheDeviceSo(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	clock := oauthTestNow
	harness.service.now = func() time.Time { return clock }
	clientID := deviceHTTPClient(t, harness, []string{grantDeviceCode})

	started := harness.form(http.MethodPost, "/oauth/device_authorization",
		url.Values{"client_id": {clientID}}, nil)
	require.Equal(t, http.StatusOK, started.Code, started.Body.String())
	var answer DeviceAuthorization
	decodeJSON(t, started, &answer)

	headers := approverHeaders(t, harness, 101)
	decision := harness.json(http.MethodPost, "/tenants/7/oauth/device-requests/decisions",
		map[string]any{"user_code": answer.UserCode, "approve": false}, headers)
	require.Equal(t, http.StatusOK, decision.Code, decision.Body.String())
	assert.Equal(t, "denied", decodeOAuthEnvelope[map[string]string](t, decision).Data["status"])

	// Deciding twice is refused, so a refusal cannot be overturned by replaying
	// the code.
	again := harness.json(http.MethodPost, "/tenants/7/oauth/device-requests/decisions",
		map[string]any{"user_code": answer.UserCode, "approve": true}, headers)
	assert.Equal(t, http.StatusNotFound, again.Code)

	clock = clock.Add(time.Minute)
	polled := harness.form(http.MethodPost, "/oauth/token", url.Values{
		"grant_type": {grantDeviceCode}, "client_id": {clientID}, "device_code": {answer.DeviceCode},
	}, nil)
	assert.Equal(t, http.StatusBadRequest, polled.Code)
	assert.Equal(t, "access_denied", decodeProtocolError(t, polled).Code)
}

func TestUnknownUserCodeLooksTheSameAsAnExpiredOne(t *testing.T) {
	// Telling those apart would let somebody probe for live codes to approve.
	harness := newOAuthHTTPHarness(t)
	headers := approverHeaders(t, harness, 101)

	for _, code := range []string{"ACDE-FGHJ", "!!!!"} {
		response := harness.request(http.MethodGet,
			"/tenants/7/oauth/device-requests?user_code="+url.QueryEscape(code), "", nil, headers)
		assert.Equal(t, http.StatusNotFound, response.Code, "code %q", code)
	}
	decision := harness.json(http.MethodPost, "/tenants/7/oauth/device-requests/decisions",
		map[string]any{"user_code": "ACDE-FGHJ", "approve": true}, headers)
	assert.Equal(t, http.StatusNotFound, decision.Code)
}

func TestDeviceApprovalNeedsASignedInCaller(t *testing.T) {
	// The token the device collects is issued for whoever approved, so an
	// anonymous approval has nobody to issue for.
	harness := newOAuthHTTPHarness(t)
	for _, headers := range []map[string]string{nil, {"Authorization": "Bearer nonsense"}} {
		response := harness.json(http.MethodPost, "/tenants/7/oauth/device-requests/decisions",
			map[string]any{"user_code": "ACDE-FGHJ", "approve": true}, headers)
		assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	}
	// A member of another tenant cannot approve in this one.
	response := harness.json(http.MethodPost, "/tenants/7/oauth/device-requests/decisions",
		map[string]any{"user_code": "ACDE-FGHJ", "approve": true}, managementHeaders(8))
	assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
}

func TestExchangeGrantEndpointsAreTenantScoped(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	service, _, err := harness.service.CreateClient(
		t.Context(), 7, "Service", nil, []string{grantTokenExchange}, []string{"openid"}, false, ConsentImplicit,
	)
	require.NoError(t, err)

	created := harness.json(http.MethodPost, "/tenants/7/oauth-exchange-grants", map[string]any{
		"client_id": service.ID, "subject_prefix": "101", "scope": "openid",
	}, managementHeaders(7))
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	grant := decodeOAuthEnvelope[ExchangeGrant](t, created).Data
	assert.Equal(t, ExchangeGrantActive, grant.Status)

	// The same pair twice is a conflict, so there is one answer to what this
	// client may do as that subject.
	conflict := harness.json(http.MethodPost, "/tenants/7/oauth-exchange-grants", map[string]any{
		"client_id": service.ID, "subject_prefix": "101", "scope": "openid",
	}, managementHeaders(7))
	assert.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())

	// A bare star would permit acting as anybody.
	wildcard := harness.json(http.MethodPost, "/tenants/7/oauth-exchange-grants", map[string]any{
		"client_id": service.ID, "subject_prefix": "*", "scope": "openid",
	}, managementHeaders(7))
	assert.Equal(t, http.StatusUnprocessableEntity, wildcard.Code, wildcard.Body.String())

	listed := harness.request(http.MethodGet, "/tenants/7/oauth-exchange-grants", "", nil, managementHeaders(7))
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	assert.Len(t, decodeOAuthEnvelope[[]ExchangeGrant](t, listed).Data, 1)

	// Another tenant sees its own grants, which are none, and cannot withdraw
	// this one by its identifier: it is told the grant is not there rather than
	// that it exists and belongs to somebody else.
	foreign := harness.request(http.MethodGet, "/tenants/8/oauth-exchange-grants", "", nil, managementHeaders(8))
	require.Equal(t, http.StatusOK, foreign.Code, foreign.Body.String())
	assert.Empty(t, decodeOAuthEnvelope[[]ExchangeGrant](t, foreign).Data)

	stolen := harness.request(http.MethodDelete, "/oauth-exchange-grants/"+grant.ID.String(), "", nil, managementHeaders(8))
	assert.Equal(t, http.StatusNotFound, stolen.Code, stolen.Body.String())

	deleted := harness.request(http.MethodDelete, "/oauth-exchange-grants/"+grant.ID.String(), "", nil, managementHeaders(7))
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	assert.True(t, decodeOAuthEnvelope[map[string]bool](t, deleted).Data["deleted"])

	gone := harness.request(http.MethodDelete, "/oauth-exchange-grants/"+grant.ID.String(), "", nil, managementHeaders(7))
	assert.Equal(t, http.StatusNotFound, gone.Code, "an unknown grant is not found rather than forbidden")

	emptied := harness.request(http.MethodGet, "/tenants/7/oauth-exchange-grants", "", nil, managementHeaders(7))
	require.Equal(t, http.StatusOK, emptied.Code)
	assert.Empty(t, decodeOAuthEnvelope[[]ExchangeGrant](t, emptied).Data)
}

func TestExchangeGrantNamingAnUnreachableClientIsRefused(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	response := harness.json(http.MethodPost, "/tenants/7/oauth-exchange-grants", map[string]any{
		"client_id": "999999", "subject_prefix": "101", "scope": "openid",
	}, managementHeaders(7))
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
}

func TestDeviceErrorMappingLeavesNothingToTheDefault(t *testing.T) {
	// Every error this module raises has a status chosen for it; anything else is
	// a fault rather than a refusal, and must not be reported as one.
	for err, want := range map[error]int{
		ErrDeviceRequestNotFound: http.StatusNotFound,
		ErrExchangeGrantMissing:  http.StatusNotFound,
		ErrExchangeGrantExists:   http.StatusConflict,
		ErrInvalidExchangeGrant:  http.StatusUnprocessableEntity,
		errOAuthTestUnexpected:   http.StatusInternalServerError,
	} {
		mapped := deviceError(err)
		require.Error(t, mapped)
		status, ok := mapped.(interface{ GetStatus() int })
		require.True(t, ok, "%v", err)
		assert.Equal(t, want, status.GetStatus(), "%v", err)
	}
	// A status chosen upstream is kept rather than flattened to a fault.
	assert.Equal(t, http.StatusTeapot, deviceError(&protocolError{
		Status: http.StatusTeapot, Code: "brewing",
	}).(interface{ GetStatus() int }).GetStatus())
}

func TestJoinWordsRendersAssuranceBackIntoStoredForm(t *testing.T) {
	// The methods are a set, so they are stored sorted and deduplicated; order
	// carries no meaning in an amr claim.
	assert.Equal(t, "otp pwd", joinWords([]string{"pwd", "otp"}))
	assert.Equal(t, "pwd", joinWords([]string{"pwd", "pwd"}))
	assert.Empty(t, joinWords(nil))
	assert.Equal(t, "pwd", joinWords([]string{"", "pwd", " "}))
}

func TestExchangeGrantOwnerIsUnknownWithoutAService(t *testing.T) {
	// The authorization layer asks before a request reaches a handler, so a
	// service that is not ready must answer "not found" rather than panic.
	var service *Service
	_, found, err := service.resolveExchangeGrantOwner(t.Context(), 1)
	assert.False(t, found)
	assert.ErrorIs(t, err, ErrExchangeGrantMissing)
}

// protocolErrorRecorder names a recorder a test polls repeatedly, so the
// assertions read as what the device was told rather than as index arithmetic.
type protocolErrorRecorder struct{ *httptest.ResponseRecorder }

var errOAuthTestUnexpected = errors.New("something unforeseen")

func stringReader(value string) io.Reader { return strings.NewReader(value) }

func decodeJSON(t *testing.T, response *httptest.ResponseRecorder, into any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), into), response.Body.String())
}

func TestTokenEndpointReportsEachGrantRefusalInItsOwnCode(t *testing.T) {
	// A client acts on the code, not on the status: a device told invalid_grant
	// discards its code, and one told authorization_pending keeps waiting. Every
	// error but invalid_client is a 400, which is what RFC 6749 gives the token
	// endpoint.
	harness := newOAuthHTTPHarness(t)
	service, secret, err := harness.service.CreateClient(
		t.Context(), 7, "Service", nil, []string{grantTokenExchange}, []string{"openid"}, false, ConsentImplicit,
	)
	require.NoError(t, err)
	subject := subjectToken(t, harness.oauthHarness, 101)
	post := func(form url.Values) *httptest.ResponseRecorder {
		return harness.form(http.MethodPost, "/oauth/token", form, nil)
	}
	exchange := func() url.Values {
		return url.Values{
			"grant_type": {grantTokenExchange}, "client_id": {service.ID},
			"client_secret": {secret}, "subject_token": {subject}, "scope": {"openid"},
		}
	}

	// No written grant: refused as unauthorized rather than told which subjects
	// would have been permitted.
	refused := post(exchange())
	assert.Equal(t, http.StatusBadRequest, refused.Code, refused.Body.String())
	assert.Equal(t, "unauthorized_client", decodeProtocolError(t, refused).Code)

	unverifiable := exchange()
	unverifiable.Set("subject_token_type", "urn:ietf:params:oauth:token-type:saml2")
	typed := post(unverifiable)
	assert.Equal(t, http.StatusBadRequest, typed.Code, typed.Body.String())
	assert.Equal(t, "invalid_request", decodeProtocolError(t, typed).Code)

	// An unknown grant name never reaches the service.
	unknown := post(url.Values{"grant_type": {"urn:example:invented"}, "client_id": {service.ID}})
	assert.Equal(t, http.StatusBadRequest, unknown.Code)
	assert.Equal(t, "invalid_request", decodeProtocolError(t, unknown).Code)

	// The two new grants are reachable, which a hardcoded whitelist once broke.
	for _, grant := range []string{grantDeviceCode, grantTokenExchange} {
		reached := post(url.Values{
			"grant_type": {grant}, "client_id": {service.ID}, "client_secret": {secret},
		})
		assert.NotEqual(t, "invalid_request", decodeProtocolError(t, reached).Code,
			"%s was refused before reaching the service", grant)
	}

	// The discovery document advertises both, since a device cannot find the
	// endpoint otherwise.
	discovery := harness.request(http.MethodGet, "/.well-known/openid-configuration", "", nil, nil)
	require.Equal(t, http.StatusOK, discovery.Code)
	var document map[string]any
	decodeJSON(t, discovery, &document)
	assert.Contains(t, document["grant_types_supported"], grantDeviceCode)
	assert.Contains(t, document["grant_types_supported"], grantTokenExchange)
	assert.Equal(t, document["issuer"].(string)+"/oauth/device_authorization",
		document["device_authorization_endpoint"])
}
