// Package httpx builds outbound HTTP clients for URLs someone else chooses.
//
// A tenant that can name an OIDC issuer or a SCIM endpoint can otherwise aim
// this process at the network it runs in: the cloud metadata service on the
// link-local address, private ranges, or the loopback interface where
// administrative endpoints tend to listen. The request goes out with this
// process's network position and whatever credentials it was given, so the
// tenant reads a reply it could never have fetched itself.
//
// The guard therefore runs when the socket is about to connect rather than when
// the URL is stored. A host that resolves to a public address while it is being
// saved can resolve to a private one a second later, so neither parsing the
// string nor a lookup at save time closes the window -- only judging the address
// the connection is actually about to use does. Because every redirect hop dials
// again, the same check covers a chain that starts in public and ends inside.
package httpx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// blockedPrefixes are the ranges an outbound request must not reach. Go's
// address predicates cover most of this, but not carrier-grade NAT, the
// benchmarking range, or the reserved space above 240/4, and a security control
// reads better as the list it actually enforces than as a set of helper calls a
// reviewer has to expand from memory.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // this network
	netip.MustParsePrefix("10.0.0.0/8"),      // RFC 1918
	netip.MustParsePrefix("100.64.0.0/10"),   // RFC 6598 carrier-grade NAT
	netip.MustParsePrefix("127.0.0.0/8"),     // loopback
	netip.MustParsePrefix("169.254.0.0/16"),  // link-local, holds the cloud metadata address
	netip.MustParsePrefix("172.16.0.0/12"),   // RFC 1918
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("192.168.0.0/16"),  // RFC 1918
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("224.0.0.0/4"),     // multicast
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, includes the broadcast address
	netip.MustParsePrefix("::/128"),          // unspecified
	netip.MustParsePrefix("::1/128"),         // loopback
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("100::/64"),        // discard-only
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("fc00::/7"),        // unique local
	netip.MustParsePrefix("fe80::/10"),       // link-local
	netip.MustParsePrefix("ff00::/8"),        // multicast
}

// DefaultTimeout is the round trip budget used when a caller passes none.
const DefaultTimeout = 10 * time.Second

// maxRedirects bounds a redirect chain. Every hop is dialed, so each one is
// checked; the cap exists so a loop between two public hosts cannot hold the
// request open for the whole timeout.
const maxRedirects = 5

// Policy decides which outbound addresses a client may reach. Its zero value is
// the strict one -- plaintext refused, every reserved range blocked -- so a
// caller that forgets to configure it gets the safe behavior rather than an
// open one.
type Policy struct {
	// AllowPlaintext permits http:// targets. Deployments that terminate TLS at
	// a sidecar need it; leaving it false keeps credentials off the wire.
	AllowPlaintext bool

	// Allow lists prefixes this deployment permits even though they are
	// otherwise blocked. It exists for the identity provider that genuinely
	// lives on the internal network, and it is deployment configuration rather
	// than tenant input for the obvious reason.
	Allow []netip.Prefix
}

// ValidateURL reports whether a stored target is acceptable on its face. It is
// a courtesy to whoever is typing the URL in, not the enforcement point: the
// address behind a name can change after this returns, so the connection is
// still judged when it is made.
func (p Policy) ValidateURL(raw string) error {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("parse outbound url: %w", err)
	}
	switch target.Scheme {
	case "https":
	case "http":
		if !p.AllowPlaintext {
			return fmt.Errorf("outbound url %q must use https", raw)
		}
	default:
		return fmt.Errorf("outbound url %q must use http or https", raw)
	}
	host := target.Hostname()
	if host == "" {
		return fmt.Errorf("outbound url %q has no host", raw)
	}
	// A literal address can be judged now. A name cannot, and deliberately is
	// not resolved here: a lookup at this point would report on an answer the
	// connection is not obliged to reuse.
	if addr, addrErr := netip.ParseAddr(host); addrErr == nil {
		return p.permits(addr)
	}
	return nil
}

// permits reports whether a resolved address may be dialed.
func (p Policy) permits(addr netip.Addr) error {
	// An IPv4 address written as ::ffff:10.0.0.1 has to be judged as 10.0.0.1,
	// or the v4 rules can be sidestepped by spelling the address differently.
	addr = addr.Unmap()
	if !addr.IsValid() {
		return fmt.Errorf("outbound address is not a valid ip")
	}
	for _, allowed := range p.Allow {
		if allowed.Contains(addr) {
			return nil
		}
	}
	for _, blocked := range blockedPrefixes {
		if blocked.Contains(addr) {
			return fmt.Errorf("outbound address %s is in the reserved range %s", addr, blocked)
		}
	}
	if !addr.IsGlobalUnicast() {
		return fmt.Errorf("outbound address %s is not global unicast", addr)
	}
	return nil
}

// control is the dial hook. It runs after the name has been resolved and before
// the socket connects, which is the only moment at which the address being used
// and the address that was checked are guaranteed to be the same one.
func (p Policy) control(network, address string, _ syscall.RawConn) error {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return fmt.Errorf("outbound network %q is not permitted", network)
	}
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("parse outbound address %q: %w", address, err)
	}
	return p.permits(addrPort.Addr())
}

// NewClient returns a client that refuses the addresses Policy blocks. A
// timeout of zero means DefaultTimeout.
func NewClient(timeout time.Duration, policy Policy) *http.Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second, Control: policy.control}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
			// No proxy, and not the environment's either. A proxied request
			// dials the proxy, so the address this policy inspects would be the
			// proxy's rather than the target's and every rule above would pass
			// while the proxy fetched whatever it was asked for.
			Proxy:                 nil,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   timeout,
			ExpectContinueTimeout: time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("outbound request stopped after %d redirects", maxRedirects)
			}
			if req.URL.Scheme == "http" && !policy.AllowPlaintext {
				return fmt.Errorf("outbound redirect to %q must use https", req.URL.Redacted())
			}
			return nil
		},
	}
}

// DialContext exposes the guarded dialer for callers that speak something other
// than HTTP over it. It is the same check NewClient installs.
func (p Policy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: DefaultTimeout, Control: p.control}
	return dialer.DialContext(ctx, network, address)
}

// ParsePolicy builds a Policy from deployment configuration. Networks are CIDR
// blocks; a malformed one is refused rather than skipped, because silently
// dropping an entry would leave an operator believing an address is reachable
// when the guard still blocks it.
func ParsePolicy(allowPlaintext bool, networks []string) (Policy, error) {
	policy := Policy{AllowPlaintext: allowPlaintext}
	for _, entry := range networks {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(trimmed)
		if err != nil {
			return Policy{}, fmt.Errorf("parse outbound allow network %q: %w", entry, err)
		}
		policy.Allow = append(policy.Allow, prefix.Masked())
	}
	return policy, nil
}
