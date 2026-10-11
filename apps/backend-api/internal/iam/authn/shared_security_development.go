package authn

import (
	"errors"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/alicebob/miniredis/v2"
)

// developmentClockInterval is how often the in-process server is told that time
// has passed. The limiter's windows are seconds to minutes, so this is far
// finer than anything measured against it and costs nothing worth counting.
const developmentClockInterval = 50 * time.Millisecond

// developmentSecurityServer is the in-process limiter store, plus the clock it
// needs to behave like a real one.
type developmentSecurityServer struct {
	server *miniredis.Miniredis
	stop   chan struct{}
	done   chan struct{}
}

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
func startDevelopmentSecurityServer(cfg authnext.SharedSecurityConfig) (*developmentSecurityServer, authnext.SharedSecurityConfig, error) {
	if redisSecurityConfigured(cfg) {
		return nil, cfg, errors.New("authn shared_security development_server cannot be combined with a configured Redis")
	}
	server, err := miniredis.Run()
	if err != nil {
		return nil, cfg, err
	}
	development := &developmentSecurityServer{
		server: server,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	// miniredis keeps a manual clock: a key carrying a TTL never expires unless
	// the server is told time has moved. Without this the very first caller to
	// spend its allowance would stay refused until the process restarted --
	// a limiter that never forgives, which is neither what the limiter does nor
	// anything a developer could learn from.
	go development.advanceClock()
	cfg.RedisAddresses = []string{server.Addr()}
	// Closed rather than open: a limiter that cannot reach its counter should
	// refuse authentication attempts, and here the counter is in this same
	// process, so the only way to fail is a bug worth surfacing loudly.
	if cfg.FailurePolicy == "" {
		cfg.FailurePolicy = "closed"
	}
	return development, cfg, nil
}

func (d *developmentSecurityServer) advanceClock() {
	defer close(d.done)
	ticker := time.NewTicker(developmentClockInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-ticker.C:
			d.server.FastForward(developmentClockInterval)
		}
	}
}

// Close stops the clock and the server. Safe to call more than once.
func (d *developmentSecurityServer) Close() {
	if d == nil {
		return
	}
	select {
	case <-d.stop:
	default:
		close(d.stop)
	}
	<-d.done
	d.server.Close()
}

// closeSecurityServer stops the in-process limiter store, if this service
// started one. Safe on a service that did not, and on a nil service.
func (s *Service) closeSecurityServer() {
	if s != nil && s.securityServer != nil {
		s.securityServer.Close()
		s.securityServer = nil
	}
}
