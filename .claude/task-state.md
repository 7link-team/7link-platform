# Task State

## Task
消除手写 SQL、表名类型化、新增静态门禁、全仓文档体检。方案见 `C:\Users\Feather\.claude\plans\giggly-bubbling-clock.md`（已批准）。

## Objective
按批准的方案分阶段推进：P0 门禁 → P1 类型化原语 → P2 core 授权引擎搬迁 → P3 可合并 UNION →
P4a/b/c 拼表名清理 → P5 注入连接器 → P6 门禁收紧；PD 文档体检独立并行。
用户确认的路线：**跨模块取表名一律走窄接口/注入**，不放宽架构测试、不放宽依赖全序（D37）。

## Current Phase
IMPLEMENTATION —— P0/P1/P2/P3 已完成并推送；P4a 未开始

## Current Step
P3 已提交推送（`f3c23c9`）。下一步：P4a 自有表拼表名清理（34 处）

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
- [ ] P4a/b/c 拼表名清理（自有表 34 处 → 跨模块单表 → 跨模块 JOIN 72 条）
- [ ] P5 表名集合注入连接器（6–9 个）
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

## Next Action
进入 **P4a（自有表拼表名清理，34 处）**：一律改用同模块 `query/` 的 `TableName()`/`Bind()`，
不得跨模块 import query；改完跑 `QUERY_GATE_UPDATE=1 go -C tests test ./architecture -run TestQueryGate`
刷新条目后**手工下调** `budget`（当前 736），再跑 `make ci` 与 `make test-mysql8-run`。
若刷新时报 "exceed the frozen budget"，说明引入了新违规，去修代码而不是用 `QUERY_GATE_REBASELINE` 掩盖。

## Last Checkpoint
2026-09-10 P3 三条语句改写完成并实证（`make ci` EXIT=0、覆盖率 90.0665%、账本 budget 736、
性能基线逐字节未变）；P4a 未开始
