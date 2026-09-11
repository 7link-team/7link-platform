---
name: backend-module
description: 本仓 Go 后端(apps/backend-api)的模块开发规约。写任何 internal/ 下的业务或基础设施模块、加表加接口、接线到 composition root、写迁移或 i18n 之前必读:模块自治边界、pkg/module 生命周期与能力接口、model/query/dbgen 三件套、三方言前缀化迁移、huma 契约与 respx 错误、依赖只走 module.Resolve、覆盖率与门禁。Use this whenever work touches apps/backend-api — 加个模块、加张表、加个接口、改迁移、接线、这个模块怎么写、后端开发, and before writing any Go code under internal/ or pkg/. 参考实现是 internal/todo。
---

# 后端模块开发规约(apps/backend-api)

本仓 Go 后端的**模块形状**是固定的。偏离它的代价不是风格问题:composition root、迁移编排、
门禁和架构测试都按这个形状写死了,自造一套会在接线或 CI 阶段失败。

**参考实现是 `internal/todo`** —— 它是唯一一个刻意保持完整的样板:model / query / dbgen /
三方言 SQL / i18n / module 生命周期 / huma 路由 / 中间件全都有。**动手前先读它对应的那个文件。**
`AGENTS.md` 是仓库级速查,本文件是模块级细则,冲突时以实际代码为准。

## 一、模块自治:先划边界,再写代码

一个模块 = 一个目录,**自己拥有**业务逻辑、持久化模型、生成的查询、SQL 迁移、i18n 和 HTTP 路由。

**禁止**:
- 中央 schema 包、中央 port 包、中央 DTO 层 —— 表归表的拥有者,契约归 provider 根包
- 跨模块 import 对方的 `model` / `query` / `dbgen` —— 要数据就让对方暴露窄接口
- 在模块内构造第二个模块 —— **只有 `internal/app` 能构造多个模块**
- 为一个用例引入 Ports-and-Adapters、feature 子包、单实现接口

**跨模块只有一条路**:被依赖方在**根包**冻结一个窄接口(如 `guid.Source`、`audit.Appender`),
依赖方用 `module.Resolve[T]` 拿。接口定义在**提供方的根包**,不在中间层。

目录形状(按需取用,不是全都要):

```
internal/<模块>/
  module.go        生命周期与能力接口装配(必有)
  <业务>.go         领域逻辑,按职责分文件,不按技术分层
  http.go          RegisterHTTP + huma 操作(有 HTTP 才有)
  migrate.go       //go:embed sql/... + MigrationSource()(有表才有)
  model/           持久化行类型;独立包,否则 query 生成物会成环
  query/           gen.go(生成) + table.go(手写 Bind)
  dbgen/main.go    生成器入口
  sql/{postgres,mysql,sqlite}/  三方言迁移,必须齐三份
  i18n/            Namespace 常量 + locales/*.json(三语齐全)
```

## 二、pkg/module 生命周期:只实现你真需要的能力

`Module` 接口只要求 `ModuleName() string`。其余全是**可选能力接口**,实现哪个就获得哪个阶段。
**不要为了"完整"把所有接口都实现一遍。**

固定顺序:`Register → Activate → Wire(Provide→Seal→Inject) → Migrate → Initialize → Start →
RegisterHTTP/RegisterGRPC → …运行… → Quiesce → Stop → Deinitialize`。
关停严格逆序;`Deinitializer` 在 `Register` 时就进回滚表,所以构造即持有资源的模块也能被释放。

| 要做的事 | 实现的接口 |
|:---|:---|
| 声明依赖(决定注册与关停顺序) | `Depender.ModuleDependencies()` |
| 对外提供能力 | `ServiceProvider.ProvideServices` + `module.Provide[T]` |
| 取别人的能力 | `ServiceInjector.InjectServices` + `module.Resolve[T]` |
| 迁移后、启动前的准备 | `Initializer.Initialize` |
| 后台任务 | `Starter.Start` / `Quiescer.Quiesce` / `Stopper.Stop` |
| 健康检查 | `HealthChecker.CheckHealth` |
| 类型化 HTTP | `HTTPRegistrar.RegisterHTTP`(只经 huma,不碰路由实现) |
| 非类型化 HTTP(控制台、pprof) | `RawHTTPHandlerModule.RawHTTPRoutes` |
| 全局中间件 | `HTTPMiddlewareModule.HTTPMiddleware`(注册在前 = 更外层) |
| 拥有表 | `MigrationSourceProvider.MigrationSource` |
| 迁移分段与跨模块依赖 | `MigrationMilestoneProvider.MigrationMilestones` |
| 可开关 | 嵌入 `module.Config` 并实现 `Enabler` |

**用编译期断言把实现的能力钉住**,照 `internal/todo/module.go` 的写法:

```go
var (
    _ module.Depender                = (*Module)(nil)
    _ module.ServiceInjector         = (*Module)(nil)
    _ module.MigrationSourceProvider = (*Module)(nil)
)
```

漏写一个接口不会报错,只会**静默不执行那个阶段** —— 断言是唯一的防线。

**关于 `Enabler`**:只有条件启用的模块嵌入 `module.Config`。
**始终注册的模块(health、telemetry、audit、metrics…)不得实现 `Enabler`。**
`Activate` 会剔除关闭的模块并重新校验依赖图:依赖和被依赖一起关掉可以,只关掉依赖会报错。

## 三、依赖注入:只有 Provide / Resolve

`Wire` 的顺序是**所有 provider → Seal → 所有 injector**,所以 provider 与 injector 之间
**与注册顺序无关**;`ModuleDependencies` 管的是生命周期顺序,不是解析顺序,两者别混。

- `Provide[T]` 注册的是**接口类型**,不是具体模块。同一类型重复注册会报 `ErrServiceDuplicate`。
- `Resolve[T]` 只能在 Seal 之后调用,即只能在 `InjectServices` 及更晚的阶段。
- **不要引入第二套 DI**:没有容器、没有 wire、没有全局单例、没有 init() 里的注册。

架构测试会检查:**凡是 import 了别的模块并调用 `module.Resolve` 的,
必须在自己 `ModuleDependencies()` 的返回表达式里声明那个模块。** 少声明会被门禁抓住。

## 四、数据:model / query / dbgen / 三方言 SQL

**四件套必须齐**,少一件就会在生成或迁移时炸。

**model** —— 独立子包(不能放根包,否则 query 生成物 import 根包成环)。
行类型带完整 gorm tag,并实现 `TableName() string` 返回**不带前缀**的表名。
时间列用 `time.Time`,可空时间用 `*time.Time`(NULL 表示"未发生",不要用零值当哨兵)。

**query** —— `gen.go` 由 `make generate` 产出,**不要手改**;`table.go` 手写,放三件事:

```go
// 物理表名:逻辑名 + 已生效的部署前缀。构造函数唯一,IsZero 可观测。
type TableNames struct { Todos gormx.TableName }

func BindNames(prefix string) TableNames {
    return TableNames{Todos: gormx.NewTableName(prefix, model.TodoRow{}.TableName())}
}

// 供模块根包发布 TableSet,给 JOIN 本模块表的消费方用。
func (names TableNames) Map() map[string]gormx.TableName {
    return map[string]gormx.TableName{model.TodoRow{}.TableName(): names.Todos}
}

// Bind 从 BindNames 构建,所以前缀只经过一条路径。
func Bind(db *gorm.DB, prefix string) *Todos {
    names := BindNames(prefix)
    return Use(db).TodoRow.Table(names.Todos.String())
}
```

**`BindNames` 是全模块唯一拼接表名的地方。** 分散拼接会让加了前缀的部署"半生效" —— 一半表带前缀一半不带,
而且单元测试发现不了(测试通常不带前缀)。`gormx.TableName` 的存在就是为了让这件事物理上只发生一次:
别处拿到的都是已经构造好的名字,它们**无法自己发明**。

**模块根包再加一个 `tables.go`**,把本模块的表名发布给别人:

```go
func TableSet(prefix string) gormx.TableSet {
    return gormx.NewTableSet(query.BindNames(prefix).Map())
}
```

消费方拿到的是**名字的集合,不是前缀**,所以它造不出拥有者没发布的名字;解析不到时
`Lookup` 返回 `ok=false`,**此时必须返回 error,不许把零值表名放进语句**(零值即空表名)。

**这两条由静态门禁强制**:`tests/architecture/query_gate_test.go` 禁止在 `Bind` 之外拼表名、
禁止手写 SQL、禁止裸 `iam_`/`goose_` 表名字面量;豁免文件只有各模块的 `query/table.go` 与
`pkg/extension/{gorm,goose}` 这两个原语包。门禁按冻结基线 `testdata/query_gate_ledger.json`
执行,**预算只减不增** —— 清理可以下调 `budget`,禁止为了变绿而抬高预算或删条目。

**服务层再收一次口**:像 `todo` 的 `scope()` 那样,提供唯一的查询构造入口,
把软删谓词和前缀绑定都锁在里面,**并让 `includeDeleted` 这类语义在每个调用点显式传参** ——
默认值是这类缺陷的温床。

**数据源在操作时解析,不要长期持有**:`runtime.Database().Write()`,拿不到就返回本模块的
`ErrUnavailable`,不要 panic 也不要静默降级。

**SQL 迁移**:`sql/{postgres,mysql,sqlite}/` 三份**必须同时存在且语义等价**;
所有对象名(表、索引、约束、触发器)都要带 `{{.Prefix}}`;`migrate.go` 用 `//go:embed` 嵌入,
`goosex.Source{FS: ..., TableName: "goose_<模块>"}` —— **版本表按模块隔离,不共用**。

写 DDL 时:金额用定点不用浮点;可空语义用 NULL 不用 0 或空串;
业务唯一键必须落唯一索引。跨模块外键归**依赖方**的迁移文件。

**时间列的类型是定死的**,三方言各自只有一种写法(此处不写具体列数 —— 它会随迁移增加而漂移,
以 `make test-mysql8` 的 schema lock 实测为准):

| 方言 | 用 | 不要用 |
|:---|:---|:---|
| PostgreSQL | `TIMESTAMPTZ(3)` | —— |
| MySQL | `DATETIME(3)` | **`TIMESTAMP`** |
| SQLite | `DATETIME` | 存纪元秒的整数列 |

**MySQL `TIMESTAMP` 的上限是 2038-01-19。** 它对当前日期完全正常,所以写错了能过全部门禁 ——
直到有人存一个长期有效期、合同到期日,或用 `9999-12-31` 当"永不过期"哨兵。
`DATETIME` 到 9999 年,和 PostgreSQL 侧对得上。

**门禁在 `tools/iam-schema-lock`**,由 `make test-mysql8` 调用(纯静态扫描,不需要数据库):
`internal/iam` 下逐列校验精确类型,**全仓**禁止 MySQL `TIMESTAMP`。
非 IAM 模块只受后一条约束 —— 已发布的 todo/audit 迁移在 PG 侧用的是无精度 `TIMESTAMPTZ`,
而改写已发布迁移是禁止的,所以精确类型这条不追溯。**新模块请直接按上表写。**

时间统一 UTC,可空时间用 NULL 表示"未发生",不要用零值当哨兵(时间列的零值哨兵也会被门禁拒绝)。

**改了 model 必须 `make generate`,且工作树要回到零差异** —— 生成物漂移是门禁项。

## 五、HTTP:huma + respx,不碰路由实现

`RegisterHTTP` 里只用 `huma.Register`。**模块永远不 import chi/gin/fiber** ——
huma 是适配器无关的,换路由不该动模块;全仓只有 `internal/app/http.go` 碰路由实现。

**需要鉴权的模块不用裸 `huma.Register`,用 `authz.Register`**,把 operation 和
`authz.Guard{Resource, Verb}` 一起交出去 —— 权限声明和路由声明写在同一处,漏一个就是越权入口。
`internal/todo` 是无鉴权样板,鉴权样板看 `internal/iam/*/http.go`。

每个 operation 必给:`OperationID`(全局唯一、稳定)、`Method`、`Path`、`Summary`、`Tags`。
按需给:`DefaultStatus`(创建 `201`、无响应体删除 `204`)、`Errors`(声明该操作会返回的非 200 状态码,
让生成的文档和客户端知道)。路径表达**稳定资源**,风格与既有接口一致;CRUD 用 HTTP 方法表达,
领域动作才用动作段(`/{id}/restore`)。

**注册顺序有坑**:静态段路由要注册在带路径参数的路由**之前**,否则静态段会被当成参数捕获
(`/todos/export` 会被 `/todos/{id}` 吃掉)。

响应统一走 `respx`:`respx.OK` / `OKMessage` / `List`,错误用 `respx.Err(ctx, code, i18nKey)`。
**不要自己拼错误结构**,也不要把内部错误、SQL 或堆栈透给调用方。

**服务不可用时的取舍**照 `todo.RegisterHTTP` 的先例:注册阶段拿不到数据源时**仍然注册路由**
(让每条路由自己回 unavailable),而不是让整个进程起不来 —— 但必须 `slog.Warn` 出来,
说明原因和补救动作。**静默降级是明确禁止的。**

## 六、i18n:三语齐全,namespace 限定

`i18n/i18n.go` 里定义 `Namespace` 常量和全部 key(`Namespace + ".xxx"`),
`locales/{en-US,ms-MY,zh-CN}.json` **三份齐全**,在 `Initialize` 里 `Register()`。

key 一律 namespace 限定,**不要裸 key** —— 跨模块撞名时是静默覆盖。
新增 key 必须三份同时加;缺语言不会报错,只会在那个 locale 下渲染出 key 本身。

## 七、接线到 composition root

**只有 `internal/app/bootstrap.go` 能构造模块。** 模块自己不构造别的模块。

注册顺序是**语义**不是随意:注册在前 = 中间件更外层 = 关停更靠后。
现有顺序有明确理由(例如 metrics 在 telemetry/audit 之前是为了让它的 HTTP 中间件保持最外层),
**要插入新模块先弄清相邻模块为什么在那个位置。**

新增可开关模块时:composition root 只声明**完整清单**,启停交给 `module.Enabler`,
不要在 root 里写 `if enabled { ... }`。

## 八、测试与门禁

- 单元测试与代码同目录,`*_test.go`,`TestBehavior` 命名,用 `testify`
- 多数据库引擎行为放 `tests/`(独立 Go module),不要在主模块起重型引擎
- **新模块要加进 `Makefile` 的 `MODULE_COVERAGE_PACKAGES`**,门禁要求**该包 ≥90%**,总体 ≥90%
- 提交前跑 `make ci`(fmt-check / lint / test-coverage / test-integration / build)
- **`make ci` 用的是内存 MySQL**,真实 MySQL 的 DDL 门禁在 `make test-mysql8`,
  性能基线在 `make test-performance` —— **改了 SQL 或查询,这两个也要跑**
- `make generate` 后工作树必须零差异

**测试文件命名是全仓强制的**(`tests/architecture` 的 `TestGoTestFileNamesMatchProductionFiles`):
测试必须命名为 `xx_test.go` 或 `xx_子主题_test.go`,且同目录下要存在对应的 `xx.go`。
凭空起一个 `helpers_test.go` 而没有 `helpers.go`,门禁会红。

**查询门禁的作用域是全仓 `internal/` + `pkg/`**(`TestQueryGate`:禁手写 SQL、禁在 `BindNames` 之外
拼表名、禁裸 `iam_`/`goose_` 表名字面量),测试文件不在其内。

`tests/architecture` 的另外几项**只作用于 `internal/iam`**(模块根包必须有自己的业务源码、
`module.Resolve` 必须在 `ModuleDependencies` 的 return 表达式里声明、禁止跨模块引用
model/query/dbgen、禁止从根包泄漏持久化类型)。**新写的模块即使不在强制范围内,也按同样的约束写** ——
这些规则是从本仓已经踩过的问题里长出来的,而且作用域随时可能扩到全仓。

**不要试图靠跨模块引用别人的 `query` 来复用表名**:`pkg/module/registry.go` 的 `Activate` 要求
"模块必须注册在其全部依赖之后",即**依赖图必须是严格全序,环即启动失败**。像 `core` 这种被大家依赖的模块,
反向 import 下游的 `query` 在启动期就是硬失败。跨模块取表名只有一条路:拥有者在**根包**发布
`TableSet`(表名集合,不是前缀)或冻结窄接口,依赖方用 `module.Resolve[T]` 拿。见 decisions D37。

## 九、动手顺序

1. **读 `internal/todo` 里对应的那个文件** —— 你要写 http 就读它的 `http.go`,要写迁移就读它的 `migrate.go` 和 `sql/`
2. 划边界:这块逻辑属于哪个模块?需要别人什么能力?对外要冻结什么接口?
3. 有表就先写 model → `make generate` → `query/table.go` 的 `Bind` → 三方言 SQL → `migrate.go`
4. 写领域逻辑,按职责分文件(`service.go` / `list.go` / `transfer.go`),不按技术分层
5. 写 `module.go`:实现需要的能力接口 + 编译期断言
6. 有 HTTP 就写 `http.go`,有文案就写 `i18n/`
7. 接线到 `internal/app/bootstrap.go`,想清楚插在哪个位置
8. 补测试到 ≥90%,加进 `MODULE_COVERAGE_PACKAGES`
9. `make generate` → `make ci`;动过 SQL 或查询再跑 `make test-mysql8`、`make test-performance`

## 不要做的事

- 不要新建中央 schema / port / DTO 包
- 不要跨模块 import model / query / dbgen
- 不要在模块里构造别的模块
- 不要手改 `query/*.gen.go`
- 不要在 `Bind` 之外拼表名
- 不要 import 具体路由实现(只有 `internal/app/http.go` 可以)
- 需要鉴权时不要绕过 `authz.Register` 自己写 `huma.Register`
- 不要为单实现定义接口、为一个用例造框架
- 不要静默降级 —— 要么失败,要么降级并告警
- 不要漏掉三方言 SQL 或三语 locale 中的任何一份
- 不要只跑 `make ci` 就宣称验证完毕
