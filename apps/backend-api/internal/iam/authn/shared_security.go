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
	if cfg.RequestTimeout < time.Millisecond || cfg.Window < time.Second || cfg.PerIP < 1 || cfg.Global < 1 {
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

func (s *sharedSecurity) allow(ctx context.Context, remote string) (time.Duration, error) {
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
	ctx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	retry, err := authenticationLimit.Run(ctx, s.client,
		[]string{s.key("limit", "global"), s.key("peer", peer)},
		s.cfg.Window.Milliseconds(), s.cfg.Global, s.cfg.PerIP).Int64()
	return time.Duration(retry) * time.Millisecond, err
}

func limitedAuthenticationOperation(op *huma.Operation) bool {
	return op != nil && op.Method == http.MethodPost && strings.HasPrefix(op.Path, "/authn/")
}

func (s *Service) registerSharedSecurity(a huma.API) {
	a.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		if s == nil || s.sharedSecurity == nil || !limitedAuthenticationOperation(ctx.Operation()) {
			next(ctx)
			return
		}
		retry, err := s.sharedSecurity.allow(ctx.Context(), ctx.RemoteAddr())
		if err != nil {
			// Deliberately omit the raw error: it may contain infrastructure or
			// credential details. The configured namespace identifies the service.
			slog.Warn("authentication request limiter unavailable", "policy", s.sharedSecurity.cfg.FailurePolicy)
			if s.sharedSecurity.cfg.FailurePolicy == "closed" {
				_ = huma.WriteErr(a, ctx, http.StatusServiceUnavailable, "authentication_unavailable")
				return
			}
		} else if retry > 0 {
			ctx.SetHeader("Retry-After", strconv.FormatInt((retry.Milliseconds()+999)/1000, 10))
			_ = huma.WriteErr(a, ctx, http.StatusTooManyRequests, "authentication_rate_limited")
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
