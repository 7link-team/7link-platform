# Module Lifecycle Redesign — Design Document (v1)

> Status: **proposal under review**. This document consolidates the redesign of the
> backend-api module lifecycle. It is the review artifact for multi-angle subagent
> review and an external (codex CLI) review pass.
>
> Repo: `7link-platform` · App: `apps/backend-api` · Package under change: `internal/app`, `pkg/module`

---

## 1. Context

### 1.1 What the app is

`apps/backend-api` is a Go service (chi + huma HTTP, gRPC, GORM) built around a
**module system**. A module is any value implementing `module.Module`
(`ModuleName() string`) plus optional capability interfaces:

- lifecycle: `Initializer`, `Starter`, `Quiescer`, `Stopper`, `Deinitializer`
- wiring: `Depender`, `ServiceProvider`, `ServiceInjector`
- protocol: `HTTPRegistrar`, `GRPCRegistrar`, `UnaryInterceptorModule`,
  `StreamInterceptorModule`, `StatsHandlerModule`
- `HealthChecker`, `PrometheusCollectorModule`

The runtime (`internal/app`) owns a typed service registry (`pkg/module.Services`,
a reflect-based `map[reflect.Type]any`) used for cross-module wiring at a
"wire" phase.

### 1.2 The problem being solved

`internal/app/bootstrap.go` currently does five jobs at once:

1. **database bootstrap** — `initializeDatabase()` opens the GORM datasource router;
2. **module construction** — builds each module inline with config structs;
3. **conditional registration** — four `if config.X.Enabled { app.AddModule(...) }` branches;
4. **cross-module wiring** — observer registration, metrics collector aggregation,
   health-checker and migration-inspector closures over `app.mods`;
5. **orchestration** — `wire → migrate → initialize → start`.

The concrete defects:

- **Ordering bug (confirmed).** Metrics collector aggregation iterates `app.mods`
  at the moment metrics is constructed. Any `PrometheusCollectorModule` registered
  *after* that point is silently missed. It only works today because the single
  implementor (audit) happens to be registered before metrics. The
  `// .. biz modules` placeholder sits directly after the metrics block — a future
  business module with a collector would regress silently.
- **Cross-module construction coupling.** `metrics.NewModule(app.Db, app.databaseMonitor)`
  requires a concrete module instance at construction time, forcing ordering.
- **Scattered module state.** `app.mods`, `app.initialized`, `app.started`,
  `app.services` are four parallel slices/fields; phase runners live as `App`
  methods; http.go reaches into concrete `app.telemetry` / `app.audit` /
  `app.metrics` / `app.databaseMonitor` fields for protocol middleware.
- **Closure wiring.** `health.NewModule(app.healthCheckers)` and
  `migrationstatus.NewModule(func(ctx){... app.mods ...})` thread the whole module
  list into constructors.

### 1.3 Goal

> `bootstrap` becomes a pure **bill of materials**: `initializeDatabase` + one
> `addModule(...)` declaration. Selection (enable/disable), wiring, migration,
> monitoring, and orchestration are owned by the **module lifecycle**. Each module
> is constructed exactly once; the lifecycle manages it uniformly.

---

## 2. Design principles

1. **Declare, don't script.** `bootstrap` lists modules; the lifecycle runs them.
2. **Construction-time isolation.** No module constructor references another
   module instance. Cross-module needs resolve at wire time via services.
3. **Verify before running.** Dependency/name/duplicate errors surface at
   registration/activation, not at runtime mid-flight.
4. **One module, one construction.** State lives in a single lifecycle container.
5. **Preserve behavior.** Existing phase semantics, health output, middleware
   layering, and the "audit is always registered even when disabled" invariant
   are kept.
6. **No new external dependency.** No DI framework, no code generation (see §9
   for the rejected alternatives).

---

## 3. Design overview

```
bootstrap.go                         lifecycle (Registry-driven)
┌─────────────────────────────┐
│ initializeDatabase           │
│ addModule( ... bill of ... ) │ → Registry.Register(...)
│   materials ... )            │ → Registry.Activate()     filter Enabled()
│                              │ → Registry.Wire(cb)       services + views
│ lifecycle(ctx)               │ → migrate (if config.Migrate)
└─────────────────────────────┘ → Registry.Initialize(ctx)
                                 → Registry.Start(ctx)
```

### 3.1 `pkg/module` — new shared types

```go
// Config is the base configuration inherited by every application module.
// Module-specific configs embed it, so app identity + the activation switch
// are one uniform, config-driven concern instead of per-module boilerplate.
type Config struct {
    Enabled bool   // lifecycle reads this via Enabler
    Name    string // app name (telemetry / audit / pprof)
    Version string // app version (telemetry / audit)
}

// Enabler exposes the inherited activation switch to the lifecycle.
type Enabler interface {
    Module
    Enabled() bool
}

// HTTPMiddlewareModule contributes a server-wide HTTP middleware. Middlewares
// run in registration order: earlier module = outer middleware.
type HTTPMiddlewareModule interface {
    Module
    HTTPMiddleware(next http.Handler) http.Handler
}
```

Conditional modules embed `module.Config` in their own config and implement
`Enabled()` as a one-liner (`return m.cfg.Enabled`).

### 3.2 `pkg/module` — `Registry` (the lifecycle container)

Consolidates the parallel module state + phase runners currently spread over
`App` methods:

```go
type Registry struct {
    entries     []*entry        // registration order; each module registered once
    services    *Services       // service registry (existing type, owned here)
    initialized []*entry        // completed Initialize (reverse rollback)
    started     []*entry        // completed Start (reverse stop)
}

func NewRegistry() *Registry
func (r *Registry) Register(mods ...Module) error          // validate name/dup/deps early
func (r *Registry) Activate() error                         // drop !Enabled() modules; final validation
func (r *Registry) Wire(provide func(*Services) error) error // providers → app cross-cutting views → seal → injectors
func (r *Registry) Initialize(ctx) error
func (r *Registry) Start(ctx) error
func (r *Registry) Quiesce(ctx) error
func (r *Registry) Stop(ctx) error
func (r *Registry) Deinitialize(ctx) error
func (r *Registry) RegisterHTTP(ctx, router, api) error
func (r *Registry) RegisterGRPC(reg grpc.ServiceRegistrar)
func (r *Registry) HTTPMiddlewares() []func(http.Handler) http.Handler
func (r *Registry) GRPCProtocolOptions() []grpc.ServerOption
func (r *Registry) Modules() []Module   // active set, registration order
func (r *Registry) ByName(name) Module
```

**Key ordering guarantee:** `Wire` runs after `Activate`, i.e. after *all*
modules are registered. Registry-wide aggregations computed inside `Wire` see the
complete active set — this is what fixes the metrics collector bug (§4.4).

### 3.3 `internal/app` — bootstrap becomes a bill of materials

```go
func (app *App) bootstrap(ctx context.Context) error {
    if err := app.initializeDatabase(ctx); err != nil {
        return err
    }
    if err := app.addModule(
        i18n.NewModule(i18n.Base),
        respx.NewModule(),
        dlock.NewModule(app.Db),
        wuid.NewModule(app.Db),
        guid.NewModule(),

        // metrics before telemetry/audit keeps its HTTP middleware outermost
        metrics.NewModule(app.Db, metricsConfig(app.config)),
        telemetry.NewModule(telemetryConfig(app.config)),
        audit.NewModule(app.Db, auditConfig(app.config)),
        databasemonitor.NewModule(app.Db, datasourceMonitorConfig(app.config)),

        migrationstatus.NewModule(migrationStatusConfig(app.config)),
        configinspector.NewModule(configInspectorConfig(app.config)),
        pprof.NewModule(pprofConfig(app.config)),

        health.NewModule(),
    ); err != nil {
        return err
    }
    return app.lifecycle(ctx)
}

func (app *App) lifecycle(ctx context.Context) error {
    if err := app.modules.Activate(); err != nil { return err }
    if err := app.modules.Wire(app.provideCrossCutting); err != nil {
        return fmt.Errorf("wire modules: %w", err)
    }
    if app.config.Migrate {
        if _, err := runModuleMigrations(ctx, app.Db.Write(), app.modules,
            MigrationConfig{Action: MigrationAuto}); err != nil {
            return fmt.Errorf("migrate modules: %w", err)
        }
    }
    if err := app.modules.Initialize(ctx); err != nil { return fmt.Errorf("initialize modules: %w", err) }
    if err := app.modules.Start(ctx); err != nil { return fmt.Errorf("start modules: %w", err) }
    return nil
}
```

### 3.4 `internal/app/moduleconfig.go` (new)

The only place AppConfig maps to module configs. `AppConfig` never imports module
packages (module packages cannot import `internal/app` — import cycle), so this
file is the wiring seam:

```go
func telemetryConfig(c AppConfig) telemetry.Config {
    return telemetry.Config{
        Config: module.Config{Name: c.Name, Version: c.Version},
        OTLP:   telemetry.OTLPConfig{Enabled: c.Telemetry.OTLP.Enabled, /* ... */},
    }
}
// metricsConfig / auditConfig / datasourceMonitorConfig / migrationStatusConfig /
// configInspectorConfig / pprofConfig — same shape
```

### 3.5 `internal/app` — thin remaining surface

```go
// app.go: App drops telemetry/databaseMonitor/audit/metrics fields;
//         mods/initialized/started/services replaced by `modules *module.Registry`.
// module.go: keeps healthCheckers(), checkHealth(); adds
//         provideCrossCutting(), migrationInspector(), metricsCollectors(), moduleOptions().
// http.go:   router.Use(app.modules.HTTPMiddlewares()...); no concrete module fields.
// grpc.go:   GRPCProtocolOptions moved into Registry (logic unchanged).
// migration.go: runModuleMigrations consumes registry.Modules().
```

`addModule` is an unexported thin wrapper:

```go
func (app *App) addModule(mods ...module.Module) error {
    return app.modules.Register(mods...)
}
```

---

## 4. Per-module changes

### 4.1 Cross-cutting views (wire-time services)

`app.provideCrossCutting` is the one app-specific aggregation seam; it runs
inside `Wire` before the registry is sealed:

```go
func (app *App) provideCrossCutting(services *module.Services) error {
    module.Provide[healthservice.CheckerProvider](services, app.healthCheckers) // existing method, lazy
    module.Provide[migrationstatus.Inspector](services, app.migrationInspector) // new method, lazy
    module.Provide[metrics.CollectorSource](services, app.metricsCollectors)    // new method
    return nil
}
```

### 4.2 databasemonitor

- New narrow contract `Monitor { Snapshot(); SnapshotStale(); QueryMetrics() }`,
  provided via `ProvideServices` (mirrors `wuid.Source` / `guid.Source` pattern).
- Query-observer registration moves from bootstrap into its `Initialize`
  (gated on `ExposeHTTP`, which equals the current `DatasourceInspector.Enabled`
  gate → behavior preserved).

### 4.3 metrics

- `Config` embeds `module.Config` (`Enabled`); implements `Enabler`.
- Constructor no longer takes the monitor instance: `NewModule(db, Config)`.
- `InjectServices` resolves `databasemonitor.Monitor` + `metrics.CollectorSource`,
  builds the datasource collector and registers every active module's collector.

### 4.4 Bug fix — collector aggregation decoupled from registration order

Before: bootstrap loop at metrics-construction time → misses later modules.

After: `Registry.Wire` computes the collector source over the **complete active
set** and metrics injects it. Registration order no longer matters.

```go
func (app *App) metricsCollectors() []prometheus.Collector {
    var collectors []prometheus.Collector
    for _, m := range app.modules.Modules() {
        if c, ok := m.(module.PrometheusCollectorModule); ok {
            collectors = append(collectors, c.Collector())
        }
    }
    return collectors
}
```

### 4.5 migrationstatus / configinspector / pprof

- `Config` embeds `module.Config`; implement `Enabler`.
- migrationstatus: `NewModule(Config)`; `InjectServices` resolves
  `migrationstatus.Inspector` (app-provided).
- configinspector: `NewModule(value, Config)` no longer returns an error —
  the snapshot is built in `Initialize` (still fails startup, earlier than HTTP).
- pprof: `NewModule(Config)`; uses `cfg.Name` for the UI title.

### 4.6 health

- `NewModule()` zero-arg; `InjectServices` resolves `healthservice.CheckerProvider`
  and builds the service. Lazy evaluation per request is preserved (the provided
  value is `app.healthCheckers`, a method value).

---

## 5. Behavioral notes (intentional, must be tested)

| # | Change | Impact |
|---|---|---|
| B1 | Metrics collector aggregation now order-independent | Fixes the latent ordering bug; behavior becomes correct instead of accidentally-correct |
| B2 | Query-observer registration moves from bootstrap (pre-migrate) to `Initialize` (post-migrate) | Migration SQL no longer appears in diagnostics/SQL stats |
| B3 | Middleware layering expressed by registration order; metrics registered before telemetry/audit | Final order unchanged: metrics → telemetry → audit → recovery |
| B4 | Conditional modules (pprof/metrics/migrationstatus/configinspector) self-declare `Enabled()` | When disabled they are absent from `app.mods`; health list / middleware / collectors all reflect the active set |
| B5 | `App` concrete module fields removed | http.go and tests resolve modules via the registry / services |

---

## 6. Test plan

- **Existing tests updated** (not weakened):
  - `TestBootstrapAddsEnabledHTTPModules`: expected order changes (metrics before telemetry).
  - `TestBootstrapRegistersAuditRecorderWhenDisabled`: resolve audit via registry/services instead of `app.audit`.
  - `TestBootstrapWithoutWriterReportsUnhealthy`: health output must stay identical.
  - `module_test.go` phase-runner tests migrate to `app.modules.*`.
- **New tests**:
  - Registry: duplicate name, empty name, nil module, dependency-order validation at `Register`.
  - `Activate`: disabled modules excluded; health list unaffected by disabled modules.
  - Collector aggregation: a `PrometheusCollectorModule` registered *after* metrics is still collected (regression test for the bug).
  - Middleware order: `HTTPMiddlewares()` yields metrics → telemetry → audit.
  - Wire-time service views resolve for health / migrationstatus / metrics.
- **Manual verification**: run app with `--debug`, hit `/debug/migrations`, `/metrics`,
  `/debug/datasources`, health endpoint; verify collector presence and middleware order.

---

## 7. Risks & open questions

1. **`module.Options` dropped in favor of base `module.Config{Enabled,Name,Version}`.**
   Modules that need neither Name/Version nor Enabled still embed the base — accepted.
2. **DB stays a direct constructor parameter** (shape differs per module: `gormx.Writer`
   vs full `*gormx.DatasourceRouter`). Not part of the base Config. Confirmed.
3. **`Registry` placement in `pkg/module`** makes it a reusable framework artifact.
   `pkg/module` currently imports only external libs + `internal/infra/health/service`?
   — the health aggregation stays in `internal/app` via the wire callback, so
   `pkg/module` does **not** import infra packages. Verify this stays true.
4. **Config-inspector snapshot timing** moves to `Initialize`; error surface unchanged.
5. **Scope:** this is a refactor of the module runtime. Business modules are out of
   scope; the placeholder sections for them are removed (they simply join `addModule`).

---

## 8. Out of scope

- Business module implementations.
- Moving `AppConfig` (import cycle prevents it) — the `moduleconfig.go` seam is the answer.
- Adopting an external DI framework or code generation (rejected, see §9).
- Changing the service registry's reflect-based resolution to constructor params
  (a larger "no reflection" migration; deferred — see §9).

---

## 9. Alternatives considered

- **Kratos / Google Wire (compile-time DI + codegen).** Strong graph verification,
  but: cannot express runtime conditional enable/disable; does not solve
  registry-wide aggregation (collectors / health / migration over the whole set);
  adds a tool dependency + generated files for a single ~14-module app. Borrowed
  ideas instead: early validation, reverse-order cleanup guarantee, thin runtime
  over declared components, and *explicit per-constructor dependencies* (which is
  why the base `Config` stays minimal and DB is not a bag field).
- **Full runtime DI container (uber/fx style).** Rejected: reflection-heavy,
  new dependency, contradicts the project's no-unnecessary-dependencies rule.
- **Keep concrete `App` fields** for protocol middleware. Rejected: keeps the
  "app stores module handles" smell; `HTTPMiddlewareModule` + registry collection
  is uniform.
- **Internal self-gating** (always register, no-op when disabled). Rejected:
  disabled modules would still appear in health lists / middleware collection;
  `Conditional`/`Enabler` filtering is explicit.
