package authn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func redisTestFile(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(path, []byte(value), 0600))
	return path
}

func TestSharedRedisTopologyConfiguration(t *testing.T) {
	for _, mode := range []string{"", "standalone", "cluster", "sentinel"} {
		t.Run("valid-"+mode, func(t *testing.T) {
			cfg := authnext.SharedSecurityConfig{RedisMode: mode, RedisAddresses: []string{"127.0.0.1:1"}, FailurePolicy: "closed", RequestTimeout: 10 * time.Millisecond}
			if mode == "sentinel" {
				cfg.RedisMasterName = "primary"
			}
			s, err := newSharedSecurity(cfg)
			require.NoError(t, err)
			if mode == "cluster" {
				require.IsType(t, &redis.ClusterClient{}, s.client, "one seed must still select Cluster")
			} else {
				require.IsType(t, &redis.Client{}, s.client)
			}
			started := time.Now()
			require.Error(t, s.Set("unavailable", "answer"))
			require.Less(t, time.Since(started), time.Second)
			require.NoError(t, s.client.Close())
			require.Error(t, s.client.Ping(t.Context()).Err())
		})
	}
	missing := filepath.Join(t.TempDir(), "missing")
	empty := redisTestFile(t, "\n")
	invalid := redisTestFile(t, "not a certificate")
	url := redisTestFile(t, "redis://localhost:6379")
	cases := map[string]func(*authnext.SharedSecurityConfig){
		"mode":                    func(c *authnext.SharedSecurityConfig) { c.RedisMode = "auto" },
		"addresses":               func(c *authnext.SharedSecurityConfig) { c.RedisAddresses = nil },
		"multiple-standalone":     func(c *authnext.SharedSecurityConfig) { c.RedisAddresses = []string{"a:1", "b:2"} },
		"cluster-db":              func(c *authnext.SharedSecurityConfig) { c.RedisMode, c.RedisDB = "cluster", 1 },
		"negative-db":             func(c *authnext.SharedSecurityConfig) { c.RedisDB = -1 },
		"sentinel-master":         func(c *authnext.SharedSecurityConfig) { c.RedisMode = "sentinel" },
		"master-without-sentinel": func(c *authnext.SharedSecurityConfig) { c.RedisMasterName = "primary" },
		"sentinel-user":           func(c *authnext.SharedSecurityConfig) { c.SentinelUsername = "sentinel" },
		"sentinel-password":       func(c *authnext.SharedSecurityConfig) { c.SentinelPasswordFile = invalid },
		"password-missing":        func(c *authnext.SharedSecurityConfig) { c.RedisPasswordFile = missing },
		"password-empty":          func(c *authnext.SharedSecurityConfig) { c.RedisPasswordFile = empty },
		"tls-not-enabled":         func(c *authnext.SharedSecurityConfig) { c.RedisTLSCAFile = invalid },
		"tls-name-not-enabled":    func(c *authnext.SharedSecurityConfig) { c.RedisTLSServerName = "localhost" },
		"tls-cert-not-enabled":    func(c *authnext.SharedSecurityConfig) { c.RedisTLSCertFile = invalid },
		"tls-key-not-enabled":     func(c *authnext.SharedSecurityConfig) { c.RedisTLSKeyFile = invalid },
		"tls-cert-alone":          func(c *authnext.SharedSecurityConfig) { c.RedisTLSEnabled, c.RedisTLSCertFile = true, invalid },
		"tls-key-alone":           func(c *authnext.SharedSecurityConfig) { c.RedisTLSEnabled, c.RedisTLSKeyFile = true, invalid },
		"tls-ca-missing":          func(c *authnext.SharedSecurityConfig) { c.RedisTLSEnabled, c.RedisTLSCAFile = true, missing },
		"tls-ca-invalid":          func(c *authnext.SharedSecurityConfig) { c.RedisTLSEnabled, c.RedisTLSCAFile = true, invalid },
		"tls-pair-invalid": func(c *authnext.SharedSecurityConfig) {
			c.RedisTLSEnabled, c.RedisTLSCertFile, c.RedisTLSKeyFile = true, invalid, invalid
		},
		"legacy-conflict": func(c *authnext.SharedSecurityConfig) { c.RedisURLFile = url },
	}
	for _, address := range []string{"redis://a:1", "a", ":123", "a:0", "a:65536", "a:no", "a/b:1", " a:1"} {
		cases["address-"+address] = func(c *authnext.SharedSecurityConfig) { c.RedisAddresses = []string{address} }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := authnext.SharedSecurityConfig{RedisMode: "standalone", RedisAddresses: []string{"localhost:6379"}, FailurePolicy: "closed"}
			mutate(&cfg)
			s, err := newSharedSecurity(cfg)
			require.Error(t, err)
			require.Nil(t, s)
		})
	}
	for _, cfg := range []authnext.SharedSecurityConfig{{RedisPasswordFile: empty}, {RedisTLSKeyFile: invalid}, {RedisDB: 1}, {RedisUsername: "user"}, {SentinelUsername: "user"}} {
		cfg.FailurePolicy = "closed"
		s, err := newSharedSecurity(cfg)
		require.Error(t, err, "partial configuration must not silently disable security")
		require.Nil(t, s)
	}
	config, err := securityRedisTLS(authnext.SharedSecurityConfig{RedisTLSEnabled: true})
	require.NoError(t, err)
	require.EqualValues(t, tls.VersionTLS12, config.MinVersion)
	require.False(t, config.InsecureSkipVerify)
}

func TestSharedRedisNoReplayHookPreservesCommand(t *testing.T) {
	cmd := redis.NewStringCmd(t.Context(), "getdel", "{test}:key")
	want := errors.New("ambiguous connection loss")
	err := (noRedisReplay{}).ProcessHook(func(_ context.Context, wrapped redis.Cmder) error {
		require.True(t, wrapped.NoRetry())
		require.Equal(t, cmd.Args(), wrapped.Args())
		wrapped.SetErr(want)
		cmd.SetVal("answer")
		return want
	})(t.Context(), cmd)
	require.ErrorIs(t, err, want)
	require.ErrorIs(t, cmd.Err(), want)
	require.Equal(t, "answer", cmd.Val())
	require.NoError(t, (noRedisReplay{}).ProcessPipelineHook(func(_ context.Context, cmds []redis.Cmder) error {
		require.Len(t, cmds, 1)
		require.True(t, cmds[0].NoRetry())
		return nil
	})(t.Context(), []redis.Cmder{cmd}))
}

func TestSharedRedisTLSAndFileCredentials(t *testing.T) {
	fixture := httptest.NewTLSServer(nil)
	cert := fixture.TLS.Certificates[0]
	fixture.Close()
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	require.NoError(t, err)
	certFile := redisTestFile(t, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})))
	keyFile := redisTestFile(t, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})))
	server := miniredis.NewMiniRedis()
	require.NoError(t, server.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}))
	t.Cleanup(server.Close)
	server.RequireUserAuth("application", "test-password")
	cfg := authnext.SharedSecurityConfig{RedisAddresses: []string{server.Addr()}, RedisUsername: "application", RedisPasswordFile: redisTestFile(t, "test-password"), FailurePolicy: "closed", RedisTLSEnabled: true, RedisTLSCAFile: certFile, RedisTLSCertFile: certFile, RedisTLSKeyFile: keyFile}
	s, err := newSharedSecurity(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.client.Close() })
	require.NoError(t, s.Set("tls", "answer"))
	require.Equal(t, "answer", s.Get("tls", true))
	for _, mutate := range []func(*authnext.SharedSecurityConfig){
		func(c *authnext.SharedSecurityConfig) { c.RedisTLSKeyFile += "missing" },
		func(c *authnext.SharedSecurityConfig) {
			c.RedisMode, c.RedisMasterName, c.SentinelPasswordFile = "sentinel", "primary", keyFile+"missing"
		},
	} {
		bad := cfg
		mutate(&bad)
		_, err := newSharedSecurity(bad)
		require.Error(t, err)
	}
}
