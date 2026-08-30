# Module Lifecycle 重构 — 最终设计文档（中文版）

> 状态：**最终定稿**（已经 codex 评审，暂不开发）。
>
> 演进：v1 → 7 角度 subagent 评审 → v2 → codex CLI 评审 #1 → 服务注入方向 → 用户简化决策 →
> **module.Runtime 设计** → codex CLI 评审 #2（sound-with-changes，must-fix 已并入）。
>
> 仓库：`7link-platform` · 应用：`apps/backend-api` · 范围：`internal/app`、`pkg/module`、infra 模块。

---

## 1. 背景与问题

`internal/app/bootstrap.go` 一个人承担了五件事：数据库引导、模块构造、条件注册、跨模块接线、编排。

已确认的缺陷：

1. **顺序 bug（真实存在）**。metrics 的 collector 聚合在 metrics 构造时遍历 `app.mods`；之后注册的 `PrometheusCollectorModule` 会被静默漏掉。今天能跑通，只是因为 audit 恰好注册在 metrics 之前。
2. **模块状态分散**。`app.mods / initialized / started / services` 是四块平行字段；phase runner 以 `App` 方法形式存在；http.go 直接拿 `app.telemetry / audit / metrics / databaseMonitor` 具体字段。
3. **闭包接线**。`health.NewModule(app.healthCheckers)` 和 `migrationstatus.NewModule(func(ctx){... app.mods ...})` 把整个模块列表塞进构造函数。
4. **无统一激活**。条件注册是 bootstrap 里四个 `if config.X.Enabled` 分支。

## 2. 目标与设计原则

> `bootstrap` 变成一张纯粹的**物料清单**：`initializeDatabase` + 一次 `addModule(...)`。**module lifecycle** 接管选择、接线、迁移、监控、编排。每个模块只构造一次。

原则：
1. **模块自治**。每个模块拥有自己的状态、配置、能力；只请求它需要的东西。构造期不引用另一个模块实例。
2. **共享环境对象，而非注入机器**。app 级基础设施（身份、数据源）用一个 `module.Runtime` 值传给构造函数。无反射、无基础设施服务注入、无双构造器。
3. **聚合即遍历**。跨模块读取（metrics collector、health checkers、migration）遍历激活集——绝不在早于完整注册的时点缓存。
4. **运行前验证**。依赖/名字/重复错误在注册/激活时报出，而非运行中途。
5. **零泄漏 bootstrap**。`app.config.*` / `app.Db` 只在一处出现（构建 `Runtime`）；`addModule` 列表只引用 `rt` + 各模块 Config。
6. **无新外部依赖**。无 DI 框架、无代码生成。
7. **保留行为**。阶段语义、health 输出、中间件分层、"audit 禁用也注册"不变式、无 writer 降级行为——全部保留。

## 3. 设计

### 3.1 `pkg/module` — `Runtime`（共享环境，按值传递）

app 构建一次；需要的模块在构造时收到，只保留自己用的窄能力。

```go
// Runtime 是模块共享的不可变 app 级环境。按值传递，构造后不再变更。
type Runtime struct {
    Name    string                  // app 身份：telemetry / audit / pprof
    Version string
    DB      *gormx.DatasourceRouter // 数据源 router：dlock / wuid / audit / databasemonitor / metrics
    // Redis、Cache…… 等真有模块要再加（YAGNI）
}
```

`*gormx.DatasourceRouter` 是唯一的 DB 类型，覆盖所有消费者：`Write()`（dlock/wuid/audit）、`Probe`/`DatasourceStats`/`DatasourceNames`（databasemonitor）、`DatasourceStats`（metrics）、`RegisterQueryObserver`。导入无循环（`pkg/extension/gorm` 不 import `pkg/module`）。

> **已知取舍**：`pkg/module` 因导入 gorm 扩展而带上了数据库特异性。中性包 `pkg/appenv` 能保住边界，但为一个 3 字段结构体多开一个包不划算；除非 `Runtime` 长大，否则推迟。
>
> 模块内部保持窄接口（如 dlock 存 `gormx.Writer`），所以 `Runtime` 里的具体 router 不会逼它们依赖整个 router 形状。

### 3.2 `pkg/module` — 基类配置与激活

```go
// Config 是条件模块继承的基类配置，只承载激活开关。常驻模块不嵌入它，
// 也绝不实现 Enabler。
type Config struct {
    Enabled bool
}

// Enabler 向生命周期暴露激活开关。纯能力接口（对 module.Module 断言），不嵌入 Module。
type Enabler interface {
    Enabled() bool
}
```

**两个不同的 "enabled" 语义轴。** `Enabler.Enabled` = 激活（是否存在于容器）。`audit.Config.Enabled` = 运营（recorder 捕获/持久化）。audit 绝不实现 `Enabler`；它禁用时是 no-op recorder（不变式）。

### 3.3 `pkg/module` — `Registry`（生命周期容器）

```go
type Registry struct {
    entries     []*entry        // 注册顺序
    services    *Services       // 跨模块服务注册表（既有）
    initialized []*entry        // 回滚表：Deinitializer 模块 + 已完成 Initialize
    started     []*entry        // 已完成 Start
    state       state           // 顺序守卫
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
func (r *Registry) RegisterHTTP(ctx, api huma.API) error      // 仅 huma
func (r *Registry) RegisterGRPC(reg grpc.ServiceRegistrar) error
func (r *Registry) HTTPMiddlewares() []func(http.Handler) http.Handler
func (r *Registry) GRPCProtocolOptions() []grpc.ServerOption
func (r *Registry) RawHTTPRoutes() []RawRoute
func (r *Registry) Services() *Services                        // 密封实例，只读
func (r *Registry) Modules() []Module                          // 激活集，防御性拷贝
func (r *Registry) ByName(name string) (Module, bool)
```

**校验拆分。** `Register`：nil（`pkg/module` 内共享 `isNilModule`，覆盖 `canBeNil` 全部类型）、空名、重名；批次先整体校验再追加。`Activate`：过滤 `!Enabled()`，然后对**激活集**重验 `Depender` 存在性与顺序。现有可观测错误串逐字保留。

**注册即预置回滚（codex must-fix）。** 每个实现 `Deinitializer` 的模块，在**注册那一刻**就进入回滚表——实现了 `Deinitialize` 意味着模块从构造起就持有资源（telemetry 在 `NewModule` 里建 tracer provider 并改动 OTel 全局）。这堵住了 `Register`/`Activate`/`Wire`/collector 接线失败时、任何阶段表尚不存在的泄漏窗。契约：**`Deinitialize` 必须 nil-safe 且幂等**（可能先于 `Initialize` 运行）。

**状态机。** `Register → Activate → Wire → (Initialize → Start)* → RegisterHTTP/GRPC`；关闭 `Quiesce → Stop → Deinitialize`。守卫拒绝乱序调用；`Stop`/`Deinitialize` 幂等且从每个失败状态都合法。

### 3.4 `internal/app` — bootstrap 作为物料清单

```go
func (app *App) bootstrap(ctx context.Context) error {
    app.modules = module.NewRegistry()
    if err := app.initializeDatabase(ctx); err != nil {
        return err
    }
    rt := app.runtime()   // app.config.* / app.Db 唯一出现的地方
    if err := app.addModule(
        i18n.NewModule(i18n.Base),
        respx.NewModule(),
        dlock.NewModule(rt),
        wuid.NewModule(rt),
        guid.NewModule(),

        // metrics 在 telemetry/audit 之前注册 → 其 HTTP 中间件保持最外层
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
    if err := app.collectMetrics(); err != nil {           // 在 Wire 之后：回滚表已建立
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

**Collector 聚合（bug 修复，靠遍历）。** `Wire` 之后（回滚表已建立）遍历激活集；每个激活的 `CollectorSink` 接收每个激活的 `PrometheusCollectorModule` 的 collector。零个 sink = no-op；多个 sink 各自接收全部。注册顺序无关。

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

`app.provideCrossCutting` 现在只提供真正跨模块的服务视图（health checkers、migration inspector）——collector 视图已移除。

### 3.6 HTTP 传输解耦（来自评审）

模块永远不接触 router。chi 只出现在 app 的 HTTP 服务里。

```go
type HTTPRegistrar interface {
    RegisterHTTP(ctx context.Context, api huma.API) error   // 仅 huma，适配器无关
}

type RawHTTPHandlerModule interface {
    Module
    RawHTTPRoutes() []RawRoute                              // raw handler：metrics/pprof/console
}

type RawRoute struct {
    Method  string
    Path    string
    Handler http.Handler
}
```

HTTP 服务：`HTTPMiddlewares()` → `respx.Recovery` → 挂载 `RawHTTPRoutes`。中间件保持 router 级（刻意）：huma 中间件会漏掉 raw 路由；router 级覆盖一切。中间件值 stdlib 可移植；只有 app 级挂载是 chi 绑定的。

## 4. 逐模块改动

| 模块 | 构造函数 | 行为 |
|---|---|---|
| dlock / wuid | `NewModule(rt module.Runtime, options ...Option)` | 从 `rt.DB` 存窄 `gormx.Writer`；`Write()` 为 nil → 降级（现状） |
| telemetry | `NewModule(rt module.Runtime, options ...Option)` | `rt.Name/Version` → OTel resource；构造期建 provider/全局不变；**注册即预置回滚保证清理** |
| audit | `NewModule(rt module.Runtime, cfg Config, options ...ModuleOption)` | `rt.Name/Version/DB`；始终注册；绝不实现 `Enabler` |
| databasemonitor | `NewModule(rt module.Runtime, cfg Config)` | `rt.DB` 满足其 `datasource` 接口；observer 在 `Initialize` 经**必需**的 `queryObserverRegistrar` 能力注册（`ExposeHTTP` 且 router 缺该能力时**失败**，不静默跳过） |
| metrics | `NewModule(rt module.Runtime, cfg Config)` | 构造期建 Prometheus registry；实现 `Enabler` + `CollectorSink`；`rt.DB` 驱动 datasource collector |
| migrationstatus | `NewModule(cfg Config)` | `Enabler`；`InjectServices` 解析 migration inspector 服务 |
| configinspector | `NewModule(value any, cfg Config)` | `Enabler`；快照在 `Initialize` 构建（构造不再报错） |
| pprof | `NewModule(rt module.Runtime, cfg Config)` | `Enabler`；`rt.Name` 在路由注册时用 |
| health | `NewModule()` | `InjectServices` 解析 `healthservice.CheckerProvider` |
| i18n / respx / guid | 不变 | 不接收 `Runtime` |

**外部调用点影响（机械改）。** 4 处其它包测试里的 telemetry 构造从 `telemetry.NewModule("test", "1.0.0", ...)` 改为 `telemetry.NewModule(module.Runtime{Name: "test", Version: "1.0.0"}, ...)`。

**独立迁移（`internal/app/migration.go`）。** `RunMigrations` 构建 `rt := module.Runtime{DB: router}` 传给 `dlock.NewModule(rt)`、`wuid.NewModule(rt)`、`audit.NewModule(rt, audit.Config{})`。`runModuleMigrations` 吃 `registry.Modules()`（激活集）。

## 5. 行为变化

| # | 变化 | 影响 |
|---|---|---|
| B1 | Collector 聚合顺序无关（Wire 后遍历） | 修复潜在 bug |
| B2 | observer 在 `Initialize`（migrate 后）注册 | 迁移 SQL 离开 `/debug/sql` / `database_sql_*` — **已接受** |
| B3 | 中间件分层由注册顺序表达 | 最终顺序不变：metrics → telemetry → audit → recovery |
| B4 | 条件模块自报 `Enabled()` | 禁用模块不在激活集；health/中间件/collector 反映激活集 |
| B5 | 删除 `App` 具体模块字段 | http.go/测试经 registry 解析 |
| B6 | 删除 "prometheus module is unavailable" 守卫 | 构造上不可达 |
| B7 | 模块不再收到 chi router | chi 只限 HTTP 服务 |
| B8 | `NewModule(name, version, db, ...)` → `NewModule(rt, ...)` | 4 处外部测试调用点 + 模块测试机械更新 |

## 6. 测试计划

### 6.1 `pkg/module` — Registry 单元测试（`registry_test.go`）

- `Register`：nil（全部 `canBeNil` 类型）、空名、重名；批次整体生效。
- `Activate`：禁用被剔除；**激活集上重验 Depender**（A 依赖 B、B 禁用 → 明确报错；都禁用 → OK）。
- **注册即预置回滚**：每个 Deinitializer 模块从 `Register` 起就在回滚表；Register/Activate/Wire/Initialize/Start 任一失败都执行清理；`Deinitialize` 从每个状态 nil-safe + 幂等。
- `Wire`：provider 先于 injector、与顺序无关；回调错误传播；重复服务键处理。
- 阶段语义从 `internal/app/module_test.go` 迁移：逆序 + `errors.Join`；回滚只对已完成阶段；`Stop`/`Deinitialize` 清空列表（二次关闭幂等）。
- `Services()` 密封实例；`Modules()` 拷贝；`ByName → (Module, bool)`；状态守卫。

### 6.2 Collector 聚合（真实 metrics 模块）

`Register(metrics)` + `Register(fakeCollectorModule)` → Activate + Wire + `collectMetrics` → `prometheus.Gather` 断言 fake collector 的 metric 家族出现。矩阵：collector 在 metrics 前后都注册（bug 回归）；collector 模块被禁用；metrics 被禁用（零 sink → no-op）；重复 collector；nil collector；多 sink 各自接收全部；collector 注册失败干净回滚。

### 6.3 中间件与协议顺序

- `HTTPMiddlewares()` + `respx.Recovery` = [metrics, telemetry, audit, recovery]，用写 header 的探针断言。
- gRPC：telemetry stats handler + audit 拦截器 + fake 拦截器顺序；真 RPC 验证多 stats handler 组合。

### 6.4 模块包测试迁移清单

| 包 | 说明 |
|---|---|
| metrics | 为 `NewModule(rt, Config)` + `CollectorSink` 重写 |
| migrationstatus / configinspector / pprof | 新 `Config` 参数；configinspector 错误移到 `Initialize` |
| health | service 经 `InjectServices` 构建 |
| dlock / wuid / audit / databasemonitor | 窄 fake 不再满足具体 `rt.DB`——用真实 SQLite router 或包私有构造缝；锁住降级无 writer（`Write()` 为 nil）行为 |
| telemetry / audit | 4 处外部调用点机械更新；functional options 保留 |

### 6.5 app 级不变式（更新，不放水）

- `TestBootstrapWithoutWriterReportsUnhealthy`：health 输出完全一致。
- `TestBootstrapRegistersAuditRecorderWhenDisabled`：经 `app.modules.Services()` 解析；扩展：audit 禁用时仍在 health 列表（UNKNOWN）和 `HTTPMiddlewares()` 中。
- `TestBootstrapAddsEnabledHTTPModules`：期望顺序更新（metrics 在 telemetry 前、pprof 近末位）；`app.mods` 顺序断言改行为断言。
- `TestBootstrapMigratesBeforeStartingWhenEnabled` / `StartsAfterExplicitMigrations`：不变。
- `moduleconfig_test.go`（新增）：每条 AppConfig→模块 Config 映射字段都有传播断言。

## 7. 评审汇总与处置

### 7.1 subagent 评审（7 角度）→ v2

采纳的共识：Activate 后对激活集重验依赖；audit 绝不实现 Enabler；基类 Config 只留 `Enabled`；Registry 需 `Services()`；错误串保留；逐模块测试迁移清单；分阶段落地；`isNilModule` → pkg/module；`ByName → (Module, bool)`；`MigrationSourceProvider` → pkg/module；跨服务复用推迟。

### 7.2 codex 评审 #1（服务注入方向）→ v3 修订

发现：`provideCrossCutting` 错误传播、回滚须覆盖构造期持有资源、`App.modules` 初始化路径、状态机/回滚对齐。随后服务注入方向被**用户简化决策取代**：模块自治 + `module.Runtime` 构造对象（见 7.3）。

### 7.3 codex 评审 #2（Runtime 设计）— sound-with-changes

| # | 发现 | 处置 |
|---|---|---|
| R1 | collector 接线须在回滚表建立后，而非紧接 Activate | **采纳**——`collectMetrics` 放 `Wire` 后（§3.5） |
| R2 | telemetry 构造副作用需尽早的清理所有权 | **采纳**——Deinitializer 在 `Register` 即预置（§3.3） |
| R3 | `migration.go` 独立构造须构建 `Runtime{DB: router}` | **采纳**（§4） |
| R4 | 保留降级无 writer 行为 + 测试锁定 | **采纳**（§6.4） |
| R5 | 重写文档（旧注入规格与修订矛盾） | **完成** |
| S1 | 无循环 + 具体 router 覆盖全部消费者——确认 | 采纳 |
| S2 | `pkg/appenv` 中性包 vs `module.Runtime` 的 DB 耦合 | **推迟**——已记录取舍（§3.1） |
| S3 | `Runtime` 按值、不可变；模块只留窄能力 | **采纳**（§3.1） |
| S4 | 模块测试需真实 SQLite router 或包私有缝 | **采纳**（§6.4） |
| S5 | Collector 测试矩阵 | **采纳**（§6.2） |
| N1 | 为 router 满足窄契约加编译期断言 | 备注 |
| N2 | 不需要 `Runtime` 的模块不传 | **采纳**（§3.4） |

## 8. 决策

- **B2**：已接受——迁移 SQL 离开诊断（observer 在 migrate 后注册）。
- **Q1（外部调用点）**：4 处测试构造机械迁移；无双构造器。
- **Q2（降级无 writer）**："服务缺失" = 接线错误；"router 在但 `Write()==nil`" = 运行时降级（health NOT_SERVING），非接线错误。
- **Runtime 位置**：`pkg/module.Runtime`（值、不可变）；`pkg/appenv` 推迟。
- **gRPC stats handler**：支持多个（grpc-go append/组合）。
- 剩余待定：Services 重复键错误溯源（加 or 删承诺）。

## 9. 风险

1. phase runner 迁移量大且机械——分阶段落地（§12）。
2. Activate 期依赖重校验是新增失败面——错误串不得改变。
3. 注册即预置回滚意味着 `Deinitialize` 可能先于 `Initialize`——模块必须 nil-safe/幂等（telemetry 已验证）。
4. chi 收敛改变 raw 路由表达（pprof 通配、重定向）——小心移植、保持 OpenAPI 文档。
5. `pkg/module` 新增 gorm 依赖——接受，已记录。

## 10. 不在范围内

- 业务模块；迁移 `AppConfig`（import 循环——`moduleconfig.go` 是接缝）。
- 外部 DI 框架 / 代码生成（否决，§11）。
- 跨服务复用 `internal/infra/*` 与视图契约；跨服务 gRPC client 生命周期。
- JSON 诊断端点迁移到 `huma.Register`（一致性跟进）。
- `Runtime` 里的 Redis/cache（YAGNI）。

## 11. 已考虑的替代方案

- **Kratos / Google Wire / 运行时 DI（uber/fx）**：否决（条件激活表达不了、注册表级聚合无解、新依赖）。
- **DB/身份服务注册表注入（AppIdentity 服务、DB 服务、双构造器）**：设计并 codex 评审过，后**否决**——对本代码库过重；`module.Runtime` 以构造函数参数之简达到同样的零泄漏 bootstrap。
- **保留具体 App 字段**：否决（模块句柄味道）。
- **内部自关**：否决（禁用模块会出现在 health/中间件里）。
- **huma 原生中间件**：否决（漏 raw 路由；router 级覆盖一切）。

## 12. 实施计划（分阶段，各自独立交付）

- **Stage 0 — 最小 bug 修复（不改架构）**。在当前代码里把 collector 聚合移到所有注册完成后（约 15 行）+ `prometheus.Gather` 回归测试。独立合并 / 回滚。
- **Stage 1 — 纯搬移**。新增 `pkg/module.Registry`（状态 + phase runner + Register 即预置回滚），App 委托。零行为变更。
- **Stage 2 — 行为修正**。`Activate`（Enabled 过滤 + 激活集依赖校验）、`collectMetrics` 放 Wire 后（B1）、状态守卫。
- **Stage 3 — Runtime + 逐模块迁移**。`module.Runtime`；构造函数改收 `rt`；条件模块嵌入 `module.Config{Enabled}` + `Enabler`；databasemonitor observer 能力；health injector；`moduleconfig.go` + `moduleconfig_test.go`（B2/B4/B8）。
- **Stage 4 — 清理 + 传输解耦**。删 `App` 字段、http.go 走 Registry（`HTTPMiddlewares` + `RawHTTPRoutes`，chi 收敛）、删 `// .. biz modules` 占位符、`AddModule` → `addModule`、更新剩余迁就实现的断言（B5/B7）。
