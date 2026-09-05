package authn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newReturnURLAllowlist builds the allowlist a realistic deployment configures:
// one entry per frontend root, plus one narrower entry to show a deployment may
// still scope a return down to a subtree.
func newReturnURLAllowlist(t *testing.T) returnURLAllowlist {
	t.Helper()
	allowlist, err := parseReturnURLAllowlist([]string{
		"https://app.example.test/",
		"  https://console.example.test/admin  ",
		"",
	})
	require.NoError(t, err)
	require.Len(t, allowlist, 2, "a blank entry configures nothing and is not an error")
	return allowlist
}

// TestReturnURLAllowlistAdmitsWhereverSomebodyWasHeading is the reason the
// allowlist is a prefix at all. The login page sends back the route the person
// was going to, so a product has as many return URLs as it has routes, and an
// exact list of them is not something an operator can keep correct.
func TestReturnURLAllowlistAdmitsWhereverSomebodyWasHeading(t *testing.T) {
	allowlist := newReturnURLAllowlist(t)

	for name, candidate := range map[string]string{
		"the configured root":       "https://app.example.test/",
		"a root with no path":       "https://app.example.test",
		"a deep link":               "https://app.example.test/tenants/42/members",
		"a deep link with a query":  "https://app.example.test/tenants/42/members?page=2&q=a%20b",
		"a deep link with a hash":   "https://app.example.test/settings#security",
		"an uppercase host":         "https://APP.EXAMPLE.TEST/settings",
		"an uppercase scheme":       "HTTPS://app.example.test/settings",
		"the scoped entry itself":   "https://console.example.test/admin",
		"a route under that scope":  "https://console.example.test/admin/audit",
		"a redundant path segment":  "https://console.example.test/admin/./audit",
		"a path that climbs inward": "https://console.example.test/admin/reports/../audit",
	} {
		t.Run(name, func(t *testing.T) {
			assert.True(t, allowlist.allows(candidate))
		})
	}
}

// TestReturnURLAllowlistRefusesEveryWayOutOfIt covers the half that makes this
// an open-redirect guard. The path is widened; the origin never is, and no
// spelling of a path may leave the subtree it was admitted under.
func TestReturnURLAllowlistRefusesEveryWayOutOfIt(t *testing.T) {
	allowlist := newReturnURLAllowlist(t)

	for name, candidate := range map[string]string{
		"another host entirely":                      "https://evil.test/home",
		"a host that merely ends in the allowed one": "https://app.example.test.evil.test/home",
		"a host the allowed one is a prefix of":      "https://app.example.testing/home",
		"a subdomain of the allowed host":            "https://tenant.app.example.test/home",
		"another port on the allowed host":           "https://app.example.test:8443/home",
		"the allowed host over plain http":           "http://app.example.test/home",
		"credentials naming the allowed host":        "https://app.example.test@evil.test/home",
		"a sibling of the scoped path":               "https://console.example.test/admin-export",
		"a climb out of the scoped path":             "https://console.example.test/admin/../secrets",
		"a scheme a browser would not navigate":      "javascript:alert(1)",
		"a protocol-relative URL":                    "//evil.test/home",
		"a bare path":                                "/home",
		"nothing at all":                             "",
		"an unparseable URL":                         "https://app.example.test/%zz",
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, allowlist.allows(candidate))
		})
	}
}

// TestReturnURLAllowlistTrustsNothingWhenItWasGivenNothing keeps an unconfigured
// deployment closed rather than open.
func TestReturnURLAllowlistTrustsNothingWhenItWasGivenNothing(t *testing.T) {
	empty, err := parseReturnURLAllowlist(nil)
	require.NoError(t, err)
	assert.False(t, empty.allows("https://app.example.test/"))
}

// TestReturnURLAllowlistRefusesEntriesThatCannotMeanWhatTheyLookLike fails the
// deployment at startup instead of at the first login, which is the only point
// where an operator is still watching.
func TestReturnURLAllowlistRefusesEntriesThatCannotMeanWhatTheyLookLike(t *testing.T) {
	for name, entry := range map[string]string{
		"a bare path":                           "/home",
		"a host with no scheme":                 "app.example.test/home",
		"a scheme a browser would not navigate": "javascript:alert(1)",
		"a mail address":                        "mailto:nobody@example.test",
		"credentials":                           "https://user:pass@app.example.test/",
		"a query that constrains nothing":       "https://app.example.test/?tab=1",
		"a fragment that constrains nothing":    "https://app.example.test/#top",
		"an unparseable URL":                    "https://app.example.test/%zz",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseReturnURLAllowlist([]string{entry})
			require.Error(t, err)
			assert.Contains(t, err.Error(), entry,
				"the operator has to be told which entry to fix")
		})
	}
}
