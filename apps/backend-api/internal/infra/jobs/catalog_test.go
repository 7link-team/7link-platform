package jobs

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func catalogModule() *Module {
	m := NewModule(nil, Config{})
	m.acquire = func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
		return nil, nil
	}
	m.tryAcquire = dlock.TryRenewingLease(m.acquire)
	m.guard = func(context.Context, *gorm.DB, string) error { return nil }
	m.ids, _ = guid.New(1)
	m.recorder = &executionRecorder{}
	return m
}
func catalogHandler(kind string) Handler {
	return Handler{Kind: kind, Run: func(context.Context, Job) error { return nil }}
}

func TestCatalogDefaultsReferencesAndSeal(t *testing.T) {
	m := catalogModule()
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	catalog, err := module.Resolve[Catalog](services)
	require.NoError(t, err)
	payload := map[string]any{"mode": "before"}
	require.NoError(t, catalog.AddSchedules("governance", Schedule{Name: "governance.hourly", Kind: "governance.seed_reconcile", Interval: time.Hour, Payload: payload}))
	payload["mode"] = "after"
	require.ErrorIs(t, m.Initialize(t.Context()), ErrUnknownKind)
	require.False(t, m.sealed)
	require.NoError(t, catalog.AddHandlers("governance", catalogHandler("governance.seed_reconcile")))
	require.NoError(t, m.Initialize(t.Context()))
	h := m.handlers["governance.seed_reconcile"]
	require.Equal(t, "governance", h.Module)
	require.Equal(t, "default", h.Queue)
	require.Equal(t, 5, h.Priority)
	require.Equal(t, 5*time.Minute, h.Timeout)
	require.Equal(t, 8, h.MaxAttempts)
	require.Equal(t, 5*time.Second, h.BackoffBase)
	require.Equal(t, time.Hour, h.BackoffMax)
	require.True(t, h.Audit)
	s := m.schedules["governance.hourly"]
	require.Equal(t, "UTC", s.Timezone)
	require.Equal(t, ScopePlatform, s.TenantScope)
	require.Equal(t, MisfireCoalesce, s.Misfire)
	require.JSONEq(t, `{"mode":"before"}`, string(s.Payload.(json.RawMessage)))
	require.ErrorIs(t, catalog.AddHandlers("governance", catalogHandler("governance.other")), ErrCatalogSealed)
	require.ErrorIs(t, catalog.AddSchedules("governance", Schedule{}), ErrCatalogSealed)
}

func TestHandlerCatalogValidationIsAtomic(t *testing.T) {
	changes := []func(*Handler){
		func(h *Handler) { h.Kind = "other.work" }, func(h *Handler) { h.Kind = "jobs.a.b" }, func(h *Handler) { h.Kind = "jobs." + strings.Repeat("a", 124) },
		func(h *Handler) { h.Queue = "BAD" }, func(h *Handler) { h.Queue = strings.Repeat("a", 65) }, func(h *Handler) { h.Run = nil },
		func(h *Handler) { h.Priority = -1 }, func(h *Handler) { h.Priority = 10 }, func(h *Handler) { h.Timeout = time.Millisecond }, func(h *Handler) { h.Timeout = time.Hour + 1 },
		func(h *Handler) { h.Timeout = time.Second + 1 }, func(h *Handler) { h.MaxAttempts = -1 }, func(h *Handler) { h.MaxAttempts = 51 },
		func(h *Handler) { h.BackoffBase = -1 }, func(h *Handler) { h.BackoffBase = time.Hour * 2 }, func(h *Handler) { h.BackoffBase = time.Millisecond + 1 }, func(h *Handler) { h.BackoffMax = time.Hour + 1 },
	}
	for i, change := range changes {
		m := catalogModule()
		bad := catalogHandler("jobs.bad")
		change(&bad)
		require.ErrorIs(t, m.AddHandlers("jobs", catalogHandler("jobs.good"), bad), ErrInvalidDeclaration, "case %d", i)
		require.Empty(t, m.handlers)
	}
	m := catalogModule()
	require.ErrorIs(t, m.AddHandlers("bad-owner"), ErrInvalidDeclaration)
	h := catalogHandler("jobs.work")
	require.ErrorIs(t, m.AddHandlers("jobs", h, h), ErrInvalidDeclaration)
	require.Empty(t, m.handlers)
	h.Queue = "urgent"
	h.Priority = 1
	h.Timeout = time.Hour
	h.MaxAttempts = 50
	h.BackoffBase = time.Second
	h.BackoffMax = time.Minute
	require.NoError(t, m.AddHandlers("jobs", h))
	require.Equal(t, time.Minute, m.handlers[h.Kind].BackoffMax)
	require.ErrorIs(t, m.AddHandlers("jobs", catalogHandler("jobs.fresh"), h), ErrInvalidDeclaration)
	require.Len(t, m.handlers, 1)
}

func TestScheduleCatalogValidationIsAtomic(t *testing.T) {
	changes := []func(*Schedule){
		func(s *Schedule) { s.Name = "other.hourly" }, func(s *Schedule) { s.Kind = "bad" }, func(s *Schedule) { s.Kind = "jobs." + strings.Repeat("x", 124) },
		func(s *Schedule) { s.Interval = 0 }, func(s *Schedule) { s.Interval = -time.Second }, func(s *Schedule) { s.Interval = time.Millisecond }, func(s *Schedule) { s.Interval = time.Second + 1 },
		func(s *Schedule) { s.Cron = "0 * * * *" }, func(s *Schedule) { s.Timezone = "Local" }, func(s *Schedule) { s.Timezone = "Not/AZone" },
		func(s *Schedule) { s.Timezone = strings.Repeat("x", 65) }, func(s *Schedule) { s.TenantScope = "UNKNOWN" }, func(s *Schedule) { s.Misfire = "UNKNOWN" },
		func(s *Schedule) { s.Interval = 0; s.Cron = "@hourly" }, func(s *Schedule) { s.Interval = 0; s.Cron = "60 * * * *" },
		func(s *Schedule) { s.Interval = 0; s.Cron = "CRON_TZ=UTC 0 * * * *" }, func(s *Schedule) { s.Interval = 0; s.Cron = strings.Repeat("x", 129) },
	}
	good := Schedule{Name: "jobs.hourly", Kind: "jobs.work", Interval: time.Hour}
	for i, change := range changes {
		m := catalogModule()
		bad := good
		bad.Name = "jobs.bad"
		change(&bad)
		require.ErrorIs(t, m.AddSchedules("jobs", good, bad), ErrInvalidDeclaration, "case %d", i)
		require.Empty(t, m.schedules)
	}
	m := catalogModule()
	require.ErrorIs(t, m.AddSchedules("bad-owner"), ErrInvalidDeclaration)
	require.ErrorIs(t, m.AddSchedules("jobs", good, good), ErrInvalidDeclaration)
	require.Empty(t, m.schedules)
	cron := Schedule{Name: "jobs.cron", Kind: "jobs.work", Cron: "*/5 9-17 * * MON-FRI", Timezone: "Asia/Singapore", TenantScope: ScopePerTenant, Misfire: MisfireSkip}
	require.NoError(t, m.AddSchedules("jobs", cron))
	require.Equal(t, ScopePerTenant, m.schedules[cron.Name].TenantScope)
	require.ErrorIs(t, m.AddSchedules("jobs", good, cron), ErrInvalidDeclaration)
	require.Len(t, m.schedules, 1)
}

func TestCatalogPayloadBoundaries(t *testing.T) {
	// A JSON object envelope is required; arrays, null and scalar JSON are rejected.
	for _, payload := range []any{[]int{1}, "text", 42, true, json.RawMessage(`null`), json.RawMessage(`{broken`), func() {}, map[string]any{"text": strings.Repeat("a", 65536)}} {
		m := catalogModule()
		err := m.AddSchedules("jobs", Schedule{Name: "jobs.hourly", Kind: "jobs.work", Interval: time.Hour, Payload: payload})
		require.ErrorIs(t, err, ErrInvalidPayload)
		require.Empty(t, m.schedules)
	}
	atLimit := json.RawMessage(`{"x":"` + strings.Repeat("a", 65528) + `"}`)
	require.Len(t, atLimit, 65536)
	encoded, err := encodePayload(atLimit)
	require.NoError(t, err)
	require.Len(t, encoded, 65536)
	atLimit[6] = 'b'
	require.NotEqual(t, atLimit[6], encoded[6], "catalog must own its payload bytes")
	encoded, err = encodePayload(nil)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(encoded))
}

func TestCatalogConcurrentDuplicateRegistration(t *testing.T) {
	m := catalogModule()
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for range 16 {
		wg.Go(func() { results <- m.AddHandlers("jobs", catalogHandler("jobs.work")) })
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrInvalidDeclaration)
		}
	}
	require.Equal(t, 1, successes)
	require.Len(t, m.handlers, 1)
}

func TestScheduleIntervalOverridesAreValidatedAtomically(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Second, time.Millisecond, time.Second + 1} {
		m := catalogModule()
		require.NoError(t, m.AddHandlers("jobs", catalogHandler("jobs.work")))
		require.NoError(t, m.AddSchedules("jobs", Schedule{Name: "jobs.daily", Kind: "jobs.work", Cron: "0 0 * * *"}))
		m.config.Schedules = map[string]ScheduleOverride{"jobs.daily": {Interval: &interval}}
		require.ErrorIs(t, m.Initialize(t.Context()), ErrInvalidDeclaration)
		require.False(t, m.sealed)
		require.Equal(t, "0 0 * * *", m.schedules["jobs.daily"].Cron)
	}
	m := catalogModule()
	m.config.Schedules = map[string]ScheduleOverride{"jobs.missing": {}}
	require.ErrorIs(t, m.Initialize(t.Context()), ErrInvalidDeclaration)
	require.False(t, m.sealed)
}

func TestScheduleIntervalOverrideReplacesCronAndCopiesInput(t *testing.T) {
	interval := 2 * time.Hour
	input := map[string]ScheduleOverride{"jobs.daily": {Interval: &interval}}
	configured := NewModule(nil, Config{Schedules: input})
	interval = 0
	delete(input, "jobs.daily")
	m := catalogModule()
	m.config.Schedules = configured.config.Schedules
	require.NoError(t, m.AddHandlers("jobs", catalogHandler("jobs.work")))
	require.NoError(t, m.AddSchedules("jobs", Schedule{Name: "jobs.daily", Kind: "jobs.work", Cron: "0 0 * * *", Timezone: "Asia/Singapore"}))
	require.NoError(t, m.Initialize(t.Context()))
	s := m.schedules["jobs.daily"]
	require.Equal(t, 2*time.Hour, s.Interval)
	require.Empty(t, s.Cron)
	require.Equal(t, "Asia/Singapore", s.Timezone)
	next, err := nextScheduleTime(s, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(7201000), next)
}
