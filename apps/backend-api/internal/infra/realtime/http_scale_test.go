package realtime

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/stretchr/testify/require"
)

type scaleStreamVerifier struct{ streamVerifier }

func (*scaleStreamVerifier) Authenticate(_ context.Context, bearer, _ string) (*authn.Claims, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(bearer, "Bearer "), 10, 64)
	if err != nil || id <= 0 {
		return nil, authn.ErrInvalidToken
	}
	return &authn.Claims{PrincipalID: guid.ID(id), Subject: strconv.FormatInt(id, 10)}, nil
}

// This opens real TCP/SSE responses, not synthetic entries in Hub.connections.
// Consumers drain concurrently and assert every connection received the hint.
func TestSSEFiveThousandOpenStreamsFanoutAndQuota(t *testing.T) {
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
	v := &scaleStreamVerifier{}
	m.registrar = authz.NewRegistrar(authz.MustRegistry(), v, v, v)
	m.hub = activeHub()
	m.lifetime = 5 * time.Minute
	m.heartbeat = time.Minute
	m.reauth = time.Minute
	require.NoError(t, m.register("project", func(context.Context, guid.ID, guid.ID, Topic) (bool, error) { return true, nil }))
	mux := http.NewServeMux()
	require.NoError(t, m.RegisterHTTP(t.Context(), humago.New(mux, huma.DefaultConfig("test", "1"))))
	server := httptest.NewServer(mux)
	transport := &http.Transport{MaxIdleConns: 10, MaxConnsPerHost: 0}
	client := &http.Client{Transport: transport}
	var bodies []io.ReadCloser
	t.Cleanup(func() {
		for _, body := range bodies {
			_ = body.Close()
		}
		_ = m.Quiesce(context.Background())
		transport.CloseIdleConnections()
		server.Close()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	open := func(principal int) *http.Response {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/tenants/1/event-stream?topic=tenant/1/project/2", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %d", principal))
		response, err := client.Do(req)
		require.NoError(t, err)
		bodies = append(bodies, response.Body)
		return response
	}
	readers := make([]*bufio.Reader, 0, 5000)
	for i := 0; i < 5000; i++ {
		response := open(i/10 + 1) //nolint:bodyclose // The fixture retains every body and closes it in t.Cleanup.
		require.Equal(t, 200, response.StatusCode, "stream %d", i)
		reader := bufio.NewReader(response.Body)
		require.Contains(t, readFrame(t, reader), "event: ready")
		readers = append(readers, reader)
	}
	require.Len(t, m.hub.connections, 5000)
	overflow := open(501) //nolint:bodyclose // The fixture retains this body and closes it in t.Cleanup.
	require.Equal(t, 429, overflow.StatusCode)
	require.Equal(t, "5", overflow.Header.Get("Retry-After"))
	arrivals := make(chan time.Time, 5000)
	errors := make(chan error, 5000)
	var consumers sync.WaitGroup
	for _, reader := range readers {
		consumers.Add(1)
		go func() {
			defer consumers.Done()
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					errors <- err
					return
				}
				if line == "event: work.changed\n" {
					arrivals <- time.Now()
					return
				}
			}
		}()
	}
	started := time.Now()
	m.hub.deliver(signalEvent(1, "tenant/1/project/2"))
	durations := make([]time.Duration, 0, 5000)
	for len(durations) < 5000 {
		select {
		case at := <-arrivals:
			durations = append(durations, at.Sub(started))
		case err := <-errors:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	consumers.Wait()
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[4749]
	t.Logf("actual TCP streams=5000, delivered=5000, fanout P95=%s max=%s", p95, durations[4999])
	require.Less(t, p95, time.Second)
}
