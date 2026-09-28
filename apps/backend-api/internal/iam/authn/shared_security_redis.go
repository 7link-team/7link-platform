package authn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strconv"
	"strings"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/secretx"
	"github.com/redis/go-redis/v9"
)

func redisSecurityConfigured(c authnext.SharedSecurityConfig) bool {
	return c.RedisURLFile != "" || c.RedisMode != "" || len(c.RedisAddresses) > 0 || c.RedisMasterName != "" || c.RedisDB != 0 || c.RedisUsername != "" || c.RedisPasswordFile != "" || c.SentinelUsername != "" || c.SentinelPasswordFile != "" || c.RedisTLSEnabled || c.RedisTLSCAFile != "" || c.RedisTLSCertFile != "" || c.RedisTLSKeyFile != "" || c.RedisTLSServerName != ""
}

func newSecurityRedisClient(c authnext.SharedSecurityConfig) (redis.UniversalClient, error) {
	bad := func() (redis.UniversalClient, error) {
		return nil, errors.New("authn shared_security Redis topology configuration is invalid or conflicting")
	}
	if c.RedisURLFile != "" {
		other := c
		other.RedisURLFile = ""
		if redisSecurityConfigured(other) {
			return bad()
		}
		value, err := secretx.Resolve("authn shared_security redis_url", "", c.RedisURLFile, secretx.DefaultMaxBytes)
		if err != nil {
			return nil, err
		}
		opts, err := redis.ParseURL(value)
		if err != nil {
			return nil, errors.New("authn shared_security Redis URL is invalid")
		}
		opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout, opts.PoolTimeout = c.RequestTimeout, c.RequestTimeout, c.RequestTimeout, c.RequestTimeout
		opts.ContextTimeoutEnabled, opts.MaxRetries = true, -1
		return redis.NewClient(opts), nil
	}
	mode := c.RedisMode
	if mode == "" {
		mode = "standalone"
	}
	if mode != "standalone" && mode != "cluster" && mode != "sentinel" {
		return bad()
	}
	if len(c.RedisAddresses) == 0 || (mode == "standalone" && len(c.RedisAddresses) != 1) || c.RedisDB < 0 || (mode == "cluster" && c.RedisDB != 0) {
		return bad()
	}
	if (mode == "sentinel") != (strings.TrimSpace(c.RedisMasterName) != "") {
		return bad()
	}
	if mode != "sentinel" && (c.SentinelUsername != "" || c.SentinelPasswordFile != "") {
		return bad()
	}
	for _, addr := range c.RedisAddresses {
		host, port, err := net.SplitHostPort(addr)
		n, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || n < 1 || n > 65535 || strings.TrimSpace(host) == "" || strings.ContainsAny(host, "/@?# \t\r\n") {
			return bad()
		}
	}
	password, err := secretx.Resolve("authn shared_security redis_password", "", c.RedisPasswordFile, secretx.DefaultMaxBytes)
	if err != nil {
		return nil, err
	}
	sentinelPassword, err := secretx.Resolve("authn shared_security sentinel_password", "", c.SentinelPasswordFile, secretx.DefaultMaxBytes)
	if err != nil {
		return nil, err
	}
	if (c.RedisPasswordFile != "" && password == "") || (c.SentinelPasswordFile != "" && sentinelPassword == "") {
		return bad()
	}
	tlsConfig, err := securityRedisTLS(c)
	if err != nil {
		return nil, err
	}
	opts := &redis.UniversalOptions{Addrs: append([]string(nil), c.RedisAddresses...), DB: c.RedisDB,
		Username: c.RedisUsername, Password: password, SentinelUsername: c.SentinelUsername, SentinelPassword: sentinelPassword,
		MasterName: c.RedisMasterName, TLSConfig: tlsConfig, MaxRetries: -1, MaxRedirects: 3,
		DialTimeout: c.RequestTimeout, ReadTimeout: c.RequestTimeout, WriteTimeout: c.RequestTimeout, PoolTimeout: c.RequestTimeout, ContextTimeoutEnabled: true}
	switch mode {
	case "cluster":
		return redis.NewClusterClient(opts.Cluster()), nil
	case "sentinel":
		return redis.NewFailoverClient(opts.Failover()), nil
	default:
		return redis.NewClient(opts.Simple()), nil
	}
}

func securityRedisTLS(c authnext.SharedSecurityConfig) (*tls.Config, error) {
	if !c.RedisTLSEnabled {
		if c.RedisTLSCAFile != "" || c.RedisTLSCertFile != "" || c.RedisTLSKeyFile != "" || c.RedisTLSServerName != "" {
			return nil, errors.New("authn shared_security TLS files require redis_tls_enabled")
		}
		return nil, nil
	}
	if (c.RedisTLSCertFile == "") != (c.RedisTLSKeyFile == "") {
		return nil, errors.New("authn shared_security TLS certificate and key must be paired")
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.RedisTLSServerName}
	if c.RedisTLSCAFile != "" {
		pem, err := secretx.Resolve("authn shared_security TLS CA", "", c.RedisTLSCAFile, secretx.DefaultMaxBytes)
		if err != nil {
			return nil, errors.New("authn shared_security TLS CA cannot be read")
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(pem)) {
			return nil, errors.New("authn shared_security TLS CA is invalid")
		}
		config.RootCAs = roots
	}
	if c.RedisTLSCertFile != "" {
		certPEM, certErr := secretx.Resolve("authn shared_security TLS certificate", "", c.RedisTLSCertFile, secretx.DefaultMaxBytes)
		keyPEM, keyErr := secretx.Resolve("authn shared_security TLS key", "", c.RedisTLSKeyFile, secretx.DefaultMaxBytes)
		if certErr != nil || keyErr != nil {
			return nil, errors.New("authn shared_security TLS client certificate/key cannot be read")
		}
		cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if err != nil {
			return nil, errors.New("authn shared_security TLS client certificate/key is invalid")
		}
		config.Certificates = []tls.Certificate{cert}
	}
	return config, nil
}

// The cluster redirect loop otherwise retries network errors even with
// MaxRetries=-1. Explicit MOVED/ASK and NOSCRIPT still follow their safe paths.
type noRedisReplay struct{}
type noReplayCommand struct{ redis.Cmder }

func (noReplayCommand) NoRetry() bool                             { return true }
func (noRedisReplay) DialHook(next redis.DialHook) redis.DialHook { return next }
func (noRedisReplay) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { return next(ctx, noReplayCommand{cmd}) }
}
func (noRedisReplay) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		wrapped := make([]redis.Cmder, len(cmds))
		for i, cmd := range cmds {
			wrapped[i] = noReplayCommand{cmd}
		}
		return next(ctx, wrapped)
	}
}
