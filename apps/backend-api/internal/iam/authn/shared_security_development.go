package authn

import (
	"errors"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/alicebob/miniredis/v2"
)

// startDevelopmentSecurityServer runs an in-process Redis and rewrites the
// configuration to point at it.
//
// The limiter, its middleware and the per-operation rate limit metadata are all
// complete; the only reason nothing is limited on a developer's machine is that
// no Redis is configured there, which makes newSharedSecurity return nil and
// the middleware pass everything through. This closes that gap without adding a
// second limiter: miniredis speaks the real protocol over a real socket, so the
// go-redis client, the Lua counters and the failure policy are the ones a
// deployment runs.
//
// Refusing this outside debug mode is the application's job, because that is
// where the debug flag lives. Anything reaching here has already passed it.
func startDevelopmentSecurityServer(cfg authnext.SharedSecurityConfig) (*miniredis.Miniredis, authnext.SharedSecurityConfig, error) {
	if redisSecurityConfigured(cfg) {
		return nil, cfg, errors.New("authn shared_security development_server cannot be combined with a configured Redis")
	}
	server, err := miniredis.Run()
	if err != nil {
		return nil, cfg, err
	}
	cfg.RedisAddresses = []string{server.Addr()}
	// Closed rather than open: a limiter that cannot reach its counter should
	// refuse authentication attempts, and in development the counter is in this
	// same process, so the only way to fail is a bug worth surfacing loudly.
	if cfg.FailurePolicy == "" {
		cfg.FailurePolicy = "closed"
	}
	return server, cfg, nil
}

// closeSecurityServer stops the in-process limiter store, if this service
// started one. Safe on a service that did not, and on a nil service.
func (s *Service) closeSecurityServer() {
	if s != nil && s.securityServer != nil {
		s.securityServer.Close()
		s.securityServer = nil
	}
}
