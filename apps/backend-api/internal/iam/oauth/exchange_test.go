package oauth

import (
	"net/url"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exchangeClient registers a confidential client, which is what a service is:
// it authenticates with a secret before it may ask to act as anybody.
func exchangeClient(t *testing.T, h *oauthHarness, grants, scopes string) (guid.ID, string) {
	t.Helper()
	created, secret, err := h.service.CreateClient(
		t.Context(), 7, "Service", nil, strings.Fields(grants), strings.Fields(scopes), false, ConsentImplicit,
	)
	require.NoError(t, err)
	id, err := guid.Parse(created.ID)
	require.NoError(t, err)
	return id, secret
}

// subjectToken mints a token for a principal, which is what a service holds when
// it asks to act as that person.
func subjectToken(t *testing.T, h *oauthHarness, principalID guid.ID) string {
	t.Helper()
	token, _, err := h.web.IssueTenantAccessTokenWithAssurance(
		t.Context(), principalID, "7", "", "openid",
		authnext.Assurance{AuthTime: oauthTestNow, Level: 1, Methods: []string{"pwd"}},
	)
	require.NoError(t, err)
	return token
}

func exchangeForm(clientID guid.ID, secret, token, scope string) url.Values {
	form := url.Values{
		"grant_type": {grantTokenExchange}, "client_id": {clientID.String()},
		"client_secret": {secret}, "subject_token": {token},
	}
	if scope != "" {
		form.Set("scope", scope)
	}
	return form
}

func TestSubjectPermittedRefusesWhatWasNotWrittenDown(t *testing.T) {
	// An empty prefix covering everybody is the obvious reading and the dangerous
	// one: a half-written row would become blanket impersonation.
	assert.False(t, subjectPermitted("", "101"))
	assert.False(t, subjectPermitted("101", ""))

	assert.True(t, subjectPermitted("101", "101"))
	assert.False(t, subjectPermitted("101", "1010"), "an exact prefix matches exactly")

	assert.True(t, subjectPermitted("10*", "101"), "a star permits a range")
	assert.True(t, subjectPermitted("10*", "10"))
	assert.False(t, subjectPermitted("10*", "201"))
	assert.False(t, subjectPermitted("*", "anyone"), "a bare star permits nobody")
}

func TestExchangeGrantMustNameSomethingReachable(t *testing.T) {
	h := newOAuthHarness(t, "")
	client, _ := exchangeClient(t, h, grantTokenExchange, "openid profile")

	// A bare star would permit acting as anybody, which is what this table exists
	// to keep from being expressible by accident.
	_, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "*", Scope: "openid",
	})
	require.ErrorIs(t, err, ErrInvalidExchangeGrant)

	for _, broken := range []ExchangeGrantInput{
		{ClientID: client, SubjectPrefix: "", Scope: "openid"},
		{ClientID: client, SubjectPrefix: "101", Scope: ""},
		{ClientID: 0, SubjectPrefix: "101", Scope: "openid"},
		{ClientID: client, SubjectPrefix: "101", Scope: "openid", Status: "nonsense"},
	} {
		_, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), broken)
		require.ErrorIs(t, err, ErrInvalidExchangeGrant, "%+v", broken)
	}

	// A grant naming a client that does not exist would read as permission that
	// nothing could ever present.
	_, err = h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: guid.ID(999999), SubjectPrefix: "101", Scope: "openid",
	})
	require.ErrorIs(t, err, ErrInvalidExchangeGrant)
}

func TestExchangeGrantsAreListedAndWithdrawn(t *testing.T) {
	h := newOAuthHarness(t, "sl_")
	client, _ := exchangeClient(t, h, grantTokenExchange, "openid profile")

	created, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "101", Scope: "openid",
	})
	require.NoError(t, err)
	assert.Equal(t, ExchangeGrantActive, created.Status)

	// The same pair twice is a conflict rather than a second row, so there is one
	// answer to "what may this client do as that subject".
	_, err = h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "101", Scope: "profile",
	})
	require.ErrorIs(t, err, ErrExchangeGrantExists)

	items, err := h.service.ListExchangeGrants(t.Context(), guid.ID(7))
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "101", items[0].SubjectPrefix)

	require.NoError(t, h.service.DeleteExchangeGrant(t.Context(), guid.ID(7), created.ID))
	require.ErrorIs(t, h.service.DeleteExchangeGrant(t.Context(), guid.ID(7), created.ID), ErrExchangeGrantMissing)

	remaining, err := h.service.ListExchangeGrants(t.Context(), guid.ID(7))
	require.NoError(t, err)
	assert.Empty(t, remaining)
}

func TestExchangeRefusesAClientWithNoWrittenGrant(t *testing.T) {
	// Nothing is implied. A client with no row cannot exchange at all, which is
	// the whole reason the permission is a table and not a flag.
	h := newOAuthHarness(t, "")
	client, secret := exchangeClient(t, h, grantTokenExchange, "openid")
	token := subjectToken(t, h, guid.ID(101))

	_, err := h.service.Token(t.Context(), exchangeForm(client, secret, token, "openid"), "")
	require.ErrorIs(t, err, ErrExchangeNotPermitted)
}

func TestExchangeRefusesAClientThatNeverDeclaredTheGrant(t *testing.T) {
	h := newOAuthHarness(t, "")
	client, secret := exchangeClient(t, h, "client_credentials", "openid")
	_, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "101", Scope: "openid",
	})
	require.NoError(t, err)

	token := subjectToken(t, h, guid.ID(101))
	_, err = h.service.Token(t.Context(), exchangeForm(client, secret, token, "openid"), "")
	require.ErrorIs(t, err, ErrInvalidRequest, "a written grant does not substitute for declaring the grant type")
}

func TestExchangeIssuesForTheSubjectOfThePresentedToken(t *testing.T) {
	h := newOAuthHarness(t, "sl_")
	client, secret := exchangeClient(t, h, grantTokenExchange+" refresh_token", "openid profile")
	_, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "101", Scope: "openid",
	})
	require.NoError(t, err)

	response, err := h.service.Token(t.Context(), exchangeForm(client, secret, subjectToken(t, h, guid.ID(101)), "openid"), "")
	require.NoError(t, err)
	assert.NotEmpty(t, response.AccessToken)
	assert.Equal(t, tokenTypeAccess, response.IssuedTokenType, "RFC 8693 requires the type to be stated")
	assert.Equal(t, "openid", response.Scope)
	assert.Empty(t, response.RefreshToken,
		"a delegation must not outlive the authority it was derived from")

	issued := h.web.claimsFor(response.AccessToken)
	require.NotNil(t, issued)
	assert.Equal(t, guid.ID(101), issued.PrincipalID, "the token acts as the subject, not as the client")
	assert.Equal(t, client.String(), issued.ClientID, "an audit can tell a delegated call from a direct one")
	assert.Equal(t, 1, issued.ACR, "the assurance travels from the subject token")
}

func TestExchangeCannotWidenBeyondTheWrittenScopes(t *testing.T) {
	// A client permitted to act as somebody is not thereby permitted everything it
	// can do as itself: the scopes are the grant's.
	h := newOAuthHarness(t, "")
	client, secret := exchangeClient(t, h, grantTokenExchange, "openid profile")
	_, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "101", Scope: "openid",
	})
	require.NoError(t, err)
	token := subjectToken(t, h, guid.ID(101))

	_, err = h.service.Token(t.Context(), exchangeForm(client, secret, token, "openid profile"), "")
	require.ErrorIs(t, err, ErrExchangeNotPermitted, "profile is the client's but not the delegation's")

	// Asking for nothing takes the grant's scopes rather than the client's.
	response, err := h.service.Token(t.Context(), exchangeForm(client, secret, token, ""), "")
	require.NoError(t, err)
	assert.Equal(t, "openid", response.Scope)
}

func TestExchangeRefusesASubjectOutsideTheWrittenRange(t *testing.T) {
	h := newOAuthHarness(t, "")
	client, secret := exchangeClient(t, h, grantTokenExchange, "openid")
	_, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "202", Scope: "openid",
	})
	require.NoError(t, err)

	_, err = h.service.Token(t.Context(), exchangeForm(client, secret, subjectToken(t, h, guid.ID(101)), "openid"), "")
	require.ErrorIs(t, err, ErrExchangeNotPermitted)
}

func TestExchangeRefusesAnUnverifiableSubjectToken(t *testing.T) {
	// A client that could hand in any string would be choosing who it acts as, so
	// the subject token is verified rather than read.
	h := newOAuthHarness(t, "")
	client, secret := exchangeClient(t, h, grantTokenExchange, "openid")
	_, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "101", Scope: "openid",
	})
	require.NoError(t, err)

	_, err = h.service.Token(t.Context(), exchangeForm(client, secret, "not-a-token", "openid"), "")
	require.ErrorIs(t, err, errInvalidClient)

	_, err = h.service.Token(t.Context(), exchangeForm(client, secret, "", "openid"), "")
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestExchangeRefusesATokenTypeItCannotVerify(t *testing.T) {
	// Admitting a type this deployment cannot check would be accepting an
	// assertion from somewhere it does not trust.
	h := newOAuthHarness(t, "")
	client, secret := exchangeClient(t, h, grantTokenExchange, "openid")
	token := subjectToken(t, h, guid.ID(101))

	form := exchangeForm(client, secret, token, "openid")
	form.Set("subject_token_type", "urn:ietf:params:oauth:token-type:saml2")
	_, err := h.service.Token(t.Context(), form, "")
	require.ErrorIs(t, err, ErrUnsupportedTokenType)

	form = exchangeForm(client, secret, token, "openid")
	form.Set("requested_token_type", "urn:ietf:params:oauth:token-type:id_token")
	_, err = h.service.Token(t.Context(), form, "")
	require.ErrorIs(t, err, ErrUnsupportedTokenType)

	// Naming the supported type explicitly is accepted.
	form = exchangeForm(client, secret, token, "openid")
	form.Set("subject_token_type", tokenTypeAccess)
	form.Set("requested_token_type", tokenTypeAccess)
	_, err = h.service.Token(t.Context(), form, "")
	require.ErrorIs(t, err, ErrExchangeNotPermitted, "it got past the type check to the grant check")
}

func TestExchangePrefersAnExactGrantOverARange(t *testing.T) {
	// Narrowing what one principal may be acted as should not require removing
	// the range that covers the rest.
	h := newOAuthHarness(t, "")
	client, secret := exchangeClient(t, h, grantTokenExchange, "openid profile")
	_, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "10*", Scope: "openid profile",
	})
	require.NoError(t, err)
	_, err = h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "101", Scope: "openid",
	})
	require.NoError(t, err)

	// The exact grant wins, so the narrower scope applies to this principal.
	_, err = h.service.Token(t.Context(), exchangeForm(client, secret, subjectToken(t, h, guid.ID(101)), "openid profile"), "")
	require.ErrorIs(t, err, ErrExchangeNotPermitted)

	response, err := h.service.Token(t.Context(), exchangeForm(client, secret, subjectToken(t, h, guid.ID(101)), "openid"), "")
	require.NoError(t, err)
	assert.Equal(t, "openid", response.Scope)
}

func TestDisabledExchangeGrantPermitsNothing(t *testing.T) {
	h := newOAuthHarness(t, "")
	client, secret := exchangeClient(t, h, grantTokenExchange, "openid")
	_, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "101", Scope: "openid", Status: ExchangeGrantDisabled,
	})
	require.NoError(t, err)

	_, err = h.service.Token(t.Context(), exchangeForm(client, secret, subjectToken(t, h, guid.ID(101)), "openid"), "")
	require.ErrorIs(t, err, ErrExchangeNotPermitted, "disabling is how a delegation is suspended without losing it")
}

func TestExchangeGrantOwnerKeepsTenantsApart(t *testing.T) {
	h := newOAuthHarness(t, "")
	client, _ := exchangeClient(t, h, grantTokenExchange, "openid")
	created, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "101", Scope: "openid",
	})
	require.NoError(t, err)

	owner, found, err := h.service.resolveExchangeGrantOwner(t.Context(), created.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, guid.ID(7), owner.TenantID)

	_, found, err = h.service.resolveExchangeGrantOwner(t.Context(), guid.ID(999999))
	require.NoError(t, err, "an unknown id is not found rather than an error")
	assert.False(t, found)

	// Another tenant cannot withdraw it by its identifier.
	require.ErrorIs(t, h.service.DeleteExchangeGrant(t.Context(), guid.ID(8), created.ID), ErrExchangeGrantMissing)

}

func TestExchangeNeedsAClientThatCanProveWhoItIs(t *testing.T) {
	// A public client authenticates with nothing, so its identifier alone would be
	// enough to act as every principal a grant covers.
	h := newOAuthHarness(t, "")
	_, _, err := h.service.CreateClient(
		t.Context(), 7, "Television", nil, []string{grantTokenExchange}, []string{"openid"}, true, ConsentImplicit,
	)
	require.ErrorIs(t, err, ErrInvalidRequest, "management must not hand this grant to a secretless client")

	// The device grant is the opposite case: a television proves itself with the
	// code a person approved, so it is permitted without a secret.
	_, _, err = h.service.CreateClient(
		t.Context(), 7, "Television", nil, []string{grantDeviceCode}, []string{"openid"}, true, ConsentImplicit,
	)
	require.NoError(t, err)

	// A row written past that check is still refused where it is used.
	public := deviceClientFixture(t, h, 71, grantTokenExchange)
	_, err = h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: public.ID, SubjectPrefix: "101", Scope: "openid",
	})
	require.NoError(t, err)
	_, err = h.service.Token(t.Context(), exchangeForm(public.ID, "", subjectToken(t, h, guid.ID(101)), "openid"), "")
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestExchangeRefusesASubjectFromAnotherTenant(t *testing.T) {
	// Holding a token minted elsewhere must not become authority here, or a
	// client could exchange whatever token happens to reach it.
	h := newOAuthHarness(t, "")
	client, secret := exchangeClient(t, h, grantTokenExchange, "openid")
	_, err := h.service.CreateExchangeGrant(t.Context(), guid.ID(7), ExchangeGrantInput{
		ClientID: client, SubjectPrefix: "9*", Scope: "openid",
	})
	require.NoError(t, err)

	outsider := subjectToken(t, h, guid.ID(909))
	_, err = h.service.Token(t.Context(), exchangeForm(client, secret, outsider, "openid"), "")
	require.ErrorIs(t, err, ErrInvalidRequest, "a non-member is refused before any grant is read")
}
