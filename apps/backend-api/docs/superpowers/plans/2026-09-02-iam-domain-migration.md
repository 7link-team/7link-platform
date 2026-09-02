# IAM 域全量迁移实施计划 v3

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 chaosplus `apps/server` 的 IAM 域(9 个 Go 模块 / 63 张表)全量迁入 7link-platform `apps/backend-api`,改造为本项目的 gorm + gorm/gen + goosex + pkg/module 规约,并修复源库既有的 SQL 缺陷。**全功能到位**:每个模块都要有生产实现与可用路由,不接受"只迁 schema、模块 Disabled"。

**Architecture:** 63 张表由**单一迁移流** `internal/iamschema` 拥有(依据见 §2.1:iam↔organization 存在双向外键环,任何按模块拆流的方案都排不出顺序);9 个 Go 模块经 `pkg/module` 能力接口消费该 schema。目标库为**全新空库,不迁移存量数据**(用户 2026-09-02 确认)。

**Tech Stack:** Go 1.26 · gorm v1.31 + gorm/gen v0.3.29 · goose v3.27 · huma v2.39 · PostgreSQL / MySQL / SQLite

**Spec:** 本文件自带规格(§1–§3)。事实来源:`/Volumes/Macintosh/Projects/chaosplus/chaosplus/apps/server/internal/`(源码)、`.temp/iam-schema-mysql.sql`(字段汇总)。

---

## 版本沿革

v1 经 codex 评审(9C/18M/5m)修订为 v2;v2 再经 codex 评审(5C/15M/3m)+ 全仓架构审计交叉验证,**逐条实测**后修订为 v3。本节记录被推翻的结论,避免读者把已作废的判断当事实。

### v1 → v2 已处置(不再赘述)
Phase 0 构建顺序、SQLite 外键未启用、版本表双前缀、`{{.Prefix}}` 不覆盖 raw SQL、范围漏 oauth/audit、`TIMESTAMPTZ(3)` 精度、MySQL DDL 隐式提交、覆盖率登记时机、`//go:generate` 缺失、baseline 冻结。以及两条**被实测推翻的 v1 缺陷**:源库三方言**都有** append-only 触发器;`menus.route` 的可空 NULL 语义**是正确的**(分组节点应可并存)。

### v2 → v3 修订(全部经实测)

| v2 内容 | 实测结论 | v3 处置 |
|---|---|---|
| **R11' 拆出独立 `tenancy` 迁移流,保留跨流外键** | ❌ **方案不成立**。跨模块外键 13 条,其中 `organization→iam` 7 条、`iam→organization` 1 条——**双向环**。且 `migration.go:120` 在 `--module X` 时会跳过其他模块,`goosex.Migrate` 的 down 路径也不反转顺序 | **废弃拆流。63 表并入单一迁移流 `internal/iamschema`**(§2.1) |
| §1.1 表数 `iam 35 + organization 13 + tenancy 1` | ❌ **算错,合计只有 62**。`iam_tenants` 由 `organization/sql/postgres/00005_tenants.sql` 创建,**iam 从来没有这张表**,不该从 iam 减 | 单一流,总数 **63**,并由测试断言集合相等(Task 1.2 Step 1) |
| Task 1.3 关系表 DDL 用 `ON DELETE CASCADE` | ❌ **擅自变更业务语义**。源库 `00015_relationships.sql:12` 与 `00016:13` 均为 `ON DELETE RESTRICT`。改 CASCADE 会把"有授权关系时拒绝删实体"变成"静默删授权" | **保持 RESTRICT**。新增 R15 强制每条 DDL 标注源文件行号 |
| Task 1.3 `description VARCHAR(1000)` | ❌ **违反本文件自己的 R7'**。`iam/service.go:105` 的上限是 `len(description) > 4096` | 按源码上限取值,并标注出处 |
| Task 0.0 `at most one writable datasource` | ❌ **会打破现有正式契约**。`pkg/extension/gorm/datasource_test.go:229` 的 `TestDatasourceRouterRoundRobinStateIsPerRouter` 显式建两个 writable 源并断言轮询。示例里的 `newDatasourceRouter(map,"")` 签名也是编的(真实为 `NewDatasourceRouter(ctx, bool, map)`) | **只修迁移双取句柄**;拓扑限制降级为可选的显式 routing mode,列入未决事项 |
| Task 1.0 `internal/testdb` | ❌ **放错 module**。`embedded-postgres`/`go-mysql-server` 只在 `tests/go.mod`,放主模块会把重测试依赖拖进运行时依赖图 | 改为 `tests/internal/testdb` |
| Phase 3–5 顺序(organization → identity → authn) | ❌ **与真实装配相反**。`organization/module.go:31` 需要 `InvitationCredentials`(即 authnWeb)与 `InvitationPrincipalCreator` | 按源库 `app/modules.go` 的真实装配顺序重排(§3) |
| Task 0.4 审计端口 | ❌ **所有权成环**。appender 要写 `iam_audit_events`(Phase 1 才建、归 iam),而 iam 又消费 `audit.Appender` | 端口放中立包 `pkg/port`,链式实现放 audit 模块阶段(§2.2) |
| Task 0.3 `PermissionChecker` 仅 `HasPermission` | ❌ **缺方法**。`authz/register.go:34` 还需要 platform 与 entity 判定 | 端口图在 Phase 0 冻结,含全部 12 个端口(§2.2) |
| Task 0.5 允许 SAML/SCIM 先 Disabled | ❌ **是范围缩减,不是依赖评审** | 撤销。全部模块生产实现到位;依赖评审只决定**引入时机**,不决定是否实现 |
| 漏迁 `core/extension/webauthnx` | ❌ **确认遗漏**,204 行生产代码,`authn/web.go:25` 直接依赖 | 补入 Phase 0(Task 0.4) |
| collation 只在 menus.route 提了一处 | ⚠️ **范围判断错误**。`utf8mb4_unicode_ci` 让 `Admin`/`admin` 在**所有**名称唯一键里相等,还可能让 CHECK 接受大小写变体 | 升为全局规则 R16 |

**统计修正**:源测试漏算 oauth(735)+ audit(1,497)= 2,232 行;迁移流最终为现有 4 + 新增 1 = **5 条**(不是 v2 说的 9 或 10)。

---

## Global Constraints

- `make ci` 必须绿:`fmt-check` → `lint` → `test-coverage` → `test-integration` → `build`;**每模块覆盖率 ≥ 90%**,新包在创建的那个任务里就登记进 `MODULE_COVERAGE_PACKAGES`,验收用 `make test-coverage`(`go test -cover` 不做门禁断言)。
- 模块经 `pkg/module` 能力接口接入。**允许**为迁移新增能力接口(那是该包的既定扩展机制);**不允许**改动生命周期的执行语义。
- DDL 的表名、索引名、约束名、触发器名走 `{{.Prefix}}`;**raw SQL 的表名同样必须前缀化**(R13)。
- `goosex.Source.TableName` 不得自行加前缀——`Source.versionTable()` 已经加了。
- 主键与外键一律 `BIGINT`(guid 生成);**协议外部标识符除外**(OIDC `client_id`、SAML `entity_id`/`key_id`、SCIM `external_id`)。
- 时间列:PG `TIMESTAMPTZ(3)` / MySQL `DATETIME(3)` / SQLite `DATETIME`,统一 UTC + 毫秒截断。
- "未设置"一律 `NULL`,**禁止哨兵 0**(生成列内部的派生哨兵除外,见 R5'')。
- 表名保留 `iam_` 前缀(最终 `sl_iam_roles`)。
- gorm 分层同 `internal/todo`:`model/` + `query/` + `dbgen/` + 包根 `//go:generate go run ./dbgen`。
- 不得为通过 lint 或覆盖率而弱化断言(CLAUDE.md §33)。

---

## 一、规格

### 1.1 迁移范围

**9 个 Go 模块,63 张表(单一 schema 流)**:

| 模块 | 生产代码 | 源测试 | 说明 |
|---|---|---|---|
| `iam` | 6,584 | 5,314 | 角色/权限/成员/菜单/实体/关系/平台管理 |
| `organization` | 4,149 | 2,625 | 租户/部门/岗位/用户组/邀请/数据范围 |
| `federation` | 3,274 | 2,972 | IdP/身份关联/SAML |
| `governance` | 1,614 | 1,154 | 访问申请/审批/复核 |
| `provisioning` | 4,076 | 2,101 | SCIM |
| `oauth` | 1,098 | 735 | OAuth 授权码/令牌/同意 |
| `audit` | 1,469 | 1,497 | 审计链/并发锁/完整性验证/导出 |
| `identity` | 1,642 | — | federation/provisioning 的硬依赖 |
| `authn` | 4,491 | — | federation/oauth/organization 的硬依赖 |
| **小计** | **28,397** | **16,398** | |

**扩展包**:`authz` 922 + `policyx` 777 + `authn`(Bearer/上下文)757 + `webauthnx` **204** + `secretx` 51 + `passwordx` 80 + `auditx` 32 = **2,823**

**总生产代码 ≈ 31,220 行**。源测试无一可复用;90% 门禁下需重写 **≥ 30,000 行**。

> ⚠️ **约 6 万行**。9 个阶段,每阶段结束仓库 `make ci` 绿。按阶段推进、按阶段验收。

### 1.2 必须修复的既有缺陷

| 类 | ID | 缺陷 | 规模 |
|---|---|---|---|
| **A** | A1 | `iam_oauth_consents` 外键指向 `iam_tenants`——**单一流后自动消解**(该表在同一流内) | 1 |
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
| R11'' | **63 表单一迁移流**(§2.1)。不拆流、不需要跨流 DAG |
| R12 | 租户隔离由**仓储 API 强制接收 tenantID**;建立"仓储方法 × tenant 参数 × 跨租户负例"矩阵;`client`/`provider`/`role` 等增加 `(tenant_id, id)` 复合外键 |
| R13 | raw SQL 表名经解析器产出,禁止字面量;配前缀化的真实业务查询测试 |
| **R15** | **每条改写过的 DDL 必须在注释里标注源文件:行号**。v2 有三处"先定规则再自己违反"(RESTRICT→CASCADE、列宽、表数),静态检查抓不到语义变更,只能靠可追溯的出处 |
| **R16** | **collation 全局策略**:机器枚举列(`status`/`relation`/`subject_type`/`effect` 等)、哈希/令牌/WebAuthn handle 与协议标识符(`route`/tenant slug/OIDC issuer/SAML entity ID/SCIM external key)用 **binary collation**;业务名称的唯一键使用 `name_key = strings.ToLower(strings.TrimSpace(value))`,不折叠重音。源库已有的 `departments/groups/scim_directories/scim_targets.name_key` 保持,为 `roles.name`、`entities.name`、`positions.code` 补齐。菜单 `label` 不参与唯一约束,不属于 collation 决策。避免 `utf8mb4_unicode_ci` 让安全标识符错配、让 CHECK 接受大小写变体,同时保证三方言的名称唯一语义一致 |

---

## 二、关键设计决策

### 2.1 单一迁移流(替代 v2 的拆流方案)

**依据(实测)**:56 条外键中 13 条跨模块,分布为

```
organization -> iam            7
iam          -> organization   1     ← 与上一行构成双向环
provisioning -> organization   2
governance   -> organization   2
federation   -> iam            1
```

`iam` 与 `organization` 在 schema 层**互相引用**。任何按模块拆分迁移流的方案都要求给这两个流排出先后,而环无法排序——codex 建议的 `MigrationDependencies` DAG 同样排不了环。v2 的 tenancy 拆分只处理了 `iam_tenants` 一张表,对这 8 条 iam↔organization 的边毫无作用。

**决策**:63 张表由 `internal/iamschema` 单一模块拥有,一条 goose 流(`goose_iamschema`)。

| ✅ 收益 | ❌ 代价 |
|---|---|
| 环不再是问题——同一流内按文件序建表即可 | `--module X down` 的粒度降到整个 IAM schema |
| 不需要改 `internal/app` 的迁移执行器 | 单流迁移文件较多(约 20 个) |
| A1(悬空 `iam_tenants` 外键)自动消解 | schema 与 Go 模块不再一一对应,需靠表归属清单维持可读性 |
| 13 条跨模块外键全部得以保留,不降级为应用层校验 | |

代价里的第一条是**诚实的**:FK 连通的 schema 本来就不能只 down 一半。

**表归属清单**:`internal/iamschema/OWNERSHIP.md` 记录 63 张表各归哪个 Go 模块读写,由 Task 1.2 的测试断言集合完全相等。

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

> **`module.Services` 按精确 `reflect.Type` 做键**(`pkg/module/services.go:45`),不会自动匹配"可赋值的实现"。因此端口必须以**接口类型**注册与解析,且提供方与消费方引用**同一个**接口声明。共享接口放中立包 `pkg/port`,避免 `iam ↔ organization` 在 Go 层再形成一次环。
>
> **声明式装配**:源库每个模块都有 `NewDeclarationOnlyModule(registrar)`,可在依赖未齐时只注册权限声明。这是分阶段落地的关键工具——v3 用它让每个阶段的 `make ci` 都能绿。

### 2.3 阶段顺序(按真实装配,非模块名)

源库 `app/modules.go` 的实际构造顺序:

```
authnWeb → administratorGuard(iam,无依赖) → identityService(需 guard)
  → identity 模块、iam 模块 → organization(需 iam checker + guard + authnWeb)
  → provisioning(需 identityService + organization groups)
  → governance(需 guard + iam 函数) → audit → federation(需 identityService + authnWeb)
```

v2 把 organization 放在 identity/authn 之前,与此相反。v3 按上述顺序编排。

---

## 三、阶段与任务

```
Phase 0  前置修复 + 扩展包 + 端口图冻结 + 依赖评审
Phase 1  单一 schema 流:63 表 × 3 方言 + 真迁移三引擎验证
Phase 2  iam 无依赖部分(AdministratorGuard / MembershipChecker / PermissionChecker)
Phase 3  authn(含 webauthnx)+ identity
Phase 4  iam 主体(角色/权限/菜单/平台)+ oauth
Phase 5  organization(需 Phase 2–4 的端口齐备)
Phase 6  audit(链式追加/完整性验证)
Phase 7  governance + provisioning + federation(互不依赖,可并行)
Phase 8  iam 实体与关系(依赖 organization 的组/岗位/部门)
Phase 9  集成与验收
```

### Phase 0

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

`authz/register.go:13` import `policyx`,故 policyx 先建;`policyx/policy.go` import bun,故拆为「condition/member_rule(纯逻辑)」+「revision store(gorm)」。

- [ ] **Step 1:** 移植零依赖的 `condition.go`/`member_rule.go`
- [ ] **Step 2:** revision store 改 gorm。**构造函数须接收 prefix**(`NewRevisionStore(prefix)`),源实现是硬编码表名;**`Lock` 是源语义的关键方法,与 `Bump`/`Current` 一并迁移**
- [ ] **Step 3:** 并发递增不丢更新的测试 → 登记覆盖率 → 提交

#### Task 0.3: authz(依赖 0.2)

- [ ] **Step 1:** 复制 `authz/{catalog,decision,gate,register}.go` + `authn/{verifier,bearer,context,model}.go`(**8 个文件,v1 漏了 verifier.go**)
- [ ] **Step 2:** Registrar 的协作者改接口,**含 tenant / platform / entity 三类判定**(`register.go:34`),放 `pkg/port`
- [ ] **Step 3:** 登记覆盖率 → 提交

#### Task 0.4: webauthnx + secretx + passwordx + 审计端口

> **webauthnx 是 v2 的遗漏**:204 行生产代码,`authn/web.go:25` 直接依赖。

- [ ] **Step 1:** 移植 `webauthnx`(204)、`secretx`(51)、`passwordx`(80)
- [ ] **Step 2:** `audit.DomainEvent` 与 `Appender` **放中立包 `pkg/port`**——v2 把它放 `internal/infra/audit` 并让测试写 `iam_audit_events`,而该表 Phase 1 才建且归 iamschema,形成所有权环。**链式实现放 Phase 6**
- [ ] **Step 3:** 给目标库 `operation_logs` 补三方言 append-only(审计 F4),并在注释写明:触发器拦不住 `TRUNCATE`、高权限账号可禁用触发器,**完整保证还需业务账号最小权限**——不在代码里假装已解决
- [ ] **Step 4:** 登记覆盖率 → 提交

#### Task 0.5: 冻结端口图 + 第三方依赖评审

- [ ] **Step 1:** 把 §2.2 的 12 个端口写成 `pkg/port` 的接口声明并加编译期断言。**Phase 0 冻结,后续阶段不得新增端口而不回头修订本计划**
- [ ] **Step 2:** 评审 10 个候选依赖(`go-webauthn`/`pquerna/otp`/SAML 四件套/`scim2/filter-parser`/两个验证码库/`minio-go`/`go-redis`),产出 `.claude/decisions.md`

> **范围澄清**(撤销 v2 的措辞):依赖评审只决定**引入时机与选型**(如两个功能重叠的验证码库至少砍一个),**不决定是否实现**。用户要求"全量全功能到位",SAML/SCIM 必须有生产实现与可用路由,不接受长期 `Enabled: false`。

- [ ] **Step 3:** `make ci` → 提交

### Phase 1 — 单一 schema 流

#### Task 1.0: `tests/internal/testdb`

> **v2 把它放主模块是错的**:`embedded-postgres`/`go-mysql-server` 只在 `tests/go.mod`。

- [ ] 提取 `OpenPostgres`/`OpenMySQL`/`OpenSQLite` 到 `tests/internal/testdb`;主模块单测保留轻量 SQLite helper。`tests/go.mod:96` 的 replace **已存在**,无需改动

#### Task 1.1: 真迁移三引擎验证套件(审计 F1)

> **顺序修正**(codex M8):v2 让测试先 import 尚不存在的包再要求提交,那个提交点编不过。v3 **先用已有 4 个模块建立框架并提交**,再随 Task 1.2 追加 iamschema。

- [ ] **Step 1:** 建参数化套件,首批覆盖 `todo`/`dlock`/`wuid`/`audit` × 三引擎(**这四个模块的迁移从未在 PG/MySQL 上跑过**),含幂等与 up/down 对称
- [ ] **Step 2:** 前缀测试——**只设 `Prefix`**,不要手工给 `TableName` 加前缀(`Source.versionTable()` 已经加了);断言 `sl_<表>` 存在、`<表>` 不存在、`sl_sl_*` 不存在
- [ ] **Step 3:** 标识符长度测试(前缀上限 21 字符 + 长约束名可能越过 PG 63 字节 / MySQL 64 字符)
- [ ] **Step 4:** 修正 `README.md` Testing 节的失实措辞(缺陷 E),说明实际覆盖范围与 go-mysql-server ≠ MySQL 8
- [ ] **Step 5:** 跑通并提交(**此提交点可编译**)

#### Task 1.2: 63 表单一流

**Files:** `internal/iamschema/sql/{postgres,mysql,sqlite}/` 约 20 个迁移文件 + `OWNERSHIP.md` + `module.go` + `migrate.go`

> **文件粒度**(codex M14):MySQL DDL 隐式提交,单文件中途失败会留下半套 schema 而 goose 版本未推进。**每个文件一张主表 + 其从属表**,文件头注明 goose 事务策略;并**创建 `docs/runbook-migration-recovery.md`**(v2 引用了它却没建)。

- [ ] **Step 1: 先冻结表归属清单并写断言**

```go
// TestSchemaOwnsExactly63Tables 断言迁移建出的表集合与 OWNERSHIP.md
// 完全相等。v2 曾把 iam 的表数从 36 误减为 35(iam_tenants 本就归
// organization),合计只剩 62 张 —— 这类错账靠人眼对不出来。
func TestSchemaOwnsExactly63Tables(t *testing.T) { /* 集合相等,双向 */ }
```

- [ ] **Step 2:** 逐表套 R1–R16。**每条改写标注源文件:行号(R15)**。范例:

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

- [ ] **Step 3:** 移植源库已有的三方言 append-only 触发器(加前缀,不重写)
- [ ] **Step 4:** `migrate.go`(`TableName: "goose_iamschema"`,不加前缀)
- [ ] **Step 5:** 接入 Task 1.1 套件,三引擎跑通
- [ ] **Step 6:** 提交

#### Task 1.3: 真实 MySQL 8 门禁 + baseline 冻结

> **门禁强化**(codex C5):v2 允许 docker 不可用时只记 Known Issue 就冻结 baseline,等于把未验证的 MySQL schema 永久钉死。

- [ ] **Step 1:** `make test-mysql8`——固定版本与 `sql_mode`,用 `trap` 保证清理、healthcheck 等就绪、随机端口、`-count=1`
- [ ] **Step 2:** **硬门禁**:真实 MySQL 8 通过 **且** R16 collation 策略、R9'' 的 `type` 不可变约定均已决策,才允许生成 `schema.lock`
- [ ] **Step 3:** 静态对账(正则,**声明为真引擎验证的补充而非替代**;先剥注释再匹配)
- [ ] **Step 4:** 冻结后已发布迁移文件不得改写,只允许追加

### Phase 2–8 — Go 代码移植

每阶段四步模板:**N.1** 行类型与生成代码 → **N.2** 仓储层(bun→gorm)→ **N.3** 领域与服务层 → **N.4** REST 与装配。

**跨阶段要点**:

- **N.1**:时间列改 `time.Time`/`*time.Time`;包根加 `//go:generate go run ./dbgen`;写**双向三方言** schema 契约测试,并**显式忽略 MySQL 的生成列**(`parent_key` 会打破单纯的列集合相等,codex M2)
- **N.2** bun→gorm 陷阱:**`Updates(struct)` 默认跳过零值**——清空字段、状态撤销、写入 0 都会静默失效,必须显式 `Select` 写集合或传 map;`RowsAffected`/upsert/锁/唯一冲突识别语义均不同,逐处确认
- **N.3** 时间语义:`> now` → `.After(now)`;`== 0` → `== nil`。**`expires_at == 0` 在不同表语义不同(有的"永不过期"、有的"已过期"),逐列查源码确认,禁止批量 sed**
- **N.4**:依赖未齐时先用 `NewDeclarationOnlyModule` 注册权限声明,保证该阶段 `make ci` 绿
- **每阶段**:创建即登记覆盖率门禁;R12 的跨租户负例矩阵随仓储方法同步补齐

| Phase | 内容 | 关键点 |
|---|---|---|
| 2 | iam 的 `AdministratorGuard` / `MembershipChecker` / `PermissionChecker` | 无模块依赖,是全图的根 |
| 3 | `authn`(4,491,含 webauthnx)+ `identity`(1,642) | 最大的一组;提供 `WebService`、`IdentityProvisioner` |
| 4 | iam 主体(角色/权限/菜单/平台)+ `oauth`(1,098) | oauth 需 Phase 3 的 `WebService` |
| 5 | `organization`(4,149) | 需 Phase 2 的 checker/guard + Phase 3 的 `InvitationCredentials` |
| 6 | `audit`(1,469) | 审计链追加、并发锁、完整性验证;`Appender` 的链式实现落在此 |
| 7 | `governance` / `provisioning` / `federation` | 互不依赖可并行;需 Phase 5 的 `GroupProvisioner` |
| 8 | iam 实体与关系(`data_scope`/`directory_binding`/`dynamic_group`) | 依赖 organization 的部门/组/岗位 |

### Phase 9 — 集成与验收

- [ ] **9.1** 跨模块集成:建租户 → 部门 → 角色 → 授权 → 绑成员 → 判定;**并在 `sl_` 前缀下跑完整链路**证明 raw SQL 也读对表(R13)
- [ ] **9.2** 性能基线:授权判定 / 会话查找 / SCIM 批量 / 审计写入四条热路径的 `EXPLAIN` 与并发基准
- [ ] **9.3** 最终验收:`make ci` 全绿;`make dev` 后 `/docs` 路由齐全、`/healthz` 绿、`/migration-status` 显示 **5 条**迁移流 clean;更新 `.claude/task-state.md` 与 `decisions.md`

---

## 四、未决事项

1. **动态菜单多语言是否纳入本次范围** —— 源库 `iam_menus` 只有单一 `label`,目标库 `pkg/i18n` 只处理编译期系统消息,两边都没有租户动态数据翻译。新增 `iam_menu_translations` 会把本计划从 63 表扩到 64 表,并同时扩大 model/repository/API/回退规则范围,属于产品功能而非迁移兼容修复,须由用户确认。本项不阻塞 Phase 0,但必须在 Phase 1 冻结表清单前决定。
2. **多写者拓扑** —— Task 0.0 只修迁移双取句柄。是否引入显式 routing mode / 逻辑 writer group 来防止误配,以及审计 F3(`Read()` 零消费者、业务读全走主库)是否借此接线,均需单独决策。至少应先修 `datasource.go:200` 的失实注释。

**已解除/已确认:** Docker Desktop 守护进程可用(`~/.docker/bin/docker`,Server 29.7.2),Task 1.3 不再受环境阻塞;R16 按上表四类规则执行;R9'' 的 `type` 不可变不是新增业务限制,源库 `iam/entity.go:273-281` 已在存在关系时拒绝修改,复合外键用于关闭应用层检查的 TOCTOU 窗口。

---

## 五、自审

**1. 规格覆盖**:A1–A3、B、C1/C3–C6、D1–D4、E 逐条有 R1–R16 对应处置并由测试断言。审计 F1/F2/F4 分别并入 Task 1.1 / 0.0 / 0.4;F3 列未决。9 个模块 × 4 类工作在 Phase 2–8 全覆盖。

**2. v2 缺陷闭环**:codex 的 5 Critical(C1 迁移流依赖 / C2 表数 / C3 阶段顺序 / C4 审计所有权 / C5 baseline 门禁)与 15 Major 中经实测确认的 6 条(M1/M4/M5/M7/M12 + M2 生成列),均在"版本沿革"逐条记录处置。M15 的范围争议按用户"全量全功能到位"的原意撤销 v2 措辞。

**3. 类型一致性**:12 个端口在 `pkg/port` 单点声明(`module.Services` 按精确 `reflect.Type` 匹配,提供方与消费方必须引用同一声明);`audit.Appender` 签名在 Task 0.4 定死;`query.Bind(db, prefix)` 与 `tableName(prefix, table)` 在 N.1 定义、N.2 引用。

**4. 已知的自我失误模式**:v2 出现三处"先定规则、再在自己的示例里违反"(RESTRICT→CASCADE、列宽 1000 vs 4096、表数 36→35)。R15 要求每条 DDL 标注源文件行号,正是为让这类语义漂移在 review 时可见——静态检查扫不出来。
