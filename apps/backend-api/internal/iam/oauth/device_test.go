package oauth

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/oauth/model"
	"7link.com/platform/internal/infra/guid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deviceClientFixture registers a client permitted the device grant.
func deviceClientFixture(t *testing.T, h *oauthHarness, id guid.ID, grants string) model.Client {
	t.Helper()
	client := model.Client{
		ID: id, TenantID: 7, Name: "Television", RedirectURIs: "[]", GrantTypes: grants,
		Scopes: "openid profile", PublicClient: true, ConsentMode: string(ConsentImplicit),
		Status: "active", CreatedAt: oauthTestNow, UpdatedAt: oauthTestNow,
	}
	require.NoError(t, h.db.Table(h.table(client.TableName())).Create(&client).Error)
	return client
}

func TestUserCodeIsReadableAloud(t *testing.T) {
	// A person reads this off a screen and types it somewhere else, so the
	// alphabet omits every character that is confused in that round trip.
	for attempt := 0; attempt < 200; attempt++ {
		code, err := newUserCode()
		require.NoError(t, err)

		assert.Len(t, code, userCodeGroups*userCodeGroupSize+userCodeGroups-1)
		assert.Equal(t, userCodeGroups-1, strings.Count(code, "-"), "grouped so it can be read aloud")
		for _, character := range strings.ReplaceAll(code, "-", "") {
			assert.Contains(t, userCodeAlphabet, string(character))
		}
		for _, confusable := range []string{"0", "O", "1", "I", "L", "2", "Z", "5", "S", "8", "B"} {
			assert.NotContains(t, code, confusable, "%s is misread or mistyped", confusable)
		}
	}
}

func TestUserCodesDoNotRepeat(t *testing.T) {
	// Guessing a live code would be approving somebody else's device, so the
	// codes have to come from real randomness rather than a counter.
	seen := map[string]struct{}{}
	for attempt := 0; attempt < 500; attempt++ {
		code, err := newUserCode()
		require.NoError(t, err)
		seen[code] = struct{}{}
	}
	assert.Greater(t, len(seen), 490, "500 draws should almost never collide")
}

func TestNormalizeUserCodeAcceptsWhatPeopleType(t *testing.T) {
	// Somebody typing a code adds or drops the dash, uses the wrong case, and
	// leaves the spaces a copy-paste brings. All of those are the same code.
	for _, typed := range []string{"ACDE-FGHJ", "acde-fghj", "ACDEFGHJ", " acde fghj ", "AcDe-FgHj"} {
		assert.Equal(t, "ACDEFGHJ", normalizeUserCode(typed), "typed %q", typed)
	}
	assert.Empty(t, normalizeUserCode(""))
	assert.Empty(t, normalizeUserCode("---"))
	assert.Equal(t, "ACDE", normalizeUserCode("ACDE!!!"), "punctuation is not part of a code")
}

func TestDeviceAuthorizationRequiresTheDeclaredGrant(t *testing.T) {
	// The device endpoint takes no secret, so the grant declaration on the client
	// is the whole of its protection: a leaked client identifier must not be
	// usable this way unless the client asked for it.
	h := newOAuthHarness(t, "")
	withoutGrant := deviceClientFixture(t, h, 31, "authorization_code")

	_, err := h.service.StartDeviceAuthorization(t.Context(), withoutGrant.ID.String(), "openid")
	require.ErrorIs(t, err, errInvalidClient)

	_, err = h.service.StartDeviceAuthorization(t.Context(), "999999", "openid")
	require.ErrorIs(t, err, errInvalidClient, "an unknown client is refused the same way")

	_, err = h.service.StartDeviceAuthorization(t.Context(), "not-an-id", "openid")
	require.ErrorIs(t, err, errInvalidClient)
}

func TestDeviceAuthorizationStoresOnlyHashes(t *testing.T) {
	// Both identifiers are secrets a holder presents. Storing either in the clear
	// would make a database read redeemable.
	h := newOAuthHarness(t, "sl_")
	client := deviceClientFixture(t, h, 32, grantDeviceCode+" refresh_token")

	answer, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
	require.NoError(t, err)
	require.NotEmpty(t, answer.DeviceCode)
	require.NotEmpty(t, answer.UserCode)
	assert.Equal(t, "https://console.example.test/device", answer.VerificationURI)
	assert.Contains(t, answer.VerificationURIComplete, answer.UserCode,
		"the complete URI saves the person typing the code")
	assert.Positive(t, answer.ExpiresIn)
	assert.Positive(t, answer.Interval)

	var row model.DeviceAuthorization
	require.NoError(t, h.db.Table(h.table(row.TableName())).Take(&row).Error)
	assert.Equal(t, hashToken(answer.DeviceCode), row.DeviceCodeHash)
	assert.Equal(t, hashToken(normalizeUserCode(answer.UserCode)), row.UserCodeHash)
	assert.NotContains(t, row.DeviceCodeHash, answer.DeviceCode)
	assert.Equal(t, DeviceStatusPending, row.Status)
	assert.Nil(t, row.PrincipalID, "nobody has approved it yet")
	assert.Equal(t, "openid", row.Scope)
}

func TestDeviceAuthorizationRefusesScopesTheClientLacks(t *testing.T) {
	h := newOAuthHarness(t, "")
	client := deviceClientFixture(t, h, 33, grantDeviceCode)

	_, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid admin")
	require.ErrorIs(t, err, ErrInvalidRequest)

	// Asking for nothing takes the client's own scopes rather than none.
	answer, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "")
	require.NoError(t, err)
	require.NotEmpty(t, answer.DeviceCode)
}

func TestDeviceRequestIsFoundByItsUserCodeAndNothingElse(t *testing.T) {
	h := newOAuthHarness(t, "")
	client := deviceClientFixture(t, h, 34, grantDeviceCode)
	answer, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
	require.NoError(t, err)

	request, err := h.service.LookupDeviceRequest(t.Context(), guid.ID(7), answer.UserCode)
	require.NoError(t, err)
	assert.Equal(t, client.ID, request.ClientID)
	assert.Equal(t, "Television", request.ClientName, "the person sees what they are approving")
	assert.Equal(t, "openid", request.Scope)
	assert.Equal(t, DeviceStatusPending, request.Status)

	// The same code typed loosely still resolves.
	loose, err := h.service.LookupDeviceRequest(t.Context(), guid.ID(7), strings.ToLower(strings.ReplaceAll(answer.UserCode, "-", " ")))
	require.NoError(t, err)
	assert.Equal(t, request.ClientID, loose.ClientID)

	// Everything unusable is reported the same way, so nobody can probe for live
	// codes by reading the difference.
	_, err = h.service.LookupDeviceRequest(t.Context(), guid.ID(7), "ACDE-FGHJ")
	require.ErrorIs(t, err, ErrDeviceRequestNotFound)
	_, err = h.service.LookupDeviceRequest(t.Context(), guid.ID(7), "")
	require.ErrorIs(t, err, ErrDeviceRequestNotFound)
}

func TestDeviceRequestDisappearsOnceItExpires(t *testing.T) {
	h := newOAuthHarness(t, "")
	client := deviceClientFixture(t, h, 35, grantDeviceCode)
	answer, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
	require.NoError(t, err)

	h.service.now = func() time.Time { return oauthTestNow.Add(time.Hour) }
	_, err = h.service.LookupDeviceRequest(t.Context(), guid.ID(7), answer.UserCode)
	require.ErrorIs(t, err, ErrDeviceRequestNotFound, "an expired request is not approvable")
}

func TestDecidingADeviceRequestHappensOnce(t *testing.T) {
	// Whoever approves first is who the token belongs to. A second decision must
	// change nothing, or a later approver could take over a device somebody else
	// already authorized.
	h := newOAuthHarness(t, "")
	client := deviceClientFixture(t, h, 36, grantDeviceCode)
	answer, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
	require.NoError(t, err)

	require.NoError(t, h.service.DecideDeviceRequest(t.Context(), guid.ID(7), answer.UserCode, guid.ID(101), true, oauthTestNow, 1, "pwd"))

	err = h.service.DecideDeviceRequest(t.Context(), guid.ID(7), answer.UserCode, guid.ID(202), false, oauthTestNow, 1, "pwd")
	require.ErrorIs(t, err, ErrDeviceRequestNotFound, "it was already decided")

	var row model.DeviceAuthorization
	require.NoError(t, h.db.Table(h.table(row.TableName())).Take(&row).Error)
	assert.Equal(t, DeviceStatusApproved, row.Status)
	require.NotNil(t, row.PrincipalID)
	assert.Equal(t, guid.ID(101), *row.PrincipalID, "the first approver owns the decision")
}

func TestDecidingRequiresAPrincipalAndARealCode(t *testing.T) {
	h := newOAuthHarness(t, "")
	client := deviceClientFixture(t, h, 37, grantDeviceCode)
	answer, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
	require.NoError(t, err)

	require.ErrorIs(t, h.service.DecideDeviceRequest(t.Context(), guid.ID(7), answer.UserCode, 0, true, oauthTestNow, 1, "pwd"), ErrDeviceRequestNotFound,
		"a decision with nobody behind it would issue a token for nobody")
	require.ErrorIs(t, h.service.DecideDeviceRequest(t.Context(), guid.ID(7), "", guid.ID(101), true, oauthTestNow, 1, "pwd"), ErrDeviceRequestNotFound)
	require.ErrorIs(t, h.service.DecideDeviceRequest(t.Context(), guid.ID(7), "ACDE-FGHJ", guid.ID(101), true, oauthTestNow, 1, "pwd"), ErrDeviceRequestNotFound)
}

// deviceTokenForm builds the token request a polling device sends.
func deviceTokenForm(client model.Client, deviceCode string) url.Values {
	return url.Values{
		"grant_type": {grantDeviceCode}, "client_id": {client.ID.String()},
		"device_code": {deviceCode},
	}
}

func TestDeviceTokenReportsWhatTheRequestIsWaitingFor(t *testing.T) {
	// RFC 8628's codes are what tell a device to keep waiting rather than give
	// up. Collapsing them would make pending indistinguishable from refused.
	h := newOAuthHarness(t, "")
	client := deviceClientFixture(t, h, 38, grantDeviceCode)
	answer, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
	require.NoError(t, err)

	_, err = h.service.Token(t.Context(), deviceTokenForm(client, answer.DeviceCode), "")
	require.ErrorIs(t, err, ErrAuthorizationPending)

	// Polling again immediately is too soon: the interval is enforced, not
	// trusted, so a device cannot turn this into a guessing loop.
	_, err = h.service.Token(t.Context(), deviceTokenForm(client, answer.DeviceCode), "")
	require.ErrorIs(t, err, ErrSlowDown)

	// After the interval it is pending again rather than slowed down.
	h.service.now = func() time.Time { return oauthTestNow.Add(time.Minute) }
	_, err = h.service.Token(t.Context(), deviceTokenForm(client, answer.DeviceCode), "")
	require.ErrorIs(t, err, ErrAuthorizationPending)
}

func TestDeviceTokenReportsARefusalAndAnExpiry(t *testing.T) {
	h := newOAuthHarness(t, "")
	client := deviceClientFixture(t, h, 39, grantDeviceCode)

	denied, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
	require.NoError(t, err)
	require.NoError(t, h.service.DecideDeviceRequest(t.Context(), guid.ID(7), denied.UserCode, guid.ID(101), false, oauthTestNow, 1, "pwd"))
	_, err = h.service.Token(t.Context(), deviceTokenForm(client, denied.DeviceCode), "")
	require.ErrorIs(t, err, ErrAccessDenied)

	stale, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
	require.NoError(t, err)
	h.service.now = func() time.Time { return oauthTestNow.Add(time.Hour) }
	_, err = h.service.Token(t.Context(), deviceTokenForm(client, stale.DeviceCode), "")
	require.ErrorIs(t, err, ErrExpiredToken)
}

func TestDeviceTokenIsIssuedOnceForWhoeverApproved(t *testing.T) {
	h := newOAuthHarness(t, "sl_")
	client := deviceClientFixture(t, h, 40, grantDeviceCode+" refresh_token")
	answer, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
	require.NoError(t, err)
	require.NoError(t, h.service.DecideDeviceRequest(t.Context(), guid.ID(7), answer.UserCode, guid.ID(101), true, oauthTestNow, 2, "pwd mfa"))

	h.service.now = func() time.Time { return oauthTestNow.Add(time.Minute) }
	response, err := h.service.Token(t.Context(), deviceTokenForm(client, answer.DeviceCode), "")
	require.NoError(t, err)
	assert.NotEmpty(t, response.AccessToken)
	assert.Equal(t, "Bearer", response.TokenType)
	assert.Equal(t, "openid", response.Scope)
	assert.NotEmpty(t, response.RefreshToken, "the client declared refresh_token")

	// The token belongs to the approver and carries the assurance they had, which
	// is read back out of the token itself rather than from what was passed in.
	issued := h.web.claimsFor(response.AccessToken)
	require.NotNil(t, issued)
	assert.Equal(t, guid.ID(101), issued.PrincipalID, "the token is the approver's, not the poller's")
	assert.Equal(t, 2, issued.ACR, "a device token cannot claim more than the approval did")
	assert.Equal(t, []string{"pwd", "mfa"}, issued.AMR)
	assert.Equal(t, client.ID.String(), issued.ClientID)

	// A second poll on a spent code gets nothing, so two devices holding one code
	// cannot both be issued a token.
	h.service.now = func() time.Time { return oauthTestNow.Add(2 * time.Minute) }
	_, err = h.service.Token(t.Context(), deviceTokenForm(client, answer.DeviceCode), "")
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestDeviceTokenRefusesAnotherClientsCode(t *testing.T) {
	// A device code is bound to the client that started the request. Admitting
	// another client's code would let one client collect another's token.
	h := newOAuthHarness(t, "")
	owner := deviceClientFixture(t, h, 41, grantDeviceCode)
	other := deviceClientFixture(t, h, 42, grantDeviceCode)
	answer, err := h.service.StartDeviceAuthorization(t.Context(), owner.ID.String(), "openid")
	require.NoError(t, err)
	require.NoError(t, h.service.DecideDeviceRequest(t.Context(), guid.ID(7), answer.UserCode, guid.ID(101), true, oauthTestNow, 1, "pwd"))

	_, err = h.service.Token(t.Context(), deviceTokenForm(other, answer.DeviceCode), "")
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestDeviceTokenRefusesWhatIsNotADeviceCode(t *testing.T) {
	h := newOAuthHarness(t, "")
	client := deviceClientFixture(t, h, 43, grantDeviceCode)

	_, err := h.service.Token(t.Context(), deviceTokenForm(client, ""), "")
	require.ErrorIs(t, err, ErrInvalidRequest)

	_, err = h.service.Token(t.Context(), deviceTokenForm(client, "never-issued"), "")
	require.ErrorIs(t, err, ErrInvalidRequest)

	// A client that never declared the grant cannot redeem one either.
	noGrant := deviceClientFixture(t, h, 44, "authorization_code")
	_, err = h.service.Token(t.Context(), deviceTokenForm(noGrant, "anything"), "")
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestDeviceConfigDefaultsKeepTheFlowUsable(t *testing.T) {
	// A zero config must still expire codes and pace polling; zero values would
	// mean a code that never expires and a device told to poll continuously.
	empty := DeviceConfig{}
	assert.Positive(t, empty.interval())
	assert.Positive(t, empty.lifetime())

	set := DeviceConfig{PollInterval: 30 * time.Second, CodeTTL: time.Hour}
	assert.Equal(t, 30, set.interval())
	assert.Equal(t, time.Hour, set.lifetime())
}

// TestDeviceAndExchangeStorageFaultsAreNotReportedAsRefusals holds the two new
// grants to the contract the rest of the module already keeps: a missing row is
// a refusal, a failed read is not. The distinction is what a client acts on --
// told invalid_grant it discards its device code and starts over, told
// not_permitted a tenant reads a delegation as withdrawn.
func TestDeviceAndExchangeStorageFaultsAreNotReportedAsRefusals(t *testing.T) {
	type scenario func(t *testing.T, h *oauthHarness) error
	start := func(t *testing.T, h *oauthHarness) (model.Client, DeviceAuthorization) {
		t.Helper()
		client := deviceClientFixture(t, h, 81, grantDeviceCode)
		answer, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
		require.NoError(t, err)
		return client, answer
	}
	permit := func(t *testing.T, h *oauthHarness) (guid.ID, string) {
		t.Helper()
		id, secret := exchangeClient(t, h, grantTokenExchange, "openid")
		_, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
			ClientID: id, SubjectPrefix: "101", Scope: "openid",
		})
		require.NoError(t, err)
		return id, secret
	}

	for name, run := range map[string]scenario{
		"device start client": func(t *testing.T, h *oauthHarness) error {
			client := deviceClientFixture(t, h, 82, grantDeviceCode)
			failReads(t, h, "iam_oauth_clients")
			_, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
			return err
		},
		"device lookup": func(t *testing.T, h *oauthHarness) error {
			_, answer := start(t, h)
			failReads(t, h, "iam_oauth_device_authorizations")
			_, err := h.service.LookupDeviceRequest(t.Context(), guid.ID(7), answer.UserCode)
			return err
		},
		"device lookup client": func(t *testing.T, h *oauthHarness) error {
			_, answer := start(t, h)
			failReads(t, h, "iam_oauth_clients")
			_, err := h.service.LookupDeviceRequest(t.Context(), guid.ID(7), answer.UserCode)
			return err
		},
		"device poll": func(t *testing.T, h *oauthHarness) error {
			client, answer := start(t, h)
			failReads(t, h, "iam_oauth_device_authorizations")
			_, err := h.service.Token(t.Context(), url.Values{
				"grant_type": {grantDeviceCode}, "client_id": {client.ID.String()},
				"device_code": {answer.DeviceCode},
			}, "")
			return err
		},
		"exchange grant read": func(t *testing.T, h *oauthHarness) error {
			id, secret := permit(t, h)
			token := subjectToken(t, h, guid.ID(101))
			failReads(t, h, "iam_oauth_exchange_grants")
			_, err := h.service.Token(t.Context(), exchangeForm(id, secret, token, "openid"), "")
			return err
		},
		"exchange grant list": func(t *testing.T, h *oauthHarness) error {
			permit(t, h)
			failReads(t, h, "iam_oauth_exchange_grants")
			_, err := h.service.ListExchangeGrants(t.Context(), guid.ID(7))
			return err
		},
		"exchange grant create": func(t *testing.T, h *oauthHarness) error {
			id, _ := exchangeClient(t, h, grantTokenExchange, "openid")
			failReads(t, h, "iam_oauth_clients")
			_, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
				ClientID: id, SubjectPrefix: "101", Scope: "openid",
			})
			return err
		},
		"exchange grant owner": func(t *testing.T, h *oauthHarness) error {
			id, _ := exchangeClient(t, h, grantTokenExchange, "openid")
			grant, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
				ClientID: id, SubjectPrefix: "101", Scope: "openid",
			})
			require.NoError(t, err)
			failReads(t, h, "iam_oauth_exchange_grants")
			_, _, err = h.service.resolveExchangeGrantOwner(t.Context(), grant.ID)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := run(t, newOAuthHarness(t, "sl_"))
			require.Error(t, err)
			for _, refusal := range []error{
				ErrInvalidRequest, errInvalidClient, ErrDeviceRequestNotFound,
				ErrExchangeNotPermitted, ErrInvalidExchangeGrant, ErrExchangeGrantMissing,
			} {
				assert.NotErrorIs(t, err, refusal)
			}
		})
	}
}

func TestADeviceRequestCanOnlyBeReadAndApprovedInItsOwnTenant(t *testing.T) {
	// The tenant in the path decides who was admitted; it has to decide what they
	// were admitted to as well. Otherwise somebody holding a user code could spend
	// another tenant's request -- no token would be issued, because the approver
	// is not a member there, but the request would be gone and the person it was
	// shown to could no longer approve it.
	h := newOAuthHarness(t, "sl_")
	client := deviceClientFixture(t, h, 91, grantDeviceCode)
	answer, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
	require.NoError(t, err)

	_, err = h.service.LookupDeviceRequest(t.Context(), guid.ID(8), answer.UserCode)
	require.ErrorIs(t, err, ErrDeviceRequestNotFound, "another tenant cannot read it")
	_, err = h.service.LookupDeviceRequest(t.Context(), 0, answer.UserCode)
	require.ErrorIs(t, err, ErrDeviceRequestNotFound, "nor can a request with no tenant at all")

	require.ErrorIs(t,
		h.service.DecideDeviceRequest(t.Context(), guid.ID(8), answer.UserCode, guid.ID(108), true, oauthTestNow, 1, "pwd"),
		ErrDeviceRequestNotFound, "another tenant cannot spend it")
	require.ErrorIs(t,
		h.service.DecideDeviceRequest(t.Context(), 0, answer.UserCode, guid.ID(101), true, oauthTestNow, 1, "pwd"),
		ErrDeviceRequestNotFound)

	// It is still there for the tenant it belongs to.
	request, err := h.service.LookupDeviceRequest(t.Context(), guid.ID(7), answer.UserCode)
	require.NoError(t, err)
	assert.Equal(t, DeviceStatusPending, request.Status)
	require.NoError(t,
		h.service.DecideDeviceRequest(t.Context(), guid.ID(7), answer.UserCode, guid.ID(101), true, oauthTestNow, 1, "pwd"))
}

func TestDeviceFlowRefusesWithNoVerificationPageConfigured(t *testing.T) {
	// RFC 8628 requires the answer to name the page a person goes to. Handing a
	// device an empty one would look like a working flow that nobody can finish,
	// so it is reported as the configuration fault it is -- not as a refusal the
	// device could act on.
	h := newOAuthHarness(t, "")
	h.service.device = DeviceConfig{}
	client := deviceClientFixture(t, h, 92, grantDeviceCode)

	_, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
	require.ErrorIs(t, err, ErrDeviceVerificationURIUnset)
	assert.NotErrorIs(t, err, errInvalidClient)
	assert.NotErrorIs(t, err, ErrInvalidRequest)

	// Nothing was written, so a later request with the page configured is the
	// first one.
	var count int64
	require.NoError(t, h.db.Table(h.table(model.DeviceAuthorization{}.TableName())).Count(&count).Error)
	assert.Zero(t, count)
}

func TestTheCompleteVerificationURIEscapesTheCode(t *testing.T) {
	h := newOAuthHarness(t, "")
	client := deviceClientFixture(t, h, 93, grantDeviceCode)
	answer, err := h.service.StartDeviceAuthorization(t.Context(), client.ID.String(), "openid")
	require.NoError(t, err)

	parsed, err := url.Parse(answer.VerificationURIComplete)
	require.NoError(t, err)
	assert.Equal(t, answer.UserCode, parsed.Query().Get("user_code"),
		"the code survives the round trip through the URI")
	assert.True(t, strings.HasPrefix(answer.VerificationURIComplete, answer.VerificationURI))
}
