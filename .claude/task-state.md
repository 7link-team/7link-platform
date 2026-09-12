# Task State

## Task
消除手写 SQL、表名类型化、新增静态门禁、全仓文档体检。方案见 `C:\Users\Feather\.claude\plans\giggly-bubbling-clock.md`（已批准）。

## Objective
按批准的方案分阶段推进：P0 门禁 → P1 类型化原语 → P2 core 授权引擎搬迁 → P3 可合并 UNION →
P4a/b/c 拼表名清理 → P5 注入连接器 → P6 门禁收紧；PD 文档体检独立并行。
用户确认的路线：**跨模块取表名一律走窄接口/注入**，不放宽架构测试、不放宽依赖全序（D37）。

## Current Phase
IMPLEMENTATION —— 按已批准的收尾计划推进（计划文件见下）。
**阶段一（authn↔identity 解耦）已全部完成并通过 `make ci` + `make test-mysql8-run`。**

## Current Step
阶段一收口完成（S0–S8，8 个提交）。下一步按计划进入**阶段二**：跨模块表名改成带类型字段。

## Overall Progress
- [x] **P0 门禁 + 冻结基线**：`tests/architecture/query_gate_test.go`（5 条 AST 规则）+
      `testdata/query_gate_ledger.json`（347 条目 / **budget 879**）+ `testdata/query_violations/` 夹具。
      五条规则均在夹具上触发；白名单可断言；三条执行路径做过变异验证；**UPDATE 无法抬高预算**，
      升预算必须带 `QUERY_GATE_REBASELINE=<原因>` 且原因入账本。
      **变异测试在冻结前抓到并修掉两个真实漏洞**：① 裸表名规则不看 `return`，
      `func helper(){return prefix+"iam_roles"}` 只要不叫 `table` 就能绕过（补上后 +55 处既有违规，
      预算 836→879）；② 补 `return` 时误报了 S3 对象键与 URL 前缀，已收紧为"必须同时出现表名字面量
      或 `.TableName()`"，夹具里正反例各留一个钉住边界。
      另有 `gorm-expr-dynamic` 初版误判 `gorm.Expr(clock+" + ?")`（列表达式），已收窄，真实仓库归零。
      与独立普查交叉验证：`handwritten-sql` 实测 18 = 普查数 18（规则补漏后仍是 18）
- [x] **P1 类型化原语**：`gormx.TableName`（唯一构造函数 / `IsZero` 可观测）+ `gormx.TableSet`
      （`NewTableSet/Lookup/Len`）+ `gormx.DatabaseClockMillis`；13 个模块 `query/table.go` 增
      `TableNames`/`BindNames`/`Map` 且 `Bind` 由 `BindNames` 构建；13 个模块根包新增 `tables.go` 的 `TableSet`
- [x] **P1 附带**：补 `internal/iam/federation/query/table_test.go`（该模块原本没有表名测试，
      含"Bind 与 BindNames 不得漂移"断言）；补全 audit(3→4)、oauth(4→5) 两个只枚举部分表的既有测试
- [x] **文档同步（P1 直接引起的部分）**：SKILL.md §4 改为描述 `BindNames`/`TableName`/`TableSet` 新机制 +
      门禁说明；§8 补"不要靠跨模块 import query"（附注册表全序的硬理由与 D37 指路）；
      去掉会漂移的"161 个时间列"硬数字
- [x] **P2 core 授权引擎搬迁**：7 条语句 + 共享 CTE + 三方言 switch 搬入
      `internal/iam/core/query/authorization.go`，**SQL 文本与 `[]any` 顺序逐字节不变**
      （两法证明：一次性提取脚本 diff = BYTE-IDENTICAL；新增 golden 断言持续钉死）。
      `Authorizer.table` 删除，21 处改用绑定表名；他人表的前缀拼接收敛到 `query/table.go` 的
      `BindForeign` 单点并标 `TODO(P5)`；未 import 其他 IAM 模块、未自造连接器。
      `administrator.go` 的**最后管理员守卫仍是单条语句**（前后两次度量同事务同快照）。
      **验收（我自跑，非采信子代理）：`make ci` EXIT=0、`make test-mysql8-run` EXIT=0、0 FAIL、
      覆盖率 90.1%、schema lock 219 未变、`iam_performance_baseline.json` diff 为空（28 指纹零变化）。**
      预算随之下调 879→**749**（naked 715→593、concatenated 146→138；`handwritten-sql` 仍 18 ——
      印证账本 key 位置无关的设计正确），并变异验证门禁仍能抓新违规。
      **子代理报告有一处不实**：`cover_base.raw`/`cover_fed.raw` 是它自己产生的覆盖率轮廓
      （它称"P1 遗留、非我创建"，时间戳在其会话内），且未被 gitignore 覆盖 → 已删除。
- [x] **P3 可合并 UNION 改类型化**：只取 3 条"不在授权热路径、且不与调用方事务前后测量耦合"的语句：
      ① `core/repository_admin.go` 的 `ListMemberRoleIDs`：UNION → 两条 gen `Pluck` + `slices.Sort`
      + **`slices.Compact`**（SQL 全消）。`Compact` 不可省——UNION 是去重的，而返回值的**长度可观测**；
      ② `governance` 的 `roleAlreadyGranted`：`COUNT(*) OVER (UNION ALL)` → 新增窄接口
      `core.RoleGrantStore.RoleGranted(ctx, *gorm.DB, tenantID, roleID, principalID, now)`，
      core 侧两条 `Count()` **相加**（UNION ALL 是袋并集，相加与计数严格相等；短路会吞掉第二个源的故障），
      且**走调用方事务**（审批流要与随后的写入同快照）；
      ③ `core/platform.go` 的 `listPlatformAdministrators`：UNION ALL + `ORDER BY principal_id ASC,
      record_kind DESC, permission_code ASC` → 两条类型化查询 + `sort.SliceStable`
      （rank：grant=0 / full=1，即 `DESC` 下 'grant' 在前；permission_code 两侧 DDL 都是
      utf8mb4_bin / "C"，字节序 == Go 字符串序），**既有分组循环一行未改**；两条语句**包进同一个事务**
      （调用方无事务时自开），并把原来"from one database statement snapshot"的注释改写成事实描述
      （调用方事务内两臂都可见自身未提交写；MySQL 默认 RR 下两臂同一快照）。
      **未动**：`administrator.go` 的 last-admin 守卫（维持单语句）、P2 已搬入 `core/query/authorization.go`
      的 7 条。门禁账本 749→**736**（8 条 key 消失、1 条 3→2、**无新增 key**）。
- [x] **P4a 自有表字面量清理**：8 个 IAM 模块共 **69 处**自有表字面量改走同模块
      `query.BindNames(prefix)`（core 7 / authn 26 / identity 2 / oauth 5 / governance 1 /
      organization 3 / federation 10 / provisioning 15），与方案普查给出的逐模块数字完全吻合。
      形态：需要 query 对象→`query.Bind(db,prefix)`；只需要字符串→`BindNames(prefix).X.String()`；
      列表/switch/map 键一律携带 `gormx.TableName`；错误信息改 `Logical()`（不加前缀）。
      **未改**：102 处他模块表字面量（P4b/P4c 范围，core 反向 import 会在 `Registry.Activate` 全序上启动失败）；
      各模块 `migrate.go` 与 `internal/app/principallifecycle.go` 的 `goose_*`（前缀由 `goosex` 外部
      `internal/app/migration.go:243` 施加，站点处无 prefix、无 typed 表示，改不了）；
      `provisioning/metrics.go` 11 条 `iam_scim_*` Prometheus 指标名（非表，改名=可观测业务语义变化）。
      两处签名收窄为 `gormx.TableName`（`authn/rewrap.go` 的 `principalSealedStore`/`countDerived`，
      原先内部再拼一次 `repo.table(name)`，传已绑定名会**双重加前缀**）。
- [~] **P4b 跨模块单表 → 窄接口契约**：已完成 1 对（见下），其余对经逐站点核查后**判定不可做或收益不足**，
      结论已写入 Remaining Work。剩余可做的只有 P4c（跨模块 JOIN，需 owner 发布 SQL 片段 = 过度设计）
- [x] **P4b 第一对：`identity.PrincipalStateStore`**（owner=identity，根包 `principalstate.go`）
      - 形态：函数结构体 `{Lock, LockActive, Status}`，每个字段收 `ctx, *gorm.DB, guid.ID`，**用调用方的事务与快照**
        （与 `core.RoleGrantStore` 同形）；由 `module.Provide[T]` 提供，消费方 `InjectServices` 里 `module.Resolve[T]`
      - 契约经 `NewService(...)` 参数传入（与 `governance.NewService(..., grants core.RoleGrantStore, ...)` 一致），
        不用"构造后再赋值字段"——后者在测试里会退化成 nil 函数指针 panic
      - 消费方 2 个、替换站点 3 处：oauth `tenant_exit.go:lockGrantAdmission`（lock + status 两条）、
        federation `repository.go:verifyPrincipalAndMember`（principal status 一条）
      - 新增门禁要求：两个消费方 `ModuleDependencies()` 的 return 表达式里声明 `identity.Name`（oauth 原缺）
      - 全覆盖：`internal/iam/identity/principalstate_test.go` 5 个测试（前缀绑定 / 找不到 / 表不可读 /
        两种锁不改行 / 调用方事务可见性 / 未接线返回 ErrUnavailable）
- [ ] P4b/c 剩余跨模块单表 / 跨模块 JOIN 拼表名清理（逐站点结论见 Remaining Work）
- [x] **P5 表名集合注入连接器（6 对，用户指定，未扩范围）**：形态完全沿用仓内既有先例
      `core.DirectoryAuthorizationSourceConnector`——消费方在**根包 `contracts.go`** 声明
      `type XTableSetConnector struct{ Bind func(gormx.TableSet) error }` 并在 `ProvideServices` 提供；
      拥有者在 `InjectServices` 里 `module.Resolve[...]` + **显式判 `Bind == nil` 即报错**，
      再 `Bind(owner.TableSet(runtime.TablePrefix()))`。
      - `core ← organization`：`core.DirectoryTableSetConnector` → `m.joined.bindDirectoryTables`，
        12 个目录表名（`BindDirectoryTables` 逐个 `Lookup`，缺一个即错）
      - `core ← identity`：`core.PrincipalTableSetConnector` → `m.joined.bindPrincipalTable`，`iam_principals`
      - `authn ← organization`：`authn.DirectoryTableSetConnector` → `m.foreign.bindDirectory`，`iam_tenants`
      - `authn ← oauth`：`authn.OAuthTableSetConnector` → `m.foreign.bindOAuth`，
        `iam_oauth_clients`（`repository_identity.go:230`）+ `iam_refresh_tokens`（`repository_session.go:122`）
      - `identity ← oauth`：`identity.OAuthTableSetConnector` → `m.foreign.bindOAuth`，`iam_refresh_tokens`
        （`revokeSessionsAndRefreshTokens`）
      - `identity ← organization`：`identity.DirectoryTableSetConnector` → `m.foreign.bindDirectory`，
        `iam_tenants`（`service_account.go` 2 个调用点经 `Repository.tenantTable`）
      - 消费方侧：`publishedTableName` / `joinedTables` 在**未绑定或 owner 没发布该名**时显式返回
        模块自身的 `ErrUnavailable`，绝不退化成一个空表名；写路径（identity 的撤销、
        core 的 `PutMember`）在语句之前解析
      - **未新增任何 `ModuleDependencies` 声明**（`Wire` 是先全体 Provide→Seal→再全体 Inject，
        与注册序无关；新增声明反而会触发 `Activate` 全序校验启动失败）；owner 侧**既有**声明未动
      - `core/query/table.go` 的 `ForeignTables`/`BindForeign` 与 `TODO(P5)` 已删除，改为
        `BindDirectoryTables`/`BindPrincipalTable`
      - **明确不做**（用户指定，两向都会违反 `Activate` 全序）：
        `identity←authn`（`iam_sessions` 仍按自身前缀拼）、`oauth←organization`（`iam_tenants`）、
        `governance←organization`（`iam_tenants` + `review_grants.go` 3 处 groups/positions/
        member_departments 片段）、`federation←organization`（`iam_tenants` 3 处）——**表名保持原样，如实报告**
- [x] **P4c 跨模块表名全部改为「只用拥有者发布的名字」**（7 个消费方 / 4 个拥有者，共约 65 处）：
      拥有者在 `tables.go` 声明 `Published gormx.TableSet` 的能力类型并在 `ProvideServices` 里 `Provide`：
      `core.AccessTables` / `identity.PrincipalTables` / `authn.CredentialTables` / `organization.DirectoryTables`。
      消费方在 `InjectServices` 里 `Resolve` + 在 `ModuleDependencies()` 声明拥有者，
      模块在 `Initialize` 把唯一的 `foreignTables` 持有者交给 repository（指针，事务克隆共享同一份发布）。
      - oauth（9 处，4 个拥有者）`89de48b`；provisioning（8 处）/ governance（**37 处**）/ federation（10 处）`a4f8e2c`；
        organization←core（10）/ identity←core（9）/ authn←core（1）`001f6a0`
      - **governance 实际是 37 处不是 11 处**：`review_grants.go` 的裸 SQL 还用 `p +` 拼了 8 张 core 表
        与 3 张 organization 表；子代理按「全模块重扫」指令一并改了，并用 gorm logger 抓 13 条语句
        改前改后 `cmp` 字节相同
      - provisioning 新增导出方法 `Service.BindForeignTables`（`internal/app` 两个测试在接线外构造 service，
        跨包够不到包内 helper）。**不是新模式**：`core` 已有三个同名同形先例
      - SQL 文本逐字节不变（解析出的名字 == 原 `prefix+逻辑名`），参数顺序未动
- [ ] P6 门禁收紧（目标 **385**，不是 0 —— 差额是 authn↔identity 的 8 处，见下）
- [ ] PD 全仓文档体检（六批）

## Important Decisions
- **D36**（已写入 `.claude/decisions.md`）：查询门禁的形状、5 条规则、基线只减不增、变异验证结论。
- **D37**（已写入）：跨模块取表名走窄接口注入；`pkg/module/registry.go:126` 的 `Activate` 要求依赖全序，
  **core 反向 import 下游 query 在启动期就是硬失败**；授权引擎语句保持单条。
- 用户先后给出两个互相冲突的答复：对 `administrator.go:70` 先答"仍然拆分"，后选路线含"不拆授权引擎"。
  **方案按"保持单语句"执行**（D3 已标注该不一致）。

## Confirmed Constraints
- 不放宽 `cross-module-storage-import` 门禁，不放宽 `Registry.Activate`，不改 `tests/architecture` 的既有规则。
- 生成物（`*.gen.go`、`openapi.json`、`api-schema.ts`）必须经生成器产出；`make generate` 后工作树零差异。
- 重命名/搬家 SQL 时**文本与参数顺序必须逐字节不变**，由性能基线 28 个指纹证明。
- `Delete(map[string]any{})` 不得换成 `Delete(&model{})`（会让带 `DeletedAt` 的模型变软删）。
- `authn/repository_credentials.go` 的 CASE UPDATE 依赖 MySQL 从左到右求值，只许搬文件不许改写。

## Known Issues / Risks
- **覆盖率门禁余量极薄（既有问题，待用户定处置）**：精确 **90.0464%**（29728 条语句，未覆盖 2959），
  距门槛 90.0% 仅 **0.0464 pp ≈ 14 条语句**。任意一处小改动没带测试就会把门禁压红。
  本次曾因此出现 `make ci` EXIT=2（**无任何测试失败**，仅覆盖率 89.9371%）：P1 新增代码占 31 条未覆盖。
  已按"不降门槛、补真测试"修复，P1 新增代码现 **100% 覆盖**。处置选项：接受现状 / 专门排一次覆盖率补齐 /
  记为已知风险。
- **Go 覆盖率机制（踩过的坑）**：`-coverprofile` **只记录被测包自身**的语句，不用 `-coverpkg`。
  在别的包（如 `internal/app`）的测试里调用某函数，**不会**给它所在包贡献覆盖率。
  因此每个包的表名契约必须在**该包内**测试。
- `make generate-check` 在本机跑不起来（GnuWin32 make 3.81 的 `$(MAKE)` 递归 + shell 语法），
  改用手工等价验证：`make generate` 后 `git status` 无任何 `.gen.go`。
- 一次 `make test-integration` 出现 `tests/lifecycle` 的 `mysql/prefixed` FAIL，**是在子代理并发改文件时跑的**，
  隔离复跑即通过。判断为并发假失败，但**未定论** —— 干净重跑结果为准。
- `make test-performance` 单独跑不校验指纹（缺 DSN 时 `TestIAMPerformanceExternalDatabase` SKIP）；
  真正带 DSN 比对的是 `make test-mysql8-run`。

## Verification
- **P4a**（干净树、无并发编辑）：
  - `go build ./...` EXIT=0；`go test ./internal/... -count=1` **51 包 ok / 0 FAIL**；`gofmt -l internal pkg` 空
  - `make ci` **EXIT=0**：fmt clean、lint 0 findings、**总覆盖率 90.1%**（≥90.0%）、
    integration architecture 5.96s / goose 20.3s / testdb 0.3s / lifecycle 138.0s /
    migration 349.4s / performance 49.3s **全 ok、0 FAIL**、build、`openapi: matches docs/openapi.json`
  - `make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4）：`TestIAMPerformanceExternalDatabase` PASS
    ——查询计划指纹与基线一致，即**表名字符串被改错会立刻在此暴露**，未暴露；
    schema lock `219 published migration files unchanged`、72 IAM 表、33 跨模块 FK
  - 账本：budget **736→581**、条目 **287→237**、逐规则 naked 585→445 / concatenated 136→121 /
    `handwritten-sql` **15 处 7 key，改前改后逐 key 完全相同**；对最终代码再跑一次
    `QUERY_GATE_UPDATE=1` 输出 `237 entries, 581 violations, budget 581` 且文件 md5 **未变**（基线精确当前）
  - `git status --porcelain`：22 个受控改动（21 源文件 + 账本），无 `.gen.go` / `cover*.raw` / `.log` /
    `bin/`，无未跟踪文件
- **P0**：`tests/architecture` PASS（含夹具与白名单断言）；三次执行路径变异均被抓住；UPDATE 无法抬高预算
- **P1**（干净树、无并发编辑）：
  - `make ci` **EXIT=0**：fmt clean、lint 0 findings、覆盖率全模块 ≥90%、
    integration architecture 5.6s / goose 24.0s / testdb 0.3s / **lifecycle 131.2s** /
    **migration 310.9s** / performance 51.2s **全 ok、0 FAIL**、build、openapi-check
  - `make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4）：含
    `TestIAMPerformanceExternalDatabase` 的**查询计划指纹"当前代码 vs 基线"比对通过**——
    这是"表名收敛逐字节未改 SQL"的决定性证据；schema lock 复核 219 文件未变、72 表、33 跨模块 FK
  - `make lint` EXIT=0、`make generate` 生成物零漂移
  - 先前那次 `mysql/prefixed` FAIL 已由干净运行**确认为并发编辑导致的假失败**
- **P3**（干净树、无并发编辑）：
  - `go build ./...` EXIT=0；`go test ./internal/... -count=1` **51 包 ok / 0 FAIL**
  - `make ci` **EXIT=0**：fmt clean、lint clean（修掉 3 处英式拼写误报）、
    **总覆盖率 90.1%**（≥90.0%）、各模块覆盖率全过、integration 全 ok、build、openapi-check
    `openapi: matches docs/openapi.json`
  - `make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4）：`TestIAMPerformanceExternalDatabase` PASS，
    4 条路径的计划指纹**与基线一致**；schema lock 219 文件未变、72 表、33 跨模块 FK
  - `gofmt -l internal pkg` 空；工作树无 `.gen.go` / `cover*.raw` 类临时产物
  - **变异验证**（三条新测试都真会响）：`return slices.Compact(ids), nil` → `return ids, nil`
    触发 1001/1002/1002 重复；删掉 `sort.SliceStable` → 排序断言失败；
    `memberCount+grantCount` 短路化 → "第二源不可读" 子测试失败
  - **性能基线零变化是实测**：`git diff --quiet internal/app/testdata/iam_performance_baseline.json`
    为真（`BASELINE_IDENTICAL_TO_HEAD`）。原因是 3 条语句**都不在**被采样的 4 条路径
    （authorization / session_lookup / scim_bulk / audit_append）内——实测基线里唯一的 UNION 条目是
    P2 的 authorization CTE。因此 **不需要** `IAM_PERFORMANCE_UPDATE=plans`，
    也没有新旧 EXPLAIN 可 diff，自然不存在新增全扫。

- **P5**（干净树、无并发编辑）：
  - `go build ./...` **EXIT=0**；`go test ./internal/... -count=1` 无 FAIL；`gofmt -l internal pkg` **空**
  - `make ci` **EXIT=0**：fmt clean、lint 0 findings、**总覆盖率 90.1%**（core 90.0 / authn 90.5 /
    identity 90.4 / oauth 90.1 / organization 90.3）、`go -C tests test ./...` 6 个包全 ok
    （architecture 3.3s / goose 16.7s / testdb 0.3s / lifecycle 131.7s / migration 327.4s /
    performance 42.0s）、build、`openapi: matches docs/openapi.json`
  - `make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4）：`schema lock: 219 published migration files
    unchanged`、`static reconciliation: 219 files, 72 IAM tables, 33 cross-module foreign keys`
  - 账本：**HEAD(85ebe5f) budget 572 → 工作树 482**（条目 236→211；`handwritten-sql` **15→15**，
    7 个 key 完全相同；concatenated 121→120、naked 436→347）。
    **注意口径**：工作树含未提交的 P4b，572→482 是 P4b 与 P5 的**合计**降幅，无法拆开归因；
    会话中曾观测到中间值 489（P5 中途刷新过一次），但那是 P5 自身改动之后就有的数。
    另跑一次 `QUERY_GATE_UPDATE=1` 后文件语义**逐字节不变**（budget 482 / 211 条目 /
    逐规则 120/15/347 / handwritten-sql 7 个 key 全同）→ 冻结预算精确等于当前违规数，未留余量
  - 「未接线即失败」实测（全部 PASS，见下）：
    - core `TestModuleReadersFailClosedUntilTheOwnersPublish`：7 个 reader + 写路径 `PutMember`
      均 `ErrUnavailable`，且 `sl_iam_tenant_members` **0 行**（拒绝的写没落地）；绑定后同一批调用正常
    - core `TestJoinedTablesReportUnboundAsUnavailable` / `TestBindForeignTablesGuardsAnAbsentReaderAndAMissingHolder`
    - authn `TestAuthnReadersFailClosedUntilTheOwnersPublish`：module 级，3 个 reader 报错、
      `sl_iam_refresh_tokens` 行未被动；绑定后同一调用到达表并给出自己的答案
    - identity `TestForeignTablesReportUnboundAsUnavailable`、`TestRepositoryReportsForeignTablesAsUnbound`、
      `TestRevokeReportsUnavailabilityWithoutTouchingStorage`（写路径：oauth 拥有的那一半被扣下，
      identity 自己拥有的 `iam_sessions` 那半仍执行，调用方拿到 error 而不是静默的半结果）
    - owner 侧「`Bind == nil` 即启动失败」：organization `TestOrganizationModuleReportsMissingWiring`
      4 个子测试（core/authn/identity 三个连接器 + 既有 directory authorization source）、
      oauth `TestOAuthModuleReportsMissingWiring` 2 个子测试，全部 `ErrorContains(..., "bind is required")`
  - `git status --porcelain` 66 项（60 个 ` M` + 6 个未跟踪新文件），**无 `.gen.go` / `cover*.raw` /
    `.log` / `bin/`**；`coverage.out`/`coverage.raw.out` 在磁盘上但被 `.gitignore` 覆盖
  - 中途真实故障与修法（不是推测）：① 加了 owner 侧 Resolve 后，organization/oauth/app 的测试假依赖
    没提供连接器 → `module: service is not registered`，补 no-op `Bind` 的假连接器解决；
    ② `QUERY_GATE_UPDATE=1` 报 `490 violations exceed the frozen budget of 489` —— **没有**用
    `QUERY_GATE_REBASELINE`，改为把 identity 撤销里的嵌套匿名结构体切片拆成
    `[]string` + `revocationTable(logical)`（门禁的 `nakedTableLiterals` 对嵌套复合字面量会重复计数），
    解决后无新增违规；③ 删除 core 死代码 `(*Repository).table` 触发 lint unused + 覆盖率掉到 89.9%，
    删代码的同时补 `TestBindForeignTablesGuardsAnAbsentReaderAndAMissingHolder` 与
    `TestModulePublishesThePlatformAccessGuard` 把 core 拉回 90.0%

## Remaining Work（P4b 的逐站点结论，实测过，不是推测）
1. **`authn → identity` 不可做（硬约束）**：`composeIAMRuntimeModules` 的注册序是
   audit→core→authn→**identity**→organization→oauth→provisioning→governance→federation，
   `pkg/module/registry.go:128` 要求 `dependencyIndex < index`，**authn 声明 identity 会在 `Activate` 直接启动失败**。
   authn 现在能 `Resolve[identity.RegistrationPrincipalCreator]` 只因为门禁的 `isStableWiringCapability`
   白名单（禁止改 `tests/architecture`）。→ 只能等 P5 的表名集合注入。
2. **`provisioning → identity`／`provisioning → organization` 不可做**：剩下的站点全是多表 JOIN
   （`repository.go:534` principals JOIN、`:585` groups JOIN、`:595` group_members 四表 JOIN、
   `filter.go:227` EXISTS 子查询片段）。要转换就得让 owner 发布 SQL 片段 → 语句结构与文本都变（违反铁律 4），
   且属"为一个用例造框架"（违反铁律 5）。
3. **`oauth → identity` 剩余 1 处**（`repository.go:606` service-account JOIN）同上，属 P4c。
4. 其余建议契约（`core.RoleReader`、`organization.DirectoryReader`、`authn.CredentialReader`、
   `audit.EventReader`）在允许方向的剩余站点均只 1 处且嵌在 JOIN 内，收益不足 —— **如实跳过**。

## Next Action
1. **推送** `3cb34f1..001f6a0`（4 个提交）到 `origin/IAM`。
2. **P6 门禁收紧**：把 budget 从 385 继续压。**目标不是 0**——authn↔identity 的 8 处无解（见下），
   且 `naked-table-literal` 里还剩大量「逻辑名作为 `accessTable("iam_x")` 的查表键」，
   那不是缺陷（名字不是自己拼的）。收紧前先决定这一类要不要从规则里区分出去，
   否则 P6 会变成为了数字而改代码。
3. PD 全仓文档体检（六批，未开工）。

### P4c 的机制（已实测确证，不要再推演）
拥有者在注册序里排在消费者**之前**时不需要连接器：拥有者 `Provide` 自己的表名集合，
消费者 `Resolve` 并在 `ModuleDependencies()` 声明拥有者即可。
连接器只在**拥有者排在消费者之后**（无法被声明为依赖）时才必需，P5 的六对正是那种情形。
注册序：`audit(0) core(1) authn(2) identity(3) organization(4) oauth(5) provisioning(6) governance(7) federation(8)`；
`Registry.Activate` 要求 `dependencyIndex < index`，架构门禁 `module-dependency` 要求
「Resolve 了谁的类型就必须声明谁」——合起来即**拥有者 index 必须小于消费者 index**。

### 唯一受阻：authn ↔ identity，共 8 处，如实保留在基线里
authn 已 import identity（`identity.RegistrationPrincipalCreator`，靠架构门禁
`isStableWiringCapability`（`tests/architecture/architecture_test.go:554`）白名单放行）。于是：
- identity 读 authn 的表 → identity 要 import authn → **Go 包环**；
- authn 读 identity 的表 → authn 要声明 `identity.Name`，而 identity(3) ≥ authn(2) → `Activate` 启动失败；
- 连接器方向同样撞上这两条之一；白名单只列了那一个类型，且**不许改 tests/architecture**。
站点：identity 读 authn 的 `iam_credentials`×2、`iam_sessions`、`iam_email_verification_tokens`、
`iam_password_recovery_tokens`（5）；authn 读 identity 的 `iam_principals`×2、`iam_service_accounts`（3）。
**唯一的解法是把 `RegistrationPrincipalCreator` 这条 authn→identity 的边移走**，属独立议题，需用户决定。

### 已知的同类缺陷，未修（D 类，超出本次范围，需用户决定）
`core.NewRevisionStore(runtime.TablePrefix())`：governance / identity / organization 把**自己的前缀**
交给 core，由 core 在 `core/revision.go` 内部拼到 core 拥有的 `iam_policy_revisions` 上。
同一缺陷类，但拼接发生在 core 的构造函数里，改动会波及 4 个模块共用的 core API。

### 用户约束（未变）
- **不要把带 SQL 的东西放进 `pkg/`。** 若某对必须走中立位置，优先考虑业务模块下的独立子目录
  （前提：两侧 import 它不构成环），而不是 `pkg/`。

### 教训（写给下一个上下文）
- 这一层的正确性只能由 `Registry.Activate` 的启动期检查（`tests/lifecycle`）确证，不要用推理代替。
  本会话此前在这个边界上推演错过三次。
- **站点数要用脚本普查，不要目测**：governance 目测 11 处，实际 37 处。
- 并发子代理共用一个工作树时**不要 `git stash`**（本会话有代理这么做了，连带暂存了同伴的在途改动，
  pop 成功但属高风险操作）。

## 事后修正（2026-09-11，用户质疑后核实，优先级高于上面的记述）

### 1. P4c 的理由我写错了，已改提交说明
`table_prefix` 是**全局唯一一个配置项**（`internal/app/config.go:34`，装在 runtime 上，整个程序一份）。
因此消费方自己拼出的 `prefix+逻辑名` 与拥有者拼出的**永远是同一个字符串**。
我在三条提交说明里写的「某个部署里拥有者前缀不同就会查到不存在的表」**场景不可能发生**。
提交（未推送）已重写为真实理由：**表名没有主人**——拥有者改名时，消费方照样编译通过、只在运行时炸；
而且跨模块读表这件事以前在依赖图上看不见。

### 2. 真正优雅的做法：让拥有者干活，而不是把表名递出去
仓内**已经有这个样板且在用**：
- `internal/app/principallifecycle.go`：app 作为编排者，依次调用每个拥有者**自己**的
  `XXX.ErasePrincipalData(ctx, tx, prefix, id, now)`，各自清自己的表。没有任何模块读别人的表，
  也就没有任何循环依赖。
- `authn/module.go:69` 已经 `Provide[identity.PrincipalCredentialsRevoker]` ——委派通道早就存在。

**设计规则（建议写进 SKILL.md）**：
- 跨模块的**写**、或**单表读** → **委派给拥有者**（表名不过界，不产生依赖边）；
- 跨模块的**单条语句 JOIN** → 名字必须过界，才用「拥有者发布表名」。
按这条规则，P4c 已做的 65 处里属于 JOIN 的那部分是对的；而下面第 3 条那些是**该委派却没委派**的。

### 3. 「authn↔identity 受阻 8 处」这个判断是错的，而且规模远大于 8
受阻只是针对「发布表名」这一种机制。按委派走不产生任何 import 边，**不受阻**。
而且真实规模被我的普查低估了（普查只认字面量，没认常量）：
- **identity 写 authn 的表**：`createCredential`（建凭据）、`incrementCredentialVersion`（改版本号）、
  `revokeAllSecurityState`（吊销 `iam_sessions` + 消费 `iam_password_recovery_tokens` /
  `iam_email_verification_tokens`）。authn 已经提供了 `PrincipalCredentialsRevoker`，
  这些本就该委派。
- **authn 操作 identity 的表**：`internal/iam/authn/repository_identity.go` 用
  `principalTableName`/`serviceAccountTableName` 两个常量，**约 15 条语句**——建 principal、改 principal、
  读 principal、JOIN service_accounts。不是 3 处。
→ 这是**双向的数据所有权破裂**，不是命名问题。修法是把两侧的跨界操作改成互相委派，
  规模是一块独立的工程，需要用户决定是否排期。**不要再试图用发布表名解决它。**

### 4. 一处注释是错的（未修，下次顺手改）
`internal/iam/identity/repository.go` 的 `revocationTable` 注释写「The sessions belong to identity,
so its own prefix names them」——**`iam_sessions` 是 authn 拥有的**
（`internal/iam/authn/model/credential.go:41`）。这条注释会把下一个读者引到错的所有权认知上。

### 5. 两个「本来可以更简单」的发现
- **gorm 的命名策略完全没用**（`pkg/extension/gorm` 里没有 `NamingStrategy`/`TablePrefix:`）。
  如果建连接时配一次 `schema.NamingStrategy{TablePrefix: ...}`，基于模型的查询就自动带前缀，
  业务代码里一个 `prefix +` 都不用写——P1~P6 这条路线的大半不会存在。现在改的代价是裸 SQL 与
  `Table()` 调用绕过命名策略，享受不到。
- **跨模块传表名应该用「带类型的字段」而不是「字符串查 map」**。现在是
  `accessTable("iam_tenant_members")`：魔法字符串还在、编译器帮不上忙、每处多一个永不触发的错误分支。
  改成拥有者导出带字段的结构体（`tables.TenantMembers`）则三者全解，且拥有者那边
  `query/table.go` 的 `TableNames` **已经是这个形状**。属返工（约 65 处），收益是代码变少。

## 收尾计划的执行进度（2026-09-12）

计划文件：`~/.claude/plans/flickering-toasting-neumann.md`（用户已批准）。

### 阶段一 authn↔identity 解耦 —— 完成，**全仓自拼他模块表名 65 → 0**
提交 `9e89c5e..501efc0`（未推送）：
- `9e89c5e` S0 冻结 9 条语句的 SQL 文本 + 补链路特征测试（零生产改动）
- `906df4e` S1 删 `EnsureBootstrapPrincipal` 等死码（净 −196 行）
- `7fbf266` S2 identity 推表名给 authn（`PrincipalTableSetReceiver`，方向反转）
- `7d41726` S3 扩 `PrincipalStateStore` 六个 reader（纯增量，SQL 等价实测）
- `c7555db` S4 authn 七条语句换方法体（签名/错误/调用点不变）
- `d374ef8` S5 authn 提供 `PrincipalCredentialStore`（四个闭包钉在调用方事务）
- `61f2b35` S6 identity 五条越界写换成能力
- `501efc0` S7/S8 保留 sessions 双写 + 计数不变量、修 authn 持有者注释、写 D39

**决定性验证**：`make ci` **EXIT=0**（总覆盖率 90.1%、六个集成套件全 ok、openapi 指纹一致）；
`make test-mysql8-run` **EXIT=0**，`TestIAMPerformanceExternalDatabase` **PASS**、
`iam_performance_baseline.json` `git diff --quiet` 为真、schema lock 219 文件未变、72 表、33 跨模块 FK。
门禁账本 **385 → 372**（四次手动下调，全程未用 `QUERY_GATE_REBASELINE`）。

详细决策见 `.claude/decisions.md` 的 **D38**（死码删除）与 **D39**（双向委派、方向反转、
`LockActiveRow` 的安全差异、sessions 双写为何保留、identity 测试为何只能用替身）。

### 阶段二 带类型字段 —— 进行中，**方案已由用户改为 C（全仓统一、0 遗憾）**

**用户的要求（2026-09-12）**：必须强类型约束写法，全仓统一，0 遗憾。
并明确指出一处我解释失败：**本次不改任何表名**，「改名就编译不过」只是在比较两种写法的安全性。

**实测规模**：生产调用点 **71 处**（不是计划估的 ~65），测试调用点 52 处。

**被推翻的原计划**：原本要「拥有者导出类型化结构体、消费方引用它」。实测有 4 对做不到——
拥有者激活在消费方之后时，消费方 import 拥有者会成包环（已验证 `organization` 确实 import 了 `core`，
3 个文件）。那会导致 66/71 强类型、5 处留字符串，两种形态并存。

**最终方案 C（一条规则，由 Go 的 import 方向决定，不可选）**：
> 跨界的类型化结构体，声明在**被对方 import 的那一侧**。

- **消费方 import 拥有者** → 拥有者导出自己的类型化结构体，消费方直接持有它。
  适用：authn←core、identity←core、organization←core、oauth←{core,identity,authn,organization}、
  provisioning←{core,identity,organization}、governance←{core,organization}、
  federation←{core,organization}、authn←identity（S2 的 receiver 形状）。
- **拥有者 import 消费方** → 消费方声明它需要的字段，拥有者负责填满（连接器的现有方向，
  只是载荷从 `gormx.TableSet` 换成消费方声明的类型化结构体）。
  适用：core←organization、core←identity、authn←organization、authn←oauth、
  identity←organization、identity←oauth。
  **这一侧更强**：消费方加字段 → 拥有者的填充处编译不过 → 被迫提供。

两种情形下消费方手里都是类型化结构体，**71 处调用点全是字段访问**。

**交付标准（0 遗憾）**：全仓无一处字符串查表、无不具名集合；`gormx.TableSet` 若无人使用即删除；
每处 SQL 文本不变（由既有的冻结断言 + MySQL 查询计划指纹证明）。

**仓内已有的先例**：`internal/iam/core/joined_tables.go` 已经在绑定时把集合转成
`query.DirectoryTables`，调用点用字段——阶段二是把这件事做成全仓唯一形态，并去掉中间那个不具名集合。

### 阶段二 原定描述（已作废，保留以便追溯）
拥有者在根包导出字段为 `gormx.TableName` 的结构体（镜像 `query.TableNames`，带漂移测试）；
把「是否已绑定」校验集中到 `Initialize`（任一字段为零值即启动失败），业务代码错误分支从约 65 降到每消费方 1 个；
P4c 写的约 30 个运行时用例会收敛成每模块 1 个。**顺序在阶段三之前**——计划里纠正过用户最初的选择：
返工之后那 142 个「查表键」从代码里消失，门禁就不需要加「查表键不算违规」这条特例。

### 阶段三 门禁规则 / 阶段四 文档体检 —— 待开始
阶段三五项修正（排除 Prometheus 指标名误报、只认前缀加法、按 file:line:col 去重、补常量洞、
补 `sql-in-format-string` 的局部变量洞）与阶段四 36 份文档清单见计划文件。

## Last Checkpoint
2026-09-11 **P4c 全部完成，全量门禁绿**（4 个提交 `3cb34f1..001f6a0`，未推送）：
- `make ci` **EXIT=0**：fmt clean、lint 0 findings、**总覆盖率 90.1%**（全模块 ≥90.0：organization 90.0 /
  identity 90.2 / authn 90.5 / oauth 90.2 / provisioning 90.4 / governance 93.6 / federation 91.1）、
  architecture 3.6s / goose 15.4s / testdb 0.3s / lifecycle 112.2s / migration 265.3s / performance 41.0s
  **全 ok、0 FAIL**、build、`openapi: matches docs/openapi.json`
- `make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4）：`TestIAMPerformanceExternalDatabase` **PASS**
  ——查询计划指纹与基线一致（表名被改错会立刻在此暴露）；`iam_performance_baseline.json`
  `git diff --quiet` 为真；`schema lock: 219 published migration files unchanged`；
  `static reconciliation: 219 files, 72 IAM tables, 33 cross-module foreign keys`
- 门禁账本 **482 → 473 → 412 → 385**，三次均**手动下调**，全程未用 `QUERY_GATE_REBASELINE`
- 脚本普查复核：全仓「消费方用自己前缀拼他模块表名」**从约 65 处降到 8 处**，
  剩下的 8 处全部落在受阻的 authn↔identity 一对内
- `git status` 仅受控改动，无 `.gen.go` / `cover*.raw` / `.log` / `bin/` / 临时产物

2026-09-11 **P4c 第一段（oauth）完成并提交 `89de48b`**：新增四个拥有者能力
（`core.AccessTables` / `identity.PrincipalTables` / `authn.CredentialTables` / `organization.DirectoryTables`），
oauth 的 9 处他模块表名改为解析而来，`ModuleDependencies` 增 `core`+`organization`。
实测：`go build ./...` EXIT=0；`go test ./internal/... -count=1` 0 FAIL；
`go test ./tests/architecture/ -count=1` **ok**（门禁账本 **482→473**，手动下调，未用 REBASELINE）；
`go test ./tests/lifecycle/ -count=1` **ok 99.194s**（= `Registry.Activate` 接受新依赖声明的决定性证据）；
`make test-coverage` 总覆盖率 **90.1%**、oauth **90.2%**；`gofmt -l internal pkg tests` 空；
`make lint` 0 findings；`git status` 仅受控改动、无临时产物。
**尚未跑**：`make ci` 全量与 `make test-mysql8-run`（留到 P4c 全部改完一次性跑）。

2026-09-11 P0–P5 已推送（`0da7f6e..9295570`，9 个提交）；P4c 受阻范围修正为约 4 处。
2026-09-11 **P5 六对连接器全部完成并实测**：`go build ./...` EXIT=0、`go test ./internal/... -count=1`
无 FAIL、`make ci` **EXIT=0**（总覆盖率 **90.1%**，全模块 ≥90.0%）、`make test-mysql8-run` **EXIT=0**
（schema lock 219 文件未变、72 IAM 表、33 跨模块 FK）、`gofmt -l internal pkg` 空、
账本 **HEAD 572 → 482**（`handwritten-sql` **15 不变**，7 个 key 逐字相同）、
`git status --porcelain` 66 项且无 `.gen.go`/`cover*.raw`/`.log`/`bin/`；
「未接线即失败」在 core/authn/identity 三处消费方 + organization/oauth 两处 owner 侧全部实测 PASS。
**未做**的 4 对（`identity←authn`、`oauth←organization`、`governance←organization`、
`federation←organization`）表名保持原样，已逐站点列出。

2026-09-11 **P4b 第一对（`identity.PrincipalStateStore`）完成并实测**：`go build ./...` EXIT=0、
`go test ./internal/... -count=1` **51 包 ok / 0 FAIL**、`make ci` **EXIT=0**（总覆盖率 **90.1%**，
oauth 90.2 / federation 91.0 / identity 90.5）、`make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4，
`TestIAMPerformanceExternalDatabase` 指纹 PASS、schema lock 219 文件未变）、`gofmt -l internal pkg` 空、
账本 budget **581→572**（`handwritten-sql` 仍 7 key/15 处）、`git status` 仅本次受控改动、无临时产物。
逐语句 SQL 实测（gorm DryRun + callback 钩子）：oauth LOCK/STATUS 与 HEAD **逐字符相同**；
federation PRINCIPAL 由 `Scan` 改为 `Take`，**多一个 `LIMIT 1`**（主键等值查找，语义等价，已如实报告）。

2026-09-11 P4a 完成并实测：`go build ./...` 0、`go test ./internal/... -count=1` 51 ok/0 FAIL、
`make ci` **EXIT=0**（总覆盖率 **90.1%**）、`make test-mysql8-run` **EXIT=0**（`TestIAMPerformanceExternalDatabase`
指纹比对 PASS、schema lock 219 文件未变）、`gofmt -l internal pkg` 空、账本 budget **736→581**
（条目 287→237；`handwritten-sql` 7 key/15 处**逐 key 相同**）；`git status` 仅 22 个受控改动、无临时产物
