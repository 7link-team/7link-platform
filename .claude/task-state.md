# Task State

## Task
消除手写 SQL、表名类型化、新增静态门禁、全仓文档体检。方案见 `C:\Users\Feather\.claude\plans\giggly-bubbling-clock.md`（已批准）。

## Objective
按批准的方案分阶段推进：P0 门禁 → P1 类型化原语 → P2 core 授权引擎搬迁 → P3 可合并 UNION →
P4a/b/c 拼表名清理 → P5 注入连接器 → P6 门禁收紧；PD 文档体检独立并行。
用户确认的路线：**跨模块取表名一律走窄接口/注入**，不放宽架构测试、不放宽依赖全序（D37）。

## Current Phase
ACCEPTANCE —— **P5「拥有者把表名集合注入给消费者」已完成并全量验证通过（6/6 对，另有 4 对结构性不可做）**，
P4b 第一个契约（identity.PrincipalStateStore）在其下方未提交；**尚未提交**

## Current Step
P5 全部 6 对连接器完成，8 项要求的实测已跑完（见 Verification 的 P5 段）。
下一步：提交（P4b + P5 同一工作树），然后才动 P6

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
- [ ] P6 门禁收紧（budget 降到 SQL 类 = 0）
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
提交本次工作树（未提交的 P4b + P5 一起）。之后：
- 若要继续 P4c，先重新核对 `Remaining Work` 第 1 条的硬约束是否已被 P5 解除；
- P6 门禁收紧时才允许动 `budget`/规则。
每阶段改完都要 `QUERY_GATE_UPDATE=1 … -run TestQueryGate` 刷新后**手工下调** `budget`（当前 482）。
若刷新时报 "exceed the frozen budget"，说明引入了新违规，去修代码而不是用 `QUERY_GATE_REBASELINE` 掩盖。

## Last Checkpoint
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
