package oauth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"

	"7link.com/platform/internal/iam/oauth/model"
	"7link.com/platform/internal/infra/guid"
)

// The device authorization grant, RFC 8628. A device with no browser and no
// keyboard cannot complete a redirect flow: it asks for a code, shows the person
// a short one to type somewhere else, and polls until they approve.
const (
	DeviceStatusPending  = "pending"
	DeviceStatusApproved = "approved"
	DeviceStatusDenied   = "denied"

	// grantDeviceCode is the grant type a client declares to use this flow.
	grantDeviceCode = "urn:ietf:params:oauth:grant-type:device_code"

	// userCodeAlphabet omits every character a person can misread aloud or
	// mistype: no 0/O, no 1/I/L, no 2/Z, no 5/S, no 8/B. What is left is what a
	// support call can spell without a second attempt.
	userCodeAlphabet = "ACDEFGHJKMNPQRTUVWXY34679"
	// userCodeGroups and userCodeGroupSize shape the code a person reads off a
	// screen. Eight characters from this alphabet carry about 37 bits, far more
	// than a code's ten-minute life and the enforced poll interval allow guessing.
	userCodeGroups    = 2
	userCodeGroupSize = 4
)

var (
	// ErrAuthorizationPending is RFC 8628's answer while nobody has approved
	// yet: the device keeps polling rather than treating it as a failure.
	ErrAuthorizationPending = errors.New("authorization_pending")
	// ErrSlowDown tells a device polling faster than it was told to back off.
	ErrSlowDown = errors.New("slow_down")
	// ErrExpiredToken reports a request nobody approved in time.
	ErrExpiredToken = errors.New("expired_token")
	// ErrAccessDenied reports that somebody refused the request.
	ErrAccessDenied = errors.New("access_denied")
	// ErrDeviceRequestNotFound reports a user code that matches nothing, which is
	// what somebody mistyping one gets.
	ErrDeviceRequestNotFound = errors.New("device authorization request not found")
	// ErrDeviceVerificationURIUnset reports a deployment with no verification
	// page configured, which makes the grant impossible to complete.
	ErrDeviceVerificationURIUnset = errors.New("device verification URI is not configured")
)

// DeviceAuthorization is what a device is told after asking. The user code is
// returned once, in this answer, because only its hash is stored.
type DeviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// DeviceRequest is what the person approving one is shown. It names the client
// and the scopes and nothing about the device, because nothing about the device
// is known.
type DeviceRequest struct {
	ClientID   guid.ID   `json:"client_id"`
	ClientName string    `json:"client_name"`
	Scope      string    `json:"scope"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// newUserCode builds the code a person types. It is grouped with a dash so it
// can be read aloud, and drawn from crypto/rand because guessing one inside its
// window would be approving somebody else's device.
func newUserCode() (string, error) {
	groups := make([]string, 0, userCodeGroups)
	for group := 0; group < userCodeGroups; group++ {
		letters := make([]byte, userCodeGroupSize)
		for index := range letters {
			choice, err := rand.Int(rand.Reader, big.NewInt(int64(len(userCodeAlphabet))))
			if err != nil {
				return "", fmt.Errorf("generate user code: %w", err)
			}
			letters[index] = userCodeAlphabet[choice.Int64()]
		}
		groups = append(groups, string(letters))
	}
	return strings.Join(groups, "-"), nil
}

// normalizeUserCode accepts what a person actually types: any case, with or
// without the grouping dash, and with the spaces a copy-paste leaves behind.
func normalizeUserCode(value string) string {
	var builder strings.Builder
	for _, character := range strings.ToUpper(strings.TrimSpace(value)) {
		if strings.ContainsRune(userCodeAlphabet, character) {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

// StartDeviceAuthorization records a device's request and returns what it needs
// to show somebody. The client must be permitted this grant: a device flow hands
// out a token with no redirect to bind it to, so admitting a client that never
// asked for it would widen what a leaked client identifier is worth.
func (s *Service) StartDeviceAuthorization(ctx context.Context, clientID, requestedScope string) (DeviceAuthorization, error) {
	// RFC 8628 requires the response to name the page a person goes to, and
	// nobody can complete the flow without one. An unset page is a deployment
	// that has not finished configuring this grant, not a bad request, so it is
	// reported as the fault it is rather than by handing a device an empty URI.
	if s.device.VerificationURI == "" {
		return DeviceAuthorization{}, ErrDeviceVerificationURIUnset
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return DeviceAuthorization{}, err
	}
	// A device cannot keep a secret, so it authenticates as a public client by
	// its identifier alone. Requiring the grant to be declared is what keeps a
	// leaked identifier from being usable this way.
	id, err := guid.RequireID(clientID)
	if err != nil {
		return DeviceAuthorization{}, errInvalidClient
	}
	client, err := repo.deviceClient(ctx, id)
	// A client that is not there is a refusal; a failed read is a fault, and a
	// device told its client is invalid stops rather than retrying.
	if err != nil && !errors.Is(err, ErrInvalidRequest) {
		return DeviceAuthorization{}, err
	}
	if err != nil || client.Status != "active" || !containsWord(client.GrantTypes, grantDeviceCode) {
		return DeviceAuthorization{}, errInvalidClient
	}
	scope := normalizeWords(requestedScope)
	if scope == "" {
		scope = normalizeWords(client.Scopes)
	}
	if !allowedScopes(client.Scopes, scope) {
		return DeviceAuthorization{}, ErrInvalidRequest
	}

	deviceCode, err := secureToken(32)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	userCode, err := newUserCode()
	if err != nil {
		return DeviceAuthorization{}, err
	}
	now := oauthTime(s.now())
	row := model.DeviceAuthorization{
		DeviceCodeHash: hashToken(deviceCode),
		UserCodeHash:   hashToken(normalizeUserCode(userCode)),
		ClientID:       client.ID, TenantID: client.TenantID, Scope: scope,
		Status: DeviceStatusPending, Interval: s.device.interval(),
		CreatedAt: now, ExpiresAt: now.Add(s.device.lifetime()),
	}
	if err := repo.insertDeviceAuthorization(ctx, &row); err != nil {
		return DeviceAuthorization{}, err
	}
	return DeviceAuthorization{
		DeviceCode: deviceCode, UserCode: userCode,
		VerificationURI: s.device.VerificationURI,
		// The complete URI saves the person typing the code. It is built here
		// rather than by the device so the code is escaped once, correctly.
		VerificationURIComplete: s.device.VerificationURI + "?user_code=" + url.QueryEscape(userCode),
		ExpiresIn:               int64(s.device.lifetime().Seconds()),
		Interval:                row.Interval,
	}, nil
}

// LookupDeviceRequest resolves a user code for the page somebody approves on. It
// reports nothing about why a code is unusable beyond that it is: distinguishing
// "never existed" from "expired" would let somebody probe for live codes.
func (s *Service) LookupDeviceRequest(ctx context.Context, tenantID guid.ID, userCode string) (DeviceRequest, error) {
	normalized := normalizeUserCode(userCode)
	if normalized == "" || tenantID.Zero() {
		return DeviceRequest{}, ErrDeviceRequestNotFound
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return DeviceRequest{}, err
	}
	row, err := repo.deviceAuthorizationByUserCode(ctx, hashToken(normalized))
	if err != nil && !errors.Is(err, ErrDeviceRequestNotFound) {
		return DeviceRequest{}, err
	}
	if err != nil {
		return DeviceRequest{}, ErrDeviceRequestNotFound
	}
	// The request belongs to one tenant, and the caller was admitted for one.
	// Without this the tenant in the path would decide who may approve but not
	// what they may approve, and a member of one tenant could read -- and spend
	// -- a request a device started against another.
	if row.TenantID != tenantID || row.Status != DeviceStatusPending || row.ConsumedAt != nil ||
		!row.ExpiresAt.After(oauthTime(s.now())) {
		return DeviceRequest{}, ErrDeviceRequestNotFound
	}
	client, err := repo.deviceClient(ctx, row.ClientID)
	if err != nil && !errors.Is(err, ErrInvalidRequest) {
		return DeviceRequest{}, err
	}
	if err != nil || client.TenantID != row.TenantID {
		return DeviceRequest{}, ErrDeviceRequestNotFound
	}
	return DeviceRequest{
		ClientID: row.ClientID, ClientName: client.Name, Scope: row.Scope,
		Status: row.Status, ExpiresAt: row.ExpiresAt,
	}, nil
}

// DecideDeviceRequest records somebody's approval or refusal. The principal is
// stored with the decision, so the token the device later collects is issued for
// whoever actually approved it rather than for whoever polls.
func (s *Service) DecideDeviceRequest(
	ctx context.Context, tenantID guid.ID, userCode string, principalID guid.ID, approve bool,
	authTime time.Time, acr int, amr string,
) error {
	normalized := normalizeUserCode(userCode)
	if normalized == "" || principalID.Zero() || tenantID.Zero() {
		return ErrDeviceRequestNotFound
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return err
	}
	status := DeviceStatusDenied
	if approve {
		status = DeviceStatusApproved
	}
	decided, err := repo.decideDeviceAuthorization(
		ctx, tenantID, hashToken(normalized), status, principalID, authTime.UTC(), acr, amr, oauthTime(s.now()),
	)
	if err != nil {
		return err
	}
	if !decided {
		// Either the code matches nothing, or somebody already decided it, or it
		// expired. A device must not be able to tell those apart by polling.
		return ErrDeviceRequestNotFound
	}
	return nil
}

// deviceToken completes the grant for a polling device. The poll interval is
// enforced here rather than trusted: a device that ignores it is slowed down, so
// a client cannot turn the endpoint into a guessing loop.
func (s *Service) deviceToken(ctx context.Context, repo *Repository, client *model.Client, deviceCode string) (TokenResponse, error) {
	if deviceCode == "" || !containsWord(client.GrantTypes, grantDeviceCode) {
		return TokenResponse{}, ErrInvalidRequest
	}
	row, err := repo.deviceAuthorizationByDeviceCode(ctx, hashToken(deviceCode))
	// A device told invalid_grant discards the device code and starts over, so a
	// failed read must not be reported as one.
	if err != nil && !errors.Is(err, ErrDeviceRequestNotFound) {
		return TokenResponse{}, err
	}
	if err != nil {
		return TokenResponse{}, ErrInvalidRequest
	}
	// The request belongs to the client that started it. Admitting another
	// client's device code would let one client collect another's token.
	if row.ClientID != client.ID || row.TenantID != client.TenantID {
		return TokenResponse{}, ErrInvalidRequest
	}
	now := oauthTime(s.now())
	if row.ConsumedAt != nil {
		return TokenResponse{}, ErrInvalidRequest
	}
	if !row.ExpiresAt.After(now) {
		return TokenResponse{}, ErrExpiredToken
	}
	if row.LastPolledAt != nil {
		earliest := row.LastPolledAt.Add(time.Duration(row.Interval) * time.Second)
		if now.Before(earliest) {
			if err := repo.touchDevicePoll(ctx, row.DeviceCodeHash, now); err != nil {
				return TokenResponse{}, err
			}
			return TokenResponse{}, ErrSlowDown
		}
	}
	if err := repo.touchDevicePoll(ctx, row.DeviceCodeHash, now); err != nil {
		return TokenResponse{}, err
	}
	switch row.Status {
	case DeviceStatusDenied:
		return TokenResponse{}, ErrAccessDenied
	case DeviceStatusPending:
		return TokenResponse{}, ErrAuthorizationPending
	}
	if row.PrincipalID == nil || row.AuthTime == nil {
		// The check constraint forbids this, so reaching it means the row was
		// written by something that bypassed it.
		return TokenResponse{}, ErrInvalidRequest
	}
	if err := repo.activeMember(ctx, client.TenantID, *row.PrincipalID); err != nil {
		return TokenResponse{}, err
	}
	assurance := oauthAssurance(*row.AuthTime, row.ACR, row.AMR, client.ID.String())
	access, expires, err := s.authn.IssueTenantAccessTokenWithAssurance(
		ctx, *row.PrincipalID, client.TenantID.String(), "", row.Scope, assurance,
	)
	if err != nil {
		return TokenResponse{}, err
	}
	response := TokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: expires, Scope: row.Scope}
	var refresh *model.RefreshToken
	if containsWord(client.GrantTypes, "refresh_token") {
		response.RefreshToken, refresh, err = s.makeRefresh(*row.PrincipalID, client.ID, row.Scope, "", assurance)
		if err != nil {
			return TokenResponse{}, err
		}
	}
	// Consuming is conditional on the row still being unconsumed, so two devices
	// polling one code cannot both be issued a token, and the refresh token is
	// stored in the same transaction so it cannot outlive a failed consume.
	consumed, err := repo.consumeDeviceAuthorization(ctx, row.DeviceCodeHash, now, refresh)
	if err != nil {
		return TokenResponse{}, err
	}
	if !consumed {
		return TokenResponse{}, ErrInvalidRequest
	}
	return response, nil
}

// DeviceConfig bounds how long a device has and how often it may ask. It is
// exported because an operator sets it: the verification page is wherever this
// deployment hosts it.
type DeviceConfig struct {
	VerificationURI string        `mapstructure:"verification_uri" description:"page where somebody enters the user code" default:""`
	CodeTTL         time.Duration `mapstructure:"code_ttl" description:"how long a device code stays usable" default:"10m"`
	PollInterval    time.Duration `mapstructure:"poll_interval" description:"how often a device may poll" default:"5s"`
}

func (c DeviceConfig) interval() int {
	if c.PollInterval > 0 {
		return int(c.PollInterval.Seconds())
	}
	return 5
}

func (c DeviceConfig) lifetime() time.Duration {
	if c.CodeTTL > 0 {
		return c.CodeTTL
	}
	return 10 * time.Minute
}
