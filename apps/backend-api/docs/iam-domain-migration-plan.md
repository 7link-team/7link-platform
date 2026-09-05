# IAM 域全量迁移实施计划 v10

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 chaosplus `apps/server` 的 IAM 域(9 个 Go 模块 / 63 张表)全量迁入 7link-platform `apps/backend-api`,改造为本项目的 gorm + gorm/gen + goosex + pkg/module 规约,并修复源库既有的 SQL 缺陷。**全功能到位**:每个模块都要有生产实现与可用路由,不接受"只迁 schema、模块 Disabled"。

**Architecture:** `internal/iam/` 是 IAM 域命名空间，`core`、`authn`、`identity`、`oauth`、`organization`、`audit`、`governance`、`provisioning`、`federation` 是 9 个自治 `pkg/module`。每个模块按 `internal/todo` 的既有规约拥有自己的业务文件、`model/query/dbgen/i18n`，以及本模块需要的 `migrate.go` 与 `sql/{postgres,mysql,sqlite}`；禁止集中 `iamschema` 包。63 张表是全域验收集合，不是一个迁移模块。跨模块外键用模块自有的基础表/约束里程碑编排，SQL 文件仍归拥有表的模块。目标库为**全新空库,不迁移存量数据**(用户 2026-09-02 确认)。

**Tech Stack:** Go 1.26 · gorm v1.31 + gorm/gen v0.3.29 · goose v3.27 · huma v2.39 · PostgreSQL / MySQL / SQLite

**Spec:** 本文件自带规格(§1–§3)。事实来源由 `make iam-preflight` 自动解析并校验，不再依赖单机绝对路径或缺失的临时汇总文件。源基线为 chaosplus `apps/server` tree `201052baddd745d1c2727cf29bbc3c68253a1af0`（当前提交 `8c034d3efa603c7fd327a658bd405252397caf24`）；预检直接从三方言正式 SQL 计算并断言同一组 63 张表。

> **2026-09-04 产品完整性复审结论：**固定源树的迁移等价性、63 表、208 条 HTTP operation 和三引擎门禁已经完成；但“迁移完成”不等于“可从空库投入运营”。首个平台管理员和新租户首个管理员均无法通过生产入口建立，数据权限尚无业务查询消费者，字段权限完全缺失。Phase 10 完成前，IAM 的状态是**源功能迁移完成、产品化闭环未完成**，不得再表述为“Remaining Work 无”。

---

## 版本沿革

### v9 → v10 产品完整性复审与初始化方案

- 区分“固定源树迁移等价”与“可部署 IAM 产品完整性”；前者已通过，后者仍有 Phase 10 待办。
- 新增两层初始化：显式、create-only 的平台管理员 bootstrap，以及租户创建事务内的 owner/Administrator role/权限/菜单 bootstrap。migration 继续只负责 schema，不写固定账号或明文秘密。
- HTTP 208 条目标契约仍完整；源项目没有 IAM gRPC，故迁移不缺 gRPC。若产品要求 IAM 双协议，则须先新增 proto/兼容/鉴权契约，不能把现有应用 gRPC 端口误当 IAM gRPC 已实现。
- 权限复审发现：接口 guard 执行链完整，但 principal 管理错误混用 platform action 作为 tenant guard；数据权限只完成计算与解释、未进入业务 repository；菜单权限缺默认租户数据；字段权限没有数据模型和执行链。
- 增加全仓测试文件命名门禁：`xx_test.go` 或 `xx_subxx_test.go` 的 `xx` 必须对应同目录真实生产 `xx.go`；纯黑盒测试目录豁免。
- 修复 IAM composition root 的集中开关：9 个 runtime module 始终登记，由各自 `module.Enabler` 决定激活；registry 在裁剪后校验 `ModuleDependencies()`。federation 开启而 IAM 基础依赖关闭时明确启动失败，不再被 app 层 `if` 静默忽略。

### v8 → v9 统一 HTTP 命名并采用 RESTful 目标契约

- 传输层文件和注册入口统一命名为 `http.go` / `RegisterHTTP`，与可能存在的 `grpc.go` / `RegisterGRPC` 对称；RESTful 是 HTTP 接口的设计约束，不再作为文件名。
- 固定源树提取的 208 条 operation 继续作为功能完整性事实，但不再要求目标原样复制 RPC 风格 path、method 或 OperationID。每个模块实现 HTTP 前必须冻结逐条 source→target 映射，保证功能不漏项。
- 目标契约优先使用复数资源、HTTP method 表达 CRUD、名词化子资源和统一 ID 参数；OpenAPI 门禁比较批准后的目标契约，并继续校验参数、schema、状态码、安全声明与运行时路由。
- authn 30 条映射已在本计划冻结。源 IAM 没有 gRPC service/proto 契约，因此本次不虚构 IAM gRPC；未来出现真实内部调用需求时再以独立契约增加 `grpc.go`。

### v7 → v8 恢复 IAM 域层级与模块 SQL 自治

- `internal/iam/` 作为领域命名空间，9 个源模块统一放入其下；`core` 对应源 `modules/iam`，包路径改变但 `ModuleName()` 仍为 `iam`。
- 废弃 direct feature 子包和集中 `internal/iamschema`。模块根按 `internal/todo` 保持内聚；只有真实形成独立 package 边界时才另行评审，不能按文件职责机械拆包。
- 源库把 authn/identity/oauth/audit 的表混放在 `modules/iam/sql`；目标按实际读写职责归还给 9 个自治模块，使 Go 模块、model/query、SQL 与版本表一一对应。
- `iam↔organization` 外键环只说明迁移需要分里程碑，不能推出集中 schema 所有权。基础表和跨模块约束仍由表所属模块维护，应用迁移器按显式里程碑依赖执行并反向回滚。

### v6 → v7 回归项目原生模块结构

- 撤回仅在 IAM 域引入 Ports-and-Adapters、集中 `pkg/port/*` 和 `internal/<capability>` 的设计；若未来采用该架构，必须以独立 ADR 对全项目统一改造，不能在本次迁移中制造第二套模块体系。
- 保留业务 feature 分包，但去掉无收益的嵌套：直接使用 `internal/iam/{access,authorization,resourcegraph,administration}`。每个 feature 包内按 `domain.go`/`service.go`/`repository.go`/`http.go` 组织。
- 沿用现有 `guid.Source`、`audit.Recorder` 模式：跨模块接口由提供方拥有，并由模块根稳定导出；消费者通过 `pkg/module.Services` 解析，不新增中央 port 包。
- 架构门禁改为检查模块 DAG、跨模块只能导入 provider root，以及按文件职责限制 gorm/Huma/`pkg/module` import，不强行套 adapter 层。

### v5 → v6 目标模块结构治理（已被 v7 废弃）

- 不再默认把源 basename 平移到模块根目录。9 个模块仍是一级 bounded context；模块内部按业务能力纵向分包，根包只保留生命周期/装配和可选的构造配置，不承载仓储、HTTP handler 或业务规则。
- 固定单向结构：`store → capability ← httpapi`，模块根只组合它们；capability 包含领域/用例/仓储端口，跨能力工作流放模块私有 `workflow`，禁止 sibling capability 互相直接导入。
- 增加架构测试：业务能力包禁止导入 gorm/Huma/`pkg/module`，模块间禁止直接导入对方 `internal/<module>`，跨模块只允许同一 `pkg/port/*` 接口；所有手写子包进入覆盖率治理。

### v4 → v5 执行粒度冻结

- Phase 2–8 不再用四步摘要代替实现计划；9 个模块均冻结 source manifest、目标 model/query/dbgen、bun→gorm 风险、领域行为、端口注册/解析、跨租户负例与逐模块 `make ci` 门禁。
- 取消把同一个 `internal/iam` 包拆成 Phase 2/4/8 三个半成品的安排：Phase 2 一次迁完可独立测试的完整 IAM 包，Phase 8 只负责 composition root 与跨模块收口。
- 从固定 source tree 展开所有生产 Go 文件中的 operation、动态注册与 SAML 常量，冻结 **208 条** `OperationID|METHOD|PATH`；OperationID 208/208 唯一，Phase 8/9 必须做集合完全相等测试。
- 关闭菜单多语言未决项：本次只迁现有功能，保持 63 表，不新增 `iam_menu_translations`（D7）。
- 重新按当前源树统计 Go 文件：9 模块生产 28,176 行、测试 22,054 行；扩展包 2,823 行，总生产约 30,999 行。

### v3 → v4 换机恢复

- 源路径改为自动解析：macOS 使用 `/Volumes/Macintosh/Projects/chaosplus/chaosplus/apps/server`，Windows 使用 `D:\Projects\Chaosplus\chaosplus-api\apps\server`；`IAM_SOURCE_ROOT` 可显式覆盖。只接受上述固定 source tree，源目录有未提交修改时拒绝执行。
- 新增 `make iam-preflight`：Windows 自动关闭本仓库的 `core.autocrlf` 并仅把 Git 认定为 `i/lf w/crlf` 的 Go 文件恢复为 LF；macOS 不改换行。机器配置写入被忽略的 `.temp/iam-env.mk`，不保存密码。
- `make build` 的输出目录创建按 OS 选择 Windows PowerShell 或 POSIX `mkdir -p`，不再要求 Windows PATH 中存在 Unix `mkdir`。
- Docker 传输自动选择：macOS 优先使用本机 Docker；Windows 无本机 Docker 时使用专用 SSH key 经 `root@10.0.0.100` 驱动远端 Docker，并把数据库测试地址设为 `10.0.0.100`。远端为 Debian 且 Docker 缺失时，预检可幂等安装 `docker.io` 并启动服务；MySQL 固定为 `mysql:8.4@sha256:b3b90af2a6552ae30c266fdb7d5dd55f3afb72404bb78d37fe8a23eb857fd3fb` 并由预检确保镜像存在。
- 删除不存在的 `.temp/iam-schema-mysql.sql` 事实依赖；表集合直接以三方言正式迁移文件为准。
- 更正端口数为 13；声明式模块只有 6 个，`authn`/`identity`/`oauth` 不能假设源库已有 `NewDeclarationOnlyModule`。

v1 经 codex 评审(9C/18M/5m)修订为 v2；v2 再经 codex 评审(5C/15M/3m)+ 全仓架构审计交叉验证，**逐条实测**后修订为 v3；换机后完成环境复核并修订为 v4；逐模块执行清单与源 HTTP operation 集合冻结后成为 v5；局部 Ports-and-Adapters 设计形成 v6，随后按项目一致性要求撤回并修订为 v7；v8 再按 `CLAUDE.md`、`internal/todo` 和用户确认恢复域层级与模块 SQL 自治；v9 统一 HTTP 文件命名并将源路由基线改为逐模块 RESTful 目标映射。本节记录被推翻的结论，避免读者把已作废的判断当事实。

### v1 → v2 已处置(不再赘述)
Phase 0 构建顺序、SQLite 外键未启用、版本表双前缀、`{{.Prefix}}` 不覆盖 raw SQL、范围漏 oauth/audit、`TIMESTAMPTZ(3)` 精度、MySQL DDL 隐式提交、覆盖率登记时机、`//go:generate` 缺失、baseline 冻结。以及两条**被实测推翻的 v1 缺陷**:源库三方言**都有** append-only 触发器;`menus.route` 的可空 NULL 语义**是正确的**(分组节点应可并存)。

### v2 → v3 修订(全部经实测)

| v2 内容 | 实测结论 | v3 处置 |
|---|---|---|
| **R11' 只按整条模块流做单次 DAG 排序** | ❌ **方案不成立**。跨模块外键 13 条,其中 `organization→iam` 7 条、`iam→organization` 1 条——**双向环**。这只能证明“每模块整条流一次跑完”的粒度不足，不能证明表和 SQL 应脱离模块 | 保持模块 SQL 自治；把含环模块拆为“基础表 → 跨模块约束”迁移里程碑，由执行器按里程碑 DAG 编排(§2.1) |
| §1.1 表数 `iam 35 + organization 13 + tenancy 1` | ❌ **算错,合计只有 62**。`iam_tenants` 由 `organization/sql/postgres/00005_tenants.sql` 创建,**iam 从来没有这张表**,不该从 iam 减 | 总数 **63**；v8 再按 9 个自治模块精确集合断言(Task 1.3 Step 1) |
| Task 1.3 关系表 DDL 用 `ON DELETE CASCADE` | ❌ **擅自变更业务语义**。源库 `00015_relationships.sql:12` 与 `00016:13` 均为 `ON DELETE RESTRICT`。改 CASCADE 会把"有授权关系时拒绝删实体"变成"静默删授权" | **保持 RESTRICT**。新增 R15 强制每条 DDL 标注源文件行号 |
| Task 1.3 `description VARCHAR(1000)` | ❌ **违反本文件自己的 R7'**。`iam/service.go:105` 的上限是 `len(description) > 4096` | 按源码上限取值,并标注出处 |
| Task 0.0 `at most one writable datasource` | ❌ **会打破现有正式契约**。`pkg/extension/gorm/datasource_test.go:229` 的 `TestDatasourceRouterRoundRobinStateIsPerRouter` 显式建两个 writable 源并断言轮询。示例里的 `newDatasourceRouter(map,"")` 签名也是编的(真实为 `NewDatasourceRouter(ctx, bool, map)`) | **只修迁移双取句柄**;拓扑限制降级为可选的显式 routing mode,列入未决事项 |
| Task 1.0 `internal/testdb` | ❌ **放错 module**。`embedded-postgres`/`go-mysql-server` 只在 `tests/go.mod`,放主模块会把重测试依赖拖进运行时依赖图 | 改为 `tests/internal/testdb` |
| Phase 3–5 顺序(organization → identity → authn) | ❌ **与真实装配相反**。`organization/module.go:31` 需要 `InvitationCredentials`(即 authnWeb)与 `InvitationPrincipalCreator` | 按源库 `app/modules.go` 的真实装配顺序重排(§3) |
| Task 0.4 审计端口 | ❌ **所有权成环**。appender 要写 `iam_audit_events`,而 iam 又消费 `audit.Appender` | v8 将审计表和 `Appender` 一并归 `internal/iam/audit`；消费者解析 provider 根包接口(§2.2) |
| Task 0.3 `PermissionChecker` 仅 `HasPermission` | ❌ **缺方法**。`authz/register.go:34` 还需要 platform 与 entity 判定 | 端口图在 Phase 0 冻结；v3 误写 12 个，v4 按 §2.2 表格更正为 13 个 |
| Task 0.5 允许 SAML/SCIM 先 Disabled | ❌ **是范围缩减,不是依赖评审** | 撤销。全部模块生产实现到位;依赖评审只决定**引入时机**,不决定是否实现 |
| 漏迁 `core/extension/webauthnx` | ❌ **确认遗漏**,204 行生产代码,`authn/web.go:25` 直接依赖 | 补入 Phase 0(Task 0.4) |
| collation 只在 menus.route 提了一处 | ⚠️ **范围判断错误**。`utf8mb4_unicode_ci` 让 `Admin`/`admin` 在**所有**名称唯一键里相等,还可能让 CHECK 接受大小写变体 | 升为全局规则 R16 |

**统计修正**:源测试漏算 oauth(735)+ audit(1,497)= 2,232 行。源 IAM 域只有 5 个 migration source，但 `modules/iam/sql` 混有 5 个模块的表；目标拆为 **9 个自治 migration source**，加上现有 4 个共 **13 条**。

---

## Global Constraints

- 每台机器开始/恢复任务时先运行 `make iam-preflight`；任何 source tree、63 表集合、LF、Docker transport 检查失败都先修环境，不进入实现。后续脚本只使用生成的 `IAM_SOURCE_ROOT`、`DOCKER_CMD`、`DOCKER_TEST_HOST`，禁止重新写死路径或假设数据库容器运行在 localhost。
- `make ci` 必须绿:`fmt-check` → `lint` → `test-coverage` → `test-integration` → `build`;**每模块覆盖率 ≥ 90%**,新包在创建的那个任务里就登记进 `MODULE_COVERAGE_PACKAGES`,验收用 `make test-coverage`(`go test -cover` 不做门禁断言)。
- 模块经 `pkg/module` 能力接口接入。**允许**为迁移新增能力接口(那是该包的既定扩展机制);**不允许**改动生命周期的执行语义。
- DDL 的表名、索引名、约束名、触发器名走 `{{.Prefix}}`;**raw SQL 的表名同样必须前缀化**(R13)。
- `goosex.Source.TableName` 不得自行加前缀——`Source.versionTable()` 已经加了。
- 主键与外键一律 `BIGINT`(guid 生成);**协议外部标识符除外**(OIDC `client_id`、SAML `entity_id`/`key_id`、SCIM `external_id`)。
- 时间列:PG `TIMESTAMPTZ(3)` / MySQL `DATETIME(3)` / SQLite `DATETIME`,统一 UTC + 毫秒截断。
- "未设置"一律 `NULL`,**禁止哨兵 0**(生成列内部的派生哨兵除外,见 R5'')。
- 表名保留 `iam_` 前缀(最终 `sl_iam_roles`)。
- gorm 分层同 `internal/todo`:`model/` + `query/` + `dbgen/` + 包根 `//go:generate go run ./dbgen`。
- 不得为通过 lint 或覆盖率而弱化断言(CLAUDE.md §10)。

---

## 一、规格

### 1.1 迁移范围

**IAM 域内 9 个 Go 模块、63 张表；9 个模块分别拥有自己的迁移源**:

| 模块 | 生产代码 | 源测试 | 说明 |
|---|---|---|---|
| `iam` | 6,363 | 5,314 | 角色/权限/成员/菜单/实体/关系/平台管理 |
| `organization` | 4,149 | 2,625 | 租户/部门/岗位/用户组/邀请/数据范围 |
| `federation` | 3,274 | 2,972 | IdP/身份关联/SAML |
| `governance` | 1,614 | 1,154 | 访问申请/审批/复核 |
| `provisioning` | 4,076 | 2,101 | SCIM |
| `oauth` | 1,098 | 735 | OAuth 授权码/令牌/同意 |
| `audit` | 1,469 | 1,497 | 审计链/并发锁/完整性验证/导出 |
| `identity` | 1,642 | 1,442 | federation/provisioning 的硬依赖 |
| `authn` | 4,491 | 4,214 | federation/oauth/organization 的硬依赖 |
| **小计** | **28,176** | **22,054** | |

**扩展包**:`authz` 922 + `policyx` 777 + `authn`(Bearer/上下文)757 + `webauthnx` **204** + `secretx` 51 + `passwordx` 80 + `auditx` 32 = **2,823**

**总生产代码 ≈ 30,999 行**。源测试不能原样复制（bun、旧模块与测试夹具均不同），但测试行为必须逐项移植；最终规模以覆盖率和行为矩阵为准，不以预估测试行数作为验收条件。

> ⚠️ **约 6 万行**。9 个阶段,每阶段结束仓库 `make ci` 绿。按阶段推进、按阶段验收。

### 1.2 必须修复的既有缺陷

| 类 | ID | 缺陷 | 规模 |
|---|---|---|---|
| **A** | A1 | `iam_oauth_consents` 外键指向 organization 的 `iam_tenants`；由 oauth constraints milestone 在双方 base 后添加 | 1 |
| | A2 | `BIGINT NOT NULL DEFAULT ''` → 一律改可空 | **10 处** |
| | A3 | 外键两端类型不兼容 → 统一 BIGINT | **7 处** |
| **B** | — | 方言漂移(SQLite 已修、PG/MySQL 未跟上)→ 统一 BIGINT 后消解 | 6 组 |
| **C** | C1 | `iam_entities` 同级重名(可空 parent_id + NULL≠NULL);源库 `00011` 补了 COALESCE 索引却没删旧约束 | 1 |
| | C3 | 关系表外键不看 `resource_type`,`validEntityType` **只校验字符串格式** | 2 表 |
| | C4 | 30 处时间列用哨兵 0;`expires_at=0` 语义跨表不一致 | 30 |
| | C5 | MySQL 版 CHECK 全部漏写 | 24 表 |
| | C6 | MySQL TEXT 无默认值导致跨库 INSERT 行为不同 | — |
| **D** | D1–D4 | 无 `{{.Prefix}}`、BIGINT 时间列、索引命名不统一 | — |
| **E** | — | 文档缺陷:`.temp` 的 entity_id 注释错误、id 类型自相矛盾;目标库 `README.md` Testing 节失实(审计 F1)、`datasource.go:200` 注释失实(审计 F3) | — |

A3 明细:`iam_role_bindings.role_id`(VARCHAR32→BIGINT)、`iam_sessions.principal_id`、`iam_refresh_tokens.principal_id`、`iam_oauth_codes.principal_id`、`iam_oauth_codes.client_id`、`iam_relationships.resource_id`、`iam_resource_relationships.entity_id`。根因是 `iam_entities.id` 声明为 VARCHAR(64);连带修 `iam_oauth_clients.tenant_id`、`iam_audit_events.tenant_id/principal_id/target_id`。

### 1.3 DDL 改写规则

| 规则 | 内容 |
|---|---|
| R1 | 表/索引/约束/触发器名前置 `{{.Prefix}}` |
| R2' | 时间列 → `TIMESTAMPTZ(3)` / `DATETIME(3)` / `DATETIME`,UTC + 毫秒截断 |
| R3 | "未发生"的时间列 → 可空,判定 `IS NULL` |
| R4 | 外键与 guid 列 → BIGINT;协议外部标识符保持 VARCHAR |
| R5'' | `BIGINT DEFAULT ''` → 可空。树形表**保留可空 parent_id + 自引用外键**;同级唯一性 PG/SQLite 用双部分索引、MySQL 用生成列 `COALESCE(parent_id,0)` **并加 `CHECK (id > 0)`** 防止合法 id=0 与根哨兵冲突 |
| R6 | CHECK 三方言逐字对齐,命名 `{{.Prefix}}chk_<table>_<col>` |
| R7'' | 列宽**必须引用源码中的校验上限**,并在 DDL 注释里写明文件:行号。注意 Go 的 `len()` 是字节而 VARCHAR(n) 通常按字符计——取字节上限时须换算或改用 TEXT |
| R8' | 唯一约束中的可空列:**可选值保持可空**(NULL≠NULL 正是所需);仅"必有值且需唯一"时才收紧 |
| R9'' | 类型判别列 → 复合外键 `(tenant_id, resource_id, resource_type) → iam_entities(tenant_id, id, type)`,父表加唯一键 `(tenant_id, id, type)`。**`ON DELETE` 语义保持源库的 RESTRICT**;`type` 列约定不可变 |
| R10' | append-only:**源库三方言都已有触发器**,移植时加前缀、不得丢失。另给目标库 `operation_logs` 补齐(审计 F4) |
| R11'' | **SQL 随模块自治**(§2.1)。63 表是全域集合；跨模块 FK 必须放在拥有子表的模块中，并通过显式迁移里程碑 DAG 在所有依赖基础表之后执行 |
| R12 | 租户隔离由**仓储 API 强制接收 tenantID**;建立"仓储方法 × tenant 参数 × 跨租户负例"矩阵;`client`/`provider`/`role` 等增加 `(tenant_id, id)` 复合外键 |
| R13 | raw SQL 表名经解析器产出,禁止字面量;配前缀化的真实业务查询测试 |
| **R15** | **每条改写过的 DDL 必须在注释里标注源文件:行号**。v2 有三处"先定规则再自己违反"(RESTRICT→CASCADE、列宽、表数),静态检查抓不到语义变更,只能靠可追溯的出处 |
| **R16** | **collation 全局策略**:机器枚举列(`status`/`relation`/`subject_type`/`effect` 等)、哈希/令牌/WebAuthn handle 与协议标识符(`route`/tenant slug/OIDC issuer/SAML entity ID/SCIM external key)用 **binary collation**;业务名称的唯一键使用 `name_key = strings.ToLower(strings.TrimSpace(value))`,不折叠重音。源库已有的 `departments/groups/scim_directories/scim_targets.name_key` 保持,为 `roles.name`、`entities.name`、`positions.code` 补齐。菜单 `label` 不参与唯一约束,不属于 collation 决策。避免 `utf8mb4_unicode_ci` 让安全标识符错配、让 CHECK 接受大小写变体,同时保证三方言的名称唯一语义一致 |

---

## 二、关键设计决策

### 2.1 模块 SQL 自治与迁移里程碑

**源库边界依据(实测)**:按源库 5 个 migration source 计算，56 条外键中 13 条跨 source，分布为

```
organization -> iam            7
iam          -> organization   1     ← 与上一行构成双向环
provisioning -> organization   2
governance   -> organization   2
federation   -> iam            1
```

目标按 9 个自治模块重新划分后，原 `modules/iam/sql` 内部关系也成为跨模块 FK，最终为 **27 条**：

```
authn       -> identity        11
oauth       -> identity         3
oauth       -> organization     1
organization -> core            7
governance  -> organization     2
provisioning -> organization    2
federation  -> identity         1
```

`core` 与 `organization` 在 schema 层**互相引用**。环说明当前执行器“按注册顺序把每个 source 一次跑到底”的粒度不足，但不改变 SQL 所有权。目标按实际写入职责形成 9 个 migration source：

| 目标模块 | 源模块 | 表数 | 版本表 |
|---|---|---:|---|
| `internal/iam/core` | `modules/iam` 的授权/关系/平台表 | 13 | `goose_iam` |
| `internal/iam/authn` | 从 `modules/iam/sql` 归还认证表 | 13 | `goose_authn` |
| `internal/iam/identity` | 从 `modules/iam/sql` 归还 principal/服务账户表 | 3 | `goose_identity` |
| `internal/iam/oauth` | 从 `modules/iam/sql` 归还 OAuth 表 | 4 | `goose_oauth` |
| `internal/iam/audit` | 从 `modules/iam/sql` 归还审计表 | 3 | `goose_audit_domain` |
| `internal/iam/organization` | `modules/organization` | 14 | `goose_organization` |
| `internal/iam/governance` | `modules/governance` | 4 | `goose_governance` |
| `internal/iam/provisioning` | `modules/provisioning` | 5 | `goose_provisioning` |
| `internal/iam/federation` | `modules/federation` | 4 | `goose_federation` |

每个 source 的建表迁移先只创建本模块表及模块内约束；跨模块 FK 使用同一模块后续 `ALTER TABLE` migration，仍由拥有子表的模块维护。模块通过可选 `MigrationMilestoneProvider` 声明 `base`/`constraints` 里程碑及依赖，应用迁移器把 `(module, milestone)` 作为 DAG 节点：所有相关 `base` 完成后才执行 `constraints`；down 按反向 DAG 执行。普通模块继续只实现现有 `MigrationSourceProvider`，行为不变。

其中 `core` 13 表为 entities、menus、platform administrators/grants、policy revisions、relationships、resource relationships、role bindings/members/permissions、roles、temporary grants、tenant members；`authn` 13 表为 credentials、email verification、MFA、notification outbox、passkey、password history/recovery、recovery codes、sessions、step-up；`identity` 3 表为 principals 与 service account 两表；`oauth` 4 表为 clients/codes/consents/refresh tokens；`audit` 3 表为 events/heads/retention policies。Task 1.3 用精确表名集合锁定此映射。

门禁必须证明：`core.base` 与 `organization.base` 都完成后，双方 `constraints` 才可执行；指定模块 down 若仍有已应用的外部依赖必须拒绝并列出依赖者，不能依赖数据库偶然报 FK 错误。63 表集合由测试从 9 个模块的迁移源合并断言，禁止另建 `OWNERSHIP.md` 或集中 schema 包复制所有权事实。

### 2.2 能力端口图(Phase 0 冻结)

从源库真实构造签名提取(非推测):

| 端口 | 提供方 | 消费方 | 源证据 |
|---|---|---|---|
| `IDGenerator` | guid | 全部 | 各 `NewModule` |
| `audit.Appender` | audit | 全部 | `auditx/port.go` |
| `authz.Registrar` | authz | 全部 | 各 `NewModule` |
| `AuthorizationEvaluator` | iam | iam | `iam/module.go:21` |
| `PermissionChecker`(tenant/**platform**/**entity** 三类) | iam | authz Registrar | `authz/register.go:34` |
| `ActiveMemberChecker` | iam(`NewMembershipChecker`) | organization | `organization/module.go:31` |
| `AdministratorGuard` | iam(`NewAdministratorGuard`) | organization, identity, governance | `app/modules.go:64` |
| `InvitationCredentials` | authn(`authnWeb`) | organization | `app/modules.go:83` |
| `InvitationPrincipalCreator` | app 装配 | organization | `app/modules.go:83` |
| `RoleGrantStore`(函数字段结构体) | iam 的多个函数 | governance | `app/modules.go:86-98` |
| `IdentityProvisioner` | identity(`identityService`) | provisioning, federation | `app/modules.go:85,101` |
| `GroupProvisioner` | organization(`ProvisioningGroups()`) | provisioning | `app/modules.go:85` |
| `WebService`(认证 Web 流程) | authn | oauth, federation, organization | `app/modules.go:50,83,101` |

> **`module.Services` 按精确 `reflect.Type` 做键**(`pkg/module/services.go:45`),不会自动匹配"可赋值的实现"。因此提供方和消费方必须引用**同一个接口类型**。沿用当前项目模式：`IDGenerator` 直接复用 `internal/infra/guid.Source`；其余接口由提供方模块根拥有（如 `internal/iam/audit.Appender`、`internal/iam/core.PermissionChecker`、`internal/iam/authn.WebService`），通过 `Provide/Resolve` 注入。禁止新增中央 `pkg/port`/`internal/port` 包。
>
> **声明式装配**:源库只有 `audit`/`iam`/`organization`/`provisioning`/`governance`/`federation` 6 个模块有 `NewDeclarationOnlyModule(registrar)`。`authn`/`identity`/`oauth` 没有该构造器；这些阶段必须在目标端先明确声明与运行时装配的边界，或在依赖齐全后一次接入，不得调用不存在的源 API。

### 2.3 阶段顺序(按真实装配,非模块名)

源库 `app/modules.go` 的实际构造顺序:

```
authnWeb → administratorGuard(iam,无依赖) → identityService(需 guard)
  → identity 模块、iam 模块 → organization(需 iam checker + guard + authnWeb)
  → provisioning(需 identityService + organization groups)
  → governance(需 guard + iam 函数) → audit → federation(需 identityService + authnWeb)
```

v2 把 organization 放在 identity/authn 之前,与此相反。v3 按上述顺序编排。

### 2.4 IAM 域与自治模块结构（v8 冻结）

`internal/iam/` 只是 Go 目录命名空间，不包含 Go 文件，不注册生命周期，也不拥有 SQL：

    internal/iam/
      core/                     # 源 modules/iam；ModuleName() == "iam"
      authn/
      identity/
      oauth/
      organization/
      audit/
      governance/
      provisioning/
      federation/

每个子目录都是独立 `pkg/module`，内部沿用 `internal/todo` 的扁平模块结构：

    internal/iam/<module>/
      module.go                 # 生命周期、Provide/Inject、RegisterHTTP
      service.go                # 模块用例；按源职责增加同级业务文件
      repository.go             # 需要时存在，gorm/gen 持久化
      http.go                   # Huma operation、DTO、guard、错误映射
      migrate.go                # 仅数据库 owning module 存在
      sql/{postgres,mysql,sqlite}/
      model/ query/ dbgen/ i18n/
      *_test.go

`core` 不能改名为 `authz`：源 `iam` 除授权外还拥有成员、菜单、实体、关系和平台管理，缩名会误导职责。跨模块接口直接由提供方模块根声明，不设 `contract` 子包和 type alias 中转；真正全项目通用的能力继续留在既有 `internal/infra` 或 `pkg`。

**项目原生依赖规则（由 `tests/architecture` 检查）**：

1. `internal/app` 是唯一可以构造多个 concrete module 并完成总装配的位置；`internal/iam/` 不增加域级 composition root。
   Composition root 必须登记完整 module inventory，不得按业务开关手工 append/omit；条件模块按 `infra` 既有模式实现 `module.Enabler`，由 `Registry.Activate()` 统一裁剪并复核依赖。
2. 模块 A 可导入依赖模块 B 的根包接口，但不得调用 B 的构造器，也不得导入 `B/model`、`B/query`、`B/dbgen` 或 concrete service；依赖同时写入 `ModuleDependencies()`。
3. 模块根可以包含本模块业务、仓储和 HTTP，不强制“根只装配”，也不按 `domain/service/repository/http` 机械拆 package。
4. `model` 只表达持久化行，`query` 只放生成物，`dbgen` 只放生成命令；模块公开接口不得泄漏 model/query 类型。
5. 模块拥有自己表的 DDL、迁移版本表和跨模块外键；本域 9 个模块都有实际表，不存在空 migration source。
6. 9 个手写模块包分别登记覆盖率 ≥90%；model/query/dbgen 走 schema 契约、生成零 diff 与编译门禁。

### 2.5 初始化数据与安全自举方案（v10）

初始化分为“平台一次性自举”和“每租户创建时自举”，二者都不是 schema migration。权限 catalog 来自 `authz.DefaultActions()`，不需要复制到数据库；部门、用户组、岗位、普通角色、OAuth client、IdP 等属于租户业务数据，也不做全局 seed。

#### 2.5.1 平台管理员 bootstrap

**入口：**增加显式运维命令 `7link-api iam bootstrap-platform-admin`。命令复用正式配置、datasource、ID generator、password hashing 和 audit 能力，要求 IAM migrations 为 clean；不启动 HTTP/gRPC server，不在 `serve`、`make dev` 或 migration 中隐式执行。

**秘密输入：**login/display name/email 可用 flags；password 只允许 `--password-file` 或 stdin，禁止命令行明文参数、固定默认密码和写入版本库。命令不得打印密码/hash；文件权限不安全时拒绝执行。生产编排应从 Secret volume/secret manager 挂载一次性文件。

**一个事务内写入：**

1. 获取现有 `dlock` 能力上的固定锁 `iam/bootstrap/platform-admin`，随后在事务内再次检查状态，保证 SQLite/MySQL/PostgreSQL 并发执行只有一个胜者。
2. 断言不存在其他 full/restricted platform administrator；请求 login 若已属于非目标 principal 或缺失完整 credential，fail closed。
3. 创建 active principal、password credential，以及 `iam_platform_administrators` full administrator 行。
4. 写 tenant `0` 的 `platform_administrator_bootstrapped` 审计事件；任一步失败整体回滚。
5. 默认 **create-only**：同一 login 已经是 full administrator 时报告 already bootstrapped，但不更新 profile、不重置密码、不撤销 session。密码轮换必须走独立、审计化的 reset 流程。

现有 `authn.EnsureBootstrapPrincipal` 只能写 principal/credential，而且会 reconcile profile/password/security state，只适合测试夹具，不能直接作为生产 bootstrap 契约。生产实现应复用其校验/hash 基础能力，但把跨 authn/core/audit 的原子编排放在 composition boundary，并提供接受同一 `*gorm.DB` 的窄能力，遵守 D10 的模块所有权。

**运行门禁：**readiness 增加可配置的 `iam.require_platform_administrator`（生产默认 true，migration/恢复工具可关闭）。启用时，IAM 已开启但不存在任何 full/restricted platform administrator 应为 `NOT_SERVING`，避免空库仍显示可用。命令退出码必须区分 created、already bootstrapped、conflict、schema not ready 和 transient failure。

#### 2.5.2 新租户 owner bootstrap

`organization-create-tenant` 必须接收或从已认证 actor 明确得到 `owner_principal_id`，并在创建 `iam_tenants` 的**同一个数据库事务**中完成：

1. 校验 owner principal active，创建 active `iam_tenant_members`。
2. 创建租户内置 `Administrator` role，授予 `tenant_administer`，将 owner 加入该 role。
3. 为 `core.DefaultMenus()` 的 15 项分配 ID 并写入 `iam_menus`；这些是租户默认导航元数据，不是全局权限 catalog。
4. 只 bump 一次 policy revision，写包含 owner/role/menu 数量的 `tenant_bootstrapped` 审计事件。
5. 任一步失败回滚 tenant 本身，禁止产生“存在租户但无人可管理”的半成品。

模块边界采用现有能力注入模式：core 提供一个可绑定调用者事务的 `TenantBootstrapper` 窄能力，organization 在自己的 tenant transaction 中调用；不得由 organization 直接写 core 表，也不得在 HTTP handler 中串联多个独立事务。为已经存在但零成员的租户提供单独的 create-only repair 命令，且仅允许在没有任何 member/role grant 时运行。

#### 2.5.3 初始化验收矩阵

- SQLite、真实 MySQL 8.4、PostgreSQL 16：fresh migrate → platform bootstrap → 登录 → create tenant → owner 访问 tenant 管理 API。
- 平台命令并发两次只有一个 created；同参重跑不改密码；异参、已有管理员、login 冲突全部 fail closed。
- tenant bootstrap 任一插入/audit 失败时 tenant/member/role/grant/menu 全部为零；并发 slug 冲突只有一个完整 tenant。
- 断言 full platform administrator 可管理平台，tenant owner 可管理本 tenant，但二者不会因此获得另一层未授予的身份。
- 默认菜单恰为 15 条、permission code 全在 catalog 中，`EffectiveMenus()` 对 owner 返回完整树，对普通成员按权限过滤。

---

## 三、阶段与任务

```
Phase 0  前置修复 + 扩展包 + 端口图冻结 + 依赖评审
Phase 1  9 个自治迁移源:63 表 × 3 方言 + 里程碑 DAG + 真引擎验证
Phase 2  internal/iam/core 完整模块(运行时装配留到 Phase 8)
Phase 3  authn(含 webauthnx)+ identity
Phase 4  oauth
Phase 5  organization
Phase 6  audit(链式追加/完整性验证)
Phase 7  governance + provisioning + federation
Phase 8  9 模块 composition root、端口解析与跨模块负例收口
Phase 9  跨模块验收 + 性能 + 文档
Phase 10 产品化闭环：安全自举 + 权限执行 + 生命周期缺口
```

### Phase 0

#### Task 0.P: 两机环境预检与恢复

- [x] **Step 1:** 新增 `make iam-preflight`，按 OS 自动解析源目录，固定并验证 `apps/server` tree，拒绝源目录未提交修改，并从正式 SQL 断言 PostgreSQL/MySQL/SQLite 各 63 张表且集合相等。
- [x] **Step 2:** Windows 自动处理 `core.autocrlf=true` 导致的纯 CRLF 检出；只转换 Git 报告为 `i/lf w/crlf` 的 Go 文件。仓库用 `.gitattributes` 固定 Go/Makefile/shell/SQL 为 LF。
- [x] **Step 3:** Docker 优先本机，Windows 自动回退到 `root@10.0.0.100`；专用 key 只放 `~/.ssh/iam-docker-10.0.0.100`，`.temp/iam-env.mk` 只记录命令与路径。远端 Docker 缺失时允许在已确认主机指纹后安装 Debian `docker.io`。
- [x] **Step 4:** 在 Mac 上实际运行一次 `make iam-preflight` 与 `make ci`，记录结果；Windows 验证不能代替 Mac 验证。

#### Task 0.0: 修复迁移双取写句柄(审计 F2 的可修部分)

> **v2 的 `at most one writable datasource` 已撤销**:`datasource_test.go:229` 显式锁定了多写者轮询契约,同步多主也可能是合法拓扑。按端点数量拒绝是错的修法。

**Files:** Modify `internal/app/migration.go:72,78`, `internal/app/module.go`;Test `internal/app/migration_test.go`

- [x] **Step 1: 写失败测试**

```go
// TestMigrationUsesSingleWriteHandle 断言迁移只取一次写连接。
// 原代码第 72 行取一次做 nil 检查、第 78 行再取一次执行;Write() 是
// 无状态轮询,双写者拓扑下这两次会落到不同节点 —— 检查 A、迁移跑 B。
func TestMigrationUsesSingleWriteHandle(t *testing.T) { /* 统计 Write() 调用次数,断言 == 1 */ }
```

> 注:`appenv.Runtime` 只接受具体的 `*DatasourceRouter`(`runtime.go:67`),无法注入 mock。计数需通过别的观测点——**编写前先确认可行的观测手段**,不要照抄 v2 那个编不过的示例。

- [x] **Step 2:** 跑测试确认失败 → **Step 3:** 合并为单次取用 → **Step 4:** `make ci`
- [x] **Step 5:** 提交

#### Task 0.1: 启用 SQLite 外键(严格版)

**已实测**:不加参数时 `PRAGMA foreign_keys=0`,指向不存在父行的插入**静默成功**;加 `_foreign_keys=on` 后报 `FOREIGN KEY constraint failed`。现有 4 个模块的迁移**零个外键**,因此本改动不会打破既有测试。

- [x] **Step 1:** 写测试(孤儿插入必须失败;并断言连接池中**多条**连接的 `PRAGMA foreign_keys=1`)
- [x] **Step 2:** 实现——**解析**参数而非字符串包含:`off`/`false`/`0` 直接报配置错误;只认当前 `mattn/go-sqlite3` 支持的 `_foreign_keys`/`_fk`(v2 把驱动不支持的 `_pragma=foreign_keys` 当作已启用,是错的)
- [x] **Step 3:** `make ci`

#### Task 0.2: policyx(拆两层)

`authz/register.go:13` import `policyx`,故 policyx 先建;源 `policyx/policy.go` import bun 且直接写 core 自有表，故拆为 `pkg/extension/policyx` 的 condition/member_rule 纯逻辑与 `internal/iam/core` 的 GORM revision store。

- [x] **Step 1:** 移植零依赖的 `condition.go`/`member_rule.go`
- [x] **Step 2:** revision store 改 gorm。**构造函数须接收 prefix**(`NewRevisionStore(prefix)`),源实现是硬编码表名;**`Lock` 是源语义的关键方法,与 `Bump`/`Current` 一并迁移**
- [x] **Step 3:** 并发递增不丢更新的测试 → 登记覆盖率 → 提交

#### Task 0.3: authz(依赖 0.2)

- [x] **Step 1:** 复制 `authz/{catalog,decision,gate,register}.go` + `authn/{verifier,bearer,context,model}.go`(**8 个文件,v1 漏了 verifier.go**)
- [x] **Step 2:** Registrar 的协作者改为 authz 消费侧窄接口,**含 tenant / platform / entity 三类判定**(`register.go:34`)；Phase 2 的 IAM 实现该接口，避免 authz 反向依赖尚未完成的 IAM 模块
- [x] **Step 3:** 登记覆盖率 → 提交

#### Task 0.4: webauthnx + secretx + passwordx + 审计端口

> **webauthnx 是 v2 的遗漏**:204 行生产代码,`authn/web.go:25` 直接依赖。

- [x] **Step 1:** 移植 `webauthnx`(204)、`secretx`(51)、`passwordx`(80)
- [x] **Step 2:** `DomainEvent` 与 `Appender` 直接在 `internal/iam/audit` 根包声明，与现有 `internal/infra/audit.Recorder` 明确区分；此阶段只冻结契约，链式实现放 Phase 6
- [x] **Step 3:** 给目标库 `operation_logs` 补三方言 append-only(审计 F4),并在注释写明:触发器拦不住 `TRUNCATE`、高权限账号可禁用触发器,**完整保证还需业务账号最小权限**——不在代码里假装已解决
- [x] **Step 4:** 登记覆盖率 → 提交

#### Task 0.5: 冻结端口图 + 第三方依赖评审

- [x] **Step 1:** 按 §2.2 冻结 13 个能力接口：复用 `guid.Source`；authz 专用 checker 放消费侧 authz 包；其余直接由 `internal/iam/<provider>` 模块根声明。Phase 0 只创建必要的模块根与编译期 fixture，**后续不得新增接口而不回头修订本计划**
- [x] **Step 2:** 先从源 `go.mod` 与 import 图列出完整模块路径，再评审候选依赖类别（WebAuthn/TOTP/SAML/SCIM filter/验证码/对象存储/Redis），去掉功能重复项并产出 `.claude/decisions.md`；禁止用“十个”“四件套”之类不可复核的简称代替清单

> **范围澄清**(撤销 v2 的措辞):依赖评审只决定**引入时机与选型**(如两个功能重叠的验证码库至少砍一个),**不决定是否实现**。用户要求"全量全功能到位",SAML/SCIM 必须有生产实现与可用路由,不接受长期 `Enabled: false`。

- [x] **Step 3:** `make ci` → 提交

### Phase 1 — 模块自治迁移源与里程碑编排

#### Task 1.0: `tests/internal/testdb`

> **v2 把它放主模块是错的**:`embedded-postgres`/`go-mysql-server` 只在 `tests/go.mod`。

- [x] 提取 `OpenPostgres`/`OpenMySQL`/`OpenSQLite` 到 `tests/internal/testdb`;主模块单测保留轻量 SQLite helper。`tests/go.mod:96` 的 replace **已存在**,无需改动

#### Task 1.1: 真迁移三引擎验证套件(审计 F1)

> **顺序修正**:先用现有 4 个 migration module 建立三引擎套件，再扩展里程碑执行器，最后加入 9 个 IAM 域 migration source；每个提交点必须可编译。

- [x] **Step 1:** 建参数化套件,首批覆盖 `todo`/`dlock`/`wuid`/`audit` × 三引擎(**这四个模块的迁移从未在 PG/MySQL 上跑过**),含幂等与 up/down 对称
- [x] **Step 2:** 前缀测试——**只设 `Prefix`**,不要手工给 `TableName` 加前缀(`Source.versionTable()` 已经加了);断言 `sl_<表>` 存在、`<表>` 不存在、`sl_sl_*` 不存在
- [x] **Step 3:** 标识符长度测试(前缀上限 21 字符 + 长约束名可能越过 PG 63 字节 / MySQL 64 字符)
- [x] **Step 4:** 修正 `README.md` Testing 节的失实措辞(缺陷 E),说明实际覆盖范围与 go-mysql-server ≠ MySQL 8
- [x] **Step 5:** 跑通并提交(**此提交点可编译**)

#### Task 1.2: 迁移里程碑执行器

**Files:** `pkg/module/module.go`、`internal/app/migration.go` 及测试；不新增 schema package。

- [x] **Step 1:** 用失败测试锁定 `MigrationMilestoneProvider` 的 DAG 校验：节点唯一、依赖存在、无环、同一 source 版本只前进；未实现该接口的模块保持当前一次执行到底的行为。
- [x] **Step 2:** up 按里程碑拓扑执行 `goosex.MigrateUpTo`，down 按反向拓扑；指定模块 down 遇到已应用依赖者时拒绝并列出节点，不隐式级联。
- [x] **Step 3:** 真实 SQLite/PostgreSQL/MySQL 套件构造 A↔B FK，证明 `A.base → B.base → A.constraints/B.constraints` 可执行，任意少跑 base 都失败；迁移状态报告仍按模块聚合且能显示当前 milestone。
- [x] **Step 4:** `make ci` 后提交；该执行器必须是通用可选能力，不写 IAM 模块名硬编码。

#### Task 1.3: 9 个模块迁移源、63 表

**Files:** 9 个 `internal/iam/<module>/{migrate.go,sql/...}`。

> MySQL DDL 隐式提交，单文件中途失败会留下半套 schema 而 goose 版本未推进。每个文件一张主表及紧密从属表，文件头注明事务策略；创建 `docs/runbook-migration-recovery.md`。

- [x] **Step 1:** 从源 SQL 与 9 个模块的真实写入路径建立精确表名清单；断言各模块三方言集合相等、9 模块集合互斥、合并后恰为 63 表（13+13+3+4+3+14+4+5+4）；禁止维护第二份集中 ownership 文件。
- [x] **Step 2:** 各模块逐表套 R1–R16。**每条改写标注源文件:行号(R15)**。范例:

```sql
-- 源: iam/sql/postgres/00001_iam_authorization.sql:2-11
CREATE TABLE {{.Prefix}}iam_roles (
    tenant_id   BIGINT         NOT NULL,
    id          BIGINT         NOT NULL,
    name        VARCHAR(128)   NOT NULL,   -- R7'': iam/service.go:105 len(name)>128
    description TEXT           NOT NULL,   -- R7'': 同上 len(description)>4096;
                                           -- Go len() 计字节,VARCHAR 计字符,故用 TEXT
    created_at  TIMESTAMPTZ(3) NOT NULL,   -- R2'
    updated_at  TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT {{.Prefix}}uq_iam_roles_name UNIQUE (tenant_id, name_key)  -- R16
);
```

关系表(**注意 RESTRICT**):

```sql
    -- 源: iam/sql/postgres/00015_relationships.sql:11-12 (ON DELETE RESTRICT)
    -- R9'': 仅在原外键上增加 resource_type 列,使被引用实体的 type 必须
    -- 与本行一致。删除语义保持 RESTRICT —— 源库的意思是"有授权关系时
    -- 拒绝删除实体",改成 CASCADE 会变成静默删除授权记录。
    CONSTRAINT {{.Prefix}}fk_iam_relationships_resource
        FOREIGN KEY (tenant_id, resource_id, resource_type)
        REFERENCES {{.Prefix}}iam_entities (tenant_id, id, type) ON DELETE RESTRICT
```

`iam_entities`(R5''):可空 `parent_id` + 自引用 FK + PG/SQLite 双部分唯一索引;MySQL 生成列 `COALESCE(parent_id,0)` **并加 `CHECK (id > 0)`**。

- [x] **Step 3:** 将所有跨模块 FK 从 CREATE TABLE 移到子表所属模块的 constraints migration；声明 §2.1 的 milestone 依赖，保持目标 9 模块边界下的 27 条约束和原 DELETE 语义。
- [x] **Step 4:** 移植三方言 append-only 触发器；9 个 `MigrationSource()` 使用 §2.1 的独立版本表名，不自行加前缀。
- [x] **Step 5:** 接入 Task 1.1/1.2 套件，三引擎执行 up → validate → reverse down → up，断言 63 表与 9 个版本表。
- [x] **Step 6:** 提交。

#### Task 1.4: 真实 MySQL 8 门禁 + baseline 冻结

> **门禁强化**(codex C5):v2 允许 docker 不可用时只记 Known Issue 就冻结 baseline,等于把未验证的 MySQL schema 永久钉死。

- [x] **Step 1:** `make test-mysql8`——固定镜像 digest、`sql_mode` 与非 root trigger 创建前提，所有容器命令使用 `$(DOCKER_CMD)`；连接地址使用 `$(DOCKER_TEST_HOST)`。用唯一 label + `trap` 保证只清理本次容器、healthcheck 等就绪、随机远端端口、`-count=1`，禁止假设容器端口位于 localhost
- [x] **Step 2:** **硬门禁**:真实 MySQL 8 通过 **且** R16 collation 策略、R9'' 的 `type` 不可变约定均已决策,才允许生成 `schema.lock`
- [x] **Step 3:** 静态对账(正则,**声明为真引擎验证的补充而非替代**;先剥注释再匹配)
- [x] **Step 4:** 冻结后已发布迁移文件不得改写,只允许追加

### Phase 2–8 — Go 代码移植

#### 统一执行契约

- [x] 每个模块先从 `$(IAM_SOURCE_ROOT)/internal/modules/<module>` 建立 source manifest；清单之外的源文件不得静默遗漏。目标路径固定为 `internal/iam/core`（源 iam）或 `internal/iam/<module>`。
- [x] 目标严格使用 §2.4 的域目录 + 自治扁平 module 结构。先把 source manifest 中每个 type/function/method 登记到目标同级业务文件或 model/query/dbgen/i18n；任何未映射 symbol 都视为遗漏。
- [x] 每张本模块拥有的表都做 model ↔ 三方言 DDL 双向契约；MySQL 的 `parent_key` 等生成列只在显式 allowlist 中忽略，不能放宽为“忽略所有额外列”。
- [x] bun 仓储逐方法改写为 gorm/gorm-gen：显式表前缀、事务句柄、行锁、upsert、唯一冲突和 `RowsAffected` 语义逐项保留。`Updates(struct)` 跳过零值，清空字段/撤销状态必须用 `Select` 或 map。
- [x] 时间列逐列从整数改为 `time.Time`/`*time.Time`；`> now` 改 `After(now)`，原 `0` 的“永不过期/尚未发生/已经过期”语义逐列移植，禁止批量替换。
- [x] 仓储公开方法逐项标注 scope：tenant-scoped 方法必须显式接收 tenant ID 并同时约束查询、更新和删除；platform/global 方法必须由 platform guard 保护，不伪造 tenant 参数。
- [x] 每个自治模块创建时立即加入 `MODULE_COVERAGE_PACKAGES`；`model/query/dbgen` 只能按生成包规则显式排除。源测试按“业务/service/repository/http/module/i18n/并发事务”建立行为对照表；架构测试和所有手写模块覆盖率 ≥90% 后运行 `make ci`。
- [x] 源服务的 **208 条** operation 是功能完整性基线，不是目标 URL 模板。每个模块在实现 HTTP 前必须冻结一对一 source→target 映射；不得遗漏、合并或凭空增加能力。目标以批准后的 `OperationID|METHOD|PATH` 集合验收，并校验 RESTful 资源命名、method、参数、schema、状态码、安全声明和运行时路由。

#### Task 2: 完整迁移 `iam`

**Source manifest**

- Production: `administrator.go`, `authorization.go`, `authorizer.go`, `data_scope.go`, `directory_binding.go`, `domain.go`, `dynamic_group.go`, `entity.go`, `escalation.go`, `i18n.go`, `membership.go`, `module.go`, `pagination.go`, `platform.go`, `platform_rest.go`, `relationship.go`, `relationship_rest.go`, `repository.go`, `repository_admin.go`, `rest.go`, `review_grant.go`, `service.go`, `service_admin.go`, `temporary_grant.go`, `transaction.go`, `i18n/locales/{en-US,ms-MY,zh-CN}.json`。
- Tests: `administrator_test.go`, `authorization_test.go`, `authorizer_test.go`, `data_scope_test.go`, `directory_binding_test.go`, `dynamic_group_test.go`, `entity_test.go`, `escalation_test.go`, `i18n_test.go`, `module_test.go`, `pagination_test.go`, `platform_rest_test.go`, `platform_test.go`, `relationship_test.go`, `repository_test.go`, `rest_test.go`, `review_grant_test.go`, `service_admin_test.go`, `service_test.go`, `temporary_grant_test.go`, `transaction_test.go`。
- Phase 1 only: `migrate.go`, `migrate_test.go`, `sql/{postgres,mysql,sqlite}/`。

- [x] **2.1 Model/query/dbgen:** 在 `internal/iam/core/{model,query,dbgen}` 为本模块 13 表建立模型与生成查询；复合 FK、nullable time、binary/name_key 语义写入契约测试。
- [x] **2.2 Repositories:** 将 `repository*.go`、`transaction.go` 与 raw SQL 按源文件职责迁入 `internal/iam/core` 同级文件并改为前缀安全 gorm；覆盖角色/权限/成员/菜单/平台管理员/实体/关系/临时授权的 tenant 条件。
- [x] **2.3 Domain/services:** 在 core 根包同级业务文件中移植全部领域与用例；直接声明并提供 checker/guard/store 窄接口，不创建 feature 或 contract 中转包。
- [x] **2.4 HTTP/module:** 根包 HTTP 文件注册 51 条 IAM 路由；`module.go` 保留 `NewDeclarationOnlyModule`，Phase 8 才接入 app composition root。
- [x] **2.5 Negatives/tests:** tenant A 对 tenant B 的 role/member/menu/entity/relationship ID 执行 get/list/update/delete/grant 必须返回 not-found/空集合且 B 行不变；platform 管理接口无 platform principal 必须拒绝；并发删除最后管理员只能一个成功。
- [x] **2.6 Gate:** 登记 `7link.com/platform/internal/iam/core`，运行 `go generate ./internal/iam/core`、模块定向测试和 `make ci`。

#### Task 3: 迁移 `authn` 与 `identity`

**authn source manifest**

- Production: `captcha.go`, `gocaptcha.go`, `i18n.go`, `invitation.go`, `mfa.go`, `module.go`, `passkey.go`, `recovery.go`, `registration.go`, `rest.go`, `stepup.go`, `verification.go`, `web.go`, `i18n/locales/{en-US,ms-MY,zh-CN}.json`。
- Tests: `captcha_test.go`, `i18n_test.go`, `invitation_test.go`, `mfa_test.go`, `module_test.go`, `passkey_test.go`, `recovery_test.go`, `registration_test.go`, `rest_test.go`, `stepup_test.go`, `verification_test.go`, `web_test.go`。

- [x] **3.1 authn data/repositories:** 在 `internal/iam/authn/{model,query,dbgen}` 为 13 张认证表生成查询；根包 repository 文件保留锁定、一次性消费、轮换与撤销事务。
- [x] **3.2 authn behavior:** 在 authn 根包按源 basename 移植 captcha、口令、TOTP、WebAuthn、注册、登录、验证和找回；只有文件职责过大且有实际独立边界时再单独评审分包。
- [x] **3.3 authn module/contracts:** 根包 `http.go` 通过 `RegisterHTTP` 注册 30 条 RESTful 路由；稳定导出 `InvitationCredentials`/`WebService`，解析 `guid.Source` 与 `internal/iam/audit.Appender`。实际 OpenAPI 与下方 authn 目标契约 30/30 相等。
- [x] **3.4 authn negatives/tests:** HTTP 与 service/repository 测试证明 principal A 不得读、改名、删除或撤销 principal B 的 session/passkey；MFA、recovery、verification token 使用 CAS 单次消费，8 路并发只能一次成功。authn 只负责 invitation ID + HMAC 凭据；跨 tenant 接受由拥有 invitation row 的 organization 在 Task 5.4 验证，不能把 tenant 查询塞入 authn。

**identity source manifest**

- Production: `i18n.go`, `module.go`, `provisioning.go`, `rest.go`, `service.go`, `service_account.go`, `i18n/locales/{en-US,ms-MY,zh-CN}.json`。
- Tests: `i18n_test.go`, `module_test.go`, `provisioning_test.go`, `rest_test.go`, `service_account_test.go`, `service_test.go`。

- [x] **3.5 identity module:** 在 `internal/iam/identity` 根包按源文件移植 principal/service account 行为，在 `model/query/dbgen` 为 3 张表生成查询；复用 `core.AdministratorGuard`，不复制规则。
- [x] **3.6 identity contracts/HTTP:** 14 条路由与 `IdentityProvisioner` 已完成；剩余 core guard 必须经 `module.Services` 解析，并在 `ModuleDependencies` 声明 core 依赖。
- [x] **3.7 identity negatives/tests:** 全局 principal guard 与直接 identity tenant 负例已完成；provisioning/invitation 创建链路的 tenant A→B 负例由拥有 directory/invitation 上下文的 Task 5.4、7.5 与 8.5 闭环。
- [x] **3.8 Gate:** authn/identity generate、定向测试、覆盖率与 `make ci` 已通过；3.6/3.7 闭环后复跑并关闭本 Gate。

#### Task 4: 迁移 `oauth`

**Source manifest**

- Production: `i18n.go`, `module.go`, `rest.go`, `service.go`, `i18n/locales/{en-US,ms-MY,zh-CN}.json`。
- Tests: `module_test.go`, `rest_test.go`, `service_test.go`。

- [x] **4.1 Data/behavior:** 在 `internal/iam/oauth` 根包按源 basename 移植 protocol/client administration，`model/query/dbgen` 为 4 张表生成查询；授权码、refresh token、secret rotation/revoke 保留事务与一次性语义。
- [x] **4.2 HTTP/module:** 根包注册 12 条路由并解析 `authn.WebService`、`guid.Source`、`audit.Appender`；源无 declaration-only API。
- [x] **4.3 Negatives/tests:** tenant A 不能列出、更新、删除或 rotate tenant B client；B 的 code/token 不得被 A client 兑换/introspect/revoke；授权码与 refresh token 并发重放只有一次成功。
- [x] **4.4 Gate:** 登记 `internal/iam/oauth`，运行 generate、架构/定向测试和 `make ci`。

#### Task 5: 迁移 `organization`

**Source manifest**

- Production: `domain.go`, `group.go`, `group_repository.go`, `group_rest.go`, `group_service.go`, `i18n.go`, `invitation.go`, `invitation_rest.go`, `module.go`, `position.go`, `position_repository.go`, `position_rest.go`, `position_service.go`, `provisioning.go`, `repository.go`, `rest.go`, `service.go`, `tenant.go`, `tenant_rest.go`, `i18n/locales/{en-US,ms-MY,zh-CN}.json`。
- Tests: `group_repository_test.go`, `group_rest_test.go`, `group_service_test.go`, `group_test.go`, `invitation_rest_test.go`, `invitation_test.go`, `module_test.go`, `position_repository_test.go`, `position_rest_test.go`, `position_service_test.go`, `position_test.go`, `provisioning_test.go`, `repository_test.go`, `rest_test.go`, `service_test.go`, `tenant_rest_test.go`, `tenant_test.go`。
- Phase 1 only: `migrate.go`, `migrate_test.go`, SQL。

- [x] **5.1 Model/behavior:** 在 `internal/iam/organization` 根包按源 basename 保留 tenant/directory/invitation 职责，`model/query/dbgen` 为 14 张表生成查询，name_key 与树 parent 做三方言契约。
- [x] **5.2 Repositories:** 同级 repository 文件完成 bun→gorm；邀请接受等原子流程由根包 service 使用同一事务协调，所有实体查询带 tenant ID。
- [x] **5.3 HTTP/module:** 根包注册 32 条路由；解析 core/authn/identity 根包契约，稳定导出 `GroupProvisioner`，保留 declaration-only。
- [x] **5.4 Negatives/tests:** tenant A 用 B 的 department/position/group/invitation/member ID 做任意读写必须不可见且 B 不变；平台 tenant CRUD 只能由 platform principal 调用；邀请 token 不能接受到错误 tenant。
- [x] **5.5 Gate:** 登记 organization，运行 generate、定向测试和 `make ci`。

#### Task 6: 迁移 `audit`

**Source manifest**

- Production: `anchor.go`, `audit.go`, `config.go`, `governance.go`, `i18n.go`, `module.go`, `rest.go`, `i18n/locales/{en-US,ms-MY,zh-CN}.json`。
- Tests: `anchor_test.go`, `audit_test.go`, `governance_test.go`, `i18n_test.go`, `module_test.go`, `rest_test.go`。

- [x] **6.1 Model/behavior:** 在 `internal/iam/audit` 根包按源 basename 移植 chain/anchoring/retention，`model/query/dbgen` 为 3 张表生成查询；与 `internal/infra/audit` 明确分工。
- [x] **6.2 Repositories:** 根包 repository 实现 append-only/head lock；anchor/sign/export 由 service 在同一 gorm 事务内协调 append。
- [x] **6.3 HTTP/module:** 根包注册 8 条路由，提供唯一生产 `audit.Appender` 并保留 declaration-only；依赖失败时启动失败。
- [x] **6.4 Negatives/tests:** tenant A 的 list/get/export/integrity/anchor/retention 不得观察或改变 B；并发 append 不分叉，篡改任一历史事件后 integrity 必须失败，append-only trigger 三引擎均拒绝 update/delete。
- [x] **6.5 Gate:** 登记 audit，运行 generate、定向/并发测试和 `make ci`。

#### Task 7: 迁移 `governance`、`provisioning`、`federation`

**governance source manifest**

- Production: `governance.go`, `i18n.go`, `module.go`, `rest.go`, `review.go`, `review_rest.go`, `i18n/locales/{en-US,ms-MY,zh-CN}.json`。
- Tests: `governance_test.go`, `module_test.go`, `rest_test.go`, `review_rest_test.go`, `review_test.go`。Phase 1 only: `migrate.go`, `migrate_test.go`, SQL。

- [x] **7.1 governance:** 在 `internal/iam/governance` 根包按源文件移植 request/review 两套状态机和 14 条路由，`model/query/dbgen` 为 4 张表生成查询；解析 core/audit 根包契约并保留 declaration-only。
- [x] **7.2 governance negatives:** tenant A 不能查看或裁决 B 的 request/review/item；非申请人不能 withdraw，非 approver 不能 decide；并发 approve/reject/complete/cancel 只能产生一个合法终态，最后管理员撤销必须失败。
- [x] **7.3 governance gate:** 登记包、generate、定向测试、覆盖率 ≥90%，运行 `make ci` 后才进入 provisioning。

**provisioning source manifest**

- Production: `bulk.go`, `config.go`, `discovery.go`, `domain.go`, `filter.go`, `i18n.go`, `module.go`, `patch.go`, `repository.go`, `resources.go`, `rest.go`, `scim.go`, `scim_rest.go`, `service.go`, `target.go`, `target_rest.go`, `i18n/locales/{en-US,ms-MY,zh-CN}.json`。
- Tests: `bulk_test.go`, `filter_test.go`, `module_test.go`, `patch_test.go`, `repository_test.go`, `resources_test.go`, `rest_test.go`, `scim_rest_test.go`, `scim_test.go`, `target_rest_test.go`, `target_test.go`。Phase 1 only: migrate files 与 SQL。

- [x] **7.4 provisioning:** 在 `internal/iam/provisioning` 根包按源文件移植 directory/SCIM/outbound 与 30 条路由，`model/query/dbgen` 为 5 张表生成查询并复用 filter parser；解析 identity/organization/audit 根包契约并保留 declaration-only。
- [x] **7.5 provisioning negatives:** directory credential 必须先唯一解析 tenant，随后所有资源读写绑定该 tenant；A credential 对 B user/group/directory/target 返回不可见；bulk 子操作不得越过认证 tenant；ETag 并发更新只有一个成功。
- [x] **7.6 provisioning gate:** 登记包、generate、SCIM 协议/定向测试、覆盖率 ≥90%，运行 `make ci` 后才进入 federation。

**federation source manifest**

- Production: `domain.go`, `i18n.go`, `module.go`, `rest.go`, `saml.go`, `saml_rest.go`, `service.go`, `state.go`, `verifier.go`, `i18n/locales/{en-US,ms-MY,zh-CN}.json`。
- Tests: `domain_test.go`, `i18n_test.go`, `module_test.go`, `rest_test.go`, `saml_rest_test.go`, `saml_test.go`, `service_test.go`, `state_test.go`, `verifier_test.go`。Phase 1 only: migrate files 与 SQL。

- [x] **7.7 federation:** 在 `internal/iam/federation` 根包按源文件移植 OIDC/SAML 的 IdP/state/verifier/link 与 SP/SSO/SLO/key 共 17 条路由，`model/query/dbgen` 为 4 张表生成查询；解析 identity/authn/audit 根包契约并保留 declaration-only。
- [x] **7.8 federation negatives:** tenant A 不能 CRUD B provider/SP；state/nonce/provider callback 必须绑定原 tenant，SAML `tenant_id` 路径不能选择 B 的 SP/key；state、assertion 与 logout request 重放必须拒绝。
- [x] **7.9 federation gate:** 登记包、generate、OIDC/SAML 定向测试、覆盖率 ≥90%，运行 `make ci`。

#### Task 8: Composition root 与跨模块收口

- [x] **8.1 声明模式:** 在 `internal/app` 先装配 authz declaration registry；只对 iam/organization/audit/governance/provisioning/federation 调用 declaration-only 构造器。authn/identity/oauth 不伪造该 API。
- [x] **8.2 运行模式:** 用 `pkg/module.Services` 按 §2.2 注册/解析 provider-owned/consumer-owned 窄接口；启动测试逐一删除提供方并断言错误明确包含缺失契约和消费模块。
- [x] **8.3 注册顺序:** audit → iam → authn → identity → organization → oauth → provisioning → governance → federation，并用 `ModuleDependencies` 固化生命周期拓扑；`Registry.Wire` 仍按“全部 provider → seal → 全部 injector”解析接口，不通过包级全局变量或 concrete type 绕开 registry。
- [x] **8.4 HTTP 契约集合:** 启动真实 Huma API，提取 OpenAPI operations，与各模块批准后的目标 `OperationID|METHOD|PATH` 做集合相等；再用 source→target 映射证明源 208 项功能逐条有且仅有一个目标 operation。额外、缺失、重复或未映射任一即失败。
- [x] **8.5 跨模块 tenant negatives:** 用 tenant A/B 固定夹具覆盖邀请建 principal、group provision、SCIM、federation callback、governance revoke、IAM authorization 和 audit trace；断言请求、数据库副作用和审计事件都不跨 tenant。
- [x] **8.6 架构边界:** `tests/architecture` 用 `go list -json` + Go AST 断言 9 个模块均位于 `internal/iam/`、各自拥有 model/query/SQL、依赖进入 `ModuleDependencies`、跨模块不导入 model/query/concrete service、无中央 schema/port 包；加入违规 fixture 证明门禁有效。
- [x] **8.7 Gate:** `make generate` 零 diff，9 个手写模块覆盖率均 ≥90%，`make ci` 全绿后进入 Phase 9。

#### 冻结源 HTTP operation 基线（source tree `201052baddd745d1c2727cf29bbc3c68253a1af0`）

提取规则覆盖所有源生产 Go 文件中的 operation、`itemOperation`、governance 循环注册，并展开 SAML path 常量。旧规则只扫描 `*rest.go`/`registration.go`，曾漏掉 `identity/service_account.go` 的 8 条 operation；修正后总计 208 条且 OperationID 208/208 唯一。该集合只证明源能力范围；目标不得直接与其做路径集合相等，而要通过逐模块 source→target 映射验证完整性，再与批准后的目标 OpenAPI 契约做集合相等。

**iam（51）**

    iam-add-role-directory-binding|PUT|/iam/roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}
    iam-add-role-member|PUT|/iam/roles/{role_id}/members/{subject}
    iam-authorization-constraint|POST|/iam/authorization/constraints
    iam-check-authorization|POST|/iam/authorization/check
    iam-clear-role-permission-condition|DELETE|/iam/roles/{role_id}/permissions/{permission_code}/condition
    iam-create-entity|POST|/iam/entities
    iam-create-menu|POST|/iam/menus
    iam-create-role|POST|/iam/roles
    iam-create-tenant-member|POST|/iam/members
    iam-delete-entity|DELETE|/iam/entities/{entity_id}
    iam-delete-entity-role-binding|DELETE|/iam/entities/{entity_id}/role-bindings/{role_id}/{principal_id}
    iam-delete-menu|DELETE|/iam/menus/{menu_id}
    iam-delete-platform-administrator|DELETE|/iam/platform-administrators/{principal_id}
    iam-delete-relationship|DELETE|/iam/relationships
    iam-delete-role|DELETE|/iam/roles/{role_id}
    iam-effective-menus|GET|/iam/me/menus
    iam-explain-authorization|POST|/iam/authorization/explain
    iam-get-entity|GET|/iam/entities/{entity_id}
    iam-get-menu|GET|/iam/menus/{menu_id}
    iam-get-role|GET|/iam/roles/{role_id}
    iam-get-role-data-scope|GET|/iam/roles/{role_id}/data-scope
    iam-get-tenant-member|GET|/iam/members/{subject}
    iam-grant-role-permission|PUT|/iam/roles/{role_id}/permissions/{permission_code}
    iam-list-entities|GET|/iam/entities
    iam-list-entity-role-bindings|GET|/iam/entities/{entity_id}/role-bindings
    iam-list-menus|GET|/iam/menus
    iam-list-platform-administrators|GET|/iam/platform-administrators
    iam-list-relationships|GET|/iam/relationships
    iam-list-role-directory-bindings|GET|/iam/roles/{role_id}/directory-bindings
    iam-list-role-members|GET|/iam/roles/{role_id}/members
    iam-list-role-permission-grants|GET|/iam/roles/{role_id}/permission-grants
    iam-list-role-permissions|GET|/iam/roles/{role_id}/permissions
    iam-list-roles|GET|/iam/roles
    iam-list-tenant-member-roles|GET|/iam/members/{subject}/roles
    iam-list-tenant-members|GET|/iam/members
    iam-menu-catalog|GET|/iam/menu-catalog
    iam-permission-catalog|GET|/iam/permission-catalog
    iam-platform-permission-catalog|GET|/iam/platform-permission-catalog
    iam-put-entity-role-binding|PUT|/iam/entities/{entity_id}/role-bindings/{role_id}/{principal_id}
    iam-put-platform-administrator|PUT|/iam/platform-administrators/{principal_id}
    iam-put-relationship|POST|/iam/relationships
    iam-remove-role-directory-binding|DELETE|/iam/roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}
    iam-remove-role-member|DELETE|/iam/roles/{role_id}/members/{subject}
    iam-revoke-role-permission|DELETE|/iam/roles/{role_id}/permissions/{permission_code}
    iam-scope-model|GET|/iam/scope-model
    iam-set-role-data-scope|PUT|/iam/roles/{role_id}/data-scope
    iam-set-role-permission-condition|PUT|/iam/roles/{role_id}/permissions/{permission_code}/condition
    iam-update-entity|PATCH|/iam/entities/{entity_id}
    iam-update-menu|PATCH|/iam/menus/{menu_id}
    iam-update-role|PATCH|/iam/roles/{role_id}
    iam-update-tenant-member|PATCH|/iam/members/{subject}

**authn（30）**

    authn-begin-passkey-login|POST|/authn/passkey/login/options
    authn-begin-passkey-registration|POST|/authn/passkeys/registration/options
    authn-begin-step-up|POST|/authn/step-up/options
    authn-begin-totp-enrollment|POST|/authn/mfa/totp/enroll
    authn-capabilities|GET|/authn/capabilities
    authn-captcha|GET|/authn/captcha
    authn-change-password|POST|/authn/password/change
    authn-complete-email-verification|POST|/authn/email/verification/complete
    authn-complete-password-recovery|POST|/authn/password/recovery/complete
    authn-confirm-totp-enrollment|POST|/authn/mfa/totp/confirm
    authn-delete-passkey|DELETE|/authn/passkeys/{id}
    authn-disable-totp|DELETE|/authn/mfa/totp
    authn-finish-passkey-login|POST|/authn/passkey/login/verify
    authn-finish-passkey-registration|POST|/authn/passkeys/registration/verify
    authn-finish-step-up|POST|/authn/step-up/verify
    authn-list-passkeys|GET|/authn/passkeys
    authn-list-sessions|GET|/authn/sessions
    authn-login|POST|/authn/login
    authn-logout|POST|/authn/logout
    authn-logout-all|POST|/authn/logout-all
    authn-me|GET|/authn/me
    authn-mfa-status|GET|/authn/mfa
    authn-regenerate-recovery-codes|POST|/authn/mfa/recovery-codes/regenerate
    authn-register|POST|/authn/register
    authn-rename-passkey|PATCH|/authn/passkeys/{id}
    authn-revoke-session|DELETE|/authn/sessions/{id}
    authn-session|GET|/authn/session
    authn-start-email-verification|POST|/authn/email/verification/start
    authn-start-password-recovery|POST|/authn/password/recovery/start
    authn-verify-login-mfa|POST|/authn/login/mfa

**authn 已批准目标 HTTP 契约（source OperationID → target 三元组）**

    authn-begin-passkey-login => authn-create-passkey-session-challenge|POST|/authn/passkey-session-challenges
    authn-begin-passkey-registration => authn-create-passkey-registration-challenge|POST|/authn/passkey-registration-challenges
    authn-begin-step-up => authn-create-step-up-challenge|POST|/authn/step-up-challenges
    authn-begin-totp-enrollment => authn-create-totp-enrollment|POST|/authn/mfa/totp-enrollments
    authn-capabilities => authn-capabilities|GET|/authn/capabilities
    authn-captcha => authn-create-captcha|POST|/authn/captchas
    authn-change-password => authn-update-password|PATCH|/authn/password
    authn-complete-email-verification => authn-create-email-verification|POST|/authn/email-verifications
    authn-complete-password-recovery => authn-create-password-recovery|POST|/authn/password-recoveries
    authn-confirm-totp-enrollment => authn-create-totp-factor|POST|/authn/mfa/totp
    authn-delete-passkey => authn-delete-passkey|DELETE|/authn/passkeys/{id}
    authn-disable-totp => authn-delete-totp-factor|DELETE|/authn/mfa/totp
    authn-finish-passkey-login => authn-create-passkey-session-verification|POST|/authn/passkey-session-verifications
    authn-finish-passkey-registration => authn-create-passkey|POST|/authn/passkeys
    authn-finish-step-up => authn-create-step-up-verification|POST|/authn/step-up-verifications
    authn-list-passkeys => authn-list-passkeys|GET|/authn/passkeys
    authn-list-sessions => authn-list-sessions|GET|/authn/sessions
    authn-login => authn-create-session|POST|/authn/sessions
    authn-logout => authn-delete-current-session|DELETE|/authn/sessions/current
    authn-logout-all => authn-delete-sessions|DELETE|/authn/sessions
    authn-me => authn-me|GET|/authn/me
    authn-mfa-status => authn-get-mfa|GET|/authn/mfa
    authn-regenerate-recovery-codes => authn-replace-recovery-codes|PUT|/authn/mfa/recovery-codes
    authn-register => authn-create-registration|POST|/authn/registrations
    authn-rename-passkey => authn-update-passkey|PATCH|/authn/passkeys/{id}
    authn-revoke-session => authn-delete-session|DELETE|/authn/sessions/{id}
    authn-session => authn-get-current-session|GET|/authn/sessions/current
    authn-start-email-verification => authn-create-email-verification-request|POST|/authn/email-verification-requests
    authn-start-password-recovery => authn-create-password-recovery-request|POST|/authn/password-recovery-requests
    authn-verify-login-mfa => authn-create-session-verification|POST|/authn/session-verifications

**identity（14）**

    identity-create-principal|POST|/iam/principals
    identity-disable-principal|POST|/iam/principals/{id}/disable
    identity-get-principal|GET|/iam/principals/{id}
    identity-list-principals|GET|/iam/principals
    identity-restore-principal|POST|/iam/principals/{id}/restore
    identity-update-principal|PATCH|/iam/principals/{id}
    identity-list-service-accounts|GET|/iam/service-accounts
    identity-create-service-account|POST|/iam/service-accounts
    identity-get-service-account|GET|/iam/service-accounts/{id}
    identity-replace-service-account|PUT|/iam/service-accounts/{id}
    identity-delete-service-account|DELETE|/iam/service-accounts/{id}
    identity-list-service-account-credentials|GET|/iam/service-accounts/{id}/credentials
    identity-create-service-account-credential|POST|/iam/service-accounts/{id}/credentials
    identity-revoke-service-account-credential|DELETE|/iam/service-accounts/{id}/credentials/{credential_id}

**identity 已批准目标 HTTP 契约（source OperationID → target 三元组）**

    identity-create-principal => identity-create-principal|POST|/iam/principals
    identity-disable-principal => identity-create-principal-suspension|PUT|/iam/principals/{id}/suspension
    identity-get-principal => identity-get-principal|GET|/iam/principals/{id}
    identity-list-principals => identity-list-principals|GET|/iam/principals
    identity-restore-principal => identity-delete-principal-suspension|DELETE|/iam/principals/{id}/suspension
    identity-update-principal => identity-update-principal|PATCH|/iam/principals/{id}
    identity-list-service-accounts => identity-list-service-accounts|GET|/iam/service-accounts
    identity-create-service-account => identity-create-service-account|POST|/iam/service-accounts
    identity-get-service-account => identity-get-service-account|GET|/iam/service-accounts/{id}
    identity-replace-service-account => identity-replace-service-account|PUT|/iam/service-accounts/{id}
    identity-delete-service-account => identity-delete-service-account|DELETE|/iam/service-accounts/{id}
    identity-list-service-account-credentials => identity-list-service-account-credentials|GET|/iam/service-accounts/{id}/credentials
    identity-create-service-account-credential => identity-create-service-account-credential|POST|/iam/service-accounts/{id}/credentials
    identity-revoke-service-account-credential => identity-delete-service-account-credential|DELETE|/iam/service-accounts/{id}/credentials/{credential_id}

**oauth（14）**

    oauth-authorize|GET|/oauth/authorize
    oauth-create-client|POST|/iam/oauth-clients
    oauth-delete-client|DELETE|/iam/oauth-clients/{id}
    oauth-discovery|GET|/.well-known/openid-configuration
    oauth-introspect|POST|/oauth/introspect
    oauth-jwks|GET|/.well-known/jwks.json
    oauth-list-clients|GET|/iam/oauth-clients
    oauth-list-consents|GET|/iam/me/oauth-consents
    oauth-revoke|POST|/oauth/revoke
    oauth-revoke-consent|DELETE|/iam/me/oauth-consents/{client_id}
    oauth-rotate-client-secret|POST|/iam/oauth-clients/{id}/rotate-secret
    oauth-token|POST|/oauth/token
    oauth-update-client|PUT|/iam/oauth-clients/{id}
    oauth-userinfo|GET|/oauth/userinfo

**organization（32）**

    organization-accept-invitation|POST|/iam/invitations/accept
    organization-create-department|POST|/iam/departments
    organization-create-group|POST|/iam/groups
    organization-create-invitation|POST|/iam/invitations
    organization-create-position|POST|/iam/positions
    organization-create-tenant|POST|/iam/tenants
    organization-delete-department|DELETE|/iam/departments/{id}
    organization-delete-group|DELETE|/iam/groups/{id}
    organization-delete-group-member|DELETE|/iam/groups/{id}/members/{principal_id}
    organization-delete-position|DELETE|/iam/positions/{id}
    organization-delete-position-member|DELETE|/iam/positions/{id}/members/{principal_id}
    organization-delete-tenant|DELETE|/iam/tenants/{tenant_id}
    organization-get-department|GET|/iam/departments/{id}
    organization-get-group|GET|/iam/groups/{id}
    organization-get-position|GET|/iam/positions/{id}
    organization-get-tenant|GET|/iam/tenants/{tenant_id}
    organization-list-departments|GET|/iam/departments
    organization-list-group-members|GET|/iam/groups/{id}/members
    organization-list-groups|GET|/iam/groups
    organization-list-invitations|GET|/iam/invitations
    organization-list-position-members|GET|/iam/positions/{id}/members
    organization-list-positions|GET|/iam/positions
    organization-list-tenants|GET|/iam/tenants
    organization-my-tenants|GET|/iam/me/tenants
    organization-put-group-member|PUT|/iam/groups/{id}/members/{principal_id}
    organization-put-position-member|PUT|/iam/positions/{id}/members/{principal_id}
    organization-resend-invitation|POST|/iam/invitations/{id}/resend
    organization-revoke-invitation|DELETE|/iam/invitations/{id}
    organization-update-department|PATCH|/iam/departments/{id}
    organization-update-group|PATCH|/iam/groups/{id}
    organization-update-position|PATCH|/iam/positions/{id}
    organization-update-tenant|PATCH|/iam/tenants/{tenant_id}

**audit（8）**

    audit-anchor-current|POST|/iam/audit-anchor
    audit-export-events|GET|/iam/audit-events/export
    audit-get-event|GET|/iam/audit-events/{id}
    audit-get-governance|GET|/iam/audit/governance
    audit-list-events|GET|/iam/audit-events
    audit-set-retention|PUT|/iam/audit/retention
    audit-sign-root|POST|/iam/audit/roots/sign
    audit-verify-integrity|GET|/iam/audit-integrity

**governance（14）**

    governance-approve-access-request|POST|/iam/access-requests/{id}/approve
    governance-cancel-access-review|POST|/iam/access-reviews/{id}/cancel
    governance-complete-access-review|POST|/iam/access-reviews/{id}/complete
    governance-create-access-request|POST|/iam/access-requests
    governance-create-access-review|POST|/iam/access-reviews
    governance-decide-access-review-item|POST|/iam/access-reviews/{id}/items/{item_id}/decide
    governance-get-access-review|GET|/iam/access-reviews/{id}
    governance-list-access-requests|GET|/iam/access-requests
    governance-list-access-reviews|GET|/iam/access-reviews
    governance-list-my-access-requests|GET|/iam/my/access-requests
    governance-list-requestable-roles|GET|/iam/requestable-roles
    governance-reject-access-request|POST|/iam/access-requests/{id}/reject
    governance-revoke-access-grant|POST|/iam/access-requests/{id}/revoke
    governance-withdraw-access-request|POST|/iam/access-requests/{id}/withdraw

**provisioning / SCIM（30）**

    provisioning-create-scim-credential|POST|/iam/scim/directories/{directory_id}/credentials
    provisioning-create-scim-directory|POST|/iam/scim/directories
    provisioning-create-scim-target|POST|/iam/scim/targets
    provisioning-delete-scim-target|DELETE|/iam/scim/targets/{target_id}
    provisioning-deprovision-scim-resource|POST|/iam/scim/targets/{target_id}/deprovision
    provisioning-list-scim-credentials|GET|/iam/scim/directories/{directory_id}/credentials
    provisioning-list-scim-directories|GET|/iam/scim/directories
    provisioning-list-scim-targets|GET|/iam/scim/targets
    provisioning-push-scim-resource|POST|/iam/scim/targets/{target_id}/push
    provisioning-replace-scim-directory|PUT|/iam/scim/directories/{directory_id}
    provisioning-replace-scim-target|PUT|/iam/scim/targets/{target_id}
    provisioning-revoke-scim-credential|DELETE|/iam/scim/directories/{directory_id}/credentials/{credential_id}
    scim-bulk|POST|/scim/v2/Bulk
    scim-create-group|POST|/scim/v2/Groups
    scim-create-user|POST|/scim/v2/Users
    scim-delete-group|DELETE|/scim/v2/Groups/{id}
    scim-delete-user|DELETE|/scim/v2/Users/{id}
    scim-get-group|GET|/scim/v2/Groups/{id}
    scim-get-resource-type|GET|/scim/v2/ResourceTypes/{id}
    scim-get-schema|GET|/scim/v2/Schemas/{id}
    scim-get-user|GET|/scim/v2/Users/{id}
    scim-list-groups|GET|/scim/v2/Groups
    scim-list-resource-types|GET|/scim/v2/ResourceTypes
    scim-list-schemas|GET|/scim/v2/Schemas
    scim-list-users|GET|/scim/v2/Users
    scim-patch-group|PATCH|/scim/v2/Groups/{id}
    scim-patch-user|PATCH|/scim/v2/Users/{id}
    scim-replace-group|PUT|/scim/v2/Groups/{id}
    scim-replace-user|PUT|/scim/v2/Users/{id}
    scim-service-provider-config|GET|/scim/v2/ServiceProviderConfig

**federation（17）**

    federation-callback-login|GET|/federation/{provider_id}/callback
    federation-callback-saml-login|POST|/federation/{provider_id}/callback
    federation-create-identity-provider|POST|/iam/identity-providers
    federation-create-saml-service-provider|POST|/iam/saml/service-providers
    federation-delete-identity-provider|DELETE|/iam/identity-providers/{provider_id}
    federation-delete-saml-service-provider|DELETE|/iam/saml/service-providers/{sp_id}
    federation-list-identity-providers|GET|/iam/identity-providers
    federation-list-saml-service-providers|GET|/iam/saml/service-providers
    federation-rotate-saml-signing-key|POST|/iam/saml/signing-key/rotate
    federation-saml-metadata|GET|/federation/saml/{tenant_id}/metadata
    federation-saml-slo|GET|/federation/saml/{tenant_id}/slo
    federation-saml-slo-post|POST|/federation/saml/{tenant_id}/slo
    federation-saml-sso|GET|/federation/saml/{tenant_id}/sso
    federation-saml-sso-post|POST|/federation/saml/{tenant_id}/sso
    federation-start-login|GET|/federation/{provider_id}/start
    federation-update-identity-provider|PUT|/iam/identity-providers/{provider_id}
    federation-update-saml-service-provider|PUT|/iam/saml/service-providers/{sp_id}

### Phase 9 — 集成与验收

- [x] **9.1 三引擎生命周期:** PostgreSQL/MySQL/SQLite 分别在全新空库和 `sl_` 前缀下执行建租户 → principal → 部门/组/岗位 → 角色/权限 → 成员绑定 → authorization check/explain → OAuth → SCIM → federation → governance → audit；每步验证数据库副作用和审计事件。
- [x] **9.2 迁移状态:** 三引擎分别执行 up → status → reverse down → up；`/migration-status` 必须显示现有 4 条加 IAM 域 9 条共 **13 条** flow clean，9 个模块表集合互斥且并集恰为 63。
- [x] **9.3 架构边界:** `tests/architecture` 对实际 package graph 通过；确认 `internal/iam/` 只作命名空间、9 个自治模块各自拥有业务/model/query/SQL、依赖均在 `ModuleDependencies` 声明、跨模块未导入 model/query/concrete service。
- [x] **9.4 HTTP 契约集合:** 从运行中 OpenAPI 提取三元组，断言与各模块批准后的目标集合完全相等、OperationID 全局唯一；断言 source→target 映射覆盖源 **208/208** 且一一对应；另断言所有非公开 operation 都带正确 tenant/platform/entity guard，不能只检查 `/docs` 页面可见。
- [x] **9.5 隔离与并发:** 执行 Task 2–8 的 tenant A/B 负例总矩阵；对授权判定、token 单次消费、最后管理员、SCIM ETag、governance 终态和 audit append 做并发门禁，使用 `-race` 跑不依赖真数据库的部分。
- [x] **9.6 性能基线:** 授权判定、会话查找、SCIM bulk、审计 append 四条热路径保存 PostgreSQL/MySQL `EXPLAIN` 与 benchmark；不得出现无 tenant 前导条件的全表扫描，阈值按同机重复样本冻结。
- [x] **9.7 最终门禁:** `make generate` 零 diff、`make ci` 全绿、`make test-mysql8` 真 MySQL 通过；`make dev` 后 `/healthz` 绿。更新 README Testing、`.claude/task-state.md` 与 `decisions.md`，逐项勾完本计划才可宣布迁移完成。

### Phase 10 — 产品完整性复审后的未完成工作

#### 已验证现状

| 能力 | 结论 | 证据与边界 |
|---|---|---|
| HTTP | **迁移契约完整** | 9 模块共 208 条 operation，实际 OpenAPI、source→target mapping、guard 分类均有集合门禁；这证明路由不漏项，不证明每个 guard 的产品语义正确 |
| gRPC | **IAM 未实现；迁移不缺失** | IAM 9 模块无 proto、`GRPCRegistrar` 或 gRPC service；固定源树同样没有 IAM gRPC。应用自身仍有 health 等 gRPC 服务 |
| 接口权限 | **执行链完整** | public/authenticated/member/tenant/entity/platform middleware 均实际执行；principal update/suspend/resume 曾把 `platform_administer` 注册为 tenant guard，10.3 已改为 `user_update`，并让这类误注册在启动期 panic |
| 数据权限 | **计算完成，执行未闭环** | `Authorizer.Constraint()` 可产出 AllowAll/resource/department/ancestor/deny；生产业务 repository 无消费者，IAM 生产路由也没有 `RegisterEntity` 调用 |
| 菜单权限 | **判定完整，初始化缺失** | `EffectiveMenus()` 读取持久化菜单并批量鉴权、未知 permission fail closed、保留有可见子项的容器；但新租户未写 `DefaultMenus()`，结果为空 |
| 字段权限 | **未实现** | 没有 schema、catalog、管理 API、repository projection、masking 或 response serialization filter |
| IAM 源迁移 | **已完成** | 9 模块、63 表、208 HTTP、三方言 migration/隔离/并发/性能门禁均通过 |
| IAM 产品闭环 | **未完成** | 缺平台/租户安全自举，另有 consent、archive、principal lifecycle、secret rotation 等产品缺口 |

#### Critical / High

- [x] **10.1 平台管理员安全自举（Critical）：**实现 §2.5.1。当前生产 CLI 只有 `serve`/`migrate`；`EnsureBootstrapPrincipal` 无生产调用者且不写 `iam_platform_administrators`。平台管理员管理 API 自身受 `platform_administer` 保护，空库无法建立第一个管理员。
- [x] **10.2 新租户 owner 自举（Critical）：**实现 §2.5.2。当前 `TenantService.Create` 只写 tenant、policy revision、audit；所有 tenant route 先要求 active membership，故新租户无法经现有 HTTP 建立第一个成员/管理员。
- [x] **10.3 修正 principal 管理权限语义（High，见 D17）：**`identity-update-principal`、suspend、resume 三条使用 tenant 注册器却挂 `platform_administer`。`CheckBulk` 对 platform scope 的 action 只认平台管理员身份，`tenant_administer` 通配不覆盖它，所以租户管理员在自己租户里改人、停用、恢复一律 403。能调通的只剩一种账号：**既是平台管理员、又是该租户活跃成员**——`CheckBulk` 先过 `memberActive` 闸，再走 `allowed = platformAdministrator` 那一支，两个条件同时满足才放行。bootstrap 出来的管理员恰好是这一种，所以浏览器审计一直是绿的，这个口子才一直没被发现（@ClaudeWEB 提供反例）。按 Keycloak realm 模型，这本就是 realm 管理员的租户内能力，改用权限表里现成的 `user/update`（scope=tenant），与同文件 list/create/get 的 `user/view`、`user/create` 一致；路由与请求体不变，前端零改动。同时给 `authz.Register`/`RegisterEntity`/`RegisterEntityAdapter` 补上 `RegisterPlatform` 早有的反向检查：platform scope 的 action 注册到 tenant registrar 直接 panic，这类错误从此在启动期暴露。
- [ ] **10.4 数据权限端到端执行（High，方案已定见 D17）：**以 `internal/todo` 为参照实现（用户 2026-09-05 确认），为其 repository/list/get/update/delete 接入 `DataConstraint`，并对 entity-scoped route 使用 `RegisterEntity`；过滤下沉到查询层，不放在 controller；覆盖 tenant A/B、owner/department/ancestor/explicit deny、分页 count 与导出路径。只暴露 constraint/explain API 不算完成。
- [ ] **10.5 字段权限设计与实现（High，方案已定见 D17）：**对标 Keycloak 声明式 User Profile 的每字段 `view`/`edit` 角色授权与 SCIM 2.0 的 `mutability`/`returned`；据此确定主体/角色继承、read/write 分离、deny precedence、mask 规则、OpenAPI 表达和审计策略，再设计 schema/catalog/API/cache revision，并在读取 projection/序列化和写入 validation 两端强制执行。不得只做前端隐藏。

#### Medium

- [x] **10.6 OAuth 交互式 consent：**当前 `prompt=consent` 产生 `ErrConsentRequired` 后映射为通用 `invalid_request`，且默认 prompt 会直接 upsert consent。补 consent challenge/approve/deny/revoke API 或页面、短期一次性状态、防 CSRF 与审计。
- [ ] **10.7 Audit archive 生命周期：**当前 retention 只保存 policy 并统计 `archive_ready_events`；补实际 archive/export worker、对象校验、重试、删除/保留边界和可观测状态。
- [ ] **10.8 Principal 数据生命周期（方案已定见 D17）：**普通 principal 有 create/update/suspend/resume，但没有删除、匿名化或法定保留流程。按 SCIM + GDPR 通行组合：停用（`active=false`）→ 匿名化 PII → 保留主体行作墓碑，审计永不硬删，外键与审计链因此始终成立。
- [ ] **10.9 Secret/key rotation（进行中，五步已完成四步）：**① 密钥环原语 `pkg/extension/keyringx`：信封记录是哪把密钥封的，一把在封、一圈都能开。② authn / federation / provisioning 三个模块接入密钥环，新增 `previous_*_keys` 配置。③ `iam rewrap-secrets` 把仍挂在旧密钥上的行重新封一遍，并报告「还剩多少行挂在旧密钥上」，让「可以把旧密钥拿掉了」有依据而不是靠猜；派生摘要（recovery code、验证令牌）无法重封，按各自是否会过期分别报 `expires=never` 或一个日期，不会给出读起来像许可的日期。④ authn 签名密钥环：`signingKeyring` 一把签、一圈验，`/.well-known/jwks.json` 同时发布 active 与 previous，验签按 token header 里的 `kid` 选key（不是逐把试——逐把试会让「把旧 key 从配置里拿掉」这个动作失效）；新增 `authn.previous_signing_keys` / `_files`；轮换步骤写进 `README.md` 的「Rotating keys」。旧 key 必须在配置里留满一个 `access_token_ttl` 才能退场，否则所有在途 token 一次性失效。剩余一步：⑤ federation replay registry 目前是进程内 map，多副本部署下同一个 nonce 每个副本各花一次，重放保护实际上是关的，需换成跨副本共享状态。
- [x] **10.10 IAM gRPC 产品决策：不做（见 D17）。**主流 IAM 产品面是 REST + OIDC/OAuth2/SCIM，本项目已具备。无明确消费方、surface 未冻结就新增 proto/service 等于维护两份 API。本项关闭；若将来确有 HTTP/gRPC 双协议需求，须先冻结 API surface、auth metadata、错误码、版本兼容与 208 HTTP 能力的映射范围，再重开。
- [ ] **10.11 最终产品门禁：**完成 10.1–10.9（10.10 已关闭）后重跑 SQLite/MySQL/PostgreSQL 初始化与权限矩阵、OpenAPI、race、`make generate`、`make ci`、真实 MySQL 及 live readiness。

#### 已完成的工程治理

- [x] **测试文件命名：**全仓生产目录只允许 `xx_test.go` 或 `xx_subxx_test.go`，其中 `xx.go` 必须真实存在于同目录；已重命名历史违规文件，并由 `tests/architecture` 自动门禁。纯黑盒测试目录因没有 production Go file 而豁免。
- [x] **API 文档发布：**`7link-api openapi` 不启动服务就导出本部署的 OpenAPI 文档；`make openapi` 生成 `docs/openapi.json`，`make openapi-check` 在文档与代码不一致时让 `make ci` 失败。文档以前只有跑起来的服务在 `/openapi.json` 上供，而需要它的东西——客户端类型生成、兼容性检查、一次 API 变更的评审——全都在服务启动之前运行。导出按配置组合真实应用（不是「全模块打开」的超集），`info.version` 取配置不取构建号，两次导出字节一致，因此可以作为 diff 基准。
- [x] **模块自治激活：**删除 `internal/app` 对整个 IAM graph 和 federation 的手工条件 append；9 个 runtime module 无条件登记并各自实现 `module.Enabler`，禁用依赖与启用消费者的非法组合由 `Registry.Activate()` fail fast。

---

## 四、未决事项

1. ~~**Phase 10 产品决策**~~ —— **已于 2026-09-05 按 D17 定案并公示**：principal 管理的 tenant/platform 所有权、数据权限落地载体、字段权限模型、principal 删除/匿名化、IAM 是否提供 gRPC，五条均按主流 IAM 通行做法决定，不再逐条等待确认。仍不得以“源项目也没有”为由宣称完整；实施时逐条对照规范与参考实现核对。
2. **多写者拓扑** —— Task 0.0 只修迁移双取句柄。是否引入显式 routing mode / 逻辑 writer group 来防止误配仍需单独决策。审计 F3 的“`Read()` 零消费者”已不再成立：core membership checker 已使用读路由，datasource 注释也已与当前行为一致；是否把更多业务查询接到副本仍属于后续架构范围。

**已解除/已确认:** 动态菜单多语言不属于“迁移现有功能”，保持 63 表且不新增 `iam_menu_translations`（D7）；Docker 不再绑定单机：Mac 使用本机 daemon，Windows 使用 `10.0.0.100` 的 Debian Docker 26.1.5，具体 transport 由预检生成；R16 按上表四类规则执行；R9'' 的 `type` 不可变不是新增业务限制，源库 `iam/entity.go:273-281` 已在存在关系时拒绝修改，复合外键用于关闭应用层检查的 TOCTOU 窗口。

---

## 五、自审

**1. 规格覆盖**:A1–A3、B、C1/C3–C6、D1–D4、E 逐条有 R1–R16 对应处置并由测试断言。审计 F1/F2/F4 分别并入 Task 1.1 / 0.0 / 0.4;F3 列未决。9 个模块均冻结 source manifest、model/query/dbgen、仓储、领域服务、端口、完整路由、隔离负例与覆盖率门禁。

**2. v2 缺陷闭环**:codex 的 5 Critical(C1 迁移流依赖 / C2 表数 / C3 阶段顺序 / C4 审计所有权 / C5 baseline 门禁)与 15 Major 中经实测确认的 6 条(M1/M4/M5/M7/M12 + M2 生成列),均在"版本沿革"逐条记录处置。M15 的范围争议按用户"全量全功能到位"的原意撤销 v2 措辞。

**3. 类型一致性**:13 个能力接口按当前项目模式由 provider root 或唯一 consumer 直接声明（`module.Services` 按精确 `reflect.Type` 匹配），不设中央 port 或 contract 中转包；`audit.Appender` 签名在 Task 0.4 定死；`query.Bind(db, prefix)` 与 `tableName(prefix, table)` 只在所属模块 repository/生成层使用。

**4. 已知的自我失误模式**:v2 出现三处"先定规则、再在自己的示例里违反"(RESTRICT→CASCADE、列宽 1000 vs 4096、表数 36→35)。R15 要求每条 DDL 标注源文件行号,正是为让这类语义漂移在 review 时可见——静态检查扫不出来。
