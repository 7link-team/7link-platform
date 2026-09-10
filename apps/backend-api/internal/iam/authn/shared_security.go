package authn

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/danielgtaylor/huma/v2"
	"github.com/redis/go-redis/v9"
)

// sharedSecurity owns one bounded Redis client for the authentication module.
// Captcha GETDEL and counter increments are atomic across application replicas.
type sharedSecurity struct {
	client redis.UniversalClient
	cfg    authnext.SharedSecurityConfig
}

func newSharedSecurity(cfg authnext.SharedSecurityConfig) (*sharedSecurity, error) {
	if !redisSecurityConfigured(cfg) {
		return nil, nil
	}
	if cfg.FailurePolicy != "closed" && cfg.FailurePolicy != "open" {
		return nil, errors.New("authn shared_security failure_policy must explicitly be closed or open")
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "iam-security"
	}
	if strings.ContainsAny(cfg.Namespace, "{}\r\n") {
		return nil, errors.New("authn shared_security namespace contains invalid characters")
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 2 * time.Second
	}
	if cfg.Window == 0 {
		cfg.Window = time.Minute
	}
	if cfg.PerIP == 0 {
		cfg.PerIP = 60
	}
	if cfg.Global == 0 {
		cfg.Global = 3000
	}
	if cfg.OAuthPerIP == 0 {
		cfg.OAuthPerIP = 600
	}
	if cfg.OAuthGlobal == 0 {
		cfg.OAuthGlobal = 30000
	}
	if cfg.RequestTimeout < time.Millisecond || cfg.Window < time.Second || cfg.PerIP < 1 || cfg.Global < 1 ||
		cfg.OAuthPerIP < 1 || cfg.OAuthGlobal < 1 {
		return nil, errors.New("authn shared_security requires positive timeouts and request limits")
	}
	client, err := newSecurityRedisClient(cfg)
	if err != nil {
		return nil, err
	}
	client.AddHook(noRedisReplay{})
	return &sharedSecurity{client: client, cfg: cfg}, nil
}

func (s *sharedSecurity) key(kind, id string) string {
	// Raw addresses and challenge identifiers never become Redis key material.
	return fmt.Sprintf("{%s}:%s:%x", s.cfg.Namespace, kind, sha256.Sum256([]byte(id)))
}

func (s *sharedSecurity) Set(id, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.RequestTimeout)
	defer cancel()
	if err := s.client.Set(ctx, s.key("captcha", id), value, interactiveTTL).Err(); err != nil {
		return errors.New("shared captcha storage unavailable")
	}
	return nil
}

func (s *sharedSecurity) Get(id string, consume bool) string {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.RequestTimeout)
	defer cancel()
	var value string
	var err error
	if consume {
		value, err = s.client.GetDel(ctx, s.key("captcha", id)).Result()
	} else {
		value, err = s.client.Get(ctx, s.key("captcha", id)).Result()
	}
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Warn("shared captcha storage unavailable; challenge rejected")
		}
		return ""
	}
	return value
}

func (s *sharedSecurity) Verify(id, answer string, consume bool) bool {
	stored := s.Get(id, consume)
	return stored != "" && subtle.ConstantTimeCompare([]byte(strings.ToLower(stored)), []byte(strings.ToLower(answer))) == 1
}

// One script increments both scopes and gives each counter a fixed lifetime.
// The common hash tag keeps the pair in the same Redis slot.
var authenticationLimit = redis.NewScript(`
local retry = 0
for i, key in ipairs(KEYS) do
  local count = redis.call('INCR', key)
  if count == 1 then redis.call('PEXPIRE', key, ARGV[1]) end
  local ttl = redis.call('PTTL', key)
  if ttl < 0 then redis.call('PEXPIRE', key, ARGV[1]); ttl = tonumber(ARGV[1]) end
  if count > tonumber(ARGV[i+1]) and ttl > retry then retry = ttl end
end
return retry
`)

func (s *sharedSecurity) allow(ctx context.Context, remote, scope string) (time.Duration, error) {
	peer, _, err := net.SplitHostPort(remote)
	if err != nil {
		peer = remote
	}
	// Do not trust X-Forwarded-For. Behind a proxy the per-peer budget applies
	// to that proxy; configure a matching budget and enforce client-IP limits
	// at the trusted ingress until the application has a trusted-proxy contract.
	if ip := net.ParseIP(peer); ip != nil {
		peer = ip.String()
	} else {
		peer = "unknown"
	}
	global, perIP := s.cfg.Global, s.cfg.PerIP
	if scope == scopeOAuth {
		global, perIP = s.cfg.OAuthGlobal, s.cfg.OAuthPerIP
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	retry, err := authenticationLimit.Run(ctx, s.client,
		[]string{s.key(scope+"-limit", "global"), s.key(scope+"-peer", peer)},
		s.cfg.Window.Milliseconds(), global, perIP).Int64()
	return time.Duration(retry) * time.Millisecond, err
}

// The two budgets are counted separately so a busy OAuth integration cannot
// exhaust the one that protects password and MFA attempts, and the other way
// round. Each scope names its own Redis keys for the same reason.
const (
	scopeAuthentication = "authn"
	scopeOAuth          = "oauth"
)

func limitedAuthenticationOperation(op *huma.Operation) bool {
	return op != nil && op.Method == http.MethodPost && strings.HasPrefix(op.Path, "/authn/")
}

// limitedOAuthOperation covers the protocol endpoints that authenticate a
// client or consume a credential. They are public routes, so nothing else
// counts attempts against them.
func limitedOAuthOperation(op *huma.Operation) bool {
	if op == nil || op.Method != http.MethodPost {
		return false
	}
	switch op.Path {
	case "/oauth/token", "/oauth/revoke", "/oauth/introspect":
		return true
	}
	return false
}

// requestScope reports which budget an operation is counted against, or the
// empty string when it is not limited at all.
func requestScope(op *huma.Operation) string {
	switch {
	case limitedAuthenticationOperation(op):
		return scopeAuthentication
	case limitedOAuthOperation(op):
		return scopeOAuth
	}
	return ""
}

// oauthLimitRefusal is the RFC 6749 error object. The OAuth protocol endpoints
// do not use the platform envelope, and a client that cannot parse the refusal
// cannot tell a rate limit from a broken provider.
const oauthLimitRefusal = `{"error":"temporarily_unavailable"}`

func refuseLimitedRequest(a huma.API, ctx huma.Context, scope string, status int, platformCode string) {
	if scope != scopeOAuth {
		_ = huma.WriteErr(a, ctx, status, platformCode)
		return
	}
	ctx.SetHeader("Content-Type", "application/json")
	ctx.SetStatus(status)
	_, _ = ctx.BodyWriter().Write([]byte(oauthLimitRefusal))
}

func (s *Service) registerSharedSecurity(a huma.API) {
	a.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		if s == nil || s.sharedSecurity == nil {
			next(ctx)
			return
		}
		scope := requestScope(ctx.Operation())
		if scope == "" {
			next(ctx)
			return
		}
		retry, err := s.sharedSecurity.allow(ctx.Context(), ctx.RemoteAddr(), scope)
		if err != nil {
			// Deliberately omit the raw error: it may contain infrastructure or
			// credential details. The configured namespace identifies the service.
			slog.Warn("authentication request limiter unavailable", "policy", s.sharedSecurity.cfg.FailurePolicy)
			if s.sharedSecurity.cfg.FailurePolicy == "closed" {
				refuseLimitedRequest(a, ctx, scope, http.StatusServiceUnavailable, "authentication_unavailable")
				return
			}
		} else if retry > 0 {
			ctx.SetHeader("Retry-After", strconv.FormatInt((retry.Milliseconds()+999)/1000, 10))
			refuseLimitedRequest(a, ctx, scope, http.StatusTooManyRequests, "authentication_rate_limited")
			return
		}
		next(ctx)
	})
}

func documentSharedSecurity(a huma.API) {
	var fallback *huma.Response
	for _, path := range a.OpenAPI().Paths {
		if limitedAuthenticationOperation(path.Post) && path.Post.Responses["500"] != nil {
			fallback = path.Post.Responses["500"]
			break
		}
	}
	for _, path := range a.OpenAPI().Paths {
		op := path.Post
		if !limitedAuthenticationOperation(op) {
			continue
		}
		// Authentication errors already use the same Huma error schema. Keep
		// configured and unconfigured deployments on one stable API contract.
		base := op.Responses["500"]
		if base == nil {
			base = fallback
		}
		if base == nil {
			continue
		}
		for code, description := range map[string]string{"429": "Authentication request limit exceeded", "503": "Authentication shared storage unavailable"} {
			if op.Responses[code] != nil {
				continue
			}
			response := *base
			response.Description = description
			op.Responses[code] = &response
		}
		op.Responses["429"].Headers = map[string]*huma.Param{"Retry-After": {Description: "Seconds until the request budget resets", Schema: &huma.Schema{Type: "string"}}}
	}
}
