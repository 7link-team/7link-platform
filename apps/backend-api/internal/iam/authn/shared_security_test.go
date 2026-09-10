package authn

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/alicebob/miniredis/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
)

func sharedSecurityPair(t *testing.T) (*sharedSecurity, *sharedSecurity) {
	t.Helper()
	address := os.Getenv("IAM_TEST_REDIS_URL")
	if address == "" {
		address = "redis://" + miniredis.RunT(t).Addr()
	}
	path := filepath.Join(t.TempDir(), "redis-url")
	require.NoError(t, os.WriteFile(path, []byte(address), 0600))
	cfg := authnext.SharedSecurityConfig{RedisURLFile: path, FailurePolicy: "closed", Namespace: fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()), PerIP: 2, Global: 3}
	a, err := newSharedSecurity(cfg)
	require.NoError(t, err)
	b, err := newSharedSecurity(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.client.Close(); _ = b.client.Close() })
	return a, b
}

// The same suite runs with real Redis by setting IAM_TEST_REDIS_URL. Distinct
// clients and concurrent consumers prove the shared one-shot contract.
func TestSharedCaptchaAcrossReplicasIsOneShot(t *testing.T) {
	a, b := sharedSecurityPair(t)
	for _, kind := range []captchaKind{captchaText, captchaSlide, captchaClick} {
		t.Run(string(kind), func(t *testing.T) {
			challenge, err := issueCaptchaWithStore(kind, a)
			require.NoError(t, err)
			require.NotEmpty(t, b.Get(challenge.CaptchaID, false))
			var won atomic.Int32
			var group sync.WaitGroup
			for i := range 12 {
				group.Go(func() {
					client := a
					if i%2 == 0 {
						client = b
					}
					if client.Get(challenge.CaptchaID, true) != "" {
						won.Add(1)
					}
				})
			}
			group.Wait()
			require.EqualValues(t, 1, won.Load())
			require.Empty(t, a.Get(challenge.CaptchaID, false))
		})
	}
	for _, sample := range []struct {
		kind           captchaKind
		stored, answer string
	}{
		{captchaText, "ABCD", "abcd"}, {captchaSlide, "S:100:50", "104"},
		{captchaClick, `C:{"0":{"x":100,"y":100}}`, "100,100"},
	} {
		id := string(sample.kind) + "-answer"
		require.NoError(t, a.Set(id, sample.stored))
		require.True(t, verifyCaptchaWithStore(sample.kind, id, sample.answer, b))
		require.False(t, verifyCaptchaWithStore(sample.kind, id, sample.answer, a))
		require.NoError(t, a.Set(id, sample.stored))
		require.False(t, verifyCaptchaWithStore(sample.kind, id, "incorrect", b))
		require.False(t, verifyCaptchaWithStore(sample.kind, id, sample.answer, a))
	}
}

func TestSharedAuthenticationLimitsBothScopesAndExpires(t *testing.T) {
	a, b := sharedSecurityPair(t)
	for _, client := range []*sharedSecurity{a, b} {
		retry, err := client.allow(t.Context(), "192.0.2.1:4567")
		require.NoError(t, err)
		require.Zero(t, retry)
	}
	retry, err := b.allow(t.Context(), "192.0.2.1:4568")
	require.NoError(t, err)
	require.Positive(t, retry)
	retry, err = a.allow(t.Context(), "198.51.100.2:1234")
	require.NoError(t, err)
	require.Positive(t, retry, "rotating peers cannot bypass global budget")
	for _, key := range []string{a.key("limit", "global"), a.key("peer", "192.0.2.1")} {
		ttl, err := a.client.PTTL(t.Context(), key).Result()
		require.NoError(t, err)
		require.Positive(t, ttl)
		require.NoError(t, a.client.Del(t.Context(), key).Err())
	}
	retry, err = b.allow(t.Context(), "192.0.2.1:9876")
	require.NoError(t, err)
	require.Zero(t, retry)
	// An accidentally persistent counter must regain an expiry, not deny forever.
	require.NoError(t, a.client.Set(t.Context(), a.key("peer", "unknown"), 5, 0).Err())
	retry, err = a.allow(t.Context(), "not-an-address")
	require.NoError(t, err)
	require.Positive(t, retry)
}

func TestSharedCaptchaExpiryAndStorageFailure(t *testing.T) {
	server := miniredis.RunT(t)
	path := filepath.Join(t.TempDir(), "url")
	require.NoError(t, os.WriteFile(path, []byte("redis://"+server.Addr()), 0600))
	s, err := newSharedSecurity(authnext.SharedSecurityConfig{RedisURLFile: path, FailurePolicy: "closed"})
	require.NoError(t, err)
	defer func() { _ = s.client.Close() }()
	require.NoError(t, s.Set("expires", "1234"))
	server.FastForward(interactiveTTL + time.Second)
	require.False(t, s.Verify("expires", "1234", true))
	server.Close()
	for _, kind := range []captchaKind{captchaText, captchaSlide, captchaClick} {
		_, err := issueCaptchaWithStore(kind, s)
		require.Error(t, err)
		require.False(t, verifyCaptchaWithStore(kind, "missing", "1234", s))
	}
}

func TestSharedAuthenticationConcurrentBudgetCannotBeExceeded(t *testing.T) {
	a, b := sharedSecurityPair(t)
	a.cfg.PerIP, b.cfg.PerIP = 20, 20
	a.cfg.Global, b.cfg.Global = 30, 30
	var accepted atomic.Int32
	var failed atomic.Int32
	var group sync.WaitGroup
	for i := range 60 {
		group.Go(func() {
			client := a
			if i%2 == 0 {
				client = b
			}
			retry, err := client.allow(t.Context(), "192.0.2.3:4321")
			if err != nil {
				failed.Add(1)
			} else if retry == 0 {
				accepted.Add(1)
			}
		})
	}
	group.Wait()
	require.Zero(t, failed.Load())
	require.EqualValues(t, 20, accepted.Load())
}

func TestSharedSecurityRequiresExplicitValidConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "url")
	require.NoError(t, os.WriteFile(path, []byte("redis://localhost:6379"), 0600))
	for _, mutate := range []func(*authnext.SharedSecurityConfig){
		func(c *authnext.SharedSecurityConfig) { c.FailurePolicy = "" },
		func(c *authnext.SharedSecurityConfig) { c.FailurePolicy = "other" },
		func(c *authnext.SharedSecurityConfig) { c.Namespace = "bad{tag}" },
		func(c *authnext.SharedSecurityConfig) { c.PerIP = -1 },
		func(c *authnext.SharedSecurityConfig) { c.Global = -1 },
		func(c *authnext.SharedSecurityConfig) { c.Window = time.Millisecond },
		func(c *authnext.SharedSecurityConfig) { c.RequestTimeout = -1 },
		func(c *authnext.SharedSecurityConfig) { c.RedisURLFile = path + "missing" },
	} {
		cfg := authnext.SharedSecurityConfig{RedisURLFile: path, FailurePolicy: "closed"}
		mutate(&cfg)
		_, err := newSharedSecurity(cfg)
		require.Error(t, err)
	}
	require.NoError(t, os.WriteFile(path, []byte("http://invalid"), 0600))
	_, err := newSharedSecurity(authnext.SharedSecurityConfig{RedisURLFile: path, FailurePolicy: "open"})
	require.Error(t, err)
	s, err := newSharedSecurity(authnext.SharedSecurityConfig{})
	require.NoError(t, err)
	require.Nil(t, s)
}

func TestSharedSecurityHTTPRateLimitAndOutagePolicy(t *testing.T) {
	a, _ := sharedSecurityPair(t)
	_, api := humatest.New(t)
	service := &Service{sharedSecurity: a}
	service.registerSharedSecurity(api)
	register := func(path, method string) {
		huma.Register(api, huma.Operation{OperationID: method + path, Method: method, Path: path}, func(context.Context, *struct{}) (*struct{ Body string }, error) {
			return &struct{ Body string }{Body: "ok"}, nil
		})
	}
	register("/authn/test", http.MethodPost)
	register("/authn/status", http.MethodGet)
	require.Equal(t, 200, api.Post("/authn/test").Code)
	require.Equal(t, 200, api.Post("/authn/test").Code)
	response := api.Post("/authn/test", "X-Forwarded-For: 1.2.3.4")
	require.Equal(t, 429, response.Code)
	require.NotEmpty(t, response.Header().Get("Retry-After"))
	require.Equal(t, 200, api.Get("/authn/status").Code)
	require.NoError(t, a.client.Close())
	require.Equal(t, 503, api.Post("/authn/test").Code)
	a.cfg.FailurePolicy = "open"
	require.Equal(t, 200, api.Post("/authn/test").Code)
	service.sharedSecurity = nil
	require.Equal(t, 200, api.Post("/authn/test").Code)
	require.False(t, limitedAuthenticationOperation(nil))
}

func TestAllAuthenticationMutationsDocumentSharedSecurityErrors(t *testing.T) {
	_, service, _ := newAuthenticationService(t)
	_, api := humatest.New(t)
	RegisterHTTP(api, service, service)
	count := 0
	for _, path := range api.OpenAPI().Paths {
		op := path.Post
		if !limitedAuthenticationOperation(op) {
			continue
		}
		count++
		require.NotNil(t, op.Responses["429"], op.OperationID)
		require.NotNil(t, op.Responses["503"], op.OperationID)
		require.NotNil(t, op.Responses["429"].Headers["Retry-After"], op.OperationID)
	}
	require.Positive(t, count)
}
