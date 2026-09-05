package authn

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// returnURLRule is one configured allowlist entry, split into the two parts a
// candidate has to satisfy: an origin it must be identical to, and a path it
// must sit at or under.
//
// The path is a prefix rather than an exact value because the login page sends
// back wherever the person was heading, and a product has as many of those as
// it has routes. Enumerating every route in deployment configuration is not
// something an operator can keep correct, and the failure mode is a rejected
// login rather than a visible misconfiguration.
type returnURLRule struct {
	origin string
	path   string
}

// returnURLAllowlist decides where a login may send a browser afterwards. It is
// the open-redirect guard, so the origin is matched exactly: the path is
// widened, the host never is.
type returnURLAllowlist []returnURLRule

// parseReturnURLAllowlist turns configured entries into rules, refusing every
// entry that cannot mean what an operator would read it to mean. It runs at
// startup so a deployment that misconfigures the allowlist fails immediately
// rather than at the first login.
func parseReturnURLAllowlist(entries []string) (returnURLAllowlist, error) {
	allowlist := make(returnURLAllowlist, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parsed, err := url.Parse(entry)
		if err != nil {
			return nil, fmt.Errorf("authn web allowed return URL %q is not a URL: %w", entry, err)
		}
		origin, ok := returnURLOrigin(parsed)
		if !ok {
			return nil, fmt.Errorf(
				"authn web allowed return URL %q must be an absolute http or https URL with no credentials", entry,
			)
		}
		// A query or a fragment on an entry constrains nothing: the candidate
		// carries its own, and matching on them would reject every real deep
		// link. Refusing is honest about that; ignoring would leave an operator
		// believing the entry is narrower than it is.
		if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
			return nil, fmt.Errorf(
				"authn web allowed return URL %q must not carry a query or a fragment", entry,
			)
		}
		allowlist = append(allowlist, returnURLRule{origin: origin, path: cleanReturnURLPath(parsed.Path)})
	}
	return allowlist, nil
}

// allows reports whether candidate is a URL a login may redirect to. An empty
// allowlist allows nothing, so a deployment that configures none cannot be
// talked into redirecting anywhere.
func (a returnURLAllowlist) allows(candidate string) bool {
	parsed, err := url.Parse(strings.TrimSpace(candidate))
	if err != nil {
		return false
	}
	origin, ok := returnURLOrigin(parsed)
	if !ok {
		return false
	}
	// The path is cleaned before it is compared, so a candidate cannot climb out
	// of the prefix it was admitted under. A browser resolves the same segments
	// the same way, so what is matched here is what it will request.
	candidatePath := cleanReturnURLPath(parsed.Path)
	for _, rule := range a {
		if rule.origin == origin && pathIsUnder(rule.path, candidatePath) {
			return true
		}
	}
	return false
}

// returnURLOrigin reports the origin to compare a URL by, refusing every shape
// that would let the compared text differ from the host a browser goes to.
func returnURLOrigin(parsed *url.URL) (string, bool) {
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	if parsed.Host == "" || parsed.Opaque != "" {
		return "", false
	}
	// Credentials are what makes https://trusted.example@evil.example read as
	// the trusted host to a person and resolve to the other one. Host already
	// excludes them, but a URL carrying any has no legitimate use here.
	if parsed.User != nil {
		return "", false
	}
	return scheme + "://" + strings.ToLower(parsed.Host), true
}

// cleanReturnURLPath resolves a path to the one a request would actually reach,
// so a prefix comparison cannot be defeated by writing the same location a
// different way.
func cleanReturnURLPath(raw string) string {
	if raw == "" {
		return "/"
	}
	cleaned := path.Clean(raw)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

// pathIsUnder reports whether candidate is prefix itself or sits beneath it.
// The comparison stops at a segment boundary, so an entry for /admin admits
// /admin/users and not /admin-export.
func pathIsUnder(prefix, candidate string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		return true
	}
	return candidate == prefix || strings.HasPrefix(candidate, prefix+"/")
}
