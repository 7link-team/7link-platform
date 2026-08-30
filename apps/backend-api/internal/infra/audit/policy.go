package audit

import (
	"net/http"
	"strings"
)

// classifyHTTP maps an HTTP method to a read/write access class. GET and the
// other idempotent inspection methods are reads; everything else is a write.
func classifyHTTP(method string) Access {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return AccessRead
	default:
		return AccessWrite
	}
}

// readGRPCPrefixes are method-name prefixes treated as read-only. The heuristic
// mirrors common gRPC naming conventions and can be overridden per request with
// audit.Access.
var readGRPCPrefixes = []string{
	"Get", "List", "Query", "Search", "Watch", "Describe", "Check", "Count", "Next",
}

// classifyGRPC maps a full gRPC method ("/pkg.Service/Method") to a read/write
// access class based on the trailing method name.
func classifyGRPC(method string) Access {
	base := method
	if index := strings.LastIndex(base, "/"); index >= 0 {
		base = base[index+1:]
	}
	for _, prefix := range readGRPCPrefixes {
		if strings.HasPrefix(base, prefix) {
			return AccessRead
		}
	}
	return AccessWrite
}

// defaultSkipRoutes are request path prefixes that are never audited. Without
// this, Prometheus scraping, health probes, and documentation assets would flood
// the audit log. The list is a baseline; configuration can extend it.
var defaultSkipRoutes = []string{
	"/healthz",
	"/livez",
	"/readyz",
	"/metrics",
	"/debug",
	"/docs",
	"/openapi",
}

// isSkipped reports whether a request path should never be audited. Matching is
// segment-aware: a skip entry "/healthz" excludes "/healthz" and "/healthz/deep"
// but not "/healthzish", while the dotted suffix (".") covers derived endpoints
// such as "/openapi.json" next to "/openapi".
func isSkipped(path string, extra []string) bool {
	if path == "/" {
		return true
	}
	skips := defaultSkipRoutes
	if len(extra) > 0 {
		combined := make([]string, 0, len(skips)+len(extra))
		combined = append(combined, skips...)
		combined = append(combined, extra...)
		skips = combined
	}
	for _, prefix := range skips {
		if prefix == "" {
			continue
		}
		if path == prefix || strings.HasPrefix(path, prefix+"/") || strings.HasPrefix(path, prefix+".") {
			return true
		}
	}
	return false
}

// isStreamRequest detects long-lived HTTP streams: WebSocket upgrades and
// Server-Sent Events. These are audited as sessions (established/closed), not
// per-message, so their bodies are never captured.
func isStreamRequest(request *http.Request) bool {
	if strings.EqualFold(request.Header.Get("Connection"), "upgrade") &&
		strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
		return true
	}
	if strings.Contains(strings.ToLower(request.Header.Get("Accept")), "text/event-stream") {
		return true
	}
	return false
}

func isSSEContentType(contentType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	return mediaType == "text/event-stream"
}
