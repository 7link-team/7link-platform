# Task State

## Task
IAM 域全量迁移：chaosplus `apps/server` → 7link-platform `apps/backend-api`

## Objective
按 `apps/backend-api/docs/superpowers/plans/2026-09-02-iam-domain-migration.md` 将 9 个 IAM 域模块与
63 张表全功能迁入 gorm/gorm-gen/goosex/pkg-module 架构，修复源 SQL 缺陷，并通过
PostgreSQL、MySQL、SQLite 真实迁移与 `make ci` 门禁。

## Current Phase
ACCEPTANCE

## Current Step
Phase 9 / Task 9.7 已完成：Mac 实机预检、完整 CI、真实 MySQL 8.4、生成零 diff 与 live health 均通过。

## Overall Progress
- [x] 深度分析、方案 v9 与两轮独立评审收口
- [x] Phase 0 前置修复、扩展包（policyx/authn/authz/webauthnx/secretx/passwordx）、13 项跨模块能力冻结
- [x] Phase 1 九模块自治迁移源（63 表）、里程碑 DAG、三引擎与真实 MySQL 8.4 门禁、`schema.lock` 冻结
- [x] Phase 2 core / Phase 3 authn+identity / Phase 4 oauth / Phase 5 organization / Phase 6 audit
- [x] Phase 7 governance / provisioning / federation
- [x] Phase 8 应用装配、OpenAPI、跨模块负例矩阵、架构门禁
- [x] Phase 9 集成与验收

## Active Work
迁移实现与最终验收已完成，无进行中的实现工作。执行模式 Direct（上层约束禁止未授权子代理）。

## Completed Work
按阶段结论记录；逐任务执行过程见 Git 历史与 `.claude/decisions.md`。

- **模块与表**：9 个自治模块各自拥有 model/query/dbgen、三方言 SQL、迁移版本表与跨模块 FK，无中央
  port/schema 包。表归属 core 13、authn 13、identity 3、oauth 4、audit 3、organization 14、governance 4、
  provisioning 5、federation 4 = 63 表；三方言集合相等、互斥，并集恰为 63。
- **HTTP**：208 条 operation 全部落地并与固定源树逐条对账（core 51、authn 30、identity 14、oauth 12、
  organization 32、audit 8、governance 14、provisioning 30、federation 17），OperationID 全局唯一，
  实际 Huma OpenAPI 与各模块冻结 manifest 相等。
- **装配**：`internal/app` 已接入 authz registrar、IAM 配置映射、冻结的 9 模块运行时顺序与 6 模块声明顺序；
  core 提供 `ActiveMemberChecker`/`AdministratorGuard`/`RoleGrantStore`，跨模块契约只经 provider 根包。
- **不变量冻结**：`schema.lock` 冻结 147 个 migration 文件 / 63 张 IAM 表 / 27 条跨模块 FK，
  禁止改写或插入历史版本。
- **工具链**：`make iam-preflight`（两机源路径与 Docker transport 解析、63 表核对、产出 `.temp/iam-env.mk`）、
  `make test-mysql8`（真实 MySQL 8.4，固定 image digest 与 `sql_mode`）、`make test-performance`（冻结
  EXPLAIN 与时延阈值）、`tests/architecture` 全称门禁、`tests/lifecycle` 与 `tests/migration` 三引擎双前缀矩阵。
- **Phase 9**：9.1 生命周期矩阵、9.2 migration-status 与 ownership 对账、9.3 架构门禁强化、
  9.6 真实数据库性能契约均已通过；9.7 Windows 与 Mac 最终门禁均已完成。
- **Mac 并发修复**：最终 CI 发现 SQLite deferred transaction 在 SCIM ETag 与 session rotation 并发下
  可能返回 `SQLITE_BUSY_SNAPSHOT`。SQLite DSN 现默认 `_txlock=immediate`，显式配置仍保留；两个原失败
  用例各连续 20 轮通过，相关 gorm/provisioning/authn 全包 race 通过。

## Remaining Work
无。

## Important Decisions
- 目标为全量、全功能迁移；SAML/SCIM 不允许长期 Disabled。
- `internal/iam/` 只作命名空间；core/authn/identity/oauth/organization/audit/governance/
  provisioning/federation 是 9 个自治模块，各自拥有 SQL、版本表和跨模块 FK（D10）。
- 多写者是现有正式契约；Task 0.0 只修同一次迁移的 writer 句柄漂移，不禁用多写者。
- 动态菜单多语言不属于本次迁移；保持 63 表，不新增第 64 张表（D7）。
- 不局部引入 Ports-and-Adapters，不设中央 port/schema，不按 feature 机械拆包（D10 取代 D3/D9）。
- 只支持已确认的原 Mac 与当前 Windows 两机拓扑；源 tree 变化必须先复审（D6）。
- 传输文件统一 `http.go`/`RegisterHTTP`，接口按 RESTful 标准设计；208 条 operation 通过逐模块
  source→target 映射保证完整性，不要求复制源 RPC 路径；源无 IAM gRPC，当前不虚构（D12）。
- Federation replay 使用 bounded fail-closed TTL registry，不新增表、不复用会按 nonce 无界增长的
  distributed-lock rows；跨 replica 一次性消费为明确部署限制。
- SQLite 使用驱动默认 5 秒 busy timeout，并在未显式配置时采用 `BEGIN IMMEDIATE`，避免读后升级写事务
  产生不可等待的 snapshot contention（D14）。

## Confirmed Constraints
- 三方言 DDL 全部前缀化、真实时间类型、无 0 哨兵、FK/guid 使用 BIGINT。
- 每个新增受控模块覆盖率 ≥90%，阶段结束 `make ci` 必须通过。
- 不迁移存量数据；不缩减现有 IAM 域功能。
- 实现按依赖顺序推进；阶段评审用隔离只读 subagent，并由主执行者亲自证伪。
- 每次换机或恢复任务先运行 `make iam-preflight`，后续只消费其生成的路径与 Docker transport。
- MySQL `iam_entities.parent_key`（generated column）是唯一显式方言例外，parity helper 中显式 allowlist。
- Go 禁止测试成环：`internal/iam/core` 包内测试不得 import identity/organization，core 测试直接用真实
  SQL/DDL 建表，完整 schema 由 `tests/migration` 验证。

## Known Issues / Risks
- 验证码依赖的传递模块 `github.com/golang/freetype` 已归档；当前无 `govulncheck` 可达漏洞，等价迁移阶段
  保留，迁移完成后单独评估验证码/字体栈替换。
- Federation replay registry 为进程内状态，跨 replica 需计划外的 shared nonce capability 或 schema 变更，
  当前不得静默增加。
- 多写者 routing mode 与读副本接线仍是独立架构决策，不在本任务范围内。

## Verification
Build: PASS（Mac 最终 `make ci` production build）
Tests: PASS（9 个 IAM root 包与 `internal/app` focused/`-race`；Mac 完整 tests module，含 lifecycle、migration、
architecture、performance 矩阵；最终代码版 migration 496.939s，fresh performance 97.230s）
Lint: PASS（`go vet ./...` 与 pinned golangci-lint，0 findings；`make fmt-check` PASS）
Coverage: PASS（总计 90.0%；audit 93.7、oauth 91.6、federation 91.2、provisioning 90.9、identity 90.6、
authn 90.3、governance 90.2、core 90.0、organization 90.0）
Integration: PASS（三引擎 × 双前缀 up/幂等/down/up、跨模块 FK DAG、append-only、索引生命周期；
真实 PostgreSQL 16.9 与 MySQL 8.4.11）
Contract: PASS（实际 OpenAPI 208/208 与冻结 manifest 相等；`schema.lock` 147 files / 63 tables /
27 cross-module FKs；`make generate` 工作树 fingerprint 零变化）
Real MySQL: PASS（Mac `make test-mysql8`：preflight、真实 8.4 迁移、四路径 performance、schema lock 全绿）
Runtime: PASS（Mac `make dev` 监听 8080/9090，`GET /healthz` HTTP 200 且 `SERVING`，interrupt 后
`shutdown complete`，两端口均释放）
Other: PASS（Mac `make iam-preflight` source commit/tree 与三方言 63 表通过；`make generate` 无生成 diff；
工作区与 `main...HEAD` 的 `git diff --check` 均通过）

## Next Action
IAM 迁移已完成验收。提交前区分本次 SQLite/验收收口与工作区中用户已有的 `CLAUDE.md`、
`docs/todo-module-design.md` 修改，避免盲目暂存。

## Last Checkpoint
2026-09-04 · MAC FINAL ACCEPTANCE / COMPLETE：`make iam-preflight` PASS（source commit
`8c034d3efa603c7fd327a658bd405252397caf24`、tree `201052baddd745d1c2727cf29bbc3c68253a1af0`、
Docker 29.7.2）。初次 CI 暴露 provisioning SQLite lock，第二次暴露同根因的 authn lock；将共享 SQLite
默认事务改为 immediate 后，定向 20 轮、全包 race 与第三次完整 `make ci` 均 PASS。随后当前代码的
`make generate`、真实 `make test-mysql8`、live `/healthz`、shutdown/端口释放与 diff/tidy 门禁全部通过；
空 `_txlock` 边界收紧后，最终代码版完整 `make ci` 再次 PASS（migration 496.939s）。
