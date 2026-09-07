package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyPermitsRejectsReservedAddresses(t *testing.T) {
	policy := Policy{}
	blocked := map[string]string{
		"loopback":             "127.0.0.1",
		"loopback high":        "127.255.255.254",
		"unspecified":          "0.0.0.0",
		"private 10":           "10.0.0.1",
		"private 172":          "172.16.0.1",
		"private 192":          "192.168.1.1",
		"carrier grade nat":    "100.64.0.1",
		"link local":           "169.254.1.1",
		"cloud metadata":       "169.254.169.254",
		"benchmarking":         "198.18.0.1",
		"documentation":        "192.0.2.1",
		"multicast":            "224.0.0.1",
		"reserved":             "240.0.0.1",
		"broadcast":            "255.255.255.255",
		"ipv6 loopback":        "::1",
		"ipv6 unspecified":     "::",
		"ipv6 unique local":    "fd00::1",
		"ipv6 link local":      "fe80::1",
		"ipv6 multicast":       "ff02::1",
		"ipv6 documentation":   "2001:db8::1",
		"ipv4 mapped loopback": "::ffff:127.0.0.1",
		"ipv4 mapped private":  "::ffff:10.0.0.1",
		"ipv4 mapped metadata": "::ffff:169.254.169.254",
	}
	for name, literal := range blocked {
		t.Run(name, func(t *testing.T) {
			addr, err := netip.ParseAddr(literal)
			require.NoError(t, err)
			assert.Error(t, policy.permits(addr), "%s must not be reachable", literal)
		})
	}
}

func TestPolicyPermitsAllowsPublicAddresses(t *testing.T) {
	policy := Policy{}
	for _, literal := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"} {
		addr, err := netip.ParseAddr(literal)
		require.NoError(t, err)
		assert.NoError(t, policy.permits(addr), "%s must be reachable", literal)
	}
}

func TestPolicyAllowListOverridesBlockedRange(t *testing.T) {
	policy := Policy{Allow: []netip.Prefix{netip.MustParsePrefix("10.10.0.0/16")}}

	permitted, err := netip.ParseAddr("10.10.0.5")
	require.NoError(t, err)
	assert.NoError(t, policy.permits(permitted), "an allow-listed internal provider must be reachable")

	// The allow list names one range, not the whole private space.
	neighbor, err := netip.ParseAddr("10.11.0.5")
	require.NoError(t, err)
	assert.Error(t, policy.permits(neighbor))

	elsewhere, err := netip.ParseAddr("127.0.0.1")
	require.NoError(t, err)
	assert.Error(t, policy.permits(elsewhere))
}

func TestPolicyValidateURLScheme(t *testing.T) {
	strict := Policy{}
	assert.NoError(t, strict.ValidateURL("https://idp.example.com/.well-known/openid-configuration"))
	assert.ErrorContains(t, strict.ValidateURL("http://idp.example.com"), "must use https")
	assert.ErrorContains(t, strict.ValidateURL("file:///etc/passwd"), "must use http or https")
	assert.ErrorContains(t, strict.ValidateURL("gopher://idp.example.com"), "must use http or https")
	assert.ErrorContains(t, strict.ValidateURL("https://"), "no host")

	relaxed := Policy{AllowPlaintext: true}
	assert.NoError(t, relaxed.ValidateURL("http://idp.internal"))
}

func TestPolicyValidateURLJudgesLiteralAddresses(t *testing.T) {
	policy := Policy{}
	assert.ErrorContains(t, policy.ValidateURL("https://169.254.169.254/latest/meta-data/"), "reserved range")
	assert.ErrorContains(t, policy.ValidateURL("https://127.0.0.1:8080/token"), "reserved range")
	assert.ErrorContains(t, policy.ValidateURL("https://[::1]/token"), "reserved range")
	assert.NoError(t, policy.ValidateURL("https://93.184.216.34/token"))

	// A name is left alone here on purpose: whatever it resolves to now is not
	// binding on the connection, so the dial guard is what has to catch it.
	assert.NoError(t, policy.ValidateURL("https://localhost/token"))
}

func TestNewClientRefusesLoopbackServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// httptest listens on loopback, so a strict policy must refuse to reach it.
	// This is the assertion that proves the guard runs on the real dial rather
	// than only inside permits.
	response, err := NewClient(0, Policy{}).Get(server.URL)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	require.Error(t, err)
	assert.ErrorContains(t, err, "reserved range")
}

func TestNewClientReachesAllowListedLoopbackServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()

	policy := Policy{Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}
	response, err := NewClient(0, policy).Get(server.URL)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusTeapot, response.StatusCode)
}

func TestNewClientRefusesRedirectToBlockedAddress(t *testing.T) {
	// The host being reached is permitted; it answers with a redirect to the
	// cloud metadata address. This is the chain a check performed only on the
	// stored URL lets through, and the reason the guard has to sit on the dial:
	// the second hop is never a URL anybody configured.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer redirector.Close()

	// Plaintext is permitted so that the address guard is the only rule left
	// that can refuse the second hop; the scheme rule is covered separately.
	policy := Policy{AllowPlaintext: true, Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}
	response, err := NewClient(0, policy).Get(redirector.URL)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	require.Error(t, err)
	assert.ErrorContains(t, err, "reserved range")
}

func TestNewClientRefusesPlaintextRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	policy := Policy{Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}
	response, err := NewClient(0, policy).Get(redirector.URL)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	require.Error(t, err)
	assert.ErrorContains(t, err, "must use https")
}

func TestPolicyControlRejectsNonTCPNetworks(t *testing.T) {
	policy := Policy{}
	assert.ErrorContains(t, policy.control("udp", "93.184.216.34:53", nil), "not permitted")
	assert.NoError(t, policy.control("tcp4", "93.184.216.34:443", nil))
}

func TestPolicyDialContextRefusesBlockedAddress(t *testing.T) {
	conn, err := Policy{}.DialContext(t.Context(), "tcp", "127.0.0.1:1")
	if conn != nil {
		defer func() { _ = conn.Close() }()
	}
	require.Error(t, err)
	assert.ErrorContains(t, err, "reserved range")
}

func TestNewClientStopsRedirectLoop(t *testing.T) {
	// A host that keeps redirecting to itself would otherwise hold the request
	// open for the whole timeout, and every hop passes the address check.
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL, http.StatusFound)
	}))
	defer server.Close()

	policy := Policy{AllowPlaintext: true, Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}
	response, err := NewClient(0, policy).Get(server.URL)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	require.Error(t, err)
	assert.ErrorContains(t, err, "stopped after")
}

func TestPolicyValidateURLRejectsUnparseableTarget(t *testing.T) {
	assert.ErrorContains(t, Policy{}.ValidateURL("https://[::1"), "parse outbound url")
}

func TestPolicyPermitsRejectsInvalidAddress(t *testing.T) {
	assert.ErrorContains(t, Policy{}.permits(netip.Addr{}), "not a valid ip")
}

func TestPolicyControlRejectsUnparseableAddress(t *testing.T) {
	assert.ErrorContains(t, Policy{}.control("tcp", "not-an-address", nil), "parse outbound address")
}

func TestParsePolicy(t *testing.T) {
	policy, err := ParsePolicy(true, []string{"10.10.0.0/16", "  ", "fd00::/8"})
	require.NoError(t, err)
	assert.True(t, policy.AllowPlaintext)
	require.Len(t, policy.Allow, 2, "a blank entry is skipped, a real one is not")

	permitted, err := netip.ParseAddr("10.10.0.5")
	require.NoError(t, err)
	assert.NoError(t, policy.permits(permitted))

	// A host address rather than a network still covers the range it names.
	masked, err := ParsePolicy(false, []string{"10.10.0.5/16"})
	require.NoError(t, err)
	assert.Equal(t, netip.MustParsePrefix("10.10.0.0/16"), masked.Allow[0])

	_, err = ParsePolicy(false, []string{"not-a-cidr"})
	assert.ErrorContains(t, err, "parse outbound allow network")

	empty, err := ParsePolicy(false, nil)
	require.NoError(t, err)
	assert.Equal(t, Policy{}, empty, "no configuration must mean the strict policy")
}
