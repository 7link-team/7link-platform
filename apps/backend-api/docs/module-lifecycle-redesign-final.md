# Module Lifecycle Redesign — Final Design Document

> Status: **final, approved for record** (codex-reviewed, no further development planned yet).
>
> Provenance: v1 → 7-angle subagent review → v2 → codex CLI review #1 → services-injection
> direction → user simplification decision → **module.Runtime design** → codex CLI review #2
> (sound-with-changes, must-fixes incorporated).
>
> Repo: `7link-platform` · App: `apps/backend-api` · Scope: `internal/app`, `pkg/module`, infra modules.

---

## 1. Context & problem

`internal/app/bootstrap.go` does five jobs at once: database bootstrap, module
construction, conditional registration, cross-module wiring, orchestration.

Confirmed defects:

1. **Ordering bug (real).** Metrics collector aggregation iterates `app.mods` at
   metrics-construction time; a `PrometheusCollectorModule` registered later is
   silently missed. Works today only because audit is registered before metrics.
2. **Scattered module state.** `app.mods / initialized / started / services` are
   four parallel fields; phase runners live as `App` methods; http.go reaches into
   concrete `app.telemetry / audit / metrics / databaseMonitor` fields.
3. **Closure wiring.** `health.NewModule(app.healthCheckers)` and
   `migrationstatus.NewModule(func(ctx){... app.mods ...})` thread the module list
   into constructors.
4. **No uniform activation.** Conditional registration is four `if config.X.Enabled`
   branches in bootstrap.

## 2. Goal & design principles

> `bootstrap` becomes a pure **bill of materials**: `initializeDatabase` + one
> `addModule(...)`. The **module lifecycle** owns selection, wiring, migration,
> monitoring, and orchestration. Each module is constructed exactly once.

Principles:
1. **Modules are autonomous.** Each module owns its state, its config, and its
   capabilities; it requests only what it needs. No module references another
   module instance at construction.
2. **A shared environment object, not injection machinery.** App-level
   infrastructure (identity, datasource) travels in one `module.Runtime` value
   passed to constructors. No reflection, no service injection for
   infrastructure, no dual constructors.
3. **Aggregation is iteration.** Cross-cutting readers (metrics collectors, health
   checkers, migration) traverse the active module set — never cached at a point
   that predates the full registration.
4. **Verify before running.** Dependency/name/duplicate errors surface at
   registration/activation, not mid-flight.
5. **Zero-leak bootstrap.** `app.config.*` / `app.Db` appear in exactly one place
   (building `Runtime`); the `addModule` list references only `rt` + per-module config.
6. **No new external dependency.** No DI framework, no code generation.
7. **Preserve behavior.** Phase semantics, health output, middleware layering, the
   "audit always registered even when disabled" invariant, and degraded
   no-writer behavior are kept.

## 3. Design

### 3.1 `pkg/module` — `Runtime` (shared environment, by value)

The app builds this once; modules that need it receive it as a constructor
parameter and retain only the narrow capabilities they use.

```go
// Runtime is the immutable app-level environment shared by modules. Passed by
// value; never mutated after construction.
type Runtime struct {
    Name    string                  // app identity: telemetry / audit / pprof
    Version string
    DB      *gormx.DatasourceRouter // datasource router: dlock / wuid / audit / databasemonitor / metrics
    // Redis, Cache, ... are added only when a real module needs one (YAGNI).
}
```

`*gormx.DatasourceRouter` is the single DB type and serves every consumer:
`Write()` (dlock/wuid/audit), `Probe`/`DatasourceStats`/`DatasourceNames`
(databasemonitor), `DatasourceStats` (metrics), `RegisterQueryObserver`.
Import is cycle-free (`pkg/extension/gorm` does not import `pkg/module`).

> **Known tradeoff:** `pkg/module` becomes database-specific by importing the gorm
> extension. A neutral `pkg/appenv` package would preserve the boundary but adds a
> package for a 3-field struct; deferred unless `Runtime` grows.
>
> Modules keep narrow interfaces internally (e.g. dlock stores a `gormx.Writer`),
> so a concrete router in `Runtime` does not force them to depend on the full
> router shape.

### 3.2 `pkg/module` — base config + activation

```go
// Config is the base configuration inherited by conditional modules. It carries
// only the activation switch. Always-registered modules do NOT embed it and MUST
// NOT implement Enabler.
type Config struct {
    Enabled bool
}

// Enabler exposes the activation switch to the lifecycle. Pure capability
// interface (asserted against module.Module values), so it does not embed Module.
type Enabler interface {
    Enabled() bool
}
```

**Two distinct "enabled" axes.** `Enabler.Enabled` = activation (presence in the
container). `audit.Config.Enabled` = operational (recorder capture/persist).
audit is never an `Enabler`; its disabled behavior is a no-op recorder (invariant).

### 3.3 `pkg/module` — `Registry` (lifecycle container)

```go
type Registry struct {
    entries     []*entry        // registration order
    services    *Services       // cross-module service registry (existing)
    initialized []*entry        // rollback list: Deinitializer modules + completed Initialize
    started     []*entry        // completed Start
    state       state           // ordering guards
}

func NewRegistry() *Registry
func (r *Registry) Register(mods ...Module) error
func (r *Registry) Activate() error
func (r *Registry) Wire(provide func(*Services) error) error
func (r *Registry) Initialize(ctx) error
func (r *Registry) Start(ctx) error
func (r *Registry) Quiesce(ctx) error
func (r *Registry) Stop(ctx) error
func (r *Registry) Deinitialize(ctx) error
func (r *Registry) RegisterHTTP(ctx, api huma.API) error      // huma-only
func (r *Registry) RegisterGRPC(reg grpc.ServiceRegistrar) error
func (r *Registry) HTTPMiddlewares() []func(http.Handler) http.Handler
func (r *Registry) GRPCProtocolOptions() []grpc.ServerOption
func (r *Registry) RawHTTPRoutes() []RawRoute
func (r *Registry) Services() *Services                        // sealed instance, read-only
func (r *Registry) Modules() []Module                          // active set, defensive copy
func (r *Registry) ByName(name string) (Module, bool)
```

**Validation split.** `Register`: nil (shared `isNilModule` in `pkg/module`,
covering the `canBeNil` kind set), empty name, duplicate name; batch validates as
a whole before appending. `Activate`: filters `!Enabled()`, then re-validates
`Depender` existence and order against the **active** set. Existing observable
error strings preserved verbatim.

**Rollback pre-seed at Register (codex must-fix).** Every module implementing
`Deinitializer` is added to the rollback list the moment it is registered — a
`Deinitialize` means the module owns resources from construction (telemetry
creates its tracer provider and mutates OTel globals in `NewModule`). This closes
the leak window for failures in `Register`/`Activate`/`Wire`/collector-wiring,
before any phase list exists. Contract: **`Deinitialize` must be nil-safe and
idempotent** (it may run before `Initialize`).

**State machine.** `Register → Activate → Wire → (Initialize → Start)* →
RegisterHTTP/GRPC`; shutdown `Quiesce → Stop → Deinitialize`. Guards reject
out-of-order calls; `Stop`/`Deinitialize` are idempotent and legal from every
failed state.

### 3.4 `internal/app` — bootstrap as a bill of materials

```go
func (app *App) bootstrap(ctx context.Context) error {
    app.modules = module.NewRegistry()
    if err := app.initializeDatabase(ctx); err != nil {
        return err
    }
    rt := app.runtime()   // the ONLY place app.config.* / app.Db appear
    if err := app.addModule(
        i18n.NewModule(i18n.Base),
        respx.NewModule(),
        dlock.NewModule(rt),
        wuid.NewModule(rt),
        guid.NewModule(),

        // metrics before telemetry/audit keeps its HTTP middleware outermost
        metrics.NewModule(rt, metricsConfig(app.config)),
        telemetry.NewModule(rt, telemetry.WithConfig(telemetryConfig(app.config))),
        audit.NewModule(rt, auditConfig(app.config)),
        databasemonitor.NewModule(rt, datasourceMonitorConfig(app.config)),

        migrationstatus.NewModule(migrationStatusConfig(app.config)),
        configinspector.NewModule(app.config, configInspectorConfig(app.config)),
        pprof.NewModule(rt, pprofConfig(app.config)),

        health.NewModule(),
    ); err != nil {
        return err
    }
    return app.lifecycle(ctx)
}

func (app *App) runtime() module.Runtime {
    return module.Runtime{Name: app.config.Name, Version: app.config.Version, DB: app.Db}
}
```

### 3.5 `internal/app` — lifecycle

```go
func (app *App) lifecycle(ctx context.Context) error {
    if err := app.modules.Activate(); err != nil { return err }
    if err := app.modules.Wire(app.provideCrossCutting); err != nil {
        return fmt.Errorf("wire modules: %w", err)
    }
    if err := app.collectMetrics(); err != nil {           // after Wire: rollback list exists
        return fmt.Errorf("collect module metrics: %w", err)
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

**Collector aggregation (bug fix, by iteration).** After `Wire` (rollback list
established), traverse the active set; every active `CollectorSink` receives every
active `PrometheusCollectorModule`'s collector. Zero sinks = no-op; multiple sinks
each receive all collectors. Registration order is irrelevant.

```go
func (app *App) collectMetrics() error {
    for _, sink := range app.modules.Modules() {
        s, ok := sink.(module.CollectorSink)
        if !ok { continue }
        for _, src := range app.modules.Modules() {
            c, ok := src.(module.PrometheusCollectorModule)
            if !ok { continue }
            if err := s.RegisterCollector(c.Collector()); err != nil {
                return err
            }
        }
    }
    return nil
}
```

`app.provideCrossCutting` now provides only the genuinely cross-module service
views (health checkers, migration inspector) — the collector view is gone.

### 3.6 HTTP transport decoupling (from review)

Modules never see a router. chi appears only in the app's HTTP server.

```go
type HTTPRegistrar interface {
    RegisterHTTP(ctx context.Context, api huma.API) error   // huma-only, adapter-agnostic
}

type RawHTTPHandlerModule interface {
    Module
    RawHTTPRoutes() []RawRoute                              // raw handlers: metrics/pprof/console
}

type RawRoute struct {
    Method  string
    Path    string
    Handler http.Handler
}
```

HTTP server: `HTTPMiddlewares()` then `respx.Recovery` then mount `RawHTTPRoutes`.
Middleware stays router-level (deliberate): huma middleware misses raw routes;
router level covers everything. The middleware value is stdlib-portable; only the
app-level mount is chi-bound.

## 4. Per-module changes

| Module | Constructor | Behavior |
|---|---|---|
| dlock / wuid | `NewModule(rt module.Runtime, options ...Option)` | stores narrow `gormx.Writer` from `rt.DB`; nil `Write()` → degraded (existing) |
| telemetry | `NewModule(rt module.Runtime, options ...Option)` | `rt.Name/Version` → OTel resource; unchanged construction-time provider/globals; **rollback pre-seed guarantees cleanup** |
| audit | `NewModule(rt module.Runtime, cfg Config, options ...ModuleOption)` | `rt.Name/Version/DB`; always registered; never an `Enabler` |
| databasemonitor | `NewModule(rt module.Runtime, cfg Config)` | `rt.DB` satisfies its `datasource` interface; observer registered in `Initialize` via a **required** `queryObserverRegistrar` capability (fail, not silently skip, when `ExposeHTTP` and the router lacks it) |
| metrics | `NewModule(rt module.Runtime, cfg Config)` | builds Prometheus registry at construction; implements `Enabler` + `CollectorSink`; `rt.DB` drives the datasource collector |
| migrationstatus | `NewModule(cfg Config)` | `Enabler`; `InjectServices` resolves the migration inspector service |
| configinspector | `NewModule(value any, cfg Config)` | `Enabler`; snapshot built in `Initialize` (no constructor error) |
| pprof | `NewModule(rt module.Runtime, cfg Config)` | `Enabler`; `rt.Name` used at route registration |
| health | `NewModule()` | `InjectServices` resolves `healthservice.CheckerProvider` |
| i18n / respx / guid | unchanged | do not take `Runtime` |

**External call-site impact (mechanical).** The 4 telemetry constructions in other
packages' tests change from `telemetry.NewModule("test", "1.0.0", ...)` to
`telemetry.NewModule(module.Runtime{Name: "test", Version: "1.0.0"}, ...)`.

**Standalone migration (`internal/app/migration.go`).** `RunMigrations` builds
`rt := module.Runtime{DB: router}` and passes it to `dlock.NewModule(rt)`,
`wuid.NewModule(rt)`, `audit.NewModule(rt, audit.Config{})`. `runModuleMigrations`
consumes `registry.Modules()` (the active set).

## 5. Behavioral notes

| # | Change | Impact |
|---|---|---|
| B1 | Collector aggregation order-independent (post-Wire iteration) | Fixes the latent bug |
| B2 | Observer registration in `Initialize` (post-migrate) | Migration SQL leaves `/debug/sql` / `database_sql_*` — **accepted** |
| B3 | Middleware layering by registration order | Final order unchanged: metrics → telemetry → audit → recovery |
| B4 | Conditional modules self-declare `Enabled()` | Disabled modules absent from active set; health/middleware/collectors reflect it |
| B5 | `App` concrete module fields removed | http.go/tests resolve via registry |
| B6 | "prometheus module is unavailable" guard removed | Unreachable by construction |
| B7 | Modules never receive a chi router | chi confined to the HTTP server |
| B8 | `NewModule(name, version, db, ...)` → `NewModule(rt, ...)` | 4 external test call sites + module tests update mechanically |

## 6. Test plan

### 6.1 `pkg/module` — Registry unit tests (`registry_test.go`)

- `Register`: nil (all `canBeNil` kinds), empty name, duplicate name; batch all-or-nothing.
- `Activate`: disabled excluded; **Depender re-validation on active set** (A dep B, B
  disabled → clear error; both disabled → OK).
- **Rollback pre-seed**: every Deinitializer module is in the rollback list from
  `Register`; cleanup runs on Register/Activate/Wire/Initialize/Start failure;
  `Deinitialize` nil-safe + idempotent from every state.
- `Wire`: provider-before-injector order independence; callback errors propagate;
  duplicate service key handling.
- Phase semantics migrated from `internal/app/module_test.go`: reverse order +
  `errors.Join`; rollback only for completed phases; `Stop`/`Deinitialize` clear
  their lists (second shutdown idempotent).
- `Services()` sealed instance; `Modules()` copy; `ByName → (Module, bool)`; state guards.

### 6.2 Collector aggregation (real metrics module)

`Register(metrics)` + `Register(fakeCollectorModule)` → Activate + Wire +
`collectMetrics` → `prometheus.Gather` asserts the fake collector's metric family
appears. Matrix: collector before AND after metrics (the bug regression);
collector module disabled; metrics disabled (zero sink → no-op); duplicate
collector; nil collector; multiple sinks each receive all; collector registration
failure rolls back cleanly.

### 6.3 Middleware & protocol order

- `HTTPMiddlewares()` + `respx.Recovery` = [metrics, telemetry, audit, recovery],
  asserted via header-probe handlers.
- gRPC: telemetry stats handler + audit interceptors + fake interceptor order;
  real RPC verifies multiple stats handlers compose.

### 6.4 Module-package test migration checklist

| Package | Notes |
|---|---|
| metrics | rewrite for `NewModule(rt, Config)` + `CollectorSink` |
| migrationstatus / configinspector / pprof | new `Config` param; configinspector error moves to `Initialize` |
| health | service built via `InjectServices` |
| dlock / wuid / audit / databasemonitor | narrow fakes no longer satisfy concrete `rt.DB` — use real SQLite routers or package-private constructor seams; lock degraded no-writer (nil `Write()`) behavior |
| telemetry / audit | 4 external call sites updated mechanically; functional options preserved |

### 6.5 App-level invariants (updated, not weakened)

- `TestBootstrapWithoutWriterReportsUnhealthy`: health output identical.
- `TestBootstrapRegistersAuditRecorderWhenDisabled`: resolve via
  `app.modules.Services()`; extended: audit still in health list (UNKNOWN) and
  `HTTPMiddlewares()` when disabled.
- `TestBootstrapAddsEnabledHTTPModules`: expected order updated (metrics before
  telemetry, pprof near-last); `app.mods` order assertion → behavior assertion.
- `TestBootstrapMigratesBeforeStartingWhenEnabled` / `StartsAfterExplicitMigrations`: unchanged.
- `moduleconfig_test.go` (new): every AppConfig→module-Config mapping field has a
  propagation assertion.

## 7. Review synthesis & dispositions

### 7.1 Subagent review (7 angles) → v2

Consensus adopted: dependency validation on the active set after `Activate`;
audit never an `Enabler`; base `Config` carries only `Enabled`; Registry needs
`Services()`; error strings preserved; per-module test migration checklist;
staged landing; `isNilModule` → pkg/module; `ByName → (Module, bool)`;
`MigrationSourceProvider` → pkg/module; cross-service reuse deferred.

### 7.2 codex review #1 (services-injection direction) → v3 revision

Identified: `provideCrossCutting` error propagation, rollback must cover
construction-owned state, `App.modules` init path, state-machine/rollback
reconciliation. The services-injection direction was then **replaced** by the
user's simplification decision: modules autonomous + `module.Runtime`
constructor object (see 7.3).

### 7.3 codex review #2 (Runtime design) — sound-with-changes

| # | Finding | Disposition |
|---|---|---|
| R1 | Collector wiring must run after rollback list exists, not right after Activate | **Adopted** — `collectMetrics` after `Wire` (§3.5) |
| R2 | telemetry's constructor side effects need early cleanup ownership | **Adopted** — Deinitializer pre-seed at `Register`, not `Wire` (§3.3) |
| R3 | `migration.go` standalone construction must build `Runtime{DB: router}` | **Adopted** (§4) |
| R4 | Preserve degraded no-writer behavior + lock with tests | **Adopted** (§6.4) |
| R5 | Rewrite the doc (old injection spec contradicted the revision) | **Done** |
| S1 | Cycle-free + concrete router serves all consumers — confirmed | Adopted |
| S2 | `pkg/appenv` neutral package vs `module.Runtime` DB coupling | **Deferred** — documented tradeoff (§3.1) |
| S3 | Pass `Runtime` by value, immutable; modules retain narrow capabilities only | **Adopted** (§3.1) |
| S4 | Module tests need real SQLite routers or package-private seams | **Adopted** (§6.4) |
| S5 | Collector test matrix | **Adopted** (§6.2) |
| N1 | Compile-time assertions for router satisfying narrow contracts | Noted |
| N2 | Don't pass `Runtime` to modules that don't need it | **Adopted** (§3.4) |

## 8. Decisions

- **B2**: accepted — migration SQL leaves diagnostics (observer registers post-migrate).
- **Q1 (external call sites)**: mechanical migration of 4 test constructions; no dual constructors.
- **Q2 (degraded no-writer)**: "service missing" = wiring error; "router present but
  `Write()==nil`" = runtime degraded (health NOT_SERVING), not a wiring failure.
- **Runtime location**: `pkg/module.Runtime` (value, immutable); `pkg/appenv` deferred.
- **gRPC stats handlers**: multiple supported (grpc-go appends/composes).
- Remaining open: Services duplicate-key error provenance (add or drop promise).

## 9. Risks

1. Phase-runner migration is large and mechanical — staged landing (§12).
2. `Activate`-time dependency re-validation is new failure surface — error strings
   must not change.
3. Rollback pre-seed means `Deinitialize` may run before `Initialize` — modules
   must be nil-safe/idempotent (telemetry verified).
4. chi confinement changes raw-route expression (pprof wildcards, redirects) —
   port carefully, keep OpenAPI docs.
5. `pkg/module` gains a gorm dependency — accepted, documented.

## 10. Out of scope

- Business modules; moving `AppConfig` (import cycle — `moduleconfig.go` is the seam).
- External DI framework / code generation (rejected, §11).
- Cross-service reuse of `internal/infra/*` and view contracts; cross-service gRPC
  client lifecycle.
- Migrating JSON debug endpoints to `huma.Register` (consistency follow-up).
- Redis/cache in `Runtime` (YAGNI).

## 11. Alternatives considered

- **Kratos / Google Wire / runtime DI (uber/fx):** rejected (conditional activation
  unexpressible, registry-wide aggregation unsolved, new dependency).
- **Services-registry injection of DB/identity (AppIdentity service, DB-as-service,
  dual constructors):** designed and codex-reviewed, then **rejected** as heavier
  than this codebase needs — `module.Runtime` gives the same zero-leak bootstrap
  with constructor-arg simplicity.
- **Keep concrete `App` fields:** rejected (module-handle smell).
- **Internal self-gating:** rejected (disabled modules would appear in health/middleware).
- **huma-native middleware:** rejected (misses raw routes; router level covers all).

## 12. Implementation plan (staged, independently deliverable)

- **Stage 0 — minimal bug fix (no architecture change).** In current code, move
  collector aggregation after all registrations (~15 lines) + `prometheus.Gather`
  regression test. Independent merge / rollback.
- **Stage 1 — pure move.** Add `pkg/module.Registry` (state + phase runners + rollback
  pre-seed at Register), App delegates. Zero behavior change.
- **Stage 2 — behavior fixes.** `Activate` (Enabled filter + active-set dep check),
  `collectMetrics` post-Wire (B1), state guards.
- **Stage 3 — Runtime + per-module migration.** `module.Runtime`; constructors take
  `rt`; conditional modules embed `module.Config{Enabled}` + `Enabler`;
  databasemonitor observer capability; health injector; `moduleconfig.go` +
  `moduleconfig_test.go` (B2/B4/B8).
- **Stage 4 — cleanup + transport decoupling.** Drop `App` fields, http.go via
  Registry (`HTTPMiddlewares` + `RawHTTPRoutes`, chi confined), delete
  `// .. biz modules` placeholder, `AddModule` → `addModule`, update remaining
  implementation-coupled assertions (B5/B7).
