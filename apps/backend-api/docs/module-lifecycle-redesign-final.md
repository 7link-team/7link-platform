# Module Lifecycle Redesign — Final Design Document

> Status: **implemented; pending final human acceptance**.
>
> Provenance: v1 → 7-angle subagent review → v2 → codex CLI review #1 → services-injection
> direction → user simplification decision → **shared appenv.Runtime design** → codex CLI review #2
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
2. **A shared environment object, not injection machinery.** The App creates one
   `*appenv.Runtime` for its lifetime. Infrastructure modules retain that same
   pointer; business/module services still use narrow `module.Services`
   contracts. No reflection, infrastructure service locator, or dual constructors.
3. **Aggregation is iteration.** Cross-cutting readers (metrics collectors, health
   checkers, migration) traverse the active module set — never cached at a point
   that predates the full registration.
4. **Verify before running.** Dependency/name/duplicate errors surface at
   registration/activation, not mid-flight.
5. **One infrastructure owner.** `App.runtime` is the only application-level
   infrastructure field; the `addModule` list references only `rt` + per-module config.
6. **No new external dependency.** No DI framework, no code generation.
7. **Preserve behavior.** Phase semantics, health output, middleware layering, the
   "audit always registered even when disabled" invariant, and degraded
   no-writer behavior are kept.

## 3. Design

### 3.1 `pkg/appenv` — `Runtime` (shared application environment)

The App builds and owns one instance. Infrastructure modules that need it retain
the same pointer. Manager references are private and cannot be replaced after
construction; the managers themselves own live routing, health, reconnection,
and pool state.

```go
// Runtime owns stable application-lifetime manager references.
type Runtime struct {
    identity Identity
    database *gormx.DatasourceRouter
}

type Identity struct { name, version string } // private; read through Name()/Version()
```

`*gormx.DatasourceRouter` is the single DB type and serves every consumer:
`Write()` (dlock/wuid/audit), `Probe`/`DatasourceStats`/`DatasourceNames`
(databasemonitor), `DatasourceStats` (metrics), `RegisterQueryObserver`.
`Runtime.Database()` exposes the stable router, not a selected `*gorm.DB`;
dlock/wuid/audit call `Write()` at operation or batch time. Redis/MQ/ES are added
as private manager references plus read-only accessors only when implemented.
`Runtime.Close()` releases managers once in reverse construction order, so App
shutdown does not grow per-manager branches. `Runtime` never contains module or
business services.

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
`Deinitializer` is added to the rollback list the moment it is registered. This
conservatively covers both construction-owned resources and resources acquired
before a failing `Initialize` returns. Contract: **`Deinitialize` must be
nil-safe and idempotent** (it may run before `Initialize`). Telemetry itself now
creates its provider and mutates OTel globals only in `Initialize`.

**State machine.** `Register → Activate → Wire → (Initialize → Start)* →
RegisterHTTP/GRPC`; shutdown `Quiesce → Stop → Deinitialize`. Guards reject
out-of-order calls; `Stop`/`Deinitialize` are idempotent and legal from every
failed state.

### 3.4 `internal/app` — bootstrap as a bill of materials

```go
func (app *App) bootstrap(ctx context.Context) error {
    if err := app.prepare(ctx); err != nil { return err }
    return app.lifecycle(ctx)
}

func (app *App) prepare(ctx context.Context) error {
    if err := app.initializeRuntime(ctx); err != nil {
        return err
    }
    if err := app.composeModules(); err != nil { return err }
    return app.prepareModules()
}

func (app *App) composeModules() error {
    app.modules = module.NewRegistry()
    rt := app.runtime
    return app.addModule(
        i18n.NewModule(rt, i18n.Base),
        respx.NewModule(rt),
        dlock.NewModule(rt),
        wuid.NewModule(rt),
        guid.NewModule(rt),

        // The HTTP adapter resolves the metrics observer and mounts it outermost.
        metrics.NewModule(rt, metricsConfig(app.config)),
        telemetry.NewModule(rt, telemetry.WithConfig(telemetryConfig(app.config))),
        audit.NewModule(rt, auditConfig(app.config)),
        databasemonitor.NewModule(rt, datasourceMonitorConfig(app.config)),

        migrationstatus.NewModule(rt, migrationStatusConfig(app.config)),
        configinspector.NewModule(rt, app.config, configInspectorConfig(app.config)),
        pprof.NewModule(rt, pprofConfig(app.config)),

        health.NewModule(rt),
    )
}

func (app *App) initializeRuntime(ctx context.Context) error {
    router, err := newDatasourceRouter(ctx, app.config)
    // validate partial availability and close unusable routers ...
    app.runtime = appenv.NewRuntime(
        appenv.NewIdentity(app.config.Name, app.config.Version),
        appenv.WithDatabase(router),
    )
    return nil
}
```

### 3.5 `internal/app` — lifecycle

```go
func (app *App) prepareModules() error {
    if err := app.modules.Activate(); err != nil { return err }
    if err := app.modules.Wire(app.provideCrossCutting); err != nil {
        return fmt.Errorf("wire modules: %w", err)
    }
    if err := app.collectMetrics(); err != nil {           // after Wire: rollback list exists
        return fmt.Errorf("collect module metrics: %w", err)
    }
    return nil
}

func (app *App) lifecycle(ctx context.Context) error {
    if app.config.Migrate {
        if _, err := app.migrateModules(ctx,
            MigrationRequest{Action: MigrationAuto}); err != nil {
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

Modules never see a router. chi is confined to the selected HTTP adapter
assembly (`internal/app` and the current Huma chi adapter); changing the HTTP
engine does not change Module contracts or implementations.

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

HTTP server: resolve the optional metrics `HTTPObserver`, install its
adapter-owned middleware, apply `HTTPMiddlewares()`, then `respx.Recovery`, then
mount `RawHTTPRoutes`. Middleware stays router-level (deliberate): huma
middleware misses raw routes; router level covers everything. Module middleware
values are stdlib-portable; route mounting and route-pattern extraction belong
to the selected HTTP adapter.

## 4. Per-module changes

| Module | Constructor | Behavior |
|---|---|---|
| dlock / wuid | `NewModule(rt *appenv.Runtime, options ...Option)` | retains `rt`; resolves `rt.Database().Write()` for each lock/worker lifecycle; nil writer → degraded |
| telemetry | `NewModule(rt *appenv.Runtime, options ...Option)` | retains `rt` and config only; `Initialize` creates the OTel resource/provider and publishes globals; nil-safe/idempotent cleanup |
| audit | `NewModule(rt *appenv.Runtime, cfg Config, options ...ModuleOption)` | retains `rt`; identity + routed database sink; always registered; never an `Enabler` |
| databasemonitor | `NewModule(rt *appenv.Runtime, cfg Config)` | retains `rt`; database manager satisfies its `datasource` interface; observer registered in `Initialize` |
| metrics | `NewModule(rt *appenv.Runtime, cfg Config)` | retains `rt`; builds Prometheus registry; database manager drives datasource collector; provides an adapter-independent `HTTPObserver` |
| migrationstatus | `NewModule(rt *appenv.Runtime, cfg Config)` | retains `rt`; `Enabler`; `InjectServices` resolves the migration inspector service |
| configinspector | `NewModule(rt *appenv.Runtime, value any, cfg Config)` | retains `rt`; `Enabler`; snapshot built in `Initialize` (no constructor error) |
| pprof | `NewModule(rt *appenv.Runtime, cfg Config)` | `Enabler`; `rt.Identity().Name()` used at route registration |
| health | `NewModule(rt *appenv.Runtime)` | retains `rt`; `InjectServices` resolves `healthservice.CheckerProvider` |
| i18n / respx / guid | `NewModule(rt *appenv.Runtime, ...)` | retain the same App Runtime for a uniform lifecycle contract; current behavior otherwise unchanged |

**External call-site impact (mechanical).** Direct Module tests construct a test
Runtime and pass it through the same constructors. Tests that directly consume
telemetry instrumentation explicitly call `Initialize` before use.

**Standalone migration (`internal/app/migration.go`).** `MigrationConfig`
squashes the normal `AppConfig` together with a narrow `MigrationRequest`, so
serve and migrate consume the same application identity, middleware settings,
and datasource configuration. `RunMigrations` calls the same `App.prepare` as
service startup, then `App.migrateModules`, which operates only on active Modules
implementing `module.MigrationSourceProvider`. It exits through the normal
`App.shutdown` path without entering `Initialize` or `Start`.

## 5. Behavioral notes

| # | Change | Impact |
|---|---|---|
| B1 | Collector aggregation order-independent (post-Wire iteration) | Fixes the latent bug |
| B2 | Observer registration in `Initialize` (post-migrate) | Migration SQL leaves `/debug/sql` / `database_sql_*` — **accepted** |
| B3 | Adapter metrics middleware outside module middleware registration order | Final order unchanged: metrics → telemetry → audit → recovery |
| B4 | Conditional modules self-declare `Enabled()` | Disabled modules absent from active set; health/middleware/collectors reflect it |
| B5 | `App` concrete module fields removed | http.go/tests resolve via registry |
| B6 | "prometheus module is unavailable" guard removed | Unreachable by construction |
| B7 | Modules never receive or import a chi router | chi confined to the selected HTTP adapter assembly |
| B8 | every application Module constructor receives the shared `rt` | one explicit App-lifetime environment is available throughout every Module lifecycle |

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

- `mountHTTPMiddlewares` resolves the metrics observer, then composes Module
  middleware and recovery = [metrics, telemetry, audit, recovery]. Route-pattern
  labels and empty-method raw routes are asserted at the app adapter boundary.
- gRPC: telemetry stats handler + audit interceptors + fake interceptor order;
  real RPC verifies multiple stats handlers compose.

### 6.4 Module-package test migration checklist

| Package | Notes |
|---|---|
| metrics | rewrite for `NewModule(rt, Config)` + `CollectorSink` |
| migrationstatus / configinspector / pprof | new `Config` param; configinspector error moves to `Initialize` |
| health | service built via `InjectServices` |
| dlock / wuid / audit / databasemonitor | dlock/wuid tests use a real `Runtime + SQLite router`; databasemonitor narrow fakes are assembled only in `_test.go`; lock degraded no-writer (nil `Write()`) behavior |
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
user's simplification decision: modules autonomous + shared `*appenv.Runtime`
constructor object (see 7.3).

### 7.3 codex review #2 (Runtime design) — sound-with-changes

| # | Finding | Disposition |
|---|---|---|
| R1 | Collector wiring must run after rollback list exists, not right after Activate | **Adopted** — `collectMetrics` after `Wire` (§3.5) |
| R2 | telemetry's constructor side effects need early cleanup ownership | **Adopted** — Deinitializer pre-seed at `Register`, not `Wire` (§3.3) |
| R3 | standalone migration must not maintain a second Router/Runtime/Registry/Module construction path | **Adopted** — both modes use `App.prepare` (§4) |
| R4 | Preserve degraded no-writer behavior + lock with tests | **Adopted** (§6.4) |
| R5 | Rewrite the doc (old injection spec contradicted the revision) | **Done** |
| S1 | Cycle-free + concrete router serves all consumers — confirmed | Adopted |
| S2 | `pkg/appenv` neutral package vs `pkg/module` DB coupling | **Adopted** after the runtime became an application-lifetime manager container (§3.1) |
| S3 | Share one stable Runtime pointer; manager references are private and read-only | **Adopted** (§3.1) |
| S4 | Module tests need real SQLite routers or package-private seams | **Adopted** (§6.4) |
| S5 | Collector test matrix | **Adopted** (§6.2) |
| N1 | Compile-time assertions for router satisfying narrow contracts | Noted |
| N2 | Omit `Runtime` from modules that do not currently use it | **Rejected by the uniform lifecycle decision** — every application Module retains the shared Runtime (§3.4, B8) |

## 8. Decisions

- **B2**: accepted — migration SQL leaves diagnostics (observer registers post-migrate).
- **Q1 (external call sites)**: mechanical migration of 4 test constructions; no dual constructors.
- **Q2 (degraded no-writer)**: "service missing" = wiring error; "router present but
  `Write()==nil`" = runtime degraded (health NOT_SERVING), not a wiring failure.
- **Runtime location**: `pkg/appenv.Runtime`; App owns one pointer for its lifetime.
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
5. Runtime becoming a module/business service locator — prevented by private manager fields and keeping module collaboration in `module.Services`.

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
  than this codebase needs — `*appenv.Runtime` gives the same zero-leak bootstrap
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
- **Stage 3 — Runtime + per-module migration.** `*appenv.Runtime`; constructors take
  `rt`; conditional modules embed `module.Config{Enabled}` + `Enabler`;
  databasemonitor observer capability; health injector; `moduleconfig.go` +
  `moduleconfig_test.go` (B2/B4/B8).
- **Stage 4 — cleanup + transport decoupling.** Drop `App` fields, http.go via
  Registry (`HTTPMiddlewares` + `RawHTTPRoutes`, chi confined), delete
  `// .. biz modules` placeholder, `AddModule` → `addModule`, update remaining
  implementation-coupled assertions (B5/B7).
