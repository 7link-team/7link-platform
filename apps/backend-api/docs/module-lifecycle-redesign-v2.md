# Module Lifecycle Redesign — Design Document (v2)

> Status: **proposal under review (v2)**. v2 incorporates the consolidated findings
> of a 7-angle subagent review (architecture / QA / framework / engineering
> governance / microservices / modularity / plugin-ability). It is the review
> artifact for a codex CLI (third-party LLM) pass.
>
> v1 → v2 delta is documented in §7 (Review synthesis) and §8 (Open decisions).

---

## 1. Context & problem (unchanged from v1)

`internal/app/bootstrap.go` does five jobs at once: database bootstrap, module
construction, conditional registration, cross-module wiring, and orchestration.
Concrete defects:

1. **Ordering bug (confirmed).** Metrics collector aggregation iterates `app.mods`
   at metrics-construction time; any `PrometheusCollectorModule` registered after
   that point is silently missed. Only works today because audit happens to be
   registered before metrics.
2. **Cross-module construction coupling.** `metrics.NewModule(app.Db, app.databaseMonitor)`
   forces construction ordering on a concrete instance.
3. **Scattered module state.** `app.mods / initialized / started / services` are
   four parallel fields; phase runners live as `App` methods; http.go reaches into
   concrete `app.telemetry / audit / metrics / databaseMonitor` fields.
4. **Closure wiring.** `health.NewModule(app.healthCheckers)` and
   `migrationstatus.NewModule(func(ctx){... app.mods ...})` thread the module list
   into constructors.

## 2. Goal & authorization

> `bootstrap` becomes a pure **bill of materials**: `initializeDatabase` + one
> `addModule(...)` declaration. Selection (enable/disable), wiring, migration,
> monitoring, and orchestration are owned by the **module lifecycle**. Each module
> is constructed exactly once; the lifecycle manages it uniformly.

**Authorization note.** The redesign is a refactor (~10 packages / 30+ files), not
only a bug fix. The confirmed bug (collector ordering) is decoupled into a
standalone **Stage 0** so it can be fixed and verified independently of the
architecture migration (see §12). The architecture migration itself is delivered in
stages where each stage compiles, tests, and rolls back independently.

## 3. Design

```
bootstrap.go                         lifecycle (Registry-driven)
┌─────────────────────────────┐
│ initializeDatabase           │
│ addModule( ... bill of ... ) │ → Registry.Register(...)   single-module invariants
│   materials ... )            │ → Registry.Activate()      filter Enabled() + active-set dep check
│                              │ → Registry.Wire(cb)        services + app views
│ lifecycle(ctx)               │ → migrate (if config.Migrate)
└─────────────────────────────┘ → Registry.Initialize(ctx)
                                 → Registry.Start(ctx)
```

### 3.1 `pkg/module` — base config + activation (revised)

The base config carries **only the activation switch** — the one genuinely common,
lifecycle-owned concern. It is embedded **only** by conditional modules. App
identity (Name/Version) is *not* part of the framework base: telemetry/audit/pprof
keep taking it as constructor arguments (preserves their functional-options test
seams and 4 external call sites — see §4.5).

```go
// Config is the base configuration inherited by conditional modules. It carries
// only the activation switch. Always-registered modules (audit, telemetry,
// database-monitor, health, ...) do NOT embed it and MUST NOT implement Enabler.
type Config struct {
    Enabled bool
}

// Enabler exposes the inherited activation switch to the lifecycle.
type Enabler interface {
    Module
    Enabled() bool
}

// HTTPMiddlewareModule contributes a server-wide HTTP middleware. Middlewares
// run in registration order: earlier module = outer middleware. The outermost
// module sees the request first; respx.Recovery is always appended innermost by
// the HTTP server.
type HTTPMiddlewareModule interface {
    Module
    HTTPMiddleware(next http.Handler) http.Handler
}
```

Conditional modules (metrics, migrationstatus, configinspector, pprof) embed
`module.Config` in their own config and implement `Enabled()` as a one-liner.

**Two distinct "enabled" axes (explicit).** `Enabler.Enabled` is the **activation**
axis — decides presence in the module container. `audit.Config.Enabled` is the
**operational** axis — decides whether the recorder captures/persists. audit is an
always-registered module, never an `Enabler`; its disabled behavior is a no-op
recorder (existing invariant, preserved and tested).

### 3.2 `pkg/module` — `Registry` (the lifecycle container)

```go
type Registry struct {
    entries     []*entry        // registration order
    services    *Services
    initialized []*entry        // completed Initialize / pre-seeded Deinitializer-only
    started     []*entry        // completed Start
    state       state           // ordering guards (see below)
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
func (r *Registry) RegisterHTTP(ctx, router, api) error
func (r *Registry) RegisterGRPC(reg grpc.ServiceRegistrar)
func (r *Registry) HTTPMiddlewares() []func(http.Handler) http.Handler
func (r *Registry) GRPCProtocolOptions() []grpc.ServerOption
func (r *Registry) Services() *module.Services   // sealed instance, read-only after Wire
func (r *Registry) Modules() []Module            // defensive copy of active set
func (r *Registry) ByName(name string) (Module, bool)
```

**Validation split (review consensus).**
- `Register`: only single-module invariants — nil, empty name, duplicate name.
  Applies the shared `isNilModule` helper moved into `pkg/module`, extended to the
  `canBeNil` kind set (pointer/interface/func/map/slice) already used by `Services`.
- `Activate`: filters `!Enabled()` modules, then re-validates `Depender` existence
  **and** ordering against the **active** set, with clear errors:
  `"module %q requires unregistered module %q"` / `"module %q must be registered
  after dependency %q"` (existing observable strings preserved verbatim).

**State guards.** A small state enum rejects out-of-order calls: `Register` after
`Activate`, double `Wire`, `Initialize`/`Start` before `Activate`, `RegisterHTTP`
before `Wire`. Each multi-module `Register` batch validates as a whole before
appending, so a failing batch cannot leave a half-registered set.

**Rollback invariant preserved (review consensus).** `Wire` pre-seeds
`initialized` with any module that implements `Deinitializer` but not `Initializer`
(resources owned from construction time must always be released on shutdown).

**Failure contract.** `lifecycle` returns at the first failing phase; rollback is
the caller's job (`Run` already does `errors.Join(err, app.shutdown())`).
`Stop`/`Deinitialize` clear their `started`/`initialized` lists so a second
shutdown is idempotent (current behavior preserved).

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
        telemetry.NewModule(app.config.Name, app.config.Version, telemetry.WithConfig(telemetryConfig(app.config))),
        audit.NewModule(app.config.Name, app.config.Version, app.Db, auditConfig(app.config)),
        databasemonitor.NewModule(app.Db, datasourceMonitorConfig(app.config)),

        migrationstatus.NewModule(migrationStatusConfig(app.config)),
        configinspector.NewModule(app.config, configInspectorConfig(app.config)),
        pprof.NewModule(app.config.Name, pprofConfig(app.config)),

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

**Notable: telemetry/audit/pprof constructors are unchanged.** App identity stays
positional (review consensus — preserves test seams and external call sites).
`metricsConfig/migrationStatusConfig/configInspectorConfig/pprofConfig` map the
`Enabled` flag from AppConfig into the module's embedded `module.Config`; the
other module configs map their own fields. All mapping lives in
`internal/app/moduleconfig.go` (+ `moduleconfig_test.go`).

### 3.4 `internal/app` — thin remaining surface

```go
// app.go: App drops telemetry/databaseMonitor/audit/metrics fields;
//         mods/initialized/started/services → `modules *module.Registry`.
// module.go: keeps healthCheckers(), checkHealth(); adds
//         provideCrossCutting(), migrationInspector(), metricsCollectors().
// http.go:   router.Use(app.modules.HTTPMiddlewares()...) then respx.Recovery.
// grpc.go:   GRPCProtocolOptions moved into Registry (logic unchanged).
// migration.go: runModuleMigrations consumes registry.Modules().
```

`addModule` is an unexported thin wrapper over `Registry.Register`.

## 4. Per-module changes

### 4.1 Cross-cutting views (wire-time services)

```go
func (app *App) provideCrossCutting(services *module.Services) error {
    module.Provide[healthservice.CheckerProvider](services, app.healthCheckers) // existing method, lazy
    module.Provide[migrationstatus.Inspector](services, app.migrationInspector) // new, lazy
    module.Provide[metrics.CollectorSource](services, app.metricsCollectors)    // new
    return nil
}
```

### 4.2 databasemonitor

- New narrow contract `Monitor { Snapshot(); SnapshotStale(); QueryMetrics() }`,
  provided via `ProvideServices`.
- Query-observer registration moves from bootstrap into `Initialize`, gated on
  `ExposeHTTP`. **Mechanism (review gap resolved):** `Initialize` re-asserts the
  router through a minimal interface — `if r, ok := m.datasources.(interface{ RegisterQueryObserver(gormx.QueryObserver) error }); ok` — so the narrow `datasource`
  test interface stays unchanged and the concrete router registers the module
  (which already implements `gormx.QueryObserver`).

### 4.3 metrics

- `Config` embeds `module.Config` (`Enabled`); implements `Enabler`.
- Constructor drops the monitor instance: `NewModule(db, Config)`.
- `InjectServices` resolves `databasemonitor.Monitor` + `metrics.CollectorSource`.
  **Lenient on Monitor**: a missing monitor skips the datasource collector instead
  of failing Wire (preserves the current `NewModule(nil)`-style optional semantics;
  QA review question resolved).
- `Collector()` on other modules is documented as "must be safe to call right
  after construction" (it is called at Wire time, before Initialize).

### 4.4 Bug fix — collector aggregation decoupled from registration order

`Registry.Wire` runs after `Activate` (all modules registered). The collector
source is computed over the complete active set and injected into metrics.
Registration order no longer matters. `prometheus.Gather`-based regression tests
prove the collector actually reaches the registry output (§6.2).

### 4.5 telemetry / audit — constructors unchanged (review consensus)

Both keep `NewModule(name, version, ...options)` / `NewModule(name, version, db, cfg, ...options)`.
Rationale: `WithIDGenerator`, `WithSpanExporter`, `WithSinks` are test seams used
by 4 external packages (`pkg/extension/slog/trace_test.go`,
`pkg/extension/gorm/datasource_test.go`, `internal/infra/health/grpc/client_test.go`,
`internal/infra/audit/grpc_test.go`) plus the module packages' own tests; changing
them would widen the blast radius without design benefit. `migration.go`'s
`RunMigrations` call site is therefore untouched.

### 4.6 migrationstatus / configinspector / pprof

- `Config` embeds `module.Config`; implement `Enabler`.
- migrationstatus: `NewModule(Config)`; `InjectServices` resolves `migrationstatus.Inspector`.
- configinspector: `NewModule(value, Config)` no error (snapshot built in `Initialize`).
- pprof: `NewModule(name, Config)`; `Enabled` gates registration.

### 4.7 health

- `NewModule()` zero-arg; `InjectServices` resolves `healthservice.CheckerProvider`
  (a method value over the active set — lazy per request, as today).

### 4.8 capability visibility (modularity review)

The `migrationSourceProvider` structural interface moves to
`pkg/module.MigrationSourceProvider` so business modules can discover migration
capability alongside the other module contracts. No import-cycle impact (modules
just implement the method).

## 5. Behavioral notes (must be tested)

| # | Change | Impact |
|---|---|---|
| B1 | Metrics collector aggregation order-independent | Fixes the latent ordering bug |
| B2 | Query-observer registration moves from bootstrap (pre-migrate) to `Initialize` (post-migrate) | Migration SQL no longer appears in `/debug/sql` / `database_sql_*` metrics |
| B3 | Middleware layering by registration order; metrics registered before telemetry/audit | Final order unchanged: metrics → telemetry → audit → recovery |
| B4 | Conditional modules self-declare `Enabled()` | When disabled they are absent from `app.mods`; health / middleware / collectors reflect the active set |
| B5 | `App` concrete module fields removed | http.go and tests resolve modules via registry / services |
| B6 | `http.go` "prometheus module is unavailable" startup guard removed | Unreachable by construction (`metrics.Enabled` is derived from the same config flag); the guard's test is rewritten to assert the reachable invariant instead |

**pprof position note.** pprof moves from first in `app.mods` to near-last
(immediately before health). Affects `TestBootstrapAddsEnabledHTTPModules`
expected order and, when pprof is enabled, the health module list order.

## 6. Test plan (expanded)

### 6.1 `pkg/module` — pure Registry unit tests (`registry_test.go` + `module_test.go`)

- `Register`: nil (all `canBeNil` kinds), empty name, duplicate name — errors
  before any state change; batch is all-or-nothing.
- `Activate`: disabled modules excluded; **Depender re-validation on active set** —
  A depends on B, B disabled → `"module %q requires unregistered module %q"`;
  A and B both disabled → OK.
- `Wire`: provider-before-injector order independence; **Deinitializer-without-
  Initializer pre-seeded into `initialized`** (regression test for the preserved
  invariant); duplicate service key error names the colliding modules.
- Phase semantics migrated verbatim from `internal/app/module_test.go`:
  quiesce/stop/deinitialize reverse order + `errors.Join`; rollback only runs
  completed phases; `Stop`/`Deinitialize` idempotent (lists cleared).
- `Services()` returns the sealed instance; `Modules()` returns a copy;
  `ByName` → `(Module, bool)`.
- State guards: out-of-order phase calls rejected.

### 6.2 Collector aggregation regression (real metrics module)

`Register(metrics)` then `Register(fakeCollectorModule)` → `Activate` + `Wire` →
`prometheus.Gather` on the metrics handler **asserts the fake collector's metric
family appears**. Plus: metrics disabled → `CollectorSource` excludes collectors;
collector module itself disabled → excluded; missing `Monitor`/`CollectorSource`
→ `InjectServices` errors and `Wire` fails.

### 6.3 Middleware & protocol order

- App-level: `HTTPMiddlewares()` + `respx.Recovery` yields [metrics, telemetry,
  audit, recovery] — asserted via header-writing probe handlers, not just slice
  names.
- gRPC: `GRPCProtocolOptions()` with telemetry + audit + a fake interceptor asserts
  stats handler before interceptor modules, platform interceptors innermost.

### 6.4 Module-package test migration checklist (per review)

| Package | Affected tests | Action |
|---|---|---|
| metrics | `TestHTTPMiddlewareExportsRouteMetricsWithoutRawURLs`, `TestDatasourceMetricsIncludeNamedPoolState` | Rewrite for `NewModule(db, Config)` + `InjectServices` |
| migrationstatus | `module_test.go` constructor calls | New `Config` param |
| configinspector | `TestNewModuleRejectsUnsupportedConfiguration` | Error moves to `Initialize` |
| pprof | `module_test.go` constructor calls | New `Config` param |
| health | `TestHealthModuleLifecycleAndRegistration` | Build service via `InjectServices` |
| databasemonitor | — | Add `TestInitializeRegistersQueryObserverGatedOnExposeHTTP` (B2) |
| telemetry / audit | constructors unchanged | No migration; existing seams intact |

### 6.5 App-level invariants (updated, not weakened)

- `TestBootstrapWithoutWriterReportsUnhealthy`: health output identical.
- `TestBootstrapRegistersAuditRecorderWhenDisabled`: resolve via
  `app.modules.Services()`; **extended** to assert `audit` still appears in health
  list (UNKNOWN) and in `HTTPMiddlewares()` when disabled.
- `TestBootstrapAddsEnabledHTTPModules`: expected order updated (metrics before
  telemetry, pprof near-last); replace `app.mods` order assertion with behavior
  where feasible.
- `TestBootstrapMigratesBeforeStartingWhenEnabled` / `StartsAfterExplicitMigrations`: unchanged.
- `moduleconfig_test.go` (new): every AppConfig→module-Config mapping field has a
  propagation assertion (guards silent zero-value drift).

## 7. Review synthesis & dispositions

| # | Finding (source) | Disposition |
|---|---|---|
| 1 | Dep validation must run on active set after Activate (arch/gov/fw/mod/plugin) | **Adopted** §3.2 |
| 2 | audit must never be an Enabler; two enabled axes (fw/mod/plugin/gov) | **Adopted** §3.1, §4.5 |
| 3 | Base Config must not bundle identity + activation (fw/mod/plugin/ms) | **Adopted** — base = `{Enabled}` only, identity stays positional |
| 4 | Registry needs `Services()` accessor (arch/QA) | **Adopted** §3.2 |
| 5 | telemetry/audit options + external call sites must survive (arch/QA) | **Adopted** §4.5 — constructors unchanged |
| 6 | Deinitializer-only rollback invariant preserved + tested (arch/fw/QA) | **Adopted** §3.2, §6.1 |
| 7 | Error strings preserved verbatim (fw) | **Adopted** §3.2 |
| 8 | Test migration surface is large; per-module checklist (QA/gov/arch) | **Adopted** §6.4 |
| 9 | Staged landing, bug fix decoupled (gov) | **Adopted** §12 |
| 10 | Observer self-registration mechanism (arch/fw) | **Adopted** §4.2 — type-assert |
| 11 | `isNilModule` → pkg/module, widen nil kinds (fw) | **Adopted** §3.2 |
| 12 | `ByName → (Module, bool)`, `Modules()` copy (fw/arch) | **Adopted** §3.2 |
| 13 | B2/B3/B1 need real tests (QA) | **Adopted** §6.2–6.3 |
| 14 | "unavailable" guard disappears; pprof shifts (QA/gov/arch) | **Adopted** B6, §5, §6.5 |
| 15 | `MigrationSourceProvider` → pkg/module (mod) | **Adopted** §4.8 |
| 16 | Registry state guards / all-or-nothing batch (arch) | **Adopted** §3.2 |
| 17 | Cross-service infra reuse + contract lifting (ms) | **Deferred/out-of-scope** — §9, §10 |
| 18 | healthCheckerProvider contract-ization (mod) | **Noted as optional** follow-up; aggregate view retained |
| 19 | Registry `Provide[[]module.Module]` alternative (arch N1) | **Rejected** — centralizes aggregation in app, keeps `provideCrossCutting` |
| 20 | `module.Enable` embedded helper to fold 4× `Enabled()` (fw N1) | **Noted** — explicit one-liners preferred (boring obvious) |

## 8. Open decisions (for final pass)

1. **Identity carrier.** v2 keeps Name/Version as constructor args. A `pkg`-level
   `ServiceIdentity{Name, Version, InstanceID, Environment}` (microservices S1) is
   a cleaner cross-service carrier and also fixes audit's `os.Hostname()` instance
   ID (S3) — adopt now or defer? (Defer keeps this refactor minimal.)
2. **Health contract-ization** (modularity S2): lift databasemonitor's
   `HealthCheckers()` behind a service contract instead of the app-private
   `healthCheckerProvider` interface. Optional; marginal gain.
3. **http.go metrics guard (B6)**: confirmed removal is acceptable? Alternative is
   an Activate-time "config requests X but no module provides it" check.

## 9. Risks

1. Phase-runner migration is a large mechanical diff; staged landing (§12) keeps
   each stage reviewable and independently rollback-able.
2. `Activate`-time dependency re-validation is new failure surface — must not
   change observable error strings.
3. databasemonitor observer timing (B2) is a real observability change; the SQL
   diagnostics for migration no longer exist. Confirmed intentional.
4. Registry state guards add a small invariant layer; must be covered by unit tests
   or they become dead code.

## 10. Out of scope

- Business module implementations.
- Moving `AppConfig` (import cycle) — `moduleconfig.go` is the seam.
- Adopting an external DI framework / code generation (rejected, §11).
- **Cross-service reuse of `internal/infra/*` modules and contracts.** `pkg/module`
  itself remains importable, but the concrete infra modules and the three view
  contract types (`CheckerProvider`, `Inspector`, `CollectorSource`) live under
  `internal/` and are not shared across `go.mod` boundaries. A future shared
  carrier (lift contracts to `pkg/` or a shared module) is a separate, explicit
  decision. v2 does not claim "reusable across services" (§7.3 claim of v1 is
  corrected).
- Cross-service gRPC client lifecycle. Extension path exists (a "grpc-clients"
  module via `ServiceProvider` + `Stopper`), documented here for future work.

## 11. Alternatives considered (unchanged)

- **Kratos / Google Wire (compile-time DI + codegen).** Strong graph verification,
  but cannot express runtime conditional enable/disable; does not solve
  registry-wide aggregation; adds a tool dependency. Borrowed ideas instead: early
  validation, reverse-order cleanup, thin runtime over declared components,
  explicit per-constructor dependencies.
- **Full runtime DI container (uber/fx).** Rejected: reflection-heavy, new dependency.
- **Keep concrete `App` fields.** Rejected: keeps the module-handle smell;
  `HTTPMiddlewareModule` + registry collection is uniform.
- **Internal self-gating.** Rejected: disabled modules would appear in health
  lists / middleware; `Enabler` filtering is explicit.

## 12. Implementation plan (staged, each independently deliverable)

- **Stage 0 — minimal bug fix (no architecture change).** In the current code,
  move collector aggregation after all registrations (~15 lines) + regression
  test. Independent merge / rollback.
- **Stage 1 — pure move.** Add `pkg/module.Registry` carrying state + phase
  runners, App delegates; `AddModule` retained. Zero behavior change; diff is
  mechanical.
- **Stage 2 — behavior fixes.** `Activate` (Enabled filter + active-set dep
  check), `Wire` callback + `metricsCollectors()` fix (B1), state guards. Landing
  of §4.1/§4.4.
- **Stage 3 — per-module migration.** metrics/migrationstatus/configinspector/
  pprof Config + Enabler; databasemonitor Monitor + observer `Initialize`;
  health injector; `moduleconfig.go` + `moduleconfig_test.go` (B2/B4).
- **Stage 4 — cleanup.** Drop `App` fields, http.go via Registry (B3/B6), delete
  `// .. biz modules` placeholder, `AddModule` → `addModule`, update remaining
  implementation-coupled test assertions (B5).
