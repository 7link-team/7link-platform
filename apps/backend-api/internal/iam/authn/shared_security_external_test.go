package authn

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Run with scripts/test-redis-security.sh; all servers contain disposable data.
func TestSharedRedisExternal(t *testing.T) {
	if os.Getenv("IAM_TEST_REDIS_TOPOLOGIES") != "1" {
		t.Skip("requires disposable Redis topology runner")
	}
	base := authnext.SharedSecurityConfig{FailurePolicy: "closed", RedisUsername: "application", RedisPasswordFile: "/work/data-password", RequestTimeout: time.Second, PerIP: 2, Global: 3}
	if os.Getenv("IAM_TEST_REDIS_TLS") == "1" {
		base.RedisTLSEnabled, base.RedisTLSCAFile, base.RedisTLSServerName = true, "/work/ca.crt", "localhost"
		base.RedisTLSCertFile, base.RedisTLSKeyFile = "/work/client.crt", "/work/client.key"
	}
	for _, mode := range []string{"standalone", "cluster", "sentinel"} {
		t.Run(mode, func(t *testing.T) {
			cfg := base
			cfg.RedisMode, cfg.Namespace = mode, "external-"+mode
			switch mode {
			case "standalone":
				cfg.RedisAddresses = []string{"127.0.0.1:6400"}
				cfg.RedisTLSEnabled, cfg.RedisTLSCAFile = true, "/work/ca.crt"
				cfg.RedisTLSCertFile, cfg.RedisTLSKeyFile = "/work/client.crt", "/work/client.key"
				cfg.RedisTLSServerName = "localhost"
			case "cluster":
				cfg.RedisAddresses = []string{"127.0.0.1:7000"}
			case "sentinel":
				cfg.RedisAddresses = []string{"127.0.0.1:26379", "127.0.0.1:26380", "127.0.0.1:26381"}
				cfg.RedisMasterName, cfg.SentinelUsername, cfg.SentinelPasswordFile = "primary", "discovery", "/work/sentinel-password"
			}
			a, err := newSharedSecurity(cfg)
			require.NoError(t, err)
			b, err := newSharedSecurity(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { _ = a.client.Close(); _ = b.client.Close() })
			require.NoError(t, a.Set("one-shot", "answer"))
			var winners atomic.Int32
			var group sync.WaitGroup
			for i := range 16 {
				group.Go(func() {
					client := a
					if i%2 == 0 {
						client = b
					}
					if client.Get("one-shot", true) == "answer" {
						winners.Add(1)
					}
				})
			}
			group.Wait()
			require.EqualValues(t, 1, winners.Load())
			for range 2 {
				retry, err := a.allow(t.Context(), "192.0.2.1:123", scopeAuthentication)
				require.NoError(t, err)
				require.Zero(t, retry)
			}
			retry, err := b.allow(t.Context(), "192.0.2.1:456", scopeAuthentication)
			require.NoError(t, err)
			require.Positive(t, retry)
			retry, err = b.allow(t.Context(), "198.51.100.1:123", scopeAuthentication)
			require.NoError(t, err)
			require.Positive(t, retry)
			switch mode {
			case "standalone":
				for _, mutate := range []func(*authnext.SharedSecurityConfig){
					func(c *authnext.SharedSecurityConfig) { c.RedisTLSServerName = "wrong.invalid" },
					func(c *authnext.SharedSecurityConfig) { c.RedisTLSCertFile, c.RedisTLSKeyFile = "", "" },
					func(c *authnext.SharedSecurityConfig) { c.RedisUsername = "wrong-user" },
				} {
					bad := cfg
					mutate(&bad)
					s, err := newSharedSecurity(bad)
					require.NoError(t, err)
					require.Error(t, s.Set("rejected", "answer"))
					require.Empty(t, s.Get("one-shot", true))
					require.NoError(t, s.client.Close())
				}
			case "cluster":
				testSharedRedisRedirects(t, a)
				testSharedRedisAmbiguousResponse(t, a)
				testSharedRedisClusterFailover(t, a)
			case "sentinel":
				testSharedRedisSentinelFailover(t, a, b)
			}
			require.NoError(t, a.Set("after-topology-change", "fresh"))
			require.Equal(t, "fresh", b.Get("after-topology-change", true))
			require.Empty(t, a.Get("after-topology-change", true))
		})
	}
}

func redisExternalNode(t *testing.T, addr string) *redis.Client {
	t.Helper()
	password, err := os.ReadFile("/work/data-password")
	require.NoError(t, err)
	opts := &redis.Options{Addr: addr, Username: "application", Password: strings.TrimSpace(string(password)), MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, ContextTimeoutEnabled: true}
	if os.Getenv("IAM_TEST_REDIS_TLS") == "1" {
		opts.TLSConfig, err = securityRedisTLS(authnext.SharedSecurityConfig{RedisTLSEnabled: true, RedisTLSCAFile: "/work/ca.crt", RedisTLSCertFile: "/work/client.crt", RedisTLSKeyFile: "/work/client.key", RedisTLSServerName: "localhost"})
		require.NoError(t, err)
	}
	c := redis.NewClient(opts)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func testSharedRedisRedirects(t *testing.T, s *sharedSecurity) {
	ctx := t.Context()
	c := s.client.(*redis.ClusterClient)
	slot, err := c.ClusterKeySlot(ctx, s.key("captcha", "ask")).Result()
	require.NoError(t, err)
	slots, err := c.ClusterSlots(ctx).Result() //nolint:staticcheck // ClusterSlots also supports the application's Redis 6.2 minimum.
	require.NoError(t, err)
	var sourceAddr, targetAddr string
	for _, item := range slots {
		if int64(item.Start) <= slot && slot <= int64(item.End) {
			sourceAddr = item.Nodes[0].Addr
		} else {
			targetAddr = item.Nodes[0].Addr
		}
	}
	require.NotEmpty(t, sourceAddr)
	require.NotEmpty(t, targetAddr)
	source, target := redisExternalNode(t, sourceAddr), redisExternalNode(t, targetAddr)
	sourceID, err := source.ClusterMyID(ctx).Result()
	require.NoError(t, err)
	targetID, err := target.ClusterMyID(ctx).Result()
	require.NoError(t, err)
	require.NoError(t, target.Do(ctx, "CLUSTER", "SETSLOT", slot, "IMPORTING", sourceID).Err())
	require.NoError(t, source.Do(ctx, "CLUSTER", "SETSLOT", slot, "MIGRATING", targetID).Err())
	// The key is absent on the old owner, requiring ASK + ASKING for SET/GETDEL.
	require.NoError(t, s.Set("ask", "redirected"))
	require.Equal(t, "redirected", s.Get("ask", true))
	keys, err := source.ClusterGetKeysInSlot(ctx, int(slot), 100).Result()
	require.NoError(t, err)
	if len(keys) > 0 {
		require.NoError(t, source.Del(ctx, keys...).Err())
	}
	for _, item := range slots {
		node := redisExternalNode(t, item.Nodes[0].Addr)
		require.NoError(t, node.Do(ctx, "CLUSTER", "SETSLOT", slot, "NODE", targetID).Err())
	}
	// This client cached the former owner; its next command must follow MOVED.
	require.NoError(t, s.Set("moved", "redirected"))
	require.Equal(t, "redirected", s.Get("moved", true))
}

type redisLostResponse struct {
	noRedisReplay
	calls *atomic.Int32
}

func (h redisLostResponse) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if cmd.Name() == "incr" {
			if h.calls.Add(1) == 1 && err == nil {
				return io.EOF
			}
		}
		return err
	}
}

func testSharedRedisAmbiguousResponse(t *testing.T, s *sharedSecurity) {
	var calls atomic.Int32
	probe, err := newSharedSecurity(s.cfg)
	require.NoError(t, err)
	defer func() { _ = probe.client.Close() }()
	c := probe.client.(*redis.ClusterClient)
	c.OnNewNode(func(node *redis.Client) { node.AddHook(redisLostResponse{calls: &calls}) })
	key := s.key("test", "lost-response")
	require.ErrorIs(t, c.Incr(t.Context(), key).Err(), io.EOF)
	require.EqualValues(t, 1, calls.Load(), "an executed command with a lost response must not be replayed")
	value, err := c.Get(t.Context(), key).Int()
	require.NoError(t, err)
	require.Equal(t, 1, value)
}

func testSharedRedisClusterFailover(t *testing.T, s *sharedSecurity) {
	slot, err := s.client.ClusterKeySlot(t.Context(), s.key("captcha", "failover-probe")).Result()
	require.NoError(t, err)
	var owner redis.ClusterSlot
	require.Eventually(t, func() bool {
		slots, err := s.client.ClusterSlots(t.Context()).Result()
		if err != nil {
			return false
		}
		for _, item := range slots {
			if int64(item.Start) <= slot && slot <= int64(item.End) {
				owner = item
				return len(owner.Nodes) >= 2
			}
		}
		return false
	}, 15*time.Second, 100*time.Millisecond)
	replica := redisExternalNode(t, owner.Nodes[1].Addr)
	replicaID, err := replica.ClusterMyID(t.Context()).Result()
	require.NoError(t, err)
	require.NoError(t, replica.Do(t.Context(), "CLUSTER", "FAILOVER", "TAKEOVER").Err())
	require.Eventually(t, func() bool {
		updated, err := s.client.ClusterSlots(t.Context()).Result()
		if err != nil {
			return false
		}
		for _, item := range updated {
			if int64(item.Start) <= slot && slot <= int64(item.End) {
				return item.Nodes[0].ID == replicaID
			}
		}
		return false
	}, 15*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool { return s.Set("failover-probe", "fresh") == nil }, 15*time.Second, 100*time.Millisecond)
}

func testSharedRedisSentinelFailover(t *testing.T, s, peer *sharedSecurity) {
	password, err := os.ReadFile("/work/sentinel-password")
	require.NoError(t, err)
	tlsConfig, err := securityRedisTLS(s.cfg)
	require.NoError(t, err)
	sentinel := redis.NewSentinelClient(&redis.Options{Addr: "127.0.0.1:26379", Username: "discovery", Password: strings.TrimSpace(string(password)), TLSConfig: tlsConfig, ReadTimeout: time.Second, WriteTimeout: time.Second})
	defer func() { _ = sentinel.Close() }()
	before, err := sentinel.GetMasterAddrByName(t.Context(), "primary").Result()
	require.NoError(t, err)
	require.NoError(t, sentinel.Failover(t.Context(), "primary").Err())
	var promoted string
	require.Eventually(t, func() bool {
		after, err := sentinel.GetMasterAddrByName(t.Context(), "primary").Result()
		promoted = strings.Join(after, ":")
		return err == nil && strings.Join(before, ":") != promoted
	}, 20*time.Second, 100*time.Millisecond)
	// Promotion and each client's asynchronous +switch-master notification are
	// separate events. A successful SET may still hit the not-yet-demoted old
	// primary. Observe both actual connections reaching the promoted node before
	// issuing the fresh one-shot challenge; never retry its consuming GETDEL.
	for _, client := range []*sharedSecurity{s, peer} {
		require.Eventually(t, func() bool {
			info, err := client.client.Do(t.Context(), "CLIENT", "INFO").Text()
			return err == nil && strings.Contains(info, "laddr="+promoted+" ")
		}, 15*time.Second, 100*time.Millisecond)
	}
	t.Logf("Sentinel promoted a replacement for %s", strings.Join(before, ":"))
}
